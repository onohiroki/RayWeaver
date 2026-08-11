package main

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"os"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
	"gopkg.in/yaml.v3"
)

// captureStdout runs fn with os.Stdout swapped for a pipe (stderr discarded)
// and returns the bytes written to stdout. os.Args must already be set by the
// caller.
func captureStdout(t *testing.T, fn func()) []byte {
	t.Helper()
	oldOut := os.Stdout
	rOut, wOut, _ := os.Pipe()
	os.Stdout = wOut
	oldErr := os.Stderr
	rErr, wErr, _ := os.Pipe()
	os.Stderr = wErr

	var out bytes.Buffer
	done := make(chan struct{})
	errDone := make(chan struct{})
	go func() {
		defer close(done)
		io.Copy(&out, rOut)
	}()
	go func() {
		defer close(errDone)
		io.Copy(io.Discard, rErr)
	}()

	fn()
	wOut.Close()
	wErr.Close()
	os.Stdout = oldOut
	os.Stderr = oldErr
	<-done
	<-errDone
	rOut.Close()
	rErr.Close()
	return out.Bytes()
}

// runCommand runs fn with os.Args set to args, capturing stdout.
func runCommand(t *testing.T, args []string, fn func()) []byte {
	t.Helper()
	oldArgs := os.Args
	os.Args = args
	defer func() { os.Args = oldArgs }()
	return captureStdout(t, fn)
}

// singletYAML is a minimal single-lens system (no chief / rays / scale /
// vignette / asphere sections) shared by the write-back tests.
const singletYAML = `glass_catalog:
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

// chiefInput returns the singlet with a chief section. A non-zero wl sets
// chief.wavelength.
func chiefInput(wl float64) string {
	s := `glass_catalog:
  entries:
    - {type: model, name: N-BK7, nd: 1.5168, vd: 64.17}
chief:
  fields:
    - {angle: 0, direction: [0, 1]}
  reference_surface: 3
  num_rays: 32
  grid_type: hex
`
	if wl > 0 {
		s += fmt.Sprintf("  wavelength: %.6g\n", wl)
	}
	s += `configs:
  - id: cfg1
    active: true
    surfaces:
      - {id: 1, type: sphere, radius: 50.0, thickness: 5.0, material: N-BK7, diameter: 30.0}
      - {id: 2, type: sphere, radius: -50.0, thickness: 100.0, material: AIR, diameter: 30.0}
      - {id: 3, type: sphere, radius: 0, thickness: 0, material: AIR, diameter: 30.0}
`
	return s
}

// traceInput returns the singlet with a rays section. lenient sets
// rays.lenient.
func traceInput(lenient bool) string {
	s := `glass_catalog:
  entries:
    - {type: model, name: N-BK7, nd: 1.5168, vd: 64.17}
rays:
`
	if lenient {
		s += "  lenient: true\n"
	}
	s += `  rays:
    - id: r1
      wavelength: 0.0005876
      initial: {origin: [0, 0, -100], direction: [0, 0, 1]}
      path: [0, 1, 2, 3]
configs:
  - id: cfg1
    active: true
    surfaces:
      - {id: 1, type: sphere, radius: 50.0, thickness: 5.0, material: N-BK7, diameter: 30.0}
      - {id: 2, type: sphere, radius: -50.0, thickness: 100.0, material: AIR, diameter: 30.0}
      - {id: 3, type: sphere, radius: 0, thickness: 0, material: AIR, diameter: 30.0}
