package paraxial

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

func singletSystem() (types.System, *glass.Catalog) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Name: "N-BK7", ND: 1.5168, VD: 64.17})
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 10.0, Material: "N-BK7", Diameter: 50.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 100.0, Material: "AIR", Diameter: 50.0},
	}
	surface.Precompute(surfaces)
	return types.System{Surfaces: surfaces}, gc
}

func TestComputeSingletEFL(t *testing.T) {
	sys, gc := singletSystem()
	result := Compute(sys, 0.00058756, gc, 0, nil)
	// Singlet R=100, n=1.5168, t=10, thick lens EFL should be positive (converging)
	if result.FocalLength <= 0 {
		t.Errorf("FocalLength = %v, want positive (converging)", result.FocalLength)
	}
}

func TestComputeWithObjectHeight(t *testing.T) {
	sys, gc := singletSystem()
	result := Compute(sys, 0.00058756, gc, 10.0, nil)
	if result.Magnification == 0 {
		t.Error("Magnification should be non-zero for finite conjugate")
	}
}

func TestComputeWithChiefRays(t *testing.T) {
	sys, gc := singletSystem()
	chiefRays := []types.ChiefRayResult{
		{
			FieldAngle: 0.0,
			ChiefRay: types.Ray{
				Initial: types.RayState{
					Origin:    types.Vec3{Y: 0},
					Direction: types.Vec3{Z: 1},
				},
			},
		},
	}
	result := Compute(sys, 0.00058756, gc, 0, chiefRays)
	if result.FocalLength <= 0 {
		t.Errorf("FocalLength = %v, want positive", result.FocalLength)
	}
}

func TestStopSurfaceID(t *testing.T) {
	surfaces := []types.Surface{
		{ID: 1, Diameter: 50.0},
		{ID: 2, Diameter: 10.0},
		{ID: 3, Diameter: 20.0},
	}
	idx := stopSurfaceIndex(surfaces)
	if idx != 1 {
		t.Errorf("Stop index = %d, want 1", idx)
	}
}

func TestComputeTotalTrack(t *testing.T) {
	sys, gc := singletSystem()
	result := Compute(sys, 0.00058756, gc, 0, nil)
	want := 10.0 + 100.0
	if math.Abs(result.TotalTrack-want) > 1e-10 {
		t.Errorf("TotalTrack = %v, want %v", result.TotalTrack, want)
	}
}
