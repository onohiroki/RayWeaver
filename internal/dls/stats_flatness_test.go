package dls

import (
	"math"
	"testing"
)

// gridPoints builds a synthetic image-plane spot from a displacement model:
// d(rho) = f(rho) + offset, sampled on a square pupil grid with unit weights.
// A nil offset means no chief-ray offset.
func gridPoints(f func(rx, ry float64) (dx, dy float64), offset []float64, n int) []IPoint {
	var ox, oy float64
	if len(offset) == 2 {
		ox, oy = offset[0], offset[1]
	}
	pts := make([]IPoint, 0, n*n)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			px := -1 + 2*float64(i)/float64(n-1)
			py := -1 + 2*float64(j)/float64(n-1)
			dx, dy := f(px, py)
			pts = append(pts, IPoint{
				X: dx + ox, Y: dy + oy,
				PupilX: px, PupilY: py,
				OK: true, Area: 1, Intensity: 1,
			})
		}
	}
	return pts
}

func TestComputeSpotDecompDefocus(t *testing.T) {
	const alpha = 0.037
	pts := gridPoints(func(rx, ry float64) (float64, float64) {
		return alpha * rx, alpha * ry
	}, []float64{0.4, -0.2}, 9)
	d := ComputeSpotDecomp(pts)
	if !d.OK {
		t.Fatalf("fit not OK: %+v", d)
	}
	if math.Abs(d.Defocus-alpha) > 1e-12 {
		t.Errorf("Defocus = %.12f, want %.12f", d.Defocus, alpha)
	}
	if math.Abs(d.Astig) > 1e-12 {
		t.Errorf("Astig = %.3g, want 0", d.Astig)
	}
	// The chief-ray offset must not leak into the slopes.
	if math.Abs(d.Residual) > 1e-12 {
		t.Errorf("Residual = %.3g, want 0", d.Residual)
	}
	if d.Count != 81 {
		t.Errorf("Count = %d, want 81", d.Count)
	}
}

func TestComputeSpotDecompAstigmatism(t *testing.T) {
	const s = 0.021
	pts := gridPoints(func(rx, ry float64) (float64, float64) {
		return s * rx, -s * ry
	}, nil, 9)
	d := ComputeSpotDecomp(pts)
	if !d.OK {
		t.Fatalf("fit not OK: %+v", d)
	}
	if math.Abs(d.Astig-s) > 1e-12 {
		t.Errorf("Astig = %.12f, want %.12f", d.Astig, s)
	}
	if math.Abs(d.Defocus) > 1e-12 {
		t.Errorf("Defocus = %.3g, want 0", d.Defocus)
	}
	// 45-degree astigmatism: the shear term, also traceless symmetric.
	pts = gridPoints(func(rx, ry float64) (float64, float64) {
		return s * ry, s * rx
	}, nil, 9)
	d = ComputeSpotDecomp(pts)
	if !d.OK {
		t.Fatalf("fit not OK: %+v", d)
	}
	if math.Abs(d.Astig-s) > 1e-12 {
		t.Errorf("45deg Astig = %.12f, want %.12f", d.Astig, s)
	}
	if math.Abs(d.Defocus) > 1e-12 {
		t.Errorf("45deg Defocus = %.3g, want 0", d.Defocus)
	}
}

// TestComputeSpotDecompRotationAndTilt pins that the two magnitudes only see
// the focus terms: a pure image rotation (antisymmetric A) and a constant
// chief-ray offset must both read zero.
func TestComputeSpotDecompRotationAndTilt(t *testing.T) {
	const r = 0.017
	pts := gridPoints(func(rx, ry float64) (float64, float64) {
		return -r * ry, r * rx
	}, []float64{0.7, 0.3}, 9)
	d := ComputeSpotDecomp(pts)
	if !d.OK {
		t.Fatalf("fit not OK: %+v", d)
	}
	if math.Abs(d.Defocus) > 1e-12 || math.Abs(d.Astig) > 1e-12 {
		t.Errorf("Defocus = %.3g Astig = %.3g, want 0 for a pure rotation", d.Defocus, d.Astig)
	}
	if math.Abs(d.Rotation-r) > 1e-12 {
		t.Errorf("Rotation = %.12f, want %.12f", d.Rotation, r)
	}
}

