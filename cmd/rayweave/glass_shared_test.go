package main

import (
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/types"
	"gopkg.in/yaml.v3"
)

const sharedGlassMultiYAML = `
metadata:
  tool: {name: RayWeaver, schema_version: 1}
glass_catalog:
  entries:
  - {type: model, label: BK7, nd: 1.51680, vd: 64.1673}
  - {type: model, label: F2, nd: 1.62004, vd: 36.37}
chief:
  reference_surface: 3
  fields: [{angle: 0.0}]
optimization:
  method: dls
  max_iter: 1
  num_rays: 8
configs:
- id: config0
  active: true
  fields: [{id: 0, angle_deg: 0.0, weight: 1.0}]
  wavelengths: [{id: 0, value: 0.00058756, weight: 1.0}]
  surfaces:
  - {id: 1, type: sphere, radius: 60.0, thickness: 4.0, material: {key: BK7}, diameter: 20.0}
  - {id: 2, type: sphere, radius: -60.0, thickness: 50.0, material: AIR, diameter: 20.0}
  - {id: 3, type: sphere, radius: 0.0, thickness: 0.0, material: AIR, diameter: 20.0}
  merit:
    type: spot_rms
    terms: [{kind: spot_rms, field: 0, wavelength: 0.00058756, weight: 1.0}]
- id: config1
  active: true
  fields: [{id: 0, angle_deg: 0.0, weight: 1.0}]
  wavelengths: [{id: 0, value: 0.00058756, weight: 1.0}]
  surfaces:
  - {id: 1, type: sphere, radius: 70.0, thickness: 4.0, material: {key: BK7}, diameter: 20.0}
  - {id: 2, type: sphere, radius: -70.0, thickness: 50.0, material: AIR, diameter: 20.0}
  - {id: 3, type: sphere, radius: 0.0, thickness: 0.0, material: AIR, diameter: 20.0}
  merit:
    type: spot_rms
    terms: [{kind: spot_rms, field: 0, wavelength: 0.00058756, weight: 1.0}]
`

func parseSharedGlassInput(t *testing.T) *types.Input {
	t.Helper()
	var in types.Input
	if err := yaml.Unmarshal([]byte(sharedGlassMultiYAML), &in); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	return &in
}

func sharedGlassCatalog() *glass.Catalog {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "BK7", ND: 1.5168, VD: 64.1673})
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "F2", ND: 1.62004, VD: 36.37})
	return gc
}

// TestGlassVariablesSharedInMultiConfig verifies --glass-variables emits one
// shared nd/vd pair per element (bound across every active config) rather than
// per-config local variables.
func TestGlassVariablesSharedInMultiConfig(t *testing.T) {
	in := parseSharedGlassInput(t)
	applyGlassVariables(in, sharedGlassCatalog())

	if len(in.Optimization.LocalVariables) != 0 {
		t.Errorf("multi-config glass variables should be shared, got %d local", len(in.Optimization.LocalVariables))
	}
	byName := map[string]types.SharedVariable{}
	for _, sv := range in.Optimization.SharedVariables {
		byName[sv.Name] = sv
	}
	nd, ok := byName["s1_nd"]
	if !ok {
		t.Fatalf("no shared s1_nd variable, got %v", byName)
	}
	if len(nd.Bindings) != 2 {
		t.Fatalf("s1_nd bindings = %d, want 2 (one per config)", len(nd.Bindings))
	}
	for _, b := range nd.Bindings {
		if b.Param != "nd" {
			t.Errorf("binding param = %q, want nd", b.Param)
		}
	}
	if _, ok := byName["s1_vd"]; !ok {
		t.Errorf("no shared s1_vd variable")
	}
}

// TestValidateSharedGlassVariables verifies the startup guards: a shared
// glass variable is accepted when the bindings agree, and rejected when they
// mix nd/vd or start from different glasses.
func TestValidateSharedGlassVariables(t *testing.T) {
	gc := sharedGlassCatalog()

	sharedVar := func(param0, param1 string) []types.SharedVariable {
		return []types.SharedVariable{{
			Name: "s1_nd", Min: 1.4, Max: 2.0, Active: true,
			Bindings: []types.SharedVariableBinding{
				{Config: "config0", ID: 1, Param: param0},
				{Config: "config1", ID: 1, Param: param1},
			},
		}}
	}

	// Same glass, same param -> accepted.
	in := parseSharedGlassInput(t)
	in.Optimization.SharedVariables = sharedVar("nd", "nd")
	if err := validateSharedGlassVariables(in, gc); err != nil {
		t.Errorf("agreeing shared glass rejected: %v", err)
	}

	// Different initial glass -> rejected.
	in = parseSharedGlassInput(t)
	in.Optimization.SharedVariables = sharedVar("nd", "nd")
	in.Configs[1].Surfaces[0].Material = types.Material{Key: "F2"}
	if err := validateSharedGlassVariables(in, gc); err == nil {
		t.Errorf("shared glass over differing glasses was accepted")
	}

	// Mixed nd/vd -> rejected.
	in = parseSharedGlassInput(t)
	in.Optimization.SharedVariables = sharedVar("nd", "vd")
	if err := validateSharedGlassVariables(in, gc); err == nil {
		t.Errorf("shared glass mixing nd and vd was accepted")
	}
}
