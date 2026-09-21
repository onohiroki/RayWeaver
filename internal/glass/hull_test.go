package glass

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
)

func makeTestCatalog(glasses []types.Glass) *Catalog {
	cat := NewCatalog()
	for _, g := range glasses {
		cat.Add(g)
	}
	return cat
}

func TestConvexHullPenaltyInside(t *testing.T) {
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
	inside := hull.Penalty(1.60, 48.0, 0.02, 1.0)
	if inside > 0.1 {
		t.Errorf("expected small penalty inside hull, got %f", inside)
	}
}

func TestConvexHullPenaltyExactVertex(t *testing.T) {
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
	atVertex := hull.Penalty(1.64769, 33.82, 0.02, 1.0)
	if atVertex != 0 {
		t.Errorf("expected zero penalty at exact vertex, got %f", atVertex)
	}
}

func TestConvexHullPenaltyOutside(t *testing.T) {
	cat := makeTestCatalog([]types.Glass{
		{Key: "N-BK7", ND: 1.5168, VD: 64.17},
		{Key: "N-SF2", ND: 1.64769, VD: 33.82},
		{Key: "N-LAK9", ND: 1.691, VD: 54.71},
	})
	hull := NewDefaultConvexHull(cat)
	if hull == nil || !hull.Enabled() {
		t.Fatal("expected enabled hull")
	}
	outside := hull.Penalty(2.5, 80.0, 0.02, 1.0)
	if outside < 1e5 {
		t.Errorf("expected large penalty outside hull, got %f", outside)
	}
}

func TestConvexHullEnforceBounds(t *testing.T) {
	cat := makeTestCatalog([]types.Glass{
		{Key: "N-BK7", ND: 1.5168, VD: 64.17},
		{Key: "N-SF2", ND: 1.64769, VD: 33.82},
		{Key: "N-LAK9", ND: 1.691, VD: 54.71},
	})
	hull := NewDefaultConvexHull(cat)
	nd, vd := hull.EnforceBounds(2.5, 80.0)
	if nd >= 2.5 || vd >= 80.0 {
		t.Errorf("expected enforced bounds to pull point inward, got nd=%f vd=%f", nd, vd)
	}
	if nd <= 0 || vd <= 0 {
		t.Errorf("expected positive nd/vd after enforcement, got nd=%f vd=%f", nd, vd)
	}
}

func TestConvexHullNegatives(t *testing.T) {
	cat := makeTestCatalog([]types.Glass{
		{Key: "N-BK7", ND: 1.5168, VD: 64.17},
		{Key: "N-SF2", ND: 1.64769, VD: 33.82},
	})
	hull := NewDefaultConvexHull(cat)
	nd, vd := hull.EnforceBounds(-0.5, -3.0)
	if nd <= 0 || vd <= 0 {
		t.Errorf("expected positive nd/vd, got nd=%f vd=%f", nd, vd)
	}
}

func TestConvexHullBarycentricInside(t *testing.T) {
	cat := makeTestCatalog([]types.Glass{
		{Key: "N-BK7", ND: 1.5168, VD: 64.17},
		{Key: "N-SF2", ND: 1.64769, VD: 33.82},
		{Key: "N-LAK9", ND: 1.691, VD: 54.71},
	})
	hull := NewDefaultConvexHull(cat)
	if hull == nil || len(hull.hull) < 3 {
		t.Fatal("expected hull with >=3 vertices")
	}
	nd, vd := 1.60, 48.0
	coords, dist := hull.Barycentric(nd, vd)
	if len(coords) != len(hull.hull) {
		t.Fatalf("expected %d coords, got %d", len(hull.hull), len(coords))
	}
	if dist < -1e-9 {
		t.Errorf("expected non-negative distance inside hull, got %f", dist)
	}
	sum := 0.0
	for _, c := range coords {
		sum += c
	}
	if math.Abs(sum-1.0) > 1e-6 {
		t.Errorf("expected barycentric coords to sum to 1.0, got %f", sum)
	}
}

func TestConvexHullBarycentricOutside(t *testing.T) {
	cat := makeTestCatalog([]types.Glass{
		{Key: "N-BK7", ND: 1.5168, VD: 64.17},
		{Key: "N-SF2", ND: 1.64769, VD: 33.82},
		{Key: "N-LAK9", ND: 1.691, VD: 54.71},
	})
	hull := NewDefaultConvexHull(cat)
	nd, vd := 2.5, 80.0
	_, dist := hull.Barycentric(nd, vd)
	if dist > 1e-9 {
		t.Errorf("expected negative distance outside hull, got %f", dist)
	}
}

func TestConvexHullTooFewPoints(t *testing.T) {
	cat := makeTestCatalog([]types.Glass{
		{Key: "N-BK7", ND: 1.5168, VD: 64.17},
	})
	// NewConvexHull keeps the raw (degenerate) point set.
	hull := NewConvexHull(cat, DefaultGlassKindOrder)
	if !hull.Enabled() {
		t.Fatal("hull should be enabled even with 1 point (legacy fallback)")
	}
	pen := hull.Penalty(2.5, 80.0, 0.02, 1.0)
	if pen > 0 {
		t.Errorf("expected zero penalty with legacy hull, got %f", pen)
	}
}

