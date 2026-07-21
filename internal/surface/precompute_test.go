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
			Radius:    100.0,
			Thickness: 10.0,
		},
		{
			Type:      types.Sphere,
			Radius:    -100.0,
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
		{Type: types.Sphere, Radius: 0.0, Thickness: 50.0},
		{Type: types.Sphere, Radius: 0.0, Thickness: 0.0},
	}
	Precompute(surfaces)
	if surfaces[0].ParaxialRadius != 0.0 {
		t.Errorf("Plane ParaxialRadius = %v, want 0", surfaces[0].ParaxialRadius)
	}
}
