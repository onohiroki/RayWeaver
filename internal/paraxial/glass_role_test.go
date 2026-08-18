package paraxial

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

func roleGC() *glass.Catalog {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "G1", ND: 1.5, VD: 60})
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "G2", ND: 1.7, VD: 30})
	return gc
}

// cookeTripletSurfaces is the US2645157 Cooke-triplet geometry (SK18/SF12/SK18).
func cookeTripletSurfaces() []types.Surface {
	return []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 1 / 10.2871491742, Thickness: 1.524, Material: types.Material{Key: "SK18"}, Diameter: 10.0},
		{ID: 2, Type: types.Sphere, Curvature: 1 / -239.3967954752, Thickness: 2.3368, Material: types.Material{}, Diameter: 10.0},
		{ID: 3, Type: types.Sphere, Curvature: 1 / -12.8269871730, Thickness: 0.508, Material: types.Material{Key: "SF12"}, Diameter: 6.0},
		{ID: 4, Type: types.Sphere, Curvature: 1 / 10.5917184406, Thickness: 1.4986, Material: types.Material{}, Diameter: 6.0},
		{ID: 5, Type: types.Sphere, Curvature: 0.0, Thickness: 1.016, Material: types.Material{}, Diameter: 3.78},
		{ID: 6, Type: types.Sphere, Curvature: 1 / 61.8456294200, Thickness: 1.524, Material: types.Material{Key: "SK18"}, Diameter: 6.0},
		{ID: 7, Type: types.Sphere, Curvature: 1 / -10.0074859032, Thickness: 21.36695183553, Material: types.Material{}, Diameter: 6.0},
		{ID: 8, Type: types.Sphere, Curvature: 0.0, Thickness: 0.0, Material: types.Material{}, Diameter: 44.0},
	}
}

func cookeGC() *glass.Catalog {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "SK18", ND: 1.63854, VD: 55.42})
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "SF12", ND: 1.64831, VD: 33.84})
	return gc
}

// TestGlassRolesCookeTriplet verifies the y²-weighted role judgement on the
// US2645157 Cooke triplet: the negative middle element has the largest bare
// |phi|, yet its marginal-ray height (near the stop) makes its chromatic
// weight the couple's flint — the lowest vd target — while the positive outer
// elements sit crown-side of it.
func TestGlassRolesCookeTriplet(t *testing.T) {
	gc := cookeGC()
	s := cookeTripletSurfaces()
	surface.Precompute(s)

	roles := GlassRoles(s, gc)
	if len(roles) != 3 {
		t.Fatalf("expected 3 elements, got %d", len(roles))
	}
	if roles[0].W <= 0 || roles[2].W <= 0 || roles[1].W >= 0 {
		t.Errorf("chromatic-weight signs wrong: %v %v %v", roles[0].W, roles[1].W, roles[2].W)
	}
	if math.Abs(roles[1].Phi) <= math.Abs(roles[0].Phi) {
		t.Errorf("middle |phi| %v should exceed front |phi| %v (frequency-dependent case)",
			roles[1].Phi, roles[0].Phi)
	}
	if roles[1].VTarget >= 45 {
		t.Errorf("middle vd target %v, want flint-side (< 45)", roles[1].VTarget)
	}
	if roles[0].VTarget < 45 || roles[2].VTarget < 45 {
		t.Errorf("outer crown-side vd targets %v / %v, want >= 45", roles[0].VTarget, roles[2].VTarget)
	}
	if roles[1].VTarget >= roles[0].VTarget || roles[1].VTarget >= roles[2].VTarget {
		t.Errorf("middle vd target %v not the lowest (%v, %v)", roles[1].VTarget, roles[0].VTarget, roles[2].VTarget)
	}
	for _, r := range roles {
		if r.VTarget < glassRoleVDMin || r.VTarget > glassRoleVDMax {
			t.Errorf("vd target %v outside the glass range", r.VTarget)
		}
		if r.NDTarget < glassRoleNDMin || r.NDTarget > glassRoleNDMax {
			t.Errorf("nd target %v outside the glass range", r.NDTarget)
		}
	}
}

