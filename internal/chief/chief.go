package chief

import (
	"math"
	"runtime"
	"sync"

	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/raymath"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

type Result struct {
	FieldAngle     float64
	FieldHeight    float64
	FieldDir       types.Vec3
	ChiefRay       types.Ray
	ImageHeight    types.Vec3
	EntrancePupil  *types.Pupil
	ExitPupil      *types.Pupil
	GridPoints     []types.GridPoint
	SpotStats      *types.SpotStats
	RayFan         *types.RayFan
	PerSurfaceMaxY []float64
	Wavelengths    []types.WavelengthStats
}

func DetermineChiefRays(
	system types.System,
	fields []types.FieldDef,
	refSurfaceID int,
	numRays int,
	gc *glass.Catalog,
	pol types.JonesVector,
	wavelength float64,
	dumpMap bool,
) []Result {
	return determineChiefRays(system, fields, refSurfaceID, numRays, gc, pol, wavelength, dumpMap, types.GridPolar, nil, nil, nil)
}

func DetermineChiefRaysGrid(
	system types.System,
	fields []types.FieldDef,
	refSurfaceID int,
	numRays int,
	gc *glass.Catalog,
	pol types.JonesVector,
	wavelength float64,
	dumpMap bool,
	gridType types.GridType,
	passThrough *types.PassThroughTarget,
	fanCfg *types.RayFanConfig,
	wavelengths []float64,
) []Result {
	if gridType == "" {
		gridType = types.GridPolar
	}
	return determineChiefRays(system, fields, refSurfaceID, numRays, gc, pol, wavelength, dumpMap, gridType, passThrough, fanCfg, wavelengths)
}

// maxPupilIterations bounds the dynamic-pupil fixed-point loop. The grid is
// re-centred on the recomputed entrance pupil each pass; the chief rays (and
// with them the pupil) settle after a few passes.
const maxPupilIterations = 3

// pupilTolMM is the convergence threshold for the dynamic pupil Z.
const pupilTolMM = 0.05

func determineChiefRays(
	system types.System,
	fields []types.FieldDef,
	refSurfaceID int,
	numRays int,
	gc *glass.Catalog,
	pol types.JonesVector,
	wavelength float64,
	dumpMap bool,
	gridType types.GridType,
	passThrough *types.PassThroughTarget,
	fanCfg *types.RayFanConfig,
	wavelengths []float64,
) []Result {
	engine := ray.NewEngine(gc, nil)

	// Grid radius: with an explicit stop the paraxial entrance pupil (the
	// stop's image); without one the beam-aware fixed-aperture cap so the
	// F-number is preserved while auto_aperture:false surfaces cap the beam.
	apertureRadius := dls.ApertureRadiusForGrid(system.Surfaces, system.StopSurface, wavelength, gc, 1.0)
	if apertureRadius <= 0 {
		return nil
	}

	// No stop (stop_surface <= 0) → dynamic pupil: per-field entrance pupil Z
	// from the chief-ray crossings, iterated until it settles. An explicit stop
	// keeps the traditional stop-centred grid (one pass).
	pupilZs := seedPupilZs(system, fields)
	dynamic := system.StopSurface <= 0

	var results []Result
	if dynamic {
		for iter := 0; iter < maxPupilIterations; iter++ {
			results = traceFields(system, engine, fields, refSurfaceID, numRays, apertureRadius,
				pol, wavelength, dumpMap, gridType, passThrough, fanCfg, pupilZs)
			next := recomputeEntrancePupils(results, pupilZs, engine, system.Surfaces)
			changed := false
			for i := range pupilZs {
				if math.Abs(next[i]-pupilZs[i]) > pupilTolMM {
					changed = true
				}
			}
			pupilZs = next
			if !changed {
				break
			}
		}
	} else {
		results = traceFields(system, engine, fields, refSurfaceID, numRays, apertureRadius,
			pol, wavelength, dumpMap, gridType, passThrough, fanCfg, pupilZs)
	}

	setPupils(results, engine, system.Surfaces, pupilZs, apertureRadius)

	// Multi-wavelength spot stats
	if len(wavelengths) > 0 {
		for i := range results {
			results[i].Wavelengths = computeWavelengthStats(
				engine, system, results[i].ChiefRay.Path, refSurfaceID, pol,
				results[i].GridPoints, results[i].ImageHeight, wavelengths,
			)
		}
	}

	if fanCfg != nil && len(fanCfg.Angles) > 0 {
		for i := range results {
			results[i].RayFan = computeRayFan(
				engine, system, results[i].ChiefRay.Path,
				results[i].ChiefRay.Initial.Origin,
				results[i].ChiefRay.Initial.Direction,
				refSurfaceID, pol, wavelength,
				apertureRadius, pupilZs[i], fanCfg.Angles, fanCfg.NumRays,
			)
		}
	}

	return results
}

// seedPupilZs returns the initial per-field entrance pupil Z: the explicit stop
// surface Z, else the fixed-minimum-aperture surface Z (the tightest
// auto_aperture: false surface — where the beam is physically limited), else
// the first surface Z.
func seedPupilZs(system types.System, fields []types.FieldDef) []float64 {
	seed := 0.0
	if system.StopSurface > 0 {
		for _, s := range system.Surfaces {
			if s.ID == system.StopSurface {
				seed = s.PhysicalZ
				break
			}
		}
	} else {
		seed = surface.FixedMinApertureRadiusZ(system.Surfaces)
		if seed == 0 && len(system.Surfaces) > 0 {
			seed = system.Surfaces[0].PhysicalZ
		}
	}
	zs := make([]float64, len(fields))
	for i := range zs {
		zs[i] = seed
	}
	return zs
}

// traceFields traces one grid per field, each centred on its own entrance pupil
// Z, and returns the centroid chief rays.
func traceFields(
	system types.System,
	engine *ray.Engine,
	fields []types.FieldDef,
	refSurfaceID int,
	numRays int,
	apertureRadius float64,
	pol types.JonesVector,
	wavelength float64,
	dumpMap bool,
	gridType types.GridType,
	passThrough *types.PassThroughTarget,
	fanCfg *types.RayFanConfig,
	pupilZs []float64,
) []Result {
	var results []Result

	for fi, fd := range fields {
		dx, dy := 0.0, 1.0
		if len(fd.Direction) >= 2 {
			norm := math.Hypot(fd.Direction[0], fd.Direction[1])
			if norm > 0 {
				dx = fd.Direction[0] / norm
				dy = fd.Direction[1] / norm
			}
		}

		path := dls.BuildPath(system.Surfaces)
		if len(fd.Path) > 0 {
			path = append([]int{}, fd.Path...)
			if path[0] != 0 {
				path = append([]int{0}, path...)
			}
		}

		pupilZ := 0.0
		if fi < len(pupilZs) {
			pupilZ = pupilZs[fi]
		}

		var result Result
		switch {
		case math.Abs(fd.ImageHeight) > 1e-12:
			angle := searchAngleForImageHeight(system, engine, fd.ImageHeight,
				dx, dy, refSurfaceID, numRays, apertureRadius, pol, wavelength, gridType, passThrough, pupilZ)
			thetaRad := raymath.DegToRad(angle)
			if passThrough != nil && passThrough.Surface > 0 {
				result = computeChiefRayAngleGridWithPassThrough(system, engine, path, thetaRad, dx, dy,
					refSurfaceID, numRays, apertureRadius, pol, wavelength, dumpMap, gridType, passThrough, pupilZ)
			} else {
				result = computeChiefRayAngleGrid(system, engine, path, thetaRad, dx, dy,
					refSurfaceID, numRays, apertureRadius, pol, wavelength, dumpMap, gridType, pupilZ)
			}
			result.FieldAngle = angle

		case math.Abs(fd.Height) > 1e-12:
			objectZ := fd.ObjectZ
			if objectZ == 0 {
				objectZ = -1000.0
			}
			if passThrough != nil && passThrough.Surface > 0 {
				result = computeChiefRayHeightGridWithPassThrough(system, engine, path, fd.Height, dx, dy,
					objectZ, refSurfaceID, numRays, apertureRadius, pol, wavelength, dumpMap, gridType, passThrough)
			} else {
				result = computeChiefRayHeightGrid(system, engine, path, fd.Height, dx, dy,
					objectZ, refSurfaceID, numRays, apertureRadius, pol, wavelength, dumpMap, gridType)
			}
			result.FieldHeight = fd.Height

		default:
			thetaRad := raymath.DegToRad(fd.Angle)
			if passThrough != nil && passThrough.Surface > 0 {
				result = computeChiefRayAngleGridWithPassThrough(system, engine, path, thetaRad, dx, dy,
					refSurfaceID, numRays, apertureRadius, pol, wavelength, dumpMap, gridType, passThrough, pupilZ)
			} else {
				result = computeChiefRayAngleGrid(system, engine, path, thetaRad, dx, dy,
					refSurfaceID, numRays, apertureRadius, pol, wavelength, dumpMap, gridType, pupilZ)
			}
			result.FieldAngle = fd.Angle
		}
		result.FieldDir = types.Vec3{X: dx, Y: dy}

		results = append(results, result)
	}

	return results
}

// outgoingLine returns the image-space segment of a traced chief ray: the line
// through the last two surface hits.
func outgoingLine(engine *ray.Engine, r types.Ray, surfaces []types.Surface) (types.Vec3, types.Vec3, bool) {
	tr := engine.TraceRay(r, surfaces)
	if tr.Error != "" || len(tr.Surfaces) < 2 {
		return types.Vec3{}, types.Vec3{}, false
	}
	n := len(tr.Surfaces)
	p1 := tr.Surfaces[n-2].Position
	p2 := tr.Surfaces[n-1].Position
	d := p2.Subtract(p1)
	if d.LengthSq() < 1e-18 {
		return types.Vec3{}, types.Vec3{}, false
	}
	return p1, d.Normalize(), true
}

// lineCrossingZ returns the Z of the midpoint of the closest approach of two
// lines. For chief rays in a common field plane this is the crossing point.
func lineCrossingZ(p0, d0, p1, d1 types.Vec3) (float64, bool) {
	w0 := p0.Subtract(p1)
	a := d0.Dot(d0)
	b := d0.Dot(d1)
	c := d1.Dot(d1)
	e := d0.Dot(w0)
	f := d1.Dot(w0)
	denom := a*c - b*b
	if math.Abs(denom) < 1e-18 {
		return 0, false
	}
	s := (b*f - c*e) / denom
	t := (a*f - b*e) / denom
	mid := p0.Add(d0.Scale(s)).Add(p1.Add(d1.Scale(t))).Scale(0.5)
	return mid.Z, true
}

// fullChiefPath returns the traced polyline (per-surface positions) of a ray.
func fullChiefPath(engine *ray.Engine, r types.Ray, surfaces []types.Surface) []types.Vec3 {
	tr := engine.TraceRay(r, surfaces)
	if tr.Error != "" {
		return nil
	}
	out := make([]types.Vec3, len(tr.Surfaces))
	for i, sr := range tr.Surfaces {
		out[i] = sr.Position
	}
	return out
}

// inLensCrossingZ finds where two traced chief-ray polylines cross inside the
// given Z window (the aperture region where the on-axis and off-axis chief rays
// intersect). Returns the Z of the cleanest crossing.
func inLensCrossingZ(p0, p1 []types.Vec3, zLo, zHi float64) (float64, bool) {
	bestZ := 0.0
	bestDist := math.Inf(1)
	for a := 0; a+1 < len(p0); a++ {
		for b := 0; b+1 < len(p1); b++ {
			z, ok := lineCrossingZ(p0[a], p0[a+1].Subtract(p0[a]), p1[b], p1[b+1].Subtract(p1[b]))
			if !ok {
				continue
			}
			if z < zLo-1 || z > zHi+1 {
				continue
			}
			// distance between the two closest points
			wa := p0[a].Subtract(p1[b])
			da := p0[a+1].Subtract(p0[a])
			db := p1[b+1].Subtract(p1[b])
			aa := da.Dot(da)
			bb := da.Dot(db)
			cc := db.Dot(db)
			ee := da.Dot(wa)
			ff := db.Dot(wa)
			den := aa*cc - bb*bb
			if math.Abs(den) < 1e-18 {
				continue
			}
			s := (bb*ff - cc*ee) / den
			t := (aa*ff - bb*ee) / den
			pa := p0[a].Add(da.Scale(s))
			pb := p1[b].Add(db.Scale(t))
			dist := pa.Subtract(pb).Length()
			if dist < bestDist {
				bestDist = dist
				bestZ = z
			}
		}
	}
	if math.IsInf(bestDist, 1) || bestDist > 2.0 {
		return 0, false
	}
	return bestZ, true
}

// recomputeEntrancePupils updates each off-axis field's pupil Z from the
// in-lens crossing of its chief-ray polyline with field 0's — the physical
// aperture position where the chief rays intersect. The on-axis field's pupil
// (field 0) stays as the reference seed.
func recomputeEntrancePupils(results []Result, cur []float64, engine *ray.Engine, surfaces []types.Surface) []float64 {
	next := make([]float64, len(cur))
	copy(next, cur)
	if len(results) < 2 {
		return next
	}
	zLo, zHi, _ := surfaceZRange(surfaces)
	path0 := fullChiefPath(engine, results[0].ChiefRay, surfaces)
	for i := 1; i < len(results); i++ {
		pathI := fullChiefPath(engine, results[i].ChiefRay, surfaces)
		if z, ok := inLensCrossingZ(path0, pathI, zLo, zHi); ok {
			next[i] = z
		}
	}
	return next
}

// setPupils records the per-field entrance and exit pupils. The entrance pupil
// is the in-lens chief-ray crossing (the aperture position where each field's
// chief ray crosses field 0's); field 0's is the mean of the off-axis fields.
// The exit pupil is the image-space crossing of the outgoing segments, accepted
// only within a plausible window (the outgoing rays are nearly parallel on the
// image side, so the crossing is ill-conditioned for strongly aberrated designs
// and is then omitted).
func setPupils(results []Result, engine *ray.Engine, surfaces []types.Surface, pupilZs []float64, apertureRadius float64) {
	n := len(results)
	if n == 0 {
		return
	}

	// Entrance pupil: centre = chief-ray position at the pupil plane (full
	// x/y/z, not only z) and radius = entrance-pupil radius. The full centre is
	// required so stop-edge marginal rays and off-axis pupils are placed at the
	// correct transverse position.
	entMean, entCnt := 0.0, 0
	for i := 1; i < n; i++ {
		if results[i].EntrancePupil == nil {
			continue
		}
		z := pupilZs[i]
		results[i].EntrancePupil.Center = chiefAtZ(results[i].ChiefRay, z)
		results[i].EntrancePupil.Radius = apertureRadius
		entMean += z
		entCnt++
	}
	if entCnt > 0 && results[0].EntrancePupil != nil {
		z := entMean / float64(entCnt)
		results[0].EntrancePupil.Center = chiefAtZ(results[0].ChiefRay, z)
		results[0].EntrancePupil.Radius = apertureRadius
	}

	if n < 2 {
		return
	}
	zLo, zHi, track := surfaceZRange(surfaces)
	if track <= 0 {
		track = 100
	}
	e0, ed0, ok0 := outgoingLine(engine, results[0].ChiefRay, surfaces)
	if !ok0 {
		return
	}
	extMean, extCnt := 0.0, 0
	for i := 1; i < n; i++ {
		e1, ed1, ok := outgoingLine(engine, results[i].ChiefRay, surfaces)
		if !ok {
			continue
		}
		if z, ok := lineCrossingZ(e0, ed0, e1, ed1); ok && z >= zLo-2*track && z <= zHi+2*track {
			results[i].ExitPupil = &types.Pupil{Center: types.Vec3{Z: z}}
			extMean += z
			extCnt++
		}
	}
	if extCnt > 0 {
		results[0].ExitPupil = &types.Pupil{Center: types.Vec3{Z: extMean / float64(extCnt)}}
	}
}

// chiefAtZ returns the point where the chief ray crosses the plane Z = z. If the
// chief ray is degenerate (grazing, |dz|~0) the Z-only centre is returned.
func chiefAtZ(chiefRay types.Ray, z float64) types.Vec3 {
	o := chiefRay.Initial.Origin
	d := chiefRay.Initial.Direction
	if math.Abs(d.Z) < 1e-12 {
		return types.Vec3{Z: z}
	}
	t := (z - o.Z) / d.Z
	return types.Vec3{X: o.X + t*d.X, Y: o.Y + t*d.Y, Z: z}
}

// surfaceZRange returns the min/max physical Z over the surfaces and the total
// track (first-to-last thickness distance).
func surfaceZRange(surfaces []types.Surface) (lo, hi, track float64) {
	lo, hi = math.Inf(1), math.Inf(-1)
	for _, s := range surfaces {
		if s.PhysicalZ < lo {
			lo = s.PhysicalZ
		}
		if s.PhysicalZ > hi {
			hi = s.PhysicalZ
		}
	}
	if lo == math.Inf(1) {
		return 0, 0, 0
	}
	track = hi - lo
	return lo, hi, track
}

// --- angle-based (infinite conjugate) ---

func computeChiefRayAngle(
	system types.System,
	engine *ray.Engine,
	thetaRad, dx, dy float64,
	refSurfaceID int,
	numRays int,
	apertureRadius float64,
	pol types.JonesVector,
	wavelength float64,
	dumpMap bool,
	pupilZ float64,
) Result {
	return computeChiefRayAngleGrid(system, engine, dls.BuildPath(system.Surfaces), thetaRad, dx, dy, refSurfaceID, numRays, apertureRadius, pol, wavelength, dumpMap, types.GridPolar, pupilZ)
}

func computeChiefRayAngleGrid(
	system types.System,
	engine *ray.Engine,
	path []int,
	thetaRad, dx, dy float64,
	refSurfaceID int,
	numRays int,
	apertureRadius float64,
	pol types.JonesVector,
	wavelength float64,
	dumpMap bool,
	gridType types.GridType,
	pupilZ float64,
) Result {
	zStart := -100.0

	sinT := math.Sin(thetaRad)
	cosT := math.Cos(thetaRad)
	rayDir := types.Vec3{
		X: sinT * dx,
		Y: sinT * dy,
		Z: cosT,
	}.Normalize()

	tanT := sinT / cosT
	pupilCenterX := -(pupilZ - zStart) * tanT * dx
	pupilCenterY := -(pupilZ - zStart) * tanT * dy

	cx, cy, grid := tracePupilGrid(system, engine, path, numRays, apertureRadius,
		pupilCenterX, pupilCenterY, zStart, rayDir, types.Vec3{},
		refSurfaceID, pol, wavelength, dumpMap, gridType)

	originY := searchOriginForTarget(rayDir.Y, rayDir, zStart, refSurfaceID, cy,
		path, wavelength, pol, engine, system.Surfaces, pupilZ, false,
		func(sr types.SurfaceResult) float64 { return sr.Position.Y })
	originX := 0.0
	if math.Abs(rayDir.X) > 1e-12 {
		originX = searchOriginForTarget(rayDir.X, rayDir, zStart, refSurfaceID, cx,
			path, wavelength, pol, engine, system.Surfaces, pupilZ, true,
			func(sr types.SurfaceResult) float64 { return sr.Position.X })
	}

	origin := types.Vec3{X: originX, Y: originY, Z: zStart}
	return buildResult(engine, system, path, origin, rayDir, refSurfaceID, pol, wavelength, cx, cy, apertureRadius, grid, dumpMap)
}

// --- pass-through constrained chief ray (angle-based) ---

// backwardChiefOrigin constructs the through-stop chief ray by tracing backward
// from the stop centre through the front optics and matching the emergent
// object-space angle to the field angle. This is more robust than the forward
// origin search (which can overshoot the narrow valid origin window of a small
// lens). Returns the origin at zStart and the forward direction, or ok=false
// when the construction is not applicable (no front optics, a folded front, or
// no matching direction such as a fisheye field).
func backwardChiefOrigin(
	engine *ray.Engine,
	surfaces []types.Surface,
	path []int,
	stopID int,
	ptCoord types.Vec3,
	thetaRad, dx, dy float64,
	zStart, wavelength float64,
) (types.Vec3, types.Vec3, bool) {
	stopCenter, frontSeq, ok := backwardFrontSetup(surfaces, stopID, ptCoord)
	if !ok {
		return types.Vec3{}, types.Vec3{}, false
	}

	uY, ok := searchBackwardTangent(engine, surfaces, frontSeq, stopCenter,
		math.Atan(math.Tan(thetaRad)*dy), false, wavelength)
	if !ok {
		return types.Vec3{}, types.Vec3{}, false
	}
	uX := 0.0
	if math.Abs(dx) > 1e-9 {
		var okX bool
		if uX, okX = searchBackwardTangent(engine, surfaces, frontSeq, stopCenter,
			math.Atan(math.Tan(thetaRad)*dx), true, wavelength); !okX {
			return types.Vec3{}, types.Vec3{}, false
		}
	}

	bwd := types.Vec3{X: uX, Y: uY, Z: -1}.Normalize()
	emPos, emDir, ok := engine.TraceBackward(surfaces, frontSeq, stopCenter, bwd, wavelength)
	if !ok {
		return types.Vec3{}, types.Vec3{}, false
	}
	fwdObj := emDir.Scale(-1)
	if fwdObj.Z <= 1e-12 {
		return types.Vec3{}, types.Vec3{}, false
	}
	origin := emPos.Add(fwdObj.Scale((zStart - emPos.Z) / fwdObj.Z))

	// Validate the forward chief ray actually passes through the stop centre
	// (within a micron). A failure after the stop is acceptable only at the
	// image surface (an off-axis image landing outside the detector aperture);
	// a mid-lens failure means the construction is unreliable, so fall back to
	// the forward search.
	probe := types.Ray{
		Wavelength: wavelength,
		Initial:    types.RayState{Origin: origin, Direction: fwdObj},
		Path:       path,
		Jones:      types.NewCircularJones(true),
	}
	pres := engine.TraceRay(probe, surfaces)
	reachedStop := false
	for i := range pres.Surfaces {
		if pres.Surfaces[i].SurfaceID == stopID {
			hit := pres.Surfaces[i].Position
			if math.Hypot(hit.X-stopCenter.X, hit.Y-stopCenter.Y) > 1e-3 {
				return types.Vec3{}, types.Vec3{}, false
			}
			reachedStop = true
			break
		}
	}
	if !reachedStop {
		return types.Vec3{}, types.Vec3{}, false
	}
	if pres.Error != "" && len(path) >= 2 {
		lastReached := -1
		if len(pres.Surfaces) > 0 {
			lastReached = pres.Surfaces[len(pres.Surfaces)-1].SurfaceID
		}
		if lastReached != path[len(path)-2] {
			return types.Vec3{}, types.Vec3{}, false
		}
	}
	return origin, fwdObj, true
}

// backwardFrontSetup resolves the stop surface centre (global) and the front
// optics sequence for a backward trace from the stop, applying the applicability
// guards shared by the angle and object-point constructions: the stop must exist
// with front optics before it, the front must be un-folded, and the front
// surfaces' physical Z must be strictly decreasing (positive thicknesses).
func backwardFrontSetup(surfaces []types.Surface, stopID int, ptCoord types.Vec3) (types.Vec3, []int, bool) {
	var stopSurf *types.Surface
	for i := range surfaces {
		if surfaces[i].ID == stopID {
			stopSurf = &surfaces[i]
			break
		}
	}
	if stopSurf == nil {
		return types.Vec3{}, nil, false
	}

	frontSeq := ray.FrontPath(surfaces, stopID)
	if len(frontSeq) == 0 {
		// The stop is the first surface: no front optics to trace through, so
		// the geometric estimate applies.
		return types.Vec3{}, nil, false
	}
	for _, idx := range frontSeq {
		if surfaces[idx].Reflects() {
			return types.Vec3{}, nil, false
		}
	}
	// The backward ray visits the front surfaces in decreasing index order, so
	// their physical Z must be strictly decreasing (positive thicknesses).
	if stopSurf.PhysicalZ <= surfaces[frontSeq[0]].PhysicalZ {
		return types.Vec3{}, nil, false
	}
	for i := 1; i < len(frontSeq); i++ {
		if surfaces[frontSeq[i-1]].PhysicalZ <= surfaces[frontSeq[i]].PhysicalZ {
			return types.Vec3{}, nil, false
		}
	}

	return stopSurf.LocalToGlobal.MultiplyPoint(ptCoord), frontSeq, true
}

// backwardChiefObjectPoint constructs the through-stop chief ray for a finite
// conjugate (object-height) field by tracing backward from the stop centre and
// matching the emergent object-space line to the object point. Returns the
// object point (chief-ray origin) and the forward direction, or ok=false when
// not applicable.
func backwardChiefObjectPoint(
	engine *ray.Engine,
	surfaces []types.Surface,
	path []int,
	stopID int,
	ptCoord types.Vec3,
	height, dx, dy, objectZ, wavelength float64,
) (types.Vec3, types.Vec3, bool) {
	stopCenter, frontSeq, ok := backwardFrontSetup(surfaces, stopID, ptCoord)
	if !ok {
		return types.Vec3{}, types.Vec3{}, false
	}

	objectPoint := types.Vec3{X: height * dx, Y: height * dy, Z: objectZ}

	uY, ok := searchBackwardPosition(engine, surfaces, frontSeq, stopCenter,
		objectPoint.Y, objectZ, false, wavelength)
	if !ok {
		return types.Vec3{}, types.Vec3{}, false
	}
	uX := 0.0
	if math.Abs(objectPoint.X) > 1e-9 {
		var okX bool
		if uX, okX = searchBackwardPosition(engine, surfaces, frontSeq, stopCenter,
			objectPoint.X, objectZ, true, wavelength); !okX {
			return types.Vec3{}, types.Vec3{}, false
		}
	}

	bwd := types.Vec3{X: uX, Y: uY, Z: -1}.Normalize()
	_, emDir, ok := engine.TraceBackward(surfaces, frontSeq, stopCenter, bwd, wavelength)
	if !ok {
		return types.Vec3{}, types.Vec3{}, false
	}
	fwdDir := emDir.Scale(-1)
	if fwdDir.Z <= 1e-12 {
		return types.Vec3{}, types.Vec3{}, false
	}

	// Validate the forward chief ray from the object point passes through the
	// stop centre (within a micron). A failure after the stop is acceptable only
	// at the image surface; a mid-lens failure means the construction is
	// unreliable, so fall back.
	probe := types.Ray{
		Wavelength: wavelength,
		Initial:    types.RayState{Origin: objectPoint, Direction: fwdDir},
		Path:       path,
		Jones:      types.NewCircularJones(true),
	}
	pres := engine.TraceRay(probe, surfaces)
	reachedStop := false
	for i := range pres.Surfaces {
		if pres.Surfaces[i].SurfaceID == stopID {
			hit := pres.Surfaces[i].Position
			if math.Hypot(hit.X-stopCenter.X, hit.Y-stopCenter.Y) > 1e-3 {
				return types.Vec3{}, types.Vec3{}, false
			}
			reachedStop = true
			break
		}
	}
	if !reachedStop {
		return types.Vec3{}, types.Vec3{}, false
	}
	if pres.Error != "" && len(path) >= 2 {
		lastReached := -1
		if len(pres.Surfaces) > 0 {
			lastReached = pres.Surfaces[len(pres.Surfaces)-1].SurfaceID
		}
		if lastReached != path[len(path)-2] {
			return types.Vec3{}, types.Vec3{}, false
		}
	}
	return objectPoint, fwdDir, true
}

// searchBackwardPosition finds the tangent u of the backward ray at the stop
// (direction (0,u,-1) or (u,0,-1)) such that the emergent object-space line
// passes through the target position (height) at the object plane objectZ.
// Returns (u, ok); ok=false when no such direction exists.
func searchBackwardPosition(
	engine *ray.Engine,
	surfaces []types.Surface,
	frontSeq []int,
	stopCenter types.Vec3,
	targetVal, objectZ float64,
	isX bool,
	wavelength float64,
) (float64, bool) {
	posFn := func(u float64) (float64, bool) {
		dir := types.Vec3{X: 0, Y: 0, Z: -1}
		if isX {
			dir.X = u
		} else {
			dir.Y = u
		}
		emPos, emDir, ok := engine.TraceBackward(surfaces, frontSeq, stopCenter, dir, wavelength)
		if !ok || math.Abs(emDir.Z) < 1e-12 {
			return 0, false
		}
		if isX {
			return emPos.X + (objectZ-emPos.Z)/emDir.Z*emDir.X, true
		}
		return emPos.Y + (objectZ-emPos.Z)/emDir.Z*emDir.Y, true
	}

	if math.Abs(targetVal) < 1e-12 {
		return 0, true
	}

	// u = 0 is the on-axis backward ray, which must emerge on the axis.
	v0, ok := posFn(0)
	if !ok {
		return 0, false
	}
	if math.Abs(v0-targetVal) < 1e-9 {
		return 0, true
	}

	// Positive u gives positive position for an on-axis lens; search in the
	// sign direction of the target.
	sign := 1.0
	if targetVal < 0 {
		sign = -1.0
	}

	lo, loVal := 0.0, v0
	step := 0.002
	for mag := step; mag <= 10.0; {
		u := sign * mag
		v, ok := posFn(u)
		if ok {
			if (v-targetVal)*(loVal-targetVal) <= 0 {
				hi := u
				for iter := 0; iter < 60; iter++ {
					mid := (lo + hi) / 2
					mv, mok := posFn(mid)
					if !mok {
						hi = mid
						continue
					}
					if math.Abs(mv-targetVal) < 1e-9 {
						return mid, true
					}
					if (mv-targetVal)*(loVal-targetVal) >= 0 {
						lo, loVal = mid, mv
					} else {
						hi = mid
					}
				}
				return (lo + hi) / 2, true
			}
			lo, loVal = u, v
		}
		mag += step
		step *= 1.5
	}
	return 0, false
}

// searchBackwardTangent finds the tangent u of the backward ray at the stop
// (direction (0,u,-1) or (u,0,-1)) such that the emergent object-space angle in
// that plane equals targetAngle. Returns (u, ok); ok=false when no such
// direction exists (the field angle is beyond the reachable range).
func searchBackwardTangent(
	engine *ray.Engine,
	surfaces []types.Surface,
	frontSeq []int,
	stopCenter types.Vec3,
	targetAngle float64,
	isX bool,
	wavelength float64,
) (float64, bool) {
	angleFn := func(u float64) (float64, bool) {
		dir := types.Vec3{X: 0, Y: 0, Z: -1}
		if isX {
			dir.X = u
		} else {
			dir.Y = u
		}
		_, emDir, ok := engine.TraceBackward(surfaces, frontSeq, stopCenter, dir, wavelength)
		if !ok {
			return 0, false
		}
		if isX {
			return math.Atan2(math.Abs(emDir.X), math.Abs(emDir.Z)), true
		}
		return math.Atan2(math.Abs(emDir.Y), math.Abs(emDir.Z)), true
	}

	if targetAngle < 1e-12 {
		return 0, true
	}

	// u = 0 is the on-axis backward ray, which must emerge on-axis.
	if a, ok := angleFn(0); !ok || a >= targetAngle {
		return 0, ok
	}

	lo := 0.0
	step := 0.002
	for u := step; u <= 10.0; {
		ang, ok := angleFn(u)
		if ok {
			if ang >= targetAngle {
				hi := u
				for iter := 0; iter < 60; iter++ {
					mid := (lo + hi) / 2
					mang, mok := angleFn(mid)
					if !mok {
						hi = mid
						continue
					}
					if math.Abs(mang-targetAngle) < 1e-9 {
						return mid, true
					}
					if mang < targetAngle {
						lo = mid
					} else {
						hi = mid
					}
				}
				return (lo + hi) / 2, true
			}
			lo = u
		}
		u += step
		step *= 1.5
	}
	return 0, false
}

func computeChiefRayAngleGridWithPassThrough(
	system types.System,
	engine *ray.Engine,
	path []int,
	thetaRad, dx, dy float64,
	refSurfaceID int,
	numRays int,
	apertureRadius float64,
	pol types.JonesVector,
	wavelength float64,
	dumpMap bool,
	gridType types.GridType,
	pt *types.PassThroughTarget,
	pupilZ float64,
) Result {
	zStart := -100.0

	ptCoord := pt.Coordinate

	// Preferred: construct the chief ray by backward tracing from the stop,
	// matching the object-space field angle. This avoids the forward origin
	// search, which can overshoot the narrow valid origin window of a small
	// lens. Fall back to the origin search when not applicable.
	var origin types.Vec3
	var rayDir types.Vec3
	if o, d, ok := backwardChiefOrigin(engine, system.Surfaces, path, pt.Surface,
		ptCoord, thetaRad, dx, dy, zStart, wavelength); ok {
		origin = o
		rayDir = d
	} else {
		sinT := math.Sin(thetaRad)
		cosT := math.Cos(thetaRad)
		rayDir = types.Vec3{
			X: sinT * dx,
			Y: sinT * dy,
			Z: cosT,
		}.Normalize()

		originY := searchOriginForTarget(rayDir.Y, rayDir, zStart, pt.Surface, ptCoord.Y,
			path, wavelength, pol, engine, system.Surfaces, pupilZ, false,
			func(sr types.SurfaceResult) float64 { return sr.Position.Y })

		originX := 0.0
		if math.Abs(rayDir.X) > 1e-12 || math.Abs(ptCoord.X) > 1e-12 {
			originX = searchOriginForTarget(rayDir.X, rayDir, zStart, pt.Surface, ptCoord.X,
				path, wavelength, pol, engine, system.Surfaces, pupilZ, true,
				func(sr types.SurfaceResult) float64 { return sr.Position.X })
		}
		origin = types.Vec3{X: originX, Y: originY, Z: zStart}
	}

	t := (pupilZ - origin.Z) / rayDir.Z
	pupilCenterX := origin.X + t*rayDir.X
	pupilCenterY := origin.Y + t*rayDir.Y

	cx, cy, grid := tracePupilGrid(system, engine, path, numRays, apertureRadius,
		pupilCenterX, pupilCenterY, zStart, rayDir, types.Vec3{},
		refSurfaceID, pol, wavelength, dumpMap, gridType)

	return buildResult(engine, system, path, origin, rayDir, refSurfaceID,
		pol, wavelength, cx, cy, apertureRadius, grid, dumpMap)
}

// --- pass-through constrained chief ray (height-based) ---

func computeChiefRayHeightGridWithPassThrough(
	system types.System,
	engine *ray.Engine,
	path []int,
	height, dx, dy, objectZ float64,
	refSurfaceID int,
	numRays int,
	apertureRadius float64,
	pol types.JonesVector,
	wavelength float64,
	dumpMap bool,
	gridType types.GridType,
	pt *types.PassThroughTarget,
) Result {
	objectPoint := types.Vec3{X: height * dx, Y: height * dy, Z: objectZ}
	zStart := objectZ + (0.0-objectZ)*0.5
	ptCoord := pt.Coordinate

	// Preferred: construct the chief ray by backward tracing from the stop,
	// matching the emergent object-space line to the object point. Fall back to
	// the forward direction search when not applicable.
	refinedDir := types.Vec3{}
	if op, dir, ok := backwardChiefObjectPoint(engine, system.Surfaces, path,
		pt.Surface, ptCoord, height, dx, dy, objectZ, wavelength); ok {
		objectPoint = op
		refinedDir = dir
	} else {
		targetZ := computeTargetZ(system.Surfaces, pt.Surface)
		baseDir := types.Vec3{
			X: ptCoord.X - objectPoint.X,
			Y: ptCoord.Y - objectPoint.Y,
			Z: targetZ - objectPoint.Z,
		}.Normalize()

		refinedDir = searchDirectionForTarget(objectPoint, pt.Surface, ptCoord.Y, baseDir,
			path, wavelength, pol, engine, system.Surfaces,
			func(sr types.SurfaceResult) float64 { return sr.Position.Y })

		if math.Abs(ptCoord.X) > 1e-12 {
			refinedDir = searchDirectionForTarget(objectPoint, pt.Surface, ptCoord.X, refinedDir,
				path, wavelength, pol, engine, system.Surfaces,
				func(sr types.SurfaceResult) float64 { return sr.Position.X })
		}
	}

	cx, cy, grid := tracePupilGrid(system, engine, path, numRays, apertureRadius,
		0, 0, zStart, refinedDir, objectPoint,
		refSurfaceID, pol, wavelength, dumpMap, gridType)

	return buildResult(engine, system, path, objectPoint, refinedDir, refSurfaceID,
		pol, wavelength, cx, cy, apertureRadius, grid, dumpMap)
}

// --- height-based (finite conjugate) ---

func computeChiefRayHeight(
	system types.System,
	engine *ray.Engine,
	height, dx, dy, objectZ float64,
	refSurfaceID int,
	numRays int,
	apertureRadius float64,
	pol types.JonesVector,
	wavelength float64,
	dumpMap bool,
) Result {
	return computeChiefRayHeightGrid(system, engine, dls.BuildPath(system.Surfaces), height, dx, dy, objectZ, refSurfaceID, numRays, apertureRadius, pol, wavelength, dumpMap, types.GridPolar)
}

func computeChiefRayHeightGrid(
	system types.System,
	engine *ray.Engine,
	path []int,
	height, dx, dy, objectZ float64,
	refSurfaceID int,
	numRays int,
	apertureRadius float64,
	pol types.JonesVector,
	wavelength float64,
	dumpMap bool,
	gridType types.GridType,
) Result {
	objectPoint := types.Vec3{X: height * dx, Y: height * dy, Z: objectZ}

	// Estimate pupil plane between object and first surface.
	zStart := objectZ + (0.0-objectZ)*0.5

	// Initial direction estimate: from object toward the optical axis at the pupil plane.
	baseDir := types.Vec3{
		X: -objectPoint.X,
		Y: -objectPoint.Y,
		Z: zStart - objectPoint.Z,
	}.Normalize()

	// Sample grid and get centroid.
	cx, cy, grid := tracePupilGrid(system, engine, path, numRays, apertureRadius,
		0, 0, zStart, baseDir, objectPoint,
		refSurfaceID, pol, wavelength, dumpMap, gridType)

	// Refine the direction so the chief ray passes through the centroid at reference surface.
	refinedDir := searchDirectionForTarget(objectPoint, refSurfaceID, cy, baseDir,
		path, wavelength, pol, engine, system.Surfaces,
		func(sr types.SurfaceResult) float64 { return sr.Position.Y })

	return buildResult(engine, system, path, objectPoint, refinedDir, refSurfaceID,
		pol, wavelength, cx, cy, apertureRadius, grid, dumpMap)
}

// --- binary search helpers ---

// searchOriginForTarget finds the initial origin component (X or Y) on the
// zStart plane such that the ray passes through targetVal at the target
// surface after refraction.
func searchOriginForTarget(
	dirComp float64,
	rayDir types.Vec3,
	zStart float64, targetID int, targetVal float64, path []int,
	wavelength float64, pol types.JonesVector,
	engine *ray.Engine, surfaces []types.Surface,
	pupilZ float64,
	isX bool, getPos func(types.SurfaceResult) float64,
) float64 {
	if math.Abs(dirComp) < 1e-12 {
		return 0
	}

	tanComp := dirComp / math.Sqrt(1-dirComp*dirComp)
	geoEst := -(pupilZ - zStart) * tanComp

	makeRay := func(originComp float64) types.Ray {
		orig := types.Vec3{X: 0, Y: 0, Z: zStart}
		if isX {
			orig.X = originComp
		} else {
			orig.Y = originComp
		}
		return types.Ray{
			Wavelength: wavelength,
			Initial:    types.RayState{Origin: orig, Direction: rayDir},
			Path:       path,
			Jones:      pol,
		}
	}

	trace := func(originComp float64) (float64, bool, bool) {
		r := makeRay(originComp)
		res := engine.TraceRay(r, surfaces)
		for _, sr := range res.Surfaces {
			if sr.SurfaceID == targetID {
				return getPos(sr), true, false
			}
		}
		if res.Error != "" {
			return math.Inf(1), true, true
		}
		return 0, false, false
	}

	vEst, okEst, clipEst := trace(geoEst)
	if okEst && !clipEst && math.Abs(vEst-targetVal) < 1e-12 {
		return geoEst
	}

	// Find a safe anchor: any origin value where trace succeeds.
	// Try geoEst first; if that clips, walk outward in both directions.
	anchor := geoEst
	var vAnc float64
	found := false

	if okEst && !clipEst {
		vAnc = vEst
		found = true
	} else {
		step := math.Max(math.Abs(geoEst)*0.05, 0.5)
		for s := step; s < 200; s += step {
			if v, ok, clipped := trace(geoEst + s); ok && !clipped {
				anchor = geoEst + s
				vAnc = v
				found = true
				break
			}
			if v, ok, clipped := trace(geoEst - s); ok && !clipped {
				anchor = geoEst - s
				vAnc = v
				found = true
				break
			}
		}
	}

	if !found {
		return geoEst
	}

	if math.Abs(vAnc-targetVal) < 1e-12 {
		return anchor
	}

	sign := 1.0
	if targetVal < vAnc {
		sign = -1.0
	}

	maxV := math.Max(math.Abs(anchor)*2, 20.0)
	if maxV < 5 {
		maxV = 5
	}
	step := maxV / 200
	if step < 0.01 {
		step = 0.01
	}

	v := anchor
	currentVal := vAnc

	for iter := 0; iter < 400; iter++ {
		prevV := v
		prevVal := currentVal

		v += sign * step
		if math.Abs(v) > maxV {
			break
		}

		val, ok, clipped := trace(v)
		if !ok {
			break
		}
		currentVal = val

		if clipped || (currentVal-targetVal)*(prevVal-targetVal) <= 0 {
			return binarySearchOrigin(math.Min(prevV, v), math.Max(prevV, v),
				targetVal, prevVal, currentVal, trace)
		}
	}

	return geoEst
}

func binarySearchOrigin(lo, hi, targetVal, valLo, valHi float64, trace func(float64) (float64, bool, bool)) float64 {
	for iter := 0; iter < 60; iter++ {
		mid := (lo + hi) / 2
		midVal, ok, _ := trace(mid)
		if !ok {
			return mid
		}
		if math.IsInf(midVal, 1) {
			if math.IsInf(valLo, 1) {
				lo = mid
			} else {
				hi = mid
			}
			continue
		}
		if math.Abs(midVal-targetVal) < 1e-12 {
			return mid
		}
		if (midVal-targetVal)*(valLo-targetVal) >= 0 {
			lo = mid
			valLo = midVal
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}

// searchDirectionForTarget varies the tangent component of the ray direction
// so that the ray passes through targetVal at the target surface.
func searchDirectionForTarget(
	objectPoint types.Vec3, targetID int, targetVal float64,
	baseDir types.Vec3, path []int,
	wavelength float64, pol types.JonesVector,
	engine *ray.Engine, surfaces []types.Surface,
	getPos func(types.SurfaceResult) float64,
) types.Vec3 {
	tanComp := baseDir.Y / baseDir.Z
	useX := math.Abs(baseDir.X) > math.Abs(baseDir.Y)
	if useX {
		tanComp = baseDir.X / baseDir.Z
	} else if math.Abs(baseDir.Y) < 1e-12 {
		return baseDir
	}

	trace := func(tan float64) (float64, bool, bool) {
		dz := 1.0 / math.Sqrt(1+tan*tan)
		dx, dy := 0.0, 0.0
		if useX {
			dx = tan * dz
		} else {
			dy = tan * dz
		}
		dir := types.Vec3{X: dx, Y: dy, Z: dz}.Normalize()
		r := types.Ray{
			Wavelength: wavelength,
			Initial:    types.RayState{Origin: objectPoint, Direction: dir},
			Path:       path,
			Jones:      pol,
		}
		res := engine.TraceRay(r, surfaces)
		for _, sr := range res.Surfaces {
			if sr.SurfaceID == targetID {
				return getPos(sr), true, false
			}
		}
		if res.Error != "" {
			return math.Inf(1), true, true
		}
		return 0, false, false
	}

	v0, ok0, clip0 := trace(tanComp)
	if ok0 && !clip0 && math.Abs(v0-targetVal) < 1e-12 {
		return baseDir
	}

	// Anchor: trace with tan=0 (direction parallel to axis).
	anchor := 0.0
	vAnc, okAnc, clipAnc := trace(anchor)
	if !okAnc || clipAnc {
		return baseDir
	}
	if math.Abs(vAnc-targetVal) < 1e-12 {
		dz := 1.0
		dir := types.Vec3{X: 0, Y: 0, Z: dz}.Normalize()
		return dir
	}

	sign := 1.0
	if targetVal < vAnc {
		sign = -1.0
	}

	maxV := math.Max(math.Abs(tanComp)*2, 0.5)
	if maxV < 0.5 {
		maxV = 0.5
	}
	step := maxV / 200
	if step < 0.0001 {
		step = 0.0001
	}

	v := anchor
	currentVal := vAnc

	for iter := 0; iter < 400; iter++ {
		prevV := v
		prevVal := currentVal

		v += sign * step
		if math.Abs(v) > maxV {
			break
		}

		val, ok, clipped := trace(v)
		if !ok {
			break
		}
		currentVal = val

		if clipped || (currentVal-targetVal)*(prevVal-targetVal) <= 0 {
			lo, hi := math.Min(prevV, v), math.Max(prevV, v)
			bestTan := binarySearchOrigin(lo, hi, targetVal, prevVal, currentVal, trace)

			dz := 1.0 / math.Sqrt(1+bestTan*bestTan)
			dxBest, dyBest := 0.0, 0.0
			if useX {
				dxBest = bestTan * dz
			} else {
				dyBest = bestTan * dz
			}
			return types.Vec3{X: dxBest, Y: dyBest, Z: dz}.Normalize()
		}
	}

	return baseDir
}

// --- shared helpers ---

// generateGridPoints returns a list of pupil (px, py) sample coordinates
// for the given grid type and aperture radius.
func GenerateGridPoints(numRays int, apertureRadius float64, gridType types.GridType) []struct{ X, Y float64 } {
	var pts []struct{ X, Y float64 }

	switch gridType {
	case types.GridSquare:
		n := int(math.Sqrt(float64(numRays)))
		if n < 2 {
			n = 2
		}
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				x := (float64(i)+0.5)/float64(n)*2 - 1
				y := (float64(j)+0.5)/float64(n)*2 - 1
				if x*x+y*y <= 1 {
					pts = append(pts, struct{ X, Y float64 }{
						X: x * apertureRadius,
						Y: y * apertureRadius,
					})
				}
			}
		}

	case types.GridHex:
		n := int(math.Sqrt(float64(numRays))) + 1
		if n < 2 {
			n = 2
		}
		dy := apertureRadius * 2 / float64(n)
		dx := dy * math.Sqrt(3) / 2
		for i := 0; i < n; i++ {
			y := -apertureRadius + (float64(i)+0.5)*dy
			xOff := 0.0
			if i%2 == 1 {
				xOff = dx / 2
			}
			nx := int(float64(n) * apertureRadius / (dx * float64(n) / 2))
			if nx < 1 {
				nx = 1
			}
			for j := 0; j < nx; j++ {
				x := -apertureRadius + (float64(j)+0.5)*dx + xOff
				if x*x+y*y <= apertureRadius*apertureRadius {
					pts = append(pts, struct{ X, Y float64 }{
						X: x,
						Y: y,
					})
				}
			}
		}

	default: // GridPolar
		n := int(math.Sqrt(float64(numRays)))
		if n < 2 {
			n = 2
		}
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				r := (float64(i) + 0.5) / float64(n) * apertureRadius
				theta := 2 * math.Pi * (float64(j) + 0.5) / float64(n)
				pts = append(pts, struct{ X, Y float64 }{
					X: r * math.Cos(theta),
					Y: r * math.Sin(theta),
				})
			}
		}
	}

	return pts
}

