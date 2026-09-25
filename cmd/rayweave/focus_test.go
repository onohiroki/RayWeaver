package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
	"gopkg.in/yaml.v3"
)

// focusInput is the singlet with a config-level field list (needed by the
// all-field wavefront back-focus solve) and an optional extra YAML tail. The
// image plane sits at 100 mm while the focal length is ~48 mm, so the
// all-field / per-field best-focus planes differ strongly from the file plane.
func focusInput(extra string) string {
	return `glass_catalog:
  entries:
    - {type: model, name: N-BK7, nd: 1.5168, vd: 64.17}
chief:
  fields:
    - {angle: 0, direction: [0, 1]}
  reference_surface: 3
  num_rays: 24
  grid_type: hex
configs:
  - id: cfg1
    active: true
    fields:
      - {id: 0, angle_deg: 0, weight: 1}
    surfaces:
      - {id: 1, type: sphere, radius: 50.0, thickness: 5.0, material: N-BK7, diameter: 30.0}
      - {id: 2, type: sphere, radius: -50.0, thickness: 100.0, material: AIR, diameter: 30.0}
      - {id: 3, type: sphere, radius: 0, thickness: 0, material: AIR, diameter: 30.0}
` + extra
}

// sameMTFPlane reports whether two MTF plane summaries are field-for-field
// identical (used to assert a solve actually moved the plane).
func sameMTFPlane(a, b *types.FocusMTFPlane) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Strehl != b.Strehl || a.BestFocusShiftMM != b.BestFocusShiftMM ||
		len(a.Sagittal) != len(b.Sagittal) || len(a.Tangential) != len(b.Tangential) {
		return false
	}
	for i := range a.Sagittal {
		if a.Sagittal[i] != b.Sagittal[i] {
			return false
		}
	}
	for i := range a.Tangential {
		if a.Tangential[i] != b.Tangential[i] {
			return false
		}
	}
	return true
}

// TestFocusMTFComparison verifies the `focus mtf` table: the three plane
// blocks, the requested frequency list, the reported all-field shift, and the
// CLI-over-YAML write-back.
func TestFocusMTFComparison(t *testing.T) {
	args := []string{"rayweave", "focus", "mtf", "--frequencies", "20,40", "--num-rays", "24", "--psf-grid", "24"}
	out := runCommand(t, args, func() {
		runFocusMTF([]byte(focusInput("")), []string{"--frequencies", "20,40", "--num-rays", "24", "--psf-grid", "24"})
	})

	var res types.Output
	if err := yaml.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if res.FocusComparison == nil || res.FocusComparison.MTF == nil {
		t.Fatal("focus_comparison.mtf missing")
	}
	m := res.FocusComparison.MTF
	if len(m.Planes) != 3 || m.Planes[0] != "file" || m.Planes[1] != "focus_plane_all" || m.Planes[2] != "best_focus" {
		t.Errorf("planes = %v, want [file focus_plane_all best_focus]", m.Planes)
	}
	if len(m.Frequencies) != 2 || m.Frequencies[0] != 20 || m.Frequencies[1] != 40 {
		t.Errorf("frequencies = %v, want [20 40]", m.Frequencies)
	}
	if m.FocusPlaneAllShiftMM == 0 {
		t.Error("focus_plane_all_shift_mm = 0, want the (nonzero) all-field shift")
	}
	if len(m.Rows) == 0 {
		t.Fatal("rows empty")
	}
	for _, row := range m.Rows {
		for _, pl := range []*types.FocusMTFPlane{row.File, row.FocusPlaneAll, row.BestFocus} {
			if pl == nil {
				t.Fatal("plane block missing for a requested plane")
			}
			if len(pl.Sagittal) != 2 || len(pl.Tangential) != 2 {
				t.Errorf("MTF arrays = %d/%d, want 2/2", len(pl.Sagittal), len(pl.Tangential))
			}
			for _, v := range append(append([]float64{}, pl.Sagittal...), pl.Tangential...) {
				if v < 0 || v > 1.0000001 {
					t.Errorf("MTF value %v out of [0,1]", v)
				}
			}
		}
		// The solved planes must actually differ from the file plane (guards
		// the missing-Precompute regression where the solve silently no-ops).
		if sameMTFPlane(row.FocusPlaneAll, row.File) {
			t.Error("focus_plane_all identical to file: the all-field solve did not move the plane")
		}
		if sameMTFPlane(row.BestFocus, row.File) {
			t.Error("best_focus identical to file: the per-field solve did not move the plane")
		}
		if row.BestFocus.BestFocusShiftMM == 0 {
			t.Error("best_focus_shift_mm = 0, want the per-field best-focus shift")
		}
	}

	// Write-back: the effective values land in focus.mtf.
	if res.Focus == nil || res.Focus.MTF == nil {
		t.Fatal("focus.mtf not written back")
	}
	if len(res.Focus.MTF.Frequencies) != 2 || res.Focus.MTF.Frequencies[0] != 20 {
		t.Errorf("focus.mtf.frequencies = %v, want the CLI list", res.Focus.MTF.Frequencies)
	}
	if len(res.Focus.MTF.Planes) != 3 {
		t.Errorf("focus.mtf.planes = %v, want 3 entries", res.Focus.MTF.Planes)
	}
}

