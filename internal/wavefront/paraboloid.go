package wavefront

import (
	"fmt"
	"math"

	"github.com/hiroki/rayweaver/internal/psf"
	"github.com/hiroki/rayweaver/internal/raymath"
)

// Paraboloid is a least-squares quadratic fit
//
//	P(x,y) = a·x² + b·y² + c·xy + d·x + e·y + f
//
// to the sampled OPD on the reference surface, weighted by the Delaunay sample
// areas. All coefficients are in OPL units (mm); the derived magnitudes are
// the standard low-order decomposition:
//
//	defocus     = (a + b) / 2
//	astigmatism = sqrt(((a-b)/2)² + (c/2)²)
//	tilt        = sqrt(d² + e²)
type Paraboloid struct {
	X2, Y2, XY, X, Y, Constant float64
	Defocus, Astigmatism, Tilt float64
	// RMSResidual is the area-weighted RMS of OPD minus the paraboloid, in mm.
	RMSResidual float64
}

// FitParaboloid fits the 6-coefficient paraboloid to the wavefront samples'
// OPD values with area-weighted least squares (normal equations solved by
// Gaussian elimination with partial pivoting). At least 6 samples are needed.
func FitParaboloid(samples []psf.WavefrontSample) (Paraboloid, error) {
	n := len(samples)
	if n < 6 {
		return Paraboloid{}, fmt.Errorf("paraboloid fit needs >= 6 samples, got %d", n)
	}
	// Normal equations AᵀWA x = AᵀWb with columns [x², y², xy, x, y, 1].
	var m [6][7]float64
	for _, s := range samples {
		w := s.Area
		if w <= 0 {
			w = 1
		}
		x, y, o := s.Position.X, s.Position.Y, s.OPL
		cols := [6]float64{x * x, y * y, x * y, x, y, 1}
		for i := 0; i < 6; i++ {
			for j := 0; j < 6; j++ {
				m[i][j] += w * cols[i] * cols[j]
			}
			m[i][6] += w * cols[i] * o
		}
	}
	a := make([][]float64, 6)
	b := make([]float64, 6)
	for i := 0; i < 6; i++ {
		a[i] = make([]float64, 6)
		for j := 0; j < 6; j++ {
			a[i][j] = m[i][j]
		}
		b[i] = m[i][6]
	}
	if !raymath.SolveLinear(a, b) {
		return Paraboloid{}, fmt.Errorf("paraboloid fit: singular normal matrix")
	}
	p := Paraboloid{
		X2: b[0], Y2: b[1], XY: b[2],
		X: b[3], Y: b[4], Constant: b[5],
	}
	p.Defocus = (p.X2 + p.Y2) / 2
	astX := (p.X2 - p.Y2) / 2
	astY := p.XY / 2
	p.Astigmatism = math.Hypot(astX, astY)
	p.Tilt = math.Hypot(p.X, p.Y)
	p.RMSResidual = weightedRMS(samples, p.Eval)
	return p, nil
}

// Eval evaluates the paraboloid at (x, y).
func (p Paraboloid) Eval(x, y float64) float64 {
	return p.X2*x*x + p.Y2*y*y + p.XY*x*y + p.X*x + p.Y*y + p.Constant
}

// weightedRMS returns the area-weighted RMS of (s.OPL - model(x, y)) over the
// samples, in mm.
func weightedRMS(samples []psf.WavefrontSample, model func(x, y float64) float64) float64 {
	wSum, sqSum := 0.0, 0.0
	for _, s := range samples {
		w := s.Area
		if w <= 0 {
			w = 1
		}
		d := s.OPL - model(s.Position.X, s.Position.Y)
		wSum += w
		sqSum += w * d * d
	}
	if wSum <= 0 {
		return 0
	}
	return math.Sqrt(sqSum / wSum)
}

// weightedPV returns the area-weighted peak-to-valley of (s.OPL - model) over
// the samples, in mm.
func weightedPV(samples []psf.WavefrontSample, model func(x, y float64) float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	minV, maxV := math.Inf(1), math.Inf(-1)
	for _, s := range samples {
		d := s.OPL - model(s.Position.X, s.Position.Y)
		if d < minV {
			minV = d
		}
		if d > maxV {
			maxV = d
		}
	}
	return maxV - minV
}

// ReferenceSphere is the best-fit reference sphere OPD model
// S(x,y) = a + b·x + c·y + d·(x²+y²) — piston, tilt and defocus only. The
// residual is the standard wavefront aberration (astigmatism retained).
type ReferenceSphere struct {
	A, B, C, D float64
	RMSResidual float64
	PV         float64
}

// FitReferenceSphere fits the 4-coefficient piston/tilt/defocus model to the
// samples' OPD with area-weighted least squares, returning the residual RMS
// and PV. This is the wavefront-aberration definition used by `psf`
// (rms_opd/pv_opd).
func FitReferenceSphere(samples []psf.WavefrontSample) (ReferenceSphere, error) {
	n := len(samples)
	if n < 4 {
		return ReferenceSphere{}, fmt.Errorf("reference-sphere fit needs >= 4 samples, got %d", n)
	}
	// Normal equations AᵀWA x = AᵀWb with columns [1, x, y, x²+y²].
	var m [4][5]float64
	for _, s := range samples {
		w := s.Area
		if w <= 0 {
			w = 1
		}
		x, y, o := s.Position.X, s.Position.Y, s.OPL
		cols := [4]float64{1, x, y, x*x + y*y}
		for i := 0; i < 4; i++ {
			for j := 0; j < 4; j++ {
				m[i][j] += w * cols[i] * cols[j]
			}
			m[i][4] += w * cols[i] * o
		}
	}
	a := make([][]float64, 4)
	b := make([]float64, 4)
	for i := 0; i < 4; i++ {
		a[i] = make([]float64, 4)
		for j := 0; j < 4; j++ {
			a[i][j] = m[i][j]
		}
		b[i] = m[i][4]
	}
	if !raymath.SolveLinear(a, b) {
		return ReferenceSphere{}, fmt.Errorf("reference-sphere fit: singular normal matrix")
	}
	rs := ReferenceSphere{A: b[0], B: b[1], C: b[2], D: b[3]}
	model := func(x, y float64) float64 {
		return rs.A + rs.B*x + rs.C*y + rs.D*(x*x+y*y)
	}
	rs.RMSResidual = weightedRMS(samples, model)
	rs.PV = weightedPV(samples, model)
	return rs, nil
}

// Eval evaluates the reference-sphere model at (x, y).
func (rs ReferenceSphere) Eval(x, y float64) float64 {
	return rs.A + rs.B*x + rs.C*y + rs.D*(x*x+y*y)
}
