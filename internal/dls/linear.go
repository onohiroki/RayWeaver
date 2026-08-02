package dls

import (
	"math"

	"github.com/hiroki/rayweaver/internal/raymath"
)

func solveLinearSystem(H [][]float64, g []float64) []float64 {
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
