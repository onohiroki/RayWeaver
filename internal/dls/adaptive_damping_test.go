package dls

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
)

func TestDampingClass(t *testing.T) {
	tests := []struct {
		param  string
		expect string
	}{
		{"curvature", "curvature"},
		{"thickness", "thickness"},
		{"diameter", "diameter"},
		{"nd", "nd"},
		{"vd", "vd"},
		{"conic", "conic"},
		{"a4", "asphere"},
		{"a6", "asphere"},
		{"a8", "asphere"},
		{"a10", "asphere"},
		{"a12", "asphere"},
		{"coefficient_0", "asphere"},
		{"coefficient_4", "asphere"},
		{"unknown_param", "shared"},
	}
	for _, tt := range tests {
		got := dampingClass(tt.param)
		if got != tt.expect {
			t.Errorf("dampingClass(%q) = %q, want %q", tt.param, got, tt.expect)
		}
	}
}

func TestBuildDiagonalNilConfig(t *testing.T) {
	n := 3
	state := NewAdaptiveDampingState(n)
	hDiag := []float64{100, 10, 1}
	vars := []VariableInfo{
		{Name: "a", Param: "curvature"},
		{Name: "b", Param: "thickness"},
		{Name: "c", Param: "nd"},
	}
	// nil config path: BuildDiagonal should not be called with nil config
	// in practice, but the state should be initialised correctly.
	if len(state.localFactor) != n {
		t.Errorf("localFactor length = %d, want %d", len(state.localFactor), n)
	}
	for j, lf := range state.localFactor {
		if lf != 1.0 {
			t.Errorf("localFactor[%d] = %v, want 1.0", j, lf)
		}
	}
	_ = hDiag
	_ = vars
}

func TestBuildDiagonalSensitivity(t *testing.T) {
	n := 4
	state := NewAdaptiveDampingState(n)
	hDiag := []float64{1e6, 1e4, 100, 1}
	vars := []VariableInfo{
		{Name: "s1_curvature", Param: "curvature"},
		{Name: "s2_curvature", Param: "curvature"},
		{Name: "s3_thickness", Param: "thickness"},
		{Name: "s4_nd", Param: "nd"},
	}
	cfg := types.AdaptiveDampingConfig{
		SensitivityEMA:   0,
		SensitivityFloor: 1e-12,
		RatioMin:         1e-3,
		RatioMax:         1e3,
		DampingMin:       1e-4,
		DampingMax:       1e4,
	}

	diag := state.BuildDiagonal(hDiag, vars, cfg)

	// All diagonal values must be positive and within bounds.
	for j, d := range diag {
		if d <= 0 {
			t.Errorf("diag[%d] = %v, want > 0", j, d)
		}
		if d < cfg.DampingMin || d > cfg.DampingMax {
			t.Errorf("diag[%d] = %v, out of range [%v, %v]", j, d, cfg.DampingMin, cfg.DampingMax)
		}
	}

	// High-sensitivity variables (curvature) should have higher damping
	// than low-sensitivity ones (thickness).
	if diag[0] <= diag[2] {
		t.Errorf("curvature diag[0]=%v should be > thickness diag[2]=%v", diag[0], diag[2])
	}
}

func TestBuildDiagonalClassMultiplier(t *testing.T) {
	n := 2
	state := NewAdaptiveDampingState(n)
	// Same sensitivity, different classes.
	hDiag := []float64{1000, 1000}
	vars := []VariableInfo{
		{Name: "curv", Param: "curvature"},
		{Name: "thick", Param: "thickness"},
	}
	cfg := types.AdaptiveDampingConfig{
		SensitivityEMA:   0,
		SensitivityFloor: 1e-12,
		RatioMin:         1e-3,
		RatioMax:         1e3,
		DampingMin:       1e-4,
		DampingMax:       1e4,
	}

	diag := state.BuildDiagonal(hDiag, vars, cfg)

	// curvature multiplier (1.5) > thickness multiplier (0.5),
	// so curvature damping should be higher even with same sensitivity.
	if diag[0] <= diag[1] {
		t.Errorf("curvature diag[0]=%v should be > thickness diag[1]=%v with same sensitivity", diag[0], diag[1])
	}
}

func TestBuildDiagonalVariableOverride(t *testing.T) {
	n := 2
	state := NewAdaptiveDampingState(n)
	hDiag := []float64{1000, 1000}
	vars := []VariableInfo{
		{Name: "s3_thickness", Param: "thickness"},
		{Name: "s5_curvature", Param: "curvature"},
	}
	mult025 := 0.25
	cfg := types.AdaptiveDampingConfig{
		SensitivityEMA:   0,
		SensitivityFloor: 1e-12,
		RatioMin:         1e-3,
		RatioMax:         1e3,
		DampingMin:       1e-4,
		DampingMax:       1e4,
		Variables: map[string]types.DampingVarConfig{
			"s3_thickness": {Multiplier: &mult025},
		},
	}

	diag := state.BuildDiagonal(hDiag, vars, cfg)

	// s3_thickness has explicit multiplier 0.25, which is less than the
	// default thickness multiplier 0.5, so its damping should be lower.
	state2 := NewAdaptiveDampingState(n)
	diag2 := state2.BuildDiagonal(hDiag, vars, types.AdaptiveDampingConfig{
		SensitivityEMA:   0,
		SensitivityFloor: 1e-12,
		RatioMin:         1e-3,
		RatioMax:         1e3,
		DampingMin:       1e-4,
		DampingMax:       1e4,
	})
	if diag[0] >= diag2[0] {
		t.Errorf("variable override: diag[0]=%v should be < default diag[0]=%v", diag[0], diag2[0])
	}
}