// tracePupilGrid distributes numRays samples in the given grid pattern
// centred on (pupilCenterX, pupilCenterY) at plane zStart.
func tracePupilGrid(
	system types.System,
	engine *ray.Engine,
	path []int,
	numRays int,
	apertureRadius float64,
	pupilCenterX, pupilCenterY, zStart float64,
	rayDir, rayOrigin types.Vec3,
	refSurfaceID int,
	pol types.JonesVector,
	wavelength float64,
	dumpMap bool,
	gridType types.GridType,
) (cx, cy float64, grid []types.GridPoint) {
	var totalWeight float64
	var weightedX, weightedY float64

	isHeightBased := rayOrigin.Z != 0 || rayOrigin.X != 0 || rayOrigin.Y != 0

	// For a parallel angle-field bundle the wavefront is perpendicular to
	// rayDir. Projecting each launch origin onto the wavefront plane through
	// the grid centre removes the launch-geometry OPL tilt (the linear ramp
	// that otherwise contaminates off-axis OPD and shifts the Huygens PSF
	// peak). The projection is along rayDir so the ray line is unchanged.
	wavefrontC := types.Vec3{X: pupilCenterX, Y: pupilCenterY, Z: zStart}

	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.NumCPU())

	samples := GenerateGridPoints(numRays, apertureRadius, gridType)
	for i := range samples {
		px := pupilCenterX + samples[i].X
		py := pupilCenterY + samples[i].Y

		wg.Add(1)
		sem <- struct{}{}
		go func(px, py float64) {
			defer wg.Done()
			defer func() { <-sem }()

			var rDir types.Vec3
			var rOrg types.Vec3
			if isHeightBased {
				rOrg = rayOrigin
				rDir = types.Vec3{
					X: px - rayOrigin.X,
					Y: py - rayOrigin.Y,
					Z: zStart - rayOrigin.Z,
				}.Normalize()
			} else {
				rOrg = raymath.ProjectOntoWavefront(
					types.Vec3{X: px, Y: py, Z: zStart}, wavefrontC, rayDir)
				rDir = rayDir
			}

			ray := types.Ray{
				Wavelength: wavelength,
				Initial:    types.RayState{Origin: rOrg, Direction: rDir},
				Path:       path,
				Jones:      pol,
			}

			traceResult := engine.TraceRay(ray, system.Surfaces)
			if traceResult.Error != "" {
				mu.Lock()
				grid = append(grid, types.GridPoint{
					PupilX: px, PupilY: py,
					ImageX: nil, ImageY: nil, Intensity: 0,
					ErrorCode: traceResult.ErrorCode,
					Origin:    rOrg, Direction: rDir,
				})
				mu.Unlock()
				return
			}

			for _, sr := range traceResult.Surfaces {
				if sr.SurfaceID == refSurfaceID {
					weight := (sr.IntensityS + sr.IntensityP) / 2.0

					mu.Lock()
					totalWeight += weight
					weightedX += sr.Position.X * weight
					weightedY += sr.Position.Y * weight
					ix, iy := sr.Position.X, sr.Position.Y
					grid = append(grid, types.GridPoint{
						PupilX: px, PupilY: py,
						ImageX: &ix, ImageY: &iy, Intensity: weight,
						OPL:    traceResult.OPLTotal,
						Origin: rOrg, Direction: rDir,
					})
					mu.Unlock()
				}
			}
		}(px, py)
	}

	wg.Wait()
	close(sem)

	if totalWeight > 0 {
		cx = weightedX / totalWeight
		cy = weightedY / totalWeight
	}
	return
}

