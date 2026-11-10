package chief

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// TestBeamEnvelopeKeepsTheReachedPrefix pins the rule that an aperture is sized
// from the rays that reach it, not from the rays that pass the whole system. The
// old envelope dropped a ray at its first failure anywhere, so one undersized
// aperture downstream starved the envelope of every surface before it: a field
// that clips at the rear surface reported no front-surface footprint at all,
// the front stayed too small, and `chief --clear-aperture` could never open it
// (measured 39/74 rays still clipping after five sizing passes).
func TestBeamEnvelopeKeepsTheReachedPrefix(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 10.0, Material: types.Material{Key: "N-BK7"}, Diameter: 50.0},
		// The rear aperture is FIXED and deliberately far too small: every ray
		// clips here, so nothing survives to the reference surface, and the
		// envelope pass cannot skip the check (only auto_aperture surfaces are
		// skipped).
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 100.0, Material: types.Material{}, Diameter: 0.4},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 100.0},
	}
	surface.Precompute(surfaces)
	sys := types.System{Surfaces: surfaces}

	res := DetermineChiefRaysGridMode(sys, []types.FieldDef{{Angle: 12, Direction: []float64{0, 1}}},
		3, 64, gc, types.NewCircularJones(true), 0.00058756, false, types.GridHex,
		nil, nil, nil, nil, 0, 0, "")
	if len(res) != 1 {
		t.Fatalf("results = %d, want 1", len(res))
	}
	if res[0].PrescribedTraced != 0 {
		t.Fatalf("fixture does not clip every ray: %d/%d survived",
			res[0].PrescribedTraced, res[0].PrescribedTotal)
	}

	engine := ray.NewEngine(gc, nil)
	env := BeamEnvelope(res, engine, surfaces, dls.BuildPath(surfaces), 0.00058756, types.NewCircularJones(true))
	if env[1] <= 0 {
		t.Fatalf("front-surface envelope = %v, want the radius the rays did reach (the prefix must survive the downstream clip)", env[1])
	}
	// A FIXED aperture still bounds the bundle: nothing passes it, so no
	// footprint is measured beyond it.
	if env[2] != 0 {
		t.Errorf("fixed-aperture surface envelope = %v, want 0 (it stays closed in the envelope pass)", env[2])
	}
	// The measured front footprint must be at least the on-axis beam radius of
	// this launch (the field bundle is centred on the pupil), not a rounding
	// artefact of the surviving-zero rays.
	if env[1] < 1.0 {
		t.Errorf("front-surface envelope = %v, want the launched bundle radius", env[1])
	}
}

// TestBeamEnvelopeSizingConverges pins the sizing loop end to end: measure the
// envelope, size the auto_aperture surfaces from it, re-trace, repeat — and no
// ray is stopped by a sized surface once the sizes settle. Iteration is needed
// because the grid centring follows the surviving bundle (the chief aim is the
// traced centroid), so a nearly dead field starts aimed at the few rays that
// got through and re-centres once its own beam passes.
func TestBeamEnvelopeSizingConverges(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	// Every lens surface starts far too small; the stop is fixed.
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.02, Thickness: 8.0, Material: types.Material{Key: "N-BK7"}, Diameter: 2.0, AutoAperture: true},
		{ID: 2, Type: types.Sphere, Curvature: -0.02, Thickness: 12.0, Material: types.Material{}, Diameter: 2.0, AutoAperture: true},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 60.0, Material: types.Material{}, Diameter: 8.0},
		{ID: 4, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 100.0},
	}
	surface.Precompute(surfaces)
	pol := types.NewCircularJones(true)
	fields := []types.FieldDef{{Angle: 10, Direction: []float64{0, 1}}}
	measure := func(surfs []types.Surface) Result {
		surface.Precompute(surfs)
		res := DetermineChiefRaysGridMode(types.System{Surfaces: surfs, StopSurface: 3},
			fields, 4, 64, gc, pol, 0.00058756, false, types.GridHex, nil, nil, nil, nil, 0, 0, "")
		if len(res) != 1 {
			t.Fatalf("results = %d, want 1", len(res))
		}
		return res[0]
	}

	if r := measure(surfaces); r.PrescribedTraced*10 > r.PrescribedTotal {
		t.Fatalf("fixture is not clipped enough at the start: %d/%d rays survived", r.PrescribedTraced, r.PrescribedTotal)
	}

	engine := ray.NewEngine(gc, nil)
	sized := append([]types.Surface(nil), surfaces...)
	settled := false
	for pass := 0; pass < 4; pass++ {
		env := BeamEnvelope([]Result{measure(sized)}, engine, sized, dls.BuildPath(sized), 0.00058756, pol)
		changed := false
		for i := range sized {
			if !sized[i].AutoAperture {
				continue
			}
			e := env[sized[i].ID]
			if e <= 0 {
				continue
			}
			if d := 2 * (e + 0.05); math.Abs(d-sized[i].Diameter) > 0.01 {
				changed = true
				sized[i].Diameter = d
			}
		}
		if !changed {
			settled = true
			break
		}
	}
	if !settled {
		t.Fatal("sizing did not settle within four passes")
	}
	a := measure(sized)
	b := measure(sized)
	if a.PrescribedTraced != b.PrescribedTraced {
		t.Fatalf("measure is not deterministic: %d vs %d", a.PrescribedTraced, b.PrescribedTraced)
	}
	for _, gp := range a.GridPoints {
		if gp.ImageX == nil && gp.ErrorCode == "aperture_stop" && (gp.ErrorSurface == 1 || gp.ErrorSurface == 2) {
			t.Fatalf("a ray is still clipped at surface %d after the sizes settled (prescribed %d/%d, diam1=%.3f diam2=%.3f)",
				gp.ErrorSurface, a.PrescribedTraced, a.PrescribedTotal, sized[0].Diameter, sized[1].Diameter)
		}
	}
}
