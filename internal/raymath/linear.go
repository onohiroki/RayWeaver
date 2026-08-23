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

// SolveLinearCopy solves a·x = b without mutating the inputs, returning the
// solution in a fresh slice. It reports false when the matrix is singular.
func SolveLinearCopy(a [][]float64, b []float64) ([]float64, bool) {
	n := len(b)
	m := make([][]float64, n)
	rhs := make([]float64, n)
	for i := 0; i < n; i++ {
		m[i] = append([]float64(nil), a[i]...)
		rhs[i] = b[i]
	}
	if !SolveLinear(m, rhs) {
		return nil, false
	}
	return rhs, true
}

// SolveCholesky solves the symmetric positive-definite system H·x = g via
// Cholesky decomposition H = L·Lᵀ. It returns the solution and reports true
// on success. The input H is not mutated. On failure (non-positive-definite or
// NaN/Inf) it returns nil, false so the caller can fall back to the general
// Gaussian elimination solver.
func SolveCholesky(H [][]float64, g []float64) ([]float64, bool) {
	n := len(g)
	if n == 0 {
		return nil, false
	}

	// Copy H into L (lower triangular Cholesky factor).
	L := make([][]float64, n)
	for i := range L {
		L[i] = make([]float64, n)
		for j := range L[i] {
			L[i][j] = sanitize(H[i][j])
		}
	}

	for j := 0; j < n; j++ {
		sum := L[j][j]
		for k := 0; k < j; k++ {
			sum -= L[j][k] * L[j][k]
		}
		if sum <= 0 {
			return nil, false
		}
		Ljj := math.Sqrt(sum)
		L[j][j] = Ljj
		for i := j + 1; i < n; i++ {
			sum := L[i][j]
			for k := 0; k < j; k++ {
				sum -= L[i][k] * L[j][k]
			}
			L[i][j] = sum / Ljj
		}
	}

	// Forward substitution: L·y = g.
	y := make([]float64, n)
	for i := 0; i < n; i++ {
		sum := sanitize(g[i])
		for k := 0; k < i; k++ {
			sum -= L[i][k] * y[k]
		}
		y[i] = sum / L[i][i]
	}

	// Back substitution: Lᵀ·x = y.
	x := make([]float64, n)
	for i := n - 1; i >= 0; i-- {
		sum := y[i]
		for k := i + 1; k < n; k++ {
			sum -= L[k][i] * x[k]
		}
		x[i] = sum / L[i][i]
	}

	// Verify the solution is finite.
	for i := 0; i < n; i++ {
		if math.IsNaN(x[i]) || math.IsInf(x[i], 0) {
			return nil, false
		}
	}
	return x, true
}

func sanitize(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}
