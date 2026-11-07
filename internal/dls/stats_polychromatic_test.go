package dls

import (
	"math"
	"testing"
)

func TestWhiteSpotRMSIncludesCentroidSpread(t *testing.T) {
	// Two equal-weight groups, each a single ray: the combined RMS is the
	// distance of each ray from the common centroid, i.e. half their spacing.
	a := []IPoint{{X: 0, Y: 0, OK: true}, {X: 0, Y: 0, OK: true}}
	b := []IPoint{{X: 2, Y: 0, OK: true}, {X: 2, Y: 0, OK: true}}
	got, ok := WhiteSpotRMS([][]IPoint{a, b}, []float64{0.5, 0.5})
	if !ok || math.Abs(got-1) > 1e-12 {
		t.Fatalf("WhiteSpotRMS = %v, ok=%v; want 1", got, ok)
	}
}

func TestWhiteSpotRMSWithinGroup(t *testing.T) {
	// One group with a ±1 spread about its centroid: RMS = 1.
	a := []IPoint{{X: -1, Y: 0, OK: true}, {X: 1, Y: 0, OK: true}}
	got, ok := WhiteSpotRMS([][]IPoint{a}, []float64{1})
	if !ok || math.Abs(got-1) > 1e-12 {
		t.Fatalf("WhiteSpotRMS = %v, ok=%v; want 1", got, ok)
	}
}

func TestWhiteSpotEERadius(t *testing.T) {
	a := []IPoint{{X: 0, Y: 0, OK: true}, {X: 10, Y: 0, OK: true}}
	got, ok := WhiteSpotEERadius([][]IPoint{a}, []float64{1}, 0.5)
	if !ok || math.Abs(got-5) > 1e-12 {
		t.Fatalf("WhiteSpotEERadius = %v, ok=%v; want 5", got, ok)
	}
}
