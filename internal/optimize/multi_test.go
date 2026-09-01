package optimize

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// TestMultiOptimizerSeidelDistortionKind verifies merit term kinds are honored
// in multi-config mode: with target = current S5 the merit must be ~0. Before
// kind support, the term was evaluated as spot RMS (far from 0).
func TestMultiOptimizerSeidelDistortionKind(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	surfaces := singletSurfaces()
	surface.Precompute(surfaces)

	s5 := paraxial.ComputeSeidel(surfaces, 10, 0.00058756, gc).Distortion

	configs := []ConfigInput{
		{
			ID:          "cfg1",
			Weight:      1.0,
			Surfaces:    singletSurfaces(),
			Fields:      []types.FieldItem{{ID: 0, AngleDeg: 10.0, Weight: 1.0}},
			Wavelengths: []types.WavelengthItem{{ID: 0, Value: 0.00058756, Weight: 1.0}},
			MeritTerms: []types.MeritTerm{{
				Kind:       "seidel_distortion",
				Field:      0,
				Wavelength: 0.00058756,
				Target:     s5,
				Weight:     1.0,
			}},
		},
	}

	opt := NewMultiOptimizer(configs, nil, nil, gc, 1, 0.01, 1e-6, 1e-6, 2.0, 64, 0, 0, nil, nil, 0, 0, false, false, nil, nil)
	x := []float64{}
	merit := opt.EvaluateMerit(x)

	if merit > 1e-12 {
		t.Errorf("merit with target = current S5 = %v, want ~0", merit)
	}
}

func TestMultiOptimizerApplySharedVariables(t *testing.T) {
	sharedVars := []types.SharedVariable{
		{
			Name:   "curvature_shift",
			Min:    -0.1,
			Max:    0.1,
			Active: true,
			Bindings: []types.SharedVariableBinding{
				{Config: "wide", ID: 1, Param: "curvature", Scale: 1.0, Offset: 0.0},
				{Config: "tele", ID: 1, Param: "curvature", Scale: 1.0, Offset: 0.0},
			},
		},
	}

	configs := []ConfigInput{
		{
			ID:          "wide",
			Weight:      1.0,
			Surfaces:    singletSurfaces(),
			Fields:      []types.FieldItem{{ID: 0, AngleDeg: 0.0, Weight: 1.0}},
			Wavelengths: []types.WavelengthItem{{ID: 0, Value: 0.00058756, Weight: 1.0}},
			MeritTerms: []types.MeritTerm{
				{Field: 0, Wavelength: 0.00058756, Weight: 1.0},
			},
		},
		{
			ID:          "tele",
			Weight:      1.0,
			Surfaces:    singletSurfaces(),
			Fields:      []types.FieldItem{{ID: 0, AngleDeg: 0.0, Weight: 1.0}},
			Wavelengths: []types.WavelengthItem{{ID: 0, Value: 0.00058756, Weight: 1.0}},
			MeritTerms: []types.MeritTerm{
				{Field: 0, Wavelength: 0.00058756, Weight: 1.0},
			},
		},
	}

	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	opt := NewMultiOptimizer(configs, sharedVars, nil, gc, 1, 0.01, 1e-6, 1e-6, 2.0, 64, 0, 0, nil, nil, 0, 0, false, false, nil, nil)

	x := []float64{0.05}
	configSurfaces, _ := opt.applyVariables(x)

	wideSurf := configSurfaces["wide"]
	teleSurf := configSurfaces["tele"]

	foundWide := false
	foundTele := false
	for _, s := range wideSurf {
		if s.ID == 1 {
			foundWide = true
			if math.Abs(s.Curvature-0.05) > 1e-10 {
				t.Errorf("Wide curvature = %v, want 0.05", s.Curvature)
			}
		}
	}
	for _, s := range teleSurf {
		if s.ID == 1 {
			foundTele = true
			if math.Abs(s.Curvature-0.05) > 1e-10 {
				t.Errorf("Tele curvature = %v, want 0.05", s.Curvature)
			}
		}
	}
	if !foundWide {
		t.Error("Wide surface 1 not found")
	}
	if !foundTele {
		t.Error("Tele surface 1 not found")
	}
}

