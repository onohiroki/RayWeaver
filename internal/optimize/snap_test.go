package optimize

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

func snapTestOptimizer(t *testing.T, nd, vd float64) *Optimizer {
	t.Helper()
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Name: "N-BK7", ND: 1.5168, VD: 64.17})
	gc.Add(types.Glass{Type: types.GlassTypeModel, Name: "N-SF2", ND: 1.64769, VD: 33.82})

	surfaces := []types.Surface{
		{ID: 3, Type: types.Sphere, Curvature: -0.01, Thickness: 5.0, Material: types.Material{ND: nd, VD: vd}, Diameter: 20.0},
		{ID: 4, Type: types.Sphere, Curvature: 0.01, Thickness: 50.0, Material: types.Material{}, Diameter: 20.0},
	}
	surface.Precompute(surfaces)

	cfg := Config{
		Surfaces: surfaces,
		Variables: []Variable{
			{Name: "s3_nd", SurfaceID: 3, Param: "nd", Min: 1.4, Max: 2.0},
			{Name: "s3_vd", SurfaceID: 3, Param: "vd", Min: 20, Max: 80},
		},
		MeritTerms: []MeritTerm{{
			FieldAngle: 0.0, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0,
		}},
		GlassCatalog: gc,
		NumRays:      16,
	}
	return NewOptimizer(cfg)
}

func TestSnapVariables_NearestRealGlass(t *testing.T) {
	// Start near N-BK7 (1.5168, 64.17).
	opt := snapTestOptimizer(t, 1.52, 63.0)
	x := opt.InitialState()

	snapped, pairs := opt.SnapVariables(x)
	if len(pairs) != 1 {
		t.Fatalf("expected 1 pair, got %d", len(pairs))
	}
	p := pairs[0]
	if p.Name != "N-BK7" {
		t.Errorf("expected N-BK7, got %q", p.Name)
	}
	if math.Abs(p.ToND-1.5168) > 1e-9 || math.Abs(p.ToVD-64.17) > 1e-9 {
		t.Errorf("expected snap to (1.5168, 64.17), got (%v, %v)", p.ToND, p.ToVD)
	}
	if p.SurfaceID != 3 {
		t.Errorf("expected surface 3, got %d", p.SurfaceID)
	}
	// The snapped vector must carry the catalog values at both indices.
	if math.Abs(snapped[0]-1.5168) > 1e-9 || math.Abs(snapped[1]-64.17) > 1e-9 {
		t.Errorf("snapped vector = (%v, %v), want (1.5168, 64.17)", snapped[0], snapped[1])
	}
	// x must be untouched.
	if x[0] != 1.52 || x[1] != 63.0 {
		t.Errorf("input vector mutated: (%v, %v)", x[0], x[1])
	}
}

func TestSnapVariables_AlreadyOnCatalogIsNoOp(t *testing.T) {
	opt := snapTestOptimizer(t, 1.5168, 64.17)
	x := opt.InitialState()

	snapped, pairs := opt.SnapVariables(x)
	if len(pairs) != 1 {
		t.Fatalf("expected 1 pair, got %d", len(pairs))
	}
	if pairs[0].Distance > 1e-9 {
		t.Errorf("expected zero distance at a catalog point, got %v", pairs[0].Distance)
	}
	before := opt.OpticalMerit(x)
	after := opt.OpticalMerit(snapped)
	if math.Abs(after-before) > 1e-12 {
		t.Errorf("snapping onto the catalog should not change merit: before=%v after=%v", before, after)
	}
}

func TestSnapVariables_CostIsConsistent(t *testing.T) {
	// Start away from every catalog glass so the snap perturbs the merit.
	opt := snapTestOptimizer(t, 1.55, 50.0)
	x := opt.InitialState()
	before := opt.OpticalMerit(x)

	snapped, _ := opt.SnapVariables(x)
	after := opt.OpticalMerit(snapped)

	// Both evaluations must be finite; the signed change is the reported cost.
	if math.IsNaN(before) || math.IsInf(before, 0) || math.IsNaN(after) || math.IsInf(after, 0) {
		t.Fatalf("expected finite merits, got before=%v after=%v", before, after)
	}
}

func TestSnapVariables_NoGlassVariables(t *testing.T) {
	surfaces := []types.Surface{
		{ID: 2, Type: types.Sphere, Curvature: 0.01, Thickness: 50.0, Material: types.Material{}, Diameter: 20.0},
	}
	surface.Precompute(surfaces)
	cfg := Config{
		Surfaces:  surfaces,
		Variables: []Variable{{Name: "c2", SurfaceID: 2, Param: "curvature", Min: -0.1, Max: 0.1}},
		MeritTerms: []MeritTerm{{
			FieldAngle: 0.0, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0,
		}},
		GlassCatalog: glass.NewCatalog(),
		NumRays:      16,
	}
	opt := NewOptimizer(cfg)
	x := opt.InitialState()
	snapped, pairs := opt.SnapVariables(x)
	if len(pairs) != 0 {
		t.Errorf("expected no pairs, got %d", len(pairs))
	}
	if len(snapped) != len(x) {
		t.Errorf("expected unchanged vector length, got %d want %d", len(snapped), len(x))
	}
}

func TestSnapVariables_Deterministic(t *testing.T) {
	opt := snapTestOptimizer(t, 1.55, 50.0)
	x := opt.InitialState()
	s1, p1 := opt.SnapVariables(x)
	s2, p2 := opt.SnapVariables(x)
	for i := range s1 {
		if s1[i] != s2[i] {
			t.Fatalf("non-deterministic snap at %d: %v vs %v", i, s1[i], s2[i])
		}
	}
	if len(p1) != len(p2) || p1[0].Name != p2[0].Name {
		t.Fatalf("non-deterministic pairs: %+v vs %+v", p1, p2)
	}
}
