package paraxial

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

func TestSolveElementPowerPreservesPower(t *testing.T) {
	gc := cookeGC()
	surfaces := cookeTripletSurfaces()
	surface.Precompute(surfaces)

	// Element 1 = surfaces 1,2 (SK18 front). Pin the back surface 2.
	target := ElementPowerForSurface(surfaces, DLine, gc, 1)
	if target == 0 {
		t.Fatal("initial element power is 0")
	}

	// Swap the element's glass dramatically: surface 1 becomes a different
	// material (nd 1.9, vd 25 instead of SK18). Mirror the real variable path
	// by mutating the surface material before the solve.
	surfaces[0].Material = types.Material{ND: 1.9, VD: 25.0}

	if !SolveElementPower(surfaces, gc, 2, target) {
		t.Fatal("SolveElementPower returned false")
	}
	// The solver writes Curvature; Precompute refreshes ParaxialRadius from it,
	// exactly as the optimizer does before consuming the surfaces.
	surface.Precompute(surfaces)

	after := ElementPowerForSurface(surfaces, DLine, gc, 1)
	if math.Abs(after-target) > 1e-9 {
		t.Errorf("power not preserved: before=%v after=%v", target, after)
	}
}

func TestSolveElementPowerSkipsMirror(t *testing.T) {
	gc := cookeGC()
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 2, Material: types.Material{Key: "SK18"}},
		{ID: 2, Type: types.Sphere, Curvature: 0.1, Thickness: 1, Material: types.Material{}},
		{ID: 3, Type: types.Sphere, Curvature: -0.02, Thickness: 0, Material: types.Material{}, Reflect: true},
	}
	surface.Precompute(surfaces)

	// A mirror surface cannot be pinned (no dispersion drives its power).
	if SolveElementPower(surfaces, gc, 3, 0.1) {
		t.Error("mirror should not be solvable")
	}
}