func TestMultiOptimizerApplyLocalVariables(t *testing.T) {
	localVars := []types.LocalVariableDef{
		{
			Name:   "wide_thickness",
			Config: "wide",
			Target: types.VariableTarget{Type: "surface", ID: 2, Param: "thickness"},
			Min:    0.1,
			Max:    200.0,
			Active: true,
		},
	}

	configs := []ConfigInput{
		{
			ID:          "wide",
			Weight:      1.0,
			Surfaces:    singletSurfaces(),
			Fields:      []types.FieldItem{{ID: 0, AngleDeg: 0.0, Weight: 1.0}},
			Wavelengths: []types.WavelengthItem{{ID: 0, Value: 0.00058756, Weight: 1.0}},
			MeritTerms: []types.MeritTerm{
				{Field: 0, Wavelength: 0.00058756, Weight: 1.0},
			},
		},
	}

	gc := glass.NewCatalog()

	opt := NewMultiOptimizer(configs, nil, localVars, gc, 1, 0.01, 1e-6, 1e-6, 2.0, 64, 0, 0, nil, nil, 0, 0, false, false, nil, nil)
	x := []float64{75.0}
	configSurfaces, _ := opt.applyVariables(x)

	wideSurf := configSurfaces["wide"]
	found := false
	for _, s := range wideSurf {
		if s.ID == 2 {
			found = true
			if math.Abs(s.Thickness-75.0) > 1e-10 {
				t.Errorf("Wide thickness = %v, want 75.0", s.Thickness)
			}
		}
	}
	if !found {
		t.Error("Surface 2 not found in wide config")
	}
}

func TestMultiOptimizerSizeAutoAperturesGeometric(t *testing.T) {
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0, Thickness: 10.0, Material: types.Material{}, Diameter: 20.0, AutoAperture: true},
		{ID: 2, Type: types.Sphere, Curvature: 0, Thickness: 10.0, Material: types.Material{}, Diameter: 10.0, AutoAperture: true},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 100.0, Material: types.Material{}, Diameter: 8.0},
	}
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	configs := []ConfigInput{
		{
			ID:          "wide",
			Weight:      1.0,
			Surfaces:    surfaces,
			Fields:      []types.FieldItem{{ID: 0, AngleDeg: 0.0, Weight: 1.0}, {ID: 1, AngleDeg: 16.0, Weight: 1.0}},
			Wavelengths: []types.WavelengthItem{{ID: 0, Value: 0.00058756, Weight: 1.0}},
			MeritTerms: []types.MeritTerm{
				{Field: 0, Wavelength: 0.00058756, Weight: 1.0},
				{Field: 1, Wavelength: 0.00058756, Weight: 1.0},
			},
		},
	}

	opt := NewMultiOptimizer(configs, nil, nil, gc, 10, 1.0, 1e-6, 1e-6, 1.0, 64, 100.0, 0, nil, nil, 0, 0, false, false, nil, nil)

	// True geometric beam extent at surface 2 for the extreme (16deg) field,
	// measured without aperture clipping.
	surface.Precompute(surfaces)
	ext := dls.TraceFieldGridExtents(gc, surfaces, 0, 0, 16.0, []float64{0, 1}, 0.00058756, 1.0, 64, 0, 1)
	geoExtent2 := ext[2]
	if geoExtent2 <= 5.0 {
		t.Fatalf("test setup: geometric extent at surface 2 = %.3f, want > 5.0 (initial aperture radius)", geoExtent2)
	}

	aps := opt.FinalApertures([]float64{})
	dia2 := aps["wide"][2]
	if dia2/2 < geoExtent2-1e-9 {
		t.Errorf("auto aperture surface 2 diameter %.3f does not cover geometric beam extent %.3f", dia2, geoExtent2)
	}
	if dia2 <= 10.0 {
		t.Errorf("auto aperture surface 2 diameter %.3f, want > initial 10.0 (aperture-checked sizing)", dia2)
	}
}

