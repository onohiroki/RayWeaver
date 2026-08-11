package wavefront

import (
	"fmt"
	"math"

	"github.com/hiroki/rayweaver/internal/psf"
	"github.com/hiroki/rayweaver/internal/raymath"
)

// Zernike is a Fringe-Zernike decomposition of a wavefront residual.
// Coefficients use the Zemax convention: on the unit circle the peak value of
// the term equals its coefficient (each term's P-V on the unit pupil is 1).
type Zernike struct {
	Basis        string
	MaxOrder     int
	RemovedTerms []int
	Terms        []ZernikeTerm
	RMSResidual  float64
	PV           float64
}

// ZernikeTerm is one Fringe Zernike coefficient (mm).
type ZernikeTerm struct {
	Index       int
	Name        string
	Coefficient float64
}

// fringeNM is the (n, m) pair of Fringe Zernike index k (1..37) in the
// standard Zemax ordering. Positive m means the cos(m·θ) angular term, negative
// m the sin(|m|·θ) term, m = 0 the constant term.
var fringeNM = [38][2]int{
	0: {0, 0},   // unused
	1: {0, 0},   // Z1 piston
	2: {1, 1},   // Z2 tilt x
	3: {1, -1},  // Z3 tilt y
	4: {2, 0},   // Z4 defocus
	5: {2, 2},   // Z5 astigmatism 0°
	6: {2, -2},  // Z6 astigmatism 45°
	7: {3, 1},   // Z7 coma x
	8: {3, -1},  // Z8 coma y
	9: {4, 0},   // Z9 primary spherical
	10: {3, 3},  // Z10 trefoil x
	11: {3, -3}, // Z11 trefoil y
	12: {4, 2},  // Z12 secondary astigmatism x
	13: {4, -2}, // Z13 secondary astigmatism y
	14: {5, 1},  // Z14 secondary coma x
	15: {5, -1}, // Z15 secondary coma y
	16: {6, 0},  // Z16 secondary spherical
	17: {4, 4},  // Z17 tetrafoil x
	18: {4, -4}, // Z18 tetrafoil y
	19: {5, 3},  // Z19 secondary trefoil x
	20: {5, -3}, // Z20 secondary trefoil y
	21: {6, 2},  // Z21 secondary astigmatism x
	22: {6, -2}, // Z22 secondary astigmatism y
	23: {7, 1},  // Z23 tertiary coma x
	24: {7, -1}, // Z24 tertiary coma y
	25: {8, 0},  // Z25 tertiary spherical
	26: {5, 5},  // Z26 pentafoil x
	27: {5, -5}, // Z27 pentafoil y
	28: {6, 4},  // Z28 secondary tetrafoil x
	29: {6, -4}, // Z29 secondary tetrafoil y
	30: {7, 3},  // Z30 secondary trefoil x
	31: {7, -3}, // Z31 secondary trefoil y
	32: {8, 2},  // Z32 secondary astigmatism x
	33: {8, -2}, // Z33 secondary astigmatism y
	34: {9, 1},  // Z34 quaternary coma x
	35: {9, -1}, // Z35 quaternary coma y
	36: {10, 0}, // Z36 quaternary spherical
	37: {12, 0}, // Z37 quinary spherical
}

// fringeZernikeName returns the human-readable name of Fringe index k.
var fringeZernikeName = [38]string{
	0: "",
	1: "piston",
	2: "tilt x",
	3: "tilt y",
	4: "defocus",
	5: "astigmatism 0deg",
	6: "astigmatism 45deg",
	7: "primary coma x",
	8: "primary coma y",
	9: "primary spherical",
	10: "trefoil x",
	11: "trefoil y",
	12: "secondary astigmatism x",
	13: "secondary astigmatism y",
	14: "secondary coma x",
	15: "secondary coma y",
	16: "secondary spherical",
	17: "tetrafoil x",
	18: "tetrafoil y",
	19: "secondary trefoil x",
	20: "secondary trefoil y",
	21: "secondary astigmatism x",
	22: "secondary astigmatism y",
	23: "tertiary coma x",
	24: "tertiary coma y",
	25: "tertiary spherical",
	26: "pentafoil x",
	27: "pentafoil y",
	28: "secondary tetrafoil x",
	29: "secondary tetrafoil y",
	30: "secondary trefoil x",
	31: "secondary trefoil y",
	32: "secondary astigmatism x",
	33: "secondary astigmatism y",
	34: "quaternary coma x",
	35: "quaternary coma y",
	36: "quaternary spherical",
	37: "quinary spherical",
}

// fringeZernike evaluates Fringe Zernike index k at normalized pupil
// coordinates (rho >= 0, theta). rho must be <= 1 for a well-defined value.
func fringeZernike(k int, rho, theta float64) float64 {
	if k < 1 || k > 37 {
		return 0
	}
	nm := fringeNM[k]
	n, m := nm[0], nm[1]
	r := radialPoly(n, absInt(m), rho)
	switch {
	case m > 0:
		return r * math.Cos(float64(m)*theta)
	case m < 0:
		return r * math.Sin(float64(-m)*theta)
	default:
		return r
	}
}

