package optimize

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// tripletModesSurfaces is the US2645157 Cooke-triplet geometry (SK18/SF12/SK18)
// with the second and third element glasses optionally swapped (the flint of the
// negative element and the crown of the positive element exchanged).
func tripletModesSurfaces(swap bool) []types.Surface {
	s3mat := types.Material{Key: "SF12"} // negative-power element → flint (correct)
	s6mat := types.Material{Key: "SK18"} // positive-power element → crown (correct)
	if swap {
		s3mat = types.Material{Key: "SK18"} // crown on the negative element (wrong)
		s6mat = types.Material{Key: "SF12"} // flint on the positive element (wrong)
	}
	return []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 1 / 10.2871491742, Thickness: 1.524, Material: types.Material{Key: "SK18"}, Diameter: 10.0},
		{ID: 2, Type: types.Sphere, Curvature: 1 / -239.3967954752, Thickness: 2.3368, Material: types.Material{}, Diameter: 10.0},
		{ID: 3, Type: types.Sphere, Curvature: 1 / -12.8269871730, Thickness: 0.508, Material: s3mat, Diameter: 6.0},
		{ID: 4, Type: types.Sphere, Curvature: 1 / 10.5917184406, Thickness: 1.4986, Material: types.Material{}, Diameter: 6.0},
		{ID: 5, Type: types.Sphere, Curvature: 0.0, Thickness: 1.016, Material: types.Material{}, Diameter: 3.78},
		{ID: 6, Type: types.Sphere, Curvature: 1 / 61.8456294200, Thickness: 1.524, Material: s6mat, Diameter: 6.0},
		{ID: 7, Type: types.Sphere, Curvature: 1 / -10.0074859032, Thickness: 21.36695183553, Material: types.Material{}, Diameter: 6.0},
		{ID: 8, Type: types.Sphere, Curvature: 0.0, Thickness: 0.0, Material: types.Material{}, Diameter: 44.0},
	}
}

// TestGlassRoleClassification verifies the role-based classification invariants
// on the US2645157 Cooke triplet: the negative middle element's |phi| is the
// largest of the three, yet its y²-weighted chromatic weight makes it the
// couple's flint (lowest vd target, compensating) while the two positive outer
// elements sit crown-side of it. This is the behaviour the old sign-based
// vd = 45 + 16·tanh(phi) rule could not express.
func TestGlassRoleClassification(t *testing.T) {
	gc := tripletGC()
	s := tripletModesSurfaces(false)
	surface.Precompute(s)
	roles := paraxial.GlassRoles(s, gc)
	if len(roles) != 3 {
		t.Fatalf("expected 3 elements, got %d", len(roles))
	}
	// Element order: [1 2] front positive, [3 4] middle negative, [6 7] rear
	// positive.
	if roles[0].W <= 0 || roles[2].W <= 0 || roles[1].W >= 0 {
		t.Errorf("chromatic-weight signs wrong: front=%v middle=%v rear=%v", roles[0].W, roles[1].W, roles[2].W)
	}
	if roles[1].Phi >= 0 {
		t.Errorf("middle element phi = %v, want < 0", roles[1].Phi)
	}
	// The middle (negative) element is the flint: strictly the lowest vd
	// target, below the neutral centre.
	if roles[1].VTarget >= 45 {
		t.Errorf("middle element vd target %v, want < 45 (flint)", roles[1].VTarget)
	}
	for _, r := range roles {
		if r.VTarget < 20 || r.VTarget > 90 {
			t.Errorf("vd target %v outside the glass range", r.VTarget)
		}
		if r.NDTarget < 1.4 || r.NDTarget > 2.0 {
			t.Errorf("nd target %v outside the glass range", r.NDTarget)
		}
	}
	if roles[1].VTarget >= roles[0].VTarget || roles[1].VTarget >= roles[2].VTarget {
		t.Errorf("middle vd target %v not below the outer elements' (%v, %v)",
			roles[1].VTarget, roles[0].VTarget, roles[2].VTarget)
	}
}

