package polarization

import (
	"math"

	"github.com/hiroki/rayweaver/internal/types"
)

// SPBasis is the local s/p polarization frame of a ray at a surface.
//
//	S — unit vector perpendicular to the plane of incidence (invariant under
//	    refraction/reflection, since the incident and outgoing rays and the
//	    surface normal are coplanar)
//	P — unit vector in the plane of incidence, perpendicular to the incident
//	    direction: p = s × d_in
//
// The P direction for the outgoing ray differs from P for the incident one;
// use OutgoingP with the refracted/reflected direction.
type SPBasis struct {
	S, P types.Vec3
}

// ComputeSPBasis returns the s/p frame for an incident direction and surface
// normal. The normal must be oriented against the incident ray
// (incident·normal < 0). At normal incidence the s direction is degenerate and
// a fixed lab-referenced transverse frame is chosen so the choice is
// consistent for the incident and outgoing rays.
func ComputeSPBasis(incident, normal types.Vec3) SPBasis {
	s := incident.Cross(normal)
	ls := s.Length()
	if ls < 1e-9 {
		s = fixedTransverse(incident)
	} else {
		s = s.Scale(1 / ls)
	}
	return SPBasis{S: s, P: s.Cross(incident).Normalize()}
}

// fixedTransverse returns a unit vector perpendicular to d, chosen from a
// fixed lab reference so it is stable across a surface.
func fixedTransverse(d types.Vec3) types.Vec3 {
	ref := types.Vec3{X: 0, Y: 1, Z: 0}
	if math.Abs(d.Dot(ref)) > 0.9 {
		ref = types.Vec3{X: 1, Y: 0, Z: 0}
	}
	return ref.Cross(d).Normalize()
}

// OutgoingP returns the p direction for the outgoing ray: p' = s × d_out.
func (b SPBasis) OutgoingP(outgoing types.Vec3) types.Vec3 {
	return b.S.Cross(outgoing).Normalize()
}

// Project decomposes the 3D field E into the s and p components of the basis.
func (b SPBasis) Project(E types.Vec3C) (es, ep complex128) {
	return E.DotVec(b.S), E.DotVec(b.P)
}

// FromSP reconstructs a 3D field from s/p components expressed in the given
// directions.
func FromSP(s, p types.Vec3, es, ep complex128) types.Vec3C {
	return types.Vec3C{
		X: es*complex(s.X, 0) + ep*complex(p.X, 0),
		Y: es*complex(s.Y, 0) + ep*complex(p.Y, 0),
		Z: es*complex(s.Z, 0) + ep*complex(p.Z, 0),
	}
}

// MirrorReflect reflects the electric field across the surface normal (ideal
// mirror): tangential components are preserved and the normal component is
// reversed, E_out = E - 2(E·n)n. The result is transverse to the reflected
// ray and preserves |E|.
func MirrorReflect(E types.Vec3C, normal types.Vec3) types.Vec3C {
	en := E.DotVec(normal)
	f := 2 * en
	return types.Vec3C{
		X: E.X - f*complex(normal.X, 0),
		Y: E.Y - f*complex(normal.Y, 0),
		Z: E.Z - f*complex(normal.Z, 0),
	}
}

// TransverseFrame returns two unit vectors (u, v) perpendicular to the
// direction d and to each other. u is horizontal (perpendicular to the
// meridional plane spanned by d and +Z); for d along +Z, u = (1,0,0) and
// v = (0,1,0). The result is well defined for any d except exactly anti-
// parallel to +Z.
func TransverseFrame(d types.Vec3) (u, v types.Vec3) {
	z := types.Vec3{X: 0, Y: 0, Z: 1}
	u = z.Cross(d)
	lu := u.Length()
	if lu < 1e-9 {
		u = types.Vec3{X: 1, Y: 0, Z: 0}
	} else {
		u = u.Scale(1 / lu)
	}
	v = d.Cross(u).Normalize()
	return u, v
}

// TransverseField returns a 3D field from a Jones vector expressed in the
// transverse frame (u, v) of a reference direction d (u, v ⊥ d, mutually
// perpendicular). For d along +Z this reproduces the global XY convention.
func TransverseField(u, v types.Vec3, j types.JonesVector) types.Vec3C {
	return FromSP(u, v, j.Ex, j.Ey)
}
