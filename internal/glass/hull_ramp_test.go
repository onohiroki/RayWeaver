package glass

import (
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
)

// TestConvexHullPenaltyRampsOutside guards the P3 fix: the penalty was a flat
// 1e6 beyond the smooth band, with zero gradient, so a point that overshot the
// band settled just outside the hull with no restoring force and the escape
// later rejected it as glass_hull_violation. The penalty must now keep a slope
// outside the band (bounded, saturating far outside).
func TestConvexHullPenaltyRampsOutside(t *testing.T) {
	cat := makeTestCatalog([]types.Glass{
		{Key: "N-BK7", ND: 1.5168, VD: 64.17},
		{Key: "N-SF2", ND: 1.64769, VD: 33.82},
		{Key: "N-LAK9", ND: 1.691, VD: 54.71},
		{Key: "N-LAF21", ND: 1.72, VD: 48.0},
	})
	hull := NewDefaultConvexHull(cat)
	if hull == nil || !hull.Enabled() {
		t.Fatal("expected enabled hull")
	}

	// Centroid, then walk in +nd at the centroid's vd until the hull boundary
	// is crossed. Staying at a constant vd keeps the probe inside the hull's
	// bounding box, so the test exercises the band/ramp rather than the box
	// clamp.
	cx, cy := 0.0, 0.0
	for _, v := range hull.hull {
		cx += v.nd
		cy += v.vd
	}
	cx /= float64(len(hull.hull))
	cy /= float64(len(hull.hull))

	ndEdge := cx
	for hull.Contains(ndEdge, cy) && ndEdge < cx+20 {
		ndEdge += 0.002
	}
	if ndEdge >= cx+20 {
		t.Fatal("did not reach the hull boundary along +nd")
	}

	const w = 1.0
	prev := -1.0
	for _, step := range []float64{0.02, 0.05, 0.1, 0.2} {
		p := hull.Penalty(ndEdge+step, cy, 0.02, w)
		if p <= 0 {
			t.Fatalf("penalty %.2f beyond the boundary = %v, want > 0", step, p)
		}
		if p >= hullHardPenalty {
			t.Fatalf("penalty %.2f beyond the boundary is a flat hard cap (%v): no gradient", step, p)
		}
		if prev >= 0 && p <= prev {
			t.Errorf("penalty did not increase moving outside: %v then %v (step %.2f)", prev, p, step)
		}
		prev = p
	}

	// Far outside (still inside the box would leave the ramp unsaturated, so
	// use a point beyond the box) saturates at the bounded hard penalty.
	if got := hull.Penalty(cx+1e3, cy, 0.02, w); got != hullHardPenalty {
		t.Errorf("far-outside penalty = %v, want the bounded hard penalty %v", got, hullHardPenalty)
	}
}
