package chief

import (
	"fmt"
	"math"

	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/raymath"
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

// marginalsAtStop builds the cardinal marginal rays for one field as the rays
// through the aperture-stop edge. The edge is located where it is actually
// traced: a launch offset from the entrance pupil is solved by bisection on the
// traced hit at the stop until the ray crosses the stop at its clear-aperture
// radius. Aiming a straight line from the launch plane at the stop vertex plane
// is not enough — the refracting surfaces in front of the stop change the beam
// height, so a demagnifying front group overshoots the stop by the inverse pupil
// magnification (about 2.6x on the P2008 zoom, where a launch at the stop radius
// crossed the stop at 6.85 mm for a 2.643 mm radius).
//
// The stop-edge ray is then vignetted against every other surface's clear
// aperture: a bisection on the pupil fraction f (0 = chief, 1 = stop edge) finds
// the largest f whose ray passes all non-stop surfaces. A marginal is omitted
// when it is fully vignetted (its maximum valid fraction is ~0).
//
// The bundle is launched from the entrance pupil (the stop's image): a parallel
// bundle along the field's chief direction for angle / image-height fields, or
// from the object point toward the pupil edge for a finite conjugate
// (object-height) field. Sagittal (X) marginals are only produced for fields
// that carry an X direction component; otherwise only the Y pair is emitted.
//
// ok is false when there is no usable stop edge or no engine to solve it with,
// signalling the caller to fall back to the already-traced pupil-grid extremes
// (MarginalRaysForField).
func marginalsAtStop(fi int, r Result, stop types.Surface, engine *ray.Engine, surfaces []types.Surface, wavelength float64, path []int, pol types.JonesVector) ([]types.Ray, bool) {
	radius := stop.Diameter / 2
	hasStopEdge := radius > 0
	if !hasStopEdge {
		// No physical aperture declared on the stop: fall back to the paraxial
		// entrance-pupil radius if it was recorded. There is no stop edge to
		// solve for; the marginal is the pupil edge itself.
		if ep := r.EntrancePupil; ep != nil && ep.Radius > 0 {
			radius = ep.Radius
		} else {
			return nil, false
		}
	}
	if engine == nil {
		// Without an engine the stop edge cannot be located; let the caller use
		// the already-traced pupil-grid extremes instead of a straight-line
		// guess that ignores refraction.
		return nil, false
	}

	rayDir := r.ChiefRay.Initial.Direction
	if rayDir.LengthSq() == 0 {
		return nil, false
	}
	origin0 := r.ChiefRay.Initial.Origin
	zStart := origin0.Z
	isHeight := math.Abs(r.FieldHeight) > 1e-12
	hasX := math.Abs(r.FieldDir.X) > 1e-6
	fid := fmt.Sprintf("f%d", fi)

	// Stop aperture centre: the clear aperture is centred on the stop vertex
	// (its X/Y decenter), not on the chief ray's position at the stop plane.
	stopVertex := types.Vec3{Z: stop.PhysicalZ}
	if v := stop.LocalToGlobal.MultiplyPoint(types.Vec3{}); v.X != 0 || v.Y != 0 || v.Z != 0 {
		stopVertex = v
	}

	// Launch basis: the entrance-pupil centre and radius (the stop's image).
	// Fall back to the chief launch point for a parallel bundle, or the stop
	// vertex for a finite conjugate, when the result carries no pupil record.
	pupilCenter := stopVertex
	pupilRadius := radius
	if ep := r.EntrancePupil; ep != nil {
		pupilCenter = ep.Center
		if ep.Radius > 0 {
			pupilRadius = ep.Radius
		}
	} else if !isHeight {
		pupilCenter = origin0
	}
	launchCenter := pupilCenter
	if !isHeight {
		// Point on the launch plane whose chief-direction ray crosses the pupil
		// centre; the grid and fan rays use the same point.
		launchCenter = raymath.WavefrontGridCenter(pupilCenter, rayDir, zStart)
	}

	// construct builds the marginal ray for the given pupil-plane offset and
	// leniency, or nil when the geometry is degenerate.
	construct := func(offset float64, edir types.Vec3, tag string, lenient bool) *types.Ray {
		var origin, d types.Vec3
		if isHeight {
			aim := pupilCenter.Add(edir.Scale(offset))
			d = aim.Subtract(origin0).Normalize()
			if d.LengthSq() == 0 {
				return nil
			}
			origin = origin0
		} else {
			d = rayDir
			// Launch from the wavefront plane (perpendicular to rayDir) through
			// the chief ray's launch point, so the marginal's OPL shares the
			// same reference as the grid rays and carries no launch-geometry
			// tilt.
			origin = raymath.ProjectOntoWavefront(launchCenter.Add(edir.Scale(offset)), origin0, d)
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

	// stopCoord returns the lateral coordinate of the candidate's hit on the
	// stop, measured in the stop's local frame along edir (the same coordinate
	// the aperture check uses), or ok=false when the ray does not reach the
	// stop.
	stopCoord := func(cand *types.Ray, edir types.Vec3) (float64, bool) {
		if cand == nil {
			return 0, false
		}
		cand.Lenient = true
		res := engine.TraceRay(*cand, surfaces, false)
		for _, sr := range res.Surfaces {
			if sr.SurfaceID != stop.ID {
				continue
			}
			local := stop.GlobalToLocal.MultiplyPoint(sr.Position)
			e := stop.GlobalToLocal.MultiplyVector(edir)
			l := math.Hypot(e.X, e.Y)
			if l < 1e-12 {
				return 0, false
			}
			e.X, e.Y = e.X/l, e.Y/l
			w := local.X*e.X + local.Y*e.Y
			if math.IsNaN(w) || math.IsInf(w, 0) {
				return 0, false
			}
			return w, true
		}
		return 0, false
	}

	// edgeOffset solves for the launch offset whose traced stop hit sits on the
	// clear-aperture edge. It returns ok=false when the chief is already outside
	// on this side or the ray cannot reach the stop.
	edgeOffset := func(edir types.Vec3) (float64, bool) {
		if !hasStopEdge {
			return pupilRadius, pupilRadius > 0
		}
		w0, ok := stopCoord(construct(0, edir, "", false), edir)
		if !ok || w0 >= radius {
			return 0, false
		}
		hi := math.Max(pupilRadius, radius)
		wHi, ok := stopCoord(construct(hi, edir, "", false), edir)
		for i := 0; i < 8 && ok && wHi < radius; i++ {
			hi *= 2
			wHi, ok = stopCoord(construct(hi, edir, "", false), edir)
		}
		if !ok || wHi < radius {
			return 0, false
		}
		lo := 0.0
		for i := 0; i < 40; i++ {
			mid := (lo + hi) / 2
			w, ok := stopCoord(construct(mid, edir, "", false), edir)
			if !ok {
				return 0, false
			}
			if w < radius {
				lo = mid
			} else {
				hi = mid
			}
			if hi-lo < 1e-6*(1+radius) {
				break
			}
		}
		return lo, true
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
		if cand == nil {
			return false
		}
		cand.Lenient = true
		res := engine.TraceRay(*cand, surfaces, false)
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
	// passes every non-stop surface aperture, via bisection, or 0 when not even
	// the chief fraction (f=0) traces.
	maxValidFraction := func(edge float64, edir types.Vec3) float64 {
		lo, hi := 0.0, 1.0
		if traces(construct(hi*edge, edir, "", false)) {
			return hi // fast path: the full stop-edge ray already passes
		}
		if !traces(construct(lo, edir, "", false)) {
			return 0 // fully vignetted on this side
		}
		for i := 0; i < 20; i++ {
			mid := (lo + hi) / 2
			if traces(construct(mid*edge, edir, "", false)) {
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
	solved := false
	for _, e := range edges {
		edge, ok := edgeOffset(e.dir)
		if !ok || edge <= 0 {
			continue
		}
		solved = true
		f := maxValidFraction(edge, e.dir)
		if f <= 0 {
			// Even a vanishing fraction of the stop edge does not pass on this
			// side: there is no marginal ray at all. Otherwise emit the largest
			// fraction that passes every surface, however small, so both sides
			// always get a marginal.
			continue
		}
		if r := construct(f*edge, e.dir, e.tag, true); r != nil {
			rays = append(rays, *r)
		}
	}
	return rays, solved
}

// MarginalRays extracts the marginal rays for one field. When an aperture stop
// is defined (stopSurfaceID > 0) the rays through the (vignetted) stop edge are
// used -- robust and grid-independent, correct for tilted / off-axis fields;
// otherwise the pupil-grid extremes (MarginalRaysForField) are used as a
// fallback. A nil engine cannot solve the stop edge, so the grid extremes are
// used there too.
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
