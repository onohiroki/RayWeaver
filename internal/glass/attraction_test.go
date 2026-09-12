package glass

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
)

func TestBuildCatalogField_NilCatalog(t *testing.T) {
	f := BuildCatalogField(nil)
	if f == nil {
		t.Fatal("expected non-nil field")
	}
	if len(f.Points) != 0 {
		t.Errorf("expected 0 points, got %d", len(f.Points))
	}
}

func TestBuildCatalogField_EmptyCatalog(t *testing.T) {
	c := NewCatalog()
	f := BuildCatalogField(c)
	if len(f.Points) != 0 {
		t.Errorf("expected 0 points, got %d", len(f.Points))
	}
}

func TestBuildCatalogField_WithCatalog(t *testing.T) {
	c := NewCatalog()
	c.Add(types.Glass{Key: "N-BK7", ND: 1.5168, VD: 64.17})
	c.Add(types.Glass{Key: "N-F2", ND: 1.6200, VD: 36.37})
	c.Add(types.Glass{Key: "N-LAK9", ND: 1.6910, VD: 54.71})
	f := BuildCatalogField(c)
	if len(f.Points) != 3 {
		t.Errorf("expected 3 points, got %d", len(f.Points))
	}
	if len(f.NormPoints) != 3 {
		t.Errorf("expected 3 norm points, got %d", len(f.NormPoints))
	}
	for i, p := range f.NormPoints {
		if p.ND < 0 || p.ND > 1 || p.VD < 0 || p.VD > 1 {
			t.Errorf("norm point %d out of range: (%f, %f)", i, p.ND, p.VD)
		}
	}
}

func TestBuildCatalogField_Deduplication(t *testing.T) {
	c := NewCatalog()
	// Same glass added under two different keys — both point to the same
	// glass pointer, so they should deduplicate to one point.
	g := types.Glass{Key: "BK7", ND: 1.5168, VD: 64.17}
	c.Add(g)
	c.Add(types.Glass{Key: "BK7", ND: 1.5168, VD: 64.17})
	f := BuildCatalogField(c)
	if len(f.Points) != 1 {
		t.Errorf("expected 1 point after dedup, got %d", len(f.Points))
	}
}

func TestSoftMinPotential_AtCatalogGlass(t *testing.T) {
	c := NewCatalog()
	c.Add(types.Glass{Key: "N-BK7", ND: 1.5168, VD: 64.17})
	f := BuildCatalogField(c)
	pot := SoftMinPotential(f, 1.5168, 64.17, 0.03, 0.03, "distance")
	if pot != 0 {
		t.Errorf("potential at catalog glass should be 0, got %f", pot)
	}
}

func TestSoftMinPotential_MonotonicAwayFromGlass(t *testing.T) {
	c := NewCatalog()
	c.Add(types.Glass{Key: "N-BK7", ND: 1.5168, VD: 64.17})
	f := BuildCatalogField(c)
	d0 := SoftMinPotential(f, 1.5168, 64.17, 0.03, 0.03, "distance")
	d1 := SoftMinPotential(f, 1.52, 65.0, 0.03, 0.03, "distance")
	d2 := SoftMinPotential(f, 1.60, 70.0, 0.03, 0.03, "distance")
	if d0 >= d1 || d1 >= d2 {
		t.Errorf("expected monotonic increase: %f < %f < %f", d0, d1, d2)
	}
}

func TestSoftMinPotential_NearestGlass(t *testing.T) {
	c := NewCatalog()
	c.Add(types.Glass{Key: "N-BK7", ND: 1.5168, VD: 64.17})
	c.Add(types.Glass{Key: "N-F2", ND: 1.6200, VD: 36.37})
	f := BuildCatalogField(c)
	// Closer to N-BK7
	pot := SoftMinPotential(f, 1.51, 63.0, 0.03, 0.03, "distance")
	// Closer to N-F2
	pot2 := SoftMinPotential(f, 1.61, 37.0, 0.03, 0.03, "distance")
	if pot == 0 || pot2 == 0 {
		t.Errorf("expected non-zero potentials, got %f and %f", pot, pot2)
	}
}

func TestSoftMinPotential_Gaussian(t *testing.T) {
	c := NewCatalog()
	c.Add(types.Glass{Key: "N-BK7", ND: 1.5168, VD: 64.17})
	f := BuildCatalogField(c)
	pot := SoftMinPotential(f, 1.5168, 64.17, 0.03, 0.03, "gaussian")
	if pot != 0 {
		t.Errorf("gaussian potential at catalog glass should be 0, got %f", pot)
	}
	pot2 := SoftMinPotential(f, 1.60, 70.0, 0.03, 0.03, "gaussian")
	if pot2 <= 0 {
		t.Errorf("gaussian potential away from glass should be > 0, got %f", pot2)
	}
}

