package surface

import (
	"github.com/hiroki/rayweaver/internal/raymath"
	"github.com/hiroki/rayweaver/internal/types"
)

// SagFunc returns the sag function for a surface: its height-dependent
// deviation from the vertex plane along the optical axis.
func SagFunc(s types.Surface) func(h float64) float64 {
	switch s.Type {
	case types.AspherePolynomial:
		return func(h float64) float64 {
			return raymath.PolynomialAsphereSag(h, s.Radius(), s.Conic, s.Coefficients)
		}
	case types.AsphereZernike:
		return func(h float64) float64 {
			return raymath.ZernikeAsphereSag(h, s.Radius(), s.Conic, s.Coefficients, s.NormRadius)
		}
	default:
		return func(h float64) float64 {
			return raymath.PolynomialAsphereSag(h, s.Radius(), s.Conic, nil)
		}
	}
}

// Normal returns the outward surface normal at point p.
func Normal(s types.Surface, p types.Vec3) types.Vec3 {
	switch s.Type {
	case types.AspherePolynomial, types.AsphereZernike:
		return raymath.AsphereNormal(p, SagFunc(s))
	default:
		if s.Radius() == 0 {
			return types.Vec3{0, 0, 1}
		}
		return raymath.SphereNormal(p, s.Radius())
	}
}

// Intersect returns the ray parameter t at which the ray (origin, dir)
// intersects the surface, or false on a miss.
func Intersect(s types.Surface, origin, dir types.Vec3) (float64, bool) {
	switch s.Type {
	case types.AspherePolynomial, types.AsphereZernike:
		return raymath.IntersectAsphere(origin, dir, SagFunc(s), 50, 1e-12)
	default:
		return raymath.IntersectSphere(origin, dir, s.Radius())
	}
}
