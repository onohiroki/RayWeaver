package dls

import (
	"strings"
	"testing"
)

// constantMeritModel has a non-zero residual Jacobian (so the solver does not
// converge by gradient) but a merit that never changes — no step can ever be
// accepted. It is the deterministic stand-in for the "no improving step" stall
// the gap analysis hit, where the merit stayed at its initial value for the
// whole budget and the run reported nothing.
type constantMeritModel struct{ n int }

func (m constantMeritModel) Variables() []VariableInfo {
	vs := make([]VariableInfo, m.n)
	for i := range vs {
		vs[i] = VariableInfo{Name: "v", Min: -1, Max: 1}
	}
	return vs
}

func (m constantMeritModel) InitialState() []float64 {
	x := make([]float64, m.n)
	for i := range x {
		x[i] = 0.4
	}
	return x
}

func (m constantMeritModel) HasConstraints() bool { return false }

func (m constantMeritModel) Options() Options { return Options{MaxIter: 8} }

func (m constantMeritModel) EvaluateMerit(x []float64) float64 { return 1.0 }

func (m constantMeritModel) ComputeResiduals(x []float64) []float64 {
	r := make([]float64, m.n)
	copy(r, x)
	return r
}

func (m constantMeritModel) ComputeConstraints(x []float64) []float64 { return nil }

// TestSolveStallReportsDiagnostic pins the end-to-end plumbing: a solve that
// accepts no step is reported as such (AcceptedSteps 0 + a diagnostic naming
// the stall), instead of the status alone.
func TestSolveStallReportsDiagnostic(t *testing.T) {
	res := Solve(constantMeritModel{n: 3})
	if res.Status != "max_iterations" {
		t.Fatalf("status = %q, want max_iterations", res.Status)
	}
	if res.AcceptedSteps != 0 {
		t.Errorf("AcceptedSteps = %d, want 0", res.AcceptedSteps)
	}
	if !strings.Contains(res.Diagnostic, "no step improved the merit") {
		t.Errorf("Diagnostic = %q, want the stall statement", res.Diagnostic)
	}
	if !strings.Contains(res.Diagnostic, "optimization.central_diff") {
		t.Errorf("Diagnostic = %q, want the conditioning advice", res.Diagnostic)
	}
}

// TestJacobianConditioningRecord pins the two conditioning statistics read off
// the undamped normal equations: the smallest column norm relative to the
// largest, and the largest |cos| between two columns (a near-duplicate pair).
func TestJacobianConditioningRecord(t *testing.T) {
	c := jacobianConditioning{minNormRatio: 1.0, minNormIdx: -1, dupA: -1, dupB: -1}
	// Columns with norms 2, 2, 1 and a perfectly parallel pair (1, 2).
	c.record([][]float64{
		{4, 0, 0},
		{0, 4, 2},
		{0, 2, 1},
	}, 3)

	if c.minNormIdx != 2 || c.minNormRatio != 0.5 {
		t.Errorf("minNorm = (idx %d, ratio %v), want (2, 0.5)", c.minNormIdx, c.minNormRatio)
	}
	if c.dupA != 1 || c.dupB != 2 || c.maxAbsCos != 1.0 {
		t.Errorf("duplicate pair = (%d, %d) cos %v, want (1, 2) cos 1", c.dupA, c.dupB, c.maxAbsCos)
	}

	// A dead column (zero norm) reports ratio 0, and a later, better-conditioned
	// iteration must not erase the worst-case record.
	c2 := jacobianConditioning{minNormRatio: 1.0, minNormIdx: -1, dupA: -1, dupB: -1}
	c2.record([][]float64{{4, 0}, {0, 0}}, 2)
	if c2.minNormIdx != 1 || c2.minNormRatio != 0 {
		t.Errorf("dead column = (idx %d, ratio %v), want (1, 0)", c2.minNormIdx, c2.minNormRatio)
	}
	c2.record([][]float64{{4, 0}, {0, 4}}, 2)
	if c2.minNormIdx != 1 || c2.minNormRatio != 0 {
		t.Errorf("worst case not kept: (idx %d, ratio %v), want (1, 0)", c2.minNormIdx, c2.minNormRatio)
	}
}

// TestStallDiagnostic pins the message the solver reports for a stalled run: it
// names the offending variables and suggests the conditioning knobs, and stays
// quiet about causes that were not observed.
func TestStallDiagnostic(t *testing.T) {
	vars := []VariableInfo{{Name: "s5_curvature"}, {Name: "s6_curvature"}, {Name: "s7_curvature"}}

	msg := stallDiagnostic(200, 200, 237.0, vars, jacobianConditioning{
		minNormRatio: 1e-9, minNormIdx: 0,
		maxAbsCos: 0.9999, dupA: 1, dupB: 2,
	}, 12)
	for _, want := range []string{
		"last 200 of 200 iterations",
		"237",
		`s5_curvature`,
		"near-zero Jacobian column",
		`s6_curvature" and "s7_curvature`,
		"nearly parallel",
		"singular on 12 iteration(s)",
		"optimization.central_diff or optimization.bfgs",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("diagnostic %q missing %q", msg, want)
		}
	}

	// A flat run with healthy conditioning reports only the stall and the
	// generic advice.
	clean := stallDiagnostic(50, 200, 1.0, vars, jacobianConditioning{minNormRatio: 1.0, minNormIdx: -1, dupA: -1, dupB: -1}, 0)
	if strings.Contains(clean, "near-zero") || strings.Contains(clean, "parallel") || strings.Contains(clean, "singular") {
		t.Errorf("clean diagnostic invented a cause: %q", clean)
	}
	if !strings.Contains(clean, "no step improved the merit") {
		t.Errorf("clean diagnostic = %q, want the stall statement", clean)
	}
}
