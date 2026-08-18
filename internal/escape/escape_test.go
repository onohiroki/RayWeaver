package escape

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
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
	cycle := NewCycle(wrapper, store, params, cfg.MaxCycles, 0, nil, time.Time{}, context.Background(), nil)

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
	cycle := NewCycle(wrapper, store, params, 10, 0, nil, deadline, context.Background(), nil)

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
	cycle := NewCycle(wrapper, store, params, cfg.MaxCycles, 0, nil, time.Time{}, context.Background(), nil)

	cycle.Run([]float64{0.8})

	if cycle.StoppedByTime() {
		t.Fatal("cycle without a deadline must not report stopped by time")
	}
	if store.Len() < 1 {
		t.Fatalf("expected at least one recorded minimum, got %d", store.Len())
	}
}

// slowShallow is a 1D model that barely descends and sleeps per residual
// evaluation, so a DLS solve stays in progress long enough for a test to
// interrupt it mid-run.
type slowShallow struct{}

func (slowShallow) Variables() []dls.VariableInfo {
	return []dls.VariableInfo{{Name: "x", Param: "x", Min: 0, Max: 1}}
}

func (slowShallow) InitialState() []float64 { return []float64{0.3} }

func (slowShallow) Options() dls.Options {
	return dls.Options{MaxIter: 100, Tol: 1e-14, Epsilon: 1e-6}
}

func (slowShallow) EvaluateMerit(x []float64) float64 {
	d := x[0] - 0.5
	return d * d * 1e-6
}

func (slowShallow) ComputeResiduals(x []float64) []float64 {
	time.Sleep(5 * time.Millisecond)
	d := x[0] - 0.5
	return []float64{d * 1e-3}
}

func (slowShallow) ComputeConstraints(x []float64) []float64 { return nil }

