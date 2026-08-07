package ray

import (
	"math"

	"github.com/hiroki/rayweaver/internal/raymath"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// TraceBackward traces a ray in the backward (-Z) beam-frame direction through
// the surface sequence seq (indices into surfaces, in visit order), starting at
// startPos with direction startDir. Media are resolved like TraceRay: a backward
// ray approaches each surface from the image side (the surface's own material),
// so the incident/emergent media are swapped relative to a forward ray. Fold
// mirrors are handled by their precomputed beam-frame transforms and ideal
// reflection. Returns the final global position and direction, or ok=false on a
// miss or total internal reflection.
func (e *Engine) TraceBackward(surfaces []types.Surface, seq []int, startPos, startDir types.Vec3, wavelength float64) (types.Vec3, types.Vec3, bool) {
	pos := startPos
	dir := startDir.Normalize()

	for _, idx := range seq {
		if idx < 0 || idx >= len(surfaces) {
			return pos, dir, false
		}
		s := &surfaces[idx]

		localPos := s.GlobalToLocal.MultiplyPoint(pos)
		localDir := s.GlobalToLocal.MultiplyVector(dir).Normalize()

		// A backward ray approaching from the image side first hits the BACK of
		// a sphere; select the intersection closest to the vertex plane (local
		// z ≈ 0), which is the physical surface.
		// A backward ray approaching from the image side first hits the BACK of
		// a sphere; select the intersection closest to the vertex plane (local
		// z ≈ 0), which is the physical surface.
		var t float64
		if s.Type == types.Sphere {
			t1, t2, ok := raymath.IntersectSphereBoth(localPos, localDir, s.Radius())
			if !ok {
				return pos, dir, false
			}
			h1 := localPos.Z + localDir.Z*t1
			h2 := localPos.Z + localDir.Z*t2
			// Only hits in front of the ray are valid; among them prefer the
			// one closest to the vertex plane (local z ≈ 0), which is the
			// physical surface rather than the back of the sphere.
			v1, v2 := t1 >= -1e-6, t2 >= -1e-6
			switch {
			case v1 && v2:
				if math.Abs(h2) < math.Abs(h1) {
					t = t2
				} else {
					t = t1
				}
			case v1:
				t = t1
			case v2:
				t = t2
			default:
				return pos, dir, false
			}
		} else {
			var ok bool
			t, ok = surface.Intersect(*s, localPos, localDir)
			if !ok {
				return pos, dir, false
			}
		}
		// A slightly negative t (already at a coincident surface) is accepted;
		// a clearly-behind hit is a miss.
		if t < -1e-6 {
			return pos, dir, false
		}

		hit := types.Vec3{
			X: localPos.X + localDir.X*t,
			Y: localPos.Y + localDir.Y*t,
			Z: localPos.Z + localDir.Z*t,
		}
		normal := surface.Normal(*s, hit)
		if cosTheta1 := -localDir.Dot(normal); cosTheta1 < 0 {
			normal = normal.Negate()
		}

		// Incident/emergent media depend on the travel direction: a backward ray
		// approaches from the image side (the surface's own material).
		n1mat := materialBefore(surfaces, s.ID)
		n2mat := s.Material
		if localDir.Z < 0 {
			n1mat, n2mat = n2mat, n1mat
		}
		n1, _ := e.Glass.RefractiveIndex(n1mat, wavelength)
		n2, _ := e.Glass.RefractiveIndex(n2mat, wavelength)

		if s.Reflects() {
			dir = raymath.Reflect(localDir, normal)
		} else {
			newDir, ok := raymath.Refract(localDir, normal, n1, n2)
			if !ok {
				return pos, dir, false
			}
			dir = newDir
		}

		pos = s.LocalToGlobal.MultiplyPoint(hit)
		dir = s.LocalToGlobal.MultiplyVector(dir).Normalize()
	}

	return pos, dir, true
}

// FrontPath returns the surface indices before the surface with stopID (in the
// surfaces slice), in reverse visit order — the sequence a backward ray starting
// at the stop travels to reach object space. Nil when the stop is the first
// surface (no front optics).
func FrontPath(surfaces []types.Surface, stopID int) []int {
	stopIdx := -1
	for i := range surfaces {
		if surfaces[i].ID == stopID {
			stopIdx = i
			break
		}
	}
	if stopIdx <= 0 {
		return nil
	}
	seq := make([]int, 0, stopIdx)
	for i := stopIdx - 1; i >= 0; i-- {
		seq = append(seq, i)
	}
	return seq
}

// EmergentAngle returns the angle (radians) of dir to the optical axis: the
// angle a backward-traced ray makes in object space after emerging from the
// front optics.
func EmergentAngle(dir types.Vec3) float64 {
	perp := math.Hypot(dir.X, dir.Y)
	return math.Atan2(perp, math.Abs(dir.Z))
}
