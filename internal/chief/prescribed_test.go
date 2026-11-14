package chief

import (
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// clippedSinglet builds a lens whose 12 deg beam is clipped to a thin sliver at
// surface 3 (diameter 3 mm), so the full-aperture grid loses most of its rays:
// the geometry in which the delivered-pupil grid and the prescribed grid
// disagree by an order of magnitude.
func clippedSinglet(t *testing.T) (types.System, *glass.Catalog) {
	t.Helper()
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 10.0, Material: types.Material{Key: "N-BK7"}, Diameter: 50.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 10.0, Material: types.Material{}, Diameter: 8.0, Role: "stop"},
		{ID: 3, Type: types.Sphere, Curvature: 0.01, Thickness: 60.0, Material: types.Material{}, Diameter: 3.0},
		{ID: 4, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 100.0},
	}
	surface.Precompute(surfaces)
	return types.System{Surfaces: surfaces, StopSurface: 2}, gc
}

// TestPrescribedSurvivalIsTheVignettingMeasure pins the two measurements apart
// on a heavily clipped field. The spot statistics are measured on the DELIVERED
// (effective) pupil — a reduced grid laid over the surviving sliver — so their
// traced fraction says how much of the *shrunken* pupil arrived, not how much
// of the prescribed pupil the design delivers. `zoom` read that fraction as the
// vignetting and reported 0.90 for a field that delivered 0.16; the prescribed
// counts are the honest number.
func TestPrescribedSurvivalIsTheVignettingMeasure(t *testing.T) {
	sys, gc := clippedSinglet(t)
	pol := types.JonesVector{Ex: complex(1, 0), Ey: complex(0, 1)}
	fields := []types.FieldDef{{Angle: 12, Direction: []float64{0, 1}}}

	res := DetermineChiefRaysGridMode(sys, fields, 4, 64, gc, pol, 0.00058756,
		false, types.GridHex, nil, nil, nil, nil, 0, 0, "")
	if len(res) != 1 {
		t.Fatalf("results = %d, want 1", len(res))
	}
	r := res[0]
	if r.PrescribedTotal != len(r.GridPoints) {
		t.Errorf("prescribed total %d != %d emitted grid points", r.PrescribedTotal, len(r.GridPoints))
	}
	if r.PrescribedTotal == 0 {
		t.Fatal("no grid points launched")
	}
	prescribed := float64(r.PrescribedTraced) / float64(r.PrescribedTotal)
	if prescribed >= 0.5 {
		t.Fatalf("fixture is not clipped hard enough: prescribed survival %.3f", prescribed)
	}
	if r.SpotStats == nil || r.SpotStats.TotalRays == 0 {
		t.Fatal("no spot statistics")
	}
	delivered := float64(r.SpotStats.TracedRays) / float64(r.SpotStats.TotalRays)
	// The delivered grid follows the surviving sliver, so its own survival is
	// near 1 — using it as the vignetting was the bug this test guards.
	if delivered < 0.9 {
		t.Errorf("delivered-pupil survival %.3f, want a reduced grid near 1 (prescribed %.3f)", delivered, prescribed)
	}
	if r.EffectiveVignetting == nil {
		t.Error("no effective-vignetting ellipse for a heavily clipped field")
	}
	// The reduced-grid fallback must never erase a spot the design supports.
	if r.PrescribedTraced > 0 && r.SpotStats.TracedRays == 0 {
		t.Errorf("prescribed grid delivered %d rays but the spot statistics are empty", r.PrescribedTraced)
	}
}

// TestGridPointsNameTheFailingSurface pins the diagnostic that makes a clipped
// field actionable: a grid ray that does not reach the reference surface
// records the surface whose aperture (or geometry) stopped it, so
// `chief --dump-map` can say "field 1 dies at surface 6", not just
// "aperture_stop".
func TestGridPointsNameTheFailingSurface(t *testing.T) {
	sys, gc := clippedSinglet(t)
	pol := types.JonesVector{Ex: complex(1, 0), Ey: complex(0, 1)}
	res := DetermineChiefRaysGridMode(sys, []types.FieldDef{{Angle: 12, Direction: []float64{0, 1}}},
		4, 64, gc, pol, 0.00058756, false, types.GridHex, nil, nil, nil, nil, 0, 0, "")
	if len(res) != 1 {
		t.Fatalf("results = %d, want 1", len(res))
	}
	failed := map[int]int{}
	for _, gp := range res[0].GridPoints {
		if gp.ImageX != nil {
			continue
		}
		if gp.ErrorCode == "" {
			t.Fatal("a missed grid ray carries no error code")
		}
		failed[gp.ErrorSurface]++
	}
	if len(failed) == 0 {
		t.Fatal("fixture delivered every ray; nothing to diagnose")
	}
	// Surface 3 is the 3 mm aperture this fixture clips on.
	if failed[3] == 0 {
		t.Errorf("no grid ray blames surface 3: %v", failed)
	}
}
