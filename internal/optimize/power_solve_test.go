package optimize

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

func powerSolveTripletSurfaces() []types.Surface {
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

// TestPowerSolvePreservesElementPowers drives a glass-only applyVariables with a
// power-preserving solve active and verifies each pinned element's thin-lens
// power stays at its initial value even when the glass is pushed hard.
func TestPowerSolvePreservesElementPowers(t *testing.T) {
	gc := tripletGC()
	surfaces := powerSolveTripletSurfaces()
	surface.Precompute(surfaces)
	targets := map[int]float64{
		1: paraxial.ElementPowerForSurface(surfaces, paraxial.DLine, gc, 1), // +0.0647
		3: paraxial.ElementPowerForSurface(surfaces, paraxial.DLine, gc, 3), // -0.1118
		6: paraxial.ElementPowerForSurface(surfaces, paraxial.DLine, gc, 6), // +0.0741
	}

	var vars []Variable
	for _, id := range []int{1, 3, 6} {
		vars = append(vars,
			Variable{Name: "s_nd", SurfaceID: id, Param: "nd", Min: 1.4, Max: 2.0, Config: "config1"},
			Variable{Name: "s_vd", SurfaceID: id, Param: "vd", Min: 20, Max: 90, Config: "config1"},
		)
	}
	cfg := Config{
		Surfaces:          powerSolveTripletSurfaces(),
		GlassCatalog:      gc,
		Variables:         vars,
		PowerSolveSurfaces: []int{2, 4, 7},
	}
	opt := NewOptimizer(cfg)

	// Push every glass variable to an extreme (nd=2.0, vd=90), as a pure
	// chromatic merit tends to do, and re-apply.
	x := make([]float64, len(vars))
	for i := range vars {
		if vars[i].Param == "nd" {
			x[i] = 2.0
		} else {
			x[i] = 90.0
		}
	}
	surfMap, tempGC := opt.applyVariables(x)
	eff := effectiveGC(gc, tempGC)
	for cid, s := range surfMap {
		surface.Precompute(s)
		for _, tid := range []int{1, 3, 6} {
			got := paraxial.ElementPowerForSurface(s, paraxial.DLine, eff, tid)
			if math.Abs(got-targets[tid]) > 1e-9 {
				t.Errorf("config %s: element surf %d power changed: want %v got %v", cid, tid, targets[tid], got)
			}
		}
	}
}

// TestPowerSolveDisabledByDefault verifies the solve is a no-op when no solve
// surfaces are configured (the standard optimise path is unchanged).
func TestPowerSolveDisabledByDefault(t *testing.T) {
	gc := tripletGC()
	cfg := Config{
		Surfaces:     powerSolveTripletSurfaces(),
		GlassCatalog: gc,
		Variables: []Variable{
			{Name: "s_nd", SurfaceID: 1, Param: "nd", Min: 1.4, Max: 2.0, Config: "config1"},
		},
	}
	opt := NewOptimizer(cfg)
	if opt.powerSolve != nil && len(opt.powerSolve) > 0 {
		t.Error("powerSolve should be empty by default")
	}
}

// TestGlassPhaseLocksNonGlassAndPreservesPower drives the escape glass phase:
// EnterGlassPhase must lock every variable except nd/vd (Min==Max) so only the
// glasses move, enable the power-preserving solve, and route the merit to the
// glass terms; ExitGlassPhase must restore the original variable bounds and
// disable the solve. The element powers must stay constant while power-solve is
// active even when the glasses are pushed.
func TestGlassPhaseLocksNonGlassAndPreservesPower(t *testing.T) {
	gc := tripletGC()
	// One curvature variable per element (back surfaces) + nd/vd, mirroring the
	// double-Gauss escape: all elements have both curvature and glass free.
	var vars []Variable
	for _, id := range []int{2, 4, 7} {
		vars = append(vars, Variable{Name: "c_back", SurfaceID: id, Param: "curvature", Min: -0.5, Max: 0.5, Config: "config1"})
	}
	for _, id := range []int{1, 3, 6} {
		vars = append(vars,
			Variable{Name: "nd", SurfaceID: id, Param: "nd", Min: 1.4, Max: 2.0, Config: "config1"},
			Variable{Name: "vd", SurfaceID: id, Param: "vd", Min: 20, Max: 90, Config: "config1"},
		)
	}
	cfg := Config{
		Surfaces:          powerSolveTripletSurfaces(),
		GlassCatalog:      gc,
		Variables:         vars,
		PowerSolveSurfaces: []int{2, 4, 7},
	}
	opt := NewOptimizer(cfg)

	// Set up a glass (colour-only) merit with one lateral_color term on field 0.
	opt.SetGlassMerit("config1", []types.MeritTerm{
		{Kind: MeritLongitudinalColor, Wavelength: 0.0004358, Wavelength2: 0.0006563, Weight: 1.0},
	})

	// Build a realistic current state x where every var is set to a value
	// midway in its range (a post-escape layout) and the glasses at the bounds.
	x := make([]float64, len(vars))
	for i := range vars {
		switch vars[i].Param {
		case "curvature":
			x[i] = 0.1
		case "nd":
			x[i] = 1.9
		case "vd":
			x[i] = 35
		}
	}

	// Before the glass phase, curvatures are free (Min != Max).
	vi := opt.Variables()
	for i, v := range vars {
		if v.Param == "curvature" && vi[i].Min == vi[i].Max {
			t.Fatalf("curvature var initially locked (Min==Max)")
		}
	}

	// Snapshot expected element powers at the pre-phase state (power-solve off).
	preSurf, preGC := opt.applyVariables(x)
	eff := effectiveGC(gc, preGC)
	for _, s := range preSurf {
		surface.Precompute(s)
	}
	targets := map[int]float64{}
	if s := preSurf["config1"]; true {
		for _, id := range []int{1, 3, 6} {
			targets[id] = paraxial.ElementPowerForSurface(s, paraxial.DLine, eff, id)
		}
	}

	// Enter the glass phase: lock non-glass vars, enable power-solve, glass merit.
	opt.EnterGlassPhase(x)
	if !opt.powerSolveEnabled {
		t.Error("powerSolveEnabled should be true after EnterGlassPhase")
	}
	// Verify curvature is locked and nd/vd is free.
	vi = opt.Variables()
	paramByVar := map[int]string{}
	for i, v := range vars {
		paramByVar[i] = v.Param
	}
	for i := range vars {
		if paramByVar[i] == "curvature" && vi[i].Min != vi[i].Max {
			t.Errorf("curvature var not locked after EnterGlassPhase")
		}
		if (paramByVar[i] == "nd" || paramByVar[i] == "vd") && vi[i].Min == vi[i].Max {
			t.Errorf("glass var locked (Min==Max) during glass phase")
		}
	}
	// The glass merit routes the merit: evaluate the glass merit value (colour).
	if !opt.glassMeritActive {
		t.Error("glassMeritActive should be true after EnterGlassPhase")
	}
	merit := opt.EvaluateMerit(x)
	if merit < 0 || math.IsNaN(merit) || math.IsInf(merit, 0) {
		t.Errorf("glass-phase merit invalid: %v", merit)
	}

	// With the solve on, element powers must stay at the pre-phase targets even
	// though the glass optimises.
	glassSurf, glassGC := opt.applyVariables(x)
	effG := effectiveGC(gc, glassGC)
	if s := glassSurf["config1"]; true {
		surface.Precompute(s)
		for _, id := range []int{1, 3, 6} {
			got := paraxial.ElementPowerForSurface(s, paraxial.DLine, effG, id)
			if math.Abs(got-targets[id]) > 1e-9 {
				t.Errorf("glass-phase element power %d changed: want %v got %v", id, targets[id], got)
			}
		}
	}

	// Exit the glass phase: restore bounds, disable solve.
	opt.ExitGlassPhase()
	if opt.powerSolveEnabled {
		t.Error("powerSolveEnabled should be false after ExitGlassPhase")
	}
	if opt.glassMeritActive {
		t.Error("glassMeritActive should be false after ExitGlassPhase")
	}
	vi = opt.Variables()
	for i := range vars {
		if paramByVar[i] == "curvature" && vi[i].Min == vi[i].Max {
			t.Errorf("curvature var not restored after ExitGlassPhase")
		}
	}
}
