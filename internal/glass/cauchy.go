package glass

import "github.com/hiroki/rayweaver/internal/raymath"

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

	raymath.SolveLinear(matrix, rhs)

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

// ConnectedCauchy returns ca shifted so that it passes through (lambdaBoundary,
// target), making it C0-continuous with the spline/table at the band boundary.
// Only the constant term A is adjusted, so the whole curve shifts uniformly.
func ConnectedCauchy(ca Cauchy, lambdaBoundary, target float64) Cauchy {
	connected := ca
	connected.A += target - ca.Eval(lambdaBoundary)
	return connected
}
