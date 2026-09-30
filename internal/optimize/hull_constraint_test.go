package optimize

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// buildHullTestOptimizer returns an optimizer with one nd/vd pair on surface 1.
func buildHullTestOptimizer(t *testing.T) *Optimizer {
	t.Helper()
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-SF2", ND: 1.64769, VD: 33.82})
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-LAK9", ND: 1.691, VD: 54.71})
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-LAF21", ND: 1.72, VD: 48.0})

	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 10.0, Material: types.Material{ND: 1.5168, VD: 64.17}, Diameter: 50.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 100.0, Material: types.Material{}, Diameter: 50.0},
	}
	surface.Precompute(surfaces)

	hull := glass.NewDefaultConvexHull(gc)
	cfg := Config{
		Surfaces: surfaces,
		Variables: []Variable{
			{Name: "nd1", SurfaceID: 1, Param: "nd", Min: 1.4, Max: 2.0},
			{Name: "vd1", SurfaceID: 1, Param: "vd", Min: 20, Max: 100},
		},
		MeritTerms: []MeritTerm{{
			FieldAngle: 0.0, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0,
		}},
		GlassCatalog: gc,
		NumRays:      16,
		Hull:         hull,
		HullMargin:   0.02,
		HullWeight:   1.0,
	}
	return NewOptimizer(cfg)
}

func TestGlassHullViolationsInside(t *testing.T) {
	opt := buildHullTestOptimizer(t)
	x := []float64{1.60, 48.0}
	if ids := opt.GlassHullViolations(x); len(ids) != 0 {
		t.Errorf("expected no hull violation for an interior point, got %v", ids)
	}
}

func TestGlassHullViolationsOutside(t *testing.T) {
	opt := buildHullTestOptimizer(t)
	x := []float64{2.5, 80.0}
	ids := opt.GlassHullViolations(x)
	if len(ids) == 0 {
		t.Fatal("expected a hull violation for an exterior point")
	}
	// The element's glass surface (surface 1) must be reported.
	found := false
	for _, id := range ids {
		if id == 1 {
			found = true
		}
	}
	if !found {
		t.Errorf("expected surface 1 in violations, got %v", ids)
	}
}

func TestGlassHullViolationsNoHull(t *testing.T) {
	opt := buildHullTestOptimizer(t)
	opt.hull = nil
	if ids := opt.GlassHullViolations([]float64{2.5, 80.0}); len(ids) != 0 {
		t.Errorf("expected no violation when the hull is absent, got %v", ids)
	}
}

func TestGlassHullViolationsNoPairs(t *testing.T) {
	opt := buildHullTestOptimizer(t)
	opt.hullPairs = nil
	if ids := opt.GlassHullViolations([]float64{2.5, 80.0}); len(ids) != 0 {
		t.Errorf("expected no violation when no glass pairs exist, got %v", ids)
	}
}

func TestConvexHullContains(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-SF2", ND: 1.64769, VD: 33.82})
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-LAK9", ND: 1.691, VD: 54.71})
	hull := glass.NewDefaultConvexHull(gc)

	if !hull.Contains(1.60, 48.0) {
		t.Error("expected interior point to be contained")
	}
	if !hull.Contains(1.5168, 64.17) {
		t.Error("expected exact vertex to be contained")
	}
	if hull.Contains(2.5, 80.0) {
		t.Error("expected exterior point to be outside the hull")
	}
}

func TestConvexHullContainsHalfSpaceConsistency(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-SF2", ND: 1.64769, VD: 33.82})
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-LAK9", ND: 1.691, VD: 54.71})
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-LAF21", ND: 1.72, VD: 48.0})
	hull := glass.NewDefaultConvexHull(gc)

	// Half-space containment must agree with the barycentric distance sign.
	for _, p := range []struct{ nd, vd float64 }{
		{1.60, 48.0}, {1.5168, 64.17}, {1.7, 50.0}, {2.5, 80.0}, {1.0, 20.0}, {1.65, 33.9},
	} {
		_, dist := hull.Barycentric(p.nd, p.vd)
		baryInside := dist >= -1e-9
		if got := hull.Contains(p.nd, p.vd); got != baryInside {
			t.Errorf("Contains(%f,%f)=%v but barycentric inside=%v (dist=%f)",
				p.nd, p.vd, got, baryInside, dist)
		}
	}
}

func TestConvexHullProjectOntoHull(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-SF2", ND: 1.64769, VD: 33.82})
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-LAK9", ND: 1.691, VD: 54.71})
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-LAF21", ND: 1.72, VD: 48.0})
	hull := glass.NewDefaultConvexHull(gc)

	// An interior point is returned unchanged.
	nd, vd := hull.ProjectOntoHull(1.60, 48.0)
	if math.Abs(nd-1.60) > 1e-9 || math.Abs(vd-48.0) > 1e-9 {
		t.Errorf("interior point should be unchanged, got (%f,%f)", nd, vd)
	}

	// An exterior point is anchored onto the boundary (contained afterwards).
	nd, vd = hull.ProjectOntoHull(2.5, 80.0)
	if !hull.Contains(nd, vd) {
		t.Errorf("projected point (%f,%f) should lie on/inside the hull", nd, vd)
	}
}

func TestConvexHullEnforceBoundsAnchors(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-SF2", ND: 1.64769, VD: 33.82})
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-LAK9", ND: 1.691, VD: 54.71})
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-LAF21", ND: 1.72, VD: 48.0})
	hull := glass.NewDefaultConvexHull(gc)

	nd, vd := hull.EnforceBounds(2.5, 80.0)
	if !hull.Contains(nd, vd) {
		t.Errorf("enforced point (%f,%f) should be contained by the hull", nd, vd)
	}
}