func TestNewDefaultConvexHullFallback(t *testing.T) {
	// A catalogue too small to form a hull must fall back to the built-in
	// full real-glass hull so the constraint never disappears.
	cat := makeTestCatalog([]types.Glass{
		{Key: "N-BK7", ND: 1.5168, VD: 64.17},
		{Key: "N-SF2", ND: 1.64769, VD: 33.82},
	})
	hull := NewDefaultConvexHull(cat)
	if hull == nil || !hull.Enabled() {
		t.Fatal("expected enabled fallback hull")
	}
	if len(hull.hull) < 3 {
		t.Fatalf("expected fallback hull with >=3 vertices, got %d", len(hull.hull))
	}
	// A point in the broad real-glass region is contained.
	if !hull.Contains(1.60, 48.0) {
		t.Error("expected a mid-range glass to be contained by the fallback hull")
	}
	// A clearly unphysical point is outside.
	if hull.Contains(2.9, 5.0) {
		t.Error("expected an unphysical glass to be outside the fallback hull")
	}

	// Nil catalogue also yields the fallback.
	if h := NewDefaultConvexHull(nil); h == nil || len(h.hull) < 3 {
		t.Error("expected nil catalogue to yield the fallback hull")
	}
}

func TestNewBuiltinConvexHull(t *testing.T) {
	h := NewBuiltinConvexHull()
	if h == nil || !h.Enabled() || len(h.hull) < 3 {
		t.Fatal("expected the built-in full real-glass hull")
	}
	if !h.Contains(1.60, 48.0) {
		t.Error("expected a mid-range glass to be contained")
	}
	if h.Contains(2.9, 5.0) {
		t.Error("expected an unphysical glass to be outside")
	}
}

func TestNewUnionConvexHull(t *testing.T) {
	// A catalogue glass inside the built-in region does not change the hull.
	inside := makeTestCatalog([]types.Glass{{Key: "N-BK7", ND: 1.5168, VD: 64.17}})
	h := NewUnionConvexHull(inside)
	if h == nil || len(h.hull) < 3 {
		t.Fatal("expected a union hull even with a tiny catalogue")
	}
	if !h.Contains(1.60, 48.0) {
		t.Error("union: expected a mid-range glass to be contained")
	}

	// A catalogue glass outside the built-in region widens the hull.
	// (nd > DefaultHullNDMax, so it must be a new vertex.)
	outside := makeTestCatalog([]types.Glass{{Key: "X-OUT", ND: 2.35, VD: 40.0}})
	h2 := NewUnionConvexHull(outside)
	if h2 == nil {
		t.Fatal("expected a union hull")
	}
	if !h2.Contains(2.35, 40.0) {
		t.Error("union: expected an outside catalogue glass to be admitted")
	}

	// A nil catalogue still yields the built-in region.
	if h3 := NewUnionConvexHull(nil); h3 == nil || !h3.Contains(1.60, 48.0) {
		t.Error("union: nil catalogue should yield the built-in region")
	}
}

func TestNewConvexHullFromPoints(t *testing.T) {
	tri := []Point2D{{1.45, 70.0}, {1.75, 70.0}, {1.60, 30.0}}
	h := NewConvexHullFromPoints(tri)
	if h == nil {
		t.Fatal("expected a hull from 3 points")
	}
	if !h.Contains(1.60, 50.0) {
		t.Error("expected the triangle's interior point to be contained")
	}
	if h.Contains(1.42, 100.0) {
		t.Error("expected a far point to be outside the triangle")
	}

	// Invalid (non-positive) points are dropped; too few remain -> nil.
	if h := NewConvexHullFromPoints([]Point2D{{-1, 60}, {1.6, -2}}); h != nil {
		t.Error("expected nil when fewer than 3 valid points remain")
	}
	// Collinear points cannot form a hull -> nil. (Same vd keeps the cross
	// product exactly zero, so the degeneracy is not a floating-point artifact.)
	if h := NewConvexHullFromPoints([]Point2D{{1.5, 60}, {1.6, 60}, {1.7, 60}}); h != nil {
		t.Error("expected nil for collinear points")
	}
}

func TestConvexHullZeroWeight(t *testing.T) {
	cat := makeTestCatalog([]types.Glass{
		{Key: "N-BK7", ND: 1.5168, VD: 64.17},
		{Key: "N-SF2", ND: 1.64769, VD: 33.82},
		{Key: "N-LAK9", ND: 1.691, VD: 54.71},
	})
	hull := NewDefaultConvexHull(cat)
	inside := hull.Penalty(1.60, 48.0, 0.02, 0)
	if inside != 0 {
		t.Errorf("expected zero penalty with weight=0, got %f", inside)
	}
	outside := hull.Penalty(2.5, 80.0, 0.02, 0)
	if outside != 0 {
		t.Errorf("expected zero penalty outside with weight=0, got %f", outside)
	}
}

