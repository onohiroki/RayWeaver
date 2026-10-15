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

// TestResolveConfigFieldDefs pins the field-resolution order (G2). A zoom
// config carries its own fields (constant image height at that zoom position);
// analysing it with the top-level chief field list answers a different
// question, so the config's own fields win, then configs[0], then the chief
// section.
func TestResolveConfigFieldDefs(t *testing.T) {
	input := types.Input{
		Chief: &types.ChiefInput{
			Fields: []types.FieldDef{{Angle: 0}, {Angle: 8}},
		},
		Configs: []types.Config{
			{ID: "wide", Fields: []types.FieldItem{{ID: 0, ImageHeight: 4.0, Weight: 1}}},
			{ID: "tele", Fields: []types.FieldItem{
				{ID: 0, ImageHeight: 9.0, Weight: 1},
				{ID: 1, AngleDeg: 3.0, Weight: 1},
			}},
		},
	}
	flag := func(s string) *string { return &s }

	got := resolveConfigFieldDefs(input, flag("tele"))
	if len(got) != 2 || got[0].ImageHeight != 9.0 || got[1].Angle != 3.0 {
		t.Errorf("--config tele = %+v, want tele's two fields", got)
	}
	if got := resolveConfigFieldDefs(input, flag("0")); len(got) != 1 || got[0].ImageHeight != 4.0 {
		t.Errorf("--config 0 = %+v, want the wide config's field", got)
	}
	if got := resolveConfigFieldDefs(input, flag("")); len(got) != 1 || got[0].ImageHeight != 4.0 {
		t.Errorf("no --config = %+v, want configs[0]'s field", got)
	}

	// A document whose configs declare no fields keeps using the chief section.
	plain := types.Input{Chief: input.Chief, Configs: []types.Config{{ID: "c0"}}}
	if got := resolveConfigFieldDefs(plain, flag("")); len(got) != 2 || got[0].Angle != 0 || got[1].Angle != 8 {
		t.Errorf("chief fallback = %+v, want the chief section's two angles", got)
	}
	angles := types.Input{Chief: &types.ChiefInput{FieldAngles: []float64{0, 5}}}
	if got := resolveConfigFieldDefs(angles, flag("")); len(got) != 2 || got[1].Angle != 5 {
		t.Errorf("field_angles fallback = %+v, want [0 5]", got)
	}
}