func computeSpotStats(grid []types.GridPoint, cx, cy float64) *types.SpotStats {
	s := &types.SpotStats{
		Centroid: types.Vec3{X: cx, Y: cy},
		MinX:     1e18,
		MinY:     1e18,
		MaxX:     -1e18,
		MaxY:     -1e18,
	}
	var sumSqX, sumSqY float64

	for _, gp := range grid {
		s.TotalRays++
		if gp.ImageX == nil || gp.ImageY == nil {
			s.MissedRays++
			continue
		}
		s.TracedRays++
		dx := *gp.ImageX - cx
		dy := *gp.ImageY - cy
		sumSqX += dx * dx
		sumSqY += dy * dy

		if *gp.ImageX < s.MinX {
			s.MinX = *gp.ImageX
		}
		if *gp.ImageX > s.MaxX {
			s.MaxX = *gp.ImageX
		}
		if *gp.ImageY < s.MinY {
			s.MinY = *gp.ImageY
		}
		if *gp.ImageY > s.MaxY {
			s.MaxY = *gp.ImageY
		}
	}

	if s.TracedRays > 0 {
		n := float64(s.TracedRays)
		s.RMS_X = math.Sqrt(sumSqX / n)
		s.RMS_Y = math.Sqrt(sumSqY / n)
		s.RMS_R = math.Sqrt((sumSqX + sumSqY) / n)
	}
	return s
}

