package main

import (
	"encoding/json"
	"testing"
)

// listFieldsTwoConfigYAML is a two-position document whose fields live only in
// configs[].fields (no top-level chief.fields), so which configuration is
// selected decides what `list fields` reports.
const listFieldsTwoConfigYAML = `chief:
  stop_surface: 2
glass_catalog:
  entries:
    - {type: model, name: N-BK7, nd: 1.5168, vd: 64.17}
configs:
  - id: wide
    active: true
    fields:
      - {id: 0, angle_deg: 2, weight: 1}
    surfaces:
      - {id: 1, type: sphere, radius: 50.0, thickness: 5.0, material: N-BK7, diameter: 30.0}
      - {id: 2, type: sphere, radius: -50.0, thickness: 100.0, material: AIR, diameter: 30.0}
  - id: tele
    active: true
    fields:
      - {id: 0, angle_deg: 12, weight: 1}
    surfaces:
      - {id: 1, type: sphere, radius: 80.0, thickness: 5.0, material: N-BK7, diameter: 30.0}
      - {id: 2, type: sphere, radius: -80.0, thickness: 100.0, material: AIR, diameter: 30.0}
`

// TestListFieldsHonoursConfig pins the --config selector: `list fields` used to
// resolve fields from the top-level chief section and then the first config,
// silently ignoring --config (and --glass-dir), so the reported fields belonged
// to the wrong zoom position.
func TestListFieldsHonoursConfig(t *testing.T) {
	for _, tc := range []struct {
		config string
		want   float64
	}{
		{"wide", 2},
		{"tele", 12},
	} {
		t.Run(tc.config, func(t *testing.T) {
			out := runCommand(t, []string{"rayweave", "list", "fields", "--config", tc.config, "--format", "json"},
				func() { runList([]byte(listFieldsTwoConfigYAML)) })

			var doc fieldsListOutput
			if err := json.Unmarshal(out, &doc); err != nil {
				t.Fatalf("unmarshal list fields output: %v\n%s", err, out)
			}
			if len(doc.Fields) != 1 {
				t.Fatalf("got %d field rows, want 1\n%s", len(doc.Fields), out)
			}
			row := doc.Fields[0]
			if row.InputAngle == nil || *row.InputAngle != tc.want {
				t.Fatalf("config %q: input_angle = %v, want %v\n%s", tc.config, row.InputAngle, tc.want, out)
			}
		})
	}
}
