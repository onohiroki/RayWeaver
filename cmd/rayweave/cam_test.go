package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
	"gopkg.in/yaml.v3"
)

// camSurfaceYAML is a three-element layout with three airspaces (after surfaces
// 2, 4 and 6) that can act as cam variables and the image plane on surface 7.
// With the focus on its plane (Z = 43) the cam can reach EFL 21.74 .. 26.51, so
// the targets below sit inside that band.
const camSurfaceYAML = `      - {id: 1, type: sphere, radius: 50.0, thickness: 6.0, material: {nd: 1.62, vd: 36.0}, diameter: 40.0}
      - {id: 2, type: sphere, radius: -100.0, thickness: 12.0, material: AIR, diameter: 40.0}
      - {id: 3, type: sphere, radius: 33.333333, thickness: 5.0, material: {nd: 1.52, vd: 64.0}, diameter: 40.0}
      - {id: 4, type: sphere, radius: -50.0, thickness: 10.0, material: AIR, diameter: 40.0}
      - {id: 5, type: sphere, radius: 66.666667, thickness: 6.0, material: {nd: 1.62, vd: 36.0}, diameter: 40.0}
      - {id: 6, type: sphere, radius: -33.333333, thickness: 4.0, material: AIR, diameter: 40.0}
      - {id: 7, type: sphere, radius: 0.0, thickness: 0.0, material: AIR, diameter: 40.0}
`

const (
	camPlaneZ     = 43.0
	camTargetWide = 22.8
	camTargetMid  = 24.2
	camTargetTele = 25.7
)

// camConfigYAML renders one position of the fixture.
func camConfigYAML(id string) string {
	return fmt.Sprintf(`  - id: %s
    active: true
    fields:
      - {id: 0, angle_deg: 0.0, weight: 1.0}
    wavelengths:
      - {id: 0, value: 0.00058756, weight: 1.0}
    surfaces:
%s`, id, camSurfaceYAML)
}

// camConfigsYAML is the three-position block of the fixture.
func camConfigsYAML() string {
	return "configs:\n" + camConfigYAML("wide") + camConfigYAML("mid") + camConfigYAML("tele")
}

// camDocYAML is the fixture document.
func camDocYAML() string {
	return camConfigsYAML() + fmt.Sprintf(`cam:
  surfaces: [2, 4, 6]
  targets:
    - {config: wide, efl: %g}
    - {config: mid, efl: %g}
    - {config: tele, efl: %g}
`, camTargetWide, camTargetMid, camTargetTele)
}

// TestCamCLIHelper is the subprocess entry point (same pattern as
// TestQueryCLIHelper): the parent invokes the test binary with -test.run and the
// argument list in the environment, feeds the document on stdin and lets main()
// write the pipeline document to stdout.
func TestCamCLIHelper(t *testing.T) {
	if os.Getenv("GO_CAM_HELPER") != "1" {
		return
	}
	var args []string
	_ = json.Unmarshal([]byte(os.Getenv("GO_CAM_ARGS")), &args)
	os.Args = append([]string{"rayweave", "cam"}, args...)
	main()
	os.Exit(0)
}

// runCamCLI runs `rayweave cam` with args on stdin and returns stdout, stderr
// and the exit code.
func runCamCLI(t *testing.T, stdin string, args ...string) (string, string, int) {
	t.Helper()
	rawArgs, _ := json.Marshal(args)
	cmd := exec.Command(os.Args[0], "-test.run=^TestCamCLIHelper$")
	cmd.Env = append(os.Environ(), "GO_CAM_HELPER=1", "GO_CAM_ARGS="+string(rawArgs))
	cmd.Stdin = strings.NewReader(stdin)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("cam subprocess error: %v", err)
	}
	return out.String(), errBuf.String(), code
}

// runCamOK runs the command and fails on a non-zero exit.
func runCamOK(t *testing.T, stdin string, args ...string) (string, string) {
	t.Helper()
	out, stderr, code := runCamCLI(t, stdin, args...)
	if code != 0 {
		t.Fatalf("rayweave cam %v exited %d\nstderr:\n%s", args, code, stderr)
	}
	return out, stderr
}

// parseCamOutput parses a cam output document.
func parseCamOutput(t *testing.T, out string) *types.Output {
	t.Helper()
	var parsed types.Output
	if err := yaml.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("cam output does not parse: %v\n%s", err, out)
	}
	if parsed.CamResult == nil {
		t.Fatalf("no cam_result section in output:\n%s", out)
	}
	return &parsed
}

