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

func singletSurfaces() []types.Surface {
	return []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 10.0, Material: "N-BK7", Diameter: 50.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 100.0, Material: "AIR", Diameter: 50.0},
	}
}

// TestOptimizerSeidelDistortionKind verifies the seidel_distortion merit kind
// is evaluated via the distortion coefficient: with target = current S5 the
// merit must be ~0 (a spot_rms evaluation of the singlet would be far from 0).
func TestOptimizerSeidelDistortionKind(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	surfaces := singletSurfaces()
	surface.Precompute(surfaces)

	s5 := paraxial.ComputeSeidel(surfaces, 10, 0.00058756, gc).Distortion

	cfg := Config{
		Surfaces: surfaces,
		Variables: []Variable{},
		MeritTerms: []MeritTerm{{
			Kind:        MeritSeidelDistortion,
			FieldAngle:  10.0,
			FieldWeight: 1.0,
			Wavelength:  0.00058756,
			WavWeight:   1.0,
			Weight:      1.0,
			Target:      s5,
		}},
		GlassCatalog: gc,
		NumRays:      16,
	}

	opt := NewOptimizer(cfg)
	x := opt.getInitialState()
	merit := opt.EvaluateMerit(x)

	if merit > 1e-12 {
		t.Errorf("merit with target = current S5 = %v, want ~0", merit)
	}
}

func TestOptimizerEvaluateMerit(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	cfg := Config{
		Surfaces:     singletSurfaces(),
		Variables:    []Variable{},
		MeritTerms:   []MeritTerm{{FieldAngle: 0.0, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0}},
		GlassCatalog: gc,
		NumRays:      16,
	}

	opt := NewOptimizer(cfg)
	x := opt.getInitialState()
	merit := opt.EvaluateMerit(x)

	if math.IsInf(merit, 0) || math.IsNaN(merit) {
		t.Fatalf("evaluateMerit returned non-finite value: %v", merit)
	}
	if merit < 0 {
		t.Fatalf("evaluateMerit returned negative value: %v", merit)
	}
}

func TestOptimizerApplyVariablesCurvature(t *testing.T) {
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 10.0, Material: "N-BK7", Diameter: 50.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 100.0, Material: "AIR", Diameter: 50.0},
	}

	cfg := Config{
		Surfaces: surfaces,
		Variables: []Variable{
			{SurfaceID: 1, Param: "curvature", Min: -1.0, Max: 1.0},
		},
		MeritTerms:   []MeritTerm{{FieldAngle: 0.0, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0}},
		GlassCatalog: glass.NewCatalog(),
		NumRays:      4,
	}

	opt := NewOptimizer(cfg)
	surfacesAfter := opt.applyVariables([]float64{0.02})

	found := false
	for _, s := range surfacesAfter {
		if s.ID == 1 {
			found = true
			if math.Abs(s.Curvature-0.02) > 1e-10 {
				t.Errorf("Curvature = %v, want 0.02", s.Curvature)
			}
		}
	}
	if !found {
		t.Error("Surface 1 not found after applyVariables")
	}
}

func TestOptimizerApplyVariablesThickness(t *testing.T) {
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 10.0, Material: "N-BK7", Diameter: 50.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 100.0, Material: "AIR", Diameter: 50.0},
	}

	cfg := Config{
		Surfaces: surfaces,
		Variables: []Variable{
			{SurfaceID: 2, Param: "thickness", Min: 0.1, Max: 200.0},
		},
		MeritTerms:   []MeritTerm{{FieldAngle: 0.0, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0}},
		GlassCatalog: glass.NewCatalog(),
		NumRays:      4,
	}

	opt := NewOptimizer(cfg)
	surfacesAfter := opt.applyVariables([]float64{50.0})

	found := false
	for _, s := range surfacesAfter {
		if s.ID == 2 {
			found = true
			if math.Abs(s.Thickness-50.0) > 1e-10 {
				t.Errorf("Thickness = %v, want 50.0", s.Thickness)
			}
		}
	}
	if !found {
		t.Error("Surface 2 not found after applyVariables")
	}
}

