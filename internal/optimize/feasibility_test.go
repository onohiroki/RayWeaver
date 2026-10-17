package optimize

import (
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
)

// TestFeasibilityConstraintViolationsRelative verifies the scale-free rule
// escape feasibility uses: a constraint is violated when its unweighted error
// exceeds 1% of its natural scale. The recorded merit does not include
// constraints (only the DLS augmented Lagrangian does), so this is the only
// place a violated constraint can reject a minimum.
func TestFeasibilityConstraintViolationsRelative(t *testing.T) {
	localVars := []types.LocalVariableDef{
		{Name: "s1_c", Config: "cfg1", Target: types.VariableTarget{Type: "surface", ID: 1, Param: "curvature"}, Min: 0.05, Max: 0.2, Active: true},
		{Name: "s3_c", Config: "cfg1", Target: types.VariableTarget{Type: "surface", ID: 3, Param: "curvature"}, Min: -0.15, Max: -0.01, Active: true},
		{Name: "s6_c", Config: "cfg1", Target: types.VariableTarget{Type: "surface", ID: 6, Param: "curvature"}, Min: 0.0, Max: 0.05, Active: true},
		{Name: "s7_c", Config: "cfg1", Target: types.VariableTarget{Type: "surface", ID: 7, Param: "curvature"}, Min: -0.2, Max: -0.01, Active: true},
	}

	// Satisfiable: the solver reaches both targets.
	configs := tripletEqualityConfigs(25.0, 4.61)
	opt := NewMultiOptimizer(configs, nil, localVars, nil, tripletGC(), 80, 0.01, 1e-6, 1e-6, 1.0, 64, 100, 0, nil, nil, 0, 0, false, false, nil, nil)
	result := opt.Optimize()
	x := make([]float64, len(result.Variables))
	for i, vs := range result.Variables {
		x[i] = vs.After
	}
	if v := opt.FeasibilityConstraintViolations(x); len(v) != 0 {
		t.Errorf("satisfiable case: violations = %+v, want none", v)
	}

	// Unsatisfiable: EPD target 8.0 is far beyond what the triplet reaches.
	configs2 := tripletEqualityConfigs(25.0, 8.0)
	opt2 := NewMultiOptimizer(configs2, nil, localVars, nil, tripletGC(), 80, 0.01, 1e-6, 1e-6, 1.0, 64, 100, 0, nil, nil, 0, 0, false, false, nil, nil)
	result2 := opt2.Optimize()
	x2 := make([]float64, len(result2.Variables))
	for i, vs := range result2.Variables {
		x2[i] = vs.After
	}
	found := false
	for _, v := range opt2.FeasibilityConstraintViolations(x2) {
		if v.Measure == string(types.MeasureEntrancePupilDiameter) {
			found = true
		}
	}
	if !found {
		t.Errorf("unsatisfiable EPD constraint not reported as a feasibility violation")
	}
}
