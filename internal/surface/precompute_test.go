package surface

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
)

func TestPrecomputeSpherical(t *testing.T) {
	surfaces := []types.Surface{
		{
			Type:      types.Sphere,
			Curvature: 0.01,
			Thickness: 10.0,
		},
		{
			Type:      types.Sphere,
			Curvature: -0.01,
			Thickness: 100.0,
		},
	}
	Precompute(surfaces)
	if surfaces[0].ParaxialRadius != 100.0 {
		t.Errorf("ParaxialRadius[0] = %v, want 100", surfaces[0].ParaxialRadius)
	}
	if surfaces[1].ParaxialRadius != -100.0 {
		t.Errorf("ParaxialRadius[1] = %v, want -100", surfaces[1].ParaxialRadius)
	}
	// Check Z positions
	if surfaces[0].LocalToGlobal[2][3] != 0 {
		t.Errorf("Surface 0 Z = %v, want 0", surfaces[0].LocalToGlobal[2][3])
	}
	if math.Abs(surfaces[1].LocalToGlobal[2][3]-10.0) > 1e-12 {
		t.Errorf("Surface 1 Z = %v, want 10", surfaces[1].LocalToGlobal[2][3])
	}
}

func TestPrecomputePlane(t *testing.T) {
	surfaces := []types.Surface{
		{Type: types.Sphere, Curvature: 0.0, Thickness: 50.0},
		{Type: types.Sphere, Curvature: 0.0, Thickness: 0.0},
	}
	Precompute(surfaces)
	if surfaces[0].ParaxialRadius != 0.0 {
		t.Errorf("Plane ParaxialRadius = %v, want 0", surfaces[0].ParaxialRadius)
	}
}

// TestPrecomputeFold walks a folded Schmidt-style layout: mirror at physical
// Z=800 (tilt-180 decenter reflect), then surfaces at decreasing physical Z.
// It checks Reflects(), PhysicalZ, and the folded frame orientation.
func TestPrecomputeFold(t *testing.T) {
	reflect := []types.DecenterStep{{Tilt: types.Vec3{X: 0, Y: 180, Z: 0}, Scope: types.ScopeBoth}}
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0, Thickness: 800.0, Material: "AIR"},
		{ID: 2, Type: types.Sphere, Curvature: 1.0 / 800.0, Thickness: 340.0, Material: "AIR", Reflect: true, Decenter: reflect},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 42.0, Material: "AIR"},
		{ID: 4, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: "AIR"},
	}
	Precompute(surfaces)

	if surfaces[1].Reflects() != true {
		t.Error("surface 2 (mirror) should Reflects() == true")
	}
	if surfaces[0].Reflects() || surfaces[3].Reflects() {
		t.Error("non-mirror surfaces should not Reflects()")
	}

	// Physical Z: stop at 0, mirror at 800, then back toward the stop.
	wantZ := []float64{0, 800, 800 - 340, 800 - 340 - 42}
	for i, want := range wantZ {
		if math.Abs(surfaces[i].PhysicalZ-want) > 1e-9 {
			t.Errorf("PhysicalZ[%d] = %v, want %v", i, surfaces[i].PhysicalZ, want)
		}
	}

	// Mirror frame: local +Z maps to global -Z (Y-tilt 180 deg flips Z).
	// LocalToGlobal.MultiplyVector({0,0,1}).Z must be ~ -1.
	zAxis := surfaces[1].LocalToGlobal.MultiplyVector(types.Vec3{Z: 1})
	if math.Abs(zAxis.Z+1) > 1e-9 {
		t.Errorf("mirror local +Z maps to global Z=%v, want -1", zAxis.Z)
	}
	// Y is preserved under a Y-tilt.
	if math.Abs(zAxis.Y) > 1e-9 {
		t.Errorf("mirror local +Z Y component = %v, want 0", zAxis.Y)
	}

	// The helper PhysicalZ() must agree with the precomputed values.
	z := PhysicalZ(surfaces)
	for i := range surfaces {
		if math.Abs(z[i]-surfaces[i].PhysicalZ) > 1e-9 {
			t.Errorf("PhysicalZ() helper[%d] = %v, want %v", i, z[i], surfaces[i].PhysicalZ)
		}
	}
}
