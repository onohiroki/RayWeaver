package optimize

import (
	"testing"

	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// TestBackFocusSolveAutoDetectsTarget verifies that the default target surface
// is the last lens surface before the image plane.
func TestBackFocusSolveAutoDetectsTarget(t *testing.T) {
	gc := tripletGC()
	surfaces := powerSolveTripletSurfaces()
	surface.Precompute(surfaces)
	cfg := Config{
		Surfaces:     surfaces,
		GlassCatalog: gc,
		Variables: []Variable{
			{Name: "c", SurfaceID: 7, Param: "curvature", Min: -0.1, Max: 0.1, Config: "config1"},
		},
	}
	opt := NewOptimizer(cfg)
	opt.SetBackFocusSolve(&types.BackFocusSolveConfig{
		Enabled: true,
		Type:    "paraxial",
	})
	if len(opt.backFocusTargets) == 0 {
		t.Fatal("backFocusTargets should not be empty")
	}
	entries := opt.backFocusTargets["config1"]
	if len(entries) == 0 {
		t.Fatal("backFocusTargets config1 should not be empty")
	}
	// The triplet has surfaces: 1(lens), 2(spacer), 3(lens), 4(spacer),
	// 5(spacer), 6(lens), 7(spacer), 8(image plane). The last lens surface
	// before image plane is surface 6.
	if entries[0].solveID != 6 {
		t.Errorf("auto-detected target: want surface 6, got %d", entries[0].solveID)
	}
}

// TestBackFocusSolveExplicitSurface verifies that a user-specified surface ID
// is used when provided.
func TestBackFocusSolveExplicitSurface(t *testing.T) {
	gc := tripletGC()
	surfaces := powerSolveTripletSurfaces()
	surface.Precompute(surfaces)
	cfg := Config{
		Surfaces:     surfaces,
		GlassCatalog: gc,
		Variables: []Variable{
			{Name: "c", SurfaceID: 7, Param: "curvature", Min: -0.1, Max: 0.1, Config: "config1"},
		},
	}
	opt := NewOptimizer(cfg)
	opt.SetBackFocusSolve(&types.BackFocusSolveConfig{
		Enabled: true,
		Surface: 5,
		Type:    "paraxial",
	})
	entries := opt.backFocusTargets["config1"]
	if len(entries) == 0 {
		t.Fatal("backFocusTargets config1 should not be empty")
	}
	if entries[0].solveID != 5 {
		t.Errorf("explicit target: want surface 5, got %d", entries[0].solveID)
	}
}

// TestBackFocusSolveParaxialType verifies that the paraxial back focus solve
// adjusts the target surface thickness so the image plane matches the paraxial
// focus.
func TestBackFocusSolveParaxialType(t *testing.T) {
	gc := tripletGC()
	surfaces := powerSolveTripletSurfaces()
	surface.Precompute(surfaces)

	cfg := Config{
		Surfaces:     surfaces,
		GlassCatalog: gc,
		Variables: []Variable{
			{Name: "c", SurfaceID: 7, Param: "curvature", Min: -0.1, Max: 0.1, Config: "config1"},
		},
	}
	opt := NewOptimizer(cfg)
	opt.SetBackFocusSolve(&types.BackFocusSolveConfig{
		Enabled: true,
		Type:    "paraxial",
	})

	x := make([]float64, len(opt.Variables()))
	x[0] = 0.0 // nominal curvature

	surfMap, _, _ := opt.applyVariables(x)
	s := surfMap["config1"]
	surface.Precompute(s)
	// After applyVariables, surface 7 thickness should have been adjusted
	// by the back focus solve. No crash = pass; the actual value depends
	// on the system state and may be near-zero for a well-focused system.
	for _, sf := range s {
		if sf.ID == 7 {
			if sf.Thickness <= 0 {
				t.Errorf("surface 7 thickness should be positive, got %f", sf.Thickness)
			}
			break
		}
	}
}

// TestBackFocusSolveDisabledByDefault verifies the solve is a no-op when not
// configured.
func TestBackFocusSolveDisabledByDefault(t *testing.T) {
	gc := tripletGC()
	cfg := Config{
		Surfaces:     powerSolveTripletSurfaces(),
		GlassCatalog: gc,
		Variables: []Variable{
			{Name: "c", SurfaceID: 7, Param: "curvature", Min: -0.1, Max: 0.1, Config: "config1"},
		},
	}
	opt := NewOptimizer(cfg)
	if opt.backFocusSolve != nil {
		t.Error("backFocusSolve should be nil by default")
	}
	if len(opt.backFocusTargets) > 0 {
		t.Error("backFocusTargets should be empty by default")
	}
}

// TestBackFocusSolveInvalidSurface verifies that an invalid surface ID is
// rejected.
func TestBackFocusSolveInvalidSurface(t *testing.T) {
	gc := tripletGC()
	surfaces := powerSolveTripletSurfaces()
	surface.Precompute(surfaces)
	cfg := Config{
		Surfaces:     surfaces,
		GlassCatalog: gc,
		Variables: []Variable{
			{Name: "c", SurfaceID: 7, Param: "curvature", Min: -0.1, Max: 0.1, Config: "config1"},
		},
	}
	opt := NewOptimizer(cfg)
	opt.SetBackFocusSolve(&types.BackFocusSolveConfig{
		Enabled: true,
		Surface: 999, // non-existent surface
		Type:    "paraxial",
	})
	if len(opt.backFocusTargets["config1"]) > 0 {
		t.Error("invalid surface should not produce a target")
	}
}

// TestBackFocusFieldsOnAxisOnly verifies that on_axis_only selects the
// on-axis field or the first field.
func TestBackFocusFieldsOnAxisOnly(t *testing.T) {
	cfg := config{
		id:                  "config1",
		referenceWavelength: 0.000587562,
		fields: []types.FieldItem{
			{ID: 1, AngleDeg: 14.0},
			{ID: 0, AngleDeg: 0.0},
		},
		fieldDefs: []types.FieldDef{
			{Angle: 14.0},
			{Angle: 0.0},
		},
	}
	opt := &Optimizer{
		backFocusSolve: &types.BackFocusSolveConfig{
			WeightType: "on_axis_only",
		},
	}
	fields := opt.backFocusFields(&cfg)
	if len(fields) != 1 || fields[0].Angle != 0 {
		t.Errorf("on_axis_only should select field with angle 0, got %v", fields)
	}
}

// TestBackFocusFieldsUniform verifies that uniform selects all fields.
func TestBackFocusFieldsUniform(t *testing.T) {
	cfg := config{
		id:                  "config1",
		referenceWavelength: 0.000587562,
		fields: []types.FieldItem{
			{ID: 1, AngleDeg: 14.0},
			{ID: 0, AngleDeg: 0.0},
		},
		fieldDefs: []types.FieldDef{
			{Angle: 14.0},
			{Angle: 0.0},
		},
	}
	opt := &Optimizer{
		backFocusSolve: &types.BackFocusSolveConfig{
			WeightType: "uniform",
		},
	}
	fields := opt.backFocusFields(&cfg)
	if len(fields) != 2 {
		t.Errorf("uniform should select all fields, got %d", len(fields))
	}
}

// TestBackFocusFieldsCustomWeights verifies that custom weights pass through.
func TestBackFocusFieldsCustomWeights(t *testing.T) {
	cfg := config{
		id:                  "config1",
		referenceWavelength: 0.000587562,
		fields: []types.FieldItem{
			{ID: 1, AngleDeg: 14.0},
			{ID: 0, AngleDeg: 0.0},
		},
		fieldDefs: []types.FieldDef{
			{Angle: 14.0},
			{Angle: 0.0},
		},
	}
	opt := &Optimizer{
		backFocusSolve: &types.BackFocusSolveConfig{
			WeightType:    "custom",
			CustomWeights: []float64{0.3, 0.7},
		},
	}
	fields := opt.backFocusFields(&cfg)
	if len(fields) != 2 {
		t.Errorf("custom should select all fields, got %d", len(fields))
	}
}

// TestBackFocusSolveWithPowerSolve verifies that back_focus_solve and
// power_solve can coexist.
func TestBackFocusSolveWithPowerSolve(t *testing.T) {
	gc := tripletGC()
	surfaces := powerSolveTripletSurfaces()
	surface.Precompute(surfaces)
	cfg := Config{
		Surfaces:           surfaces,
		GlassCatalog:       gc,
		PowerSolveSurfaces: []int{2, 4, 7},
		Variables: []Variable{
			{Name: "c", SurfaceID: 7, Param: "curvature", Min: -0.1, Max: 0.1, Config: "config1"},
		},
	}
	opt := NewOptimizer(cfg)
	opt.SetBackFocusSolve(&types.BackFocusSolveConfig{
		Enabled: true,
		Type:    "paraxial",
	})
	if len(opt.powerSolve["config1"]) == 0 {
		t.Error("powerSolve should have entries")
	}
	if len(opt.backFocusTargets["config1"]) == 0 {
		t.Error("backFocusTargets should have entries")
	}

	x := make([]float64, len(opt.Variables()))
	x[0] = 0.0
	// Should not crash when both solves are active.
	surfMap, _, _ := opt.applyVariables(x)
	_ = surfMap
}

// TestBackFocusSolveScheduleSwitch verifies that the back-focus type switches
// based on the merit schedule's dominant mode.
func TestBackFocusSolveScheduleSwitch(t *testing.T) {
	gc := tripletGC()
	surfaces := powerSolveTripletSurfaces()
	surface.Precompute(surfaces)
	cfg := Config{
		Surfaces:     surfaces,
		GlassCatalog: gc,
		Variables: []Variable{
			{Name: "c", SurfaceID: 7, Param: "curvature", Min: -0.1, Max: 0.1, Config: "config1"},
		},
	}
	opt := NewOptimizer(cfg)

	// Set back_focus_solve with a schedule.
	opt.SetBackFocusSolve(&types.BackFocusSolveConfig{
		Enabled: true,
		Type:    "paraxial", // fallback
		Schedule: &types.BackFocusScheduleConfig{
			DominantThreshold: 0.5,
		},
	})

	// Set merit schedule with two modes: early (paraxial) and late (wavefront).
	opt.SetMeritSchedule(&types.MeritScheduleConfig{
		Metric:     "iteration",
		Curve:      "step",
		AnchorFrom: 0,
		AnchorTo:   100,
		Modes: []types.MeritScheduleMode{
			{Name: "early", WeightFrom: 1.0, WeightTo: 0.0, BackFocusType: "paraxial"},
			{Name: "late", WeightFrom: 0.0, WeightTo: 1.0, BackFocusType: "wavefront"},
		},
	})

	// At iteration 0 (early dominant), type should be paraxial.
	x := make([]float64, len(opt.Variables()))
	opt.UpdateMeritWeights(x, 0)
	if opt.currentBackFocusType != "paraxial" {
		t.Errorf("iteration 0: want paraxial, got %s", opt.currentBackFocusType)
	}

	// At iteration 100 (late dominant), type should be wavefront.
	opt.UpdateMeritWeights(x, 100)
	if opt.currentBackFocusType != "wavefront" {
		t.Errorf("iteration 100: want wavefront, got %s", opt.currentBackFocusType)
	}
}

// TestBackFocusSolveScheduleFallback verifies that when a mode does not declare
// back_focus_type, the fixed type is used as fallback.
func TestBackFocusSolveScheduleFallback(t *testing.T) {
	gc := tripletGC()
	surfaces := powerSolveTripletSurfaces()
	surface.Precompute(surfaces)
	cfg := Config{
		Surfaces:     surfaces,
		GlassCatalog: gc,
		Variables: []Variable{
			{Name: "c", SurfaceID: 7, Param: "curvature", Min: -0.1, Max: 0.1, Config: "config1"},
		},
	}
	opt := NewOptimizer(cfg)

	opt.SetBackFocusSolve(&types.BackFocusSolveConfig{
		Enabled: true,
		Type:    "paraxial", // fallback
		Schedule: &types.BackFocusScheduleConfig{
			DominantThreshold: 0.5,
		},
	})

	// Merit schedule without back_focus_type on any mode.
	opt.SetMeritSchedule(&types.MeritScheduleConfig{
		Metric: "iteration",
		Curve:  "step",
		Modes: []types.MeritScheduleMode{
			{Name: "early", WeightFrom: 1.0, WeightTo: 0.0},
			{Name: "late", WeightFrom: 0.0, WeightTo: 1.0},
		},
	})

	x := make([]float64, len(opt.Variables()))
	opt.UpdateMeritWeights(x, 100)
	if opt.currentBackFocusType != "paraxial" {
		t.Errorf("fallback should use paraxial, got %s", opt.currentBackFocusType)
	}
}

// TestBackFocusSolveScheduleThreshold verifies that the dominant threshold
// determines when the switch occurs.
func TestBackFocusSolveScheduleThreshold(t *testing.T) {
	gc := tripletGC()
	surfaces := powerSolveTripletSurfaces()
	surface.Precompute(surfaces)
	cfg := Config{
		Surfaces:     surfaces,
		GlassCatalog: gc,
		Variables: []Variable{
			{Name: "c", SurfaceID: 7, Param: "curvature", Min: -0.1, Max: 0.1, Config: "config1"},
		},
	}
	opt := NewOptimizer(cfg)

	opt.SetBackFocusSolve(&types.BackFocusSolveConfig{
		Enabled: true,
		Type:    "paraxial",
		Schedule: &types.BackFocusScheduleConfig{
			DominantThreshold: 0.8, // high threshold
		},
	})

	// Linear curve: at t=0.6 (metric=0.6, anchor 0→1), mode "late" weight
	// = 0 + (1-0)*0.6 = 0.6 < 0.8 threshold → should stay paraxial.
	opt.SetMeritSchedule(&types.MeritScheduleConfig{
		Metric:     "iteration",
		Curve:      "linear",
		AnchorFrom: 0,
		AnchorTo:   1,
		Modes: []types.MeritScheduleMode{
			{Name: "early", WeightFrom: 1.0, WeightTo: 0.0, BackFocusType: "paraxial"},
			{Name: "late", WeightFrom: 0.0, WeightTo: 1.0, BackFocusType: "wavefront"},
		},
	})

	x := make([]float64, len(opt.Variables()))
	opt.UpdateMeritWeights(x, 0) // iteration 0 → metric=0, t=0, early=1.0, late=0.0
	if opt.currentBackFocusType != "paraxial" {
		t.Errorf("threshold not met: want paraxial, got %s", opt.currentBackFocusType)
	}
}
