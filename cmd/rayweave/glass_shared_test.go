package main

import (
	"strings"
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

// glassUnionMultiYAML gives config1 an extra F2 element on surface 3 where
// config0 has only its image plane: the configurations do not share a surface
// layout, so an element list taken from the first config alone would silently
// leave surface 3 out of the optimisation.
const glassUnionMultiYAML = `
glass_catalog:
  entries:
  - {type: model, label: BK7, nd: 1.51680, vd: 64.1673}
  - {type: model, label: F2, nd: 1.62004, vd: 36.37}
optimization:
  method: dls
  max_iter: 1
  num_rays: 8
configs:
- id: config0
  active: true
  surfaces:
  - {id: 1, type: sphere, radius: 60.0, thickness: 4.0, material: {key: BK7}, diameter: 20.0}
  - {id: 2, type: sphere, radius: -60.0, thickness: 50.0, material: AIR, diameter: 20.0}
  - {id: 3, type: sphere, radius: 0.0, thickness: 0.0, material: AIR, diameter: 20.0}
- id: config1
  active: true
  surfaces:
  - {id: 1, type: sphere, radius: 70.0, thickness: 4.0, material: {key: BK7}, diameter: 20.0}
  - {id: 2, type: sphere, radius: -70.0, thickness: 20.0, material: AIR, diameter: 20.0}
  - {id: 3, type: sphere, radius: -40.0, thickness: 3.0, material: {key: F2}, diameter: 20.0}
  - {id: 4, type: sphere, radius: 0.0, thickness: 0.0, material: AIR, diameter: 20.0}
`

// TestGlassVariablesUnionAcrossConfigs verifies --glass-variables collects the
// element list from every active config, not just the first: a config that
// numbers its surfaces differently (or carries an extra element) used to have
// that element left out of the optimisation without a word. An element that is
// not glass in some config binds only the configs where it exists, and the
// partial binding is reported.
func TestGlassVariablesUnionAcrossConfigs(t *testing.T) {
	var in types.Input
	if err := yaml.Unmarshal([]byte(glassUnionMultiYAML), &in); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}

	var stderr string
	stderr = captureStderr(t, func() { applyGlassVariables(&in, sharedGlassCatalog()) })

	byName := map[string]types.SharedVariable{}
	for _, sv := range in.Optimization.SharedVariables {
		byName[sv.Name] = sv
	}

	// Surface 3 is an F2 element in config1 only: the union must find it.
	s3, ok := byName["s3_nd"]
	if !ok {
		t.Fatalf("config1's extra element (surface 3) got no shared variable; got %v", byName)
	}
	if len(s3.Bindings) != 1 || s3.Bindings[0].Config != "config1" || s3.Bindings[0].ID != 3 {
		t.Errorf("s3_nd bindings = %+v, want config1/surface 3 only", s3.Bindings)
	}
	if _, ok := byName["s3_vd"]; !ok {
		t.Errorf("no shared s3_vd variable")
	}
	if !strings.Contains(stderr, "surface 3 is not glass in config0") {
		t.Errorf("partial binding not reported; stderr = %q", stderr)
	}

	// Surface 1 exists in both configs and stays fully shared.
	if got := len(byName["s1_nd"].Bindings); got != 2 {
		t.Errorf("s1_nd bindings = %d, want 2", got)
	}

	// A shared layout (the common case) reports nothing.
	full := parseSharedGlassInput(t)
	if out := captureStderr(t, func() { applyGlassVariables(full, sharedGlassCatalog()) }); out != "" {
		t.Errorf("shared layout warned: %q", out)
	}
}