func TestMultiOptimizerEvaluateMerit(t *testing.T) {
	sharedVars := []types.SharedVariable{
		{
			Name:   "curvature_shift",
			Min:    -0.1,
			Max:    0.1,
			Active: true,
			Bindings: []types.SharedVariableBinding{
				{Config: "wide", ID: 1, Param: "curvature", Scale: 1.0, Offset: 0.0},
				{Config: "tele", ID: 1, Param: "curvature", Scale: 1.0, Offset: 0.0},
			},
		},
	}

	configs := []ConfigInput{
		{
			ID:          "wide",
			Weight:      1.0,
			Surfaces:    singletSurfaces(),
			Fields:      []types.FieldItem{{ID: 0, AngleDeg: 0.0, Weight: 1.0}},
			Wavelengths: []types.WavelengthItem{{ID: 0, Value: 0.00058756, Weight: 1.0}},
			MeritTerms: []types.MeritTerm{
				{Field: 0, Wavelength: 0.00058756, Weight: 1.0},
			},
		},
		{
			ID:          "tele",
			Weight:      1.0,
			Surfaces:    singletSurfaces(),
			Fields:      []types.FieldItem{{ID: 0, AngleDeg: 0.0, Weight: 1.0}},
			Wavelengths: []types.WavelengthItem{{ID: 0, Value: 0.00058756, Weight: 1.0}},
			MeritTerms: []types.MeritTerm{
				{Field: 0, Wavelength: 0.00058756, Weight: 1.0},
			},
		},
	}

	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	opt := NewMultiOptimizer(configs, sharedVars, nil, gc, 1, 0.01, 1e-6, 1e-6, 2.0, 64, 0, 0, nil, nil, 0, 0, false, false, nil, nil)
	x := []float64{0.0}
	merit := opt.EvaluateMerit(x)

	if math.IsInf(merit, 0) || math.IsNaN(merit) {
		t.Fatalf("EvaluateMerit returned non-finite value: %v", merit)
	}
	if merit < 0 {
		t.Fatalf("EvaluateMerit returned negative value: %v", merit)
	}
}

func TestMultiOptimizerGetInitialState(t *testing.T) {
	sharedVars := []types.SharedVariable{
		{
			Name:   "curvature_shift",
			Min:    -0.1,
			Max:    0.1,
			Active: true,
			Bindings: []types.SharedVariableBinding{
				{Config: "wide", ID: 1, Param: "curvature", Scale: 1.0, Offset: 0.0},
			},
		},
	}

	localVars := []types.LocalVariableDef{
		{
			Name:   "wide_thickness",
			Config: "wide",
			Target: types.VariableTarget{Type: "surface", ID: 2, Param: "thickness"},
			Min:    0.1,
			Max:    200.0,
			Active: true,
		},
	}

	configs := []ConfigInput{
		{
			ID:          "wide",
			Weight:      1.0,
			Surfaces:    singletSurfaces(),
			Fields:      []types.FieldItem{{ID: 0, AngleDeg: 0.0, Weight: 1.0}},
			Wavelengths: []types.WavelengthItem{{ID: 0, Value: 0.00058756, Weight: 1.0}},
			MeritTerms: []types.MeritTerm{
				{Field: 0, Wavelength: 0.00058756, Weight: 1.0},
			},
		},
	}

	gc := glass.NewCatalog()

	opt := NewMultiOptimizer(configs, sharedVars, localVars, gc, 1, 0.01, 1e-6, 1e-6, 2.0, 64, 0, 0, nil, nil, 0, 0, false, false, nil, nil)
	x := opt.getInitialState()

	if len(x) != 2 {
		t.Fatalf("Expected 2 variables, got %d", len(x))
	}
	// Shared variable initial value should be read from the first binding's surface (curvature 0.01)
	if math.Abs(x[0]-0.01) > 1e-10 {
		t.Errorf("Shared variable initial = %v, want 0.01", x[0])
	}
	// Local variable initial value reads the surface thickness (100).
	if math.Abs(x[1]-100.0) > 1e-10 {
		t.Errorf("Local variable initial = %v, want 100.0", x[1])
	}
}

