package optimize

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

func rayValidityProbe(c1, diameter2 float64) *Optimizer {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: c1, Thickness: 20.0, Material: types.Material{Key: "N-BK7"}, Diameter: 40.0, Role: "stop"},
		{ID: 2, Type: types.Sphere, Curvature: 0, Thickness: 40.0, Material: types.Material{}, Diameter: diameter2},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 60.0},
	}
	surface.Precompute(surfaces)
	return NewOptimizer(Config{
		Surfaces:     surfaces,
		GlassCatalog: gc,
		Variables: []Variable{
			{SurfaceID: 1, Param: "curvature", Min: 0.005, Max: 0.05},
		},
		Fields:      []types.FieldItem{{ID: 0, AngleDeg: 0, Weight: 1}},
		StopSurface: 1,
		RefSurface:  3,
		NumRays:     16,
		Mu:          1e-3,
		Tol:         1e-9,
		Epsilon:     1e-9,
	})
}

// TestRayValidityMetric pins the Layer R state signal: 0 while a clear aperture
// clips the beam, 1 when every aperture has the full clearance scale, and a
// monotone rise as the first surface strengthens.
func TestClearanceValidity(t *testing.T) {
	cases := []struct {
		margin, target, want float64
	}{
		{0.3, 0.2, 1},
		{0.1, 0.2, 0.5},
		{-1, 0.2, 0},
		{0.01, 0, 0.1}, // target floor: 0.01 / 0.1
		{0.05, 0, 0.5},
	}
	for _, c := range cases {
		if got := clearanceValidity(c.margin, c.target); math.Abs(got-c.want) > 1e-12 {
			t.Errorf("clearanceValidity(%v, %v) = %v, want %v", c.margin, c.target, got, c.want)
		}
	}
}

func TestClearanceTargets(t *testing.T) {
	cfg := &config{
		meritTerms: []meritTerm{{kind: MeritBeamClearance, surfaceSet: []int{5}, target: 0.2, fieldIndex: 2}},
		meritModes: map[string][]meritTerm{
			"feasibility": {{kind: MeritBeamClearance, surfaceSet: []int{2}, target: 0.4, fieldIndex: 3}},
		},
	}
	got := clearanceTargets(cfg)
	if len(got) != 2 {
		t.Fatalf("clearanceTargets = %v, want 2 entries", got)
	}
	want := map[[2]int]float64{{2, 5}: 0.2, {3, 2}: 0.4}
	for _, c := range got {
		key := [2]int{c.field, c.surface}
		if v, ok := want[key]; !ok || v != c.target {
			t.Errorf("clearanceTargets entry = %+v, want target %v", c, want[key])
		}
	}
}

// TestRayValidityPhysicalGate verifies that the relaxed envelope cannot lift
// the metric above the delivered-pupil fraction: a field whose rays miss a
// small sphere (the virtual extension keeps the relaxed clearance positive)
// must still read as invalid.
func TestRayValidityPhysicalGate(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0, Thickness: 10.0, Material: types.Material{Key: "N-BK7"}, Diameter: 40.0, Role: "stop"},
		{ID: 2, Type: types.Sphere, Curvature: 0.2, Thickness: 10.0, Material: types.Material{}, Diameter: 60.0},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 60.0},
	}
	surface.Precompute(surfaces)
	opt := NewOptimizer(Config{
		Surfaces:     surfaces,
		GlassCatalog: gc,
		Fields:       []types.FieldItem{{ID: 0, AngleDeg: 0, Weight: 1}},
		StopSurface:  1,
		RefSurface:   3,
		NumRays:      16,
	})
	if v := opt.rayValidity(opt.getInitialState()); v > 0.5 {
		t.Errorf("rayValidity = %v, want the physical survival gate (<0.5)", v)
	}
}

// TestMeritModeFieldUsesIndex pins the documented meaning of `field` in a
// merit mode: the field index, not the field's id. An imported document used
// to give every field id 0, which sent every mode term with field > 0 back to
// field 0; a non-sequential id set must still resolve by index.
func TestMeritModeFieldUsesIndex(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.02, Thickness: 5.0, Material: types.Material{Key: "N-BK7"}, Diameter: 40.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.02, Thickness: 40.0, Material: types.Material{}, Diameter: 40.0},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 40.0},
	}
	surface.Precompute(surfaces)
	opt := NewMultiOptimizer([]ConfigInput{{
		ID:     "cfg1",
		Weight: 1,
		Fields: []types.FieldItem{
			{ID: 10, AngleDeg: 0, Weight: 1},
			{ID: 20, AngleDeg: 5, Weight: 1},
		},
		Wavelengths: []types.WavelengthItem{{ID: 0, Value: 0.00058756, Weight: 1}},
		Surfaces:    surfaces,
		MeritModes: []types.MeritMode{{Name: "m", Terms: []types.MeritTerm{
			{Kind: "field_alive", Field: 1, Target: 0.9, Weight: 1},
		}}},
	}}, nil, nil, nil, gc, 1, 0.01, 1e-6, 1e-6, 1.0, 8, 100, 0, nil, nil, 0, 0, false, false, nil, nil)

	terms := opt.configs[0].meritModes["m"]
	if len(terms) != 1 {
		t.Fatalf("mode terms = %d, want 1", len(terms))
	}
	if terms[0].fieldIndex != 1 || terms[0].fieldAngle != 5 {
		t.Errorf("mode term field = index %d angle %v, want index 1 angle 5", terms[0].fieldIndex, terms[0].fieldAngle)
	}
}

