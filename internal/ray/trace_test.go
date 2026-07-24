package ray

import (
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

func simpleSingletEngine() (*Engine, []types.Surface) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	engine := NewEngine(gc, nil)
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 10.0, Material: "N-BK7", Diameter: 50.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 100.0, Material: "AIR", Diameter: 50.0},
	}
	surface.Precompute(surfaces)
	return engine, surfaces
}

func TestTraceRayOnAxis(t *testing.T) {
	engine, surfaces := simpleSingletEngine()
	ray := types.Ray{
		ID:        "onaxis",
		Wavelength: 0.00058756,
		Path:      []int{1, 2},
		Initial: types.RayState{
			Origin:    types.Vec3{X: 0, Y: 0, Z: -100.0},
			Direction: types.Vec3{X: 0, Y: 0, Z: 1.0},
		},
	}
	result := engine.TraceRay(ray, surfaces)
	if result.Error != "" {
		t.Fatalf("TraceRay error: %v", result.Error)
	}
	if len(result.Surfaces) != 2 {
		t.Fatalf("Expected 2 surface results, got %d", len(result.Surfaces))
	}
	if result.Surfaces[0].Position.Z < -1 || result.Surfaces[0].Position.Z > 1 {
		t.Errorf("Surface 1 position Z = %v, want near 0", result.Surfaces[0].Position.Z)
	}
}

func TestTraceRayOffAxis(t *testing.T) {
	engine, surfaces := simpleSingletEngine()
	ray := types.Ray{
		ID:        "offaxis",
		Wavelength: 0.00058756,
		Path:      []int{1, 2},
		Initial: types.RayState{
			Origin:    types.Vec3{X: 0, Y: 5.0, Z: -100.0},
			Direction: types.Vec3{X: 0, Y: 0, Z: 1.0},
		},
	}
	result := engine.TraceRay(ray, surfaces)
	if result.Error != "" {
		t.Fatalf("TraceRay error: %v", result.Error)
	}
	if result.Surfaces[0].Position.Y == 0 {
		t.Error("Off-axis ray should not hit surface 1 at Y=0")
	}
}

func TestTraceRayMissesAperture(t *testing.T) {
	engine, surfaces := simpleSingletEngine()
	ray := types.Ray{
		ID:        "miss",
		Wavelength: 0.00058756,
		Path:      []int{1, 2},
		Initial: types.RayState{
			Origin:    types.Vec3{X: 0, Y: 100.0, Z: -100.0},
			Direction: types.Vec3{X: 0, Y: 0, Z: 1.0},
		},
	}
	result := engine.TraceRay(ray, surfaces)
	if result.Error == "" {
		t.Error("Expected error for ray that misses aperture")
	}
}

func TestNewEngine(t *testing.T) {
	gc := glass.NewCatalog()
	engine := NewEngine(gc, nil)
	if engine == nil {
		t.Fatal("NewEngine returned nil")
	}
	if engine.Glass != gc {
		t.Error("Glass catalog mismatch")
	}
}

func TestTraceRayPreservesOPL(t *testing.T) {
	engine, surfaces := simpleSingletEngine()
	ray := types.Ray{
		ID:        "opl",
		Wavelength: 0.00058756,
		Path:      []int{1, 2},
		Initial: types.RayState{
			Origin:    types.Vec3{X: 0, Y: 0, Z: -100.0},
			Direction: types.Vec3{X: 0, Y: 0, Z: 1.0},
		},
	}
	result := engine.TraceRay(ray, surfaces)
	if result.Error != "" {
		t.Fatalf("TraceRay error: %v", result.Error)
	}
	if result.OPLTotal <= 0 {
		t.Error("OPL should be positive")
	}
	// OPL should be > geometric distance (n>1 for glass)
	if result.OPLTotal < (4.0 + 10.0 + 100.0) {
		t.Errorf("OPL = %v, should be > geometric path", result.OPLTotal)
	}
}
