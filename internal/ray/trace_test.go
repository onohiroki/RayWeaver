package ray

import (
	"math"
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

func foldedMirrorSurfaces() ([]types.Surface, *glass.Catalog) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0, Thickness: 1000.0, Material: "AIR", Diameter: 200.0},
		{ID: 2, Type: types.Sphere, Curvature: 1.0 / 1000.0, Thickness: 480.0, Material: "AIR", Diameter: 300.0,
			Decenter: []types.DecenterStep{{Tilt: types.Vec3{X: 0, Y: 180, Z: 0}, Reflect: true}}},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 20.0, Material: "AIR", Diameter: 60.0},
		{ID: 4, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: "AIR", Diameter: 50.0},
	}
	surface.Precompute(surfaces)
	return surfaces, gc
}

func TestTraceRayReflectFlag(t *testing.T) {
	surfaces, gc := foldedMirrorSurfaces()
	engine := NewEngine(gc, nil)
	ray := types.Ray{
		ID:        "mirror",
		Wavelength: 0.00058756,
		Path:      []int{1, 2, 3, 4},
		Initial: types.RayState{
			Origin:    types.Vec3{X: 0, Y: 0, Z: -100.0},
			Direction: types.Vec3{X: 0, Y: 0, Z: 1.0},
		},
	}
	result := engine.TraceRay(ray, surfaces)
	if result.Error != "" {
		t.Fatalf("TraceRay error: %v", result.Error)
	}
	if result.Surfaces[1].Interaction != types.Reflect {
		t.Errorf("Surface 2 interaction = %v, want Reflect", result.Surfaces[1].Interaction)
	}
	if result.Surfaces[2].Interaction != types.Transmit {
		t.Errorf("Surface 3 interaction = %v, want Transmit", result.Surfaces[2].Interaction)
	}
	// Ideal uncoated mirror in air: intensity = 1.0
	if result.Surfaces[1].IntensityS != 1.0 {
		t.Errorf("Mirror IntensityS = %v, want 1.0", result.Surfaces[1].IntensityS)
	}
	// Ray reflects at the mirror's physical Z=1000 and returns toward the image.
	if result.Surfaces[1].Position.Z < 999 || result.Surfaces[1].Position.Z > 1001 {
		t.Errorf("Mirror hit Z = %v, want ~1000", result.Surfaces[1].Position.Z)
	}
	if result.Surfaces[2].Position.Z > 521 || result.Surfaces[2].Position.Z < 519 {
		t.Errorf("Surface 3 hit Z = %v, want ~520", result.Surfaces[2].Position.Z)
	}
}

func TestTraceRayReflectFlagTilted(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	engine := NewEngine(gc, nil)
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0, Thickness: 1000.0, Material: "AIR", Diameter: 200.0},
		{ID: 2, Type: types.Sphere, Curvature: 1.0 / 1000.0, Thickness: 480.0, Material: "AIR", Diameter: 300.0,
			Decenter: []types.DecenterStep{{Tilt: types.Vec3{X: 0, Y: 180, Z: 0}, Reflect: true}}},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 20.0, Material: "AIR", Diameter: 60.0},
		{ID: 4, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: "AIR", Diameter: 50.0},
	}
	surface.Precompute(surfaces)

	// Parallel bundle: concave mirror (f=R/2=500) focuses all heights at
	// physical Z = 1000-500 = 500, on axis.
	for _, h := range []float64{0, 20, 50, 80} {
		ray := types.Ray{
			ID:        "bundle",
			Wavelength: 0.00058756,
			Path:      []int{1, 2, 3, 4},
			Initial: types.RayState{
				Origin:    types.Vec3{X: h, Y: 0, Z: -100.0},
				Direction: types.Vec3{X: 0, Y: 0, Z: 1.0},
			},
		}
		result := engine.TraceRay(ray, surfaces)
		if result.Error != "" {
			t.Fatalf("h=%v TraceRay error: %v", h, result.Error)
		}
		last := result.Surfaces[len(result.Surfaces)-1]
		if last.SurfaceID != 4 {
			t.Fatalf("h=%v last surface = %d, want 4", h, last.SurfaceID)
		}
		if math.Abs(last.Position.Y) > 1e-6 {
			t.Errorf("h=%v image Y = %v, want ~0", h, last.Position.Y)
		}
		if math.Abs(last.Position.Z-500.0) > 1.0 {
			t.Errorf("h=%v image Z = %v, want ~500", h, last.Position.Z)
		}
	}
}
