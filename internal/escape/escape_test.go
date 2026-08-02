package escape

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/types"
)

// twoWell is a 1D function f(x) = (x² - 0.25)² with two global minima at
// x = ±0.5 and a local maximum at x = 0. A perfect test bed for escape.
type twoWell struct{}

func (twoWell) Variables() []dls.VariableInfo {
	return []dls.VariableInfo{{Name: "x", Param: "x", Min: -2, Max: 2}}
}

func (twoWell) InitialState() []float64 { return []float64{0} }

func (twoWell) Options() dls.Options {
	return dls.Options{MaxIter: 200, Mu: 0.1, Tol: 1e-8, Epsilon: 1e-7}
}

func (twoWell) EvaluateMerit(x []float64) float64 {
	r := x[0]*x[0] - 0.25
	return r * r
}

func (twoWell) ComputeResiduals(x []float64) []float64 {
	return []float64{x[0]*x[0] - 0.25}
}

func (twoWell) ComputeConstraints(x []float64) []float64 { return nil }

func testParams() Params {
	cfg := types.EscapeConfig{
		HInitial:          0.05,
		WInitial:          0.5,
		HMult:             2.0,
		WMult:             1.3,
		DistanceThreshold: 0.1,
	}
	return BuildParams(cfg, twoWell{}.Variables())
}

func TestTwoWellDLSConverges(t *testing.T) {
	r := dls.Solve(twoWell{})
	if !isConverged(r.Status) {
		t.Fatalf("expected convergence, got status %q", r.Status)
	}
	// From x=0 (local max) the solver should be pushed to one of the wells.
	if math.Abs(r.Variables[0].After) < 0.3 {
		t.Fatalf("expected to land near a well (±0.5), got x=%v", r.Variables[0].After)
	}
}

func TestWrapperAddsEscapeTerm(t *testing.T) {
	params := testParams()
	w := NewWrapper(twoWell{}, params)
	w.SetEscapes([]Point{{X: []float64{0.5}, Merit: 0, H: params.H, W: params.W}})

	// At the escape centre the merit is raised by H.
	mAtCenter := w.EvaluateMerit([]float64{0.5})
	want := twoWell{}.EvaluateMerit([]float64{0.5}) + params.H
	if math.Abs(mAtCenter-want) > 1e-9 {
		t.Fatalf("merit at centre = %v, want %v", mAtCenter, want)
	}

	// At a non-centre point the escape term equals H*exp(-d²/W²) exactly.
	xFar := []float64{1.2}
	mFar := w.EvaluateMerit(xFar)
	innerFar := twoWell{}.EvaluateMerit(xFar)
	d2 := ((xFar[0] - 0.5) / 4.0) * ((xFar[0] - 0.5) / 4.0)
	wantEscape := params.H * math.Exp(-d2/(params.W*params.W))
	wantFar := innerFar + wantEscape
	if math.Abs(mFar-wantFar) > 1e-9 {
		t.Fatalf("merit = %v, want %v (inner=%v escape=%v)", mFar, wantFar, innerFar, wantEscape)
	}
}

func TestWrapperResidualCount(t *testing.T) {
	params := testParams()
	w := NewWrapper(twoWell{}, params)
	w.SetEscapes([]Point{{X: []float64{0.5}, H: 0.05, W: 0.5}, {X: []float64{-0.5}, H: 0.05, W: 0.5}})

	r := w.ComputeResiduals([]float64{0.5}) // at the first escape centre
	if len(r) != 3 {                        // 1 inner + 2 escape residuals
		t.Fatalf("expected 3 residuals, got %d", len(r))
	}

	// Escape residual squared must equal the escape term added to merit: at
	// the escape centre d²=0 so r² = H.
	if math.Abs(r[1]*r[1]-0.05) > 1e-9 {
		t.Fatalf("escape residual squared = %v, want 0.05", r[1]*r[1])
	}

	// At the second escape centre the first escape contributes via its width.
	r2 := w.ComputeResiduals([]float64{-0.5})
	if math.Abs(r2[2]*r2[2]-0.05) > 1e-9 {
		t.Fatalf("second escape residual squared = %v, want 0.05", r2[2]*r2[2])
	}
}

func TestWrapperDisablesStallEscape(t *testing.T) {
	params := testParams()
	w := NewWrapper(twoWell{}, params)
	if !w.Options().DisableStallEscape {
		t.Fatal("escape wrapper must disable DLS stall escape")
	}
}

func TestWrapperCustomStartX(t *testing.T) {
	params := testParams()
	w := NewWrapper(twoWell{}, params)
	if got := w.InitialState()[0]; got != 0 {
		t.Fatalf("initial state = %v, want 0", got)
	}
	w.SetStartX([]float64{1.2})
	if got := w.InitialState()[0]; got != 1.2 {
		t.Fatalf("custom initial state = %v, want 1.2", got)
	}
}