func computeWavelengthStats(
	engine *ray.Engine,
	system types.System,
	path []int,
	refSurfaceID int,
	pol types.JonesVector,
	grid []types.GridPoint,
	chiefImgHeight types.Vec3,
	wavelengths []float64,
) []types.WavelengthStats {
	type wlResult struct {
		index int
		stats types.SpotStats
	}
	ch := make(chan wlResult, len(wavelengths))
	var wg sync.WaitGroup

	for i, wl := range wavelengths {
		wg.Add(1)
		go func(i int, wl float64) {
			defer wg.Done()
			var cx, cy float64
			var sumW, wcx, wcy float64
			var traced []types.GridPoint

			for _, gp := range grid {
				r := types.Ray{
					Wavelength: wl,
					Initial:    types.RayState{Origin: gp.Origin, Direction: gp.Direction},
					Path:       path,
					Jones:      pol,
				}
				tr := engine.TraceRay(r, system.Surfaces)
				if tr.Error != "" {
					traced = append(traced, types.GridPoint{
						PupilX: gp.PupilX, PupilY: gp.PupilY,
						ImageX: nil, ImageY: nil, Intensity: 0,
					})
					continue
				}
				for _, sr := range tr.Surfaces {
					if sr.SurfaceID == refSurfaceID {
						weight := (sr.IntensityS + sr.IntensityP) / 2.0
						sumW += weight
						wcx += sr.Position.X * weight
						wcy += sr.Position.Y * weight
						ix, iy := sr.Position.X, sr.Position.Y
						traced = append(traced, types.GridPoint{
							PupilX: gp.PupilX, PupilY: gp.PupilY,
							ImageX: &ix, ImageY: &iy, Intensity: weight,
						})
						break
					}
				}
			}
			cx, cy = chiefImgHeight.X, chiefImgHeight.Y
			if sumW > 0 {
				cx = wcx / sumW
				cy = wcy / sumW
			}
			ss := computeSpotStats(traced, cx, cy)
			ch <- wlResult{i, *ss}
		}(i, wl)
	}
	wg.Wait()
	close(ch)

	stats := make([]types.WavelengthStats, len(wavelengths))
	for r := range ch {
		stats[r.index] = types.WavelengthStats{Value: wavelengths[r.index], SpotStats: r.stats}
	}
	return stats
}