// camThicknessOf returns one config's surface thickness.
func camThicknessOf(t *testing.T, cfg types.Config, id int) float64 {
	t.Helper()
	for _, s := range cfg.Surfaces {
		if s.ID == id {
			return s.Thickness
		}
	}
	t.Fatalf("config %q has no surface %d", cfg.ID, id)
	return 0
}

// TestCamSolvesEveryPositionOntoTheFixedPlane is the core behaviour: three
// positions, three target EFLs, one image plane. Each position must reach its
// target with the paraxial focus on the plane, the plane must not move, and the
// solved airspaces must be written back into the configs.
func TestCamSolvesEveryPositionOntoTheFixedPlane(t *testing.T) {
	out, _ := runCamOK(t, camDocYAML())
	parsed := parseCamOutput(t, out)
	res := parsed.CamResult

	if !res.Converged {
		t.Errorf("cam_result.converged = false; %s", out)
	}
	if math.Abs(res.ImagePlaneZ-camPlaneZ) > 1e-9 {
		t.Errorf("cam_result.image_plane_z = %v, want %v", res.ImagePlaneZ, camPlaneZ)
	}
	if len(res.Configs) != 3 {
		t.Fatalf("cam_result.configs = %d rows, want 3", len(res.Configs))
	}

	want := map[string]float64{"wide": camTargetWide, "mid": camTargetMid, "tele": camTargetTele}
	seen := 0
	for _, c := range res.Configs {
		target, ok := want[c.Config]
		if !ok {
			t.Fatalf("unexpected config row %q", c.Config)
		}
		seen++
		if math.Abs(c.EFLTarget-target) > 1e-12 {
			t.Errorf("%s: efl_target %v, want %v", c.Config, c.EFLTarget, target)
		}
		if math.Abs(c.EFL-target) > 1e-6 {
			t.Errorf("%s: efl %v, want the target %v", c.Config, c.EFL, target)
		}
		for name, r := range map[string]float64{
			"residual_efl_mm":         c.ResidualEFL,
			"residual_focus_mm":       c.ResidualFocus,
			"residual_image_plane_mm": c.ResidualImagePlane,
		} {
			if math.Abs(r) > 1e-6 {
				t.Errorf("%s: %s = %g, want ~0", c.Config, name, r)
			}
		}
		if math.Abs(c.ImagePlaneZ-camPlaneZ) > 1e-6 {
			t.Errorf("%s: image_plane_z %v, want the fixed %v (one detector)", c.Config, c.ImagePlaneZ, camPlaneZ)
		}
		if math.Abs(c.FocusZ-camPlaneZ) > 1e-6 {
			t.Errorf("%s: focus_z %v is not on the image plane %v", c.Config, c.FocusZ, camPlaneZ)
		}
		if !c.Converged {
			t.Errorf("%s: converged = false", c.Config)
		}
		if len(c.Airspaces) != 3 {
			t.Fatalf("%s: %d airspaces, want 3", c.Config, len(c.Airspaces))
		}
	}
	if seen != 3 {
		t.Errorf("checked %d config rows, want 3", seen)
	}

	// The solved airspaces are written back into configs[].surfaces[].
	byID := map[string]types.Config{}
	for _, c := range parsed.Configs {
		byID[c.ID] = c
	}
	for _, c := range res.Configs {
		cfg := byID[c.Config]
		for _, a := range c.Airspaces {
			if got := camThicknessOf(t, cfg, a.Surface); math.Abs(got-a.To) > 1e-12 {
				t.Errorf("%s: surface %d thickness %v != solved %v", c.Config, a.Surface, got, a.To)
			}
			if math.Abs(a.Delta-(a.To-a.From)) > 1e-15 {
				t.Errorf("%s: surface %d delta %v != to-from %v", c.Config, a.Surface, a.Delta, a.To-a.From)
			}
		}
	}
	// The `from` column is the input layout.
	for _, c := range res.Configs {
		wantFrom := map[int]float64{2: 12, 4: 10, 6: 4}
		for _, a := range c.Airspaces {
			if math.Abs(a.From-wantFrom[a.Surface]) > 1e-12 {
				t.Errorf("%s: surface %d from %v, want the input %v", c.Config, a.Surface, a.From, wantFrom[a.Surface])
			}
		}
	}

	// The cam curve is reported as a table, one row per variable, in config
	// order, with no fitting.
	if len(res.Strokes) != 3 {
		t.Fatalf("strokes = %d rows, want 3", len(res.Strokes))
	}
	for _, st := range res.Strokes {
		if len(st.Values) != 3 {
			t.Errorf("stroke for surface %d has %d values, want 3", st.Surface, len(st.Values))
		}
		if st.Values[0].Config != "wide" || st.Values[2].Config != "tele" {
			t.Errorf("stroke for surface %d is not in config order: %+v", st.Surface, st.Values)
		}
		for _, v := range st.Values {
			for _, c := range res.Configs {
				if c.Config != v.Config {
					continue
				}
				for _, a := range c.Airspaces {
					if a.Surface == st.Surface && math.Abs(a.To-v.Thickness) > 1e-15 {
						t.Errorf("stroke %d/%s = %v, airspace reports %v", st.Surface, v.Config, v.Thickness, a.To)
					}
				}
			}
		}
	}

	// The resolved plane settings are recorded in the cam section.
	if parsed.Cam.ImagePlane != 7 {
		t.Errorf("cam.image_plane = %d, want the resolved 7", parsed.Cam.ImagePlane)
	}
	if math.Abs(parsed.Cam.ImagePlaneZ-camPlaneZ) > 1e-9 {
		t.Errorf("cam.image_plane_z = %v, want the resolved %v", parsed.Cam.ImagePlaneZ, camPlaneZ)
	}
	if parsed.Cam.HoldImagePlane == nil || !*parsed.Cam.HoldImagePlane {
		t.Errorf("cam.hold_image_plane = %v, want the default true written back", parsed.Cam.HoldImagePlane)
	}
}

