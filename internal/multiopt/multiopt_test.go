package multiopt

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/types"
)

func singletSurfaces() []types.Surface {
	return []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 10.0, Material: "N-BK7", Diameter: 50.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 100.0, Material: "AIR", Diameter: 50.0},
	}
}

func TestMultiOptimizerApplySharedVariables(t *testing.T) {
	sharedVars := []types.SharedVariable{
		{
			Name:  "curvature_shift",
			Min:   -0.1,
			Max:   0.1,
			Active: true,
			Bindings: []types.SharedVariableBinding{
				{Config: "wide", ID: 1, Param: "curvature", Scale: 1.0, Offset: 0.0},
				{Config: "tele", ID: 1, Param: "curvature", Scale: 1.0, Offset: 0.0},
			},
		},
	}

	configs := []ConfigInput{
		{
			ID:       "wide",
			Weight:   1.0,
			Surfaces: singletSurfaces(),
			Fields:   []types.FieldItem{{ID: 0, AngleDeg: 0.0, Weight: 1.0}},
			Wavelengths: []types.WavelengthItem{{ID: 0, Value: 0.00058756, Weight: 1.0}},
			MeritTerms: []types.MeritTerm{
				{Field: 0, Wavelength: 0.00058756, Weight: 1.0},
			},
		},
		{
			ID:       "tele",
			Weight:   1.0,
			Surfaces: singletSurfaces(),
			Fields:   []types.FieldItem{{ID: 0, AngleDeg: 0.0, Weight: 1.0}},
			Wavelengths: []types.WavelengthItem{{ID: 0, Value: 0.00058756, Weight: 1.0}},
			MeritTerms: []types.MeritTerm{
				{Field: 0, Wavelength: 0.00058756, Weight: 1.0},
			},
		},
	}

	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	opt := New(configs, sharedVars, nil, gc, 1, 0.01, 1e-6, 1e-6, 2.0, 64, 0, nil)

	x := []float64{0.05}
	configSurfaces := opt.applyVariables(x)

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
			ID:       "wide",
			Weight:   1.0,
			Surfaces: singletSurfaces(),
			Fields:   []types.FieldItem{{ID: 0, AngleDeg: 0.0, Weight: 1.0}},
			Wavelengths: []types.WavelengthItem{{ID: 0, Value: 0.00058756, Weight: 1.0}},
			MeritTerms: []types.MeritTerm{
				{Field: 0, Wavelength: 0.00058756, Weight: 1.0},
			},
		},
	}

	gc := glass.NewCatalog()

	opt := New(configs, nil, localVars, gc, 1, 0.01, 1e-6, 1e-6, 2.0, 64, 0, nil)
	x := []float64{75.0}
	configSurfaces := opt.applyVariables(x)

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

func TestMultiOptimizerEvaluateMerit(t *testing.T) {
	sharedVars := []types.SharedVariable{
		{
			Name:  "curvature_shift",
			Min:   -0.1,
			Max:   0.1,
			Active: true,
			Bindings: []types.SharedVariableBinding{
				{Config: "wide", ID: 1, Param: "curvature", Scale: 1.0, Offset: 0.0},
				{Config: "tele", ID: 1, Param: "curvature", Scale: 1.0, Offset: 0.0},
			},
		},
	}

	configs := []ConfigInput{
		{
			ID:       "wide",
			Weight:   1.0,
			Surfaces: singletSurfaces(),
			Fields:   []types.FieldItem{{ID: 0, AngleDeg: 0.0, Weight: 1.0}},
			Wavelengths: []types.WavelengthItem{{ID: 0, Value: 0.00058756, Weight: 1.0}},
			MeritTerms: []types.MeritTerm{
				{Field: 0, Wavelength: 0.00058756, Weight: 1.0},
			},
		},
		{
			ID:       "tele",
			Weight:   1.0,
			Surfaces: singletSurfaces(),
			Fields:   []types.FieldItem{{ID: 0, AngleDeg: 0.0, Weight: 1.0}},
			Wavelengths: []types.WavelengthItem{{ID: 0, Value: 0.00058756, Weight: 1.0}},
			MeritTerms: []types.MeritTerm{
				{Field: 0, Wavelength: 0.00058756, Weight: 1.0},
			},
		},
	}

	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	opt := New(configs, sharedVars, nil, gc, 1, 0.01, 1e-6, 1e-6, 2.0, 64, 0, nil)
	x := []float64{0.0}
	merit := opt.evaluateMerit(x)

	if math.IsInf(merit, 0) || math.IsNaN(merit) {
		t.Fatalf("evaluateMerit returned non-finite value: %v", merit)
	}
	if merit < 0 {
		t.Fatalf("evaluateMerit returned negative value: %v", merit)
	}
}

