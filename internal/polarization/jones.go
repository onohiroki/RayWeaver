package polarization

import (
	"github.com/hiroki/rayweaver/internal/types"
)

// JonesMatrix is a 2x2 complex matrix acting on a JonesVector in the local
// s/p basis of a ray.
type JonesMatrix [2][2]complex128

// IdentityJones returns the identity 2x2 matrix.
func IdentityJones() JonesMatrix {
	return JonesMatrix{
		{1, 0},
		{0, 1},
	}
}

// DiagonalJones returns diag(a, b).
func DiagonalJones(a, b complex128) JonesMatrix {
	return JonesMatrix{
		{a, 0},
		{0, b},
	}
}

// Apply multiplies the matrix by the Jones vector.
func (m JonesMatrix) Apply(v types.JonesVector) types.JonesVector {
	return types.JonesVector{
		Ex: m[0][0]*v.Ex + m[0][1]*v.Ey,
		Ey: m[1][0]*v.Ex + m[1][1]*v.Ey,
	}
}

// Multiply returns the matrix product m·o.
func (m JonesMatrix) Multiply(o JonesMatrix) JonesMatrix {
	var r JonesMatrix
	for i := 0; i < 2; i++ {
		for j := 0; j < 2; j++ {
			r[i][j] = m[i][0]*o[0][j] + m[i][1]*o[1][j]
		}
	}
	return r
}

// FresnelTransmissionMatrix returns diag(ts, tp): the complex amplitude
// transmission matrix in the local s/p basis.
func FresnelTransmissionMatrix(ts, tp float64) JonesMatrix {
	return DiagonalJones(complex(ts, 0), complex(tp, 0))
}

// FresnelReflectionMatrix returns diag(rs, rp): the complex amplitude
// reflection matrix in the local s/p basis.
func FresnelReflectionMatrix(rs, rp float64) JonesMatrix {
	return DiagonalJones(complex(rs, 0), complex(rp, 0))
}