// TestRescueGlassHullProjectsOutside: the escape cycle's hull-rescue repair
// projects an outside nd/vd pair back onto the hull without mutating the
// input vector (the caller keeps the original point when ok is false).
func TestRescueGlassHullProjectsOutside(t *testing.T) {
	opt := buildHullTestOptimizer(t)
	x := []float64{2.5, 80.0}
	out, ok := opt.RescueGlassHull(x)
	if !ok {
		t.Fatal("expected an exterior point to be rescued")
	}
	if x[0] != 2.5 || x[1] != 80.0 {
		t.Errorf("RescueGlassHull mutated the input: %v", x)
	}
	if len(out) != len(x) {
		t.Fatalf("rescued vector length = %d, want %d", len(out), len(x))
	}
	if !opt.hull.Contains(out[0], out[1]) {
		t.Errorf("rescued glass (%v, %v) still outside the hull", out[0], out[1])
	}
}

// TestRescueGlassHullNoop: contained points (and hull-less / pair-less
// optimizers) report ok=false so the caller keeps the original point.
func TestRescueGlassHullNoop(t *testing.T) {
	opt := buildHullTestOptimizer(t)
	if out, ok := opt.RescueGlassHull([]float64{1.60, 48.0}); ok {
		t.Errorf("interior point needs no rescue, got %v", out)
	}

	noHull := buildHullTestOptimizer(t)
	noHull.hull = nil
	if out, ok := noHull.RescueGlassHull([]float64{2.5, 80.0}); ok {
		t.Errorf("hull-less optimizer should not rescue, got %v", out)
	}

	noPairs := buildHullTestOptimizer(t)
	noPairs.hullPairs = nil
	if out, ok := noPairs.RescueGlassHull([]float64{2.5, 80.0}); ok {
		t.Errorf("pair-less optimizer should not rescue, got %v", out)
	}
}

// TestHullEscapePhaseFactor: the hull penalty keeps full weight outside an
// escape run (phaseActive false — a plain optimize run never calls
// SetEscapePhase) and during the glass (0.5) and clean (1) phases, and is
// scaled by the hull's escape_weight_factor only during escape exploration
// (phase 0). Hulls without a configured factor are never scaled.
func TestHullEscapePhaseFactor(t *testing.T) {
	opt := buildHullTestOptimizer(t)
	// No SetEscapePhase call: full strength.
	if got := opt.hullPhaseFactor(); got != 1 {
		t.Errorf("before SetEscapePhase factor = %v, want 1", got)
	}
	// Escape phase but the hull has no configured factor: still full.
	opt.SetEscapePhase(0)
	if got := opt.hullPhaseFactor(); got != 1 {
		t.Errorf("escape phase without configured factor = %v, want 1", got)
	}
	opt.hull.SetEscapeWeightFactor(0.1)
	if got := opt.hullPhaseFactor(); got != 0.1 {
		t.Errorf("escape phase factor = %v, want 0.1", got)
	}
	opt.SetEscapePhase(0.5)
	if got := opt.hullPhaseFactor(); got != 1 {
		t.Errorf("glass phase factor = %v, want 1", got)
	}
	opt.SetEscapePhase(1)
	if got := opt.hullPhaseFactor(); got != 1 {
		t.Errorf("clean phase factor = %v, want 1", got)
	}

	// A plain optimize run (SetEscapePhase never called) stays at full
	// strength even when the hull carries a factor.
	plain := buildHullTestOptimizer(t)
	plain.hull.SetEscapeWeightFactor(0.1)
	if got := plain.hullPhaseFactor(); got != 1 {
		t.Errorf("plain optimize factor = %v, want 1", got)
	}
}

// TestEscapePhaseScalesHullMerit: behavioural check of the factor at the
// merit/residual sites — during escape the hull term (and the squared hull
// residual) is multiplied by escape_weight_factor, otherwise it is full.
func TestEscapePhaseScalesHullMerit(t *testing.T) {
	opt := buildHullTestOptimizer(t)
	opt.hull.SetEscapeWeightFactor(0.1)
	x := []float64{2.5, 80.0} // outside the hull: nonzero penalty

	base := opt.MeritBreakdown(x)["hull"]
	if base <= 0 {
		t.Fatalf("expected nonzero hull penalty at an exterior point, got %v", base)
	}

	opt.SetEscapePhase(0) // escape exploration
	escapeHull := opt.MeritBreakdown(x)["hull"]
	if math.Abs(escapeHull-0.1*base) > 1e-9*math.Max(1, base) {
		t.Errorf("escape-phase hull term = %v, want %v", escapeHull, 0.1*base)
	}
	// The squared hull residual must equal the scaled merit contribution.
	r := opt.ComputeResiduals(x)
	if len(r) == 0 {
		t.Fatal("expected residuals")
	}
	hullR := r[len(r)-1]
	if math.Abs(hullR*hullR-escapeHull) > 1e-9*math.Max(1, escapeHull) {
		t.Errorf("hull residual² = %v, want %v (escape-scaled)", hullR*hullR, escapeHull)
	}

	opt.SetEscapePhase(1) // clean: full strength
	if got := opt.MeritBreakdown(x)["hull"]; math.Abs(got-base) > 1e-9*math.Max(1, base) {
		t.Errorf("clean-phase hull term = %v, want %v", got, base)
	}
}
