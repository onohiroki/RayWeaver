package main

import (
	"strings"
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
)

// errorRayResult builds a RayResult that stopped at surface 1 with an
// aperture_stop error (the trace single IncludeErrorSurfaces shape).
func errorRayResult() types.RayResult {
	aoi := 17.53
	n1 := 1.0
	n2 := 1.72
	return types.RayResult{
		ID:         "trace_single",
		Wavelength: 0.00058756,
		Error:      "ray missed surface (aperture stop)",
		ErrorCode:  "aperture_stop",
		Surfaces: []types.SurfaceResult{
			{SurfaceID: 0, Position: types.Vec3{Y: 2, Z: -100}, Direction: types.Vec3{Z: 1}, Interaction: types.Transmit, IntensityS: 1, IntensityP: 1},
			{
				SurfaceID:   1,
				Position:    types.Vec3{Y: 10.85, Z: 1.19},
				Direction:   types.Vec3{Y: 0.087, Z: 0.996},
				Interaction: types.Missed,
				IntensityS:  0,
				IntensityP:  0,
				ErrorCode:   "aperture_stop",
				// Detail fields mirror --details output.
				AngleOfIncidence: &aoi,
				N1:               &n1,
				N2:               &n2,
			},
		},
	}
}

// TestBuildRaySummaryRowsError verifies that a ray whose trace stopped carries
// its error message into the summary row.
func TestBuildRaySummaryRowsError(t *testing.T) {
	rows := buildRaySummaryRows([]types.RayResult{errorRayResult()})
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	r := rows[0]
	if r.ID != "trace_single" {
		t.Errorf("ID = %q, want trace_single", r.ID)
	}
	if r.Error != "ray missed surface (aperture stop)" {
		t.Errorf("Error = %q, want the aperture_stop message", r.Error)
	}
	if r.Missed != 1 {
		t.Errorf("Missed = %d, want 1", r.Missed)
	}
	if r.Transmitted != 1 {
		t.Errorf("Transmitted = %d, want 1", r.Transmitted)
	}
}

// TestBuildRayDetailRowsError verifies the per-surface detail row carries the
// error code of the stopping surface.
func TestBuildRayDetailRowsError(t *testing.T) {
	rows := buildRayDetailRows([]types.RayResult{errorRayResult()})
	if len(rows) != 2 {
		t.Fatalf("got %d detail rows, want 2", len(rows))
	}
	if rows[1].SurfaceID != 1 {
		t.Errorf("second row surface_id = %d, want 1", rows[1].SurfaceID)
	}
	if rows[1].ErrorCode != "aperture_stop" {
		t.Errorf("second row error_code = %q, want aperture_stop", rows[1].ErrorCode)
	}
	if rows[0].ErrorCode != "" {
		t.Errorf("object plane row error_code = %q, want empty", rows[0].ErrorCode)
	}
	// Detail pointers are copied, not aliased.
	if rows[1].AngleOfIncidence == nil || *rows[1].AngleOfIncidence != 17.53 {
		t.Errorf("AngleOfIncidence not copied into detail row")
	}
}

// TestPrintRayDetailTableError verifies the rendered detail table marks the
// erroring surface with its error code column value.
func TestPrintRayDetailTableError(t *testing.T) {
	rows := buildRayDetailRows([]types.RayResult{errorRayResult()})
	out := captureStdout(t, func() { printRayDetailTable(rows) })
	text := string(out)
	if !strings.Contains(text, "aperture_stop") {
		t.Errorf("detail table does not contain the error code:\n%s", text)
	}
}