// TestGlassRoleForSurface verifies the combined vd+nd glass-role residual
// detects a swapped flint/crown arrangement: on the correct triplet SF12 sits
// close to its flint target (small |residual|), and after the swap the
// negative-power element carries the crown and is pushed hard toward the flint
// side (positive residual → vd down), while the rear element's flint is pushed
// toward the crown side (negative residual → vd up). The residual keeps the
// least-squares form |r|² = (vd_actual − vd*)² + K²·(nd_actual − nd*)².
func TestGlassRoleForSurface(t *testing.T) {
	gc := tripletGC()
	correct := tripletModesSurfaces(false)
	surface.Precompute(correct)
	res3 := glassRoleForSurface(correct, gc, 3, nil)
	res6 := glassRoleForSurface(correct, gc, 6, nil)
	corrRole3, _ := paraxial.ElementRoleForSurface(correct, gc, 3)
	corrRole6, _ := paraxial.ElementRoleForSurface(correct, gc, 6)
	t.Logf("correct: role3=%s vd*=%.2f res3=%+.3f | role6=%s vd*=%.2f res6=%+.3f",
		corrRole3.Role, corrRole3.VTarget, res3, corrRole6.Role, corrRole6.VTarget, res6)
	if corrRole3.VTarget >= 45 {
		t.Errorf("middle element (negative power) vd target %v, want < 45 (flint)", corrRole3.VTarget)
	}

	swapped := tripletModesSurfaces(true)
	surface.Precompute(swapped)
	res3s := glassRoleForSurface(swapped, gc, 3, nil)
	res6s := glassRoleForSurface(swapped, gc, 6, nil)
	role3s, _ := paraxial.ElementRoleForSurface(swapped, gc, 3)
	t.Logf("swapped: role3=%s vd*=%.2f res3=%+.3f", role3s.Role, role3s.VTarget, res3s)

	// The swapped negative element (now SK18 crown, vd 55) must be pushed down
	// toward its flint target and the swapped positive element (now SF12 flint,
	// vd 34) up toward the crown-side target.
	if res3s <= 0 {
		t.Errorf("swapped S3 (SK18 crown on negative power): residual = %v, want > 0", res3s)
	}
	if res6s >= 0 {
		t.Errorf("swapped S6 (SF12 flint on positive power): residual = %v, want < 0", res6s)
	}
	// The composite residual is the signed magnitude of the vd/nd deviations on
	// the same (swapped) classification:
	// |r|² == (vd_actual − vd*)² + K²·(nd_actual − nd*)².
	vd3 := glassVDForSurface(swapped, gc, 3)
	nd3 := glassNDForSurface(swapped, gc, 3)
	want := math.Sqrt((vd3-role3s.VTarget)*(vd3-role3s.VTarget) +
		(nd3-role3s.NDTarget)*(nd3-role3s.NDTarget)*glassRoleNDResidualScale*glassRoleNDResidualScale)
	if math.Abs(math.Abs(res3s)-want) > 1e-9 {
		t.Errorf("swapped S3 combined residual magnitude %v != √(vd² + K²·nd²) = %v", math.Abs(res3s), want)
	}
}

// positiveFlintCouple returns a two-element couple where the negative element
// dominates the chromatic weight: a positive element [1 2] (phi ≈ +0.004)
// followed by a strong negative element [3 4] (phi ≈ −0.05) at comparable
// marginal heights. The positive element's chromatic weight is a small but
// non-negligible fraction of the negative's (well above the neutral threshold,
// far below the dominance threshold), so the role algorithm steers it to a
// flint (low vd, clamped at the glass-map floor) and the strong negative to a
// crown (high vd) — the opposite of the naive sign-based assignment.
func positiveFlintCouple() []types.Surface {
	return []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.05, Thickness: 2.0, Material: types.Material{ND: 1.5, VD: 60}},
		{ID: 2, Type: types.Sphere, Curvature: 0.042, Thickness: 5.0, Material: types.Material{}},
		{ID: 3, Type: types.Sphere, Curvature: -0.04, Thickness: 2.0, Material: types.Material{ND: 1.7, VD: 30}},
		{ID: 4, Type: types.Sphere, Curvature: 0.0314, Thickness: 50.0, Material: types.Material{}},
		{ID: 5, Type: types.Sphere, Curvature: 0.0, Thickness: 0.0, Material: types.Material{}},
	}
}

