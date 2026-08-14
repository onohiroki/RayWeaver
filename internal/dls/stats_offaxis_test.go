package dls

import (
	"math"
	"testing"
)

func TestComputeSpotWeightedRMS(t *testing.T) {
	// Four rays at radius 1 in a cross, weighted area = radius.
	points := []IPoint{
		{X: 1.0, Y: 0.0, OK: true, Area: 1.0},
		{X: -1.0, Y: 0.0, OK: true, Area: 1.0},
		{X: 0.0, Y: 1.0, OK: true, Area: 1.0},
		{X: 0.0, Y: -1.0, OK: true, Area: 1.0},
	}
	rms := ComputeSpotWeightedRMS(points)
	if math.Abs(rms-1.0) > 1e-10 {
		t.Errorf("weighted RMS = %v, want 1.0", rms)
	}

	// Same cross, but the +X ray carries 9x the weight (area*intensity):
	// the centroid shifts toward +X and the RMS collapses.
	weighted := []IPoint{
		{X: 1.0, Y: 0.0, OK: true, Area: 1.0, Intensity: 9.0},
		{X: -1.0, Y: 0.0, OK: true, Area: 1.0, Intensity: 1.0},
		{X: 0.0, Y: 1.0, OK: true, Area: 1.0, Intensity: 1.0},
		{X: 0.0, Y: -1.0, OK: true, Area: 1.0, Intensity: 1.0},
	}
	rmsW := ComputeSpotWeightedRMS(weighted)
	if rmsW >= 1.0 {
		t.Errorf("intensity-weighted RMS = %v, want < 1.0", rmsW)
	}

	allFailed := []IPoint{{OK: false}}
	if ComputeSpotWeightedRMS(allFailed) != 1e6 {
		t.Error("weighted RMS should return 1e6 for all-failed points")
	}
}

func TestComputeSpotWeightedRMSDegradesToEqual(t *testing.T) {
	// No area/intensity data: identical to ComputeSpotRMS.
	points := []IPoint{
		{X: 1.0, Y: 0.0, OK: true},
		{X: -1.0, Y: 0.0, OK: true},
		{X: 0.0, Y: 1.0, OK: true},
		{X: 0.0, Y: -1.0, OK: true},
	}
	if got, want := ComputeSpotWeightedRMS(points), ComputeSpotRMS(points); math.Abs(got-want) > 1e-12 {
		t.Errorf("unweighted degrade: weighted RMS = %v, legacy = %v", got, want)
	}
}

func TestComputeSpotAxisRMS(t *testing.T) {
	// Pure tangential flare along +Y (field azimuth Y): rays spread in Y,
	// none in X. RMS_T must equal the cross RMS about the centroid, RMS_S ~ 0.
	points := []IPoint{
		{X: 0.0, Y: 2.0, OK: true},
		{X: 0.0, Y: 1.0, OK: true},
		{X: 0.0, Y: 0.0, OK: true},
		{X: 0.0, Y: -1.0, OK: true},
	}
	rmsT, rmsS := ComputeSpotAxisRMS(points, 0, 1)
	if math.Abs(rmsT-ComputeSpotRMS(points)) > 1e-10 {
		t.Errorf("RMS_T = %v, want equal to full RMS %v", rmsT, ComputeSpotRMS(points))
	}
	if rmsS > 1e-10 {
		t.Errorf("RMS_S = %v, want ~0 for a pure tangential flare", rmsS)
	}

	// Field azimuth X: the same spread is now sagittal.
	rmsT2, rmsS2 := ComputeSpotAxisRMS(points, 1, 0)
	if rmsT2 > 1e-10 {
		t.Errorf("RMS_T = %v, want ~0 for a pure sagittal spread", rmsT2)
	}
	if math.Abs(rmsS2-ComputeSpotRMS(points)) > 1e-10 {
		t.Errorf("RMS_S = %v, want equal to full RMS %v", rmsS2, ComputeSpotRMS(points))
	}
}

func TestComputeSpotAxisRMSAllFailed(t *testing.T) {
	points := []IPoint{{OK: false}}
	rmsT, rmsS := ComputeSpotAxisRMS(points, 0, 1)
	if rmsT != 1e6 || rmsS != 1e6 {
		t.Errorf("axis RMS = (%v, %v), want (1e6, 1e6)", rmsT, rmsS)
	}
}

func TestComputeSpotEERadius(t *testing.T) {
	// Two rays: one at radius 0 (weight 10), one at radius 10 (weight 1).
	// The flux-weighted centroid sits at x = 10/11, so the inner ray is 10/11
	// from it and the outer ray 100/11. EE80 returns the inner ray's distance
	// (it holds >80% of the weight); EE100 returns the outer one.
	points := []IPoint{
		{X: 0.0, Y: 0.0, OK: true, Area: 1.0, Intensity: 10.0},
		{X: 10.0, Y: 0.0, OK: true, Area: 1.0, Intensity: 1.0},
	}
	inner, outer := 10.0/11.0, 100.0/11.0
	if r := ComputeSpotEERadius(points, 0.8); math.Abs(r-inner) > 1e-10 {
		t.Errorf("EE80 = %v, want %v (inner ray holds >80%% of weight)", r, inner)
	}
	if r := ComputeSpotEERadius(points, 1.0); math.Abs(r-outer) > 1e-10 {
		t.Errorf("EE100 = %v, want %v", r, outer)
	}

	// Default fraction (0.8) matches an explicit 0.8.
	if got, want := ComputeSpotEERadius(points, 0), ComputeSpotEERadius(points, 0.8); math.Abs(got-want) > 1e-12 {
		t.Errorf("default fraction = %v, want equal to EE80 %v", got, want)
	}
}

func TestComputeSpotEERadiusAllFailed(t *testing.T) {
	points := []IPoint{{OK: false}}
	if r := ComputeSpotEERadius(points, 0.8); r != 1e6 {
		t.Errorf("EE radius = %v, want 1e6 for all-failed points", r)
	}
}
