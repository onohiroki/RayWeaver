package main

import (
	"reflect"
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
)

// TestBuildMeritTermsKeepsSurfaceSet guards the single-config escape merit
// path: buildMeritTerms must copy surface_set onto the optimizer MeritTerm,
// otherwise a glass_role term would always evaluate to 0 (it reads
// surface_set[0]).
func TestBuildMeritTermsKeepsSurfaceSet(t *testing.T) {
	input := types.Input{
		Configs: []types.Config{{
			ID:     "c0",
			Fields: []types.FieldItem{{ID: 0, AngleDeg: 0.0, Weight: 1.0}},
			Wavelengths: []types.WavelengthItem{{
				ID: 0, Value: 0.0005876, Weight: 1.0,
			}},
			Merit: &types.MeritFunction{
				Type: "weighted_sum",
				Terms: []types.MeritTerm{
					{Kind: "glass_role", Field: 0, Wavelength: 0.0005876, SurfaceSet: []int{3}, Weight: 1.0},
				},
			},
		}},
	}

	terms := buildMeritTerms(input)
	if len(terms) != 1 {
		t.Fatalf("expected 1 term, got %d", len(terms))
	}
	if terms[0].Kind != "glass_role" {
		t.Fatalf("kind = %q, want glass_role", terms[0].Kind)
	}
	if !reflect.DeepEqual(terms[0].SurfaceSet, []int{3}) {
		t.Errorf("SurfaceSet = %v, want [3] (glass_role needs surface_set[0])", terms[0].SurfaceSet)
	}
}