func buildResult(
	engine *ray.Engine, system types.System,
	path []int,
	origin, rayDir types.Vec3,
	refSurfaceID int,
	pol types.JonesVector,
	wavelength float64,
	cx, cy, apertureRadius float64,
	grid []types.GridPoint,
	dumpMap bool,
) Result {
	chiefRay := types.Ray{
		Wavelength: wavelength,
		Initial:    types.RayState{Origin: origin, Direction: rayDir},
		Path:       path,
		Jones:      pol,
		Lenient:    true,
	}

	// Trace chief ray for actual image height
	if tr := engine.TraceRay(chiefRay, system.Surfaces); tr.Error == "" {
		for _, sr := range tr.Surfaces {
			if sr.SurfaceID == refSurfaceID {
				cx = sr.Position.X
				cy = sr.Position.Y
			}
		}
	}

	res := Result{
		ImageHeight:   types.Vec3{X: cx, Y: cy},
		EntrancePupil: &types.Pupil{Radius: apertureRadius},
		ChiefRay:      chiefRay,
		GridPoints:    grid,
	}
	if len(grid) > 0 {
		res.SpotStats = computeSpotStats(grid, cx, cy)
	}
	return res
}

func computeRayFan(
	engine *ray.Engine, system types.System,
	path []int,
	origin, rayDir types.Vec3,
	refSurfaceID int,
	pol types.JonesVector,
	wavelength float64,
	apertureRadius float64,
	pupilZ float64,
	angles []float64,
	numFan int,
) *types.RayFan {
	if numFan <= 0 {
		numFan = 256
	}

	zStart := origin.Z

	// Fan rays sample the entrance pupil: scan positions are offset from the
	// pupil center, which is the point at zStart where a ray parallel to the
	// chief ray crosses the stop. Without this offset, off-axis fields would
	// start fan rays near the axis and miss the lens aperture entirely.
	rayDirN := rayDir.Normalize()
	pupilCenterX := -(pupilZ - zStart) * rayDirN.X / rayDirN.Z
	pupilCenterY := -(pupilZ - zStart) * rayDirN.Y / rayDirN.Z

	// Chief ray image point
	chiefRay := types.Ray{
		Wavelength: wavelength,
		Initial:    types.RayState{Origin: origin, Direction: rayDir},
		Path:       path,
		Jones:      pol,
		Lenient:    true,
	}
	var chiefX, chiefY float64
	if tr := engine.TraceRay(chiefRay, system.Surfaces); tr.Error == "" {
		for _, sr := range tr.Surfaces {
			if sr.SurfaceID == refSurfaceID {
				chiefX = sr.Position.X
				chiefY = sr.Position.Y
			}
		}
	}

	traceOne := func(px, py, cosA, sinA float64) (types.FanPoint, bool) {
		rDir := rayDir
		rOrg := raymath.ProjectOntoWavefront(
			types.Vec3{X: pupilCenterX + px, Y: pupilCenterY + py, Z: zStart},
			types.Vec3{X: pupilCenterX, Y: pupilCenterY, Z: zStart}, rayDir)
		r := types.Ray{
			Wavelength: wavelength,
			Initial:    types.RayState{Origin: rOrg, Direction: rDir},
			Path:       path,
			Jones:      pol,
			Lenient:    true,
		}
		tr := engine.TraceRay(r, system.Surfaces)
		if tr.Error != "" {
			return types.FanPoint{}, false
		}
		fp := types.FanPoint{
			PupilX: px,
			PupilY: py,
		}
		for _, sr := range tr.Surfaces {
			if sr.SurfaceID == refSurfaceID {
				fp.EX = sr.Position.X - chiefX
				fp.EY = sr.Position.Y - chiefY
				fp.Long = longitudinalAberration(sr, cosA, sinA)
			}
		}
		fp.Path = tr.Surfaces
		return fp, true
	}

	fan := &types.RayFan{}

	type fanResult struct {
		points       []types.FanPoint
		isMeridional bool
		isSagittal   bool
	}
	results := make([]fanResult, len(angles))
	var wg sync.WaitGroup
	workers := runtime.NumCPU()
	if workers > len(angles) {
		workers = len(angles)
	}
	if workers < 1 {
		workers = 1
	}
	jobCh := make(chan int)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ai := range jobCh {
				angleDeg := angles[ai]
				rad := raymath.DegToRad(angleDeg)
				cosA := math.Cos(rad)
				sinA := math.Sin(rad)

				points := make([]types.FanPoint, 0, numFan)
				for k := 0; k < numFan; k++ {
					t := -apertureRadius + (float64(k)+0.5)*2.0*apertureRadius/float64(numFan)
					px := t * cosA
					py := t * sinA
					fp, ok := traceOne(px, py, cosA, sinA)
					if !ok {
						continue
					}
					points = append(points, fp)
				}
				results[ai] = fanResult{
					points:       points,
					isMeridional: math.Abs(angleDeg-90) < 1e-9,
					isSagittal:   math.Abs(angleDeg) < 1e-9,
				}
			}
		}()
	}
	for i := range angles {
		jobCh <- i
	}
	close(jobCh)
	wg.Wait()

	for i, res := range results {
		switch {
		case res.isMeridional:
			fan.Meridional = res.points
		case res.isSagittal:
			fan.Sagittal = res.points
		default:
			fan.Rotated = append(fan.Rotated, types.RotatedFan{
				AngleDeg: angles[i],
				Points:   res.points,
			})
		}
	}

	if len(fan.Meridional) == 0 && len(fan.Sagittal) == 0 && len(fan.Rotated) == 0 {
		return nil
	}
	return fan
}