func TestMultiOptimizerBuildVariableStates(t *testing.T) {
	sharedVars := []types.SharedVariable{
		{
			Name:   "curvature_shift",
			Min:    -0.1,
			Max:    0.1,
			Active: true,
			Bindings: []types.SharedVariableBinding{
				{Config: "wide", ID: 1, Param: "curvature", Scale: 1.0, Offset: 0.0},
			},
		},
	}

	configs := []ConfigInput{
		{
			ID:          "wide",
			Weight:      1.0,
			Surfaces:    singletSurfaces(),
			Fields:      []types.FieldItem{{ID: 0, AngleDeg: 0.0, Weight: 1.0}},
			Wavelengths: []types.WavelengthItem{{ID: 0, Value: 0.00058756, Weight: 1.0}},
			MeritTerms: []types.MeritTerm{
				{Field: 0, Wavelength: 0.00058756, Weight: 1.0},
			},
		},
	}

	gc := glass.NewCatalog()

	opt := NewMultiOptimizer(configs, sharedVars, nil, gc, 1, 0.01, 1e-6, 1e-6, 2.0, 64, 0, 0, nil, nil, 0, 0, false, false, nil, nil)
	x := []float64{0.03}
	states := opt.buildVariableStates(x)

	if len(states) != 1 {
		t.Fatalf("Expected 1 state, got %d", len(states))
	}
	if states[0].Name != "curvature_shift" {
		t.Errorf("Name = %q, want curvature_shift", states[0].Name)
	}
	if math.Abs(states[0].After-0.03) > 1e-10 {
		t.Errorf("After = %v, want 0.03", states[0].After)
	}
	if math.Abs(states[0].Before-0.01) > 1e-10 {
		t.Errorf("Before = %v, want 0.01", states[0].Before)
	}
}

func TestMultiOptimizerNoGlassCatalog(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	sharedVars := []types.SharedVariable{
		{
			Name:   "curvature_shift",
			Min:    -0.1,
			Max:    0.1,
			Active: true,
			Bindings: []types.SharedVariableBinding{
				{Config: "wide", ID: 1, Param: "curvature", Scale: 1.0, Offset: 0.0},
			},
		},
	}

	configs := []ConfigInput{
		{
			ID:          "wide",
			Weight:      1.0,
			Surfaces:    singletSurfaces(),
			Fields:      []types.FieldItem{{ID: 0, AngleDeg: 0.0, Weight: 1.0}},
			Wavelengths: []types.WavelengthItem{{ID: 0, Value: 0.00058756, Weight: 1.0}},
			MeritTerms: []types.MeritTerm{
				{Field: 0, Wavelength: 0.00058756, Weight: 1.0},
			},
		},
	}

	opt := NewMultiOptimizer(configs, sharedVars, nil, gc, 1, 0.01, 1e-6, 1e-6, 2.0, 64, 0, 0, nil, nil, 0, 0, false, false, nil, nil)
	x := []float64{0.0}
	merit := opt.EvaluateMerit(x)

	if math.IsInf(merit, 0) || math.IsNaN(merit) {
		t.Fatalf("EvaluateMerit returned non-finite value: %v", merit)
	}
	if merit < 0 {
		t.Fatalf("EvaluateMerit returned negative value: %v", merit)
	}
}

