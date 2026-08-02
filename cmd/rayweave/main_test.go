package main

import (
	"bytes"
	"io"
	"math"
	"os"
	"reflect"
	"testing"

	"github.com/hiroki/rayweaver/internal/chief"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
	"gopkg.in/yaml.v3"
)

func TestResolveRayFanConfig(t *testing.T) {
	cases := []struct {
		name       string
		rayFan     bool
		fanPlane   string
		rotation   []float64
		wantAngles []float64
		wantNil    bool
	}{
		{name: "no fan", rayFan: false, wantNil: true},
		{name: "ray-fan default", rayFan: true, wantAngles: []float64{0, 90}},
		{name: "plane yz", rayFan: false, fanPlane: "yz", wantAngles: []float64{90}},
		{name: "plane xz", rayFan: false, fanPlane: "xz", wantAngles: []float64{0}},
		{name: "rotation", rayFan: false, rotation: []float64{0, 45, 90}, wantAngles: []float64{0, 45, 90}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := resolveRayFanConfig(c.rayFan, c.fanPlane, c.rotation)
			if c.wantNil {
				if cfg != nil {
					t.Errorf("expected nil config, got %+v", cfg)
				}
				return
			}
			if cfg == nil {
				t.Fatal("expected non-nil config")
			}
			if !reflect.DeepEqual(cfg.Angles, c.wantAngles) {
				t.Errorf("Angles = %v, want %v", cfg.Angles, c.wantAngles)
			}
			if cfg.NumRays != 256 {
				t.Errorf("NumRays = %d, want 256", cfg.NumRays)
			}
		})
	}
}

func TestExpandFanRotationArgs(t *testing.T) {
	in := []string{"--fan-rotation", "0", "45", "90", "--config", "cfg1"}
	want := []string{"--fan-rotation", "0", "--fan-rotation", "45", "--fan-rotation", "90", "--config", "cfg1"}
	got := expandFanRotationArgs(in)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("expandFanRotationArgs = %v, want %v", got, want)
	}
}

func floatPtr(v float64) *float64 { return &v }

func TestExtractMarginalRaysHasX(t *testing.T) {
	mkGrid := func(xs, ys []float64) []types.GridPoint {
		pts := make([]types.GridPoint, len(xs))
		for i := range pts {
			pts[i].ImageX = floatPtr(xs[i])
			pts[i].ImageY = floatPtr(ys[i])
		}
		return pts
	}

	// Pure X-direction field must yield X marginal rays.
	xResult := chief.Result{
		FieldDir: types.Vec3{X: 1, Y: 0},
		GridPoints: mkGrid(
			[]float64{0.1, 0.9, -0.8, 0.0},
			[]float64{0.2, 0.3, -0.4, 0.5},
		),
	}
	// Pure Y-direction field must NOT yield X marginal rays.
	yResult := chief.Result{
		FieldDir: types.Vec3{X: 0, Y: 1},
		GridPoints: mkGrid(
			[]float64{0.9, -0.8, 0.1},
			[]float64{0.9, -0.7, 0.2},
		),
	}

	rays := extractMarginalRays([]chief.Result{xResult, yResult}, 0.00058756, nil, types.JonesVector{})
	ids := map[string]bool{}
	for _, r := range rays {
		ids[r.ID] = true
	}
	if !ids["marginal_f0_Xplus"] || !ids["marginal_f0_Xminus"] {
		t.Errorf("pure-X field: expected X marginal rays, got %v", ids)
	}
	if ids["marginal_f1_Xplus"] || ids["marginal_f1_Xminus"] {
		t.Errorf("pure-Y field: X marginal rays must not be generated, got %v", ids)
	}
	if !ids["marginal_f1_Yplus"] || !ids["marginal_f1_Yminus"] {
		t.Errorf("pure-Y field: expected Y marginal rays, got %v", ids)
	}
}