// longitudinalAberration returns the axial distance from the reference
// surface (sr.Position.Z) to where the ray's lateral offset along the fan
// scan axis (cosA, sinA) becomes zero. For a meridional fan (cosA=0, sinA=1)
// this is the Z-axis crossing in Y; for a sagittal fan (cosA=1, sinA=0) the
// crossing in X. A positive value means the ray focuses beyond the reference
// surface.
func longitudinalAberration(sr types.SurfaceResult, cosA, sinA float64) float64 {
	u := sr.Position.X*cosA + sr.Position.Y*sinA
	du := sr.Direction.X*cosA + sr.Direction.Y*sinA
	if math.Abs(du) < 1e-12 {
		return 0
	}
	t := -u / du
	return (sr.Position.Z + t*sr.Direction.Z) - sr.Position.Z
}

func computeStopZ(surfaces []types.Surface, stopID int) float64 {
	return surface.ComputeStopZ(surfaces, stopID)
}

func computeTargetZ(surfaces []types.Surface, targetID int) float64 {
	for _, s := range surfaces {
		if s.ID == targetID {
			return s.PhysicalZ
		}
	}
	return 0
}

// imageHeightForAngle returns the projected centroid (dx*cx + dy*cy) at the
// reference surface for a given field angle (degrees).  Uses a full grid trace
// with the given numRays.
func imageHeightForAngle(
	system types.System,
	engine *ray.Engine,
	angleDeg, dx, dy float64,
	refSurfaceID int,
	numRays int,
	apertureRadius float64,
	pol types.JonesVector,
	wavelength float64,
	gridType types.GridType,
	pupilZ float64,
) (float64, bool) {
	thetaRad := raymath.DegToRad(angleDeg)
	sinT := math.Sin(thetaRad)
	cosT := math.Cos(thetaRad)
	zStart := -100.0
	rayDir := types.Vec3{X: sinT * dx, Y: sinT * dy, Z: cosT}.Normalize()

	tanT := sinT / cosT
	pupilCenterX := -(pupilZ - zStart) * tanT * dx
	pupilCenterY := -(pupilZ - zStart) * tanT * dy

	cx, cy, grid := tracePupilGrid(system, engine, dls.BuildPath(system.Surfaces), numRays, apertureRadius,
		pupilCenterX, pupilCenterY, zStart, rayDir, types.Vec3{},
		refSurfaceID, pol, wavelength, false, gridType)

	height := cx*dx + cy*dy
	for _, gp := range grid {
		if gp.ImageX != nil {
			return height, true
		}
	}
	return 0, false
}

