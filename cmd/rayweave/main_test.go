package main

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
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

	rays := extractMarginalRays([]chief.Result{xResult, yResult}, 0, nil, 0.00058756, nil, types.JonesVector{})
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
			runOptimize(outYAML, false, "", "", "", false, "", false)
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

// TestClearApertureAutoAperture sizes only auto_aperture: true surfaces to the
// beam footprint (including off-axis fields) while fixed (auto_aperture: false)
// surfaces keep their diameter, and the off-axis beam must still pass.
func TestClearApertureAutoAperture(t *testing.T) {
	// A wide-field triplet with oversized auto_aperture diameters and a fixed
	// aperture (surface 5) that must be preserved.
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
      - {id: 1, type: sphere, radius: 10.2871491742, thickness: 1.524, material: SK18, diameter: 20.0, auto_aperture: true}
      - {id: 2, type: sphere, radius: -239.3967954752, thickness: 2.3368, material: AIR, diameter: 20.0, auto_aperture: true}
      - {id: 3, type: sphere, radius: -12.826987173, thickness: 0.508, material: SF12, diameter: 20.0, auto_aperture: true}
      - {id: 4, type: sphere, radius: 10.5917184406, thickness: 1.4986, material: AIR, diameter: 20.0, auto_aperture: true}
      - {id: 5, type: sphere, radius: 0, thickness: 1.016, material: AIR, diameter: 3.78}
      - {id: 6, type: sphere, radius: 61.84562942, thickness: 1.524, material: SK18, diameter: 20.0, auto_aperture: true}
      - {id: 7, type: sphere, radius: -10.0074859032, thickness: 21.36695183553, material: AIR, diameter: 20.0, auto_aperture: true}
      - {id: 8, type: sphere, radius: 0, thickness: 0, material: AIR, diameter: 44.0}
`
	out := runChiefWithArgs(t, []string{"--clear-aperture", "--clear-aperture-rays", "1024"}, []byte(yamlData))

	var res struct {
		Configs []types.Config `yaml:"configs"`
	}
	if err := yaml.Unmarshal(out, &res); err != nil {
		t.Fatalf("yaml.Unmarshal clear-aperture output: %v", err)
	}
	surfs := res.Configs[0].Surfaces
	// The fixed aperture (surface 5) is preserved.
	if s5 := surfaceDiameterOf(surfs, 5); math.Abs(s5-3.78) > 1e-6 {
		t.Errorf("fixed aperture (s5) diameter = %v, want 3.78 (auto_aperture: false surfaces are never resized)", s5)
	}
	// The reference surface is untouched.
	if s8 := surfaceDiameterOf(surfs, 8); math.Abs(s8-44.0) > 1e-6 {
		t.Errorf("reference (s8) diameter = %v, want 44", s8)
	}
	// At least one auto_aperture surface was resized below the input 20 mm.
	resized := false
	for _, id := range []int{1, 2, 3, 4, 6, 7} {
		if d := surfaceDiameterOf(surfs, id); d < 19.0 {
			resized = true
		}
	}
	if !resized {
		t.Errorf("no auto_aperture surface was shrunk below 19 mm (beam footprint sizing failed)")
	}
	// With the sized diameters the on-axis beam must pass fully; the off-axis
	// beam is legitimately vignetted by the fixed aperture (surface 5), so it
	// must still trace a substantial fraction of its rays.
	trace := runChiefWithArgs(t, nil, out)
	var tr struct {
		ChiefRays []struct {
			SpotStats *struct {
				TotalRays  int `yaml:"total_rays"`
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
	if cr := tr.ChiefRays[0]; cr.SpotStats == nil || cr.SpotStats.TracedRays < cr.SpotStats.TotalRays {
		t.Errorf("on-axis field traced %d/%d rays: on-axis beam must never be clipped", func() int {
			if cr.SpotStats == nil {
				return -1
			}
			return cr.SpotStats.TracedRays
		}(), func() int {
			if cr.SpotStats == nil {
				return -1
			}
			return cr.SpotStats.TotalRays
		}())
	}
	if cr := tr.ChiefRays[1]; cr.SpotStats == nil || cr.SpotStats.TracedRays < 150 {
		t.Errorf("off-axis field traced %d rays (<150): beam too heavily clipped after sizing", func() int {
			if cr.SpotStats == nil {
				return -1
			}
			return cr.SpotStats.TracedRays
		}())
	}
}

// TestClearApertureUndersizedInput regresses the stale-diameter self-clipping
// bug: when the input auto_aperture diameters are smaller than the true beam,
// --clear-aperture must still size to the full beam footprint (auto-aperture
// checks are skipped during the measurement) rather than inherit the
// truncation. The front surface must grow well beyond the 1.0 mm input and the
// sized lens must pass the on-axis beam untruncated.
func TestClearApertureUndersizedInput(t *testing.T) {
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
      - {id: 1, type: sphere, radius: 10.2871491742, thickness: 1.524, material: SK18, diameter: 1.0, auto_aperture: true}
      - {id: 2, type: sphere, radius: -239.3967954752, thickness: 2.3368, material: AIR, diameter: 1.0, auto_aperture: true}
      - {id: 3, type: sphere, radius: -12.826987173, thickness: 0.508, material: SF12, diameter: 1.0, auto_aperture: true}
      - {id: 4, type: sphere, radius: 10.5917184406, thickness: 1.4986, material: AIR, diameter: 1.0, auto_aperture: true}
      - {id: 5, type: sphere, radius: 0, thickness: 1.016, material: AIR, diameter: 3.7825297358}
      - {id: 6, type: sphere, radius: 61.84562942, thickness: 1.524, material: SK18, diameter: 1.0, auto_aperture: true}
      - {id: 7, type: sphere, radius: -10.0074859032, thickness: 21.36695183553, material: AIR, diameter: 1.0, auto_aperture: true}
      - {id: 8, type: sphere, radius: 0, thickness: 0, material: AIR}
`
	out := runChiefWithArgs(t, []string{"--clear-aperture", "--clear-aperture-rays", "1024"}, []byte(yamlData))

	var res struct {
		Configs []types.Config `yaml:"configs"`
	}
	if err := yaml.Unmarshal(out, &res); err != nil {
		t.Fatalf("yaml.Unmarshal clear-aperture output: %v", err)
	}
	surfs := res.Configs[0].Surfaces
	// The undersized input must NOT limit the sizing: the front surface grows
	// to cover the real beam (the on-axis marginal is ~1.8 mm at surface 1),
	// far beyond the 1.0 mm input semi-diameter.
	if d := surfaceDiameterOf(surfs, 1); d < 3.0 {
		t.Errorf("front auto_aperture diameter = %v, want >= 3.0 (undersized input truncated the beam footprint)", d)
	}
	if s5 := surfaceDiameterOf(surfs, 5); math.Abs(s5-3.7825297358) > 1e-6 {
		t.Errorf("fixed aperture (s5) diameter = %v, want 3.7825297358 (auto_aperture: false surfaces are never resized)", s5)
	}
	// With the sized diameters the full beam must pass: the on-axis field
	// untruncated, the off-axis field at most vignetted by the fixed aperture.
	trace := runChiefWithArgs(t, nil, out)
	var tr struct {
		ChiefRays []struct {
			SpotStats *struct {
				TotalRays  int `yaml:"total_rays"`
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
	if cr := tr.ChiefRays[0]; cr.SpotStats == nil || cr.SpotStats.TracedRays != cr.SpotStats.TotalRays {
		t.Errorf("on-axis field traced %d/%d rays: undersized input must not clip the on-axis beam", func() int {
			if cr.SpotStats == nil {
				return -1
			}
			return cr.SpotStats.TracedRays
		}(), func() int {
			if cr.SpotStats == nil {
				return -1
			}
			return cr.SpotStats.TotalRays
		}())
	}
	if cr := tr.ChiefRays[1]; cr.SpotStats == nil || cr.SpotStats.TracedRays < 150 {
		t.Errorf("off-axis field traced %d rays (<150): beam too heavily clipped after sizing", func() int {
			if cr.SpotStats == nil {
				return -1
			}
			return cr.SpotStats.TracedRays
		}())
	}
}

// TestLoadCatalogsFiltersUnreferencedAGF verifies that --glass-dir registers
// into the emitted glass_catalog only the glasses referenced by
// configs[].surfaces.material.key, while the runtime catalog still keeps every
// AGF glass for lookups.
func TestLoadCatalogsFiltersUnreferencedAGF(t *testing.T) {
	dir := t.TempDir()
	// AGF line format: NM <name> <dispersion> <glasscode> <nd> <vd>.
	agf := `NM GLASS_A 1 1 1.50 70.0
NM GLASS_B 1 1 1.60 40.0
NM GLASS_C 1 1 1.70 30.0
`
	agfPath := filepath.Join(dir, "test.agf")
	if err := os.WriteFile(agfPath, []byte(agf), 0644); err != nil {
		t.Fatalf("write AGF: %v", err)
	}

	yamlData := `configs:
  - id: cfg1
    active: true
    surfaces:
      - {id: 1, type: sphere, radius: 50, thickness: 5, material: {key: GLASS_A}}
      - {id: 2, type: sphere, radius: -50, thickness: 100, material: {key: GLASS_B}}
`
	var input types.Input
	if err := yaml.Unmarshal([]byte(yamlData), &input); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}

	gc, _ := loadCatalogs(&input, dir)

	// Only the referenced GLASS_A / GLASS_B are registered; GLASS_C is not.
	got := map[string]bool{}
	for _, g := range input.GlassCatalog.Entries {
		got[types.ResolveGlassKey(g)] = true
	}
	if !got["GLASS_A"] || !got["GLASS_B"] {
		t.Errorf("referenced glasses not registered: got %v", got)
	}
	if got["GLASS_C"] {
		t.Errorf("unreferenced AGF glass GLASS_C must not be registered, got %v", got)
	}

	// Runtime catalog still resolves all AGF glasses.
	for _, k := range []string{"GLASS_A", "GLASS_B", "GLASS_C"} {
		if _, ok := gc.Lookup(k); !ok {
			t.Errorf("runtime gc: %s not resolved", k)
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

func TestBuildGlassMapResolvesNormalizedNames(t *testing.T) {
	output := types.Output{
		Input: types.Input{
			GlassCatalog: &types.GlassCatalog{
				Entries: []types.Glass{
					{
						Type: types.GlassTypeCatalog, Name: "L-LAL12", ND: 1.62004, VD: 36.41,
						Manufacturer: "OHARA", DispersionFormula: types.Sellmeier1,
					},
				},
			},
		},
	}

	cases := []struct {
		material string
		wantND   float64
	}{
		{material: "L-LAL12", wantND: 1.62004},      // exact AGF name
		{material: "LLAL12", wantND: 1.62004},       // CODE V hyphen-stripped
		{material: "LLAL12_OHARA", wantND: 1.62004}, // CODE V manufacturer suffix
	}
	for _, c := range cases {
		surfaces := []types.Surface{
			{ID: 1, Type: types.Sphere, Material: types.Material{Key: c.material}},
			{ID: 2, Type: types.Sphere, Material: types.Material{}},
		}
		m := buildGlassMap(output, surfaces)
		gi, ok := m[c.material]
		if !ok {
			t.Errorf("material %q: no GlassInfo resolved", c.material)
			continue
		}
		if math.Abs(gi.ND-c.wantND) > 1e-6 {
			t.Errorf("material %q: ND = %v, want %v", c.material, gi.ND, c.wantND)
		}
	}

	// A material with no catalog entry stays unresolved (drawn gray).
	surfaces := []types.Surface{{ID: 1, Type: types.Sphere, Material: types.Material{Key: "UNKNOWN_GLASS"}}}
	if m := buildGlassMap(output, surfaces); len(m) != 0 {
		t.Errorf("expected no resolution for unknown material, got %v", m)
	}
}

func TestConfigIndicesForExport(t *testing.T) {
	mkInput := func(n int) *types.Input {
		in := &types.Input{}
		for i := 0; i < n; i++ {
			in.Configs = append(in.Configs, types.Config{ID: "config" + itoaTest(i+1), Surfaces: []types.Surface{{ID: 1}}})
		}
		return in
	}
	cfg1 := "config1"
	cfg2 := "config2"
	cases := []struct {
		name   string
		format string
		flag   *string
		n      int
		want   []int
	}{
		{name: "zemax all", format: "zemax", n: 3, want: []int{0, 1, 2}},
		{name: "codev all", format: "codev", n: 3, want: []int{0, 1, 2}},
		{name: "oslo default", format: "oslo", n: 3, want: []int{0}},
		{name: "zemax single", format: "zemax", flag: &cfg2, n: 3, want: []int{1}},
		{name: "oslo single", format: "oslo", flag: &cfg1, n: 3, want: []int{0}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := configIndicesForExport(mkInput(c.n), c.format, c.flag)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("configIndicesForExport = %v, want %v", got, c.want)
			}
		})
	}
}

func itoaTest(v int) string {
	return fmt.Sprintf("%d", v)
}

func TestExportOutput(t *testing.T) {
	captureStdout := func(fn func()) []byte {
		t.Helper()
		old := os.Stdout
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		os.Stdout = w
		fn()
		w.Close()
		os.Stdout = old
		out, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	foreign := []byte("SURF 1\n")
	yaml := []byte("metadata:\n  tool:\n    name: RayWeaver\n")

	// Without -o the foreign format goes to stdout.
	got := captureStdout(func() { exportOutput("", foreign, yaml) })
	if string(got) != string(foreign) {
		t.Errorf("no -o: stdout = %q, want foreign %q", got, foreign)
	}

	// With -o the foreign format goes to the file and the YAML passes through.
	dir := t.TempDir()
	path := filepath.Join(dir, "out.zmx")
	got = captureStdout(func() { exportOutput(path, foreign, yaml) })
	if string(got) != string(yaml) {
		t.Errorf("with -o: stdout = %q, want input YAML %q", got, yaml)
	}
	fb, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(fb) != string(foreign) {
		t.Errorf("file = %q, want foreign %q", fb, foreign)
	}
}

func TestFormatFromExt(t *testing.T) {
	cases := []struct{ path, want string }{
		{"out.zmx", "zemax"},
		{"lens.ZMX", "zemax"},
		{"out.seq", "codev"},
		{"out.len", "oslo"},
		{"out.txt", ""},
		{"noext", ""},
	}
	for _, c := range cases {
		if got := formatFromExt(c.path); got != c.want {
			t.Errorf("formatFromExt(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestResolveExportFormat(t *testing.T) {
	cases := []struct{ format, outPath, want string }{
		{"zemax", "", "zemax"},        // explicit flag wins
		{"codev", "out.zmx", "codev"}, // explicit flag beats extension
		{"", "out.zmx", "zemax"},      // inferred from extension
		{"", "out.seq", "codev"},
		{"", "out.len", "oslo"},
		{"", "", ""},        // nothing to infer from
		{"", "out.txt", ""}, // unrecognized extension
	}
	for _, c := range cases {
		if got := resolveExportFormat(c.format, c.outPath); got != c.want {
			t.Errorf("resolveExportFormat(%q, %q) = %q, want %q", c.format, c.outPath, got, c.want)
		}
	}
}
