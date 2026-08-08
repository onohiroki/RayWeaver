package paraxial

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

func powGC() *glass.Catalog {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "G1", ND: 1.5, VD: 60})
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "G2", ND: 1.7, VD: 30})
	return gc
}

func TestElementPowersSinglet(t *testing.T) {
	gc := powGC()
	// Thin lens in air: n=1.5, R1=100, R2=-200 -> (n-1)(c1-c2) = 0.5*(0.01+0.005).
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 10.0, Material: types.Material{Key: "G1"}},
		{ID: 2, Type: types.Sphere, Curvature: -0.005, Thickness: 100.0, Material: types.Material{}},
	}
	surface.Precompute(surfaces)

	got := ElementPowers(surfaces, DLine, gc)
	if len(got) != 1 {
		t.Fatalf("expected 1 element, got %d (%v)", len(got), got)
	}
	if math.Abs(got[0]-0.0075) > 1e-9 {
		t.Errorf("element power = %v, want 0.0075", got[0])
	}
}

func TestElementPowersCementedDoublet(t *testing.T) {
	gc := powGC()
	// Two glass bodies: [air->G1, G1->G2] and [G1->G2, G2->air].
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.02, Thickness: 0.1, Material: types.Material{Key: "G1"}},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 0.1, Material: types.Material{Key: "G2"}},
		{ID: 3, Type: types.Sphere, Curvature: 0.005, Thickness: 50.0, Material: types.Material{}},
	}
	surface.Precompute(surfaces)

	got := ElementPowers(surfaces, DLine, gc)
	if len(got) != 2 {
		t.Fatalf("expected 2 elements, got %d (%v)", len(got), got)
	}
	want0 := (1.5-1)*0.02 + (1.7-1.5)*(-0.01)
	want1 := (1.7-1.5)*(-0.01) + (1-1.7)*0.005
	if math.Abs(got[0]-want0) > 1e-9 {
		t.Errorf("element[0] = %v, want %v", got[0], want0)
	}
	if math.Abs(got[1]-want1) > 1e-9 {
		t.Errorf("element[1] = %v, want %v", got[1], want1)
	}
}

func TestElementPowersMirror(t *testing.T) {
	gc := powGC()
	// Concave mirror in the fold model: beam-frame R = +800, power = -2/800.
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 1.0 / 800.0, Thickness: 100.0, Reflect: true,
			Decenter: []types.DecenterStep{{Tilt: types.Vec3{Y: 180}, Scope: types.ScopeBoth}}},
	}
	surface.Precompute(surfaces)

	got := ElementPowers(surfaces, DLine, gc)
	if len(got) != 1 {
		t.Fatalf("expected 1 element, got %d (%v)", len(got), got)
	}
	if math.Abs(got[0]-(-2.0/800.0)) > 1e-9 {
		t.Errorf("mirror power = %v, want %v", got[0], -2.0/800.0)
	}
}

func TestElementPowersAirOnly(t *testing.T) {
	gc := powGC()
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0, Thickness: 10.0, Material: types.Material{}},
		{ID: 2, Type: types.Sphere, Curvature: 0, Thickness: 100.0, Material: types.Material{}},
	}
	surface.Precompute(surfaces)

	got := ElementPowers(surfaces, DLine, gc)
	if len(got) != 0 {
		t.Errorf("expected no elements, got %v", got)
	}
}

func TestElementPowersSumApproximatesSystemPower(t *testing.T) {
	gc := powGC()
	// The thin-lens element power should be close to the real (thick) system
	// power 1/EFL; the difference is the thickness term, small here.
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 5.0, Material: types.Material{Key: "G1"}},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 100.0, Material: types.Material{}},
	}
	surface.Precompute(surfaces)

	got := ElementPowers(surfaces, DLine, gc)
	pr := Compute(types.System{Surfaces: surfaces}, DLine, gc, 0, nil)
	total := 0.0
	for _, p := range got {
		total += p
	}
	realPower := 1.0 / pr.FocalLength
	if math.Abs(total-realPower) > 0.1*math.Abs(realPower) {
		t.Errorf("sum of element powers %v not within 10%% of 1/EFL %v", total, realPower)
	}
}