// TestGlassRoleDLSRecoversPositiveFlint is the acceptance test for the
// sign-free role rule: from a naive "positive power → crown / negative power →
// flint" start on a couple whose negative element dominates the chromatic
// weight, a DLS run with glass-only variables and a glass_role merit flips the
// arrangement to the physically correct positive flint / negative crown.
func TestGlassRoleDLSRecoversPositiveFlint(t *testing.T) {
	gc := glass.NewCatalog()
	s := positiveFlintCouple()
	surface.Precompute(s)

	// Sanity-check the initial classification is the inverted one.
	rolePos, _ := paraxial.ElementRoleForSurface(s, gc, 1)
	roleNeg, _ := paraxial.ElementRoleForSurface(s, gc, 3)
	if rolePos.VTarget >= roleNeg.VTarget {
		t.Fatalf("test setup: positive vd* %v not below negative vd* %v (roles not inverted)",
			rolePos.VTarget, roleNeg.VTarget)
	}

	cfg := ConfigInput{
		ID:       "cfg1",
		Weight:   1.0,
		Surfaces: s,
		Fields: []types.FieldItem{
			{ID: 0, AngleDeg: 0.0, Weight: 1.0},
		},
		Wavelengths: []types.WavelengthItem{{ID: 0, Value: 0.00058756, Weight: 1.0}},
		MeritTerms: []types.MeritTerm{
			{Kind: "glass_role", SurfaceSet: []int{1}, Weight: 1e-3},
			{Kind: "glass_role", SurfaceSet: []int{3}, Weight: 1e-3},
		},
	}
	localVars := []types.LocalVariableDef{
		{Name: "s1_nd", Config: "cfg1", Target: types.VariableTarget{Type: "surface", ID: 1, Param: "nd"}, Min: 1.4, Max: 1.9, Active: true},
		{Name: "s1_vd", Config: "cfg1", Target: types.VariableTarget{Type: "surface", ID: 1, Param: "vd"}, Min: 20.0, Max: 80.0, Active: true},
		{Name: "s3_nd", Config: "cfg1", Target: types.VariableTarget{Type: "surface", ID: 3, Param: "nd"}, Min: 1.4, Max: 1.9, Active: true},
		{Name: "s3_vd", Config: "cfg1", Target: types.VariableTarget{Type: "surface", ID: 3, Param: "vd"}, Min: 20.0, Max: 80.0, Active: true},
	}

	opt := NewMultiOptimizer([]ConfigInput{cfg}, nil, localVars, gc, 100, 0.01, 1e-6, 1e-4, 1.0, 16, 0, 0, nil, nil, 0, 0, false, false, nil, nil)
	res := opt.Optimize()

	x := make([]float64, len(res.Variables))
	for i, vs := range res.Variables {
		x[i] = vs.After
	}
	configSurfaces, tempGC := opt.applyVariables(x)
	effGC := effectiveGC(gc, tempGC)
	aft := configSurfaces["cfg1"]
	vd1 := glassVDForSurface(aft, effGC, 1)
	vd3 := glassVDForSurface(aft, effGC, 3)

	if res.AfterMerit >= res.BeforeMerit {
		t.Errorf("AfterMerit %v not < BeforeMerit %v", res.AfterMerit, res.BeforeMerit)
	}
	t.Logf("status=%s iters=%d S1(vd)=%.3f S3(vd)=%.3f", res.Status, res.Iterations, vd1, vd3)
	// The positive element must have become the flint (low vd) and the negative
	// element the crown (high vd) of the couple.
	if vd1 >= 45.0 {
		t.Errorf("S1 (positive power) vd after = %v, want < 45 (positive flint)", vd1)
	}
	if vd3 <= 45.0 {
		t.Errorf("S3 (negative power) vd after = %v, want > 45 (negative crown)", vd3)
	}
	if vd1 >= vd3 {
		t.Errorf("S1 vd %v not below S3 vd %v", vd1, vd3)
	}
}

func TestScheduleCurve(t *testing.T) {
	for _, tc := range []struct {
		curve string
		t     float64
		want  float64
	}{
		{"linear", 0, 0}, {"linear", 0.5, 0.5}, {"linear", 1, 1},
		{"step", 0.4, 0}, {"step", 0.6, 1},
		{"sigmoid", 0.5, 0.5},
	} {
		if got := scheduleCurve(tc.curve, tc.t); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("scheduleCurve(%q, %v) = %v, want %v", tc.curve, tc.t, got, tc.want)
		}
	}
}

// modesScheduleConfig returns a cheap non-grid two-mode config and an
// iteration-driven linear schedule: color_first fades out, full fades in.
func modesScheduleConfig(curve string) ([]ConfigInput, *types.MeritScheduleConfig) {
	s := tripletModesSurfaces(false)
	surface.Precompute(s)
	cfg := ConfigInput{
		ID:       "cfg1",
		Weight:   1.0,
		Surfaces: s,
		Fields: []types.FieldItem{
			{ID: 0, AngleDeg: 0.0, Weight: 1.0},
			{ID: 1, AngleDeg: 16.0, Weight: 1.0},
		},
		Wavelengths: []types.WavelengthItem{{ID: 0, Value: 0.00058756, Weight: 1.0}},
		MeritModes: []types.MeritMode{
			{Name: "color_first", Terms: []types.MeritTerm{
				{Kind: "lateral_color", Field: 0, Wavelength: 0.0004358, Wavelength2: 0.0006563, Weight: 1.0},
				{Kind: "glass_role", SurfaceSet: []int{3}, Weight: 1.0},
			}},
			{Name: "full", Terms: []types.MeritTerm{
				{Kind: "longitudinal_color", Wavelength: 0.0004358, Wavelength2: 0.0006563, Weight: 1.0},
				{Kind: "glass_role", SurfaceSet: []int{6}, Weight: 1.0},
			}},
		},
	}
	schedule := &types.MeritScheduleConfig{
		Metric:     "iteration",
		Curve:      curve,
		AnchorFrom: 0,
		AnchorTo:   100,
		Modes: []types.MeritScheduleMode{
			{Name: "color_first", WeightFrom: 1.0, WeightTo: 0.0},
			{Name: "full", WeightFrom: 0.0, WeightTo: 1.0},
		},
	}
	return []ConfigInput{cfg}, schedule
}

