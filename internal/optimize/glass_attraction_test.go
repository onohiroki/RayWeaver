package optimize

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

func TestGlassAttraction_NoOp_WhenDisabled(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-F2", ND: 1.6200, VD: 36.37})

	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 10.0, Material: types.Material{ND: 1.5168, VD: 64.17}, Diameter: 50.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 100.0, Material: types.Material{}, Diameter: 50.0},
	}
	surface.Precompute(surfaces)

	cfg := Config{
		Surfaces:  surfaces,
		Variables: []Variable{{Name: "nd1", SurfaceID: 1, Param: "nd", Min: 1.4, Max: 2.0}, {Name: "vd1", SurfaceID: 1, Param: "vd", Min: 20, Max: 100}},
		MeritTerms: []MeritTerm{{
			FieldAngle: 0.0, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0,
		}},
		GlassCatalog: gc,
		NumRays:      16,
	}

	opt := NewOptimizer(cfg)
	x := opt.getInitialState()

	meritWithout := opt.EvaluateMerit(x)

	// Enable attraction — should add zero penalty (glass is in catalog)
	opt.SetGlassAttraction(&types.GlassAttractionConfig{Enabled: true}, gc)
	meritWith := opt.EvaluateMerit(x)

	if math.Abs(meritWith-meritWithout) > 1e-12 {
		t.Errorf("attraction at catalog glass should add zero penalty: without=%v with=%v", meritWithout, meritWith)
	}
}

func TestGlassAttraction_AddsPenalty_FarFromCatalog(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 10.0, Material: types.Material{ND: 1.5168, VD: 64.17}, Diameter: 50.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 100.0, Material: types.Material{}, Diameter: 50.0},
	}
	surface.Precompute(surfaces)

	cfg := Config{
		Surfaces:  surfaces,
		Variables: []Variable{{Name: "nd1", SurfaceID: 1, Param: "nd", Min: 1.4, Max: 2.0}, {Name: "vd1", SurfaceID: 1, Param: "vd", Min: 20, Max: 100}},
		MeritTerms: []MeritTerm{{
			FieldAngle: 0.0, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0,
		}},
		GlassCatalog: gc,
		NumRays:      16,
	}

	opt := NewOptimizer(cfg)

	// Enable attraction with high weight
	opt.SetGlassAttraction(&types.GlassAttractionConfig{
		Enabled:    true,
		WeightFrom: 100.0,
		WeightTo:   100.0,
		Metric:     "iteration",
	}, gc)

	// Set nd/vd far from any catalog glass
	x := opt.getInitialState()
	x[0] = 1.8  // nd far from N-BK7
	x[1] = 25.0 // vd far from N-BK7

	meritWith := opt.EvaluateMerit(x)

	// Now check without attraction
	opt.skipAttraction = true
	meritWithout := opt.EvaluateMerit(x)
	opt.skipAttraction = false

	if meritWith <= meritWithout {
		t.Errorf("attraction far from catalog should add penalty: with=%v without=%v", meritWith, meritWithout)
	}
}

func TestGlassAttraction_ResidualSquaredEqualsPenalty(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 10.0, Material: types.Material{ND: 1.5168, VD: 64.17}, Diameter: 50.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 100.0, Material: types.Material{}, Diameter: 50.0},
	}
	surface.Precompute(surfaces)

	cfg := Config{
		Surfaces:  surfaces,
		Variables: []Variable{{Name: "nd1", SurfaceID: 1, Param: "nd", Min: 1.4, Max: 2.0}, {Name: "vd1", SurfaceID: 1, Param: "vd", Min: 20, Max: 100}},
		MeritTerms: []MeritTerm{{
			FieldAngle: 0.0, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0,
		}},
		GlassCatalog: gc,
		NumRays:      16,
	}

	opt := NewOptimizer(cfg)
	opt.SetGlassAttraction(&types.GlassAttractionConfig{
		Enabled:    true,
		WeightFrom: 5.0,
		WeightTo:   5.0,
		Metric:     "iteration",
	}, gc)

	// Set nd/vd away from catalog
	x := opt.getInitialState()
	x[0] = 1.7
	x[1] = 30.0

	merit := opt.EvaluateMerit(x)

	// Compute attraction-only contribution: merit minus optical
	opt.skipAttraction = true
	opt.skipHull = true
	opticalMerit := opt.EvaluateMerit(x)
	opt.skipAttraction = false
	opt.skipHull = false

	attractionMerit := merit - opticalMerit

	// Check via residuals
	residuals := opt.ComputeResiduals(x)
	// Sum the attraction residuals (last pair count entries)
	nPairs := len(opt.hullPairs)
	if nPairs == 0 {
		t.Fatal("expected hull pairs")
	}
	var rSum float64
	for i := len(residuals) - nPairs; i < len(residuals); i++ {
		rSum += residuals[i] * residuals[i]
	}

	if math.Abs(rSum-attractionMerit) > 1e-10 {
		t.Errorf("residual² sum should equal attraction penalty: residual²=%v attraction=%v", rSum, attractionMerit)
	}
}

