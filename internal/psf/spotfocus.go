package psf

import (
	"fmt"
	"math"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/types"
)

// SpotFocusResult is one geometric spot-RMS focus solve's report: the image
// plane shift it prescribes (mm) and the (field, wavelength) sets that could
// not be sampled.
type SpotFocusResult struct {
	ShiftMM       float64
	FieldsUsed    int
	FieldsDropped []string
}

// SpotFocusShift returns the image-plane shift that minimises the weighted sum
// of flux-weighted spot RMS² over the given fields and wavelengths — the
// geometric analogue of the wavefront best-focus shift.
//
// The spot radius squared of one (field, wavelength) is an *exact* quadratic of
// the evaluation plane: every sample propagates linearly to the plane, so its
// transverse position is affine in the plane coordinate z, and the RMS about
// the (z-dependent) centroid is a quadratic. No plane scan is needed — one
// pupil-grid trace per (field, wavelength) and a closed-form minimiser give the
// exact geometric focus, which is why this is far cheaper than the coherent
// peak search of BestFocusShift (no Huygens integral, no golden-section
// refinement, one trace instead of ~20 evaluations).
//
// weights aligns with fields (nil = uniform); a field with weight <= 0 is not
// traced at all. The objective is Σ_f w_f · Σ_λ RMS²_{f,λ}(u), so the
// wavelengths are averaged and each field's own radius is normalised by its
// total flux weight before the field weights combine them.
//
// u is measured from the *current* image plane (the last surface), so the
// returned shift is directly applicable to that plane. A (field, wavelength)
// that cannot be sampled is dropped and reported (partial); an error means
// nothing usable was traced — or the bundle has no defocus sensitivity (an
// afocal / parallel bundle whose radius is plane-independent) — and the caller
// must fall back instead of keeping a stale plane.
func SpotFocusShift(system types.System, gc *glass.Catalog, fields []types.FieldDef,
	wavelengths []float64, weights []float64, opts Options) (SpotFocusResult, error) {
	var out SpotFocusResult
	if len(fields) == 0 {
		return out, fmt.Errorf("spot focus: no field to solve for")
	}
	if len(weights) > 0 && len(weights) != len(fields) {
		return out, fmt.Errorf("spot focus: %d weights for %d fields", len(weights), len(fields))
	}
	if len(wavelengths) == 0 {
		wavelengths = []float64{types.DefaultWavelength}
	}
	if opts.ReferenceSurface <= 0 {
		opts.ReferenceSurface = DefaultReferenceSurface(system.Surfaces)
	}
	if opts.NumRays <= 0 {
		opts.NumRays = DefaultNumRays
	}
	if opts.GridType == "" {
		opts.GridType = types.GridPolar
	}
	total := 0.0
	for i := range fields {
		w := 1.0
		if len(weights) > 0 {
			w = weights[i]
		}
		total += w
	}
	if total <= 0 {
		return out, fmt.Errorf("spot focus: non-positive total field weight")
	}

	// Objective in u (mm from the current image plane): A + B·u + C·u² with
	// B = Σ w·b and C = Σ w·c; the minimiser is u* = -B/(2C).
	planeZ := imagePlaneZ(system.Surfaces)
	engine := ray.NewEngine(gc, nil)
	// The spot geometry is polarization-independent (see ComputeSpot), so a
	// single reference state samples the bundle exactly once.
	pol := types.NewCircularJones(true)
	var bSum, cSum float64
	for fi, fd := range fields {
		w := 1.0
		if len(weights) > 0 {
			w = weights[fi]
		}
		if w <= 0 {
			continue
		}
		used := false
		for _, wl := range wavelengths {
			pg, err := ComputeFieldGrid(system, gc, fd, opts.ReferenceSurface, opts.NumRays, wl, opts.GridType, opts.PupilModel, opts.RayDefinition)
			if err != nil {
				out.FieldsDropped = append(out.FieldsDropped, focusDrop(fi, wl, err))
				continue
			}
			samples, stats := TraceWavefront(system, engine, pg, fd, opts.ReferenceSurface, wl, pol, opts.Workers)
			if stats.Valid < 3 || len(samples) < 3 {
				out.FieldsDropped = append(out.FieldsDropped, focusDrop(fi, wl, fmt.Errorf("only %d valid grid rays", stats.Valid)))
				continue
			}
			b, c, ok := spotFocusCoefficients(samples, planeZ)
			if !ok {
				out.FieldsDropped = append(out.FieldsDropped, focusDrop(fi, wl, fmt.Errorf("only %d usable spot rays", stats.Valid)))
				continue
			}
			bSum += w * b
			cSum += w * c
			used = true
		}
		if used {
			// FieldsUsed counts analysed *fields* (the wavefront solve's
			// convention); the solve itself passes a single wavelength.
			out.FieldsUsed++
		}
	}
	if out.FieldsUsed == 0 {
		if len(out.FieldsDropped) == 0 {
			return out, fmt.Errorf("spot focus: no field could be sampled")
		}
		return out, fmt.Errorf("spot focus: every field failed to sample (%d); first: %s",
			len(out.FieldsDropped), out.FieldsDropped[0])
	}
	if !(cSum > 0) || math.IsNaN(cSum) || math.IsInf(cSum, 0) {
		return out, fmt.Errorf("spot focus: the sampled bundle has no defocus sensitivity (radius is plane-independent)")
	}
	shift := -bSum / (2 * cSum)
	if math.IsNaN(shift) || math.IsInf(shift, 0) {
		return out, fmt.Errorf("spot focus: degenerate spot minimum")
	}
	out.ShiftMM = shift
	return out, nil
}