// TestFocusAllPlaneFallbackToChiefFields guards the fallback used when a
// hand-written document declares its fields only in chief.fields: the all-field
// back-focus solve must use them instead of silently no-oping (which left
// focus_plane_all identical to the file plane).
func TestFocusAllPlaneFallbackToChiefFields(t *testing.T) {
	chiefOnly := strings.Replace(focusInput(""),
		"    fields:\n      - {id: 0, angle_deg: 0, weight: 1}\n", "", 1)
	if strings.Contains(chiefOnly, "angle_deg") {
		t.Fatal("test setup: config fields were not removed")
	}
	out := runCommand(t, []string{"rayweave", "focus", "psf", "--planes", "all", "--num-rays", "24", "--psf-grid", "24"},
		func() {
			runFocusPSF([]byte(chiefOnly), []string{"--planes", "all", "--num-rays", "24", "--psf-grid", "24"})
		})
	var res types.Output
	if err := yaml.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	p := res.FocusComparison.PSF
	if p == nil || len(p.Rows) == 0 {
		t.Fatal("focus_comparison.psf missing")
	}
	if p.FocusPlaneAllShiftMM == 0 {
		t.Error("focus_plane_all_shift_mm = 0: the solve did not fall back to chief.fields")
	}
	if p.Rows[0].FocusPlaneAll == nil {
		t.Error("focus_plane_all block missing")
	}
}

// TestFocusPSFComparison verifies `focus psf` reports the PSF metric blocks.
func TestFocusPSFComparison(t *testing.T) {
	args := []string{"rayweave", "focus", "psf", "--num-rays", "24", "--psf-grid", "24"}
	out := runCommand(t, args, func() { runFocusPSF([]byte(focusInput("")), []string{"--num-rays", "24", "--psf-grid", "24"}) })

	var res types.Output
	if err := yaml.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if res.FocusComparison == nil || res.FocusComparison.PSF == nil {
		t.Fatal("focus_comparison.psf missing")
	}
	p := res.FocusComparison.PSF
	if len(p.Rows) == 0 {
		t.Fatal("rows empty")
	}
	row := p.Rows[0]
	if row.File == nil || row.FocusPlaneAll == nil || row.BestFocus == nil {
		t.Fatal("PSF plane blocks missing")
	}
	if row.File.EncircledEnergy50 <= 0 {
		t.Error("encircled_energy_50 not populated")
	}
	if row.FocusPlaneAll.Strehl == row.File.Strehl && row.FocusPlaneAll.FWHMX == row.File.FWHMX {
		t.Error("focus_plane_all identical to file: the all-field solve did not move the plane")
	}
	if row.BestFocus.BestFocusShiftMM == 0 {
		t.Error("best_focus_shift_mm = 0, want the per-field best-focus shift")
	}
	if res.Focus == nil || res.Focus.PSF == nil {
		t.Fatal("focus.psf not written back")
	}
}