// TestRayValiditySkipsConfigsWithoutTerms pins the per-config feasibility
// contract: when one config declares beam_clearance terms, the metric is
// driven by those terms and a config that declares none keeps its own fixed
// merit instead of holding the schedule back through the all-surfaces fallback.
func TestRayValiditySkipsConfigsWithoutTerms(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	healthy := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0, Thickness: 20.0, Material: types.Material{Key: "N-BK7"}, Diameter: 40.0, Role: "stop"},
		{ID: 2, Type: types.Sphere, Curvature: 0, Thickness: 40.0, Material: types.Material{}, Diameter: 60.0},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 100.0},
	}
	broken := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0, Thickness: 20.0, Material: types.Material{Key: "N-BK7"}, Diameter: 40.0, Role: "stop"},
		{ID: 2, Type: types.Sphere, Curvature: 0.2, Thickness: 40.0, Material: types.Material{}, Diameter: 60.0},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 100.0},
	}
	surface.Precompute(healthy)
	surface.Precompute(broken)
	opt := NewMultiOptimizer([]ConfigInput{
		{
			ID: "healthy", Weight: 1, StopSurface: 1, RefSurface: 3,
			Fields:      []types.FieldItem{{ID: 0, AngleDeg: 0, Weight: 1}},
			Wavelengths: []types.WavelengthItem{{ID: 0, Value: 0.00058756, Weight: 1}},
			Surfaces:    healthy,
			MeritTerms: []types.MeritTerm{
				{Kind: MeritBeamClearance, Field: 0, SurfaceSet: []int{2}, Target: 0.2, Weight: 1},
			},
		},
		{
			ID: "broken", Weight: 1, StopSurface: 1, RefSurface: 3,
			Fields:      []types.FieldItem{{ID: 0, AngleDeg: 0, Weight: 1}},
			Wavelengths: []types.WavelengthItem{{ID: 0, Value: 0.00058756, Weight: 1}},
			Surfaces:    broken,
		},
	}, nil, nil, nil, gc, 1, 0.01, 1e-6, 1e-6, 1.0, 32, 100, 0, nil, nil, 0, 0, false, false, nil, nil)

	if v := opt.rayValidity(opt.getInitialState()); v < 0.9 {
		t.Errorf("rayValidity = %v, want the healthy config's clearance (~1); the term-less config must not gate", v)
	}
}

func TestRayValidityMetric(t *testing.T) {
	validity := func(c1, diameter2 float64) float64 {
		opt := rayValidityProbe(c1, diameter2)
		return opt.rayValidity(opt.getInitialState())
	}

	if v := validity(0.01, 60); v < 0.99 {
		t.Errorf("clear aperture validity = %v, want 1", v)
	}
	if v := validity(0.01, 10); v > 0.01 {
		t.Errorf("clipped aperture validity = %v, want 0", v)
	}
	weak := validity(0.02, 34)
	strong := validity(0.05, 34)
	if !(strong > weak) {
		t.Errorf("validity did not rise as the beam narrowed: c=0.02 -> %v, c=0.05 -> %v", weak, strong)
	}
	for _, c := range [][2]float64{{0.01, 10}, {0.01, 60}, {0.05, 34}} {
		if v := validity(c[0], c[1]); v < 0 || v > 1 {
			t.Errorf("validity(%v, %v) = %v, want [0,1]", c[0], c[1], v)
		}
	}
}

// TestRayValiditySchedule drives the paraxial-to-real mode blend: a clipped
// design stays in the analytic mode, a clear one selects the real mode.
func TestRayValiditySchedule(t *testing.T) {
	schedule := func(c1, diameter2 float64) (string, map[string]float64, float64) {
		opt := rayValidityProbe(c1, diameter2)
		opt.SetMeritSchedule(&types.MeritScheduleConfig{
			Metric:     "ray_validity",
			Curve:      "linear",
			AnchorFrom: 0.1,
			AnchorTo:   0.9,
			Modes: []types.MeritScheduleMode{
				{Name: "analytic", WeightFrom: 1, WeightTo: 0},
				{Name: "real", WeightFrom: 0, WeightTo: 1},
			},
		})
		active, weights, _, metric, _ := opt.MeritScheduleState()
		return active, weights, metric
	}

	active, weights, metric := schedule(0.01, 10)
	if active != "analytic" || weights["real"] > 1e-9 {
		t.Errorf("clipped design: active=%q weights=%v metric=%v, want analytic with real=0", active, weights, metric)
	}
	active, weights, metric = schedule(0.05, 34)
	if active != "real" || weights["analytic"] > 1e-9 {
		t.Errorf("clear design: active=%q weights=%v metric=%v, want real with analytic=0", active, weights, metric)
	}
}