// TestCycleHardStopInterruptsMidSolve verifies that closing the mid-DLS stop
// channel aborts the running solve promptly, marks the cycle interrupted, and
// preserves the interrupted solve's best-so-far in the store.
func TestCycleHardStopInterruptsMidSolve(t *testing.T) {
	cfg := types.EscapeConfig{
		MaxCycles:         50,
		DistanceThreshold: 0.1,
		HInitial:          0.5,
		WInitial:          0.5,
		HMult:             2.0,
		WMult:             1.0,
	}
	params := BuildParams(cfg, slowShallow{}.Variables())
	store := NewStore(params)
	wrapper := NewWrapper(slowShallow{}, params)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hardStop := make(chan struct{})
	cycle := NewCycle(wrapper, store, params, cfg.MaxCycles, 0, nil, time.Time{}, ctx, hardStop)

	start := time.Now()
	done := make(chan struct{}, 1)
	go func() { cycle.Run([]float64{0.3}); done <- struct{}{} }()

	// Let a DLS solve get going, then mimic a second Ctrl-C: interrupt it
	// while it is running rather than waiting for the cycle boundary.
	time.Sleep(50 * time.Millisecond)
	close(hardStop)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cycle did not return promptly after the hard stop fired mid-solve")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("cycle took too long to stop: %v", time.Since(start))
	}
	if !cycle.Interrupted() {
		t.Fatal("Interrupted() must be true after a hard stop")
	}
	if store.Len() < 1 {
		t.Fatalf("expected the interrupted best-so-far to be recorded, store.Len()=%d", store.Len())
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

// TestParallelEscapeHardStop verifies that closing the mid-DLS stop channel
// aborts the running solves, records the interrupted best-so-far points, and
// reports Interrupted=true — the second-Ctrl-C path at the parallel level.
func TestParallelEscapeHardStop(t *testing.T) {
	cfg := types.EscapeConfig{
		MaxCycles:         50,
		EscapeWorkers:     2,
		DistanceThreshold: 0.1,
		HInitial:          0.5,
		WInitial:          0.5,
		HMult:             2.0,
		WMult:             1.0,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hardStop := make(chan struct{})
	resCh := make(chan Result, 1)
	go func() {
		resCh <- ParallelEscape(func() dls.Model { return slowShallow{} }, cfg, RunOptions{Context: ctx, HardStop: hardStop})
	}()

	// Let the workers get into long-running solves, then interrupt mid-run.
	time.Sleep(50 * time.Millisecond)
	close(hardStop)

	select {
	case res := <-resCh:
		if !res.Interrupted {
			t.Fatal("expected Interrupted=true after a hard stop")
		}
		if res.TimedOut {
			t.Fatal("a hard stop must not be reported as a time budget expiry")
		}
		if len(res.Minima) < 1 {
			t.Fatalf("expected interrupted best-so-far points recorded, got %d minima", len(res.Minima))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ParallelEscape did not return after the hard stop")
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

// fingerprintParams returns escape params for the fingerprint store tests: the
// two-well variable scale is 4 (x in [-2,2]) and the fingerprint distance
// threshold is 0.5.
func fingerprintParams() Params {
	cfg := types.EscapeConfig{
		HInitial:                     0.05,
		WInitial:                     0.5,
		HMult:                        2.0,
		WMult:                        1.3,
		DistanceThreshold:            0.1,
		FingerprintDistanceThreshold: 0.5,
	}
	return BuildParams(cfg, twoWell{}.Variables())
}

// TestStoreFingerprintCriterion verifies that IsNew treats a candidate as the
// same minimum only when it is close in variable space AND close in the design
// fingerprint: numerically-close but structurally-different points are distinct.
func TestStoreFingerprintCriterion(t *testing.T) {
	params := fingerprintParams()
	s := NewStore(params)
	// Step-function fingerprint: floor(x*20) changes at 0.05 increments, so a
	// variable-close candidate can still differ in fingerprint.
	s.SetFingerprint(func(x []float64) []float64 { return []float64{math.Floor(x[0] * 20)} })

	s.Add(Point{X: []float64{0.5}, Merit: 0})

	// Close in variables and close in fingerprint -> same minimum.
	if s.IsNew([]float64{0.52}) {
		t.Fatal("0.52 is variable- and fingerprint-close to 0.5; must be a repeat")
	}
	// Close in variables (varDist 0.02 < Dt) but fingerprint-different
	// (floor(0.58*20)=11 vs floor(0.5*20)=10) -> distinct minimum.
	if !s.IsNew([]float64{0.58}) {
		t.Fatal("0.58 is variable-close but fingerprint-different; must be new")
	}
	// Far in variables -> distinct regardless of the fingerprint.
	if !s.IsNew([]float64{1.5}) {
		t.Fatal("1.5 is variable-far; must be new")
	}

	// Add must store the fingerprint for the recorded point.
	if got := s.All()[0].Fingerprint; len(got) != 1 || got[0] != 10 {
		t.Fatalf("stored fingerprint = %v, want [10]", got)
	}

	// Replace must recompute the fingerprint from the improved X.
	s.Replace(0, Point{X: []float64{0.58}, Merit: -1})
	if got := s.All()[0].Fingerprint; len(got) != 1 || got[0] != 11 {
		t.Fatalf("replaced fingerprint = %v, want [11]", got)
	}
}

// TestStoreFingerprintDisabled verifies that a zero fingerprint threshold keeps
// the original variable-distance-only behaviour: fingerprint differences are
// ignored and a variable-close candidate is a repeat even when structurally
// different.
func TestStoreFingerprintDisabled(t *testing.T) {
	params := testParams() // DtFp = 0 -> criterion disabled
	s := NewStore(params)
	s.SetFingerprint(func(x []float64) []float64 { return []float64{math.Floor(x[0] * 20)} })

	s.Add(Point{X: []float64{0.5}, Merit: 0})
	if s.IsNew([]float64{0.58}) {
		t.Fatal("with the fingerprint criterion disabled, a variable-close candidate must be a repeat")
	}
}

// TestStoreFingerprintNoFunction verifies that a nil fingerprint function keeps
// the original variable-distance-only behaviour even when DtFp is set.
func TestStoreFingerprintNoFunction(t *testing.T) {
	s := NewStore(fingerprintParams()) // DtFp set, but no SetFingerprint call
	s.Add(Point{X: []float64{0.5}, Merit: 0})
	if s.IsNew([]float64{0.58}) {
		t.Fatal("without a fingerprint function, a variable-close candidate must be a repeat")
	}
}

// TestStoreFingerprintElementCountMismatch verifies that candidates whose
// fingerprint has a different number of elements (structural topology change)
// are always treated as distinct.
func TestStoreFingerprintElementCountMismatch(t *testing.T) {
	s := NewStore(fingerprintParams())
	s.SetFingerprint(func(x []float64) []float64 {
		if x[0] > 0.6 {
			return []float64{1, 2} // two-element design
		}
		return []float64{1} // one-element design
	})
	s.Add(Point{X: []float64{0.5}, Merit: 0})
	if !s.IsNew([]float64{0.62}) {
		t.Fatal("an element-count mismatch in the fingerprint must be treated as a distinct design")
	}
}

// TestBuildParamsFingerprintThreshold verifies that BuildParams applies the
// fingerprint distance threshold from the config.
func TestBuildParamsFingerprintThreshold(t *testing.T) {
	cfg := types.EscapeConfig{FingerprintDistanceThreshold: 0.02}
	p := BuildParams(cfg, twoWell{}.Variables())
	if p.DtFp != 0.02 {
		t.Fatalf("DtFp = %v, want 0.02", p.DtFp)
	}
}

// TestParallelEscapeRecordsFingerprints verifies that the fingerprint function
// passed through RunOptions is applied to every recorded minimum.
func TestParallelEscapeRecordsFingerprints(t *testing.T) {
	cfg := types.EscapeConfig{
		MaxCycles:                    8,
		EscapeWorkers:                2,
		DistanceThreshold:            0.1,
		FingerprintDistanceThreshold: 0.5,
		HInitial:                     0.5,
		WInitial:                     0.5,
		HMult:                        2.0,
		WMult:                        1.0,
	}
	fp := func(x []float64) []float64 { return []float64{math.Floor(x[0] * 20)} }
	res := ParallelEscape(func() dls.Model { return twoWell{} }, cfg, RunOptions{Fingerprint: fp})

	if len(res.Minima) < 2 {
		t.Fatalf("expected >=2 minima, got %d", len(res.Minima))
	}
	for _, p := range res.Minima {
		if len(p.Fingerprint) != 1 {
			t.Fatalf("minimum fingerprint = %v, want 1 element", p.Fingerprint)
		}
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

func TestProgressCompactAndFullStreams(t *testing.T) {
	var full, compact bytes.Buffer
	p := NewProgress()
	p.AddWriter(&full)
	p.AddCompactWriter(&compact)
	p.Event("cycle", map[string]any{
		"cycle":      7,
		"worker":     0,
		"phase":      "escape_dls",
		"status":     "accepted",
		"dls_status": "converged",
		"merit":      1.1168667646339256,
	})

	var fullEv struct {
		Event     string  `json:"event"`
		Time      string  `json:"time"`
		Elapsed   float64 `json:"elapsed"`
		Cycle     int     `json:"cycle"`
		Status    string  `json:"status"`
		DLSStatus string  `json:"dls_status"`
		Merit     float64 `json:"merit"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(full.Bytes()), &fullEv); err != nil {
		t.Fatalf("full json.Unmarshal: %v", err)
	}
	if fullEv.Status != "accepted" {
		t.Fatalf("full status = %q, want accepted (status retained in --log)", fullEv.Status)
	}
	if fullEv.Merit != 1.1168667646339256 {
		t.Fatalf("full merit = %v, want full precision", fullEv.Merit)
	}
	if fullEv.Elapsed < 0 {
		t.Fatalf("full elapsed = %v, want >= 0", fullEv.Elapsed)
	}

	var compactEv struct {
		Event   string  `json:"event"`
		T       string  `json:"t"`
		EMin    int64   `json:"e_min"`
		Cycle   int     `json:"cycle"`
		Merit   float64 `json:"merit"`
		Status  string  `json:"status"`
		Time    string  `json:"time"`
		Elapsed float64 `json:"elapsed"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(compact.Bytes()), &compactEv); err != nil {
		t.Fatalf("compact json.Unmarshal: %v", err)
	}
	if !regexp.MustCompile(`^\d{2}:\d{2}:\d{2}$`).MatchString(compactEv.T) {
		t.Fatalf("compact t = %q, want HH:MM:SS", compactEv.T)
	}
	if compactEv.Time != "" {
		t.Fatalf("compact must rename time to t, got time=%q", compactEv.Time)
	}
	if compactEv.Elapsed != 0 {
		t.Fatalf("compact must rename elapsed to e_min, got elapsed=%v", compactEv.Elapsed)
	}
	if compactEv.EMin < 0 {
		t.Fatalf("compact e_min = %d, want >= 0", compactEv.EMin)
	}
	if compactEv.Status != "" {
		t.Fatalf("compact must drop status, got %q", compactEv.Status)
	}

	got := string(bytes.TrimSpace(compact.Bytes()))
	if !strings.Contains(got, `"merit":1.11687e+00`) {
		t.Fatalf("compact merit not 6-sig-fig exponent: %s", got)
	}
	// Keys follow the fixed order: cycle, e_min, t, event, merit, worker,
	// dls_status, phase.
	wantOrder := []string{`"cycle":`, `"e_min":`, `"t":`, `"event":`, `"merit":`, `"worker":`, `"dls_status":`, `"phase":`}
	last := -1
	for _, k := range wantOrder {
		i := strings.Index(got, k)
		if i < 0 {
			t.Fatalf("compact line missing %s: %s", k, got)
		}
		if i <= last {
			t.Fatalf("compact key order violated (%s after %s): %s", k, wantOrder[last], got)
		}
		last = i
	}
}

func TestProgressCompactAllEvents(t *testing.T) {
	var full, compact bytes.Buffer
	p := NewProgress()
	p.AddWriter(&full)
	p.AddCompactWriter(&compact)

	emit := func(name string, fields map[string]any) {
		full.Reset()
		compact.Reset()
		p.Event(name, fields)
	}

	// params: the escape-parameter keys are shown with exponent floats.
	emit("params", map[string]any{
		"h": 0.1, "w": 0.5, "h_mult": 2.0, "w_mult": 1.3, "distance_threshold": 0.1,
	})
	got := string(bytes.TrimSpace(compact.Bytes()))
	for _, want := range []string{
		`"event":"params"`, `"h":1.00000e-01`, `"w":5.00000e-01`,
		`"h_mult":2.00000e+00`, `"w_mult":1.30000e+00`, `"distance_threshold":1.00000e-01`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("params compact missing %s: %s", want, got)
		}
	}

	// start: workers/max_cycles/max_seconds.
	emit("start", map[string]any{"workers": 4, "max_cycles": 10, "max_seconds": 3600.0})
	got = string(bytes.TrimSpace(compact.Bytes()))
	for _, want := range []string{`"event":"start"`, `"workers":4`, `"max_cycles":10`, `"max_seconds":3.60000e+03`} {
		if !strings.Contains(got, want) {
			t.Fatalf("start compact missing %s: %s", want, got)
		}
	}

	// worker_done: escaped/recorded shown, timed_out/interrupted hidden in the
	// compact stream but retained in the full one.
	emit("worker_done", map[string]any{"worker": 3, "escaped": 5, "recorded": 3, "timed_out": false, "interrupted": false})
	got = string(bytes.TrimSpace(compact.Bytes()))
	if !strings.Contains(got, `"escaped":5`) || !strings.Contains(got, `"recorded":3`) {
		t.Fatalf("worker_done compact missing escaped/recorded: %s", got)
	}
	if strings.Contains(got, "timed_out") || strings.Contains(got, `"interrupted"`) {
		t.Fatalf("worker_done compact must hide timed_out/interrupted: %s", got)
	}
	if !strings.Contains(full.String(), `"timed_out":false`) || !strings.Contains(full.String(), `"interrupted":false`) {
		t.Fatalf("worker_done full must keep timed_out/interrupted: %s", full.String())
	}

	// done: aggregates shown, timed_out/interrupted hidden.
	emit("done", map[string]any{"workers": 4, "cycles": 8, "escapes": 18, "minima": 5, "best_merit": 0.3551473063945567, "timed_out": false, "interrupted": false})
	got = string(bytes.TrimSpace(compact.Bytes()))
	for _, want := range []string{`"event":"done"`, `"workers":4`, `"best_merit":3.55147e-01`, `"cycles":8`, `"escapes":18`, `"minima":5`} {
		if !strings.Contains(got, want) {
			t.Fatalf("done compact missing %s: %s", want, got)
		}
	}
	if strings.Contains(got, "timed_out") || strings.Contains(got, `"interrupted"`) {
		t.Fatalf("done compact must hide timed_out/interrupted: %s", got)
	}
	if !strings.Contains(full.String(), `"timed_out":false`) || !strings.Contains(full.String(), `"interrupted":false`) {
		t.Fatalf("done full must keep timed_out/interrupted: %s", full.String())
	}

	// timeout: max_cycles shown.
	emit("timeout", map[string]any{"worker": 3, "cycle": 6, "max_cycles": 10})
	got = string(bytes.TrimSpace(compact.Bytes()))
	if !strings.Contains(got, `"event":"timeout"`) || !strings.Contains(got, `"max_cycles":10`) {
		t.Fatalf("timeout compact missing keys: %s", got)
	}

	// interrupt: signal hidden in compact, kept in full.
	emit("interrupt", map[string]any{"signal": "interrupt"})
	got = string(bytes.TrimSpace(compact.Bytes()))
	if strings.Contains(got, "signal") {
		t.Fatalf("interrupt compact must hide signal: %s", got)
	}
	if !strings.Contains(full.String(), `"signal":"interrupt"`) {
		t.Fatalf("interrupt full must keep signal: %s", full.String())
	}
}

func TestBuildParamsAppliesExecutionTuning(t *testing.T) {
	early := false
	cfg := types.EscapeConfig{
		EscapeIterFrac:  0.25,
		WSpan:           3.0,
		StallWindowFrac: 0.5,
		StallRelTol:     1e-3,
		StallEarlyStop:  &early,
		InitialPerturb:  0.1,
	}
	p := BuildParams(cfg, twoWell{}.Variables())
	if p.EscapeIterFrac != 0.25 || p.WSpan != 3.0 || p.StallWindowFrac != 0.5 ||
		p.StallRelTol != 1e-3 || p.StallEarlyStop || p.InitialPerturb != 0.1 {
		t.Fatalf("tuning not applied: %+v", p)
	}
}

func TestDefaultParamsTuning(t *testing.T) {
	p := DefaultParams()
	if p.EscapeIterFrac != 1.0/3.0 || p.WSpan != 2.0 || p.StallWindowFrac != 0.2 ||
		p.StallRelTol != 1e-4 || !p.StallEarlyStop || p.InitialPerturb != 0.05 {
		t.Fatalf("unexpected tuning defaults: %+v", p)
	}
}

func TestWrapperOptionsPhaseLimitsMaxIter(t *testing.T) {
	params := testParams()
	params.EscapeIterFrac = 0.25
	w := NewWrapper(twoWell{}, params)

	w.SetPhase(PhaseEscape)
	opts := w.Options()
	// twoWell.Options() returns MaxIter=200; capped to 200*0.25 = 50.
	if opts.MaxIter != 50 {
		t.Fatalf("PhaseEscape MaxIter = %d, want 50", opts.MaxIter)
	}
	if !opts.EnableStallDone {
		t.Fatal("PhaseEscape should enable stalled early stop")
	}
	if opts.StallWindowFrac != params.StallWindowFrac || opts.StallRelTol != params.StallRelTol {
		t.Fatalf("stall tuning not propagated: %+v", opts)
	}

	w.SetPhase(PhaseClean)
	opts = w.Options()
	if opts.MaxIter != 200 {
		t.Fatalf("PhaseClean MaxIter = %d, want 200 (full budget)", opts.MaxIter)
	}
	if opts.EnableStallDone {
		t.Fatal("PhaseClean should not enable stalled early stop")
	}
}

func TestWrapperOptionsDisableStallEarlyStop(t *testing.T) {
	params := testParams()
	params.StallEarlyStop = false
	w := NewWrapper(twoWell{}, params)
	w.SetPhase(PhaseEscape)
	if w.Options().EnableStallDone {
		t.Fatal("StallEarlyStop=false should keep stalled stop disabled")
	}
}
