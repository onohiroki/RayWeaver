package main

import (
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
)

// stubFinalState implements the FinalConfigs/FinalApertures capabilities the
// escape feasibility check relies on, with hand-set values so the geometry
// mirroring can be asserted without tracing rays.
type stubFinalState struct {
	geo map[string][]types.Surface
	ap  map[string]map[int]float64
}

func (s stubFinalState) FinalConfigs([]float64) (map[string][]types.Surface, []types.Glass) {
	return s.geo, nil
}

func (s stubFinalState) FinalApertures([]float64) map[string]map[int]float64 {
	return s.ap
}

// TestValidationSurfaces pins the escape feasibility check to the prescription
// the optimizer evaluated: the sized auto apertures (not the stale stored
// diameters, which would clip the beam and fail the throughput check) together
// with the hard-solve geometry from FinalConfigs, and the variable-only
// projection only as a fallback for models that do not expose the hooks.
func TestValidationSurfaces(t *testing.T) {
	// What the optimizer would report: surface 1 is an auto aperture the tool
	// sizes up, surface 2 carries the back-focus-solved thickness.
	final := []types.Surface{
		{ID: 1, AutoAperture: true, Diameter: 4.0, Thickness: 10.0},
		{ID: 2, Diameter: 50.0, Thickness: 17.88},
	}
	stub := stubFinalState{
		geo: map[string][]types.Surface{"config1": final},
		ap: map[string]map[int]float64{
			"config1": {1: 44.44, 2: 50.0},
		},
	}
	raw := []types.Surface{
		{ID: 1, AutoAperture: true, Diameter: 4.0, Thickness: 42.0},
		{ID: 2, Diameter: 50.0, Thickness: 42.0},
	}
	fallbackCalls := 0
	fallback := func() []types.Surface {
		fallbackCalls++
		return raw
	}

	got := validationSurfaces(stub, nil, "config1", fallback)
	if fallbackCalls != 0 {
		t.Fatalf("fallback called %d times, want 0 (optimizer exposes the final-state hooks)", fallbackCalls)
	}
	if len(got) != 2 {
		t.Fatalf("got %d surfaces, want 2", len(got))
	}
	if got[0].Diameter != 44.44 {
		t.Errorf("auto aperture diameter = %v, want the sized 44.44 (stored 4.0 would clip)", got[0].Diameter)
	}
	if got[1].Diameter != 50.0 {
		t.Errorf("fixed surface diameter = %v, want 50.0 (only auto apertures are resized)", got[1].Diameter)
	}
	if got[1].Thickness != 17.88 {
		t.Errorf("back-focus surface thickness = %v, want the hard-solved 17.88", got[1].Thickness)
	}
	// The optimizer's own surfaces must stay untouched.
	if final[0].Diameter != 4.0 || final[1].Thickness != 17.88 {
		t.Errorf("FinalConfigs surfaces were mutated: %+v", final)
	}

	// Unknown config -> fall back to the variable-only projection.
	fallbackCalls = 0
	if got := validationSurfaces(stub, nil, "other", fallback); fallbackCalls != 1 || len(got) != 2 {
		t.Errorf("unknown config: fallback calls = %d, surfaces = %d; want 1 and 2", fallbackCalls, len(got))
	}

	// A model without the hooks (stub dls.Model in tests) -> fall back.
	fallbackCalls = 0
	if got := validationSurfaces(struct{}{}, nil, "config1", fallback); fallbackCalls != 1 || got[0].Thickness != 42.0 {
		t.Errorf("hook-less model: fallback calls = %d, thickness = %v; want 1 and 42.0", fallbackCalls, got[0].Thickness)
	}
	if got := validationSurfaces(nil, nil, "config1", fallback); fallbackCalls != 2 || len(got) != 2 {
		t.Errorf("nil model: fallback calls = %d, surfaces = %d; want 2 and 2", fallbackCalls, len(got))
	}
}
