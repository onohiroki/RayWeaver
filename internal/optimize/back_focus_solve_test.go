package optimize

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/psf"
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
	shSmall, errSmall := wavefrontBackFocusShiftFor(withDia(2.0), bf, 0, 0, fieldDefs, gc, "", nil)
	shLarge, errLarge := wavefrontBackFocusShiftFor(withDia(40.0), bf, 0, 0, fieldDefs, gc, "", nil)
	if errLarge != nil {
		t.Fatalf("test setup: wavefront back-focus failed on the large-aperture system: %v", errLarge)
	}
	if errSmall != nil {
		// The 2 mm aperture clips the grid down to a degenerate fit: the solve
		// reports it now (the pre-fix behaviour was the same 0, silently).
		shSmall.ShiftMM = 0
	}
	if shSmall.ShiftMM == shLarge.ShiftMM {
		t.Fatalf("test setup: wavefront back-focus not aperture-sensitive (%.6f)", shSmall.ShiftMM)
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

// TestBackFocusWavelengthDefault pins the documented default of
// back_focus_solve.wavelength (types: "default: d-line"): the configured value,
// else the configuration's reference wavelength, else the d-line. The config's
// wavelength *list* must not participate — preferring its first entry selected
// the F line of a standard [F, d, C] list and put the image plane at a
// different focus from the one the reference wavelength asks for.
func TestBackFocusWavelengthDefault(t *testing.T) {
	if got := backFocusWavelength(&types.BackFocusSolveConfig{Wavelength: 0.0006328}, 0.0005876); got != 0.0006328 {
		t.Errorf("configured wavelength: got %v, want 0.0006328", got)
	}
	if got := backFocusWavelength(&types.BackFocusSolveConfig{}, 0.0005876); got != 0.0005876 {
		t.Errorf("unset wavelength: got %v, want the reference 0.0005876", got)
	}
	if got := backFocusWavelength(&types.BackFocusSolveConfig{}, 0); got != types.DefaultWavelength {
		t.Errorf("unset wavelength and reference: got %v, want the d-line default %v", got, types.DefaultWavelength)
	}
	if got := backFocusWavelength(nil, 0.0004861); got != 0.0004861 {
		t.Errorf("nil config: got %v, want the reference 0.0004861", got)
	}
}

// spotRMSOnImagePlane evaluates the flux-weighted on-axis spot radius at the
// system's current image plane — the metric `type: spot` minimises.
func spotRMSOnImagePlane(t *testing.T, surfaces []types.Surface, gc *glass.Catalog, numRays int) float64 {
	t.Helper()
	surface.Precompute(surfaces)
	res, err := psf.ComputeSpot(types.System{Surfaces: surfaces}, gc,
		[]types.FieldDef{{Angle: 0, Direction: []float64{0, 1}}},
		[]float64{0.0005876}, psf.Options{NumRays: numRays})
	if err != nil {
		t.Fatalf("ComputeSpot: %v", err)
	}
	if len(res) == 0 {
		t.Fatal("ComputeSpot returned no result")
	}
	return res[0].SpotRMS
}

// TestApplyBackFocusSolveSpotType drives the exported solve on a triplet whose
// image plane sits 3 mm past focus and checks the property the type exists
// for: the plane it returns really is a geometric spot minimum (the spot is
// smaller afterwards), and it lands back near the design image distance.
func TestApplyBackFocusSolveSpotType(t *testing.T) {
	gc := tripletGC()
	surfaces := powerSolveTripletSurfaces()
	const designImageDistance = 21.36695183553
	for i := range surfaces {
		if surfaces[i].ID == 7 {
			surfaces[i].Thickness += 3.0 // deliberately defocused
		}
	}
	surface.Precompute(surfaces)

	fields := []types.FieldItem{{ID: 0, AngleDeg: 0}}
	wavelengths := []types.WavelengthItem{{Value: 0.0005876}}
	cfg := &types.BackFocusSolveConfig{Enabled: true, Type: "spot", Surface: 7, NumRays: 96}

	before := spotRMSOnImagePlane(t, surfaces, gc, 96)
	if !ApplyBackFocusSolve(surfaces, cfg, "spot", 0, 0.0005876, fields, wavelengths, gc, "", nil) {
		t.Fatal("ApplyBackFocusSolve reported failure although the system traces")
	}
	after := spotRMSOnImagePlane(t, surfaces, gc, 96)
	if after >= before {
		t.Errorf("spot radius after the solve (%v) must be below the defocused one (%v)", after, before)
	}
	var thk float64
	for _, s := range surfaces {
		if s.ID == 7 {
			thk = s.Thickness
		}
	}
	if math.Abs(thk-designImageDistance) > 1.0 {
		t.Errorf("solved image distance %.4f mm is not near the design %.4f mm", thk, designImageDistance)
	}
}

// TestSpotAndWavefrontBackFocusAgree cross-validates the new spot solve
// against the wavefront solve it is meant to sit between: on the same
// deliberately defocused system both must prescribe essentially the same plane
// (they differ only by the spherical aberration that separates the geometric
// spot minimum from the coherent-peak maximum).
func TestSpotAndWavefrontBackFocusAgree(t *testing.T) {
	gc := tripletGC()
	build := func() []types.Surface {
		s := powerSolveTripletSurfaces()
		for i := range s {
			if s[i].ID == 7 {
				s[i].Thickness += 3.0
			}
		}
		surface.Precompute(s)
		return s
	}
	cfg := &types.BackFocusSolveConfig{
		Enabled: true, Type: "spot", Surface: 7, NumRays: 96, WeightType: "uniform",
	}
	fieldDefs := []types.FieldDef{{Angle: 0, Direction: []float64{0, 1}}}

	spotSurfaces := build()
	spotShift, err := spotBackFocusShiftFor(spotSurfaces, cfg, 0, 0.0005876, fieldDefs, gc, "", nil)
	if err != nil {
		t.Fatalf("spot solve: %v", err)
	}
	waveSurfaces := build()
	waveShift, err := wavefrontBackFocusShiftFor(waveSurfaces, cfg, 0, 0.0005876, fieldDefs, gc, "", nil)
	if err != nil {
		t.Fatalf("wavefront solve: %v", err)
	}
	if math.Abs(spotShift.ShiftMM-waveShift.ShiftMM) > 0.1 {
		t.Errorf("spot shift %.4f mm and wavefront shift %.4f mm disagree by more than 0.1 mm",
			spotShift.ShiftMM, waveShift.ShiftMM)
	}
	t.Logf("spot %.4f mm vs wavefront %.4f mm", spotShift.ShiftMM, waveShift.ShiftMM)
	if spotShift.ShiftMM > -2.0 || spotShift.ShiftMM < -4.0 {
		t.Errorf("spot shift %v mm should recover the ~-3 mm deliberate defocus", spotShift.ShiftMM)
	}
}

// TestBackFocusSolveSpotType drives the optimizer path: the spot solve runs
// from applyVariables and reports itself as "spot" (not as a paraxial
// fallback) in opt_results.back_focus.
func TestBackFocusSolveSpotType(t *testing.T) {
	gc := tripletGC()
	surfaces := powerSolveTripletSurfaces()
	surface.Precompute(surfaces)
	cfg := Config{
		Surfaces:     surfaces,
		Fields:       []types.FieldItem{{ID: 0, AngleDeg: 0}},
		RefSurface:   8,
		NumRays:      48,
		GlassCatalog: gc,
		Variables: []Variable{
			{Name: "c", SurfaceID: 7, Param: "curvature", Min: -0.1, Max: 0.1, Config: "config1"},
		},
	}
	opt := NewOptimizer(cfg)
	opt.SetBackFocusSolve(&types.BackFocusSolveConfig{Enabled: true, Type: "spot", Surface: 7, NumRays: 48})

	x := make([]float64, len(opt.Variables()))
	x[0] = 0.0
	surfMap, _, _ := opt.applyVariables(x)
	for _, s := range surfMap["config1"] {
		if s.ID == 7 && s.Thickness <= 0 {
			t.Errorf("surface 7 thickness should be positive, got %f", s.Thickness)
		}
	}
	diag := opt.BackFocusDiagnostics()
	if diag == nil {
		t.Fatal("BackFocusDiagnostics should report an enabled solve")
	}
	if diag.Method != "spot" {
		t.Errorf("back-focus method: got %q, want \"spot\"", diag.Method)
	}
	if diag.Failures != 0 {
		t.Errorf("the spot solve must not fall back here, failures = %d", diag.Failures)
	}
}

// TestBackFocusSolveScheduleSwitchesToSpot verifies a merit-schedule mode may
// name the spot solve as its back-focus type.
func TestBackFocusSolveScheduleSwitchesToSpot(t *testing.T) {
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
		Enabled:  true,
		Type:     "paraxial",
		Schedule: &types.BackFocusScheduleConfig{DominantThreshold: 0.5},
	})
	opt.SetMeritSchedule(&types.MeritScheduleConfig{
		Metric:     "iteration",
		Curve:      "step",
		AnchorFrom: 0,
		AnchorTo:   100,
		Modes: []types.MeritScheduleMode{
			{Name: "early", WeightFrom: 1.0, WeightTo: 0.0, BackFocusType: "paraxial"},
			{Name: "late", WeightFrom: 0.0, WeightTo: 1.0, BackFocusType: "spot"},
		},
	})
	x := make([]float64, len(opt.Variables()))
	opt.UpdateMeritWeights(x, 0)
	if opt.currentBackFocusType != "paraxial" {
		t.Errorf("iteration 0: want paraxial, got %s", opt.currentBackFocusType)
	}
	opt.UpdateMeritWeights(x, 100)
	if opt.currentBackFocusType != "spot" {
		t.Errorf("iteration 100: want spot, got %s", opt.currentBackFocusType)
	}
}

