package main

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
	"gopkg.in/yaml.v3"
)

const glassColorTripletYAML = `
metadata:
  tool: RayWeaver
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

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