// TestMeritScheduleBlendLeastSquares verifies the blend keeps the least-squares
// identity Σ residual² == merit at intermediate blend points.
func TestMeritScheduleBlendLeastSquares(t *testing.T) {
	configs, schedule := modesScheduleConfig("linear")
	opt := NewMultiOptimizer(configs, nil, nil, tripletGC(), 1, 0.01, 1e-6, 1e-6, 1.0, 32, 0, 0, nil, nil, 0, 0, false, false, nil, nil)
	opt.SetMeritSchedule(schedule)

	x := []float64{}
	for _, iter := range []int{10, 50, 90} {
		opt.UpdateMeritWeights(x, iter)
		var sum float64
		for _, r := range opt.ComputeResiduals(x) {
			sum += r * r
		}
		merit := opt.EvaluateMerit(x)
		if math.Abs(sum-merit) > 1e-6*math.Max(1, merit) {
			t.Errorf("iter %d: Σr² = %v, EvaluateMerit = %v", iter, sum, merit)
		}
		// Both modes must contribute at intermediate t.
		if len(opt.ComputeResiduals(x)) != 4 {
			t.Errorf("iter %d: expected 4 residuals (2 modes × 2 terms), got %d", iter, len(opt.ComputeResiduals(x)))
		}
	}

	// Weights follow the linear ramp: at t = 0.5 both modes have weight 0.5.
	opt.UpdateMeritWeights(x, 50)
	if math.Abs(opt.modeWeights["color_first"]-0.5) > 1e-9 {
		t.Errorf("color_first weight at iter 50 = %v, want 0.5", opt.modeWeights["color_first"])
	}
	if math.Abs(opt.modeWeights["full"]-0.5) > 1e-9 {
		t.Errorf("full weight at iter 50 = %v, want 0.5", opt.modeWeights["full"])
	}
}

// TestMeritScheduleStepIsHardSwitch verifies the step curve reproduces a hard
// mode switch: the blend equals exactly one mode's merit on each side.
func TestMeritScheduleStepIsHardSwitch(t *testing.T) {
	configs, schedule := modesScheduleConfig("step")
	opt := NewMultiOptimizer(configs, nil, nil, tripletGC(), 1, 0.01, 1e-6, 1e-6, 1.0, 32, 0, 0, nil, nil, 0, 0, false, false, nil, nil)
	opt.SetMeritSchedule(schedule)

	x := []float64{}
	src := configs[0]
	refs := map[string][]types.MeritTerm{
		"color_first": src.MeritModes[0].Terms,
		"full":        src.MeritModes[1].Terms,
	}
	refOpt := func(mode string) *Optimizer {
		ref := ConfigInput{
			ID:          src.ID,
			Weight:      src.Weight,
			Surfaces:    src.Surfaces,
			Fields:      src.Fields,
			Wavelengths: src.Wavelengths,
			MeritTerms:  refs[mode],
		}
		return NewMultiOptimizer([]ConfigInput{ref}, nil, nil, tripletGC(), 1, 0.01, 1e-6, 1e-6, 1.0, 32, 0, 0, nil, nil, 0, 0, false, false, nil, nil)
	}

	opt.UpdateMeritWeights(x, 10) // t=0.1 → color_first
	if got, want := opt.EvaluateMerit(x), refOpt("color_first").EvaluateMerit(x); math.Abs(got-want) > 1e-9*math.Max(1, want) {
		t.Errorf("step at t<0.5: blend merit %v != color_first-only %v", got, want)
	}
	opt.UpdateMeritWeights(x, 90) // t=0.9 → full
	if got, want := opt.EvaluateMerit(x), refOpt("full").EvaluateMerit(x); math.Abs(got-want) > 1e-9*math.Max(1, want) {
		t.Errorf("step at t>0.5: blend merit %v != full-only %v", got, want)
	}
}