// TestOptimizeRayPathsIgnored is a regression test for the improvement report
// (3.3): a configs[].ray_paths block was reported to break spot optimization.
// ray_paths is render-only metadata (the optimizer never reads it), so the
// optimized EFL must be identical with and without it.
func TestOptimizeRayPathsIgnored(t *testing.T) {
	base := `glass_catalog:
  entries:
    - {type: model, name: SK18, nd: 1.63854, vd: 55.42}
    - {type: model, name: SF12, nd: 1.64831, vd: 33.84}
optimization:
  method: dls
  max_iter: 20
  aperture_margin: 1.0
  variables:
    - {name: s1_c, target: {type: surface, id: 1, param: curvature}, min: 0.05, max: 0.2, active: true}
    - {name: s3_c, target: {type: surface, id: 3, param: curvature}, min: -0.15, max: -0.01, active: true}
    - {name: s6_c, target: {type: surface, id: 6, param: curvature}, min: 0.0, max: 0.05, active: true}
    - {name: s7_c, target: {type: surface, id: 7, param: curvature}, min: -0.2, max: -0.01, active: true}
configs:
  - id: cfg1
    name: cfg1
    active: true
    fields:
      - {id: 0, angle_deg: 0.0, weight: 1.0}
    wavelengths:
      - {id: 0, value: 0.0005876, weight: 1.0}
    surfaces:
      - {id: 1, type: sphere, radius: 10.2871491742, thickness: 1.524, material: SK18, diameter: 10.0}
      - {id: 2, type: sphere, radius: -239.3967954752, thickness: 2.3368, material: AIR, diameter: 10.0}
      - {id: 3, type: sphere, radius: -12.826987173, thickness: 0.508, material: SF12, diameter: 6.0}
      - {id: 4, type: sphere, radius: 10.5917184406, thickness: 1.4986, material: AIR, diameter: 6.0}
      - {id: 5, type: sphere, radius: 0, thickness: 1.016, material: AIR, diameter: 3.78}
      - {id: 6, type: sphere, radius: 61.84562942, thickness: 1.524, material: SK18, diameter: 6.0}
      - {id: 7, type: sphere, radius: -10.0074859032, thickness: 21.36695183553, material: AIR, diameter: 6.0}
      - {id: 8, type: sphere, radius: 0, thickness: 0, material: AIR, diameter: 44.0}
    merit:
      type: weighted_sum
      terms:
        - {kind: spot_rms, field: 0, wavelength: 0.0005876, weight: 1.0}
`
	// With ray_paths block inserted into the config.
	withRay := base + `    ray_paths:
      - {object_surface: 0, image_surface: 8, stop_surface: 5}
`

	eflOf := func(yamlData string) float64 {
		var input types.Input
		if err := yaml.Unmarshal([]byte(yamlData), &input); err != nil {
			t.Fatalf("yaml.Unmarshal: %v", err)
		}
		gc, _ := loadCatalogs(&input, "")
		var out bytes.Buffer
		old := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w
		// swallow stderr
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
			outYAML, err := yaml.Marshal(&input)
			if err != nil {
				t.Fatalf("yaml.Marshal: %v", err)
			}
			runOptimize(outYAML, false, "", "", "")
		}()
		io.Copy(&out, r)
		r.Close()
		var outInput types.Input
		if err := yaml.Unmarshal(out.Bytes(), &outInput); err != nil {
			t.Fatalf("output yaml.Unmarshal: %v", err)
		}
		sys := types.System{Surfaces: outInput.Configs[0].Surfaces}
		res := paraxial.Compute(sys, 0.0005876, gc, 0, nil)
		return res.FocalLength
	}

	noRay := eflOf(base)
	yesRay := eflOf(withRay)
	if math.Abs(noRay-yesRay) > 1e-6 {
		t.Errorf("optimized EFL differs with ray_paths: without=%v with=%v", noRay, yesRay)
	}
}

// runChiefWithArgs calls runChief with the given flag arguments and returns the
// YAML written to stdout.
func runChiefWithArgs(t *testing.T, args []string, data []byte) []byte {
	oldArgs := os.Args
	os.Args = append([]string{"rayweave", "chief"}, args...)
	defer func() { os.Args = oldArgs }()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	var out bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		io.Copy(&out, r)
	}()
	runChief(data)
	w.Close()
	os.Stdout = old
	<-done
	r.Close()
	return out.Bytes()
}

// TestClearApertureShrink is a regression test for the improvement report
// (3.5): --clear-aperture --shrink must size diameters to the beam footprint
// including off-axis fields (the old re-aiming lost the axial component and
// produced undersized apertures that vignetted off-axis beams). After
// shrinking, a chief trace must still pass all field rays.
func TestClearApertureShrink(t *testing.T) {
	// A wide-field triplet with oversized diameters.
	yamlData := `glass_catalog:
  entries:
    - {type: model, name: SK18, nd: 1.63854, vd: 55.42}
    - {type: model, name: SF12, nd: 1.64831, vd: 33.84}
chief:
  fields:
    - {angle: 0.0, direction: [0, 1]}
    - {angle: 23.0, direction: [0, 1]}
  reference_surface: 8
  num_rays: 256
  grid_type: hex
  dump_map: false
configs:
  - id: cfg1
    active: true
    surfaces:
      - {id: 1, type: sphere, radius: 10.2871491742, thickness: 1.524, material: SK18, diameter: 20.0}
      - {id: 2, type: sphere, radius: -239.3967954752, thickness: 2.3368, material: AIR, diameter: 20.0}
      - {id: 3, type: sphere, radius: -12.826987173, thickness: 0.508, material: SF12, diameter: 20.0}
      - {id: 4, type: sphere, radius: 10.5917184406, thickness: 1.4986, material: AIR, diameter: 20.0}
      - {id: 5, type: sphere, radius: 0, thickness: 1.016, material: AIR, diameter: 3.78}
      - {id: 6, type: sphere, radius: 61.84562942, thickness: 1.524, material: SK18, diameter: 20.0}
      - {id: 7, type: sphere, radius: -10.0074859032, thickness: 21.36695183553, material: AIR, diameter: 20.0}
      - {id: 8, type: sphere, radius: 0, thickness: 0, material: AIR, diameter: 44.0}
`
	out := runChiefWithArgs(t, []string{"--clear-aperture", "--shrink", "--clear-aperture-rays", "1024"}, []byte(yamlData))

	var res struct {
		Configs []types.Config `yaml:"configs"`
	}
	if err := yaml.Unmarshal(out, &res); err != nil {
		t.Fatalf("yaml.Unmarshal clear-aperture output: %v", err)
	}
	surfs := res.Configs[0].Surfaces
	// Front element should have been shrunk well below its input 20 mm.
	if s1 := surfaceDiameterOf(surfs, 1); s1 >= 19.0 {
		t.Errorf("s1 diameter after --shrink = %v, want < 19 (beam footprint)", s1)
	}
	// The stop (surface 5) is preserved as the aperture.
	if s5 := surfaceDiameterOf(surfs, 5); math.Abs(s5-3.78) > 1e-6 {
		t.Errorf("stop (s5) diameter = %v, want 3.78 (aperture preserved)", s5)
	}
	// With the shrunk diameters, a chief trace must still pass the off-axis
	// field rays (the bug used to undersize and clip them).
	trace := runChiefWithArgs(t, nil, out)
	var tr struct {
		ChiefRays []struct {
			SpotStats *struct {
				TracedRays int `yaml:"traced_rays"`
			} `yaml:"spot_stats"`
		} `yaml:"chief_rays"`
	}
	if err := yaml.Unmarshal(trace, &tr); err != nil {
		t.Fatalf("yaml.Unmarshal trace output: %v", err)
	}
	if len(tr.ChiefRays) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(tr.ChiefRays))
	}
	for i, cr := range tr.ChiefRays {
		if cr.SpotStats == nil || cr.SpotStats.TracedRays < 200 {
			t.Errorf("field %d traced %d rays (<200): off-axis beam clipped after shrink", i, func() int {
				if cr.SpotStats == nil {
					return -1
				}
				return cr.SpotStats.TracedRays
			}())
		}
	}
}