// TestFocusPlanesSelection verifies --planes limits the evaluated conventions
// (and that the YAML list is honoured when the flag is absent).
func TestFocusPlanesSelection(t *testing.T) {
	out := runCommand(t, []string{"rayweave", "focus", "psf", "--planes", "all", "--num-rays", "24", "--psf-grid", "24"},
		func() {
			runFocusPSF([]byte(focusInput("")), []string{"--planes", "all", "--num-rays", "24", "--psf-grid", "24"})
		})
	var res types.Output
	if err := yaml.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	p := res.FocusComparison.PSF
	if len(p.Planes) != 1 || p.Planes[0] != "focus_plane_all" {
		t.Fatalf("planes = %v, want [focus_plane_all]", p.Planes)
	}
	row := p.Rows[0]
	if row.File != nil || row.BestFocus != nil {
		t.Error("unrequested plane blocks must be omitted")
	}
	if row.FocusPlaneAll == nil {
		t.Error("focus_plane_all block missing")
	}

	// YAML planes list honoured without a flag.
	yamlCfg := focusInput("focus:\n  psf:\n    planes: [best]\n")
	out = runCommand(t, []string{"rayweave", "focus", "psf", "--num-rays", "24", "--psf-grid", "24"},
		func() {
			runFocusPSF([]byte(yamlCfg), []string{"--num-rays", "24", "--psf-grid", "24"})
		})
	if err := yaml.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if len(res.FocusComparison.PSF.Planes) != 1 || res.FocusComparison.PSF.Planes[0] != "best_focus" {
		t.Errorf("YAML planes not honoured: %v", res.FocusComparison.PSF.Planes)
	}
}

// TestFocusCLIWinsOverYAML verifies the frequency list resolves flag > YAML.
func TestFocusCLIWinsOverYAML(t *testing.T) {
	yamlCfg := focusInput("focus:\n  mtf:\n    frequencies: [10]\n")

	out := runCommand(t, []string{"rayweave", "focus", "mtf", "--num-rays", "24", "--psf-grid", "24"},
		func() { runFocusMTF([]byte(yamlCfg), []string{"--num-rays", "24", "--psf-grid", "24"}) })
	var res types.Output
	if err := yaml.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if got := res.FocusComparison.MTF.Frequencies; len(got) != 1 || got[0] != 10 {
		t.Errorf("YAML frequencies = %v, want [10]", got)
	}

	out = runCommand(t, []string{"rayweave", "focus", "mtf", "--frequencies", "30,60", "--num-rays", "24", "--psf-grid", "24"},
		func() {
			runFocusMTF([]byte(yamlCfg), []string{"--frequencies", "30,60", "--num-rays", "24", "--psf-grid", "24"})
		})
	if err := yaml.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if got := res.FocusComparison.MTF.Frequencies; len(got) != 2 || got[0] != 30 || got[1] != 60 {
		t.Errorf("CLI frequencies = %v, want [30 60] (CLI must win)", got)
	}
}

// TestCleanRemovesFocusComparison verifies the clean command strips the focus
// comparison result section.
func TestCleanRemovesFocusComparison(t *testing.T) {
	focusOut := runCommand(t, []string{"rayweave", "focus", "psf", "--num-rays", "24", "--psf-grid", "24"},
		func() { runFocusPSF([]byte(focusInput("")), []string{"--num-rays", "24", "--psf-grid", "24"}) })

	cleaned := runCommand(t, []string{"rayweave", "clean"}, func() { runClean(focusOut) })
	var res types.Output
	if err := yaml.Unmarshal(cleaned, &res); err != nil {
		t.Fatalf("unmarshal cleaned output: %v", err)
	}
	if res.FocusComparison != nil {
		t.Error("clean must remove focus_comparison")
	}
}

// runListFocus runs `list focus` over a piped pipeline document with the given
// extra list flags, returning stdout.
func runListFocus(t *testing.T, pipeline []byte, extra ...string) []byte {
	t.Helper()
	args := append([]string{"rayweave", "list", "focus"}, extra...)
	return runCommand(t, args, func() { runList(pipeline) })
}

// TestListFocusPSFTable verifies `list focus` renders the piped focus psf
// comparison as a table (the table the focus command used to print to stderr).
func TestListFocusPSFTable(t *testing.T) {
	pipeline := runCommand(t, []string{"rayweave", "focus", "psf", "--num-rays", "24", "--psf-grid", "24"},
		func() { runFocusPSF([]byte(focusInput("")), []string{"--num-rays", "24", "--psf-grid", "24"}) })

	text := string(runListFocus(t, pipeline))
	for _, want := range []string{"focus psf: PSF comparison", "file", "focus_plane_all", "best_focus"} {
		if !strings.Contains(text, want) {
			t.Errorf("list focus table missing %q:\n%s", want, text)
		}
	}
}

