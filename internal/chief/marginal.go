package chief

import (
	"fmt"
	"math"

	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/raymath"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// MarginalRaysForField extracts the grid rays at the extreme pupil-plane
// positions (max/min PupilY and optionally PupilX) for one chief result and
// returns them as marginal rays. Pupil-plane coordinates come from the grid
// so that the marginal rays trace the aperture boundary.
func MarginalRaysForField(fi int, r Result, wavelength float64, path []int, pol types.JonesVector) []types.Ray {
	var maxY, minY *types.GridPoint
	var maxX, minX *types.GridPoint
	hasX := math.Abs(r.FieldDir.X) > 1e-6

	for i := range r.GridPoints {
		gp := &r.GridPoints[i]
		if gp.ImageX == nil || gp.ImageY == nil {
			continue
		}
		py := gp.PupilY
		if maxY == nil || py > maxY.PupilY {
			maxY = gp
		}
		if minY == nil || py < minY.PupilY {
			minY = gp
		}
		if hasX {
			px := gp.PupilX
			if maxX == nil || px > maxX.PupilX {
				maxX = gp
			}
			if minX == nil || px < minX.PupilX {
				minX = gp
			}
		}
	}

	fid := fmt.Sprintf("f%d", fi)
	var rays []types.Ray
	if maxY != nil && *maxY.ImageY != 0 {
		rays = append(rays, types.Ray{
			ID:         fmt.Sprintf("marginal_%s_Yplus", fid),
			Wavelength: wavelength,
			Initial:    types.RayState{Origin: maxY.Origin, Direction: maxY.Direction},
			Path:       path,
			Jones:      pol,
			Lenient:    true,
		})
	}
	if minY != nil && *minY.ImageY != 0 {
		rays = append(rays, types.Ray{
			ID:         fmt.Sprintf("marginal_%s_Yminus", fid),
			Wavelength: wavelength,
			Initial:    types.RayState{Origin: minY.Origin, Direction: minY.Direction},
			Path:       path,
			Jones:      pol,
			Lenient:    true,
		})
	}
	if hasX {
		if maxX != nil && *maxX.ImageX != 0 {
			rays = append(rays, types.Ray{
				ID:         fmt.Sprintf("marginal_%s_Xplus", fid),
				Wavelength: wavelength,
				Initial:    types.RayState{Origin: maxX.Origin, Direction: maxX.Direction},
				Path:       path,
				Jones:      pol,
				Lenient:    true,
			})
		}
		if minX != nil && *minX.ImageX != 0 {
			rays = append(rays, types.Ray{
				ID:         fmt.Sprintf("marginal_%s_Xminus", fid),
				Wavelength: wavelength,
				Initial:    types.RayState{Origin: minX.Origin, Direction: minX.Direction},
				Path:       path,
				Jones:      pol,
				Lenient:    true,
			})
		}
	}
	return rays
}

// marginalsAtStop builds the cardinal marginal rays for one field. Each marginal
// starts as a ray through the aperture-stop edge (stop vertex centre +/- physical
// stop radius, in the stop vertex plane) and is then vignetted against every
// other surface's clear aperture: a bisection search on the pupil fraction
// f (0 = chief, 1 = stop edge) finds the largest f whose ray passes all surfaces
// without an aperture or glass-path clip. Only that validated ray is emitted, so
// the marginal truly grazes the effective (vignetted) aperture. It is independent
// of the pupil grid and of the ray launch plane (robust for tilted / off-axis
// fields).
//
// The bundle is taken along the field's chief direction (angle / image-height
// fields, object at infinity -- a parallel bundle grazing the stop edge) or from
// the object point through the edge for a finite conjugate (object-height)
// field. Sagittal (X) marginals are only produced for fields that carry an X
// direction component; otherwise only the Y pair is emitted. A marginal is
// omitted when it is fully vignetted (its maximum valid fraction is ~0).
//
// ok is false when there is no usable stop edge, signalling the caller to fall
// back to the pupil-grid extremes (MarginalRaysForField). engine may be nil, in
// which case vignetting is not applied and the raw stop-edge rays are returned
// (the final rays always use Lenient mode for plotting).
func marginalsAtStop(fi int, r Result, stop types.Surface, engine *ray.Engine, surfaces []types.Surface, wavelength float64, path []int, pol types.JonesVector) ([]types.Ray, bool) {
	radius := stop.Diameter / 2
	if radius <= 0 {
		// No physical aperture declared on the stop: fall back to the paraxial
		// entrance-pupil radius if it was recorded.
		if ep := r.EntrancePupil; ep != nil && ep.Radius > 0 {
			radius = ep.Radius
		} else {
			return nil, false
		}
	}

	// Stop aperture centre: the clear aperture is centred on the stop vertex
	// (its X/Y decenter), not on the chief ray's position at the stop plane. The
	// marginal must graze the aperture edge (vertex +/- radius), so it is
	// anchored to the vertex. The sag-aware rim Z is added below so a curved
	// stop is not clipped (for a flat stop the sag is zero).
	center := types.Vec3{Z: stop.PhysicalZ}
	if v := stop.LocalToGlobal.MultiplyPoint(types.Vec3{}); v.X != 0 || v.Y != 0 || v.Z != 0 {
		center.X = v.X
		center.Y = v.Y
	}
	rimZ := stopRimSag(stop, radius)
	rayDir := r.ChiefRay.Initial.Direction
	if rayDir.LengthSq() == 0 {
		return nil, false
	}
	origin0 := r.ChiefRay.Initial.Origin
	zStart := origin0.Z
	isHeight := math.Abs(r.FieldHeight) > 1e-12
	hasX := math.Abs(r.FieldDir.X) > 1e-6
	fid := fmt.Sprintf("f%d", fi)

	// construct builds the marginal ray for the given pupil fraction f and
	// leniency, or nil when the geometry is degenerate (grazing chief, etc.).
	construct := func(f float64, edir types.Vec3, tag string, lenient bool) *types.Ray {
		edgeW := center.Add(edir.Scale(f * radius))
		edgeW.Z += rimZ
		var origin, d types.Vec3
		if isHeight {
			d = edgeW.Subtract(origin0).Normalize()
			if d.LengthSq() == 0 {
				return nil
			}
			origin = origin0
		} else {
			d = rayDir
			if math.Abs(d.Z) < 1e-12 {
				return nil
			}
			t := (edgeW.Z - zStart) / d.Z
			origin = edgeW.Subtract(d.Scale(t))
			// Launch from the wavefront plane (perpendicular to rayDir)
			// through the chief ray's launch point, so the marginal's OPL
			// shares the same reference as the grid rays and carries no
			// launch-geometry tilt.
			origin = raymath.ProjectOntoWavefront(origin, origin0, d)
		}
		return &types.Ray{
			ID:         fmt.Sprintf("marginal_%s_%s", fid, tag),
			Wavelength: wavelength,
			Initial:    types.RayState{Origin: origin, Direction: d},
			Path:       path,
			Jones:      pol,
			Lenient:    lenient,
		}
	}

	// traces reports whether the candidate ray can actually pass through the
	// lens. It runs in Lenient mode and rejects the ray when:
	//   - a surface other than the stop clips it by aperture (effective
	//     diameter): this is the vignetting the marginal is adjusted for. An
	//     aperture_stop at the stop is ignored, since the marginal is meant to
	//     graze the stop edge itself (it may be a hair over on a folded curved
	//     stop);
	//   - it misses a surface geometry or undergoes total internal reflection,
	//     anywhere: such a ray cannot traverse the lens and is not a valid
	//     marginal, so it is rejected too.
	// Glass-path (edge-thickness) constraints are deliberately not considered
	// during the search.
	traces := func(cand *types.Ray) bool {
		if cand == nil || engine == nil {
			return cand != nil
		}
		cand.Lenient = true
		res := engine.TraceRay(*cand, surfaces)
		for _, s := range res.Surfaces {
			switch s.ErrorCode {
			case string(ray.ErrApertureStop):
				if s.SurfaceID != stop.ID {
					return false
				}
			case string(ray.ErrMissedSurface), string(ray.ErrTIR):
				return false
			}
		}
		return true
	}

	// maxValidFraction returns the largest pupil fraction f in [0,1] whose ray
	// passes every surface aperture, via bisection, or 0 when not even the chief
	// fraction (f=0) traces.
	maxValidFraction := func(edir types.Vec3) float64 {
		lo, hi := 0.0, 1.0
		if traces(construct(hi, edir, "", false)) {
			return hi // fast path: the full stop-edge ray already passes
		}
		if !traces(construct(lo, edir, "", false)) {
			return 0 // fully vignetted on this side
		}
		for i := 0; i < 20; i++ {
			mid := (lo + hi) / 2
			if traces(construct(mid, edir, "", false)) {
				lo = mid
			} else {
				hi = mid
			}
			if hi-lo < 0.0005 {
				break
			}
		}
		return lo
	}

	type edge struct {
		dir types.Vec3
		tag string
	}
	edges := []edge{
		{types.Vec3{Y: +1}, "Yplus"},
		{types.Vec3{Y: -1}, "Yminus"},
	}
	if hasX {
		edges = append(edges,
			edge{types.Vec3{X: +1}, "Xplus"},
			edge{types.Vec3{X: -1}, "Xminus"},
		)
	}

	var rays []types.Ray
	for _, e := range edges {
		f := 1.0
		if engine != nil {
			f = maxValidFraction(e.dir)
		}
		if f <= 0 {
			// Even the chief fraction does not pass on this side; there is no
			// marginal ray at all. Otherwise emit the largest fraction that
			// passes every surface, however small, so both sides always get a
			// marginal.
			continue
		}
		if r := construct(f, e.dir, e.tag, true); r != nil {
			rays = append(rays, *r)
		}
	}
	return rays, true
}

// MarginalRays extracts the marginal rays for one field. When an aperture stop
// is defined (stopSurfaceID > 0) the rays through the (vignetted) stop edge are
// used -- robust and grid-independent, correct for tilted / off-axis fields;
// otherwise the pupil-grid extremes (MarginalRaysForField) are used as a
// fallback. engine (may be nil) drives the vignetting bisection.
func MarginalRays(fi int, r Result, stopSurfaceID int, surfaces []types.Surface, engine *ray.Engine, wavelength float64, path []int, pol types.JonesVector) []types.Ray {
	if stopSurfaceID > 0 {
		for i := range surfaces {
			if surfaces[i].ID == stopSurfaceID {
				if rays, ok := marginalsAtStop(fi, r, surfaces[i], engine, surfaces, wavelength, path, pol); ok {
					return rays
				}
				break
			}
		}
	}
	return MarginalRaysForField(fi, r, wavelength, path, pol)
}

// stopRimSag returns the global Z-offset, relative to the stop vertex plane, of
// the stop surface rim at radial distance radius. It is used so a marginal ray
// grazes the curved rim instead of the flat vertex-plane edge (which would
// otherwise be clipped by a curved stop). Returns 0 for a flat stop, for a
// radius beyond the surface's valid sag domain, or when the surface has not been
// precomputed.
func stopRimSag(stop types.Surface, radius float64) float64 {
	if stop.Curvature == 0 || radius <= 0 {
		return 0
	}
	s := surface.SagFunc(stop)(radius)
	if math.IsNaN(s) {
		return 0
	}
	p := stop.LocalToGlobal.MultiplyPoint(types.Vec3{Y: radius, Z: s})
	return p.Z - stop.PhysicalZ
}