// TestCamIsIdempotentOnItsOwnOutput pins the minimum-motion contract: a
// document that already satisfies the conditions must not be moved at all.
func TestCamIsIdempotentOnItsOwnOutput(t *testing.T) {
	first, _ := runCamOK(t, camDocYAML())
	second, _ := runCamOK(t, first)

	a, b := parseCamOutput(t, first), parseCamOutput(t, second)
	for i := range a.CamResult.Configs {
		ca, cb := a.CamResult.Configs[i], b.CamResult.Configs[i]
		for k := range ca.Airspaces {
			if math.Abs(ca.Airspaces[k].To-cb.Airspaces[k].To) > 1e-9 {
				t.Errorf("%s surface %d: re-solving moved the airspace %v -> %v",
					ca.Config, ca.Airspaces[k].Surface, ca.Airspaces[k].To, cb.Airspaces[k].To)
			}
			if math.Abs(cb.Airspaces[k].Delta) > 1e-9 {
				t.Errorf("%s surface %d: re-solving reports a delta of %v on a solved document",
					cb.Config, cb.Airspaces[k].Surface, cb.Airspaces[k].Delta)
			}
		}
	}
}

// TestCamSelectorAndFlagWriteBack checks the two CLI conventions the command
// shares with the pipeline: --config selects one position (and is written
// nowhere), while --wl / --max-iter / --tolerance are written back into the
// sections they mirror.
func TestCamSelectorAndFlagWriteBack(t *testing.T) {
	out, _ := runCamOK(t, camDocYAML(), "--config", "tele", "--wl", "0.00055",
		"--max-iter", "37", "--tolerance", "1e-8")
	parsed := parseCamOutput(t, out)

	if len(parsed.CamResult.Configs) != 1 || parsed.CamResult.Configs[0].Config != "tele" {
		t.Fatalf("--config tele selected %+v", parsed.CamResult.Configs)
	}
	if math.Abs(parsed.CamResult.Wavelength-0.00055) > 1e-15 {
		t.Errorf("cam_result.wavelength = %v, want 0.00055", parsed.CamResult.Wavelength)
	}
	if parsed.Chief.ReferenceWavelength != 0.00055 {
		t.Errorf("chief.reference_wavelength = %v, want the flag's 0.00055", parsed.Chief.ReferenceWavelength)
	}
	if parsed.Cam.MaxIterations != 37 {
		t.Errorf("cam.max_iterations = %d, want the flag's 37", parsed.Cam.MaxIterations)
	}
	if math.Abs(parsed.Cam.Tolerance-1e-8) > 1e-18 {
		t.Errorf("cam.tolerance = %v, want the flag's 1e-8", parsed.Cam.Tolerance)
	}

	// The positions that were not solved keep their input airspaces.
	byID := map[string]types.Config{}
	for _, c := range parsed.Configs {
		byID[c.ID] = c
	}
	for _, id := range []string{"wide", "mid"} {
		if got := camThicknessOf(t, byID[id], 2); math.Abs(got-12) > 1e-12 {
			t.Errorf("%s: surface 2 moved to %v although it was not selected", id, got)
		}
		if got := camThicknessOf(t, byID[id], 6); math.Abs(got-4) > 1e-12 {
			t.Errorf("%s: surface 6 moved to %v although it was not selected", id, got)
		}
	}
}

