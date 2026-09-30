package escape

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/types"
)

// hullWell wraps the twoWell merit with a fake "glass hull": inside means
// x <= 0.49, so the converged well at +0.5 sits "outside". RescueGlassHull
// clamps an outside point back to the fake boundary and records the call.
type hullWell struct {
	twoWell
	mu     sync.Mutex
	called int
}

func (m *hullWell) RescueGlassHull(x []float64) ([]float64, bool) {
	m.mu.Lock()
	m.called++
	m.mu.Unlock()
	if x[0] <= 0.49 {
		return nil, false
	}
	out := make([]float64, len(x))
	copy(out, x)
	out[0] = 0.49
	return out, true
}

func (m *hullWell) rescueCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.called
}

func rescueTestConfig(maxCycles int) types.EscapeConfig {
	return types.EscapeConfig{
		MaxCycles:         maxCycles,
		EscapeWorkers:     1,
		DistanceThreshold: 0.1,
		HInitial:          0.5,
		WInitial:          0.5,
		HMult:             2.0,
		WMult:             1.0,
	}
}

// TestCycleHullRescueRecordsFeasible: a converged point classified as a hull
// violation is projected back onto the hull, re-solved with a short clean
// DLS, and — when the re-solve passes validation — recorded as a feasible
// minimum instead of an infeasible basin.
func TestCycleHullRescueRecordsFeasible(t *testing.T) {
	cfg := rescueTestConfig(4)
	params := BuildParams(cfg, twoWell{}.Variables())
	if !params.HullRescue {
		t.Fatal("HullRescue should default to on")
	}
	inner := &hullWell{}
	w := NewWrapper(inner, params)
	store := NewStore(params)

	// The first classification reports a hull violation; every later one is
	// feasible (the rescue re-solve is the second validation call).
	calls := 0
	validateFn := func(x []float64, merit float64, m dls.Model) (MinStatus, InvalidReason) {
		calls++
		if calls == 1 {
			return MinStatusInfeasibleBasin, ReasonGlassHullViolation
		}
		return MinStatusFeasibleLocalMinimum, ReasonNone
	}

	cycle := NewCycle(w, store, params, cfg.MaxCycles, 0, nil, time.Time{},
		context.Background(), nil, validateFn, nil, false)
	cycle.Run([]float64{0.8})

	if inner.rescueCalls() == 0 {
		t.Fatal("expected RescueGlassHull to be called for a hull violation")
	}
	if store.Len() == 0 {
		t.Fatal("expected at least one recorded point")
	}
	for _, p := range store.All() {
		if p.Status != MinStatusFeasibleLocalMinimum {
			t.Errorf("recorded point status = %v, want feasible (point %+v)", p.Status, p)
		}
	}
}

// TestCycleHullRescueDisabled: with hull_rescue off the violated point keeps
// its infeasible classification and the model's repair capability is never
// touched.
func TestCycleHullRescueDisabled(t *testing.T) {
	off := false
	cfg := rescueTestConfig(3)
	cfg.HullRescue = &off
	params := BuildParams(cfg, twoWell{}.Variables())
	if params.HullRescue {
		t.Fatal("HullRescue should be off when the config disables it")
	}
	inner := &hullWell{}
	w := NewWrapper(inner, params)
	store := NewStore(params)

	// Always a hull violation: without the rescue nothing can become feasible.
	validateFn := func(x []float64, merit float64, m dls.Model) (MinStatus, InvalidReason) {
		return MinStatusInfeasibleBasin, ReasonGlassHullViolation
	}

	cycle := NewCycle(w, store, params, cfg.MaxCycles, 0, nil, time.Time{},
		context.Background(), nil, validateFn, nil, false)
	cycle.Run([]float64{0.8})

	if inner.rescueCalls() != 0 {
		t.Errorf("RescueGlassHull called %d times, want 0 with hull_rescue off", inner.rescueCalls())
	}
	if store.Len() == 0 {
		t.Fatal("expected the infeasible basin to be recorded anyway")
	}
	for _, p := range store.All() {
		if p.Status != MinStatusInfeasibleBasin {
			t.Errorf("recorded point status = %v, want infeasible basin", p.Status)
		}
	}
}

