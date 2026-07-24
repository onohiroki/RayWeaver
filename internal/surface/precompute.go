package surface

import (
	"github.com/hiroki/rayweaver/internal/raymath"
	"github.com/hiroki/rayweaver/internal/types"
)

func Precompute(surfaces []types.Surface) {
	currentZ := 0.0

	for i := range surfaces {
		s := &surfaces[i]

		local := raymath.ComputeDecenterTransform(s.Decenter)

		position := types.NewTranslation(types.Vec3{X: 0, Y: 0, Z: currentZ})
		s.LocalToGlobal = position.Multiply(local)
		s.GlobalToLocal = s.LocalToGlobal.Inverse()

		switch s.Type {
		case types.Sphere:
			if s.Curvature != 0 {
				s.ParaxialRadius = raymath.SphereParaxialRadius(s.Radius(), s.Coefficients)
			} else {
				s.ParaxialRadius = 0
			}
		case types.AspherePolynomial:
			sagFunc := func(h float64) float64 {
				return raymath.PolynomialAsphereSag(h, s.Radius(), s.Conic, s.Coefficients)
			}
			curvature := raymath.ComputeParaxialCurvature(sagFunc)
			if curvature != 0 {
				s.ParaxialRadius = 1.0 / curvature
			} else {
				s.ParaxialRadius = 0
			}
		case types.AsphereZernike:
			sagFunc := func(h float64) float64 {
				return raymath.ZernikeAsphereSag(h, s.Radius(), s.Conic, s.Coefficients, s.NormRadius)
			}
			curvature := raymath.ComputeParaxialCurvature(sagFunc)
			if curvature != 0 {
				s.ParaxialRadius = 1.0 / curvature
			} else {
				s.ParaxialRadius = 0
			}
		}

		currentZ += s.Thickness
	}
}