`
	return s
}

// TestGlassDirWriteBackAcrossCommands verifies the CLI/YAML rule principle 3:
// --glass-dir is echoed into glass_catalog.directory by every pipeline
// subcommand that accepts it.
func TestGlassDirWriteBackAcrossCommands(t *testing.T) {
	dir := t.TempDir()

	check := func(t *testing.T, name string, out []byte) {
		t.Helper()
		var res types.Input
		if err := yaml.Unmarshal(out, &res); err != nil {
			t.Fatalf("%s: unmarshal output: %v", name, err)
		}
		got := ""
		if res.GlassCatalog != nil {
			got = res.GlassCatalog.Directory
		}
		if got != dir {
			t.Errorf("%s: glass_catalog.directory = %q, want %q", name, got, dir)
		}
	}

	// scale (paraxial only, fast).
	out := runCommand(t, []string{"rayweave", "scale", "--efl", "40", "--glass-dir", dir},
		func() { runScale([]byte(singletYAML)) })
	check(t, "scale", out)

	// chief (small grid).
	out = runCommand(t, []string{"rayweave", "chief", "--glass-dir", dir},
		func() { runChief([]byte(chiefInput(0))) })
	check(t, "chief", out)

	// trace (single ray).
	out = runCommand(t, []string{"rayweave", "trace", "--glass-dir", dir},
		func() { runTrace([]byte(traceInput(false))) })
	check(t, "trace", out)

	// paraxial.
	out = runCommand(t, []string{"rayweave", "paraxial", "--glass-dir", dir},
		func() { runParaxial([]byte(singletYAML)) })
	check(t, "paraxial", out)
}

// TestGlassDirNotWrittenWithoutFlag verifies an unset --glass-dir never
// injects glass_catalog.directory (even when the catalog is present).
func TestGlassDirNotWrittenWithoutFlag(t *testing.T) {
	out := runCommand(t, []string{"rayweave", "scale", "--efl", "40"},
		func() { runScale([]byte(singletYAML)) })
	var res types.Input
	if err := yaml.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if res.GlassCatalog != nil && res.GlassCatalog.Directory != "" {
		t.Errorf("glass_catalog.directory = %q, want empty (no --glass-dir given)", res.GlassCatalog.Directory)
	}
}

// TestWriteBackGlassDirHelper covers the helper directly: it is a no-op for an
// empty dir and creates the catalog section when needed.
func TestWriteBackGlassDirHelper(t *testing.T) {
	var in types.Input
	writeBackGlassDir(&in, "")
	if in.GlassCatalog != nil {
		t.Errorf("empty dir must not create a glass_catalog section")
	}

	in = types.Input{GlassCatalog: &types.GlassCatalog{Entries: []types.Glass{{}}}}
	writeBackGlassDir(&in, "/tmp/glass")
	if in.GlassCatalog.Directory != "/tmp/glass" {
		t.Errorf("directory = %q, want /tmp/glass", in.GlassCatalog.Directory)
	}

	in = types.Input{}
	writeBackGlassDir(&in, "/tmp/glass")
	if in.GlassCatalog == nil || in.GlassCatalog.Directory != "/tmp/glass" {
		t.Errorf("must create glass_catalog with directory, got %+v", in.GlassCatalog)
	}
}

// TestChiefWavelengthWriteBack verifies --wl wins over YAML and is echoed into
// chief.wavelength; an unset flag never injects the default.
func TestChiefWavelengthWriteBack(t *testing.T) {
	// Flag set: written back.
	out := runCommand(t, []string{"rayweave", "chief", "--wl", "0.00052"},
		func() { runChief([]byte(chiefInput(0))) })
	var res types.Input
	if err := yaml.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if res.Chief == nil || math.Abs(res.Chief.Wavelength-0.00052) > 1e-9 {
		got := 0.0
		if res.Chief != nil {
			got = res.Chief.Wavelength
		}
		t.Errorf("chief.wavelength = %v, want 0.00052", got)
	}
	res = types.Input{}

	// YAML value respected when the flag is unset.
	out = runCommand(t, []string{"rayweave", "chief"},
		func() { runChief([]byte(chiefInput(0.00062))) })
	if err := yaml.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if res.Chief == nil || math.Abs(res.Chief.Wavelength-0.00062) > 1e-9 {
		got := 0.0
		if res.Chief != nil {
			got = res.Chief.Wavelength
		}
		t.Errorf("chief.wavelength = %v, want YAML value 0.00062", got)
	}
	res = types.Input{}

	// Neither flag nor YAML: nothing injected (omitempty -> 0).
	out = runCommand(t, []string{"rayweave", "chief"},
		func() { runChief([]byte(chiefInput(0))) })
	if err := yaml.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if res.Chief == nil || res.Chief.Wavelength != 0 {
		got := -1.0
		if res.Chief != nil {
			got = res.Chief.Wavelength
		}
		t.Errorf("chief.wavelength = %v, want 0 (not injected)", got)
	}
}

// TestTraceLenientWriteBack verifies --lenient is echoed into rays.lenient and
// the YAML value is honoured when the flag is unset.
func TestTraceLenientWriteBack(t *testing.T) {
	// Flag set: written back true.
	out := runCommand(t, []string{"rayweave", "trace", "--lenient"},
		func() { runTrace([]byte(traceInput(false))) })
	var res types.Input
	if err := yaml.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if res.Rays == nil || !res.Rays.Lenient {
		t.Errorf("rays.lenient = false, want true (--lenient)")
	}
	res = types.Input{}

	// YAML lenient: true honoured without the flag.
	out = runCommand(t, []string{"rayweave", "trace"},
		func() { runTrace([]byte(traceInput(true))) })
	if err := yaml.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if res.Rays == nil || !res.Rays.Lenient {
		t.Errorf("rays.lenient = false, want true (from YAML)")
	}
	res = types.Input{}

	// Neither: not injected.
	out = runCommand(t, []string{"rayweave", "trace"},
		func() { runTrace([]byte(traceInput(false))) })
	if err := yaml.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if res.Rays == nil || res.Rays.Lenient {
		t.Errorf("rays.lenient = true, want false (not injected)")
	}
}

// TestScaleEFLYAMLAndWriteBack verifies scale.efl: --efl wins over YAML and the
// effective value is echoed back; a YAML-only value is used and preserved.
func TestScaleEFLYAMLAndWriteBack(t *testing.T) {
	eflOf := func(out []byte) float64 {
		var res types.Input
		if err := yaml.Unmarshal(out, &res); err != nil {
			t.Fatalf("unmarshal output: %v", err)
		}
		if res.Scale == nil || res.Scale.EFL <= 0 {
			t.Fatalf("scale.efl missing in output: %+v", res.Scale)
		}
		gc := glass.NewCatalog()
		gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
		surface.Precompute(res.Configs[0].Surfaces)
		return paraxial.Compute(types.System{Surfaces: res.Configs[0].Surfaces}, 0.0005876, gc, 0, nil).FocalLength
	}

	// YAML-only target: used and preserved.
	yamlScale := singletYAML + "scale:\n  efl: 40\n"
	out := runCommand(t, []string{"rayweave", "scale"}, func() { runScale([]byte(yamlScale)) })
	if got := eflOf(out); math.Abs(got-40.0) > 1e-3 {
		t.Errorf("EFL = %v, want 40 (scale.efl from YAML)", got)
	}

	// Flag + YAML: flag wins, echoed back.
	yamlScale = singletYAML + "scale:\n  efl: 20\n"
	out = runCommand(t, []string{"rayweave", "scale", "--efl", "40"}, func() { runScale([]byte(yamlScale)) })
	if got := eflOf(out); math.Abs(got-40.0) > 1e-3 {
		t.Errorf("EFL = %v, want 40 (--efl wins over scale.efl)", got)
	}

	// Flag only: written back into a new scale section.
	out = runCommand(t, []string{"rayweave", "scale", "--efl", "40"}, func() { runScale([]byte(singletYAML)) })
	var res types.Input
	if err := yaml.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if res.Scale == nil || math.Abs(res.Scale.EFL-40) > 1e-9 {
		t.Errorf("scale.efl = %+v, want 40 written back", res.Scale)
	}
}

// TestVignetteYAMLAndWriteBack verifies the vignette: YAML section drives the
// run when flags are unset and that --iterations wins and is echoed back.
func TestVignetteYAMLAndWriteBack(t *testing.T) {
	input := `glass_catalog:
  entries:
    - {type: model, name: N-BK7, nd: 1.5168, vd: 64.17}