func TestOptimizerResultHasExpectedFields(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 10.0, Material: "N-BK7", Diameter: 50.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 100.0, Material: "AIR", Diameter: 50.0},
	}

	cfg := Config{
		Surfaces: surfaces,
		Variables: []Variable{
			{SurfaceID: 1, Param: "curvature", Min: -1.0, Max: 1.0},
		},
		MeritTerms:   []MeritTerm{{FieldAngle: 0.0, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0}},
		GlassCatalog: gc,
		MaxIter:      1,
		NumRays:      4,
	}

	opt := NewOptimizer(cfg)
	result := opt.Optimize()

	if result.Status != "max_iterations" {
		t.Errorf("Status = %q, want %q (max_iterations for 1 iteration)", result.Status, "max_iterations")
	}
	if result.Iterations != 1 {
		t.Errorf("Iterations = %d, want 1", result.Iterations)
	}
	if result.BeforeMerit <= 0 {
		t.Errorf("BeforeMerit = %v, want positive value", result.BeforeMerit)
	}
	if result.AfterMerit <= 0 {
		t.Errorf("AfterMerit = %v, want positive value", result.AfterMerit)
	}
}

func TestOptimizerCanImproveDegradedSystem(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "SK18", ND: 1.63854, VD: 55.42})
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "SF12", ND: 1.64831, VD: 33.84})

	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.12, Thickness: 1.524, Material: "SK18", Diameter: 10.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.004177, Thickness: 2.3368, Material: "AIR", Diameter: 10.0},
		{ID: 3, Type: types.Sphere, Curvature: -0.05, Thickness: 0.508, Material: "SF12", Diameter: 6.0},
		{ID: 4, Type: types.Sphere, Curvature: 0.094413, Thickness: 1.4986, Material: "AIR", Diameter: 6.0},
		{ID: 5, Type: types.Sphere, Curvature: 0, Thickness: 1.016, Material: "AIR", Diameter: 3.7825297358},
		{ID: 6, Type: types.Sphere, Curvature: 0.025, Thickness: 1.524, Material: "SK18", Diameter: 6.0},
		{ID: 7, Type: types.Sphere, Curvature: -0.15, Thickness: 21.36695183553, Material: "AIR", Diameter: 6.0},
		{ID: 8, Type: types.Sphere, Curvature: 0, Thickness: 0.0, Material: "AIR"},
	}

	terms := []MeritTerm{
		{FieldAngle: 0.0, FieldDir: []float64{0, 1}, FieldWeight: 1.0, Wavelength: 0.0004861, WavWeight: 1.0, Weight: 1.0},
		{FieldAngle: 0.0, FieldDir: []float64{0, 1}, FieldWeight: 1.0, Wavelength: 0.0005876, WavWeight: 1.0, Weight: 1.0},
		{FieldAngle: 0.0, FieldDir: []float64{0, 1}, FieldWeight: 1.0, Wavelength: 0.0006563, WavWeight: 1.0, Weight: 1.0},
		{FieldAngle: 16.0, FieldDir: []float64{0, 1}, FieldWeight: 1.0, Wavelength: 0.0004861, WavWeight: 1.0, Weight: 1.0},
		{FieldAngle: 16.0, FieldDir: []float64{0, 1}, FieldWeight: 1.0, Wavelength: 0.0005876, WavWeight: 1.0, Weight: 1.0},
		{FieldAngle: 16.0, FieldDir: []float64{0, 1}, FieldWeight: 1.0, Wavelength: 0.0006563, WavWeight: 1.0, Weight: 1.0},
		{FieldAngle: 24.0, FieldDir: []float64{0, 1}, FieldWeight: 0.5, Wavelength: 0.0004861, WavWeight: 0.5, Weight: 0.5},
		{FieldAngle: 24.0, FieldDir: []float64{0, 1}, FieldWeight: 0.5, Wavelength: 0.0005876, WavWeight: 0.5, Weight: 0.5},
		{FieldAngle: 24.0, FieldDir: []float64{0, 1}, FieldWeight: 0.5, Wavelength: 0.0006563, WavWeight: 0.5, Weight: 0.5},
	}

	variables := []Variable{
		{Name: "s1_curvature", SurfaceID: 1, Param: "curvature", Min: 0.02, Max: 0.3},
		{Name: "s3_curvature", SurfaceID: 3, Param: "curvature", Min: -0.3, Max: -0.01},
		{Name: "s6_curvature", SurfaceID: 6, Param: "curvature", Min: 0.005, Max: 0.5},
		{Name: "s7_curvature", SurfaceID: 7, Param: "curvature", Min: -0.3, Max: -0.01},
	}

	cfg := Config{
		Surfaces:     surfaces,
		Variables:    variables,
		MeritTerms:   terms,
		GlassCatalog: gc,
		NumRays:      512,
	}

	opt := NewOptimizer(cfg)
	result := opt.Optimize()

	t.Logf("Status: %s, Iterations: %d", result.Status, result.Iterations)
	t.Logf("Before merit: %.6e", result.BeforeMerit)
	t.Logf("After merit: %.6e", result.AfterMerit)

	if result.BeforeMerit <= 0 {
		t.Errorf("BeforeMerit = %v, want positive", result.BeforeMerit)
	}
	if result.AfterMerit <= 0 {
		t.Errorf("AfterMerit = %v, want positive", result.AfterMerit)
	}
	if result.AfterMerit >= result.BeforeMerit {
		t.Errorf("Optimizer did not improve merit: before=%.6e, after=%.6e", result.BeforeMerit, result.AfterMerit)
	}
}