func surfaceDiameterOf(surfaces []types.Surface, id int) float64 {
	for _, s := range surfaces {
		if s.ID == id {
			return s.Diameter
		}
	}
	return 0
}

// runScaleWithArgs calls runScale with the given flag arguments and returns
// the YAML written to stdout.
func runScaleWithArgs(t *testing.T, args []string, data []byte) []byte {
	oldArgs := os.Args
	os.Args = append([]string{"rayweave", "scale"}, args...)
	defer func() { os.Args = oldArgs }()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	var out bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		io.Copy(&out, r)
	}()
	runScale(data)
	w.Close()
	os.Stdout = old
	<-done
	r.Close()
	return out.Bytes()
}

// TestScaleToEFL is a regression test for the improvement report (3.8): the
// scale subcommand must resize a system so its EFL equals --efl exactly, and
// uniform scaling must preserve the f-number.
func TestScaleToEFL(t *testing.T) {
	yamlData := `glass_catalog:
  entries:
    - {type: model, name: N-BK7, nd: 1.5168, vd: 64.17}
configs:
  - id: cfg1
    active: true
    surfaces:
      - {id: 1, type: sphere, radius: 50.0, thickness: 5.0, material: N-BK7, diameter: 30.0}
      - {id: 2, type: sphere, radius: -50.0, thickness: 100.0, material: AIR, diameter: 30.0}
      - {id: 3, type: sphere, radius: 0, thickness: 0, material: AIR, diameter: 30.0}
`
	out := runScaleWithArgs(t, []string{"--efl", "40"}, []byte(yamlData))

	var res struct {
		Configs []types.Config `yaml:"configs"`
	}
	if err := yaml.Unmarshal(out, &res); err != nil {
		t.Fatalf("yaml.Unmarshal scale output: %v", err)
	}
	surfs := res.Configs[0].Surfaces

	// Current EFL of the un-scaled singlet (R = +-50, n = 1.5168).
	var orig struct {
		Configs []types.Config `yaml:"configs"`
	}
	if err := yaml.Unmarshal([]byte(yamlData), &orig); err != nil {
		t.Fatalf("yaml.Unmarshal original: %v", err)
	}
	surface.Precompute(orig.Configs[0].Surfaces)
	gc0 := glass.NewCatalog()
	gc0.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	cur := paraxial.Compute(types.System{Surfaces: orig.Configs[0].Surfaces}, 0.0005876, gc0, 0, nil).FocalLength
	s := 40.0 / cur

	// Radii and thicknesses scale by the same factor.
	if got := surfs[0].Curvature; math.Abs(got-1.0/(50*s)) > 1e-4 {
		t.Errorf("s1 curvature = %v, want %v", got, 1.0/(50*s))
	}
	if got := surfs[0].Thickness; math.Abs(got-5*s) > 1e-4 {
		t.Errorf("s1 thickness = %v, want %v", got, 5*s)
	}

	surface.Precompute(surfs)
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	efl := paraxial.Compute(types.System{Surfaces: surfs}, 0.0005876, gc, 0, nil).FocalLength
	if math.Abs(efl-40.0) > 1e-3 {
		t.Errorf("scaled EFL = %v, want 40", efl)
	}
}
