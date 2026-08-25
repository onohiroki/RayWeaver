package main

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
	"gopkg.in/yaml.v3"
)

const glassColorTripletYAML = `
metadata:
  tool:
    name: RayWeaver
    schema_version: 1
glass_catalog:
  entries:
  - name: "SK18"
    nd: 1.63854
    vd: 55.42
  - name: "SF12"
    nd: 1.64831
    vd: 33.84
chief:
  stop_surface: 4
  fields:
  - angle: 0.0
  - angle: 16.0
  - angle: 24.0
optimization:
  method: dls
  max_iter: 2
  mu: 0.01
  tol: 1e-4
  epsilon: 1e-4
  num_rays: 16
configs:
- id: 0
  active: true
  fields:
  - {id: 0, angle_deg: 0.0, weight: 1.0}
  - {id: 1, angle_deg: 16.0, weight: 1.0}
  - {id: 2, angle_deg: 24.0, weight: 0.5}
  surfaces:
  - {id: 1, type: sphere, radius: 10.2871491742, thickness: 1.524, material: {key: SK18}, diameter: 10.0}
  - {id: 2, type: sphere, radius: -239.3967954752, thickness: 2.3368, material: AIR, diameter: 10.0}
  - {id: 3, type: sphere, radius: -12.826987173, thickness: 0.508, material: {key: SF12}, diameter: 6.0}
  - {id: 4, type: sphere, radius: 10.5917184406, thickness: 1.4986, material: AIR, diameter: 6.0}
  - {id: 5, type: sphere, radius: 0, thickness: 1.016, material: AIR, diameter: 3.78}
  - {id: 6, type: sphere, radius: 61.84562942, thickness: 1.524, material: {key: SK18}, diameter: 6.0}
  - {id: 7, type: sphere, radius: -10.0074859032, thickness: 21.36695183553, material: AIR, diameter: 6.0}
  - {id: 8, type: sphere, radius: 0, thickness: 0, material: AIR, diameter: 44.0}
`

// captureOptimize runs runOptimize with the given flags and returns the YAML
// written to stdout plus the parsed output.
func captureOptimize(t *testing.T, inputYAML string, powerSolve, glassColor bool, solveSurfaces string) types.Input {
	t.Helper()
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	oldErr := os.Stderr
	_, ew, _ := os.Pipe()
	os.Stderr = ew
	func() {
		defer func() {
			os.Stdout = old
			os.Stderr = oldErr
			w.Close()
			ew.Close()
		}()
		var input types.Input
		if err := yaml.Unmarshal([]byte(inputYAML), &input); err != nil {
			t.Fatalf("yaml.Unmarshal: %v", err)
		}
		outYAML, err := yaml.Marshal(&input)
		if err != nil {
			t.Fatalf("yaml.Marshal: %v", err)
		}
		runOptimize(outYAML, false, "", "", "", powerSolve, solveSurfaces, glassColor)
	}()
	var out bytes.Buffer
	io.Copy(&out, r)
	r.Close()
	var outInput types.Input
	if err := yaml.Unmarshal(out.Bytes(), &outInput); err != nil {
		t.Fatalf("output yaml.Unmarshal: %v", err)
	}
	return outInput
}

// TestGlassColorAutoGeneratesAndWritesBack verifies --glass-color --power-solve
// auto-generates the nd/vd variables and chromatic merit, writes back the
// power_solve section, and preserves the element powers.
func TestGlassColorAutoGeneratesAndWritesBack(t *testing.T) {
	out := captureOptimize(t, glassColorTripletYAML, true, true, "2,4,7")

	// Power-solve write-back.
	ps := out.Optimization.PowerSolve
	if ps == nil || !ps.Enabled {
		t.Fatalf("power_solve write-back missing/enabled: %+v", ps)
	}
	if len(ps.Surfaces) != 3 || ps.Surfaces[0] != 2 || ps.Surfaces[1] != 4 || ps.Surfaces[2] != 7 {
		t.Errorf("power_solve.surfaces = %v, want [2 4 7]", ps.Surfaces)
	}

	// Auto-generated nd/vd variables (one repr surface per element).
	params := map[string]bool{}
	for _, v := range out.Optimization.Variables {
		params[v.Target.Param] = true
	}
	if !params["nd"] || !params["vd"] {
		t.Errorf("glass-color did not generate nd/vd variables, params=%v", params)
	}

	// Auto-generated chromatic merit on the config.
	var hasLCA, hasTCA bool
	for _, cfg := range out.Configs {
		if cfg.Merit == nil {
			continue
		}
		for _, term := range cfg.Merit.Terms {
			if term.Kind == "longitudinal_color" {
				hasLCA = true
			}
			if term.Kind == "lateral_color" {
				hasTCA = true
			}
		}
	}
	if !hasLCA || !hasTCA {
		t.Errorf("glass-color merit missing chromatic terms: lca=%v tca=%v", hasLCA, hasTCA)
	}

	// Element powers preserved between input and output.
	in := mustParseInput(t, glassColorTripletYAML)
	inGC, _ := loadCatalogs(&in, "")
	inSurf := in.Configs[0].Surfaces
	surface.Precompute(inSurf)
	inPhi := map[int]float64{}
	for _, id := range []int{1, 3, 6} {
		inPhi[id] = paraxial.ElementPowerForSurface(inSurf, paraxial.DLine, inGC, id)
	}

	outGC, _ := loadCatalogs(&out, "")
	outSurf := out.Configs[0].Surfaces
	surface.Precompute(outSurf)
	for _, id := range []int{1, 3, 6} {
		got := paraxial.ElementPowerForSurface(outSurf, paraxial.DLine, outGC, id)
		if abs(got-inPhi[id]) > 1e-9 {
			t.Errorf("element power surf %d changed: want %v got %v", id, inPhi[id], got)
		}
	}
}