type mockLogger struct {
	iterLogs  []struct {
		Iter        int
		Merit       float64
		Improvement float64
		StepNorm    float64
		Variables   []float64
	}
	finalLogs []struct {
		Iter      int
		Merit     float64
		StepNorm  float64
		Variables []float64
		Status    string
	}
}

func (m *mockLogger) LogIter(iter int, merit, improvement, stepNorm float64, variables []float64, constraints []dls.ConstraintState) {
	m.iterLogs = append(m.iterLogs, struct {
		Iter        int
		Merit       float64
		Improvement float64
		StepNorm    float64
		Variables   []float64
	}{iter, merit, improvement, stepNorm, variables})
	_ = constraints
}

func (m *mockLogger) LogFinal(iter int, status string, merit float64, stepNorm float64, variables []float64, constraints []dls.ConstraintState) {
	m.finalLogs = append(m.finalLogs, struct {
		Iter      int
		Merit     float64
		StepNorm  float64
		Variables []float64
		Status    string
	}{iter, merit, stepNorm, variables, status})
	_ = constraints
}

func TestOptimizerApplyVariablesND(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	cfg := Config{
		Surfaces: singletSurfaces(),
		Variables: []Variable{
			{SurfaceID: 1, Param: "nd", Min: 1.4, Max: 1.9},
		},
		MeritTerms:   []MeritTerm{{FieldAngle: 0.0, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0}},
		GlassCatalog: gc,
		NumRays:      4,
	}

	opt := NewOptimizer(cfg)
	opt.applyVariables([]float64{1.7})

	g, ok := opt.tempGC.Lookup("N-BK7")
	if !ok {
		t.Fatal("N-BK7 not found in tempGC after applyVariables")
	}
	if g.ND != 1.7 {
		t.Errorf("N-BK7 ND = %v, want 1.7", g.ND)
	}
}

func TestOptimizerApplyVariablesVD(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	cfg := Config{
		Surfaces: singletSurfaces(),
		Variables: []Variable{
			{SurfaceID: 1, Param: "vd", Min: 20, Max: 80},
		},
		MeritTerms:   []MeritTerm{{FieldAngle: 0.0, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0}},
		GlassCatalog: gc,
		NumRays:      4,
	}

	opt := NewOptimizer(cfg)
	opt.applyVariables([]float64{50.0})

	g, ok := opt.tempGC.Lookup("N-BK7")
	if !ok {
		t.Fatal("N-BK7 not found in tempGC after applyVariables")
	}
	if g.VD != 50.0 {
		t.Errorf("N-BK7 VD = %v, want 50.0", g.VD)
	}
}

func TestOptimizerGetInitialStateGlass(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	cfg := Config{
		Surfaces: singletSurfaces(),
		Variables: []Variable{
			{Name: "bk7_nd", SurfaceID: 1, Param: "nd", Min: 1.4, Max: 1.9},
			{Name: "bk7_vd", SurfaceID: 1, Param: "vd", Min: 20, Max: 80},
		},
		MeritTerms:   []MeritTerm{{FieldAngle: 0.0, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0}},
		GlassCatalog: gc,
		NumRays:      4,
	}

	opt := NewOptimizer(cfg)
	x := opt.getInitialState()

	if math.Abs(x[0]-1.5168) > 1e-10 {
		t.Errorf("initial nd = %v, want 1.5168", x[0])
	}
	if math.Abs(x[1]-64.17) > 1e-10 {
		t.Errorf("initial vd = %v, want 64.17", x[1])
	}
}

func TestOptimizerGetInitialStateGlassNotFound(t *testing.T) {
	gc := glass.NewCatalog()

	cfg := Config{
		Surfaces: singletSurfaces(),
		Variables: []Variable{
			{Name: "nd", SurfaceID: 99, Param: "nd", Min: 1.4, Max: 1.9},
		},
		MeritTerms:   []MeritTerm{{FieldAngle: 0.0, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0}},
		GlassCatalog: gc,
		NumRays:      4,
	}

	opt := NewOptimizer(cfg)
	x := opt.getInitialState()

	expected := (1.4 + 1.9) / 2
	if math.Abs(x[0]-expected) > 1e-10 {
		t.Errorf("initial state = %v, want %v (midpoint fallback)", x[0], expected)
	}
}