// TestListFocusMTFYAML verifies `list focus --format yaml` echoes the
// focus_comparison section under its own key.
func TestListFocusMTFYAML(t *testing.T) {
	pipeline := runCommand(t, []string{"rayweave", "focus", "mtf", "--frequencies", "20,40", "--num-rays", "24", "--psf-grid", "24"},
		func() {
			runFocusMTF([]byte(focusInput("")), []string{"--frequencies", "20,40", "--num-rays", "24", "--psf-grid", "24"})
		})

	out := runListFocus(t, pipeline, "--format", "yaml")
	var res types.Output
	if err := yaml.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal list focus yaml: %v", err)
	}
	if res.FocusComparison == nil || res.FocusComparison.MTF == nil {
		t.Fatal("list focus yaml missing focus_comparison.mtf")
	}
	if got := res.FocusComparison.MTF.Frequencies; len(got) != 2 || got[0] != 20 || got[1] != 40 {
		t.Errorf("frequencies = %v, want [20 40]", got)
	}
}

// TestListFocusJSON verifies `list focus --format json` uses the pipeline key
// names (not Go field names).
func TestListFocusJSON(t *testing.T) {
	pipeline := runCommand(t, []string{"rayweave", "focus", "psf", "--num-rays", "24", "--psf-grid", "24"},
		func() { runFocusPSF([]byte(focusInput("")), []string{"--num-rays", "24", "--psf-grid", "24"}) })

	out := runListFocus(t, pipeline, "--format", "json")
	var res map[string]map[string]any
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal list focus json: %v", err)
	}
	psf, ok := res["focus_comparison"]["psf"].(map[string]any)
	if !ok {
		t.Fatalf("focus_comparison.psf missing/wrong type: %s", string(out))
	}
	if _, ok := psf["focus_plane_all_shift_mm"]; !ok {
		t.Errorf("json missing focus_plane_all_shift_mm key: %s", string(out))
	}
	if _, ok := psf["planes"]; !ok {
		t.Errorf("json uses Go field names (no planes key): %s", string(out))
	}
}

// TestListFocusCSV verifies the flattened CSV shape: one row per (row, plane)
// with one sagittal/tangential pair per frequency.
func TestListFocusCSV(t *testing.T) {
	pipeline := runCommand(t, []string{"rayweave", "focus", "mtf", "--frequencies", "20,40", "--num-rays", "24", "--psf-grid", "24"},
		func() {
			runFocusMTF([]byte(focusInput("")), []string{"--frequencies", "20,40", "--num-rays", "24", "--psf-grid", "24"})
		})

	text := string(runListFocus(t, pipeline, "--format", "csv"))
	if !strings.Contains(text, "Focus Comparison (mtf):") {
		t.Errorf("csv missing section header:\n%s", text)
	}
	for _, want := range []string{"mtf_sagittal_20", "mtf_tangential_20", "mtf_sagittal_40", "best_focus"} {
		if !strings.Contains(text, want) {
			t.Errorf("csv missing %q:\n%s", want, text)
		}
	}
}

// TestListFocusEmpty verifies `list focus` handles a pipeline document without
// a focus_comparison section (placeholder, no crash).
func TestListFocusEmpty(t *testing.T) {
	text := string(runListFocus(t, []byte(focusInput(""))))
	if !strings.Contains(text, "no focus comparison") {
		t.Errorf("expected the empty placeholder, got:\n%s", text)
	}
}

// TestListAllIncludesFocus verifies the "all" keyword expands to the focus
// target.
func TestListAllIncludesFocus(t *testing.T) {
	pipeline := runCommand(t, []string{"rayweave", "focus", "psf", "--num-rays", "24", "--psf-grid", "24"},
		func() { runFocusPSF([]byte(focusInput("")), []string{"--num-rays", "24", "--psf-grid", "24"}) })

	text := string(runCommand(t, []string{"rayweave", "list", "all"}, func() { runList(pipeline) }))
	if !strings.Contains(text, "focus psf: PSF comparison") {
		t.Error("list all did not include the focus target")
	}
}