func mustParseInput(t *testing.T, yamlData string) types.Input {
	t.Helper()
	var in types.Input
	if err := yaml.Unmarshal([]byte(yamlData), &in); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	return in
}

// TestResolveGlassHullDefaultOn verifies the convex-hull is enabled by default
// (real-glass region), honoring an explicit disabled config, and carrying the
// custom margin/weight when enabled.
func TestResolveGlassHullDefaultOn(t *testing.T) {
	var hull *glass.ConvexHull
	m, w := resolveGlassHull(nil, &hull)
	if hull == nil {
		t.Fatal("default-on: hull should be set when glass_hull is absent")
	}
	if m != 0.02 || w != 1.0 {
		t.Errorf("default margin/weight = %v/%v, want 0.02/1.0", m, w)
	}

	// Explicitly disabled -> no hull.
	var h2 *glass.ConvexHull
	m2, w2 := resolveGlassHull(&types.GlassHullConfig{Enabled: false}, &h2)
	if h2 != nil {
		t.Error("explicitly disabled glass_hull should yield no hull")
	}
	if m2 != 0 || w2 != 0 {
		t.Errorf("disabled margin/weight = %v/%v, want 0/0", m2, w2)
	}

	// Enabled with custom values is honored.
	cfgCustom := &types.GlassHullConfig{Enabled: true, Margin: 0.05, Weight: 2.0}
	var h3 *glass.ConvexHull
	m3, w3 := resolveGlassHull(cfgCustom, &h3)
	if h3 == nil {
		t.Error("enabled glass_hull should yield a hull")
	}
	if m3 != 0.05 || w3 != 2.0 {
		t.Errorf("custom margin/weight = %v/%v, want 0.05/2.0", m3, w3)
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// TestBuildGlassPhaseContext verifies the escape glass-phase context is derived
// from the resolved optimization.power_solve section: enabled, the solve
// surfaces, and a colour-only (longitudinal + lateral) glass merit per active
// config.
func TestBuildGlassPhaseContext(t *testing.T) {
	// Enabled when power_solve is on, surfaces are listed, and at least one
	// nd/vd glass variable is declared (global determination).
	in := mustParseInput(t, glassColorTripletYAML)
	in.Optimization.PowerSolve = &types.PowerSolveConfig{Enabled: true, Surfaces: []int{2, 4, 7}}
	in.Optimization.Variables = append(in.Optimization.Variables, types.OptimizationVariable{
		Name: "s1_nd", Target: types.VariableTarget{Type: "surface", ID: 1, Param: "nd"}, Min: 1.4, Max: 2.0, Active: true,
	})
	ctx := buildGlassPhaseContext(&in)

	if !ctx.enabled {
		t.Fatal("context not enabled")
	}
	if len(ctx.surfaces) != 3 || ctx.surfaces[0] != 2 || ctx.surfaces[2] != 7 {
		t.Errorf("surfaces = %v, want [2 4 7]", ctx.surfaces)
	}
	// A colour-only merit should be present for the active config.
	terms := ctx.merit["0"]
	if len(terms) == 0 {
		t.Fatal("no glass merit for config 0")
	}
	var hasL, hasT bool
	for _, tm := range terms {
		if tm.Kind == "longitudinal_color" {
			hasL = true
		}
		if tm.Kind == "lateral_color" {
			hasT = true
		}
	}
	if !hasL || !hasT {
		t.Errorf("glass merit missing chromatic terms: lca=%v tca=%v", hasL, hasT)
	}

	// Skipped when power_solve is on but no nd/vd variable is declared.
	in3 := mustParseInput(t, glassColorTripletYAML)
	in3.Optimization.PowerSolve = &types.PowerSolveConfig{Enabled: true, Surfaces: []int{2, 4, 7}}
	if ctx3 := buildGlassPhaseContext(&in3); ctx3.enabled {
		t.Error("context should be skipped when no glass variable is declared")
	}

	// Enabled via a local (multi-config) vd variable.
	in4 := mustParseInput(t, glassColorTripletYAML)
	in4.Optimization.PowerSolve = &types.PowerSolveConfig{Enabled: true, Surfaces: []int{2, 4, 7}}
	in4.Optimization.LocalVariables = []types.LocalVariableDef{
		{Name: "s3_vd", Config: "0", Target: types.VariableTarget{Type: "surface", ID: 3, Param: "vd"}, Min: 20, Max: 80, Active: true},
	}
	if ctx4 := buildGlassPhaseContext(&in4); !ctx4.enabled {
		t.Error("context should be enabled with a local vd variable")
	}

	// Enabled via a shared nd variable binding.
	in5 := mustParseInput(t, glassColorTripletYAML)
	in5.Optimization.PowerSolve = &types.PowerSolveConfig{Enabled: true, Surfaces: []int{2, 4, 7}}
	in5.Optimization.SharedVariables = []types.SharedVariable{
		{Name: "sh_nd", Min: 1.4, Max: 2.0, Active: true, Bindings: []types.SharedVariableBinding{{Config: "0", ID: 1, Param: "nd"}}},
	}
	if ctx5 := buildGlassPhaseContext(&in5); !ctx5.enabled {
		t.Error("context should be enabled with a shared nd binding")
	}

	// Disabled power_solve -> context disabled.
	in2 := mustParseInput(t, glassColorTripletYAML)
	if ctx2 := buildGlassPhaseContext(&in2); ctx2.enabled {
		t.Error("context should be disabled when power_solve is absent")
	}
}
