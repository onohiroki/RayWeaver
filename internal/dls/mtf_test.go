package dls

import (
	"math"
	"testing"
)

func TestComputeGeometricMTF_Empty(t *testing.T) {
	sag, tan := ComputeGeometricMTF(nil, 50)
	if sag != 0 || tan != 0 {
		t.Errorf("nil slice: got (%f, %f), want (0, 0)", sag, tan)
	}

	sag, tan = ComputeGeometricMTF([]IPoint{}, 50)
	if sag != 0 || tan != 0 {
		t.Errorf("empty slice: got (%f, %f), want (0, 0)", sag, tan)
	}

	sag, tan = ComputeGeometricMTF([]IPoint{{OK: false}}, 50)
	if sag != 0 || tan != 0 {
		t.Errorf("all invalid: got (%f, %f), want (0, 0)", sag, tan)
	}
}

func TestComputeGeometricMTF_ZeroFrequency(t *testing.T) {
	// freq <= 0 returns (0, 0) by design
	pts := []IPoint{
		{X: 0, Y: 0, OK: true, Area: 1, Intensity: 1},
		{X: 1, Y: 1, OK: true, Area: 1, Intensity: 1},
	}
	sag, tan := ComputeGeometricMTF(pts, 0)
	if sag != 0 || tan != 0 {
		t.Errorf("freq=0: got (%f, %f), want (0, 0)", sag, tan)
	}
	sag, tan = ComputeGeometricMTF(pts, -1)
	if sag != 0 || tan != 0 {
		t.Errorf("freq=-1: got (%f, %f), want (0, 0)", sag, tan)
	}
}

func TestComputeGeometricMTF_SinglePoint(t *testing.T) {
	// Single point at origin: MTF = 1 at all frequencies
	pts := []IPoint{
		{X: 0, Y: 0, OK: true, Area: 1, Intensity: 1},
	}
	sag, tan := ComputeGeometricMTF(pts, 50)
	if sag != 1 || tan != 1 {
		t.Errorf("single point: got (%f, %f), want (1, 1)", sag, tan)
	}

	// Single point offset from origin: still MTF = 1 (centroid subtraction)
	pts = []IPoint{
		{X: 0.1, Y: 0.2, OK: true, Area: 1, Intensity: 1},
	}
	sag, tan = ComputeGeometricMTF(pts, 50)
	if sag != 1 || tan != 1 {
		t.Errorf("single offset point: got (%f, %f), want (1, 1)", sag, tan)
	}
}

func TestComputeGeometricMTF_TwoPoints_Symmetric(t *testing.T) {
	// Two points symmetric about origin: total separation D = 0.2 mm
	// MTF = |cos(π ν D)| for uniform weights
	// Zero crossing at ν = 1/(2D) = 2.5 lp/mm
	D := 0.2 // total separation
	pts := []IPoint{
		{X: -D/2, Y: 0, OK: true, Area: 1, Intensity: 1},
		{X: D/2, Y: 0, OK: true, Area: 1, Intensity: 1},
	}

	// At zero crossing
	sag, tan := ComputeGeometricMTF(pts, 2.5)
	if sag > 0.1 {
		t.Errorf("sag at zero crossing: got %f, want ~0", sag)
	}
	if math.Abs(tan-1) > 1e-10 {
		t.Errorf("tan should be 1: got %f", tan)
	}

	// At ν = 1/D = 5 → cos(π) = -1 → MTF = 1
	sag, tan = ComputeGeometricMTF(pts, 5.0)
	if math.Abs(sag-1) > 1e-10 {
		t.Errorf("sag at ν=1/D: got %f, want 1", sag)
	}
}

func TestComputeGeometricMTF_AstigmaticSpot(t *testing.T) {
	// Vertical line (astigmatic: elongated in Y → lower tangential MTF)
	// Use non-integer frequency to avoid 2π multiples
	pts := make([]IPoint, 9)
	for i := 0; i < 9; i++ {
		y := float64(i-4) * 0.05 // -0.2 to 0.2 in 0.05 steps
		pts[i] = IPoint{X: 0, Y: y, OK: true, Area: 1, Intensity: 1}
	}

	sag, tan := ComputeGeometricMTF(pts, 12.5) // 12.5 avoids 2π multiples for this spacing
	// Sagittal (X spread) is zero → MTF = 1
	// Tangential (Y spread) is large → MTF < 1
	if math.Abs(sag-1) > 1e-10 {
		t.Errorf("sag for vertical line: got %f, want 1", sag)
	}
	if tan >= 1 || tan <= 0 {
		t.Errorf("tan for vertical line: got %f, want 0 < tan < 1", tan)
	}

	// Horizontal line: opposite
	pts = make([]IPoint, 9)
	for i := 0; i < 9; i++ {
		x := float64(i-4) * 0.05
		pts[i] = IPoint{X: x, Y: 0, OK: true, Area: 1, Intensity: 1}
	}
	sag, tan = ComputeGeometricMTF(pts, 12.5)
	if sag >= 1 || sag <= 0 {
		t.Errorf("sag for horizontal line: got %f, want 0 < sag < 1", sag)
	}
	if math.Abs(tan-1) > 1e-10 {
		t.Errorf("tan for horizontal line: got %f, want 1", tan)
	}
}

func TestComputeGeometricMTF_Weighted(t *testing.T) {
	// Two points with different weights
	// X positions: -0.1 (w=1), 0.1 (w=2)
	// Unweighted centroid = 0, so phases at 50 lp/mm: 2π*50*0.1 = 10π → cos=1
	// Use a frequency that gives non-trivial result
	pts := []IPoint{
		{X: -0.1, Y: 0, OK: true, Area: 1, Intensity: 1},  // w=1
		{X: 0.1, Y: 0, OK: true, Area: 2, Intensity: 1},   // w=2
	}
	sag, tan := ComputeGeometricMTF(pts, 17.5) // 17.5 avoids 2π multiples
	if sag >= 1 || sag <= 0 {
		t.Errorf("weighted: got sag=%f, want 0<sag<1", sag)
	}
	if math.Abs(tan-1) > 1e-10 {
		t.Errorf("weighted tan: got %f, want 1", tan)
	}

	// Verify weights matter: compare with equal weights
	ptsEqual := []IPoint{
		{X: -0.1, Y: 0, OK: true, Area: 1, Intensity: 1},
		{X: 0.1, Y: 0, OK: true, Area: 1, Intensity: 1},
	}
	sagEq, _ := ComputeGeometricMTF(ptsEqual, 17.5)
	// Weighted should differ from equal-weighted
	if math.Abs(sag-sagEq) < 1e-10 {
		t.Errorf("weighted should differ from equal: both %f", sag)
	}
}

func TestComputeGeometricMTF_Clamping(t *testing.T) {
	// Numerical edge cases should not exceed 1
	pts := []IPoint{
		{X: 1e-10, Y: 0, OK: true, Area: 1, Intensity: 1},
		{X: -1e-10, Y: 0, OK: true, Area: 1, Intensity: 1},
	}
	sag, tan := ComputeGeometricMTF(pts, 1e6)
	if sag > 1 || tan > 1 {
		t.Errorf("clamping: got (%f, %f), both should be <= 1", sag, tan)
	}
}