func TestGlassAttraction_SkipFlags(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 10.0, Material: types.Material{ND: 1.5168, VD: 64.17}, Diameter: 50.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 100.0, Material: types.Material{}, Diameter: 50.0},
	}
	surface.Precompute(surfaces)

	cfg := Config{
		Surfaces:  surfaces,
		Variables: []Variable{{Name: "nd1", SurfaceID: 1, Param: "nd", Min: 1.4, Max: 2.0}, {Name: "vd1", SurfaceID: 1, Param: "vd", Min: 20, Max: 100}},
		MeritTerms: []MeritTerm{{
			FieldAngle: 0.0, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0,
		}},
		GlassCatalog: gc,
		NumRays:      16,
	}

	opt := NewOptimizer(cfg)
	opt.SetGlassAttraction(&types.GlassAttractionConfig{
		Enabled:    true,
		WeightFrom: 100.0,
		WeightTo:   100.0,
		Metric:     "iteration",
	}, gc)

	x := opt.getInitialState()
	x[0] = 1.8
	x[1] = 25.0

	meritFull := opt.EvaluateMerit(x)

	// With skipAttraction, attraction is excluded
	meritOptical := opt.evaluateOpticalMerit(x)

	if math.Abs(meritFull-meritOptical) < 1e-10 {
		t.Errorf("skipAttraction should exclude attraction: full=%v optical=%v", meritFull, meritOptical)
	}
}

func TestGlassAttraction_UpdateMeritWeights_Iteration(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 10.0, Material: types.Material{ND: 1.5168, VD: 64.17}, Diameter: 50.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 100.0, Material: types.Material{}, Diameter: 50.0},
	}
	surface.Precompute(surfaces)

	cfg := Config{
		Surfaces:  surfaces,
		Variables: []Variable{{Name: "nd1", SurfaceID: 1, Param: "nd", Min: 1.4, Max: 2.0}, {Name: "vd1", SurfaceID: 1, Param: "vd", Min: 20, Max: 100}},
		MeritTerms: []MeritTerm{{
			FieldAngle: 0.0, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0,
		}},
		GlassCatalog: gc,
		NumRays:      16,
	}

	opt := NewOptimizer(cfg)
	opt.SetGlassAttraction(&types.GlassAttractionConfig{
		Enabled:    true,
		WeightFrom: 0.0,
		WeightTo:   1.0,
		Metric:     "iteration",
		Curve:      "linear",
		AnchorFrom: 0.0,
		AnchorTo:   100.0,
	}, gc)

	x := opt.getInitialState()

	// At iter 0, weight should be 0 (anchorFrom=0, anchorTo=1, t=0)
	opt.UpdateMeritWeights(x, 0)
	if opt.attractionWeight != 0 {
		t.Errorf("iter 0: expected weight=0, got %v", opt.attractionWeight)
	}

	// At iter 50, weight should be 0.5 (linear, t=0.5)
	opt.UpdateMeritWeights(x, 50)
	if math.Abs(opt.attractionWeight-0.5) > 0.01 {
		t.Errorf("iter 50: expected weight≈0.5, got %v", opt.attractionWeight)
	}

	// At iter 100, weight should be 1.0
	opt.UpdateMeritWeights(x, 100)
	if math.Abs(opt.attractionWeight-1.0) > 0.01 {
		t.Errorf("iter 100: expected weight=1.0, got %v", opt.attractionWeight)
	}
}