func TestBuildVariableStatesGlass(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	cfg := Config{
		Surfaces: singletSurfaces(),
		Variables: []Variable{
			{Name: "bk7_nd", SurfaceID: 1, Param: "nd", Min: 1.4, Max: 1.9},
		},
		MeritTerms:   []MeritTerm{{FieldAngle: 0.0, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0}},
		GlassCatalog: gc,
		NumRays:      4,
	}

	opt := NewOptimizer(cfg)
	states := opt.buildVariableStates([]float64{1.7})

	if len(states) != 1 {
		t.Fatalf("expected 1 state, got %d", len(states))
	}
	if states[0].GlassName != "N-BK7" {
		t.Errorf("GlassName = %q, want N-BK7", states[0].GlassName)
	}
	if math.Abs(states[0].Before-1.5168) > 1e-10 {
		t.Errorf("Before = %v, want 1.5168", states[0].Before)
	}
	if math.Abs(states[0].After-1.7) > 1e-10 {
		t.Errorf("After = %v, want 1.7", states[0].After)
	}
	if states[0].SurfaceID != 1 {
		t.Errorf("SurfaceID = %d, want 1", states[0].SurfaceID)
	}
}

func TestOptimizerLoggerCalled(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "SK18", ND: 1.63854, VD: 55.42})
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "SF12", ND: 1.64831, VD: 33.84})

	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.05, Thickness: 1.524, Material: "SK18", Diameter: 10.0},
		{ID: 2, Type: types.Sphere, Curvature: 0, Thickness: 2.3368, Material: "AIR", Diameter: 10.0},
		{ID: 3, Type: types.Sphere, Curvature: -0.05, Thickness: 0.508, Material: "SF12", Diameter: 6.0},
		{ID: 4, Type: types.Sphere, Curvature: 0.094413, Thickness: 1.4986, Material: "AIR", Diameter: 6.0},
		{ID: 5, Type: types.Sphere, Curvature: 0, Thickness: 1.016, Material: "AIR", Diameter: 3.7825297358},
		{ID: 6, Type: types.Sphere, Curvature: 0.025, Thickness: 1.524, Material: "SK18", Diameter: 6.0},
		{ID: 7, Type: types.Sphere, Curvature: -0.15, Thickness: 21.36695183553, Material: "AIR", Diameter: 6.0},
		{ID: 8, Type: types.Sphere, Curvature: 0, Thickness: 0.0, Material: "AIR"},
	}

	terms := []MeritTerm{
		{FieldAngle: 0.0, FieldDir: []float64{0, 1}, FieldWeight: 1.0, Wavelength: 0.0004861, WavWeight: 1.0, Weight: 1.0},
		{FieldAngle: 16.0, FieldDir: []float64{0, 1}, FieldWeight: 1.0, Wavelength: 0.0005876, WavWeight: 1.0, Weight: 1.0},
	}

	variables := []Variable{
		{Name: "s1_curvature", SurfaceID: 1, Param: "curvature", Min: 0.02, Max: 0.3},
		{Name: "s3_curvature", SurfaceID: 3, Param: "curvature", Min: -0.3, Max: -0.01},
	}

	logger := &mockLogger{}
	cfg := Config{
		Surfaces:     surfaces,
		Variables:    variables,
		MeritTerms:   terms,
		GlassCatalog: gc,
		NumRays:      32,
		Logger:       logger,
	}

	opt := NewOptimizer(cfg)
	result := opt.Optimize()

	if len(logger.iterLogs) == 0 {
		t.Error("LogIter was not called during optimization")
	}
	if len(logger.finalLogs) != 1 {
		t.Errorf("expected 1 LogFinal call, got %d", len(logger.finalLogs))
	}
	if len(logger.finalLogs) > 0 {
		fl := logger.finalLogs[0]
		if fl.Status != result.Status {
			t.Errorf("final log status = %q, want %q", fl.Status, result.Status)
		}
		if fl.Iter != result.Iterations {
			t.Errorf("final log iter = %d, want %d", fl.Iter, result.Iterations)
		}
	}
	if len(logger.iterLogs) == 0 {
		t.Error("LogIter was not called")
	}
	if len(logger.iterLogs) > result.Iterations {
		t.Errorf("LogIter called %d times, want at most %d (iterations)", len(logger.iterLogs), result.Iterations)
	}
}