func TestPenalty_ZeroAtGlass(t *testing.T) {
	c := NewCatalog()
	c.Add(types.Glass{Key: "N-BK7", ND: 1.5168, VD: 64.17})
	f := BuildCatalogField(c)
	p := Penalty(f, 1.5168, 64.17, 0.03, 0.03, 0.02, 1.0, "distance")
	if p != 0 {
		t.Errorf("penalty at catalog glass should be 0, got %f", p)
	}
}

func TestPenalty_QuadraticAwayFromGlass(t *testing.T) {
	c := NewCatalog()
	c.Add(types.Glass{Key: "N-BK7", ND: 1.5168, VD: 64.17})
	f := BuildCatalogField(c)
	w1 := Penalty(f, 1.60, 70.0, 0.03, 0.03, 0.02, 1.0, "distance")
	w2 := Penalty(f, 1.70, 80.0, 0.03, 0.03, 0.02, 1.0, "distance")
	if w1 <= 0 || w2 <= w1 {
		t.Errorf("expected increasing penalty: 0 < %f < %f", w1, w2)
	}
}

func TestResidual_PenaltyConsistency(t *testing.T) {
	c := NewCatalog()
	c.Add(types.Glass{Key: "N-BK7", ND: 1.5168, VD: 64.17})
	c.Add(types.Glass{Key: "N-F2", ND: 1.6200, VD: 36.37})
	f := BuildCatalogField(c)
	tests := []struct {
		nd, vd, sigmaND, sigmaVD, margin, weight float64
		kernel                                   string
	}{
		{1.50, 60.0, 0.03, 0.03, 0.02, 1.0, "distance"},
		{1.70, 50.0, 0.03, 0.03, 0.02, 2.5, "distance"},
		{1.55, 65.0, 0.03, 0.03, 0.02, 5.0, "gaussian"},
	}
	for _, tt := range tests {
		p := Penalty(f, tt.nd, tt.vd, tt.sigmaND, tt.sigmaVD, tt.margin, tt.weight, tt.kernel)
		r := Residual(f, tt.nd, tt.vd, tt.sigmaND, tt.sigmaVD, tt.margin, tt.weight, tt.kernel)
		// Penalty = weight * d², Residual = √weight * d → r² = weight * d² = Penalty
		r2 := r * r
		if math.Abs(r2-p) > 1e-10 {
			t.Errorf("Penalty=%f, Residual²=%f, should match (nd=%f vd=%f)", p, r2, tt.nd, tt.vd)
		}
	}
}

func TestNearest(t *testing.T) {
	c := NewCatalog()
	c.Add(types.Glass{Key: "N-BK7", ND: 1.5168, VD: 64.17})
	c.Add(types.Glass{Key: "N-F2", ND: 1.6200, VD: 36.37})
	f := BuildCatalogField(c)
	bn, bv, br2 := Nearest(f, 1.51, 63.0)
	if math.Abs(bn-1.5168) > 0.001 || math.Abs(bv-64.17) > 0.001 {
		t.Errorf("expected nearest N-BK7 (1.5168, 64.17), got (%f, %f)", bn, bv)
	}
	if br2 >= 1.0 {
		t.Errorf("expected small distance², got %f", br2)
	}
}

func TestNearest_Empty(t *testing.T) {
	bn, bv, br2 := Nearest(nil, 1.5, 60.0)
	if bn != 0 || bv != 0 || br2 != math.MaxFloat64 {
		t.Errorf("expected zero values for nil field, got (%f, %f, %f)", bn, bv, br2)
	}
}

func TestNearestNamed(t *testing.T) {
	c := NewCatalog()
	c.Add(types.Glass{Key: "N-BK7", ND: 1.5168, VD: 64.17})
	c.Add(types.Glass{Key: "N-F2", ND: 1.6200, VD: 36.37})
	f := BuildCatalogField(c)
	key, bn, bv, br2 := NearestNamed(f, 1.51, 63.0)
	if key != "N-BK7" {
		t.Errorf("expected nearest key N-BK7, got %q", key)
	}
	if math.Abs(bn-1.5168) > 0.001 || math.Abs(bv-64.17) > 0.001 {
		t.Errorf("expected nearest nd/vd (1.5168, 64.17), got (%f, %f)", bn, bv)
	}
	if br2 >= 1.0 {
		t.Errorf("expected small distance², got %f", br2)
	}
}

func TestNearestNamed_Empty(t *testing.T) {
	key, bn, bv, br2 := NearestNamed(nil, 1.5, 60.0)
	if key != "" || bn != 0 || bv != 0 || br2 != math.MaxFloat64 {
		t.Errorf("expected zero values for nil field, got (%q, %f, %f, %f)", key, bn, bv, br2)
	}
}

func TestCatalogFieldCount(t *testing.T) {
	var f *CatalogField
	if f.CatalogFieldCount() != 0 {
		t.Error("nil field should have 0 count")
	}
	c := NewCatalog()
	c.Add(types.Glass{Key: "BK7", ND: 1.5168, VD: 64.17})
	f = BuildCatalogField(c)
	if f.CatalogFieldCount() != 1 {
		t.Errorf("expected 1, got %d", f.CatalogFieldCount())
	}
}
