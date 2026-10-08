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

// --- Stall sliding window test ---

func TestStallSlidingWindow(t *testing.T) {
	// With stallWindow=5 and a model that converges quickly, the PSO should
	// NOT stall at iter 5 (old 1-iteration window bug) but only after two
	// full windows (iter 5 + 5 = 10).
	cfg := DefaultConfig()
	cfg.SwarmSize = 10
	cfg.PsoIterations = 20
	cfg.StallWindowFrac = 0.25 // stallWindow = 5
	cfg.StallRelTol = 1e-4
	cfg.InitSpread = 0.5

	model := sphere1D{cx: 0.0}
	x0 := []float64{1.0}

	var stallIter int = -1
	cb := func(event string, fields map[string]any) {
		if event == "pso_stall" {
			stallIter = fields["iter"].(int)
		}
	}

	ex := NewExplorer(cfg, 42, cb)
	result := ex.Explore(model, x0)

	// The stall should fire at iter 5 or 10, never at iter 1-4.
	if stallIter >= 0 && stallIter < 5 {
		t.Errorf("stall fired too early at iter %d (stallWindow=5)", stallIter)
	}
	t.Logf("stallIter=%d status=%s iterations=%d", stallIter, result.Status, result.Iterations)
}

// --- gbest selection criterion test ---

// constrainedSphere has its merit minimum at 0 while the single constraint
// residual c(x)=x-1 vanishes only at x=1. The penalised fitness
// merit + mu*c^2 therefore has its minimum between the two, so the two
// criteria disagree about where the swarm should converge.
type constrainedSphere struct{}

func (m constrainedSphere) Variables() []dls.VariableInfo {
	return []dls.VariableInfo{{Name: "x", Param: "x", Min: -2, Max: 2}}
}
func (m constrainedSphere) InitialState() []float64 { return []float64{1.5} }
func (m constrainedSphere) Options() dls.Options {
	return dls.Options{MaxIter: 200, Mu: 0.1, Tol: 1e-8, Epsilon: 1e-7}
}
func (m constrainedSphere) EvaluateMerit(x []float64) float64 { return x[0] * x[0] }
func (m constrainedSphere) ComputeResiduals(x []float64) []float64 {
	return []float64{x[0] - 1}
}
func (m constrainedSphere) ComputeConstraints(x []float64) []float64 {
	return []float64{x[0] - 1}
}

// runCriterion runs one PSO phase on constrainedSphere with the given
// selection criterion and returns the returned particle's position.
func runCriterion(t *testing.T, criterion string) float64 {
	t.Helper()
	cfg := DefaultConfig()
	cfg.SwarmSize = 30
	cfg.PsoIterations = 60
	cfg.InitSpread = 0.4
	cfg.ConstraintPenalty = 1000
	cfg.StallWindowFrac = 0.5
	cfg.StallRelTol = 1e-3
	cfg.Gbest = criterion

	ex := NewExplorer(cfg, 42, nil)
	r := ex.Explore(constrainedSphere{}, []float64{1.5})
	if r.Status == "interrupted" {
		t.Fatal("phase was interrupted")
	}
	return r.Variables[0].After
}

func TestGBestRankMerit(t *testing.T) {
	// With gbest ranked on the plain merit the swarm must follow the merit
	// minimum (x=0) and leave the constraint residual to the cycle's DLS:
	// mu*c^2 = 1000 would otherwise drag it to x = 1000/1001.
	x := runCriterion(t, GbestMerit)
	if math.Abs(x) > 0.2 {
		t.Errorf("gbest=merit: expected x≈0 (merit minimum), got %f", x)
	}
}

func TestGBestRankPenalised(t *testing.T) {
	// The same run ranked on the penalised fitness must follow the fitness
	// minimum (x = mu/(1+mu) ≈ 0.999) instead.
	x := runCriterion(t, GbestPenalised)
	if math.Abs(x-1) > 0.2 {
		t.Errorf("gbest=penalised: expected x≈1 (constraint minimum), got %f", x)
	}
}

func TestResultMeritsArePlain(t *testing.T) {
	// Whatever scale the swarm ranked on, the phase result reports the plain
	// merit of the start point and of the returned particle, so a cycle event,
	// a recorded minimum and a DLS phase result all stay on one scale.
	cfg := DefaultConfig()
	cfg.SwarmSize = 30
	cfg.PsoIterations = 60
	cfg.InitSpread = 0.4
	cfg.ConstraintPenalty = 1000
	cfg.Gbest = GbestPenalised

	ex := NewExplorer(cfg, 42, nil)
	r := ex.Explore(constrainedSphere{}, []float64{1.5})
	x := r.Variables[0].After
	if want := x * x; math.Abs(r.AfterMerit-want) > 1e-9 {
		t.Errorf("AfterMerit = %v, want the plain merit %v", r.AfterMerit, want)
	}
	if want := 1.5 * 1.5; math.Abs(r.BeforeMerit-want) > 1e-9 {
		t.Errorf("BeforeMerit = %v, want the plain merit at x0 = %v", r.BeforeMerit, want)
	}
}