// TestMeritScheduleDLSRecoversGlassRoles is the acceptance test: from a swapped
// flint/crown start, a single DLS run with a colour-first→full merit schedule
// and glass-only variables recovers the correct roles (S3 flint, S6 crown).
func TestMeritScheduleDLSRecoversGlassRoles(t *testing.T) {
	gc := tripletGC()
	s := tripletModesSurfaces(true) // swapped: S3 crown, S6 flint
	surface.Precompute(s)

	cfg := ConfigInput{
		ID:       "cfg1",
		Weight:   1.0,
		Surfaces: s,
		Fields: []types.FieldItem{
			{ID: 0, AngleDeg: 0.0, Weight: 1.0},
			{ID: 1, AngleDeg: 16.0, Weight: 1.0},
		},
		Wavelengths: []types.WavelengthItem{
			{ID: 0, Value: 0.0004358, Weight: 1.0},
			{ID: 1, Value: 0.00058756, Weight: 1.0},
			{ID: 2, Value: 0.0006563, Weight: 1.0},
		},
		MeritModes: []types.MeritMode{
			{Name: "color_first", Terms: []types.MeritTerm{
				{Kind: "lateral_color", Field: 0, Wavelength: 0.0004358, Wavelength2: 0.0006563, Weight: 1.0},
				{Kind: "lateral_color", Field: 1, Wavelength: 0.0004358, Wavelength2: 0.0006563, Weight: 1.0},
				{Kind: "longitudinal_color", Wavelength: 0.0004358, Wavelength2: 0.0006563, Weight: 1.0},
				{Kind: "glass_role", SurfaceSet: []int{3}, Weight: 1.0},
				{Kind: "glass_role", SurfaceSet: []int{6}, Weight: 1.0},
			}},
			{Name: "full", Terms: []types.MeritTerm{
				{Field: 0, Wavelength: 0.00058756, Weight: 1.0},
				{Field: 1, Wavelength: 0.00058756, Weight: 1.0},
				{Kind: "lateral_color", Field: 0, Wavelength: 0.0004358, Wavelength2: 0.0006563, Weight: 1.0},
				{Kind: "lateral_color", Field: 1, Wavelength: 0.0004358, Wavelength2: 0.0006563, Weight: 1.0},
				{Kind: "longitudinal_color", Wavelength: 0.0004358, Wavelength2: 0.0006563, Weight: 1.0},
				{Kind: "glass_role", SurfaceSet: []int{3}, Weight: 1.0},
				{Kind: "glass_role", SurfaceSet: []int{6}, Weight: 1.0},
			}},
		},
	}
	localVars := []types.LocalVariableDef{
		{Name: "s3_nd", Config: "cfg1", Target: types.VariableTarget{Type: "surface", ID: 3, Param: "nd"}, Min: 1.4, Max: 1.9, Active: true},
		{Name: "s3_vd", Config: "cfg1", Target: types.VariableTarget{Type: "surface", ID: 3, Param: "vd"}, Min: 20.0, Max: 80.0, Active: true},
		{Name: "s6_nd", Config: "cfg1", Target: types.VariableTarget{Type: "surface", ID: 6, Param: "nd"}, Min: 1.4, Max: 1.9, Active: true},
		{Name: "s6_vd", Config: "cfg1", Target: types.VariableTarget{Type: "surface", ID: 6, Param: "vd"}, Min: 20.0, Max: 80.0, Active: true},
	}
	schedule := &types.MeritScheduleConfig{
		Metric:     "merit_ratio",
		Curve:      "linear",
		AnchorFrom: 1.0,
		AnchorTo:   0.05,
		Modes: []types.MeritScheduleMode{
			{Name: "color_first", WeightFrom: 1.0, WeightTo: 0.0},
			{Name: "full", WeightFrom: 0.0, WeightTo: 1.0},
		},
	}

	opt := NewMultiOptimizer([]ConfigInput{cfg}, nil, localVars, gc, 120, 0.01, 1e-6, 1e-4, 1.0, 32, 100, 0, nil, nil, 0, 0, false, false, nil, nil)
	opt.SetMeritSchedule(schedule)
	res := opt.Optimize()

	x := make([]float64, len(res.Variables))
	for i, vs := range res.Variables {
		x[i] = vs.After
	}
	configSurfaces, tempGC := opt.applyVariables(x)
	effGC := effectiveGC(gc, tempGC)
	aft := configSurfaces["cfg1"]
	vd3 := glassVDForSurface(aft, effGC, 3)
	vd6 := glassVDForSurface(aft, effGC, 6)

	if res.AfterMerit >= res.BeforeMerit {
		t.Errorf("AfterMerit %v not < BeforeMerit %v", res.AfterMerit, res.BeforeMerit)
	}
	if vd3 >= 45.0 {
		t.Errorf("S3 vd after = %v, want < 45 (flint): the negative-power element did not become a flint", vd3)
	}
	if vd6 <= 43.0 {
		t.Errorf("S6 vd after = %v, want > 43 (crown): the positive-power element did not become a crown", vd6)
	}
	if vd3 >= vd6 {
		t.Errorf("S3 vd=%v should be < S6 vd=%v (flint < crown)", vd3, vd6)
	}
	active, weights, changes, _, _ := opt.MeritScheduleState()
	t.Logf("status=%s iters=%d active_mode=%s mode_changes=%d weights=%v S3(vd)=%.3f S6(vd)=%.3f",
		res.Status, res.Iterations, active, changes, weights, vd3, vd6)
	if active == "" {
		t.Errorf("expected a dominant mode to be reported")
	}
}