// TestBuildParamsHullRescue: defaults (on, 0.25) and the config overrides.
func TestBuildParamsHullRescue(t *testing.T) {
	p := BuildParams(types.EscapeConfig{}, twoWell{}.Variables())
	if !p.HullRescue {
		t.Error("default HullRescue = false, want true")
	}
	if p.HullRescueIterFrac != 0.25 {
		t.Errorf("default HullRescueIterFrac = %v, want 0.25", p.HullRescueIterFrac)
	}

	off := false
	p = BuildParams(types.EscapeConfig{HullRescue: &off, HullRescueIterFrac: 0.5}, twoWell{}.Variables())
	if p.HullRescue {
		t.Error("config hull_rescue: false ignored")
	}
	if p.HullRescueIterFrac != 0.5 {
		t.Errorf("config hull_rescue_iter_frac = %v, want 0.5", p.HullRescueIterFrac)
	}
}

// phaseRecorder captures SetEscapePhase calls forwarded by Wrapper.SetPhase.
type phaseRecorder struct {
	twoWell
	mu   sync.Mutex
	got  []float64
	seen bool
}

func (m *phaseRecorder) SetEscapePhase(p float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seen = true
	m.got = append(m.got, p)
}

// TestWrapperSetPhaseForwardsEscapePhase: SetPhase pushes the phase metric to
// the inner model immediately, so phase-dependent merit shaping (the "phase"
// schedule metric, the hull escape_weight_factor) is never stale even for
// solve paths that skip ensurePhaseWeights (the PSO explorer).
func TestWrapperSetPhaseForwardsEscapePhase(t *testing.T) {
	params := testParams()
	inner := &phaseRecorder{}
	w := NewWrapper(inner, params)

	w.SetPhase(PhaseEscape)
	w.SetPhase(PhaseGlassSolve)
	w.SetPhase(PhaseClean)

	if !inner.seen {
		t.Fatal("SetPhase did not forward SetEscapePhase to the inner model")
	}
	want := []float64{0, 0.5, 1}
	if len(inner.got) != len(want) {
		t.Fatalf("forwarded phases = %v, want %v", inner.got, want)
	}
	for i := range want {
		if inner.got[i] != want[i] {
			t.Errorf("forwarded phase[%d] = %v, want %v", i, inner.got[i], want[i])
		}
	}
}

// TestWrapperRescueModeReducesMaxIter: the rescue re-solve runs on
// hull_rescue_iter_frac of the full clean budget, and the reduced budget
// applies only while rescue mode is on.
func TestWrapperRescueModeReducesMaxIter(t *testing.T) {
	params := testParams() // HullRescueIterFrac 0.25
	w := NewWrapper(twoWell{}, params)
	w.SetPhase(PhaseClean)

	base := w.Options().MaxIter
	if base != 200 {
		t.Fatalf("clean-phase MaxIter = %d, want 200", base)
	}
	w.SetRescueMode(true)
	if got := w.Options().MaxIter; got != 50 {
		t.Errorf("rescue MaxIter = %d, want %d (200*0.25)", got, 50)
	}
	w.SetRescueMode(false)
	if got := w.Options().MaxIter; got != base {
		t.Errorf("MaxIter after rescue mode off = %d, want %d", got, base)
	}

	params.HullRescueIterFrac = 0.5
	w2 := NewWrapper(twoWell{}, params)
	w2.SetPhase(PhaseClean)
	w2.SetRescueMode(true)
	if got := w2.Options().MaxIter; got != 100 {
		t.Errorf("rescue MaxIter with frac 0.5 = %d, want 100", got)
	}
}
