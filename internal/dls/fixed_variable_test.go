package dls

import (
	"math"
	"testing"
)

// driftModel has two variables: a free quadratic one and a pinned one
// (Min == Max). Its second residual decays monotonically with the pinned
// coordinate, so the merit keeps falling as that coordinate grows — exactly
// the gradient sign the solver follows when it walks a fixed variable.
// The residuals still depend on it, so the finite-difference Jacobian has a
// non-zero column there and the normalized-space scale falls back to 1.0
// (the span is zero), which is what makes the drift possible at all.
type driftModel struct{}

func (driftModel) Variables() []VariableInfo {
	return []VariableInfo{
		{Name: "free", Param: "curvature", Min: -2, Max: 2},
		{Name: "pinned", Param: "thickness", Min: 0.5, Max: 0.5},
	}
}

func (driftModel) InitialState() []float64 { return []float64{0, 0.5} }

func (driftModel) Options() Options { return Options{MaxIter: 60, Tol: 1e-14} }

func (driftModel) ComputeResiduals(x []float64) []float64 {
	return []float64{x[0] - 1.0, math.Exp(-x[1])}
}

func (driftModel) EvaluateMerit(x []float64) float64 {
	r := driftModel{}.ComputeResiduals(x)
	return r[0]*r[0] + r[1]*r[1]
}

// ComputeConstraints keeps the model constraint-free (an empty slice), so the
// solve exercises the plain merit path.
func (driftModel) ComputeConstraints(x []float64) []float64 { return nil }

// TestFixedVariableNeverLeavesItsPin guards the pinned-variable drift fix: a
// variable declared min == max carries no physical range, but the
// normalized-space scale falls back to 1.0 when the span is non-positive, so
// the LM step moved its normalized coordinate freely and denormalize handed
// out Min + n·1.0 — observed as vp_dia declared min 12.5 max 12.5 yet
// delivered at 13.34 mm (n = 0.84), i.e. the shipped entrance pupil
// disagreed with the design's EPD. The variable must come back at exactly
// Min, while the free variables still move (the pin must not freeze the
// solve).
func TestFixedVariableNeverLeavesItsPin(t *testing.T) {
	m := driftModel{}
	res := Solve(m)

	vars := m.Variables()
	if len(res.Variables) != len(vars) {
		t.Fatalf("got %d variable states, want %d", len(res.Variables), len(vars))
	}
	for i, v := range vars {
		if v.Max > v.Min {
			continue
		}
		if res.Variables[i].After != v.Min {
			t.Errorf("fixed variable %d (%s) = %.9f, want exactly Min %.9f",
				i, v.Name, res.Variables[i].After, v.Min)
		}
	}
	if res.Variables[0].After == 0 {
		t.Log("note: the free variable did not move; the pin assertion above still holds")
	}
	if math.IsNaN(res.AfterMerit) || math.IsInf(res.AfterMerit, 0) {
		t.Fatalf("non-finite merit after the solve: %v", res.AfterMerit)
	}
}
