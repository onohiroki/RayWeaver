package feasibility

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

func nodeProbe() ([]types.Surface, *glass.Catalog) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.02, Thickness: 5.0, Material: types.Material{Key: "N-BK7"}, Diameter: 80.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.02, Thickness: 40.0, Material: types.Material{}, Diameter: 80.0},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 80.0},
	}
	surface.Precompute(surfaces)
	return surfaces, gc
}

func nodeLaunch(angleDeg, height float64) types.RayState {
	angle := angleDeg * math.Pi / 180
	return types.RayState{
		Origin:    types.Vec3{X: 0, Y: height, Z: -100},
		Direction: types.Vec3{X: 0, Y: math.Sin(angle), Z: math.Cos(angle)},
	}
}

// TestSolveNodesMatchesSequentialTrace checks that the node system reproduces
// the physical path when the sequential trace already succeeds, for the axis
// and an off-axis ray. This is the correctness gate for the formulation.
func TestSolveNodesMatchesSequentialTrace(t *testing.T) {
	surfaces, gc := nodeProbe()
	for _, angle := range []float64{0, 5} {
		launch := nodeLaunch(angle, 5)
		engine := ray.NewEngine(gc, nil)
		ref := engine.TraceRay(types.Ray{
			Wavelength: 0.00058756,
			Path:       []int{1, 2, 3},
			Initial:    launch,
		}, surfaces, false)
		if ref.Error != "" {
			t.Fatalf("angle %v: reference trace failed: %v", angle, ref.Error)
		}

		np := SolveNodes(surfaces, []int{1, 2, 3}, launch, 0.00058756, gc, SolveOptions{})
		if !np.Converged {
			t.Fatalf("angle %v: node solve did not converge (residual %v)", angle, np.Residual)
		}
		if len(np.Points) != len(ref.Surfaces) {
			t.Fatalf("angle %v: %d node points, %d trace results", angle, len(np.Points), len(ref.Surfaces))
		}
		for i, sr := range ref.Surfaces {
			if d := np.Points[i].Subtract(sr.Position).Length(); d > 1e-5 {
				t.Errorf("angle %v surface %d: node %+v vs trace %+v (distance %v)",
					angle, sr.SurfaceID, np.Points[i], sr.Position, d)
			}
		}
	}
}

func TestSolveNodesReportsResidualWhenNoPhysicalPath(t *testing.T) {
	surfaces, gc := nodeProbe()
	// 10 deg at height 5 leaves the first surface's finite sphere. The node
	// solve has no missed-surface event: it returns the best least-squares
	// path, a finite positive residual and Converged=false.
	np := SolveNodes(surfaces, []int{1, 2, 3}, nodeLaunch(10, 5), 0.00058756, gc, SolveOptions{})
	if np.Converged {
		t.Error("expected non-convergence for a path that leaves the geometry")
	}
	if math.IsNaN(np.Residual) || math.IsInf(np.Residual, 0) {
		t.Errorf("residual = %v, want a finite value", np.Residual)
	}
	if np.Residual <= 0 {
		t.Errorf("residual = %v, want a positive violation", np.Residual)
	}
	if len(np.Points) != 3 {
		t.Errorf("points = %d, want 3", len(np.Points))
	}
}

func TestSolveNodesDeterministic(t *testing.T) {
	surfaces, gc := nodeProbe()
	launch := nodeLaunch(5, 5)
	a := SolveNodes(surfaces, []int{1, 2, 3}, launch, 0.00058756, gc, SolveOptions{})
	b := SolveNodes(surfaces, []int{1, 2, 3}, launch, 0.00058756, gc, SolveOptions{})
	if !a.Converged || !b.Converged {
		t.Fatalf("converged = %v / %v, residual = %v / %v", a.Converged, b.Converged, a.Residual, b.Residual)
	}
	for i := range a.Points {
		if a.Points[i] != b.Points[i] {
			t.Errorf("surface %d: %v != %v", a.Surfaces[i], a.Points[i], b.Points[i])
		}
	}
	if a.Residual != b.Residual {
		t.Errorf("residual %v != %v", a.Residual, b.Residual)
	}
}