func TestMultiOptimizerResultHasExpectedFields(t *testing.T) {
	sharedVars := []types.SharedVariable{
		{
			Name:   "curvature_shift",
			Min:    -0.1,
			Max:    0.1,
			Active: true,
			Bindings: []types.SharedVariableBinding{
				{Config: "wide", ID: 1, Param: "curvature", Scale: 1.0, Offset: 0.0},
			},
		},
	}

	configs := []ConfigInput{
		{
			ID:          "wide",
			Weight:      1.0,
			Surfaces:    singletSurfaces(),
			Fields:      []types.FieldItem{{ID: 0, AngleDeg: 0.0, Weight: 1.0}},
			Wavelengths: []types.WavelengthItem{{ID: 0, Value: 0.00058756, Weight: 1.0}},
			MeritTerms: []types.MeritTerm{
				{Field: 0, Wavelength: 0.00058756, Weight: 1.0},
			},
		},
	}

	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	opt := NewMultiOptimizer(configs, sharedVars, nil, gc, 1, 0.01, 1e-6, 1e-6, 2.0, 64, 0, 0, nil, nil, 0, 0, false, false, nil, nil)
	result := opt.Optimize()

	if result.Status != "max_iterations" {
		t.Errorf("Status = %q, want max_iterations", result.Status)
	}
	if result.Iterations != 1 {
		t.Errorf("Iterations = %d, want 1", result.Iterations)
	}
	if result.BeforeMerit <= 0 {
		t.Errorf("BeforeMerit = %v, want positive", result.BeforeMerit)
	}
	if result.AfterMerit <= 0 {
		t.Errorf("AfterMerit = %v, want positive", result.AfterMerit)
	}
}

// tripletEqualityConfigs returns a US2645157-triplet config with 2 equality
// constraints (abs_efl and entrance_pupil_diameter) on the given targets.
func tripletEqualityConfigs(eflTarget, epdTarget float64) []ConfigInput {
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 1 / 10.2871491742, Thickness: 1.524, Material: types.Material{Key: "SK18"}, Diameter: 10.0},
		{ID: 2, Type: types.Sphere, Curvature: 1 / -239.3967954752, Thickness: 2.3368, Material: types.Material{}, Diameter: 10.0},
		{ID: 3, Type: types.Sphere, Curvature: 1 / -12.8269871730, Thickness: 0.508, Material: types.Material{Key: "SF12"}, Diameter: 6.0},
		{ID: 4, Type: types.Sphere, Curvature: 1 / 10.5917184406, Thickness: 1.4986, Material: types.Material{}, Diameter: 6.0},
		{ID: 5, Type: types.Sphere, Curvature: 0.0, Thickness: 1.016, Material: types.Material{}, Diameter: 3.78},
		{ID: 6, Type: types.Sphere, Curvature: 1 / 61.8456294200, Thickness: 1.524, Material: types.Material{Key: "SK18"}, Diameter: 6.0},
		{ID: 7, Type: types.Sphere, Curvature: 1 / -10.0074859032, Thickness: 21.36695183553, Material: types.Material{}, Diameter: 6.0},
		{ID: 8, Type: types.Sphere, Curvature: 0.0, Thickness: 0.0, Material: types.Material{}, Diameter: 44.0},
	}
	return []ConfigInput{{
		ID:          "cfg1",
		Weight:      1.0,
		StopSurface: 5, // explicit aperture (was the implicit smallest-diameter stop)
		Surfaces:    surfaces,
		Fields:      []types.FieldItem{{ID: 0, AngleDeg: 0.0, Weight: 1.0}, {ID: 1, AngleDeg: 16.0, Weight: 1.0}, {ID: 2, AngleDeg: 24.0, Weight: 0.5}},
		Wavelengths: []types.WavelengthItem{{ID: 0, Value: 0.00058756, Weight: 1.0}},
		MeritTerms: []types.MeritTerm{
			{Field: 0, Wavelength: 0.00058756, Weight: 1.0},
			{Field: 1, Wavelength: 0.00058756, Weight: 1.0},
			{Field: 2, Wavelength: 0.00058756, Weight: 0.5},
		},
		Constraints: []types.ConstraintOperand{
			{Kind: types.ConstraintEquality, Measure: types.MeasureAbsEFL, Target: eflTarget, Weight: 1.0, Active: true},
			{Kind: types.ConstraintEquality, Measure: types.MeasureEntrancePupilDiameter, Target: epdTarget, Weight: 1.0, Active: true},
		},
	}}
}