// radialPoly evaluates the Zernike radial polynomial Rₙᵐ(ρ):
//
//	Rₙᵐ(ρ) = Σ_{s=0}^{(n-m)/2} (-1)^s (n-s)! / (s! ((n+m)/2-s)! ((n-m)/2-s)!) ρ^(n-2s)
func radialPoly(n, m int, rho float64) float64 {
	if n < m || (n-m)%2 != 0 {
		return 0
	}
	acc := 0.0
	for s := 0; s <= (n-m)/2; s++ {
		num := factorial(n - s)
		den := factorial(s) * factorial((n+m)/2-s) * factorial((n-m)/2-s)
		coef := num / den
		if s%2 == 1 {
			coef = -coef
		}
		acc += coef * math.Pow(rho, float64(n-2*s))
	}
	return acc
}

func factorial(v int) float64 {
	f := 1.0
	for i := 2; i <= v; i++ {
		f *= float64(i)
	}
	return f
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// FitZernike fits the Fringe-Zernike terms k = 7..maxOrder to the per-sample
// residual (wavefront minus the removed low-order model), weighted by the
// Delaunay areas. residual[i] corresponds to samples[i]. The pupil is
// normalized to the unit circle (centroid-centered, scaled by the maximum
// sample distance). The low-order terms (piston..astigmatism, Fringe 1-6) are
// excluded from the basis because the paraboloid + best-fit-sphere removal
// already accounts for them.
func FitZernike(samples []psf.WavefrontSample, residual []float64, maxOrder int) (Zernike, error) {
	if maxOrder <= 0 {
		maxOrder = 15
	}
	if maxOrder > 37 {
		maxOrder = 37
	}
	if maxOrder < 7 {
		maxOrder = 7
	}
	if len(samples) != len(residual) {
		return Zernike{}, fmt.Errorf("zernike fit: %d samples for %d residuals", len(samples), len(residual))
	}
	if len(samples) < 2 {
		return Zernike{}, fmt.Errorf("zernike fit needs >= 2 samples, got %d", len(samples))
	}

	// Pupil normalization: centroid-centered, scaled by max distance.
	cx, cy := 0.0, 0.0
	for _, s := range samples {
		cx += s.Position.X
		cy += s.Position.Y
	}
	nc := float64(len(samples))
	cx /= nc
	cy /= nc
	rMax := 0.0
	for _, s := range samples {
		r := math.Hypot(s.Position.X-cx, s.Position.Y-cy)
		if r > rMax {
			rMax = r
		}
	}
	if rMax <= 0 {
		rMax = 1
	}

	// Basis indices: 7..maxOrder (terms 1-6 removed).
	indices := make([]int, 0, maxOrder-6)
	for k := 7; k <= maxOrder; k++ {
		indices = append(indices, k)
	}
	dim := len(indices)

	// Normal equations AᵀWA c = AᵀW r.
	A := make([][]float64, dim)
	g := make([]float64, dim)
	for i := range A {
		A[i] = make([]float64, dim)
	}
	rMaxSq := rMax * rMax
	for i, s := range samples {
		w := s.Area
		if w <= 0 {
			w = 1
		}
		dx, dy := s.Position.X-cx, s.Position.Y-cy
		rho := math.Sqrt((dx*dx+dy*dy)/rMaxSq)
		theta := math.Atan2(dy, dx)
		resid := residual[i]
		cols := make([]float64, dim)
		for j, k := range indices {
			cols[j] = fringeZernike(k, rho, theta)
		}
		for a := 0; a < dim; a++ {
			g[a] += w * cols[a] * resid
			for b := 0; b < dim; b++ {
				A[a][b] += w * cols[a] * cols[b]
			}
		}
	}

	c := make([]float64, dim)
	copy(c, g)
	if !raymath.SolveLinear(A, c) {
		return Zernike{}, fmt.Errorf("zernike fit: singular normal matrix")
	}

	z := Zernike{
		Basis:        "Fringe",
		MaxOrder:     maxOrder,
		RemovedTerms: []int{1, 2, 3, 4, 5, 6},
	}
	for i, k := range indices {
		if math.Abs(c[i]) < 1e-15 {
			continue
		}
		z.Terms = append(z.Terms, ZernikeTerm{Index: k, Name: fringeZernikeName[k], Coefficient: c[i]})
	}

	// Residual RMS / PV of the fit: RMS of (residual - Σ c_k·Z_k).
	rMaxSq = rMax * rMax
	wSum, sqSum := 0.0, 0.0
	minV, maxV := math.Inf(1), math.Inf(-1)
	for i, s := range samples {
		w := s.Area
		if w <= 0 {
			w = 1
		}
		dx, dy := s.Position.X-cx, s.Position.Y-cy
		rho := math.Sqrt((dx*dx+dy*dy)/rMaxSq)
		theta := math.Atan2(dy, dx)
		fit := 0.0
		for j, k := range indices {
			fit += c[j] * fringeZernike(k, rho, theta)
		}
		d := residual[i] - fit
		wSum += w
		sqSum += w * d * d
		if d < minV {
			minV = d
		}
		if d > maxV {
			maxV = d
		}
	}
	if wSum > 0 {
		z.RMSResidual = math.Sqrt(sqSum / wSum)
	}
	if len(samples) > 0 {
		z.PV = maxV - minV
	}
	return z, nil
}