// TestGlassRolesPositiveFlint verifies the sign-free role rule on an opposite
// couple where the negative element dominates: a positive element with a
// smaller chromatic weight than its strong negative partner is steered to a
// flint (low vd) — the double-Gauss "positive flint" — and the strong negative
// is the couple's crown.
func TestGlassRolesPositiveFlint(t *testing.T) {
	gc := roleGC()
	// Positive element [1 2] phi = 0.5·(0.05−0.034) = 0.008; then a stronger
	// negative element [3 4] phi = 0.7·(−0.02−0.0086) = −0.02, so |w_neg| > |w_pos|
	// at comparable marginal heights.
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.05, Thickness: 2.0, Material: types.Material{Key: "G1"}},
		{ID: 2, Type: types.Sphere, Curvature: 0.034, Thickness: 5.0, Material: types.Material{}},
		{ID: 3, Type: types.Sphere, Curvature: -0.02, Thickness: 2.0, Material: types.Material{Key: "G2"}},
		{ID: 4, Type: types.Sphere, Curvature: 0.0086, Thickness: 50.0, Material: types.Material{}},
		{ID: 5, Type: types.Sphere, Curvature: 0.0, Thickness: 0.0, Material: types.Material{}},
	}
	surface.Precompute(surfaces)

	roles := GlassRoles(surfaces, gc)
	if len(roles) != 2 {
		t.Fatalf("expected 2 elements, got %d", len(roles))
	}
	if roles[0].W <= 0 || roles[1].W >= 0 {
		t.Fatalf("chromatic weights should be +/−, got %v / %v", roles[0].W, roles[1].W)
	}
	wPos := math.Abs(roles[0].W)
	wNeg := math.Abs(roles[1].W)
	if wNeg <= wPos {
		t.Fatalf("test setup: negative |w| %v not above positive |w| %v", wNeg, wPos)
	}
	// The strong negative element (larger |w|) is the dominant crown of the
	// couple regardless of its negative power.
	if roles[1].Role != "dominant" {
		t.Errorf("negative element role = %q, want dominant", roles[1].Role)
	}
	if roles[1].VTarget != glassRoleCrownRefV {
		t.Errorf("negative element vd target %v, want crown reference %v", roles[1].VTarget, glassRoleCrownRefV)
	}
	// The positive element is the compensating flint: the sign-free rule
	// inverts the naive "positive power → crown" assignment.
	if roles[0].Role != "compensating" {
		t.Errorf("positive element role = %q, want compensating (positive flint)", roles[0].Role)
	}
	want := glassRoleCrownRefV * wPos / wNeg
	if math.Abs(roles[0].VTarget-want) > 1e-6 {
		t.Errorf("positive element vd target %v, want the achromat ratio %v", roles[0].VTarget, want)
	}
	if roles[0].VTarget >= roles[1].VTarget {
		t.Errorf("positive element vd target %v not below the negative crown's %v", roles[0].VTarget, roles[1].VTarget)
	}
}

// TestGlassRolesAchromatRatio verifies the flint target of the compensating
// member follows the couple achromatism V_f = V_c·|w_f|/|w_c| on a clean
// two-element couple with comparable marginal heights.
func TestGlassRolesAchromatRatio(t *testing.T) {
	gc := roleGC()
	// Positive element [1 2] phi = 0.5·(0.05−0.034) = 0.008; negative element
	// [3 4] phi = 0.7·(−0.01−(−0.0043)) = −0.004. The marginal heights are
	// comparable, so the ratio V*_flint/60 ≈ |w_neg|/|w_pos|.
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.05, Thickness: 2.0, Material: types.Material{Key: "G1"}},
		{ID: 2, Type: types.Sphere, Curvature: 0.034, Thickness: 5.0, Material: types.Material{}},
		{ID: 3, Type: types.Sphere, Curvature: -0.01, Thickness: 2.0, Material: types.Material{Key: "G2"}},
		{ID: 4, Type: types.Sphere, Curvature: -0.0043, Thickness: 50.0, Material: types.Material{}},
		{ID: 5, Type: types.Sphere, Curvature: 0.0, Thickness: 0.0, Material: types.Material{}},
	}
	surface.Precompute(surfaces)

	roles := GlassRoles(surfaces, gc)
	if len(roles) != 2 {
		t.Fatalf("expected 2 elements, got %d", len(roles))
	}
	if roles[0].Role != "dominant" || roles[0].VTarget != glassRoleCrownRefV {
		t.Fatalf("positive element role=%q vd=%v, want dominant crown", roles[0].Role, roles[0].VTarget)
	}
	wPos := math.Abs(roles[0].W)
	wNeg := math.Abs(roles[1].W)
	if roles[1].Role != "compensating" {
		t.Fatalf("negative element role=%q, want compensating", roles[1].Role)
	}
	if wNeg >= wPos {
		t.Fatalf("test setup: negative |w| %v not below positive |w| %v", wNeg, wPos)
	}
	want := glassRoleCrownRefV * wNeg / wPos
	if math.Abs(roles[1].VTarget-want) > 1e-6 {
		t.Errorf("compensating vd target %v, want the achromat ratio %v", roles[1].VTarget, want)
	}
	// The nd target lands on the normal-glass line, plus the lanthanum-crown
	// boost for the positive element.
	if math.Abs(roles[0].NDTarget-(ndLine(roles[0].VTarget)+glassRoleNDBoostPos)) > 1e-9 {
		t.Errorf("positive nd target %v, want line+boost %v",
			roles[0].NDTarget, ndLine(roles[0].VTarget)+glassRoleNDBoostPos)
	}
	if math.Abs(roles[1].NDTarget-ndLine(roles[1].VTarget)) > 1e-9 {
		t.Errorf("negative nd target %v, want line %v", roles[1].NDTarget, ndLine(roles[1].VTarget))
	}
}

// TestGlassRolesSoloNeutral verifies an element without an opposite-sign
// neighbour keeps the neutral centre target: its glass choice has no paired
// chromatic role and is left to the explicit colour merit terms.
func TestGlassRolesSoloNeutral(t *testing.T) {
	gc := roleGC()
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 10.0, Material: types.Material{Key: "G1"}},
		{ID: 2, Type: types.Sphere, Curvature: -0.005, Thickness: 100.0, Material: types.Material{}},
		{ID: 3, Type: types.Sphere, Curvature: 0.0, Thickness: 0.0, Material: types.Material{}},
	}
	surface.Precompute(surfaces)

	roles := GlassRoles(surfaces, gc)
	if len(roles) != 1 {
		t.Fatalf("expected 1 element, got %d", len(roles))
	}
	if roles[0].Role != "neutral" {
		t.Errorf("singlet role = %q, want neutral", roles[0].Role)
	}
	if roles[0].VTarget != glassRoleVDNeutral {
		t.Errorf("singlet vd target %v, want neutral %v", roles[0].VTarget, glassRoleVDNeutral)
	}
}
