package optimize

import (
	"math"
	"testing"
)

// TestMeritBreakdownReconcilesWithMerit guards the diagnostic fix: the
// breakdown total must equal EvaluateMerit, including the hull (and glass
// attraction) contributions, otherwise `optimize --verbose` cannot be
// reconciled against the merit the solver reports.
func TestMeritBreakdownReconcilesWithMerit(t *testing.T) {
	opt := buildHullTestOptimizer(t)
	x := []float64{1.60, 48.0}

	bd := opt.MeritBreakdown(x)
	merit := opt.EvaluateMerit(x)

	if _, ok := bd["hull"]; !ok {
		t.Errorf("breakdown is missing the hull contribution: %v", bd)
	}
	if _, ok := bd["objective_total"]; !ok {
		t.Fatalf("breakdown is missing objective_total: %v", bd)
	}
	tol := 1e-9 * math.Max(1.0, math.Abs(merit))
	if math.Abs(bd["objective_total"]-merit) > tol {
		t.Errorf("breakdown objective_total %v != EvaluateMerit %v", bd["objective_total"], merit)
	}
}
