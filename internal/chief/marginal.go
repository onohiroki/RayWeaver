package chief

import (
	"fmt"
	"math"

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

// marginalsAtStop builds the cardinal marginal rays for one field as the rays
// through the aperture-stop edge (stop vertex centre +/- physical stop radius,
// in the stop vertex plane). Because this is tied to the physical stop -- not
// the paraxial entrance-pupil radius -- the marginal grazes the real aperture
// edge, so it does not flag as vignetted (aperture_stop) on a flat stop, and it
// is independent of the pupil grid and of the ray launch plane (robust for
// tilted / off-axis fields).
//
// The bundle is taken along the field's chief direction (angle / image-height
// fields, object at infinity -- a parallel bundle grazing the stop edge) or from
// the object point through the edge for a finite conjugate (object-height)
// field. Sagittal (X) marginals are only produced for fields that carry an X
// direction component; otherwise only the Y pair is emitted.
//
// ok is false when there is no usable stop edge, signalling the caller to fall
// back to the pupil-grid extremes (MarginalRaysForField).
func marginalsAtStop(fi int, r Result, stop types.Surface, wavelength float64, path []int, pol types.JonesVector) ([]types.Ray, bool) {
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

	fid := fmt.Sprintf("f%d", fi)
	var rays []types.Ray
	for _, e := range edges {
		edgeW := center.Add(e.dir.Scale(radius))
		edgeW.Z += rimZ
		var origin, dir types.Vec3
		if isHeight {
			dir = edgeW.Subtract(origin0).Normalize()
			if dir.LengthSq() == 0 {
				return nil, false
			}
			origin = origin0
		} else {
			dir = rayDir
			if math.Abs(dir.Z) < 1e-12 {
				return nil, false // grazing chief: cannot anchor launch plane at zStart
			}
			t := (edgeW.Z - zStart) / dir.Z
			origin = edgeW.Subtract(dir.Scale(t))
		}
		rays = append(rays, types.Ray{
			ID:         fmt.Sprintf("marginal_%s_%s", fid, e.tag),
			Wavelength: wavelength,
			Initial:    types.RayState{Origin: origin, Direction: dir},
			Path:       path,
			Jones:      pol,
			Lenient:    true,
		})
	}
	return rays, true
}

// MarginalRays extracts the marginal rays for one field. When an aperture stop
// is defined (stopSurfaceID > 0) the rays through the stop edge are used --
// robust and grid-independent, correct for tilted / off-axis fields; otherwise
// the pupil-grid extremes (MarginalRaysForField) are used as a fallback.
func MarginalRays(fi int, r Result, stopSurfaceID int, surfaces []types.Surface, wavelength float64, path []int, pol types.JonesVector) []types.Ray {
	if stopSurfaceID > 0 {
		for i := range surfaces {
			if surfaces[i].ID == stopSurfaceID {
				if rays, ok := marginalsAtStop(fi, r, surfaces[i], wavelength, path, pol); ok {
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
