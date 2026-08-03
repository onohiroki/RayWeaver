package escape

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

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
	cycle := NewCycle(wrapper, store, params, cfg.MaxCycles, 0, nil, time.Time{}, context.Background())

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
	res := ParallelEscape(func() dls.Model { return twoWell{} }, cfg, RunOptions{})

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

func TestCycleTimeBudgetStopsEarly(t *testing.T) {
	cfg := types.EscapeConfig{HInitial: 0.5, WInitial: 0.5, HMult: 2.0, WMult: 1.0}
	params := BuildParams(cfg, twoWell{}.Variables())
	store := NewStore(params)
	wrapper := NewWrapper(twoWell{}, params)
	// A deadline already in the past forces the cycle to stop before the
	// first DLS run: no escapes recorded, and StoppedByTime is set.
	deadline := time.Now().Add(-time.Second)
	cycle := NewCycle(wrapper, store, params, 10, 0, nil, deadline, context.Background())

	cycle.Run([]float64{0.8})

	if !cycle.StoppedByTime() {
		t.Fatal("expected cycle stopped by time budget")
	}
	if cycle.Escaped() != 0 {
		t.Fatalf("escaped = %d, want 0 (no DLS before expiry)", cycle.Escaped())
	}
	if store.Len() != 0 {
		t.Fatalf("no minima should be recorded before expiry, got %d", store.Len())
	}
}

func TestCycleNoDeadlineRunsToCompletion(t *testing.T) {
	cfg := types.EscapeConfig{
		MaxCycles:         3,
		DistanceThreshold: 0.1,
		HInitial:          0.5,
		WInitial:          0.5,
		HMult:             2.0,
		WMult:             1.0,
	}
	params := BuildParams(cfg, twoWell{}.Variables())
	store := NewStore(params)
	wrapper := NewWrapper(twoWell{}, params)
	cycle := NewCycle(wrapper, store, params, cfg.MaxCycles, 0, nil, time.Time{}, context.Background())

	cycle.Run([]float64{0.8})

	if cycle.StoppedByTime() {
		t.Fatal("cycle without a deadline must not report stopped by time")
	}
	if store.Len() < 1 {
		t.Fatalf("expected at least one recorded minimum, got %d", store.Len())
	}
}

func TestParallelEscapeTimeBudget(t *testing.T) {
	cfg := types.EscapeConfig{
		MaxCycles:     8,
		EscapeWorkers: 2,
		MaxSeconds:    1e-9, // 1ns budget -> always expired before the first cycle
	}
	res := ParallelEscape(func() dls.Model { return twoWell{} }, cfg, RunOptions{})

	if !res.TimedOut {
		t.Fatal("expected TimedOut=true with a tiny budget")
	}
	if res.MaxSeconds != 1e-9 {
		t.Fatalf("MaxSeconds = %v, want 1e-9", res.MaxSeconds)
	}
}

func TestParallelEscapeInterrupt(t *testing.T) {
	cfg := types.EscapeConfig{MaxCycles: 8, EscapeWorkers: 2}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled: workers must stop before the first DLS run

	res := ParallelEscape(func() dls.Model { return twoWell{} }, cfg, RunOptions{Context: ctx})

	if !res.Interrupted {
		t.Fatal("expected Interrupted=true with a cancelled context")
	}
	if res.TimedOut {
		t.Fatal("an interrupt must not be reported as a time budget expiry")
	}
	if len(res.Minima) != 0 {
		t.Fatalf("no minima expected before any DLS run, got %d", len(res.Minima))
	}
}