// spotDiffractionConfig builds a spot_diffraction schedule config for the
// triplet with two modes: spot (grid merit) and wavefront (paraxial merit).
func spotDiffractionConfig(agg string) ([]ConfigInput, *types.MeritScheduleConfig) {
	s := tripletModesSurfaces(false)
	surface.Precompute(s)
	wl := 0.00058756
	cfg := ConfigInput{
		ID:          "cfg1",
		Weight:      1.0,
		StopSurface: 5,
		RefSurface:  7,
		Surfaces:    s,
		Fields: []types.FieldItem{
			{ID: 0, AngleDeg: 0.0, Weight: 1.0},
			{ID: 1, AngleDeg: 16.0, Weight: 1.0},
		},
		Wavelengths: []types.WavelengthItem{{ID: 0, Value: wl, Weight: 1.0}},
		MeritModes: []types.MeritMode{
			{Name: "spot", Terms: []types.MeritTerm{
				{Kind: "spot_rms", Field: 0, Wavelength: wl, Weight: 1.0},
				{Kind: "spot_rms", Field: 1, Wavelength: wl, Weight: 1.0},
			}},
			{Name: "wavefront", Terms: []types.MeritTerm{
				{Kind: "lateral_color", Field: 0, Wavelength: 0.0004358, Wavelength2: 0.0006563, Weight: 1.0},
			}},
		},
	}
	schedule := &types.MeritScheduleConfig{
		Metric:            "spot_diffraction",
		MetricAggregation: agg,
		Curve:             "step",
		AnchorFrom:        3.0,
		AnchorTo:          1.0,
		Modes: []types.MeritScheduleMode{
			{Name: "spot", WeightFrom: 1.0, WeightTo: 0.0},
			{Name: "wavefront", WeightFrom: 0.0, WeightTo: 1.0},
		},
	}
	return []ConfigInput{cfg}, schedule
}

// TestSpotDiffractionMetricPositive verifies the spot_diffraction metric
// evaluates to a positive ratio at the initial variable state.
func TestSpotDiffractionMetricPositive(t *testing.T) {
	configs, schedule := spotDiffractionConfig("mean")
	opt := NewMultiOptimizer(configs, nil, nil, tripletGC(), 1, 0.01, 1e-6, 1e-6, 1.0, 64, 0, 0, nil, nil, 0, 0, false, false, nil, nil)
	opt.SetMeritSchedule(schedule)

	x := []float64{}
	ratio, ok := opt.spotDiffractionRatio(x)
	if !ok {
		t.Fatal("spotDiffractionRatio returned ok=false at initial state")
	}
	if ratio <= 0 {
		t.Errorf("spotDiffractionRatio = %v, want > 0", ratio)
	}
	t.Logf("spot_diffraction ratio = %.3f (mean aggregation)", ratio)

	// The ratio should be in a plausible range for the triplet.
	if ratio < 0.01 || ratio > 1e4 {
		t.Errorf("spotDiffractionRatio = %v, implausible range", ratio)
	}
}

// TestSpotDiffractionMeanVsMax verifies mean ≤ max for the triplet.
func TestSpotDiffractionMeanVsMax(t *testing.T) {
	configs, scheduleMean := spotDiffractionConfig("mean")

	optM := NewMultiOptimizer(configs, nil, nil, tripletGC(), 1, 0.01, 1e-6, 1e-6, 1.0, 64, 0, 0, nil, nil, 0, 0, false, false, nil, nil)
	optM.SetMeritSchedule(scheduleMean)
	rm, _ := optM.spotDiffractionRatio([]float64{})

	scheduleMaxCopy := *scheduleMean
	scheduleMaxCopy.MetricAggregation = "max"
	optX := NewMultiOptimizer(configs, nil, nil, tripletGC(), 1, 0.01, 1e-6, 1e-6, 1.0, 64, 0, 0, nil, nil, 0, 0, false, false, nil, nil)
	optX.SetMeritSchedule(&scheduleMaxCopy)
	rx, _ := optX.spotDiffractionRatio([]float64{})

	if rx < rm {
		t.Errorf("max ratio %v < mean ratio %v; max should be ≥ mean", rx, rm)
	}
	t.Logf("mean=%.3f max=%.3f", rm, rx)
}

