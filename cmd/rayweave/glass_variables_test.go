package main

import (
	"bytes"
	"io"
	"os"
	"strconv"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
	"gopkg.in/yaml.v3"
)

const glassVariablesTripletYAML = `
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
  merit:
    type: sum
    terms:
    - {kind: spot_rms, field: 0, wavelength: 0.0005876, weight: 1.0}
    - {kind: longitudinal_color, wavelength: 0.0004861, wavelength2: 0.0006563, weight: 1.0}
    - {kind: lateral_color, field: 1, wavelength: 0.0004861, wavelength2: 0.0006563, weight: 0.5}
    - {kind: seidel_astigmatism, field: 1, wavelength: 0.0005876, weight: 5.0, target: 0}
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

// captureOptimize runs runOptimize with the given flags and returns the parsed
// output.
func captureOptimize(t *testing.T, inputYAML string, powerSolve, glassVariables bool, solveSurfaces string) types.Input {
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
		runOptimize(outYAML, false, "", "", "", powerSolve, solveSurfaces, glassVariables)
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

// TestGlassVariablesAutoGenerates verifies --glass-variables --power-solve
// auto-generates the nd/vd variables, writes back the power_solve section,
// leaves the config merit unchanged (no colour-only rewrite), and preserves the
// element powers.
func TestGlassVariablesAutoGenerates(t *testing.T) {
	out := captureOptimize(t, glassVariablesTripletYAML, true, true, "2,4,7")

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
		t.Errorf("glass-variables did not generate nd/vd variables, params=%v", params)
	}

	// The config merit is preserved: the geometric spot_rms term must survive
	// (the old --glass-color replaced the merit with a colour-only one).
	if len(out.Configs) == 0 || out.Configs[0].Merit == nil {
		t.Fatal("config merit was removed")
	}
	hasSpot := false
	for _, term := range out.Configs[0].Merit.Terms {
		if term.Kind == "spot_rms" {
			hasSpot = true
		}
	}
	if !hasSpot {
		t.Errorf("config merit was rewritten; spot_rms missing: %+v", out.Configs[0].Merit.Terms)
	}

	// Element powers preserved between input and output.
	in := mustParseInput(t, glassVariablesTripletYAML)
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
		if abs(got-inPhi[id]) > 1e-6 {
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

// TestBuildGlassPhaseMerit verifies the glass-phase objective is the config's
// chromatic terms scaled by color_scale plus the cheap geometric guardrail,
// with expensive grid-trace terms excluded and terms de-duplicated.
func TestBuildGlassPhaseMerit(t *testing.T) {
	in := mustParseInput(t, glassVariablesTripletYAML)
	cfg := in.Configs[0]

	terms := buildGlassPhaseMerit(cfg, 100.0)
	byKind := map[string]types.MeritTerm{}
	for _, tm := range terms {
		byKind[tm.Kind+"#"+strconv.Itoa(tm.Field)] = tm
	}

	// Colour terms scaled by color_scale.
	if lca, ok := byKind["longitudinal_color#0"]; !ok || lca.Weight != 100.0 {
		t.Errorf("longitudinal_color not scaled: %+v", lca)
	}
	if tca, ok := byKind["lateral_color#1"]; !ok || tca.Weight != 50.0 {
		t.Errorf("lateral_color not scaled: %+v", tca)
	}
	// Cheap guardrail retained at its own weight.
	if sa, ok := byKind["seidel_astigmatism#1"]; !ok || sa.Weight != 5.0 {
		t.Errorf("seidel_astigmatism guardrail missing/changed: %+v", sa)
	}
	// Expensive grid-trace term excluded.
	if _, ok := byKind["spot_rms#0"]; ok {
		t.Error("spot_rms should be excluded from the glass phase")
	}

	// Default scale applies when the argument is the built-in default.
	if d := buildGlassPhaseMerit(cfg, defaultGlassColorScale); len(d) != len(terms) {
		t.Errorf("term count differs with default scale: %d vs %d", len(d), len(terms))
	}
}

// TestBuildGlassPhaseContext verifies the escape glass-phase context is derived
// from the resolved optimization.power_solve section: enabled, the solve
// surfaces, and a per-config merit (colour scaled + cheap guardrail) built from
// the config's own terms.
func TestBuildGlassPhaseContext(t *testing.T) {
	// Enabled when power_solve is on, surfaces are listed, and at least one
	// nd/vd glass variable is declared (global determination).
	in := mustParseInput(t, glassVariablesTripletYAML)
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
	terms := ctx.merit["0"]
	if len(terms) == 0 {
		t.Fatal("no glass merit for config 0")
	}
	var hasL, hasT, hasSeidel bool
	for _, tm := range terms {
		switch tm.Kind {
		case "longitudinal_color":
			hasL = true
			if tm.Weight != defaultGlassColorScale {
				t.Errorf("longitudinal_color weight = %v, want %v", tm.Weight, defaultGlassColorScale)
			}
		case "lateral_color":
			hasT = true
		case "seidel_astigmatism":
			hasSeidel = true
		}
	}
	if !hasL || !hasT || !hasSeidel {
		t.Errorf("glass merit composition wrong: lca=%v tca=%v seidel=%v", hasL, hasT, hasSeidel)
	}

	// Explicit color_scale is honored.
	inScale := mustParseInput(t, glassVariablesTripletYAML)
	inScale.Optimization.PowerSolve = &types.PowerSolveConfig{Enabled: true, Surfaces: []int{2, 4, 7}, ColorScale: 7.0}
	inScale.Optimization.Variables = append(inScale.Optimization.Variables, types.OptimizationVariable{
		Name: "s1_nd", Target: types.VariableTarget{Type: "surface", ID: 1, Param: "nd"}, Min: 1.4, Max: 2.0, Active: true,
	})
	for _, tm := range buildGlassPhaseContext(&inScale).merit["0"] {
		if tm.Kind == "longitudinal_color" && tm.Weight != 7.0 {
			t.Errorf("explicit color_scale not honored: weight=%v", tm.Weight)
		}
	}

	// Skipped when power_solve is on but no nd/vd variable is declared.
	in3 := mustParseInput(t, glassVariablesTripletYAML)
	in3.Optimization.PowerSolve = &types.PowerSolveConfig{Enabled: true, Surfaces: []int{2, 4, 7}}
	if ctx3 := buildGlassPhaseContext(&in3); ctx3.enabled {
		t.Error("context should be skipped when no glass variable is declared")
	}

	// Enabled via a local (multi-config) vd variable.
	in4 := mustParseInput(t, glassVariablesTripletYAML)
	in4.Optimization.PowerSolve = &types.PowerSolveConfig{Enabled: true, Surfaces: []int{2, 4, 7}}
	in4.Optimization.LocalVariables = []types.LocalVariableDef{
		{Name: "s3_vd", Config: "0", Target: types.VariableTarget{Type: "surface", ID: 3, Param: "vd"}, Min: 20, Max: 80, Active: true},
	}
	if ctx4 := buildGlassPhaseContext(&in4); !ctx4.enabled {
		t.Error("context should be enabled with a local vd variable")
	}

	// Enabled via a shared nd variable binding.
	in5 := mustParseInput(t, glassVariablesTripletYAML)
	in5.Optimization.PowerSolve = &types.PowerSolveConfig{Enabled: true, Surfaces: []int{2, 4, 7}}
	in5.Optimization.SharedVariables = []types.SharedVariable{
		{Name: "sh_nd", Min: 1.4, Max: 2.0, Active: true, Bindings: []types.SharedVariableBinding{{Config: "0", ID: 1, Param: "nd"}}},
	}
	if ctx5 := buildGlassPhaseContext(&in5); !ctx5.enabled {
		t.Error("context should be enabled with a shared nd binding")
	}

	// Disabled power_solve -> context disabled.
	in2 := mustParseInput(t, glassVariablesTripletYAML)
	if ctx2 := buildGlassPhaseContext(&in2); ctx2.enabled {
		t.Error("context should be disabled when power_solve is absent")
	}
}