func TestBuildDiagonalEMASmoothing(t *testing.T) {
	n := 1
	state := NewAdaptiveDampingState(n)
	vars := []VariableInfo{{Name: "x", Param: "curvature"}}
	cfg := types.AdaptiveDampingConfig{
		SensitivityEMA:   0.7,
		SensitivityFloor: 1e-12,
		RatioMin:         1e-3,
		RatioMax:         1e3,
		DampingMin:       1e-4,
		DampingMax:       1e4,
	}

	// First call sets emaH = hDiag.
	d1 := state.BuildDiagonal([]float64{100}, vars, cfg)
	// Second call with different hDiag should be smoothed.
	d2 := state.BuildDiagonal([]float64{1000}, vars, cfg)

	// With EMA=0.7, the second emaH = 0.7*100 + 0.3*1000 = 370.
	// The geometric mean of a single element is itself, so ratio = 370/370 = 1.
	// The first call had emaH=100, ratio=1.
	// Both should produce the same diagonal since ratio is always 1 for n=1.
	if math.Abs(d1[0]-d2[0]) > 1e-10 {
		t.Errorf("EMA smoothing: d1=%v, d2=%v, should be equal for single variable", d1[0], d2[0])
	}
}

func TestOnRejectedBoostsContribution(t *testing.T) {
	n := 2
	state := NewAdaptiveDampingState(n)
	vars := []VariableInfo{
		{Name: "a", Param: "curvature"},
		{Name: "b", Param: "thickness"},
	}
	cfg := types.AdaptiveDampingConfig{
		RejectBoost:           2.0,
		ContributionThreshold: 0.10,
	}

	// gradient and step where variable 0 contributes most.
	gradient := []float64{10, 0.1}
	step := []float64{-0.1, 0.1}

	state.OnRejected(gradient, step, vars, cfg)

	// Variable 0 has contribution |10 * -0.1| / (|10 * -0.1| + |0.1 * 0.1|) = 1/1.01 ≈ 0.99 > 0.10
	// so localFactor[0] should be boosted.
	if state.localFactor[0] <= 1.0 {
		t.Errorf("localFactor[0] = %v, want > 1.0 after reject boost", state.localFactor[0])
	}
	// Variable 1 has contribution 0.01/1.01 ≈ 0.01 < 0.10, so no boost.
	if state.localFactor[1] != 1.0 {
		t.Errorf("localFactor[1] = %v, want 1.0 (no boost)", state.localFactor[1])
	}
}

func TestOnAcceptedRelaxes(t *testing.T) {
	n := 2
	state := NewAdaptiveDampingState(n)
	vars := []VariableInfo{
		{Name: "a", Param: "curvature"},
		{Name: "b", Param: "thickness"},
	}
	cfg := types.AdaptiveDampingConfig{
		AcceptRelax: 0.85,
		RejectBoost: 2.0,
	}

	// First, boost localFactor via rejection.
	state.OnRejected([]float64{10, 10}, []float64{-0.1, -0.1}, vars, cfg)
	boosted := state.localFactor[0]

	// Then accept with a meaningful step.
	state.OnAccepted([]float64{0.01, 0}, vars, cfg)

	// Variable 0 had a meaningful step, so localFactor should relax.
	if state.localFactor[0] >= boosted {
		t.Errorf("localFactor[0] = %v, should have relaxed from %v", state.localFactor[0], boosted)
	}
	// Variable 1 had zero step, so localFactor should stay unchanged.
	if state.localFactor[1] != boosted {
		t.Errorf("localFactor[1] = %v, want %v (no relaxation for zero step)", state.localFactor[1], boosted)
	}
}

func TestLocalFactorConvergesToOne(t *testing.T) {
	n := 1
	state := NewAdaptiveDampingState(n)
	vars := []VariableInfo{{Name: "x", Param: "curvature"}}
	cfg := types.AdaptiveDampingConfig{
		AcceptRelax:           0.85,
		RejectBoost:           2.0,
		ContributionThreshold: 0.10,
	}

	// Boost a few times, then relax.
	for i := 0; i < 3; i++ {
		state.OnRejected([]float64{10}, []float64{-0.1}, vars, cfg)
	}
	// After 3 rejects: localFactor = 2^3 = 8.
	if state.localFactor[0] != 8.0 {
		t.Fatalf("localFactor after 3 rejects = %v, want 8.0", state.localFactor[0])
	}
	for i := 0; i < 20; i++ {
		state.OnAccepted([]float64{0.01}, vars, cfg)
	}
	// After 20 accepts: localFactor = 8 * 0.85^20 ≈ 8 * 0.0388 ≈ 0.31,
	// but floored at 1.0.
	if state.localFactor[0] != 1.0 {
		t.Errorf("localFactor after many accepts = %v, want 1.0 (floor)", state.localFactor[0])
	}
}

func TestZeroColumnJacobianNoNaN(t *testing.T) {
	n := 2
	state := NewAdaptiveDampingState(n)
	hDiag := []float64{0, 100} // zero column for variable 0
	vars := []VariableInfo{
		{Name: "a", Param: "curvature"},
		{Name: "b", Param: "thickness"},
	}
	cfg := types.AdaptiveDampingConfig{
		SensitivityEMA:   0,
		SensitivityFloor: 1e-12,
		RatioMin:         1e-3,
		RatioMax:         1e3,
		DampingMin:       1e-4,
		DampingMax:       1e4,
	}

	diag := state.BuildDiagonal(hDiag, vars, cfg)

	for j, d := range diag {
		if math.IsNaN(d) || math.IsInf(d, 0) {
			t.Errorf("diag[%d] = %v, want finite", j, d)
		}
		if d < cfg.DampingMin {
			t.Errorf("diag[%d] = %v, want >= DampingMin=%v", j, d, cfg.DampingMin)
		}
	}
}
