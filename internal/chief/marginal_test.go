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

// marginalStopSystem has a strong negative front meniscus that demagnifies the
// stop's image: the entrance-pupil radius is much smaller than the stop radius.
// A marginal ray aimed at the stop edge with a straight line from the launch
// plane therefore overshoots the stop by the inverse magnification.
func marginalStopSystem() (types.System, *glass.Catalog) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-SF6", ND: 1.8, VD: 25})
	air := types.Material{Key: "AIR"}
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.02, Thickness: 1.5, Material: types.Material{Key: "N-SF6"}, Diameter: 30},
		{ID: 2, Type: types.Sphere, Curvature: 0.12, Thickness: 20, Material: air, Diameter: 30},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 5, Material: air, Diameter: 6},
		{ID: 4, Type: types.Sphere, Curvature: 0.04, Thickness: 4, Material: types.Material{Key: "N-SF6"}, Diameter: 14},
		{ID: 5, Type: types.Sphere, Curvature: -0.04, Thickness: 40, Material: air, Diameter: 14},
		{ID: 6, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: air, Diameter: 20},
	}
	surface.Precompute(surfaces)
	return types.System{Surfaces: surfaces, StopSurface: 3}, gc
}

// TestMarginalRaysGrazeTheStop pins the launch construction: the marginal rays
// must cross the stop surface inside its clear aperture, and the stop-edge ray
// must actually graze it. The old construction back-projected the stop edge to
// the launch plane along the chief direction, ignoring the refraction in
// between; with this front group the axial marginals crossed the 6 mm stop at
// about 11 mm (1.9x the radius) and were drawn outside the stop marker.
func TestMarginalRaysGrazeTheStop(t *testing.T) {
	sys, gc := marginalStopSystem()
	pol := types.NewCircularJones(true)
	const wl = 0.00058756
	const stopID = 3

	results := DetermineChiefRaysGrid(sys, []types.FieldDef{{Angle: 0, Direction: []float64{0, 1}}},
		6, 32, gc, pol, wl, false, types.GridPolar, nil, nil, nil, nil, 0, 0)
	if len(results) != 1 || results[0].EntrancePupil == nil {
		t.Fatal("chief run produced no result / entrance pupil")
	}
	stopR := 3.0
	if ep := results[0].EntrancePupil.Radius; ep <= 0 || ep >= stopR {
		t.Fatalf("fixture degenerate: entrance-pupil radius %v does not demagnify the stop %v", ep, stopR)
	}

	engine := ray.NewEngine(gc, nil)
	path := dls.BuildPath(sys.Surfaces)
	rays := MarginalRays(0, results[0], stopID, sys.Surfaces, engine, wl, path, pol)
	if len(rays) < 2 {
		t.Fatalf("got %d marginal rays, want the Y pair", len(rays))
	}
	for _, r := range rays {
		res := engine.TraceRay(r, sys.Surfaces, false)
		reached := false
		for _, sr := range res.Surfaces {
			if sr.SurfaceID != stopID {
				continue
			}
			reached = true
			h := math.Hypot(sr.Position.X, sr.Position.Y)
			if h > stopR+1e-6 {
				t.Errorf("%s crosses the stop at %.4f, outside its radius %.4f", r.ID, h, stopR)
			}
			if sr.ErrorCode == string(ray.ErrApertureStop) {
				t.Errorf("%s is flagged aperture_stop at the stop (h=%.4f, radius=%.4f)", r.ID, h, stopR)
			}
		}
		if !reached {
			t.Errorf("%s did not reach the stop", r.ID)
		}
	}
}

// TestMarginalRaysGrazeTheStopOffAxis runs the same check for a tilted field:
// the pupil is off-centre, so each side of the stop edge must be solved
// independently.
func TestMarginalRaysGrazeTheStopOffAxis(t *testing.T) {
	sys, gc := marginalStopSystem()
	pol := types.NewCircularJones(true)
	const wl = 0.00058756
	const stopID = 3

	results := DetermineChiefRaysGrid(sys, []types.FieldDef{{Angle: 8, Direction: []float64{0, 1}}},
		6, 32, gc, pol, wl, false, types.GridPolar, nil, nil, nil, nil, 0, 0)
	if len(results) != 1 {
		t.Fatal("chief run produced no result")
	}
	engine := ray.NewEngine(gc, nil)
	path := dls.BuildPath(sys.Surfaces)
	rays := MarginalRays(0, results[0], stopID, sys.Surfaces, engine, wl, path, pol)
	if len(rays) < 2 {
		t.Fatalf("got %d marginal rays, want the Y pair", len(rays))
	}
	for _, r := range rays {
		res := engine.TraceRay(r, sys.Surfaces, false)
		for _, sr := range res.Surfaces {
			if sr.SurfaceID != stopID {
				continue
			}
			h := math.Hypot(sr.Position.X, sr.Position.Y)
			if h > 3.0+1e-6 {
				t.Errorf("%s crosses the stop at %.4f, outside its radius 3", r.ID, h)
			}
		}
	}
}

// TestMarginalRaysGrazeTheStopFiniteConjugate covers the object-height launch
// path: the ray starts at the object point and is aimed at the pupil edge, so
// the stop-edge solve must adjust the aim rather than a parallel bundle offset.
func TestMarginalRaysGrazeTheStopFiniteConjugate(t *testing.T) {
	sys, gc := marginalStopSystem()
	pol := types.NewCircularJones(true)
	const wl = 0.00058756
	const stopID = 3

	results := DetermineChiefRaysGrid(sys, []types.FieldDef{{Height: 1, ObjectZ: -100, Direction: []float64{0, 1}}},
		6, 32, gc, pol, wl, false, types.GridPolar, nil, nil, nil, nil, 0, 0)
	if len(results) != 1 {
		t.Fatal("chief run produced no result")
	}
	engine := ray.NewEngine(gc, nil)
	path := dls.BuildPath(sys.Surfaces)
	rays := MarginalRays(0, results[0], stopID, sys.Surfaces, engine, wl, path, pol)
	if len(rays) < 2 {
		t.Fatalf("got %d marginal rays, want the Y pair", len(rays))
	}
	for _, r := range rays {
		res := engine.TraceRay(r, sys.Surfaces, false)
		for _, sr := range res.Surfaces {
			if sr.SurfaceID != stopID {
				continue
			}
			h := math.Hypot(sr.Position.X, sr.Position.Y)
			if h > 3.0+1e-6 {
				t.Errorf("%s crosses the stop at %.4f, outside its radius 3", r.ID, h)
			}
		}
	}
}