func tripletGC() *glass.Catalog {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "SK18", ND: 1.63854, VD: 55.42})
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "SF12", ND: 1.64831, VD: 33.84})
	return gc
}

// TestMultiOptimizerSatisfiableEqualityConstraints is a regression test for
// the DLS freeze reported in the improvement report (3.1): multiple equality
// constraints used to freeze the solver (flat merit, no movement). With
// satisfiable targets the solver must make progress and satisfy both
// constraints.
func TestMultiOptimizerSatisfiableEqualityConstraints(t *testing.T) {
	configs := tripletEqualityConfigs(25.0, 4.61)
	localVars := []types.LocalVariableDef{
		{Name: "s1_c", Config: "cfg1", Target: types.VariableTarget{Type: "surface", ID: 1, Param: "curvature"}, Min: 0.05, Max: 0.2, Active: true},
		{Name: "s3_c", Config: "cfg1", Target: types.VariableTarget{Type: "surface", ID: 3, Param: "curvature"}, Min: -0.15, Max: -0.01, Active: true},
		{Name: "s6_c", Config: "cfg1", Target: types.VariableTarget{Type: "surface", ID: 6, Param: "curvature"}, Min: 0.0, Max: 0.05, Active: true},
		{Name: "s7_c", Config: "cfg1", Target: types.VariableTarget{Type: "surface", ID: 7, Param: "curvature"}, Min: -0.2, Max: -0.01, Active: true},
	}

	opt := NewMultiOptimizer(configs, nil, localVars, tripletGC(), 80, 0.01, 1e-6, 1e-6, 1.0, 64, 100, 0, nil, nil, 0, 0, false, false, nil, nil)
	result := opt.Optimize()

	if result.AfterMerit >= result.BeforeMerit {
		t.Errorf("AfterMerit=%v not < BeforeMerit=%v: solver made no progress (freeze?)", result.AfterMerit, result.BeforeMerit)
	}

	x := make([]float64, len(result.Variables))
	for i, vs := range result.Variables {
		x[i] = vs.After
	}
	c := opt.ComputeConstraints(x)
	if len(c) != 2 {
		t.Fatalf("expected 2 constraint residuals, got %d", len(c))
	}
	if math.Abs(c[0]) > 0.05 {
		t.Errorf("abs_efl residual = %v, want ~0 (constraint not satisfied)", c[0])
	}
	if math.Abs(c[1]) > 0.05 {
		t.Errorf("entrance_pupil_diameter residual = %v, want ~0 (constraint not satisfied)", c[1])
	}
}

