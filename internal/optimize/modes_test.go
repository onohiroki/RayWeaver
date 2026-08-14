package optimize

import (
	"math"
	"testing"

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

func TestGlassRoleTargetPolarity(t *testing.T) {
	if got := glassRoleTarget(0.1); got <= 45.0 {
		t.Errorf("glassRoleTarget(+0.1) = %v, want > 45 (positive power → crown)", got)
	}
	if got := glassRoleTarget(-0.1); got >= 45.0 {
		t.Errorf("glassRoleTarget(-0.1) = %v, want < 45 (negative power → flint)", got)
	}
	if got := glassRoleTarget(0); got != 45.0 {
		t.Errorf("glassRoleTarget(0) = %v, want 45", got)
	}
}

// TestGlassRoleForSurface verifies the glass-role residual detects a swapped
// flint/crown arrangement: on the correct triplet the flint's vd sits below its
// (flint) target and the crown's above its (crown) target; after the swap the
// signs reverse for the negative-power element.
func TestGlassRoleForSurface(t *testing.T) {
	gc := tripletGC()
	for _, tc := range []struct {
		name string
		id   int
		want float64 // expected sign of vd_actual − vd_target
	}{
		{"correct S3 (SF12 flint on negative power)", 3, -1},
		{"correct S6 (SK18 crown on positive power)", 6, 1},
	} {
		s := tripletModesSurfaces(false)
		surface.Precompute(s)
		res := glassRoleForSurface(s, gc, tc.id)
		if tc.want > 0 && res <= 0 {
			t.Errorf("%s: residual = %v, want > 0", tc.name, res)
		}
		if tc.want < 0 && res >= 0 {
			t.Errorf("%s: residual = %v, want < 0", tc.name, res)
		}
	}

	s := tripletModesSurfaces(true)
	surface.Precompute(s)
	// The swapped negative-power element now carries the crown (vd above the
	// flint target): the residual must be positive, flagging the wrong role.
	if res := glassRoleForSurface(s, gc, 3); res <= 0 {
		t.Errorf("swapped S3 (SK18 crown on negative power): residual = %v, want > 0", res)
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
	opt := NewMultiOptimizer(configs, nil, nil, tripletGC(), 1, 0.01, 1e-6, 1e-6, 1.0, 32, 0, 0, nil, nil, 0, 0)
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
	opt := NewMultiOptimizer(configs, nil, nil, tripletGC(), 1, 0.01, 1e-6, 1e-6, 1.0, 32, 0, 0, nil, nil, 0, 0)
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
		return NewMultiOptimizer([]ConfigInput{ref}, nil, nil, tripletGC(), 1, 0.01, 1e-6, 1e-6, 1.0, 32, 0, 0, nil, nil, 0, 0)
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

	opt := NewMultiOptimizer([]ConfigInput{cfg}, nil, localVars, gc, 80, 0.01, 1e-6, 1e-4, 1.0, 32, 100, 0, nil, nil, 0, 0)
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
	if vd6 <= 45.0 {
		t.Errorf("S6 vd after = %v, want > 45 (crown): the positive-power element did not become a crown", vd6)
	}
	active, weights, changes := opt.MeritScheduleState()
	t.Logf("status=%s iters=%d active_mode=%s mode_changes=%d weights=%v S3(vd)=%.3f S6(vd)=%.3f",
		res.Status, res.Iterations, active, changes, weights, vd3, vd6)
	if active == "" {
		t.Errorf("expected a dominant mode to be reported")
	}
}
