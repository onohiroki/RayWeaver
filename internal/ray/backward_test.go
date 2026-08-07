package ray

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// TestTraceBackwardReversibility traces a ray backward from the stop through the
// front optics, then reverses it and traces forward — the forward ray must pass
// back through the stop center (optical reciprocity).
func TestTraceBackwardReversibility(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "BK7", ND: 1.5168, VD: 64.17})

	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.1, Thickness: 2.0, Material: "BK7", Diameter: 10},
		{ID: 2, Type: types.Sphere, Curvature: 0, Thickness: 5.0, Material: "AIR", Diameter: 10},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: "AIR", Diameter: 3},
	}
	surface.Precompute(surfaces)
	e := NewEngine(gc, nil)

	seq := FrontPath(surfaces, 3)
	if len(seq) != 2 || seq[0] != 1 || seq[1] != 0 {
		t.Fatalf("FrontPath = %v, want [1 0]", seq)
	}

	stopPos := surfaces[2].LocalToGlobal.MultiplyPoint(types.Vec3{})
	const wl = 0.00058756

	for _, u := range []float64{0.0, 0.1, 0.2, 0.3} {
		dir := types.Vec3{X: 0, Y: u, Z: -1}.Normalize()
		pos, edir, ok := e.TraceBackward(surfaces, seq, stopPos, dir, wl)
		if !ok {
			t.Fatalf("TraceBackward failed for u=%v", u)
		}
		if angle := EmergentAngle(edir); u > 0 && angle <= 0 {
			t.Errorf("u=%v: emergent angle %v not positive", u, angle)
		}

		// Reverse: trace the emergent ray forward back through the lens.
		fwd := types.Ray{
			ID:         "rev",
			Wavelength: wl,
			Initial:    types.RayState{Origin: pos, Direction: edir.Scale(-1)},
			Path:       []int{0, 1, 2, 3},
			Jones:      types.NewCircularJones(true),
		}
		res := e.TraceRay(fwd, surfaces)
		if res.Error != "" {
			t.Fatalf("reverse forward trace failed u=%v: %v", u, res.Error)
		}
		var stopHit *types.SurfaceResult
		for i := range res.Surfaces {
			if res.Surfaces[i].SurfaceID == 3 {
				stopHit = &res.Surfaces[i]
			}
		}
		if stopHit == nil {
			t.Fatalf("u=%v: no stop surface hit", u)
		}
		if dist := math.Hypot(stopHit.Position.X, stopHit.Position.Y); dist > 1e-6 {
			t.Errorf("u=%v: reverse ray misses stop centre by %v mm", u, dist)
		}
	}
}

func TestFrontPathStopAtFirstSurface(t *testing.T) {
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.1, Thickness: 2.0, Material: "BK7"},
		{ID: 2, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: "AIR"},
	}
	if seq := FrontPath(surfaces, 1); seq != nil {
		t.Fatalf("FrontPath(stop=first) = %v, want nil", seq)
	}
}
