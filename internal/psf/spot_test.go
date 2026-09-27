package psf

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
)

func spotSample(x, y float64) WavefrontSample {
	return WavefrontSample{Position: types.Vec3{X: x, Y: y}, Direction: types.Vec3{Z: 1}, Area: 1, Intensity: 1}
}

// TestSpotDecomposeAxes verifies the x/y and tangential/sagittal decompositions
// of a pure-Y spread and the orthonormal invariant.
func TestSpotDecomposeAxes(t *testing.T) {
	// Two points symmetric about the origin, spread only in Y.
	s := []WavefrontSample{spotSample(0, -1), spotSample(0, 1)}
	r := spotDecompose(s, 0, 0, 1)
	if math.Abs(r.SpotRMS-1) > 1e-12 {
		t.Errorf("spot_rms = %v, want 1", r.SpotRMS)
	}
	if math.Abs(r.SpotRMSY-1) > 1e-12 || r.SpotRMSX > 1e-12 {
		t.Errorf("x/y = %v/%v, want 0/1", r.SpotRMSX, r.SpotRMSY)
	}
	if math.Abs(r.SpotRMST-1) > 1e-12 || r.SpotRMSS > 1e-12 {
		t.Errorf("t/s = %v/%v, want 1/0 (azimuth +Y)", r.SpotRMST, r.SpotRMSS)
	}
	if got := r.SpotRMS*r.SpotRMS - (r.SpotRMST*r.SpotRMST + r.SpotRMSS*r.SpotRMSS); math.Abs(got) > 1e-12 {
		t.Errorf("rms^2 != t^2+s^2 (diff %v)", got)
	}
	if got := r.SpotRMS*r.SpotRMS - (r.SpotRMSX*r.SpotRMSX + r.SpotRMSY*r.SpotRMSY); math.Abs(got) > 1e-12 {
		t.Errorf("rms^2 != x^2+y^2 (diff %v)", got)
	}

	// Rotate the azimuth to X: the same Y spread is now sagittal.
	r2 := spotDecompose(s, 0, 1, 0)
	if math.Abs(r2.SpotRMST) > 1e-12 {
		t.Errorf("t = %v, want 0 for azimuth +X", r2.SpotRMST)
	}
	if math.Abs(r2.SpotRMSS-1) > 1e-12 {
		t.Errorf("s = %v, want 1 for azimuth +X", r2.SpotRMSS)
	}
}

// TestSpotAzimuthDefault verifies the +Y default for a missing direction.
func TestSpotAzimuthDefault(t *testing.T) {
	if dx, dy := spotAzimuth(nil); dx != 0 || dy != 1 {
		t.Errorf("spotAzimuth(nil) = %v,%v, want 0,1", dx, dy)
	}
	if dx, dy := spotAzimuth([]float64{0, 0}); dx != 0 || dy != 1 {
		t.Errorf("spotAzimuth(0,0) = %v,%v, want 0,1", dx, dy)
	}
	if dx, dy := spotAzimuth([]float64{3, 4}); math.Abs(dx-0.6) > 1e-12 || math.Abs(dy-0.8) > 1e-12 {
		t.Errorf("spotAzimuth(3,4) = %v,%v, want 0.6,0.8", dx, dy)
	}
}
