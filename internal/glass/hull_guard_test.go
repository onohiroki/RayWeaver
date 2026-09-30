package glass

import (
	"math"
	"testing"
)

// findBoundaryAlongND walks +nd from cx at constant vd until the hull boundary
// is crossed, returning the first nd outside the hull (kept inside the
// bounding box so the box clamp never interferes).
func findBoundaryAlongND(t *testing.T, h *ConvexHull, cx, cy float64) float64 {
	t.Helper()
	ndEdge := cx
	for h.Contains(ndEdge, cy) && ndEdge < cx+20 {
		ndEdge += 0.0005
	}
	if ndEdge >= cx+20 {
		t.Fatal("did not reach the hull boundary along +nd")
	}
	return ndEdge
}

func hullCentroid(h *ConvexHull) (float64, float64) {
	cx, cy := 0.0, 0.0
	for _, v := range h.hull {
		cx += v.nd
		cy += v.vd
	}
	n := float64(len(h.hull))
	return cx / n, cy / n
}

// TestConvexHullGuardBandShiftsWallInside verifies the guard band's purpose:
// with guard_band on, the penalty's steep wall (ramp onset) starts INSIDE the
// true hull, so the DLS balance point rests inside and the escape validator
// (Contains) accepts it. Without the guard the band is centred on the
// boundary, so the ramp — and with it the balance point — sits outside.
func TestConvexHullGuardBandShiftsWallInside(t *testing.T) {
	guarded := NewBuiltinConvexHull()
	plain := NewBuiltinConvexHull()
	if guarded == nil || plain == nil || !guarded.Enabled() {
		t.Fatal("expected enabled builtin hull")
	}
	guarded.SetGuardBand(0.01) // default (on)

	cx, cy := hullCentroid(guarded)
	ndEdge := findBoundaryAlongND(t, guarded, cx, cy)

	const w = 1.0
	// Just outside the boundary the guard must already be on the steep ramp
	// (the wall starts guardDist-offset inside), far above the unguarded band.
	outside := plain.Penalty(ndEdge+0.005, cy, 0.02, w)
	guardedOutside := guarded.Penalty(ndEdge+0.005, cy, 0.02, w)
	if guardedOutside <= outside*100 {
		t.Errorf("guard on: penalty just outside = %v, want >> unguarded %v", guardedOutside, outside)
	}

	// Inside the hull the guarded penalty must decrease with depth: the wall
	// is a shell around the boundary, so a point deeper inside is cheaper.
	near := guarded.Penalty(ndEdge-0.01, cy, 0.02, w)
	mid := guarded.Penalty(ndEdge-0.05, cy, 0.02, w)
	if !(near > mid) {
		t.Errorf("guard on: expected penalty to decrease with depth (0.01 in = %v, 0.05 in = %v)", near, mid)
	}

	// Deep interior (the centroid) must be untouched by the guard: the cap at
	// half the centroid clearance keeps the guarded zone a boundary shell, so
	// the interior landscape a glass optimisation sees is unchanged.
	if math.Abs(guarded.Penalty(cx, cy, 0.02, w)-plain.Penalty(cx, cy, 0.02, w)) > 1e-9 {
		t.Errorf("guard on changed the deep-interior penalty: %v vs %v",
			guarded.Penalty(cx, cy, 0.02, w), plain.Penalty(cx, cy, 0.02, w))
	}
}

// TestConvexHullGuardBandKeepsVertexZero: exact catalog points stay at zero
// penalty with the guard on (the vertex exemption is checked before the
// guard-shifted ramp), so snapping onto a real glass is never punished.
func TestConvexHullGuardBandKeepsVertexZero(t *testing.T) {
	h := NewBuiltinConvexHull()
	if h == nil || !h.Enabled() {
		t.Fatal("expected enabled builtin hull")
	}
	h.SetGuardBand(0.01)
	v := h.hull[0]
	if p := h.Penalty(v.nd, v.vd, 0.02, 1.0); p != 0 {
		t.Errorf("penalty at exact vertex with guard on = %v, want 0", p)
	}
}

// TestConvexHullGuardBandDisabled: guard_band 0 restores the unguarded
// landscape exactly (the band stays centred on the boundary).
func TestConvexHullGuardBandDisabled(t *testing.T) {
	guarded := NewBuiltinConvexHull()
	plain := NewBuiltinConvexHull()
	if guarded == nil || plain == nil {
		t.Fatal("expected enabled hulls")
	}
	guarded.SetGuardBand(0)
	guarded.SetGuardBand(-1) // clamped to 0
	if guarded.GuardBand() != 0 {
		t.Fatalf("negative guard band should clamp to 0, got %v", guarded.GuardBand())
	}

	cx, cy := hullCentroid(guarded)
	ndEdge := findBoundaryAlongND(t, guarded, cx, cy)
	for _, delta := range []float64{-0.05, -0.01, 0, 0.01, 0.05} {
		a := plain.Penalty(ndEdge+delta, cy, 0.02, 1.0)
		b := guarded.Penalty(ndEdge+delta, cy, 0.02, 1.0)
		if math.Abs(a-b) > 1e-12 {
			t.Errorf("guard disabled: penalty at delta=%v differs: %v vs %v", delta, a, b)
		}
	}
}

// TestConvexHullEscapeWeightFactor: setter/getter plumbing with clamping
// (0 = no phase scaling; the optimizer reads it in hullPhaseFactor).
func TestConvexHullEscapeWeightFactor(t *testing.T) {
	h := NewBuiltinConvexHull()
	if h == nil {
		t.Fatal("expected hull")
	}
	if h.EscapeWeightFactor() != 0 {
		t.Errorf("default escape factor = %v, want 0 (no phase scaling)", h.EscapeWeightFactor())
	}
	h.SetEscapeWeightFactor(0.1)
	if h.EscapeWeightFactor() != 0.1 {
		t.Errorf("escape factor = %v, want 0.1", h.EscapeWeightFactor())
	}
	h.SetEscapeWeightFactor(-2) // clamped to 0
	if h.EscapeWeightFactor() != 0 {
		t.Errorf("negative escape factor should clamp to 0, got %v", h.EscapeWeightFactor())
	}
}
