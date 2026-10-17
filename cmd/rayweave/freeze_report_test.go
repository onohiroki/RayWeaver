package main

import (
	"strings"
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
)

// stubFieldResolution is a model stand-in exposing only the freeze report, so
// the run-level wiring can be tested without building a real optimizer.
type stubFieldResolution struct{ rows []types.FieldResolution }

func (s stubFieldResolution) FieldResolutionDiagnostics() []types.FieldResolution { return s.rows }

// TestFreezeFieldReportRecordsAndWarns pins the run-level contract of the field
// freeze: the resolved angles and their outcome land in the output document
// (optimization.field_resolution), and a field the start geometry cannot form is
// warned about — but never stops the run, because a solve cannot abort mid-search
// and the closest reachable angle is what it optimises instead.
func TestFreezeFieldReportRecordsAndWarns(t *testing.T) {
	input := types.Input{Optimization: &types.OptimizationConfig{}}
	rows := []types.FieldResolution{
		{Config: "config0", FieldIndex: 1, FieldID: 1, Requested: 10, Reached: 2.7, AngleDeg: 7.5, Resolved: false},
		{Config: "config0", FieldIndex: 2, FieldID: 2, Requested: 14, Reached: 14, AngleDeg: 15, Resolved: true},
	}

	warned := captureStderr(t, func() { freezeFieldReport(&input, stubFieldResolution{rows: rows}) })

	got := input.Optimization.FieldResolution
	if len(got) != 2 || got[0] != rows[0] || got[1] != rows[1] {
		t.Fatalf("optimization.field_resolution = %+v, want the reported rows", got)
	}
	if !strings.Contains(warned, `config "config0"`) || !strings.Contains(warned, "image_height 10") {
		t.Errorf("warning = %q, want the config and the declared height", warned)
	}
	if strings.Contains(warned, "image_height 14") {
		t.Errorf("warning = %q, want nothing for the resolved field", warned)
	}

	// A model that reports no image-height field leaves the section absent: a
	// config declared by angle needs no resolution and must not gain one.
	plain := types.Input{Optimization: &types.OptimizationConfig{}}
	captureStderr(t, func() { freezeFieldReport(&plain, stubFieldResolution{}) })
	if plain.Optimization.FieldResolution != nil {
		t.Errorf("optimization.field_resolution = %+v, want nil", plain.Optimization.FieldResolution)
	}

	// A model without the capability is ignored, not fatal (every other
	// dls.Model implementation).
	none := types.Input{Optimization: &types.OptimizationConfig{}}
	captureStderr(t, func() { freezeFieldReport(&none, struct{}{}) })
	if none.Optimization.FieldResolution != nil {
		t.Errorf("optimization.field_resolution = %+v, want nil", none.Optimization.FieldResolution)
	}
}