// TestCamDoesNotInjectDefaults checks the write-back rule: a run without flags
// must not add solver settings the document did not carry, while the resolved
// image plane (a value read out of the document, not a built-in default) is
// recorded.
func TestCamDoesNotInjectDefaults(t *testing.T) {
	out, _ := runCamOK(t, camDocYAML())
	parsed := parseCamOutput(t, out)

	for _, field := range []string{"max_iterations", "tolerance", "motion_weight"} {
		if strings.Contains(out, field) {
			t.Errorf("cam.%s appeared in a run that never set it:\n%s", field, out)
		}
	}
	if parsed.Cam.MaxIterations != 0 || parsed.Cam.Tolerance != 0 {
		t.Errorf("cam solver settings = %d / %v, want the document's zero values",
			parsed.Cam.MaxIterations, parsed.Cam.Tolerance)
	}
	if math.Abs(parsed.Cam.ImagePlaneZ-camPlaneZ) > 1e-9 {
		t.Errorf("cam.image_plane_z = %v, want the resolved %v", parsed.Cam.ImagePlaneZ, camPlaneZ)
	}
}

// TestCamReportsAnUnreachableTarget: a target outside what the cam surface set
// can reach on the held plane is a warning, not a false success — the residuals
// are reported and converged stays false, and the exit code is still 0 so the
// pipeline can keep the best layout found.
func TestCamReportsAnUnreachableTarget(t *testing.T) {
	doc := strings.Replace(camDocYAML(), "{config: tele, efl: 25.7}", "{config: tele, efl: 40.0}", 1)
	if doc == camDocYAML() {
		t.Fatal("fixture changed: the tele target was not replaced")
	}
	out, stderr, code := runCamCLI(t, doc)
	if code != 0 {
		t.Fatalf("exit code %d, want 0 (non-convergence is a warning)\nstderr:\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "did not converge for tele") {
		t.Errorf("stderr does not warn about the unreachable target:\n%s", stderr)
	}
	parsed := parseCamOutput(t, out)
	if parsed.CamResult.Converged {
		t.Errorf("cam_result.converged = true although tele is out of reach")
	}
	for _, c := range parsed.CamResult.Configs {
		if c.Config != "tele" {
			continue
		}
		if c.Converged {
			t.Errorf("tele: converged = true, want false")
		}
		if math.Abs(c.ResidualEFL) < 1 {
			t.Errorf("tele: residual_efl_mm = %g, want the miss reported", c.ResidualEFL)
		}
	}
}

// TestCamRejectsBadDocuments covers the malformed inputs: each is an error
// (exit 1) that names what is wrong, not a silent non-converged solve.
func TestCamRejectsBadDocuments(t *testing.T) {
	cases := []struct {
		name  string
		doc   string
		args  []string
		wants string
	}{
		{
			name:  "no cam section",
			doc:   strings.Replace(camDocYAML(), "cam:\n", "notcam:\n", 1),
			wants: "cam.surfaces",
		},
		{
			name:  "no targets",
			doc:   camConfigsYAML() + "cam:\n  surfaces: [2, 4, 6]\n",
			wants: "cam.targets",
		},
		{
			name: "missing target for a position",
			doc: camConfigsYAML() + "cam:\n  surfaces: [2, 4, 6]\n  targets:\n" +
				"    - {config: wide, efl: 22.8}\n",
			wants: "missing an EFL for mid, tele",
		},
		{
			name:  "unknown cam surface",
			doc:   strings.Replace(camDocYAML(), "surfaces: [2, 4, 6]", "surfaces: [2, 4, 99]", 1),
			wants: "cam surface 99",
		},
		{
			name:  "image plane not last",
			doc:   strings.Replace(camDocYAML(), "cam:\n", "cam:\n  image_plane: 6\n", 1),
			wants: "must be the last surface",
		},
		{
			name:  "non-positive target",
			doc:   strings.Replace(camDocYAML(), "{config: wide, efl: 22.8}", "{config: wide, efl: 0}", 1),
			wants: "efl must be positive",
		},
		{
			name:  "zero iterations",
			doc:   camDocYAML(),
			args:  []string{"--max-iter", "0"},
			wants: "--max-iter must be positive",
		},
		{
			name:  "zero tolerance",
			doc:   camDocYAML(),
			args:  []string{"--tolerance", "0"},
			wants: "--tolerance must be positive",
		},
		{
			name:  "unknown config",
			doc:   camDocYAML(),
			args:  []string{"--config", "nope"},
			wants: "nope",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := runCamCLI(t, tc.doc, tc.args...)
			if code != 1 {
				t.Fatalf("exit code %d, want 1\nstderr:\n%s", code, stderr)
			}
			if !strings.Contains(stderr, tc.wants) {
				t.Errorf("stderr does not mention %q:\n%s", tc.wants, stderr)
			}
		})
	}
}