func TestMultiOptimizerGetInitialState(t *testing.T) {
	sharedVars := []types.SharedVariable{
		{
			Name:  "curvature_shift",
			Min:   -0.1,
			Max:   0.1,
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
			ID:       "wide",
			Weight:   1.0,
			Surfaces: singletSurfaces(),
			Fields:   []types.FieldItem{{ID: 0, AngleDeg: 0.0, Weight: 1.0}},
			Wavelengths: []types.WavelengthItem{{ID: 0, Value: 0.00058756, Weight: 1.0}},
			MeritTerms: []types.MeritTerm{
				{Field: 0, Wavelength: 0.00058756, Weight: 1.0},
			},
		},
	}

	gc := glass.NewCatalog()

	opt := New(configs, sharedVars, localVars, gc, 1, 0.01, 1e-6, 1e-6, 2.0, 64, 0, nil)
	x := opt.getInitialState()

	if len(x) != 2 {
		t.Fatalf("Expected 2 variables, got %d", len(x))
	}
	// Shared variable initial value should be read from the first binding's surface (curvature 0.01)
	if math.Abs(x[0]-0.01) > 1e-10 {
		t.Errorf("Shared variable initial = %v, want 0.01", x[0])
	}
	// Local variable initial value should be midpoint of [0.1, 200] if not found
	// But since curvature of surface 2 is -0.01, thickness should be 100
	if math.Abs(x[1]-100.0) > 1e-10 {
		t.Errorf("Local variable initial = %v, want 100.0", x[1])
	}
}

func TestMultiOptimizerBuildVariableStates(t *testing.T) {
	sharedVars := []types.SharedVariable{
		{
			Name:  "curvature_shift",
			Min:   -0.1,
			Max:   0.1,
			Active: true,
			Bindings: []types.SharedVariableBinding{
				{Config: "wide", ID: 1, Param: "curvature", Scale: 1.0, Offset: 0.0},
			},
		},
	}

	configs := []ConfigInput{
		{
			ID:       "wide",
			Weight:   1.0,
			Surfaces: singletSurfaces(),
			Fields:   []types.FieldItem{{ID: 0, AngleDeg: 0.0, Weight: 1.0}},
			Wavelengths: []types.WavelengthItem{{ID: 0, Value: 0.00058756, Weight: 1.0}},
			MeritTerms: []types.MeritTerm{
				{Field: 0, Wavelength: 0.00058756, Weight: 1.0},
			},
		},
	}

	gc := glass.NewCatalog()

	opt := New(configs, sharedVars, nil, gc, 1, 0.01, 1e-6, 1e-6, 2.0, 64, 0, nil)
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
			Name:  "curvature_shift",
			Min:   -0.1,
			Max:   0.1,
			Active: true,
			Bindings: []types.SharedVariableBinding{
				{Config: "wide", ID: 1, Param: "curvature", Scale: 1.0, Offset: 0.0},
			},
		},
	}

	configs := []ConfigInput{
		{
			ID:       "wide",
			Weight:   1.0,
			Surfaces: singletSurfaces(),
			Fields:   []types.FieldItem{{ID: 0, AngleDeg: 0.0, Weight: 1.0}},
			Wavelengths: []types.WavelengthItem{{ID: 0, Value: 0.00058756, Weight: 1.0}},
			MeritTerms: []types.MeritTerm{
				{Field: 0, Wavelength: 0.00058756, Weight: 1.0},
			},
		},
	}

	opt := New(configs, sharedVars, nil, gc, 1, 0.01, 1e-6, 1e-6, 2.0, 64, 0, nil)
	x := []float64{0.0}
	merit := opt.evaluateMerit(x)

	if math.IsInf(merit, 0) || math.IsNaN(merit) {
		t.Fatalf("evaluateMerit returned non-finite value: %v", merit)
	}
	if merit < 0 {
		t.Fatalf("evaluateMerit returned negative value: %v", merit)
	}
}

func TestMultiOptimizerResultHasExpectedFields(t *testing.T) {
	sharedVars := []types.SharedVariable{
		{
			Name:  "curvature_shift",
			Min:   -0.1,
			Max:   0.1,
			Active: true,
			Bindings: []types.SharedVariableBinding{
				{Config: "wide", ID: 1, Param: "curvature", Scale: 1.0, Offset: 0.0},
			},
		},
	}

	configs := []ConfigInput{
		{
			ID:       "wide",
			Weight:   1.0,
			Surfaces: singletSurfaces(),
			Fields:   []types.FieldItem{{ID: 0, AngleDeg: 0.0, Weight: 1.0}},
			Wavelengths: []types.WavelengthItem{{ID: 0, Value: 0.00058756, Weight: 1.0}},
			MeritTerms: []types.MeritTerm{
				{Field: 0, Wavelength: 0.00058756, Weight: 1.0},
			},
		},
	}

	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	opt := New(configs, sharedVars, nil, gc, 1, 0.01, 1e-6, 1e-6, 2.0, 64, 0, nil)
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