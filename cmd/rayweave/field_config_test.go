package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
	"gopkg.in/yaml.v3"
)

// perConfigFieldsInput declares a different field per position and no
// top-level chief.fields, so the selected configuration alone decides which
// field angle the reporting commands analyse.
const perConfigFieldsInput = `chief:
  stop_surface: 1
glass_catalog:
  entries:
    - {type: model, name: N-BK7, nd: 1.5168, vd: 64.17}
configs:
  - id: wide
    active: true
    fields:
      - {id: 0, angle_deg: 2, weight: 1}
    wavelengths:
      - {id: 0, value: 0.00058756, weight: 1}
    surfaces:
      - {id: 1, type: sphere, radius: 50.0, thickness: 5.0, material: N-BK7, diameter: 40.0}
      - {id: 2, type: sphere, radius: -50.0, thickness: 100.0, material: AIR, diameter: 40.0}
      - {id: 3, type: sphere, radius: 0.0, thickness: 0.0, material: AIR, diameter: 40.0}
  - id: tele
    active: true
    fields:
      - {id: 0, angle_deg: 12, weight: 1}
    wavelengths:
      - {id: 0, value: 0.00058756, weight: 1}
    surfaces:
      - {id: 1, type: sphere, radius: 50.0, thickness: 5.0, material: N-BK7, diameter: 40.0}
      - {id: 2, type: sphere, radius: -50.0, thickness: 100.0, material: AIR, diameter: 40.0}
      - {id: 3, type: sphere, radius: 0.0, thickness: 0.0, material: AIR, diameter: 40.0}
`

// TestFieldConfigCLIHelper re-executes the test binary as the rayweave CLI.
// The reporting commands exit non-zero on an empty field list, which would end
// the test process if they ran in process.
func TestFieldConfigCLIHelper(t *testing.T) {
	if os.Getenv("GO_FIELD_CONFIG_HELPER") != "1" {
		return
	}
	var args []string
	_ = json.Unmarshal([]byte(os.Getenv("GO_FIELD_CONFIG_ARGS")), &args)
	os.Args = append([]string{"rayweave"}, args...)
	main()
	os.Exit(0)
}

// runFieldConfigCLI runs one subcommand on input in a subprocess.
func runFieldConfigCLI(t *testing.T, input string, args ...string) string {
	t.Helper()
	raw, _ := json.Marshal(args)
	cmd := exec.Command(os.Args[0], "-test.run=^TestFieldConfigCLIHelper$")
	cmd.Env = append(os.Environ(), "GO_FIELD_CONFIG_HELPER=1", "GO_FIELD_CONFIG_ARGS="+string(raw))
	cmd.Stdin = strings.NewReader(input)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("rayweave %v failed: %v\nstderr:\n%s", args, err, errBuf.String())
	}
	return out.String()
}

// TestReportingCommandsUseSelectedConfigFields pins the shared field-resolution
// order: psf, wavefront and focus resolve their fields from the --config
// selected position (then configs[0], then chief), exactly like chief,
// vignette, asphere and list fields. They used to read chief.fields only while
// taking the surfaces from the selected config, so a zoom document was analysed
// with another position's field angles.
func TestReportingCommandsUseSelectedConfigFields(t *testing.T) {
	t.Run("psf", func(t *testing.T) {
		for _, tc := range []struct {
			config string
			want   float64
		}{
			{"", 2},      // no --config: configs[0]
			{"wide", 2},  // explicit first position
			{"tele", 12}, // explicit second position
		} {
			args := []string{"psf", "--psf-grid", "16", "--num-rays", "32", "--converge-check", "false"}
			if tc.config != "" {
				args = append(args, "--config", tc.config)
			}
			doc := runFieldConfigCLI(t, perConfigFieldsInput, args...)
			var out struct {
				PSFResults []types.PSFResult `yaml:"psf_results"`
			}
			if err := yaml.Unmarshal([]byte(doc), &out); err != nil {
				t.Fatalf("unmarshal psf output: %v\n%s", err, doc)
			}
			if len(out.PSFResults) != 1 {
				t.Fatalf("psf --config %q: %d results, want 1\n%s", tc.config, len(out.PSFResults), doc)
			}
			if got := out.PSFResults[0].FieldAngle; got != tc.want {
				t.Fatalf("psf --config %q: field_angle = %v, want %v\n%s", tc.config, got, tc.want, doc)
			}
		}
	})

	t.Run("wavefront", func(t *testing.T) {
		doc := runFieldConfigCLI(t, perConfigFieldsInput, "wavefront", "--config", "tele", "--num-rays", "64")
		var out struct {
			Wavefront *types.WavefrontResult `yaml:"wavefront_result"`
		}
		if err := yaml.Unmarshal([]byte(doc), &out); err != nil {
			t.Fatalf("unmarshal wavefront output: %v\n%s", err, doc)
		}
		if out.Wavefront == nil || len(out.Wavefront.Fields) != 1 {
			t.Fatalf("wavefront --config tele: want 1 field result\n%s", doc)
		}
		if got := out.Wavefront.Fields[0].FieldAngle; got != 12 {
			t.Fatalf("wavefront --config tele: field_angle = %v, want 12", got)
		}
	})

	t.Run("focus spot", func(t *testing.T) {
		doc := runFieldConfigCLI(t, perConfigFieldsInput, "focus", "spot", "--config", "tele", "--num-rays", "64")
		var out struct {
			Focus *types.FocusComparison `yaml:"focus_comparison"`
		}
		if err := yaml.Unmarshal([]byte(doc), &out); err != nil {
			t.Fatalf("unmarshal focus output: %v\n%s", err, doc)
		}
		if out.Focus == nil || out.Focus.Spot == nil || len(out.Focus.Spot.Rows) != 1 {
			t.Fatalf("focus spot --config tele: want 1 spot row\n%s", doc)
		}
		if got := out.Focus.Spot.Rows[0].FieldAngle; got != 12 {
			t.Fatalf("focus spot --config tele: field_angle = %v, want 12", got)
		}
	})
}
