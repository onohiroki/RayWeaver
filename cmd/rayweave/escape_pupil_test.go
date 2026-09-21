package main

import (
	"testing"

	"github.com/hiroki/rayweaver/internal/optimize"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// stubPupilSource implements the FinalPupilModels capability the escape
// validation relies on, so effectivePupilModel can be tested without a full
// optimizer.
type stubPupilSource struct {
	models map[string]types.PupilModelConfig
}

func (s stubPupilSource) FinalPupilModels([]float64) map[string]types.PupilModelConfig {
	return s.models
}

// TestEffectivePupilModel verifies the escape feasibility grid picks up the
// optimised virtual-pupil variables (axial_position/diameter) instead of the
// static input model, and falls back safely otherwise.
func TestEffectivePupilModel(t *testing.T) {
	base := &types.PupilModelConfig{
		Mode:          "virtual_entrance_pupil",
		AxialPosition: 12.5,
		Diameter:      12.5,
	}
	stub := stubPupilSource{models: map[string]types.PupilModelConfig{
		"config1": {Mode: "virtual_entrance_pupil", AxialPosition: 40.0, Diameter: 20.0},
	}}

	got := effectivePupilModel(stub, nil, "config1", base)
	if got == base {
		t.Fatal("expected a copy with the optimised pupil applied, got the base pointer")
	}
	if got.AxialPosition != 40.0 || got.Diameter != 20.0 {
		t.Errorf("optimised pupil = z=%v d=%v, want 40/20", got.AxialPosition, got.Diameter)
	}
	// The base must not be mutated.
	if base.AxialPosition != 12.5 || base.Diameter != 12.5 {
		t.Errorf("base model was mutated: z=%v d=%v", base.AxialPosition, base.Diameter)
	}

	// Unknown config -> fall back to the static model.
	if got := effectivePupilModel(stub, nil, "missing", base); got != base {
		t.Errorf("unknown config should fall back to the static model, got %+v", got)
	}

	// A model that does not expose FinalPupilModels -> fall back.
	if got := effectivePupilModel(struct{}{}, nil, "config1", base); got != base {
		t.Error("non-pupil model should fall back to the static model")
	}

	// No virtual pupil configured -> nil (the dynamic-pupil path handles it).
	if got := effectivePupilModel(stub, nil, "config1", nil); got != nil {
		t.Errorf("nil base should stay nil, got %+v", got)
	}
}

// TestEffectivePupilModelWithOptimizer checks the integration point: a real
// optimizer exposed through the dls.Model interface yields the optimised pupil
// for the point x, matching what the DLS merit grid used.
func TestEffectivePupilModelWithOptimizer(t *testing.T) {
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 10.0, Material: types.Material{ND: 1.5168, VD: 64.17}, Diameter: 50.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 100.0, Material: types.Material{}, Diameter: 50.0},
	}
	surface.Precompute(surfaces)

	model := &types.PupilModelConfig{Mode: "virtual_entrance_pupil", AxialPosition: -2.0, Diameter: 8.0}
	opt := optimize.NewOptimizer(optimize.Config{
		Surfaces: surfaces,
		Variables: []optimize.Variable{
			{Name: "vpz", SurfaceID: 0, Param: "pupil_model_axial_position", Min: -5, Max: 5},
			{Name: "vpd", SurfaceID: 0, Param: "pupil_model_diameter", Min: 4, Max: 20},
		},
		PupilModel: model,
		NumRays:    16,
	})

	got := effectivePupilModel(opt, []float64{3.5, 15.0}, "config1", model)
	if got == model {
		t.Fatal("expected a copy with the optimised pupil applied, got the base pointer")
	}
	if got.AxialPosition != 3.5 || got.Diameter != 15.0 {
		t.Errorf("optimised pupil = z=%v d=%v, want 3.5/15", got.AxialPosition, got.Diameter)
	}
}