// focusDrop renders one dropped (field, wavelength) set the way the wavefront
// solve reports it, so both back-focus types read the same on stderr.
func focusDrop(fieldIndex int, wavelength float64, err error) string {
	return fmt.Sprintf("field %d @ %.6f nm: %v", fieldIndex, wavelength*1e6, err)
}

// spotFocusCoefficients fits one (field, wavelength)'s flux-weighted spot
// radius squared as a quadratic of the image-plane displacement u: RMS²(u) =
// a + b·u + c·u². Each sample's transverse position at the plane is affine in
// the plane coordinate, so the coefficients are obtained in closed form from
// the samples' position on the reference surface, their direction and their
// Area·Intensity weight (degrading to Intensity, then to equal weight, exactly
// as spotDecompose does). ok is false when the samples carry no usable spot.
func spotFocusCoefficients(samples []WavefrontSample, planeZ float64) (b, c float64, ok bool) {
	type rayAtPlane struct {
		x, y, sx, sy, w float64
	}
	pts := make([]rayAtPlane, 0, len(samples))
	var sw float64
	for _, s := range samples {
		dz := s.Direction.Z
		if math.Abs(dz) < 1e-9 {
			continue
		}
		w := s.Area * s.Intensity
		if w <= 0 {
			w = s.Intensity
		}
		if w <= 0 {
			w = 1
		}
		t := (planeZ - s.Position.Z) / dz
		pts = append(pts, rayAtPlane{
			x:  s.Position.X + s.Direction.X*t,
			y:  s.Position.Y + s.Direction.Y*t,
			sx: s.Direction.X / dz, // dx/dz: slope of the spot position per mm of plane shift
			sy: s.Direction.Y / dz,
			w:  w,
		})
		sw += w
	}
	if sw <= 0 || len(pts) < 3 {
		return 0, 0, false
	}
	// Centroid (of position and of slope) in the same weighted mean: the spot
	// is measured about its own centroid at every plane, so the centroid's
	// plane dependence has to be subtracted too.
	var cx, cy, csx, csy float64
	for _, p := range pts {
		cx += p.w * p.x
		cy += p.w * p.y
		csx += p.w * p.sx
		csy += p.w * p.sy
	}
	cx /= sw
	cy /= sw
	csx /= sw
	csy /= sw

	var b2, c2 float64
	for _, p := range pts {
		ax := p.x - cx
		ay := p.y - cy
		bx := p.sx - csx
		by := p.sy - csy
		b2 += p.w * (ax*bx + ay*by)
		c2 += p.w * (bx*bx + by*by)
	}
	return 2 * b2 / sw, c2 / sw, true
}
