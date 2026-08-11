package types

import "math"

// Vec3C is a 3D complex vector used for the propagated electric field during
// polarized ray tracing and coherent PSF integration.
type Vec3C struct {
	X, Y, Z complex128
}

// DotVec projects the complex field onto a real direction vector.
func (v Vec3C) DotVec(d Vec3) complex128 {
	return v.X*complex(d.X, 0) + v.Y*complex(d.Y, 0) + v.Z*complex(d.Z, 0)
}

// Add returns the component-wise sum.
func (a Vec3C) Add(b Vec3C) Vec3C {
	return Vec3C{a.X + b.X, a.Y + b.Y, a.Z + b.Z}
}

// Scale scales every component by the complex factor s.
func (v Vec3C) Scale(s complex128) Vec3C {
	return Vec3C{v.X * s, v.Y * s, v.Z * s}
}

// AbsSq returns the squared magnitude |Ex|² + |Ey|² + |Ez|².
func (v Vec3C) AbsSq() float64 {
	return real(v.X)*real(v.X) + imag(v.X)*imag(v.X) +
		real(v.Y)*real(v.Y) + imag(v.Y)*imag(v.Y) +
		real(v.Z)*real(v.Z) + imag(v.Z)*imag(v.Z)
}

// Magnitude returns |E|.
func (v Vec3C) Magnitude() float64 {
	return math.Sqrt(v.AbsSq())
}