func TestGlassAttraction_NoOp_NoPairs(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 10.0, Material: types.Material{ND: 1.5168, VD: 64.17}, Diameter: 50.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 100.0, Material: types.Material{}, Diameter: 50.0},
	}
	surface.Precompute(surfaces)

	// No glass variables → no hull pairs → attraction is a no-op
	cfg := Config{
		Surfaces:  surfaces,
		Variables: []Variable{},
		MeritTerms: []MeritTerm{{
			FieldAngle: 0.0, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0,
		}},
		GlassCatalog: gc,
		NumRays:      16,
	}

	opt := NewOptimizer(cfg)
	x := opt.getInitialState()

	meritBefore := opt.EvaluateMerit(x)
	opt.SetGlassAttraction(&types.GlassAttractionConfig{Enabled: true}, gc)
	meritAfter := opt.EvaluateMerit(x)

	if math.Abs(meritAfter-meritBefore) > 1e-12 {
		t.Errorf("attraction with no pairs should not change merit: before=%v after=%v", meritBefore, meritAfter)
	}
}

func TestGlassAttraction_Diagnostics(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-F2", ND: 1.6200, VD: 36.37})

	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 10.0, Material: types.Material{Key: "N-BK7"}, Diameter: 50.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 100.0, Material: types.Material{}, Diameter: 50.0},
	}
	surface.Precompute(surfaces)

	cfg := Config{
		Surfaces:  surfaces,
		Variables: []Variable{{Name: "nd1", SurfaceID: 1, Param: "nd", Min: 1.4, Max: 2.0}, {Name: "vd1", SurfaceID: 1, Param: "vd", Min: 20, Max: 100}},
		MeritTerms: []MeritTerm{{
			FieldAngle: 0.0, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0,
		}},
		GlassCatalog: gc,
		NumRays:      16,
	}

	opt := NewOptimizer(cfg)
	opt.SetGlassAttraction(&types.GlassAttractionConfig{
		Enabled:    true,
		WeightFrom: 1.0,
		WeightTo:   1.0,
		Metric:     "iteration",
	}, gc)

	x := opt.getInitialState()

	// Diagnostics available immediately after SetGlassAttraction
	diag := opt.GlassAttractionDiagnostics(x)
	if diag == nil {
		t.Fatal("expected non-nil diagnostics after SetGlassAttraction")
	}
	if len(diag.Pairs) != 1 {
		t.Fatalf("expected 1 pair, got %d", len(diag.Pairs))
	}

	// After UpdateMeritWeights, sensitivity is computed
	opt.UpdateMeritWeights(x, 0)
	diag = opt.GlassAttractionDiagnostics(x)
	p := diag.Pairs[0]
	if p.Name != "N-BK7" {
		t.Errorf("expected pair name 'N-BK7', got %q", p.Name)
	}
	if p.ND <= 0 || p.VD <= 0 {
		t.Errorf("expected valid nd/vd, got %f/%f", p.ND, p.VD)
	}
	if p.NearestKey == "" {
		t.Error("expected non-empty nearest key")
	}
	if p.Distance < 0 {
		t.Errorf("expected non-negative distance, got %f", p.Distance)
	}
	if p.Scale <= 0 {
		t.Errorf("expected positive scale, got %f", p.Scale)
	}
}

// Diagnostics are reported whenever attraction is configured, even at zero
// weight, so a zero-weight run can act as an unconstrained baseline that still
// exposes the nearest-glass distances.
func TestGlassAttraction_Diagnostics_ZeroWeight(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 10.0, Material: types.Material{ND: 1.5168, VD: 64.17}, Diameter: 50.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 100.0, Material: types.Material{}, Diameter: 50.0},
	}
	surface.Precompute(surfaces)

	cfg := Config{
		Surfaces:  surfaces,
		Variables: []Variable{{Name: "nd1", SurfaceID: 1, Param: "nd", Min: 1.4, Max: 2.0}, {Name: "vd1", SurfaceID: 1, Param: "vd", Min: 20, Max: 100}},
		MeritTerms: []MeritTerm{{
			FieldAngle: 0.0, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0,
		}},
		GlassCatalog: gc,
		NumRays:      16,
	}
	opt := NewOptimizer(cfg)
	opt.SetGlassAttraction(&types.GlassAttractionConfig{Enabled: true, WeightFrom: 0, WeightTo: 0}, gc)

	diag := opt.GlassAttractionDiagnostics(opt.getInitialState())
	if diag == nil {
		t.Fatal("expected diagnostics at zero weight when attraction is configured")
	}
	if diag.Weight != 0 {
		t.Errorf("expected weight 0, got %f", diag.Weight)
	}
	if len(diag.Pairs) != 1 {
		t.Fatalf("expected 1 pair, got %d", len(diag.Pairs))
	}
}