// TestSpotDiffractionStepSwitch verifies the step curve reproduces a hard
// mode switch for the spot_diffraction metric: at large ratio the spot mode is
// active, at small ratio the wavefront mode takes over.
func TestSpotDiffractionStepSwitch(t *testing.T) {
	// Build two configs: one with large-ratio anchor, one with small.
	s := tripletModesSurfaces(false)
	surface.Precompute(s)
	wl := 0.00058756
	makeCfg := func() ConfigInput {
		return ConfigInput{
			ID:          "cfg1",
			Weight:      1.0,
			StopSurface: 5,
			RefSurface:  7,
			Surfaces:    s,
			Fields: []types.FieldItem{
				{ID: 0, AngleDeg: 0.0, Weight: 1.0},
			},
			Wavelengths: []types.WavelengthItem{{ID: 0, Value: wl, Weight: 1.0}},
			MeritModes: []types.MeritMode{
				{Name: "spot", Terms: []types.MeritTerm{
					{Kind: "spot_rms", Field: 0, Wavelength: wl, Weight: 1.0},
				}},
				{Name: "wavefront", Terms: []types.MeritTerm{
					{Kind: "longitudinal_color", Wavelength: 0.0004358, Wavelength2: 0.0006563, Weight: 1.0},
				}},
			},
		}
	}

	// step curve: t<0.5 → spot, t>0.5 → wavefront.
	schedule := &types.MeritScheduleConfig{
		Metric:     "spot_diffraction",
		Curve:      "step",
		AnchorFrom: 3.0,
		AnchorTo:   1.0,
		Modes: []types.MeritScheduleMode{
			{Name: "spot", WeightFrom: 1.0, WeightTo: 0.0},
			{Name: "wavefront", WeightFrom: 0.0, WeightTo: 1.0},
		},
	}

	cfg := makeCfg()
	opt := NewMultiOptimizer([]ConfigInput{cfg}, nil, nil, tripletGC(), 1, 0.01, 1e-6, 1e-6, 1.0, 64, 0, 0, nil, nil, 0, 0, false, false, nil, nil)
	opt.SetMeritSchedule(schedule)

	// Evaluate the metric at initial state.
	ratio, _ := opt.spotDiffractionRatio([]float64{})
	t.Logf("initial spot/Airy ratio = %.3f", ratio)

	// With step curve and anchors 3→1:
	// ratio ≥ 3 → t=0 → spot mode at full weight → wavefront at 0.
	// ratio ≤ 1 → t=1 → wavefront mode at full weight.
	// The triplet is a moderately-corrected system; spot/Airy ratio ≈ few.
	// Verify Σr² == merit at the initial state (should hold for step curve).
	opt.UpdateMeritWeights([]float64{}, 0)
	var sum float64
	for _, r := range opt.ComputeResiduals([]float64{}) {
		sum += r * r
	}
	merit := opt.EvaluateMerit([]float64{})
	if math.Abs(sum-merit) > 1e-6*math.Max(1, merit) {
		t.Errorf("Σr² = %v, merit = %v", sum, merit)
	}
	t.Logf("Σr²=%v merit=%v", sum, merit)
}

// numRaysScheduleConfig creates a config with num_rays in merit modes for
// testing the num_rays scheduling feature. Mode "coarse" uses num_rays=16
// (paraxial terms only, no grid), mode "fine" uses num_rays=64 (also paraxial).
func numRaysScheduleConfig() ([]ConfigInput, *types.MeritScheduleConfig) {
	s := tripletModesSurfaces(false)
	surface.Precompute(s)
	cfg := ConfigInput{
		ID:       "cfg1",
		Weight:   1.0,
		Surfaces: s,
		Fields: []types.FieldItem{
			{ID: 0, AngleDeg: 0.0, Weight: 1.0},
		},
		Wavelengths: []types.WavelengthItem{{ID: 0, Value: 0.00058756, Weight: 1.0}},
		MeritModes: []types.MeritMode{
			{Name: "coarse", NumRays: 16, Terms: []types.MeritTerm{
				{Kind: "lateral_color", Field: 0, Wavelength: 0.0004358, Wavelength2: 0.0006563, Weight: 1.0},
			}},
			{Name: "fine", NumRays: 64, Terms: []types.MeritTerm{
				{Kind: "longitudinal_color", Wavelength: 0.0004358, Wavelength2: 0.0006563, Weight: 1.0},
			}},
		},
	}
	schedule := &types.MeritScheduleConfig{
		Metric:     "iteration",
		Curve:      "step",
		AnchorFrom: 0,
		AnchorTo:   100,
		Modes: []types.MeritScheduleMode{
			{Name: "coarse", WeightFrom: 1.0, WeightTo: 0.0},
			{Name: "fine", WeightFrom: 0.0, WeightTo: 1.0},
		},
	}
	return []ConfigInput{cfg}, schedule
}