func TestStoreBasic(t *testing.T) {
	s := NewStore(testParams())
	s.Add(Point{X: []float64{0.5}, Merit: 0})
	s.Add(Point{X: []float64{-0.5}, Merit: 0})

	if s.Len() != 2 {
		t.Fatalf("len = %d, want 2", s.Len())
	}

	d, idx := s.FindNearest([]float64{0.45})
	if d > 0.05 {
		t.Fatalf("nearest distance = %v, want small", d)
	}
	if idx != 0 {
		t.Fatalf("nearest index = %d, want 0", idx)
	}

	if s.IsNew([]float64{0.4}) {
		t.Fatal("0.4 should be near known minimum 0.5 (below Dt)")
	}
	if !s.IsNew([]float64{1.5}) {
		t.Fatal("1.5 should be a new region")
	}

	s.Strengthen(0)
	p, _ := s.Best()
	if s.All()[0].H <= 0.05 {
		t.Fatalf("strengthen did not raise H: %v", s.All()[0].H)
	}
	if p.Merit != 0 {
		t.Fatalf("best merit = %v, want 0", p.Merit)
	}
}

func TestBuildParamsExcludesFixedVariables(t *testing.T) {
	cfg := types.EscapeConfig{
		HInitial:          0.2,
		WInitial:          0.7,
		HMult:             2.5,
		WMult:             1.5,
		DistanceThreshold: 0.3,
		VariableWeights:   map[string]float64{"curvature": 100},
	}
	variables := []dls.VariableInfo{
		{Name: "c1", Param: "curvature", Min: -1, Max: 1},
		{Name: "t1", Param: "thickness", Min: 1, Max: 1}, // fixed
	}
	p := BuildParams(cfg, variables)
	if p.H != 0.2 || p.W != 0.7 || p.HMult != 2.5 || p.WMult != 1.5 || p.Dt != 0.3 {
		t.Fatalf("params not applied: %+v", p)
	}
	if len(p.Active) != 1 || p.Active[0] != 0 {
		t.Fatalf("active indices = %v, want [0] (fixed var excluded)", p.Active)
	}
	if p.Weights[0] != 100 || p.Weights[1] != 1 {
		t.Fatalf("weights = %v, want [100 1]", p.Weights)
	}
}

func TestCycleFindsTwoMinima(t *testing.T) {
	cfg := types.EscapeConfig{
		MaxCycles:         10,
		EscapeWorkers:     1,
		DistanceThreshold: 0.1,
		HInitial:          0.5,
		WInitial:          0.5,
		HMult:             2.0,
		WMult:             1.0,
	}
	params := BuildParams(cfg, twoWell{}.Variables())
	store := NewStore(params)
	wrapper := NewWrapper(twoWell{}, params)
	cycle := NewCycle(wrapper, store, params, cfg.MaxCycles, 0)

	bestX, bestMerit := cycle.Run([]float64{0.8})

	if store.Len() < 2 {
		t.Fatalf("expected at least 2 distinct minima, got %d (points=%+v)", store.Len(), store.All())
	}
	if bestMerit > 1e-6 {
		t.Fatalf("best merit = %v, want ~0", bestMerit)
	}
	// Both wells have merit ~0 and lie near ±0.5.
	seen := map[float64]bool{}
	for _, p := range store.All() {
		seen[math.Round(p.X[0]*10)/10] = true
	}
	if !seen[0.5] || !seen[-0.5] {
		t.Fatalf("expected both wells ±0.5, got %v", seen)
	}
	_ = bestX
}

func TestParallelEscapeFindsBothWells(t *testing.T) {
	cfg := types.EscapeConfig{
		MaxCycles:         8,
		EscapeWorkers:     2,
		DistanceThreshold: 0.1,
		HInitial:          0.5,
		WInitial:          0.5,
		HMult:             2.0,
		WMult:             1.0,
	}
	res := ParallelEscape(func() dls.Model { return twoWell{} }, cfg)

	if len(res.Minima) < 2 {
		t.Fatalf("expected >=2 minima, got %d (%+v)", len(res.Minima), res.Minima)
	}
	if res.BestMerit > 1e-6 {
		t.Fatalf("best merit = %v, want ~0", res.BestMerit)
	}
	if res.Workers != 2 {
		t.Fatalf("workers = %d, want 2", res.Workers)
	}
	if res.Cycles != 8 {
		t.Fatalf("cycles = %d, want 8", res.Cycles)
	}
}
