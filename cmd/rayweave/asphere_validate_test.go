package main

import (
	"os"
	"testing"

	"github.com/hiroki/rayweaver/internal/asphere"
	"github.com/hiroki/rayweaver/internal/types"
	"gopkg.in/yaml.v3"
)

// TestAsphereValidateReportsImprovement runs the asphere analysis with
// --validate semantics on the degraded triplet and checks that the top-K
// fitted surfaces get a finite validation block with a sensible merit
// improvement direction.
func TestAsphereValidateReportsImprovement(t *testing.T) {
	data, err := os.ReadFile("../../samples/us2645157-degraded.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var input types.Input
	if err := yaml.Unmarshal(data, &input); err != nil {
		t.Fatal(err)
	}
	gc, _ := loadCatalogs(&input, "")
	cfgFlag := ""
	surfaces := configSurfaces(input.Configs, &cfgFlag)
	fields, err := resolveAsphereFields(input, &cfgFlag)
	if err != nil {
		t.Fatal(err)
	}
	wavelengths := resolveAsphereWavelengths(input, &cfgFlag)

	cfg := asphere.DefaultConfig()
	cfg.TopK = 2
	res := asphere.Run(surfaces, fields, wavelengths, cfg, gc, input.Chief.StopSurface, input.Chief.ReferenceSurface)

	validateFields := asphereFieldsToItems(fields)
	validations := validateAspheres(surfaces, res.Rankings, gc, cfg.TopK, 10, 32,
		input.Chief.StopSurface, input.Chief.ReferenceSurface, 0, validateFields, wavelengths,
		types.NewCircularJones(true), types.GridPolar, nil)

	validated := 0
	for _, r := range res.Rankings {
		v, ok := validations[r.SurfaceID]
		if !ok {
			continue
		}
		validated++
		if v.BeforeMerit <= 0 || v.BeforeMerit > 1e6 {
			t.Fatalf("surface %d: before merit %v not finite", r.SurfaceID, v.BeforeMerit)
		}
		if v.AfterMerit <= 0 || v.AfterMerit > v.BeforeMerit {
			t.Fatalf("surface %d: after merit %v not an improvement over before %v", r.SurfaceID, v.AfterMerit, v.BeforeMerit)
		}
		if v.Improvement <= 0 {
			t.Fatalf("surface %d: improvement %v not positive", r.SurfaceID, v.Improvement)
		}
	}
	if validated == 0 {
		t.Fatal("no surface produced a validation block")
	}
}
