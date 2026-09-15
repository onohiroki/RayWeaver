package pso

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/dls"
)

// --- Simple test models implementing dls.Model ---

// sphere1D is a 1D sphere (paraboloid) with minimum at cx.
type sphere1D struct{ cx float64 }

func (s sphere1D) Variables() []dls.VariableInfo {
	return []dls.VariableInfo{{Name: "x", Param: "x", Min: -2, Max: 2}}
}
func (s sphere1D) InitialState() []float64 { return []float64{0} }
func (s sphere1D) Options() dls.Options {
	return dls.Options{MaxIter: 200, Mu: 0.1, Tol: 1e-8, Epsilon: 1e-7}
}
func (s sphere1D) EvaluateMerit(x []float64) float64 {
	d := x[0] - s.cx
	return d * d
}
func (s sphere1D) ComputeResiduals(x []float64) []float64 {
	return []float64{x[0] - s.cx}
}
func (s sphere1D) ComputeConstraints(x []float64) []float64 { return nil }

// twoWells1D has two equal-depth minima at positions a and b.
type twoWells1D struct{ a, b float64 }

func (w twoWells1D) Variables() []dls.VariableInfo {
	return []dls.VariableInfo{{Name: "x", Param: "x", Min: 0, Max: 1}}
}
func (w twoWells1D) InitialState() []float64 { return []float64{0.1} }
func (w twoWells1D) Options() dls.Options {
	return dls.Options{MaxIter: 200, Mu: 0.1, Tol: 1e-8, Epsilon: 1e-7}
}
func (w twoWells1D) EvaluateMerit(x []float64) float64 {
	da := x[0] - w.a
	db := x[0] - w.b
	return da * da * db * db // (x-a)²(x-b)², minima at a and b
}
func (w twoWells1D) ComputeResiduals(x []float64) []float64 {
	da := x[0] - w.a
	db := x[0] - w.b
	return []float64{da * db}
}
func (w twoWells1D) ComputeConstraints(x []float64) []float64 { return nil }

// --- Tests ---

func TestExplorerConvergence(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SwarmSize = 20
	cfg.PsoIterations = 50
	cfg.StallWindowFrac = 0.2
	cfg.StallRelTol = 1e-4

	model := sphere1D{cx: 0.0}
	x0 := []float64{1.5}

	ex := NewExplorer(cfg, 42, nil)
	result := ex.Explore(model, x0)

	if len(result.Variables) != 1 {
		t.Fatalf("expected 1 variable, got %d", len(result.Variables))
	}
	finalX := result.Variables[0].After
	if math.Abs(finalX-0.0) > 0.2 {
		t.Errorf("expected x≈0.0, got %f", finalX)
	}
	if result.AfterMerit > 0.01 {
		t.Errorf("expected merit≈0, got %f", result.AfterMerit)
	}
	t.Logf("sphere1D: x=%.6f merit=%.6e iterations=%d status=%s",
		finalX, result.AfterMerit, result.Iterations, result.Status)
}

func TestExplorerDeterminism(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SwarmSize = 10
	cfg.PsoIterations = 20

	model := sphere1D{cx: 0.3}
	x0 := []float64{0.8}

	r1 := NewExplorer(cfg, 123, nil).Explore(model, x0)
	r2 := NewExplorer(cfg, 123, nil).Explore(model, x0)

	if math.Abs(r1.AfterMerit-r2.AfterMerit) > 1e-12 {
		t.Errorf("same seed produced different merit: %f vs %f", r1.AfterMerit, r2.AfterMerit)
	}
	for i := range r1.Variables {
		if math.Abs(r1.Variables[i].After-r2.Variables[i].After) > 1e-12 {
			t.Errorf("same seed produced different X[%d]: %f vs %f", i, r1.Variables[i].After, r2.Variables[i].After)
		}
	}
}

func TestExplorerTwoWells(t *testing.T) {
	// Two-well 1D function: minima at x=0.3 and x=0.8
	wells := twoWells1D{a: 0.3, b: 0.8}

	// Run multiple seeds with a wide initial spread; should find both wells.
	foundA, foundB := false, false
	for seed := int64(0); seed < 50; seed++ {
		cfg := DefaultConfig()
		cfg.SwarmSize = 15
		cfg.PsoIterations = 40
		cfg.InitSpread = 1.0 // particles cover the full [0,1] range
		ex := NewExplorer(cfg, seed, nil)
		r := ex.Explore(wells, []float64{0.5}) // start at midpoint
		x := r.Variables[0].After
		if math.Abs(x-0.3) < 0.05 {
			foundA = true
		}
		if math.Abs(x-0.8) < 0.05 {
			foundB = true
		}
	}
	if !foundA || !foundB {
		t.Errorf("expected both wells found; foundA=%v foundB=%v", foundA, foundB)
	}
}

func TestExplorerCallback(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SwarmSize = 5
	cfg.PsoIterations = 10

	model := sphere1D{cx: 0.0}
	x0 := []float64{0.5}

	var events []string
	cb := func(event string, fields map[string]any) {
		events = append(events, event)
	}

	ex := NewExplorer(cfg, 7, cb)
	ex.Explore(model, x0)

	if len(events) == 0 {
		t.Error("expected at least one progress event")
	}
}
