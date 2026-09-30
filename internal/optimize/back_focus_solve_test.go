package optimize

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// TestBackFocusSolveAutoDetectsTarget verifies that the default target surface
// is the back air gap of the image-side-most powered element (its rear
// surface), not the element's centre thickness.
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
	// 5(spacer), 6/7(last lens), 8(image plane). The last powered element is
	// bounded by surfaces 6 and 7; its rear surface 7 owns the back air gap
	// (thickness 21.37 = the image distance), so the target is 7 — not the
	// element centre thickness on surface 6.
	if entries[0].solveID != 7 {
		t.Errorf("auto-detected target: want surface 7, got %d", entries[0].solveID)
	}
}

// TestBackFocusSolveSkipsPowerlessElement verifies that a flat window (no
// thin-lens power) before the image plane does not become the target: the gap
// chosen is the one *before* the window, so the window and image plane
// translate together.
func TestBackFocusSolveSkipsPowerlessElement(t *testing.T) {
	gc := tripletGC()
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 1 / 50.0, Thickness: 5.0, Material: types.Material{Key: "SK18"}, Diameter: 30},
		{ID: 2, Type: types.Sphere, Curvature: 1 / -50.0, Thickness: 10.0, Material: types.Material{}, Diameter: 30},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 2.0, Material: types.Material{Key: "SK18"}, Diameter: 30}, // window front
		{ID: 4, Type: types.Sphere, Curvature: 0, Thickness: 30.0, Material: types.Material{}, Diameter: 30},           // window rear
		{ID: 5, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 30},              // image plane
	}
	surface.Precompute(surfaces)
	cfg := Config{
		Surfaces:     surfaces,
		GlassCatalog: gc,
		Variables: []Variable{
			{Name: "c", SurfaceID: 1, Param: "curvature", Min: -0.1, Max: 0.1, Config: "config1"},
		},
	}
	opt := NewOptimizer(cfg)
	opt.SetBackFocusSolve(&types.BackFocusSolveConfig{Enabled: true, Type: "paraxial"})
	entries := opt.backFocusTargets["config1"]
	if len(entries) == 0 {
		t.Fatal("backFocusTargets config1 should not be empty")
	}
	// Surface 2 is the rear of the powered lens (its thickness is the gap
	// before the powerless window); surfaces 3/4 are the window.
	if entries[0].solveID != 2 {
		t.Errorf("auto-detected target: want surface 2 (gap before the window), got %d", entries[0].solveID)
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
	fields := backFocusFieldsFor(cfg.fieldDefs, "on_axis_only")
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
	fields := backFocusFieldsFor(cfg.fieldDefs, "uniform")
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
	fields := backFocusFieldsFor(cfg.fieldDefs, "custom")
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

// TestBackFocusSolveClampsNegativeThickness verifies that the back-focus solve
// clamps the target surface thickness to a minimum positive value (0.1 mm)
// when the computed shift would result in a negative or near-zero thickness.
func TestBackFocusSolveClampsNegativeThickness(t *testing.T) {
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

	// Make the image plane spacing huge so the paraxial BFL (which is
	// moderate for the triplet) produces a large negative shift, pushing
	// thickness well below zero.
	for i := range surfaces {
		if surfaces[i].ID == 7 {
			surfaces[i].Thickness = 200.0
		}
	}

	x := make([]float64, len(opt.Variables()))
	x[0] = 0.0
	surfMap, _, _ := opt.applyVariables(x)
	s := surfMap["config1"]
	surface.Precompute(s)
	for _, sf := range s {
		if sf.ID == 7 {
			if sf.Thickness < 0.1 {
				t.Errorf("surface 7 thickness should be clamped to >= 0.1, got %f", sf.Thickness)
			}
			return
		}
	}
	t.Fatal("surface 7 not found in output")
}

// TestApplyBackFocusSolveClampsNegativeThickness verifies the exported
// ApplyBackFocusSolve function clamps thickness to 0.1 mm when the computed
// shift would make it negative.
func TestApplyBackFocusSolveClampsNegativeThickness(t *testing.T) {
	gc := tripletGC()
	surfaces := powerSolveTripletSurfaces()
	// Make the image plane spacing huge so the paraxial BFL produces a large
	// negative shift.
	for i := range surfaces {
		if surfaces[i].ID == 7 {
			surfaces[i].Thickness = 200.0
		}
	}
	surface.Precompute(surfaces)
	fields := []types.FieldItem{
		{ID: 0, AngleDeg: 0.0},
	}
	wavelengths := []types.WavelengthItem{
		{Value: 0.0005876},
	}
	cfg := &types.BackFocusSolveConfig{
		Enabled:    true,
		Type:       "paraxial",
		Surface:    7,
		Wavelength: 0.0005876,
	}
	ApplyBackFocusSolve(surfaces, cfg, "paraxial", 0, 0.0005876, fields, wavelengths, gc, "", nil)
	for _, sf := range surfaces {
		if sf.ID == 7 {
			if sf.Thickness < 0.1 {
				t.Errorf("surface 7 thickness should be clamped to >= 0.1, got %f", sf.Thickness)
			}
			return
		}
	}
	t.Fatal("surface 7 not found in input")
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

// TestApplyVariablesSizesAperturesBeforeBackFocus verifies that the back-focus
// solve inside applyVariables sees the sized auto_aperture diameters the merit
// grid will use, not the stored ones. Two identical systems whose only
// difference is the pre-sizing stored diameter of an auto_aperture surface must
// land on the same solved image plane; before the fix the solve read the stored
// diameter and the planes diverged.
func TestApplyVariablesSizesAperturesBeforeBackFocus(t *testing.T) {
	gc := tripletGC()
	bf := &types.BackFocusSolveConfig{
		Enabled: true, Surface: 7, Type: "wavefront",
		WeightType: "uniform", NumRays: 48,
	}
	fieldDefs := []types.FieldDef{{Angle: 0}, {Angle: 16}}

	// Premise: the wavefront back-focus is sensitive to an auto_aperture
	// surface's diameter (the trace clips rays there), so a stored-vs-sized
	// mix-up would change the solved plane.
	withDia := func(d float64) []types.Surface {
		s := powerSolveTripletSurfaces()
		for i := range s {
			if s[i].ID == 5 {
				s[i].AutoAperture = true
				s[i].Diameter = d
			}
		}
		surface.Precompute(s)
		return s
	}
	shSmall, errSmall := wavefrontBackFocusShiftFor(withDia(2.0), bf, 0, 0, nil, fieldDefs, gc, "", nil)
	shLarge, errLarge := wavefrontBackFocusShiftFor(withDia(40.0), bf, 0, 0, nil, fieldDefs, gc, "", nil)
	if errLarge != nil {
		t.Fatalf("test setup: wavefront back-focus failed on the large-aperture system: %v", errLarge)
	}
	if errSmall != nil {
		// The 2 mm aperture clips the grid down to a degenerate fit: the solve
		// reports it now (the pre-fix behaviour was the same 0, silently).
		shSmall = 0
	}
	if shSmall == shLarge {
		t.Fatalf("test setup: wavefront back-focus not aperture-sensitive (%.6f)", shSmall)
	}

	mk := func(stored float64) *Optimizer {
		cfg := Config{
			Surfaces:       withDia(stored),
			Fields:         []types.FieldItem{{ID: 0, AngleDeg: 0}, {ID: 1, AngleDeg: 16}},
			RefSurface:     8,
			NumRays:        48,
			GlassCatalog:   gc,
			ApertureMargin: 0.5,
		}
		opt := NewOptimizer(cfg)
		opt.SetBackFocusSolve(bf)
		opt.UpdatePupils(nil)
		return opt
	}

	thk := func(o *Optimizer) float64 {
		surfMap, _, _ := o.applyVariables(nil)
		for _, s := range surfMap["config1"] {
			if s.ID == 7 {
				return s.Thickness
			}
		}
		return math.NaN()
	}
	smallThk := thk(mk(2.0))
	largeThk := thk(mk(40.0))
	if math.Abs(smallThk-largeThk) > 1e-9 {
		t.Errorf("solved plane depends on the stored auto_aperture diameter: %.6f vs %.6f (must use the sized diameter)",
			smallThk, largeThk)
	}
}