// TestMultiOptimizerUnsatisfiableConstraintWarns verifies that an
// unreachable equality constraint does not silently "converge": the solver
// keeps running (or converges only once other constraints are satisfied) and
// the violation is reported by FinalConstraintViolations.
func TestMultiOptimizerUnsatisfiableConstraintWarns(t *testing.T) {
	// EPD target 8 is far beyond what this triplet can reach (~4.6).
	configs := tripletEqualityConfigs(25.0, 8.0)
	localVars := []types.LocalVariableDef{
		{Name: "s1_c", Config: "cfg1", Target: types.VariableTarget{Type: "surface", ID: 1, Param: "curvature"}, Min: 0.05, Max: 0.2, Active: true},
		{Name: "s3_c", Config: "cfg1", Target: types.VariableTarget{Type: "surface", ID: 3, Param: "curvature"}, Min: -0.15, Max: -0.01, Active: true},
		{Name: "s6_c", Config: "cfg1", Target: types.VariableTarget{Type: "surface", ID: 6, Param: "curvature"}, Min: 0.0, Max: 0.05, Active: true},
		{Name: "s7_c", Config: "cfg1", Target: types.VariableTarget{Type: "surface", ID: 7, Param: "curvature"}, Min: -0.2, Max: -0.01, Active: true},
	}

	opt := NewMultiOptimizer(configs, nil, localVars, tripletGC(), 80, 0.01, 1e-6, 1e-6, 1.0, 64, 100, 0, nil, nil, 0, 0, false, false, nil, nil)
	result := opt.Optimize()

	if result.Status == "converged" {
		t.Errorf("status = converged although the EPD constraint is unreachable")
	}

	x := make([]float64, len(result.Variables))
	for i, vs := range result.Variables {
		x[i] = vs.After
	}
	viol := opt.FinalConstraintViolations(x, 0.1)
	found := false
	for _, v := range viol {
		if v.Measure == string(types.MeasureEntrancePupilDiameter) {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an entrance_pupil_diameter violation to be reported, got %+v", viol)
	}
}

// TestMultiOptimizerApertureMarginClamp is a regression test for the DLS
// stall reported in the improvement report (3.2): aperture_margin < 1.0
// makes the pupil grid smaller than the aperture and stalls convergence.
// The constructor must clamp it to 1.0.
func TestMultiOptimizerApertureMarginClamp(t *testing.T) {
	configs := []ConfigInput{{
		ID:          "cfg1",
		Weight:      1.0,
		Surfaces:    singletSurfaces(),
		Fields:      []types.FieldItem{{ID: 0, AngleDeg: 0.0, Weight: 1.0}},
		Wavelengths: []types.WavelengthItem{{ID: 0, Value: 0.00058756, Weight: 1.0}},
		MeritTerms:  []types.MeritTerm{{Field: 0, Wavelength: 0.00058756, Weight: 1.0}},
	}}
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	opt := NewMultiOptimizer(configs, nil, nil, gc, 10, 0.01, 1e-6, 1e-6, 0.8, 64, 100, 0, nil, nil, 0, 0, false, false, nil, nil)
	if got := opt.Options().ApertureMargin; got != 1.0 {
		t.Errorf("ApertureMargin = %v, want 1.0 (clamped)", got)
	}
}

// TestMultiOptimizerAsphereVariables is a regression test for the improvement
// report (3.7): asphere coefficients (conic + a4..a12 / coefficient_N) must be
// usable as optimization variables.
func TestMultiOptimizerAsphereVariables(t *testing.T) {
	surfaces := []types.Surface{
		{ID: 1, Type: types.AspherePolynomial, Curvature: 0.01, Conic: 0.0, Coefficients: []float64{1e-5, 0, 0}, Thickness: 10.0, Material: types.Material{Key: "N-BK7"}, Diameter: 50.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 100.0, Material: types.Material{}, Diameter: 50.0},
	}
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	configs := []ConfigInput{{
		ID:          "cfg1",
		Weight:      1.0,
		Surfaces:    surfaces,
		Fields:      []types.FieldItem{{ID: 0, AngleDeg: 0.0, Weight: 1.0}},
		Wavelengths: []types.WavelengthItem{{ID: 0, Value: 0.00058756, Weight: 1.0}},
		MeritTerms:  []types.MeritTerm{{Field: 0, Wavelength: 0.00058756, Weight: 1.0}},
	}}
	localVars := []types.LocalVariableDef{
		{Name: "s1_conic", Config: "cfg1", Target: types.VariableTarget{Type: "surface", ID: 1, Param: "conic"}, Min: -1, Max: 1, Active: true},
		{Name: "s1_a4", Config: "cfg1", Target: types.VariableTarget{Type: "surface", ID: 1, Param: "a4"}, Min: -1e-3, Max: 1e-3, Active: true},
		{Name: "s1_coef1", Config: "cfg1", Target: types.VariableTarget{Type: "surface", ID: 1, Param: "coefficient_1"}, Min: -1e-3, Max: 1e-3, Active: true},
	}

	opt := NewMultiOptimizer(configs, nil, localVars, gc, 5, 0.01, 1e-6, 1e-6, 1.0, 64, 100, 0, nil, nil, 0, 0, false, false, nil, nil)

	// Initial state must read the surface values (a4 = 1e-5, coefficient_1 = 0).
	x := opt.getInitialState()
	if math.Abs(x[1]-1e-5) > 1e-12 {
		t.Errorf("initial a4 = %v, want 1e-5", x[1])
	}
	if math.Abs(x[2]) > 1e-12 {
		t.Errorf("initial coefficient_1 = %v, want 0", x[2])
	}

	// Changing the a4 coefficient must change the merit (the asphere sag
	// affects the traced rays).
	base := opt.EvaluateMerit(x)
	x[1] = 5e-4
	pert := opt.EvaluateMerit(x)
	if math.Abs(pert-base) < 1e-12 {
		t.Errorf("merit unchanged when a4 changes (%v vs %v): asphere variable not applied", base, pert)
	}
}

// TestMultiOptimizerMeritBreakdown is a regression test for the improvement
// report (3.10): MeritBreakdown must decompose the merit into per-term
// contributions whose total matches EvaluateMerit, so the DLS merit value can
// be reconciled with an external evaluation.
func TestMultiOptimizerMeritBreakdown(t *testing.T) {
	configs := tripletEqualityConfigs(25.0, 4.61)
	localVars := []types.LocalVariableDef{
		{Name: "s1_c", Config: "cfg1", Target: types.VariableTarget{Type: "surface", ID: 1, Param: "curvature"}, Min: 0.05, Max: 0.2, Active: true},
		{Name: "s3_c", Config: "cfg1", Target: types.VariableTarget{Type: "surface", ID: 3, Param: "curvature"}, Min: -0.15, Max: -0.01, Active: true},
	}
	opt := NewMultiOptimizer(configs, nil, localVars, tripletGC(), 10, 0.01, 1e-6, 1e-6, 1.0, 64, 100, 0, nil, nil, 0, 0, false, false, nil, nil)

	x := opt.getInitialState()
	bd := opt.MeritBreakdown(x)
	total, ok := bd["objective_total"]
	if !ok {
		t.Fatalf("MeritBreakdown missing objective_total: %v", bd)
	}
	merit := opt.EvaluateMerit(x)
	if math.Abs(total-merit) > 1e-9 {
		t.Errorf("objective_total = %v, want %v (EvaluateMerit)", total, merit)
	}
	// There must be one contribution per merit term.
	if n := len(bd) - 1; n != len(configs[0].MeritTerms) {
		t.Errorf("MeritBreakdown has %d terms, want %d", n, len(configs[0].MeritTerms))
	}
}

// TestTraceFieldGridParallelDeterminism verifies the grid-ray worker loop
// produces identical points and per-surface extents regardless of worker
// count, matching the determinism guarantee of the Jacobian columns.
func TestTraceFieldGridParallelDeterminism(t *testing.T) {
	configs := tripletEqualityConfigs(25.0, 4.61)
	surfaces := configs[0].Surfaces
	gc := tripletGC()
	surface.Precompute(surfaces)

	pts1, ext1 := dls.TraceFieldGrid(gc, surfaces, 0, 0, 10.0, []float64{0, 1}, 0.00058756, 1.0, 64, 0, 1)
	pts4, ext4 := dls.TraceFieldGrid(gc, surfaces, 0, 0, 10.0, []float64{0, 1}, 0.00058756, 1.0, 64, 0, 4)

	if len(pts1) != len(pts4) {
		t.Fatalf("worker=1 gives %d points, worker=4 gives %d", len(pts1), len(pts4))
	}
	for i := range pts1 {
		a, b := pts1[i], pts4[i]
		if a.OK != b.OK || a.X != b.X || a.Y != b.Y || a.OPL != b.OPL {
			t.Errorf("point %d differs: %+v vs %+v", i, a, b)
		}
	}
	for id, e := range ext1 {
		if ext4[id] != e {
			t.Errorf("extent[%d] differs: worker=1 %.15g, worker=4 %.15g", id, e, ext4[id])
		}
	}
}