func TestConvexHullTinyRange(t *testing.T) {
	cat := makeTestCatalog([]types.Glass{
		{Key: "N-A", ND: 1.5, VD: 60.0},
		{Key: "N-B", ND: 1.5001, VD: 60.001},
	})
	// NewConvexHull keeps the raw 2-point set (legacy degenerate path).
	hull := NewConvexHull(cat, DefaultGlassKindOrder)
	if hull == nil || !hull.Enabled() {
		t.Fatal("expected enabled hull")
	}
	pen := hull.Penalty(1.50005, 60.0005, 0.02, 1.0)
	if pen > 0.01 {
		t.Errorf("expected near-zero penalty with legacy hull, got %f", pen)
	}
}

func TestConvexHullBoundaryClosure(t *testing.T) {
	cat := makeTestCatalog([]types.Glass{
		{Key: "N-BK7", ND: 1.5168, VD: 64.17},
		{Key: "N-SF2", ND: 1.64769, VD: 33.82},
		{Key: "N-LAK9", ND: 1.691, VD: 54.71},
		{Key: "N-LAF21", ND: 1.72, VD: 48.0},
	})
	hull := NewDefaultConvexHull(cat)
	if len(hull.hull) < 3 {
		t.Fatal("expected hull with >=3 vertices")
	}

	// Hull vertices are in CCW order; boundary distance wraps via modular indexing.
	// Verify all vertices are inside (distance >= 0).
	for _, v := range hull.hull {
		d := signedDistanceToHullBoundary(hull.hull, v.nd, v.vd)
		if d < -1e-6 {
			t.Errorf("hull vertex (%f,%f) is outside the hull boundary (dist=%f)", v.nd, v.vd, d)
		}
	}
}

func TestConvexHullReferences(t *testing.T) {
	cat := makeTestCatalog([]types.Glass{
		{Key: "N-BK7", ND: 1.5168, VD: 64.17},
		{Key: "N-SF2", ND: 1.64769, VD: 33.82},
		{Key: "N-LAK9", ND: 1.691, VD: 54.71},
	})
	hull := NewDefaultConvexHull(cat)
	refs := hull.References()
	if len(refs) == 0 {
		t.Error("expected non-empty references")
	}
	for _, r := range refs {
		if r.Name == "" {
			t.Error("expected non-empty reference name")
		}
		if r.Kind == "" {
			t.Error("expected non-empty reference kind")
		}
	}
}

func TestConvexHullHullPoints(t *testing.T) {
	cat := makeTestCatalog([]types.Glass{
		{Key: "N-BK7", ND: 1.5168, VD: 64.17},
		{Key: "N-SF2", ND: 1.64769, VD: 33.82},
		{Key: "N-LAK9", ND: 1.691, VD: 54.71},
	})
	hull := NewDefaultConvexHull(cat)
	points := hull.ReferencePoints()
	if len(points) == 0 {
		t.Error("expected non-empty reference points")
	}
	for _, p := range points {
		if p.ND <= 0 || p.VD <= 0 {
			t.Errorf("expected positive nd/vd in reference point, got nd=%f vd=%f", p.ND, p.VD)
		}
	}
}

func TestConvexHullKindFilter(t *testing.T) {
	cat := makeTestCatalog([]types.Glass{
		{Key: "N-BK7", ND: 1.5168, VD: 64.17}, // SCHOTT
		{Key: "S-NBH8", ND: 1.72, VD: 48.0},   // OHARA
		{Key: "FCD1", ND: 1.497, VD: 81.61},   // HOYA
	})
	// Only SCHOTT glasses
	hull := NewConvexHull(cat, []int{int(kindSchott)})
	if hull == nil || !hull.Enabled() {
		t.Fatal("expected enabled hull")
	}
	points := hull.ReferencePoints()
	if len(points) != 1 {
		t.Errorf("expected 1 SCHOTT reference point, got %d", len(points))
	}
	if points[0].Label != "N-BK7" {
		t.Errorf("expected N-BK7, got %s", points[0].Label)
	}
}

func TestGlassKindOrderForCatalog(t *testing.T) {
	cat := makeTestCatalog([]types.Glass{
		{Key: "N-BK7", ND: 1.5168, VD: 64.17},
		{Key: "S-NBH8", ND: 1.72, VD: 48.0},
	})
	order := GlassKindOrderForCatalog(cat)
	if len(order) == 0 {
		t.Fatal("expected non-empty kind order")
	}
	// SCHOTT should be first (it's the first kind in DefaultGlassKindOrder)
	if order[0] != int(kindSchott) {
		t.Errorf("expected SCHOTT first, got %d", order[0])
	}
}
