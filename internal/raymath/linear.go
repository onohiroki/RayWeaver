package raymath

import "math"

// SolveLinear solves the linear system a·x = b in place with partial pivoting.
// The coefficient matrix a and the right-hand side b are modified; on return b
// holds the solution. It reports false if the matrix is (numerically)
// singular. NaN/Inf entries in a or b are treated as zero.
func SolveLinear(a [][]float64, b []float64) bool {
	n := len(b)
	for col := 0; col < n; col++ {
		pivot := col
		maxVal := math.Abs(sanitize(a[col][col]))
		for row := col + 1; row < n; row++ {
			if v := math.Abs(sanitize(a[row][col])); v > maxVal {
				maxVal = v
				pivot = row
			}
		}
		if pivot != col {
			a[col], a[pivot] = a[pivot], a[col]
			b[col], b[pivot] = b[pivot], b[col]
		}
		if math.Abs(sanitize(a[col][col])) < 1e-15 {
			return false
		}

		for row := col + 1; row < n; row++ {
			factor := sanitize(a[row][col]) / sanitize(a[col][col])
			for j := col; j < n; j++ {
				a[row][j] = sanitize(a[row][j]) - factor*sanitize(a[col][j])
			}
			b[row] = sanitize(b[row]) - factor*sanitize(b[col])
		}
	}

	for i := n - 1; i >= 0; i-- {
		sum := sanitize(b[i])
		for j := i + 1; j < n; j++ {
			sum -= sanitize(a[i][j]) * b[j]
		}
		b[i] = sum / sanitize(a[i][i])
	}
	return true
}

func sanitize(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}
