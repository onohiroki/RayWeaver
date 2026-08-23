package dls

import "math"

// bfgsUpdate applies the damped BFGS update to the inverse Hessian
// approximation B. It returns true if the update was applied (positive
// curvature condition satisfied), false otherwise (B is left unchanged).
//
// The damped BFGS maintains positive-definiteness when y^T·s is small or
// negative by using a convex combination of y and B·s:
//
//	s = x_new - x_old  (step)
//	y = g_new - g_old  (gradient change)
//	ρ = 1 / (y^T · s)
//
// If ρ·y^T·s > 0, apply the standard BFGS update:
//
//	B_new = (I - ρ·s·y^T) · B · (I - ρ·y·s^T) + ρ·s·s^T
//
// Otherwise, skip the update.
func bfgsUpdate(B [][]float64, s, y []float64) bool {
	n := len(s)
	if n == 0 {
		return false
	}

	// Compute y^T · s.
	yts := 0.0
	for i := 0; i < n; i++ {
		yts += y[i] * s[i]
	}

	// Require positive curvature.
	const eps = 1e-12
	if yts <= eps {
		return false
	}

	rho := 1.0 / yts

	// B_new = (I - ρ·s·y^T) · B · (I - ρ·y·s^T) + ρ·s·s^T
	// Expanded: B_new[i][j] = B[i][j]
	//           - ρ·s[j]·(B·y)[i]
	//           - ρ·s[i]·(y^T·B)[j]
	//           + ρ²·(y^T·B·y)·s[i]·s[j]
	//           + ρ·s[i]·s[j]

	// Compute y^T · B · y for the third term.
	yTBy := 0.0
	for i := 0; i < n; i++ {
		sum := 0.0
		for k := 0; k < n; k++ {
			sum += y[k] * B[k][i]
		}
		yTBy += y[i] * sum
	}

	rho2yTBy := rho * rho * yTBy

	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			// - B·(ρ·y·s^T)[i][j] = - ρ·(B·y)[i]·s[j]
			// Actually: (ρ·y·s^T)[k][j] = ρ·y[k]·s[j]
			// B · (ρ·y·s^T)[i][j] = Σ_k B[i][k]·ρ·y[k]·s[j]
			// = ρ·s[j]·Σ_k B[i][k]·y[k] = ρ·s[j]·(B·y)[i]
			By_i := 0.0
			for k := 0; k < n; k++ {
				By_i += B[i][k] * y[k]
			}

			// - (ρ·s·y^T)·B[i][j] = - ρ·s[i]·Σ_k y[k]·B[k][j]
			yTB_j := 0.0
			for k := 0; k < n; k++ {
				yTB_j += y[k] * B[k][j]
			}

			B[i][j] = B[i][j] -
				rho*s[j]*By_i -
				rho*s[i]*yTB_j +
				rho2yTBy*s[i]*s[j] +
				rho*s[i]*s[j]
		}
	}

	return true
}

// invertB attempts to compute B^{-1} via Cholesky decomposition. If B is
// positive-definite, it returns B_inv and true; otherwise nil, false.
func invertB(B [][]float64) ([][]float64, bool) {
	n := len(B)
	if n == 0 {
		return nil, false
	}

	// B is symmetric; use Cholesky to solve B·X = I column by column.
	// Since SolveCholesky only solves B·x = g, we solve for each column of I.
	BInv := make([][]float64, n)
	for i := range BInv {
		BInv[i] = make([]float64, n)
	}

	// Extract the Cholesky factor L from B.
	L := make([][]float64, n)
	for i := range L {
		L[i] = make([]float64, n)
		for j := range L[i] {
			L[i][j] = B[i][j]
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

	// Solve L·y = e_j (forward), then L^T·x = y (back) for each column j.
	e := make([]float64, n)
	for col := 0; col < n; col++ {
		// Forward: L·y = e_col
		for i := 0; i < n; i++ {
			e[i] = 0
		}
		e[col] = 1.0

		y := make([]float64, n)
		for i := 0; i < n; i++ {
			sum := e[i]
			for k := 0; k < i; k++ {
				sum -= L[i][k] * y[k]
			}
			y[i] = sum / L[i][i]
		}

		// Back: L^T·x = y
		x := make([]float64, n)
		for i := n - 1; i >= 0; i-- {
			sum := y[i]
			for k := i + 1; k < n; k++ {
				sum -= L[k][i] * x[k]
			}
			x[i] = sum / L[i][i]
		}

		for i := 0; i < n; i++ {
			BInv[i][col] = x[i]
		}
	}

	// Verify all entries are finite.
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if math.IsNaN(BInv[i][j]) || math.IsInf(BInv[i][j], 0) {
				return nil, false
			}
		}
	}

	return BInv, true
}