// searchAngleForImageHeight finds the field angle (degrees) whose projected
// centroid (dx*cx + dy*cy) at the reference surface equals targetY.
func searchAngleForImageHeight(
	system types.System,
	engine *ray.Engine,
	targetY, dx, dy float64,
	refSurfaceID int,
	numRays int,
	apertureRadius float64,
	pol types.JonesVector,
	wavelength float64,
	gridType types.GridType,
	passThrough *types.PassThroughTarget,
	pupilZ float64,
) float64 {
	heightFn := func(angleDeg float64) (float64, bool) {
		if passThrough != nil && passThrough.Surface > 0 {
			y, ok := imageHeightForAnglePT(system, engine, angleDeg, dx, dy, refSurfaceID, pol, wavelength, passThrough, pupilZ)
			return y, ok
		}
		return imageHeightForAngle(system, engine, angleDeg, dx, dy, refSurfaceID, numRays, apertureRadius, pol, wavelength, gridType, pupilZ)
	}

	// Bracket search: start with 0–15° and expand as needed.
	// targetY is a positive magnitude, while the traced image heights are signed
	// (a positive lens forms an inverted image, so they are negative). Compare
	// absolute values so the sign of the image height never defeats the bracket.
	mag := math.Abs
	loDeg, hiDeg := 0.0, 15.0
	yLo, okLo := heightFn(loDeg)
	yHi, okHi := heightFn(hiDeg)
	aLo, aHi := mag(yLo), mag(yHi)

	for iter := 0; iter < 25; iter++ {
		if okLo && okHi && aLo <= targetY && targetY <= aHi {
			break
		}
		if hiDeg > 70 {
			hiDeg = 70
			yHi, okHi = heightFn(hiDeg)
			aHi = mag(yHi)
		}
		if !okLo || aLo > targetY {
			loDeg -= 3.0
			if loDeg < -80 {
				return 0
			}
			yLo, okLo = heightFn(loDeg)
			aLo = mag(yLo)
		} else if !okHi {
			hiDeg = (loDeg + hiDeg) / 2
			if math.Abs(hiDeg-loDeg) < 1e-12 {
				return 0
			}
			yHi, okHi = heightFn(hiDeg)
			aHi = mag(yHi)
		} else if aHi < targetY {
			hiDeg += 3.0
			if hiDeg > 80 {
				return 0
			}
			yHi, okHi = heightFn(hiDeg)
			aHi = mag(yHi)
		} else {
			break
		}
	}

	if !okLo || !okHi || aLo > targetY || aHi < targetY {
		return 0
	}

	for iter := 0; iter < 40; iter++ {
		midDeg := (loDeg + hiDeg) / 2
		yMid, ok := heightFn(midDeg)
		if !ok {
			if math.Abs(midDeg-loDeg) > math.Abs(hiDeg-midDeg) {
				loDeg = midDeg
			} else {
				hiDeg = midDeg
			}
			continue
		}
		aMid := mag(yMid)
		if math.Abs(aMid-targetY) < 1e-12 {
			return midDeg
		}
		if aMid < targetY {
			loDeg = midDeg
		} else {
			hiDeg = midDeg
		}
		if math.Abs(hiDeg-loDeg) < 1e-12 {
			break
		}
	}
	return (loDeg + hiDeg) / 2
}