// TestComputeSpotDecompQuadraticKeepsFocusTerm checks the fit on realistic
// content. Two models:
//
//   - an even (coma-like) quadratic term is orthogonal to the linear basis on a
//     symmetric pupil, so the defocus estimate stays exact and the unexplained
//     displacement shows up as the residual;
//   - a radial (spherical, cubic) term is odd and partially correlates with the
//     linear term, so the LSQ projection biases Defocus upward by O(q) — the
//     test pins that the bias stays within the term's own magnitude.
func TestComputeSpotDecompQuadraticKeepsFocusTerm(t *testing.T) {
	const alpha = 0.03
	pts := gridPoints(func(rx, ry float64) (float64, float64) {
		return alpha*rx + 0.02*rx*rx, alpha*ry + 0.02*ry*ry
	}, nil, 11)
	d := ComputeSpotDecomp(pts)
	if !d.OK {
		t.Fatalf("fit not OK: %+v", d)
	}
	if math.Abs(d.Defocus-alpha) > 1e-12 {
		t.Errorf("Defocus = %.12f, want %.12f", d.Defocus, alpha)
	}
	if math.Abs(d.Astig) > 1e-12 {
		t.Errorf("Astig = %.3g, want 0 for an even quadratic", d.Astig)
	}
	if d.Residual <= 0 {
		t.Errorf("Residual = %v, want > 0 for quadratic content", d.Residual)
	}

	const q = 0.01
	pts = gridPoints(func(rx, ry float64) (float64, float64) {
		f := q * (rx*rx + ry*ry)
		return alpha*rx + f*rx, alpha*ry + f*ry
	}, nil, 11)
	d = ComputeSpotDecomp(pts)
	if !d.OK {
		t.Fatalf("fit not OK: %+v", d)
	}
	if d.Defocus <= alpha || d.Defocus > alpha+2*q {
		t.Errorf("Defocus = %.4f, want %.4f < value <= %.4f (LSQ projection of a spherical term)",
			d.Defocus, alpha, alpha+2*q)
	}
	if d.Residual <= 0 {
		t.Errorf("Residual = %v, want > 0 for cubic content", d.Residual)
	}
}

func TestComputeSpotDecompDegenerate(t *testing.T) {
	if d := ComputeSpotDecomp(nil); d.OK {
		t.Errorf("empty grid: OK = true, want false")
	}
	// Fewer than four surviving rays: no fit.
	few := gridPoints(func(rx, ry float64) (float64, float64) { return rx, ry }, nil, 2)
	few[0].OK = false
	few[1].OK = false
	if d := ComputeSpotDecomp(few); d.OK {
		t.Errorf("2 surviving rays: OK = true, want false (min points)")
	}
	// Collinear samples: singular pupil second moments.
	line := make([]IPoint, 0, 10)
	for i := 0; i < 10; i++ {
		x := -1 + 2*float64(i)/9
		line = append(line, IPoint{X: 0.5 * x, Y: 0, PupilX: x, PupilY: 0, OK: true, Area: 1, Intensity: 1})
	}
	if d := ComputeSpotDecomp(line); d.OK {
		t.Errorf("collinear pupil: OK = true, want false")
	}
	// All rays rejected.
	dead := gridPoints(func(rx, ry float64) (float64, float64) { return rx, ry }, nil, 5)
	for i := range dead {
		dead[i].OK = false
	}
	if d := ComputeSpotDecomp(dead); d.OK || d.Count != 0 {
		t.Errorf("dead grid: OK = %v Count = %d, want false/0", d.OK, d.Count)
	}
}

// TestComputeSpotDecompVignettedEllipse fits a decently filled off-centre
// elliptical pupil (the realistic vignetted case) and checks the slopes are
// still recovered exactly: centring the fit on the sample means makes the
// answer independent of where the surviving pupil sits.
func TestComputeSpotDecompVignettedEllipse(t *testing.T) {
	const alpha = 0.045
	var pts []IPoint
	for i := 0; i < 17; i++ {
		for j := 0; j < 17; j++ {
			ux := -1 + 2*float64(i)/16
			uy := -1 + 2*float64(j)/16
			if (ux-0.3)*(ux-0.3)+0.25*uy*uy > 1 {
				continue // elliptical, decentered pupil
			}
			rx, ry := 0.6+0.4*ux, 0.4*uy
			pts = append(pts, IPoint{
				X: alpha * rx, Y: alpha * ry,
				PupilX: rx, PupilY: ry,
				OK: true, Area: 1, Intensity: 1,
			})
		}
	}
	d := ComputeSpotDecomp(pts)
	if !d.OK {
		t.Fatalf("fit not OK: %+v", d)
	}
	if math.Abs(d.Defocus-alpha) > 1e-12 {
		t.Errorf("Defocus = %.12f, want %.12f", d.Defocus, alpha)
	}
}
