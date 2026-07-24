package glass

import "math"

type Cauchy struct {
	A, B, C, D float64
	Terms      int
}

func FitCauchy(knots []IndexEntry, terms int) Cauchy {
	n := len(knots)
	m := terms

	sx := make([]float64, 2*m)
	for i := 0; i < n; i++ {
		x := 1.0 / (knots[i].Wavelength * knots[i].Wavelength)
		xi := 1.0
		for j := 0; j < 2*m; j++ {
			sx[j] += xi
			xi *= x
		}
	}

	matrix := make([][]float64, m)
	rhs := make([]float64, m)
	for i := 0; i < m; i++ {
		matrix[i] = make([]float64, m)
		for j := 0; j < m; j++ {
			matrix[i][j] = sx[i+j]
		}
	}

	for i := 0; i < n; i++ {
		x := 1.0 / (knots[i].Wavelength * knots[i].Wavelength)
		y := knots[i].Index
		xi := 1.0
		for j := 0; j < m; j++ {
			rhs[j] += y * xi
			xi *= x
		}
	}

	solveLinear(m, matrix, rhs)

	c := Cauchy{Terms: terms}
	if m >= 1 {
		c.A = rhs[0]
	}
	if m >= 2 {
		c.B = rhs[1]
	}
	if m >= 3 {
		c.C = rhs[2]
	}
	if m >= 4 {
		c.D = rhs[3]
	}
	return c
}

func (c *Cauchy) Eval(lambda float64) float64 {
	x := 1.0 / (lambda * lambda)
	x2 := x * x
	n := c.A + c.B*x + c.C*x2
	if c.Terms >= 4 {
		n += c.D * x2 * x
	}
	return n
}

func solveLinear(n int, a [][]float64, b []float64) {
	for col := 0; col < n; col++ {
		pivot := col
		maxVal := math.Abs(a[col][col])
		for row := col + 1; row < n; row++ {
			if v := math.Abs(a[row][col]); v > maxVal {
				maxVal = v
				pivot = row
			}
		}
		if pivot != col {
			a[col], a[pivot] = a[pivot], a[col]
			b[col], b[pivot] = b[pivot], b[col]
		}

		for row := col + 1; row < n; row++ {
			factor := a[row][col] / a[col][col]
			for j := col; j < n; j++ {
				a[row][j] -= factor * a[col][j]
			}
			b[row] -= factor * b[col]
		}
	}

	for i := n - 1; i >= 0; i-- {
		sum := b[i]
		for j := i + 1; j < n; j++ {
			sum -= a[i][j] * b[j]
		}
		b[i] = sum / a[i][i]
	}
}