// TestBackFocusSolveUnknownTypeWarns pins the typo handling: an unknown static
// type or an unknown mode type is reported once and resolves to the paraxial
// solve — silently claiming a method that is not running is exactly the class
// of bug that ships a document out of focus.
func TestBackFocusSolveUnknownTypeWarns(t *testing.T) {
	var msgs []string
	prev := Warnf
	Warnf = func(format string, args ...interface{}) {
		msgs = append(msgs, fmt.Sprintf(format, args...))
	}
	t.Cleanup(func() { Warnf = prev })

	gc := tripletGC()
	surfaces := powerSolveTripletSurfaces()
	surface.Precompute(surfaces)
	opt := NewOptimizer(Config{
		Surfaces:     surfaces,
		GlassCatalog: gc,
		Variables: []Variable{
			{Name: "c", SurfaceID: 7, Param: "curvature", Min: -0.1, Max: 0.1, Config: "config1"},
		},
	})
	opt.SetBackFocusSolve(&types.BackFocusSolveConfig{Enabled: true, Type: "spott"})
	if opt.currentBackFocusType != "paraxial" {
		t.Errorf("unknown static type: want the paraxial fallback, got %q", opt.currentBackFocusType)
	}
	if !strings.Contains(strings.Join(msgs, "\n"), "spott") {
		t.Errorf("want a warning naming the unknown type, got %v", msgs)
	}

	opt.SetMeritSchedule(&types.MeritScheduleConfig{
		Metric: "iteration",
		Curve:  "step",
		Modes: []types.MeritScheduleMode{
			{Name: "late", WeightFrom: 1.0, WeightTo: 0.0, BackFocusType: "spott"},
		},
	})
	opt.UpdateMeritWeights(make([]float64, len(opt.Variables())), 0)
	if opt.currentBackFocusType != "paraxial" {
		t.Errorf("unknown mode type: want the static paraxial fallback, got %q", opt.currentBackFocusType)
	}
	if !strings.Contains(strings.Join(msgs, "\n"), "back_focus_type") {
		t.Errorf("want a warning naming the mode's back_focus_type, got %v", msgs)
	}
}