// imageHeightForAnglePT returns the Y position at the reference surface for a
// ray at the given field angle, using pass_through to ensure the ray passes
// through the stop center.  This is more accurate than the paraxial pupil
// approximation used by imageHeightForAngle when the entrance pupil differs
// from the physical stop.
func imageHeightForAnglePT(
	system types.System,
	engine *ray.Engine,
	angleDeg, dx, dy float64,
	refSurfaceID int,
	pol types.JonesVector,
	wavelength float64,
	pt *types.PassThroughTarget,
	pupilZ float64,
) (float64, bool) {
	thetaRad := raymath.DegToRad(angleDeg)
	zStart := -100.0
	path := dls.BuildPath(system.Surfaces)

	// Prefer the same robust backward-from-stop construction as the actual chief
	// ray; fall back to the forward origin search. This keeps the image-height
	// probe consistent with the chief -- the forward search can fail on steep or
	// folded fields where the backward trace succeeds.
	var origin, rayDir types.Vec3
	if o, d, ok := backwardChiefOrigin(engine, system.Surfaces, path, pt.Surface,
		pt.Coordinate, thetaRad, dx, dy, zStart, wavelength); ok {
		origin, rayDir = o, d
	} else {
		rayDir = types.Vec3{X: math.Sin(thetaRad) * dx, Y: math.Sin(thetaRad) * dy, Z: math.Cos(thetaRad)}.Normalize()

		originY := searchOriginForTarget(rayDir.Y, rayDir, zStart, pt.Surface, pt.Coordinate.Y,
			path, wavelength, pol, engine, system.Surfaces, pupilZ, false,
			func(sr types.SurfaceResult) float64 { return sr.Position.Y })

		originX := 0.0
		if math.Abs(rayDir.X) > 1e-12 || math.Abs(pt.Coordinate.X) > 1e-12 {
			originX = searchOriginForTarget(rayDir.X, rayDir, zStart, pt.Surface, pt.Coordinate.X,
				path, wavelength, pol, engine, system.Surfaces, pupilZ, true,
				func(sr types.SurfaceResult) float64 { return sr.Position.X })
		}

		origin = types.Vec3{X: originX, Y: originY, Z: zStart}
	}

	// Trace Lenient like the actual chief ray: a steep field may graze a surface
	// aperture near the edge of the image circle (the chief tolerates that and
	// still reaches the image), so do not fail the probe on a minor clip.
	ray := types.Ray{
		Wavelength: wavelength,
		Initial:    types.RayState{Origin: origin, Direction: rayDir},
		Path:       path,
		Jones:      pol,
		Lenient:    true,
	}

	traceResult := engine.TraceRay(ray, system.Surfaces)

	for _, sr := range traceResult.Surfaces {
		if sr.SurfaceID == refSurfaceID {
			if math.IsNaN(sr.Position.Y) || math.IsInf(sr.Position.Y, 0) {
				return 0, false
			}
			return sr.Position.Y, true
		}
	}
	return 0, false
}
