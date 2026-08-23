package dls

import (
	"math"

	"github.com/hiroki/rayweaver/internal/raymath"
)

// solveLinearSystem solves H·x = -g for the step direction. The normal
// equations H = JᵀJ + μI are symmetric positive-definite, so Cholesky is tried
// first (O(n³/3), numerically superior); on failure it falls back to general
// Gaussian elimination with partial pivoting.
func solveLinearSystem(H [][]float64, g []float64) []float64 {
	if x, ok := raymath.SolveCholesky(H, g); ok {
		return x
	}
	n := len(g)
	a := make([][]float64, n)
	b := make([]float64, n)
	for i := 0; i < n; i++ {
		a[i] = make([]float64, n)
		for j := 0; j < n; j++ {
			a[i][j] = sanitize(H[i][j])
		}
		b[i] = sanitize(g[i])
	}
	if !raymath.SolveLinear(a, b) {
		return nil
	}
	return b
}

func sanitize(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}
