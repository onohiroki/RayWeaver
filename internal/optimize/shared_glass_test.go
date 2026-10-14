package optimize

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// twoElementConfig builds a single config with two keyed N-BK7 elements and an
// image plane (ids 1..5).
func twoElementConfig(id string) ConfigInput {
	return ConfigInput{
		ID:     id,
		Weight: 1.0,
		Surfaces: []types.Surface{
			{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 5.0, Material: types.Material{Key: "N-BK7"}, Diameter: 50.0},
			{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 20.0, Material: types.Material{}, Diameter: 50.0},
			{ID: 3, Type: types.Sphere, Curvature: 0.01, Thickness: 5.0, Material: types.Material{Key: "N-BK7"}, Diameter: 50.0},
			{ID: 4, Type: types.Sphere, Curvature: -0.01, Thickness: 60.0, Material: types.Material{}, Diameter: 50.0},
			{ID: 5, Type: types.Sphere, Curvature: 0.0, Thickness: 0.0, Material: types.Material{}, Diameter: 50.0},
		},
		Fields:      []types.FieldItem{{ID: 0, AngleDeg: 0.0, Weight: 1.0}},
		Wavelengths: []types.WavelengthItem{{ID: 0, Value: 0.00058756, Weight: 1.0}},
		MeritTerms:  []types.MeritTerm{{Field: 0, Wavelength: 0.00058756, Weight: 1.0}},
	}
}

func bk7Catalog() *glass.Catalog {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	return gc
}

// TestSharedGlassVariableAppliesAndMaterializes verifies a shared nd/vd variable
// is recognised end to end in multi-config mode: the initial state reads the
// catalogue glass, and the optimised value is written to every bound surface as
// an inline model glass (identical across configs).
func TestSharedGlassVariableAppliesAndMaterializes(t *testing.T) {
	gc := bk7Catalog()
	shared := []types.SharedVariable{
		{Name: "s1_nd", Min: 1.4, Max: 1.9, Active: true, Bindings: []types.SharedVariableBinding{
			{Config: "c0", ID: 1, Param: "nd"}, {Config: "c1", ID: 1, Param: "nd"}}},
		{Name: "s1_vd", Min: 20, Max: 80, Active: true, Bindings: []types.SharedVariableBinding{
			{Config: "c0", ID: 1, Param: "vd"}, {Config: "c1", ID: 1, Param: "vd"}}},
	}
	opt := NewMultiOptimizer(
		[]ConfigInput{twoElementConfig("c0"), twoElementConfig("c1")},
		shared, nil, nil, gc, 1, 0.01, 1e-6, 1e-6, 2.0, 16, 0, 0, nil, nil, 0, 0, false, false, nil, nil)

	xi := opt.getInitialState()
	if len(xi) != 2 || math.Abs(xi[0]-1.5168) > 1e-9 || math.Abs(xi[1]-64.17) > 1e-9 {
		t.Fatalf("initial shared glass = %v, want [1.5168 64.17]", xi)
	}

	surfs, _ := opt.FinalConfigs([]float64{1.62, 40.0})
	for _, id := range []string{"c0", "c1"} {
		m := surfs[id][0].Material
		if m.HasKey() || math.Abs(m.ND-1.62) > 1e-12 || math.Abs(m.VD-40.0) > 1e-12 {
			t.Errorf("config %s surface 1 material = %+v, want inline {1.62 40}", id, m)
		}
	}
}

// TestGlassVariableBindingsOnly verifies a glass variable rewrites only the
// surface it targets — a sibling surface sharing the same catalogue key keeps
// its key.
func TestGlassVariableBindingsOnly(t *testing.T) {
	gc := bk7Catalog()
	ci := twoElementConfig("config1")
	cfg := Config{
		Surfaces: ci.Surfaces,
		Variables: []Variable{
			{Name: "s1_nd", SurfaceID: 1, Param: "nd", Min: 1.4, Max: 1.9},
			{Name: "s1_vd", SurfaceID: 1, Param: "vd", Min: 20, Max: 80},
		},
		MeritTerms:   []MeritTerm{{FieldAngle: 0, FieldWeight: 1, Wavelength: 0.00058756, WavWeight: 1, Weight: 1}},
		GlassCatalog: gc,
		NumRays:      16,
	}
	surface.Precompute(cfg.Surfaces)
	opt := NewOptimizer(cfg)

	surfs, _ := opt.FinalConfigs([]float64{1.60, 50.0})
	m1 := surfs["config1"][0].Material
	if m1.HasKey() || math.Abs(m1.ND-1.60) > 1e-12 {
		t.Errorf("target surface 1 = %+v, want inline nd 1.60", m1)
	}
	m3 := surfs["config1"][2].Material
	if !m3.HasKey() || m3.Key != "N-BK7" {
		t.Errorf("sibling surface 3 = %+v, want untouched key N-BK7", m3)
	}
}