func TestParallelEscapeOnRecord(t *testing.T) {
	cfg := types.EscapeConfig{
		MaxCycles:         6,
		EscapeWorkers:     1,
		DistanceThreshold: 0.1,
		HInitial:          0.5,
		WInitial:          0.5,
		HMult:             2.0,
		WMult:             1.0,
	}
	var mu sync.Mutex
	var records []int // idx of every record event (new and improved)
	var newIndices []int
	var improved []int
	onRecord := func(idx int, p Point, isNew bool, version int) {
		mu.Lock()
		defer mu.Unlock()
		records = append(records, idx)
		if isNew {
			newIndices = append(newIndices, idx)
		} else {
			improved = append(improved, idx)
		}
	}

	res := ParallelEscape(func() dls.Model { return twoWell{} }, cfg, RunOptions{OnRecord: onRecord})

	if len(res.Minima) == 0 {
		t.Fatal("expected at least one minimum")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(records) == 0 {
		t.Fatal("expected onRecord to fire")
	}
	if len(newIndices) != len(res.Minima) {
		t.Fatalf("new records = %d, minima = %d", len(newIndices), len(res.Minima))
	}
	// Discovery indices must be distinct 0..n-1.
	seen := map[int]bool{}
	for _, idx := range newIndices {
		if seen[idx] {
			t.Fatalf("duplicate discovery index %d", idx)
		}
		seen[idx] = true
	}
	for i := range res.Minima {
		if !seen[i] {
			t.Fatalf("discovery index %d missing from onRecord", i)
		}
	}
}

func TestStoreReplaceKeepsBetterMerit(t *testing.T) {
	params := testParams()
	s := NewStore(params)
	idx := s.Add(Point{X: []float64{0.5}, Merit: 1.0})

	// Cycle repeat branch: strengthen the bump, then replace when better.
	s.Strengthen(idx)
	p, improved := s.Replace(idx, Point{X: []float64{0.52}, Merit: 0.8})
	if !improved {
		t.Fatal("expected replacement when the new merit is better")
	}
	if p.Merit != 0.8 {
		t.Fatalf("stored merit = %v, want 0.8", p.Merit)
	}
	if p.X[0] != 0.52 {
		t.Fatalf("stored X = %v, want [0.52]", p.X)
	}
	if p.H <= params.H {
		t.Fatalf("escape strength must be kept from the strengthened point, got H=%v", p.H)
	}
	if v := s.Version(idx); v != 1 {
		t.Fatalf("version = %d, want 1", v)
	}

	// A worse repeat must not replace or bump the version.
	s.Strengthen(idx)
	p, improved = s.Replace(idx, Point{X: []float64{0.53}, Merit: 0.9})
	if improved {
		t.Fatal("a worse repeat must not replace the stored point")
	}
	if p.Merit != 0.8 {
		t.Fatalf("stored merit changed on worse repeat = %v, want 0.8", p.Merit)
	}
	if v := s.Version(idx); v != 1 {
		t.Fatalf("version = %d, want 1 (worse repeat must not bump it)", v)
	}
}

func TestStoreOnRecord(t *testing.T) {
	params := testParams()
	s := NewStore(params)
	var events []string
	s.SetOnRecord(func(idx int, p Point, isNew bool, version int) {
		events = append(events, fmt.Sprintf("%d:%v:%d", idx, isNew, version))
	})

	s.Add(Point{X: []float64{0.5}, Merit: 1.0})
	if got := events; len(got) != 1 || got[0] != "0:true:0" {
		t.Fatalf("Add onRecord = %v, want [0:true:0]", got)
	}

	s.Replace(0, Point{X: []float64{0.52}, Merit: 0.8})
	if got := events; len(got) != 2 || got[1] != "0:false:1" {
		t.Fatalf("Replace onRecord = %v, want second event [0:false:1]", got)
	}

	// A non-improving replace must not fire.
	s.Replace(0, Point{X: []float64{0.53}, Merit: 0.9})
	if got := events; len(got) != 2 {
		t.Fatalf("worse replace must not fire onRecord, got %d events: %v", len(got), got)
	}
}

func TestProgressEventsCarryTimeAndElapsed(t *testing.T) {
	var buf bytes.Buffer
	p := NewProgress()
	p.AddWriter(&buf)
	p.Event("start", map[string]any{"workers": 1})

	var ev struct {
		Event   string  `json:"event"`
		Time    string  `json:"time"`
		Elapsed float64 `json:"elapsed"`
		Workers int     `json:"workers"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &ev); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if ev.Event != "start" {
		t.Fatalf("event = %q, want start", ev.Event)
	}
	if ev.Time == "" {
		t.Fatal("expected a wall-clock time field")
	}
	if ev.Elapsed < 0 {
		t.Fatalf("elapsed = %v, want >= 0", ev.Elapsed)
	}
	if ev.Workers != 1 {
		t.Fatalf("workers = %d, want 1", ev.Workers)
	}
}