// Inline model glasses carry no catalog key, so every nd/vd pair would collapse
// onto the same (empty) GlassName. buildHullPairs must key them by their
// (config, surface) location so each glass gets its own pair.
func TestBuildHullPairs_InlineModelsDistinct(t *testing.T) {
	vars := []Variable{
		{Name: "s3_nd", SurfaceID: 3, Param: "nd", Config: "0"},
		{Name: "s3_vd", SurfaceID: 3, Param: "vd", Config: "0"},
		{Name: "s6_nd", SurfaceID: 6, Param: "nd", Config: "0"},
		{Name: "s6_vd", SurfaceID: 6, Param: "vd", Config: "0"},
	}
	pairs := buildHullPairs(vars)
	if len(pairs) != 2 {
		t.Fatalf("expected 2 inline-model pairs, got %d", len(pairs))
	}
	// Pair indices must match the same surface.
	for _, p := range pairs {
		if vars[p.ndIndex].SurfaceID != vars[p.vdIndex].SurfaceID {
			t.Errorf("pair mixes surfaces: nd=%d vd=%d",
				vars[p.ndIndex].SurfaceID, vars[p.vdIndex].SurfaceID)
		}
	}
}

// A named catalog glass shared by several surfaces stays one pair.
func TestBuildHullPairs_NamedShared(t *testing.T) {
	vars := []Variable{
		{Name: "a_nd", SurfaceID: 1, Param: "nd", GlassName: "N-BK7", Config: "0"},
		{Name: "a_vd", SurfaceID: 1, Param: "vd", GlassName: "N-BK7", Config: "0"},
		{Name: "b_nd", SurfaceID: 5, Param: "nd", GlassName: "N-BK7", Config: "0"},
		{Name: "b_vd", SurfaceID: 5, Param: "vd", GlassName: "N-BK7", Config: "0"},
	}
	pairs := buildHullPairs(vars)
	if len(pairs) != 1 {
		t.Fatalf("expected 1 shared catalog pair, got %d", len(pairs))
	}
}

// With no virtual entrance pupil in use, FinalPupilModels must return nothing;
// emitting a default all-zero model would break a later trace of the output.
func TestFinalPupilModels_NoneInUse(t *testing.T) {
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 10.0, Material: types.Material{ND: 1.5168, VD: 64.17}, Diameter: 50.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 100.0, Material: types.Material{}, Diameter: 50.0},
	}
	surface.Precompute(surfaces)

	cfg := Config{
		Surfaces:  surfaces,
		Variables: []Variable{{Name: "nd1", SurfaceID: 1, Param: "nd", Min: 1.4, Max: 2.0}},
		MeritTerms: []MeritTerm{{
			FieldAngle: 0.0, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0,
		}},
		NumRays: 16,
	}
	opt := NewOptimizer(cfg)
	models := opt.FinalPupilModels(opt.getInitialState())
	if len(models) != 0 {
		t.Fatalf("expected no pupil models when none in use, got %d", len(models))
	}
}

// When a virtual entrance pupil IS configured it must still be reported.
func TestFinalPupilModels_InUse(t *testing.T) {
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 10.0, Material: types.Material{ND: 1.5168, VD: 64.17}, Diameter: 50.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 100.0, Material: types.Material{}, Diameter: 50.0},
	}
	surface.Precompute(surfaces)

	cfg := Config{
		Surfaces: surfaces,
		Variables: []Variable{
			{Name: "nd1", SurfaceID: 1, Param: "nd", Min: 1.4, Max: 2.0},
			{Name: "pmz", SurfaceID: 0, Param: "pupil_model_axial_position", Min: -5, Max: 5},
		},
		PupilModel: &types.PupilModelConfig{
			Mode:          "virtual_entrance_pupil",
			AxialPosition: -2.0,
			Diameter:      8.0,
		},
		MeritTerms: []MeritTerm{{
			FieldAngle: 0.0, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0,
		}},
		NumRays: 16,
	}
	opt := NewOptimizer(cfg)
	models := opt.FinalPupilModels(opt.getInitialState())
	if len(models) != 1 {
		t.Fatalf("expected 1 pupil model when in use, got %d", len(models))
	}
	if m, ok := models["config1"]; !ok {
		t.Errorf("expected model under config1, got %v", models)
	} else if m.Mode != "virtual_entrance_pupil" {
		t.Errorf("expected virtual_entrance_pupil mode, got %q", m.Mode)
	}
}