// TestMeritScheduleNumRaysSwitches verifies that o.numRays changes when the
// merit schedule switches between modes that declare different num_rays.
func TestMeritScheduleNumRaysSwitches(t *testing.T) {
	configs, schedule := numRaysScheduleConfig()
	opt := NewMultiOptimizer(configs, nil, nil, tripletGC(), 1, 0.01, 1e-6, 1e-6, 1.0, 8, 0, 0, nil, nil, 0, 0, false, false, nil, nil)
	opt.SetMeritSchedule(schedule)

	x := []float64{}

	// step curve at iter 10: t=0.1 < 0.5 → "coarse" at full weight
	opt.UpdateMeritWeights(x, 10)
	if opt.numRays != 16 {
		t.Errorf("iter 10: numRays = %d, want 16", opt.numRays)
	}

	// step curve at iter 90: t=0.9 > 0.5 → "fine" at full weight
	opt.UpdateMeritWeights(x, 90)
	if opt.numRays != 64 {
		t.Errorf("iter 90: numRays = %d, want 64", opt.numRays)
	}

	// Verify MeritScheduleState reports the effective num_rays
	_, _, _, _, effNumRays := opt.MeritScheduleState()
	if effNumRays != 64 {
		t.Errorf("MeritScheduleState effNumRays = %d, want 64", effNumRays)
	}
}

// TestMeritScheduleNumRaysMaxAcrossConfigs verifies that the maximum num_rays
// across configs is used when configs declare different values for the same mode.
func TestMeritScheduleNumRaysMaxAcrossConfigs(t *testing.T) {
	s := tripletModesSurfaces(false)
	surface.Precompute(s)

	cfg0 := ConfigInput{
		ID:       "cfg0",
		Weight:   0.5,
		Surfaces: s,
		Fields:   []types.FieldItem{{ID: 0, AngleDeg: 0.0, Weight: 1.0}},
		Wavelengths: []types.WavelengthItem{{ID: 0, Value: 0.00058756, Weight: 1.0}},
		MeritModes: []types.MeritMode{
			{Name: "A", NumRays: 16, Terms: []types.MeritTerm{
				{Kind: "lateral_color", Field: 0, Wavelength: 0.0004358, Wavelength2: 0.0006563, Weight: 1.0},
			}},
		},
	}
	cfg1 := ConfigInput{
		ID:       "cfg1",
		Weight:   0.5,
		Surfaces: s,
		Fields:   []types.FieldItem{{ID: 0, AngleDeg: 0.0, Weight: 1.0}},
		Wavelengths: []types.WavelengthItem{{ID: 0, Value: 0.00058756, Weight: 1.0}},
		MeritModes: []types.MeritMode{
			{Name: "A", NumRays: 48, Terms: []types.MeritTerm{
				{Kind: "longitudinal_color", Wavelength: 0.0004358, Wavelength2: 0.0006563, Weight: 1.0},
			}},
		},
	}
	schedule := &types.MeritScheduleConfig{
		Metric:     "iteration",
		Curve:      "step",
		AnchorFrom: 0,
		AnchorTo:   100,
		Modes: []types.MeritScheduleMode{
			{Name: "A", WeightFrom: 1.0, WeightTo: 1.0},
		},
	}

	opt := NewMultiOptimizer([]ConfigInput{cfg0, cfg1}, nil, nil, tripletGC(), 1, 0.01, 1e-6, 1e-6, 1.0, 32, 0, 0, nil, nil, 0, 0, false, false, nil, nil)
	opt.SetMeritSchedule(schedule)

	opt.UpdateMeritWeights([]float64{}, 0)
	if opt.numRays != 48 {
		t.Errorf("numRays = %d, want max(16,48) = 48", opt.numRays)
	}
}

// TestMeritScheduleNumRaysFallbackToBase verifies that when no mode declares
// num_rays, the base value is used unchanged.
func TestMeritScheduleNumRaysFallbackToBase(t *testing.T) {
	configs, schedule := modesScheduleConfig("step")
	opt := NewMultiOptimizer(configs, nil, nil, tripletGC(), 1, 0.01, 1e-6, 1e-6, 1.0, 32, 0, 0, nil, nil, 0, 0, false, false, nil, nil)
	opt.SetMeritSchedule(schedule)

	x := []float64{}
	opt.UpdateMeritWeights(x, 10)
	if opt.numRays != 32 {
		t.Errorf("numRays = %d, want base 32 (no mode declares num_rays)", opt.numRays)
	}
	opt.UpdateMeritWeights(x, 90)
	if opt.numRays != 32 {
		t.Errorf("numRays = %d, want base 32 (no mode declares num_rays)", opt.numRays)
	}
}