chief:
  fields:
    - {angle: 0, direction: [0, 1]}
  reference_surface: 3
  num_rays: 24
  grid_type: hex
vignette:
  iterations: 1
  min_glass_path: 0.3
  margin_mm: 0.1
configs:
  - id: cfg1
    active: true
    surfaces:
      - {id: 1, type: sphere, radius: 50.0, thickness: 5.0, material: N-BK7, diameter: 20.0, auto_aperture: true}
      - {id: 2, type: sphere, radius: -50.0, thickness: 100.0, material: AIR, diameter: 20.0, auto_aperture: true}
      - {id: 3, type: sphere, radius: 0, thickness: 0, material: AIR, diameter: 30.0}
`

	// YAML section drives the run.
	out := runCommand(t, []string{"rayweave", "vignette"}, func() { runVignette([]byte(input)) })
	var res types.Output
	if err := yaml.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if res.Vignette == nil || res.Vignette.Iterations != 1 || res.Vignette.MinGlassPath != 0.3 || res.Vignette.MarginMM != 0.1 {
		t.Errorf("vignette section = %+v, want iterations 1 / min_glass_path 0.3 / margin_mm 0.1", res.Vignette)
	}
	if res.Vignetting == nil || res.Vignetting.Iterations != 1 {
		t.Errorf("vignetting_result.iterations = %+v, want 1", res.Vignetting)
	}

	// Flag wins and is echoed back.
	out = runCommand(t, []string{"rayweave", "vignette", "--iterations", "2"}, func() { runVignette([]byte(input)) })
	if err := yaml.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if res.Vignette == nil || res.Vignette.Iterations != 2 {
		t.Errorf("vignette.iterations = %+v, want 2 (flag wins and written back)", res.Vignette)
	}
	if res.Vignetting == nil || res.Vignetting.Iterations != 2 {
		t.Errorf("vignetting_result.iterations = %+v, want 2", res.Vignetting)
	}
}

// TestAsphereValidationWriteBack verifies the asphere validation settings are
// resolved flag-first (with --apply implying --validate) and the flag-won
// values are echoed into asphere_candidate.
func TestAsphereValidationWriteBack(t *testing.T) {
	// Flags: analysis + validation settings written back, apply absent.
	args := []string{"rayweave", "asphere",
		"--rings", "2", "--angles", "4", "--pupil-samples", "5",
		"--sensitivity-samples", "0", "--top-k", "1",
		"--validate", "--dls-iter", "2", "--num-rays", "16"}
	out := runCommand(t, args, func() { runAsphere([]byte(chiefInput(0))) })
	var res types.Output
	if err := yaml.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if res.Asphere == nil {
		t.Fatal("asphere_candidate section missing in output")
	}
	if res.Asphere.CellRings != 2 || res.Asphere.TopK != 1 {
		t.Errorf("asphere_candidate cell_rings/top_k = %d/%d, want 2/1", res.Asphere.CellRings, res.Asphere.TopK)
	}
	if res.Asphere.Validate == nil || !*res.Asphere.Validate {
		t.Errorf("asphere_candidate.validate = %+v, want true", res.Asphere.Validate)
	}
	if res.Asphere.ValidationDLSIter != 2 || res.Asphere.ValidationNumRays != 16 {
		t.Errorf("validation_dls_iter/num_rays = %d/%d, want 2/16", res.Asphere.ValidationDLSIter, res.Asphere.ValidationNumRays)
	}
	if res.Asphere.Apply != nil {
		t.Errorf("asphere_candidate.apply = %+v, want absent (no --apply)", res.Asphere.Apply)
	}
	if res.AsphereResult == nil {
		t.Fatal("asphere_candidate_result missing")
	}
	validated := false
	for _, r := range res.AsphereResult.Rankings {
		if r.Validation != nil {
			validated = true
		}
	}
	if !validated {
		t.Error("no ranking carried a validation block (--validate did not run)")
	}

	// YAML section: validate: true honoured without flags.
	yamlCfg := chiefInput(0) + `asphere_candidate:
  candidate_surfaces: [1]
  max_even_order: 8
  sensitivity_samples: 0
  top_k: 1
  validate: true
  validation_dls_iter: 2
  validation_num_rays: 16
`
	out = runCommand(t, []string{"rayweave", "asphere"}, func() { runAsphere([]byte(yamlCfg)) })
	if err := yaml.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if res.Asphere == nil || res.Asphere.Validate == nil || !*res.Asphere.Validate {
		t.Errorf("asphere_candidate.validate = %+v, want true (from YAML)", res.Asphere)
	}
	if res.AsphereResult == nil {
		t.Fatal("asphere_candidate_result missing")
	}
	validated = false
	for _, r := range res.AsphereResult.Rankings {
		if r.Validation != nil {
			validated = true
		}
	}
	if !validated {
		t.Error("YAML validate: true did not run the validation DLS")
	}
}
