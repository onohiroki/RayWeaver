package asphere

import (
	"math"

	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/pupil"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/raymath"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// FocusConfig holds the focus channel analysis parameters.
type FocusConfig struct {
	// NumSamples is the number of rays per T/S fan (default 17).
	NumSamples int
	// Tangential and Sagittal control which fans to trace.
	Tangential bool
	Sagittal   bool
}

// DefaultFocusConfig returns the default focus analysis configuration.
func DefaultFocusConfig() FocusConfig {
	return FocusConfig{
		NumSamples: 17,
		Tangential: true,
		Sagittal:   true,
	}
}

// FocusFanResult is the best-focus position for one fan of one field.
type FocusFanResult struct {
	BestZ        float64
	RMSLineWidth float64
	Samples      []types.AsphereFocusSample
}

// FieldFocusResult holds the T/S focus results for one (field, wavelength).
type FieldFocusResult struct {
	FieldID    int
	Angle      float64 // field angle in degrees
	Wavelength float64
	Tangential FocusFanResult
	Sagittal   FocusFanResult
	// ReferenceImageZ is the nominal image plane Z used as the reference.
	ReferenceImageZ float64
}

// FocusResult holds the complete focus channel output.
type FocusResult struct {
	// PerField contains the base system focus results (shared across candidates).
	PerField []FieldFocusResult
	// ReferenceImageZ is the nominal image plane Z.
	ReferenceImageZ float64
}

// launchFans generates 1D tangential or sagittal fan samples for one field.
// For a field with direction [dx, dy]:
//   - Tangential fan: pupil coordinates along the field azimuth (dx, dy)
//   - Sagittal fan: pupil coordinates perpendicular to field azimuth (-dy, dx)
//
// Each fan samples n points along the entrance pupil through the centre, and
// the pupil offset follows the same wavefront-plane projection as the main
// asphere grid so OPL is launch-tilt-free.
func launchFans(f Field, numSamples int, radius float64, pupilZ float64) (tangential []pupil.Sample, sagittal []pupil.Sample) {
	if numSamples < 3 {
		numSamples = 3
	}
	zStart := -100.0
	dir := rayDirection(f)
	// Field azimuth direction and its perpendicular.
	tx, ty := raymath.FieldAzimuth(f.Direction)
	sx, sy := -ty, tx // sagittal = rotate 90°

	pupilOffsetX, pupilOffsetY := pupil.GridCentre(dir, pupilZ, zStart)

	// Normalised fan coordinates: linspace(-1, 1, numSamples).
	for i := 0; i < numSamples; i++ {
		u := -1.0 + 2.0*float64(i)/float64(numSamples-1)
		px := pupilOffsetX + u*radius*tx
		py := pupilOffsetY + u*radius*ty
		origin := raymath.ProjectOntoWavefront(
			types.Vec3{X: px, Y: py, Z: zStart},
			types.Vec3{X: pupilOffsetX, Y: pupilOffsetY, Z: zStart},
			dir,
		)
		tangential = append(tangential, pupil.Sample{
			PupilX: u * radius * tx,
			PupilY: u * radius * ty,
			Area:   1.0,
			Origin: origin,
			Dir:    dir,
		})
	}
	for i := 0; i < numSamples; i++ {
		u := -1.0 + 2.0*float64(i)/float64(numSamples-1)
		px := pupilOffsetX + u*radius*sx
		py := pupilOffsetY + u*radius*sy
		origin := raymath.ProjectOntoWavefront(
			types.Vec3{X: px, Y: py, Z: zStart},
			types.Vec3{X: pupilOffsetX, Y: pupilOffsetY, Z: zStart},
			dir,
		)
		sagittal = append(sagittal, pupil.Sample{
			PupilX: u * radius * sx,
			PupilY: u * radius * sy,
			Area:   1.0,
			Origin: origin,
			Dir:    dir,
		})
	}
	return tangential, sagittal
}

// traceFanRays traces a fan through the system and returns the intersection
// positions and emergent directions at every surface.
func traceFanRays(samples []pupil.Sample, surfaces []types.Surface, wavelength float64, gc *glass.Catalog) []pupil.Sample {
	engine := ray.NewEngine(gc, nil)
	path := dls.BuildPath(surfaces)
	pupil.Trace(engine, path, surfaces, samples, wavelength, types.NewCircularJones(true), 1)
	return samples
}

// fanLineWidth computes the RMS line width of a fan at a given z-plane along
// the chief ray direction. The chief ray is the zero-pupil sample (index
// floor(n/2)). Each ray's deviation from the chief ray at that plane is
// measured along fanDir (the fan's orientation in the pupil plane, e.g.
// field azimuth for tangential, perpendicular for sagittal).
func fanLineWidth(samples []pupil.Sample, chiefDir, fanDir types.Vec3, zPlane float64) float64 {
	if len(samples) == 0 {
		return math.Inf(1)
	}
	// Find the chief ray (centre of fan, index ≈ len/2).
	chiefIdx := len(samples) / 2
	if chiefIdx >= len(samples) {
		chiefIdx = 0
	}

	// Compute chief ray intersection with the z-plane.
	chiefSample := samples[chiefIdx]
	chiefO := chiefSample.Origin
	chiefD := chiefSample.Dir
	if chiefD.Z == 0 {
		return math.Inf(1)
	}
	tChief := (zPlane - chiefO.Z) / chiefD.Z
	chiefPt := types.Vec3{
		X: chiefO.X + tChief*chiefD.X,
		Y: chiefO.Y + tChief*chiefD.Y,
		Z: zPlane,
	}

	// Project fanDir onto the plane perpendicular to chiefDir to get the
	// measurement axis.  For a tangential fan fanDir is the field azimuth;
	// for sagittal it is the perpendicular.  This ensures the line width is
	// measured in the correct transverse direction.
	dot := fanDir.X*chiefDir.X + fanDir.Y*chiefDir.Y + fanDir.Z*chiefDir.Z
	axisX := fanDir.X - dot*chiefDir.X
	axisY := fanDir.Y - dot*chiefDir.Y
	norm := math.Hypot(axisX, axisY)
	if norm > 1e-12 {
		axisX /= norm
		axisY /= norm
	} else {
		axisX, axisY = 1, 0
	}

	var ss float64
	var wsum float64
	for _, s := range samples {
		if !s.OK {
			continue
		}
		// Intersect this ray with the z-plane.
		if s.Dir.Z == 0 {
			continue
		}
		t := (zPlane - s.Origin.Z) / s.Dir.Z
		if t < 0 {
			continue
		}
		pt := types.Vec3{
			X: s.Origin.X + t*s.Dir.X,
			Y: s.Origin.Y + t*s.Dir.Y,
			Z: zPlane,
		}
		// Transverse deviation from chief ray.
		dx := pt.X - chiefPt.X
		dy := pt.Y - chiefPt.Y
		dev := dx*axisX + dy*axisY
		w := s.Area
		if w <= 0 {
			w = 1
		}
		ss += w * dev * dev
		wsum += w
	}
	if wsum <= 0 {
		return math.Inf(1)
	}
	return math.Sqrt(ss / wsum)
}

// buildFocusSamples extracts per-ray local focus residuals from traced fan
// samples. For each successfully traced ray it records the surface hit
// coordinates on surfaceID, the normalised pupil coordinate,
// and the local focus residual Δz_loc = z_closest_approach − bestZ.
func buildFocusSamples(samples []pupil.Sample,
	chiefDir, fanDir types.Vec3, bestZ float64, fieldID, surfaceID int, fanKind string, trial bool) []types.AsphereFocusSample {

	if len(samples) == 0 {
		return nil
	}
	// Chief ray (centre of fan).
	chiefIdx := len(samples) / 2
	if chiefIdx >= len(samples) {
		chiefIdx = 0
	}
	chief := samples[chiefIdx]

	var out []types.AsphereFocusSample
	for _, s := range samples {
		if !s.OK {
			continue
		}
		if len(s.Surfaces) == 0 {
			continue
		}
		var hit types.SurfaceResult
		found := false
		for _, sr := range s.Surfaces {
			if sr.SurfaceID == surfaceID && sr.ErrorCode == "" &&
				!math.IsNaN(sr.Position.X) && !math.IsNaN(sr.Position.Y) &&
				!math.IsInf(sr.Position.X, 0) && !math.IsInf(sr.Position.Y, 0) {
				hit = sr
				found = true
				break
			}
		}
		if !found {
			continue
		}
		hitX := hit.Position.X
		hitY := hit.Position.Y

		// Local focus: z-coordinate of closest approach to chief ray.
		// Parameterise the ray as P(t) = Origin + t*Dir and find t that
		// minimises |P(t) − Chief(t')|².  For the longitudinal component
		// we project onto the chief direction.
		deltaZ := localFocusResidual(s, chief, chiefDir, bestZ)

		// Radial distance in the fan plane.
		rMM := math.Hypot(s.PupilX, s.PupilY)

		out = append(out, types.AsphereFocusSample{
			FieldID:  fieldID,
			Trial:    trial,
			PupilX:   s.PupilX,
			PupilY:   s.PupilY,
			HitX:     hitX,
			HitY:     hitY,
			FanKind:  fanKind,
			RMM:      rMM,
			DeltaZ:   deltaZ,
			Residual: closestDistanceToChief(s, chief),
		})
	}
	return out
}

// localFocusResidual computes the longitudinal focus residual of a ray
// relative to the chief ray.  It finds the z-coordinate of closest approach
// of the ray to the chief ray and subtracts bestZ.
func localFocusResidual(ray, chief pupil.Sample, chiefDir types.Vec3, bestZ float64) float64 {
	if ray.Dir.Z == 0 || chief.Dir.Z == 0 {
		return 0
	}
	// Intersect both rays with a z-plane at bestZ.
	tRay := (bestZ - ray.Origin.Z) / ray.Dir.Z
	tChief := (bestZ - chief.Origin.Z) / chief.Dir.Z
	if tRay < 0 || tChief < 0 {
		return 0
	}
	rayPt := types.Vec3{
		X: ray.Origin.X + tRay*ray.Dir.X,
		Y: ray.Origin.Y + tRay*ray.Dir.Y,
		Z: bestZ,
	}
	chiefPt := types.Vec3{
		X: chief.Origin.X + tChief*chief.Dir.X,
		Y: chief.Origin.Y + tChief*chief.Dir.Y,
		Z: bestZ,
	}
	// Project the transverse deviation onto the chief direction to get the
	// longitudinal component.
	dx := rayPt.X - chiefPt.X
	dy := rayPt.Y - chiefPt.Y
	dz := rayPt.Z - chiefPt.Z
	dot := dx*chiefDir.X + dy*chiefDir.Y + dz*chiefDir.Z
	return dot
}

// closestDistanceToChief returns the minimum distance between a ray and the
// chief ray.  A large value indicates the ray is far from the chief and its
// local focus interpretation is less reliable (e.g. strong coma).
func closestDistanceToChief(ray, chief pupil.Sample) float64 {
	if ray.Dir.Z == 0 || chief.Dir.Z == 0 {
		return math.Inf(1)
	}
	// Find closest approach in a z-slice at the chief ray's origin z.
	zSlice := chief.Origin.Z
	tRay := (zSlice - ray.Origin.Z) / ray.Dir.Z
	if tRay < 0 {
		tRay = 0
	}
	rayPt := types.Vec3{
		X: ray.Origin.X + tRay*ray.Dir.X,
		Y: ray.Origin.Y + tRay*ray.Dir.Y,
		Z: zSlice,
	}
	chiefPt := chief.Origin
	dx := rayPt.X - chiefPt.X
	dy := rayPt.Y - chiefPt.Y
	return math.Hypot(dx, dy)
}

// minimize1D finds the minimizer of f over [lo, hi] by golden-section search.
func minimize1DFocus(f func(float64) float64, lo, hi float64) (bestX, bestF float64) {
	const resphi = 2 - 1.618033988749895
	a, b := lo, hi
	c := a + resphi*(b-a)
	d := b - resphi*(b-a)
	fc, fd := f(c), f(d)
	for iter := 0; iter < 80; iter++ {
		if math.Abs(b-a) < 1e-12*(1+math.Abs(b)+math.Abs(a)) {
			break
		}
		if fc < fd {
			b, d = d, c
			fd = fc
			c = a + resphi*(b-a)
			fc = f(c)
		} else {
			a, c = c, d
			fc = fd
			d = b - resphi*(b-a)
			fd = f(d)
		}
	}
	if fc < fd {
		return c, fc
	}
	return d, fd
}

// ComputeFieldFocus traces tangential/sagittal fans for every (field,
// wavelength) pair and finds the best-focus Z for each fan by minimizing the
// RMS line width. The search window is centred on refImageZ.
func ComputeFieldFocus(surfaces []types.Surface, fields []Field, wavelengths []float64,
	refImageZ float64, cfg FocusConfig, gc *glass.Catalog, surfaceID int, pupilZs []float64) FocusResult {

	if len(wavelengths) == 0 {
		wavelengths = []float64{types.DefaultWavelength}
	}
	result := FocusResult{ReferenceImageZ: refImageZ}

	if len(wavelengths) > 1 {
		wavelengths = wavelengths[:1]
	}
	for fi, f := range fields {
		for _, wl := range wavelengths {
			ffr := FieldFocusResult{
				FieldID:         f.ID,
				Angle:           f.Angle,
				Wavelength:      wl,
				ReferenceImageZ: refImageZ,
			}

			radius := dls.ApertureRadiusForGrid(surfaces, 0, wl, gc, 1.0)
			if radius <= 0 {
				radius = surface.MinApertureRadius(surfaces)
			}
			if radius <= 0 {
				continue
			}
			var pupilZ float64
			if fi < len(pupilZs) {
				pupilZ = pupilZs[fi]
			}

			tx, ty := raymath.FieldAzimuth(f.Direction)
			sx, sy := -ty, tx // sagittal = rotate 90°

			tSamples, sSamples := launchFans(f, cfg.NumSamples, radius, pupilZ)
			if cfg.Tangential {
				traceFanRays(tSamples, surfaces, wl, gc)
				dir := rayDirection(f)
				fanDir := types.Vec3{X: tx, Y: ty, Z: 0}
				bestZ, rms := findBestFocus(tSamples, dir, fanDir, refImageZ)
				ffr.Tangential = FocusFanResult{
					BestZ:        bestZ,
					RMSLineWidth: rms,
					Samples:      buildFocusSamples(tSamples, dir, fanDir, bestZ, f.ID, surfaceID, "tangential", false),
				}
			}
			if cfg.Sagittal {
				traceFanRays(sSamples, surfaces, wl, gc)
				dir := rayDirection(f)
				fanDir := types.Vec3{X: sx, Y: sy, Z: 0}
				bestZ, rms := findBestFocus(sSamples, dir, fanDir, refImageZ)
				ffr.Sagittal = FocusFanResult{
					BestZ:        bestZ,
					RMSLineWidth: rms,
					Samples:      buildFocusSamples(sSamples, dir, fanDir, bestZ, f.ID, surfaceID, "sagittal", false),
				}
			}

			result.PerField = append(result.PerField, ffr)
		}
	}
	// Keep the per-fan residual shape, but express all samples relative to the
	// centre field's best focus so the graph has one common vertical reference.
	centerBestZ, ok := centerFieldBestZ(result.PerField)
	if ok {
		for i := range result.PerField {
			ff := &result.PerField[i]
			shiftSamples := func(samples []types.AsphereFocusSample, fanBestZ float64) {
				for j := range samples {
					samples[j].DeltaZ += fanBestZ - centerBestZ
				}
			}
			shiftSamples(ff.Tangential.Samples, ff.Tangential.BestZ)
			shiftSamples(ff.Sagittal.Samples, ff.Sagittal.BestZ)
		}
	}
	return result
}

// centerFieldBestZ returns the mean T/S best focus of the field nearest the
// optical axis. It ignores unavailable fan results without changing the focus
// search or the reported per-fan BestZ values.
func centerFieldBestZ(fields []FieldFocusResult) (float64, bool) {
	if len(fields) == 0 {
		return 0, false
	}
	center := fields[0]
	for _, f := range fields[1:] {
		if math.Abs(f.Angle) < math.Abs(center.Angle) {
			center = f
		}
	}
	var sum float64
	var count int
	if len(center.Tangential.Samples) > 0 {
		sum += center.Tangential.BestZ
		count++
	}
	if len(center.Sagittal.Samples) > 0 {
		sum += center.Sagittal.BestZ
		count++
	}
	if count == 0 {
		return 0, false
	}
	return sum / float64(count), true
}

// findBestFocus searches for the z-plane that minimises the RMS line width of
// the fan. The search window is [refImageZ - halfRange, refImageZ + halfRange].
func findBestFocus(samples []pupil.Sample, chiefDir, fanDir types.Vec3, refImageZ float64) (float64, float64) {
	halfRange := 1.0 // ±1 mm default window
	lo := refImageZ - halfRange
	hi := refImageZ + halfRange
	bestZ, _ := minimize1DFocus(func(z float64) float64 {
		return fanLineWidth(samples, chiefDir, fanDir, z)
	}, lo, hi)
	return bestZ, fanLineWidth(samples, chiefDir, fanDir, bestZ)
}

// ComputeFieldFocusTrial is the same as ComputeFieldFocus but uses a modified
// system where the given surface carries the trial asphere coefficients.
func ComputeFieldFocusTrial(surfaces []types.Surface, fields []Field, wavelengths []float64,
	refImageZ float64, cfg FocusConfig, gc *glass.Catalog,
	surfaceID int, coeffs types.AsphereCoeffs, pupilZs []float64) FocusResult {

	trial := withAsphere(surfaces, surfaceID, coeffs)
	return ComputeFieldFocus(trial, fields, wavelengths, refImageZ, cfg, gc, surfaceID, pupilZs)
}

// FocusGains computes the relative improvement of the trial system over the
// base for both field curvature (mean focus) and astigmatism (T-S split).
func FocusGains(base, trial []FieldFocusResult) (fieldCurvGain, astigGain, tangGain, sagGain float64) {
	var bc2, tc2 float64
	var ba2, ta2 float64
	var bt2, tt2 float64
	var bs2, ts2 float64
	var wSum float64
	for i := range base {
		if i >= len(trial) {
			break
		}
		w := 1.0
		bMean := (base[i].Tangential.BestZ + base[i].Sagittal.BestZ) / 2
		tMean := (trial[i].Tangential.BestZ + trial[i].Sagittal.BestZ) / 2
		bSplit := base[i].Tangential.BestZ - base[i].Sagittal.BestZ
		tSplit := trial[i].Tangential.BestZ - trial[i].Sagittal.BestZ
		bRef := base[i].ReferenceImageZ
		bc2 += w * (bMean - bRef) * (bMean - bRef)
		tc2 += w * (tMean - bRef) * (tMean - bRef)
		ba2 += w * bSplit * bSplit
		ta2 += w * tSplit * tSplit
		bt2 += w * (base[i].Tangential.BestZ - bRef) * (base[i].Tangential.BestZ - bRef)
		tt2 += w * (trial[i].Tangential.BestZ - bRef) * (trial[i].Tangential.BestZ - bRef)
		bs2 += w * (base[i].Sagittal.BestZ - bRef) * (base[i].Sagittal.BestZ - bRef)
		ts2 += w * (trial[i].Sagittal.BestZ - bRef) * (trial[i].Sagittal.BestZ - bRef)
		wSum += w
	}
	if wSum <= 0 {
		return 0, 0, 0, 0
	}
	if bc2 > 0 {
		fieldCurvGain = 1 - tc2/bc2
	}
	if ba2 > 0 {
		astigGain = 1 - ta2/ba2
	}
	if bt2 > 0 {
		tangGain = 1 - tt2/bt2
	}
	if bs2 > 0 {
		sagGain = 1 - ts2/bs2
	}
	return
}

// FocusRMS returns the weighted RMS of mean-focus residual and T-S split.
func FocusRMS(fields []FieldFocusResult) (rmsMean, rmsTS float64) {
	var m2, ts2, wSum float64
	for _, f := range fields {
		w := 1.0
		mean := (f.Tangential.BestZ + f.Sagittal.BestZ) / 2
		split := f.Tangential.BestZ - f.Sagittal.BestZ
		ref := f.ReferenceImageZ
		m2 += w * (mean - ref) * (mean - ref)
		ts2 += w * split * split
		wSum += w
	}
	if wSum <= 0 {
		return 0, 0
	}
	return math.Sqrt(m2 / wSum), math.Sqrt(ts2 / wSum)
}
