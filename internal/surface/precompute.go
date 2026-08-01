package surface

import (
	"math"

	"github.com/hiroki/rayweaver/internal/raymath"
	"github.com/hiroki/rayweaver/internal/types"
)

func foldRotation(s types.Surface) types.Mat4 {
	m := types.NewIdentity()
	for _, d := range s.Decenter {
		if !d.Reflect {
			continue
		}
		rx := types.NewRotationX(d.Tilt.X * math.Pi / 180.0)
		ry := types.NewRotationY(d.Tilt.Y * math.Pi / 180.0)
		rz := types.NewRotationZ(d.Tilt.Z * math.Pi / 180.0)
		m = m.Multiply(rx).Multiply(ry).Multiply(rz)
	}
	return m
}

func Precompute(surfaces []types.Surface) {
	frameOrigin := 0.0
	frameRot := types.NewIdentity()
	currentZ := 0.0

	for i := range surfaces {
		s := &surfaces[i]

		local := raymath.ComputeDecenterTransform(s.Decenter)
		lt := types.NewTranslation(types.Vec3{Z: frameOrigin}).
			Multiply(frameRot).
			Multiply(types.NewTranslation(types.Vec3{Z: currentZ})).
			Multiply(local)
		s.LocalToGlobal = lt
		s.GlobalToLocal = lt.Inverse()
		s.PhysicalZ = lt.MultiplyPoint(types.Vec3{}).Z

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

		if s.Reflects() {
			frameOrigin = s.PhysicalZ
			frameRot = frameRot.Multiply(foldRotation(*s))
			currentZ = s.Thickness
		} else {
			currentZ += s.Thickness
		}
	}
}

// PhysicalZ returns the folded physical vertex Z of every surface, using the
// same fold walk as Precompute. It is self-contained and does not require
// Precompute to have run.
func PhysicalZ(surfaces []types.Surface) []float64 {
	z := make([]float64, len(surfaces))
	frameOrigin := 0.0
	frameRot := types.NewIdentity()
	currentZ := 0.0

	for i := range surfaces {
		s := &surfaces[i]

		local := raymath.ComputeDecenterTransform(s.Decenter)
		lt := types.NewTranslation(types.Vec3{Z: frameOrigin}).
			Multiply(frameRot).
			Multiply(types.NewTranslation(types.Vec3{Z: currentZ})).
			Multiply(local)
		z[i] = lt.MultiplyPoint(types.Vec3{}).Z

		if s.Reflects() {
			frameOrigin = z[i]
			frameRot = frameRot.Multiply(foldRotation(*s))
			currentZ = s.Thickness
		} else {
			currentZ += s.Thickness
		}
	}
	return z
}
