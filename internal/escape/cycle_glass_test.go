package escape

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/types"
)

// glassSettleMock is a one-variable dls.Model implementing both optional
// escape capabilities — glassPhaseable (enter/exit) and powerSettler (the
// power-compensation write-back) — and it records the order in which the cycle
// uses them.
//
// Its ordinary merit is (x-0.2)² + 10 and the glass phase swaps it for
// (x-0.9)², so a glass solve drags x to ~0.9 while the settle returns a third,
// deliberately different point (~0.95) standing in for the compensated
// curvature. The +10 keeps the ordinary merit well away from zero, so the
// cycle's acceptance test has real numbers on both sides instead of resting on
// its "startMain ~ 0" escape hatch. Three distinct points (0.2 / 0.9 / 0.95)
// are what lets the test tell "the clean phase inherited the settled vector"
// from "it inherited the raw glass result" or "it fell back to the escape
// result".
type glassSettleMock struct {
	mu      sync.Mutex
	events  []string
	inGlass bool
}

func (m *glassSettleMock) Variables() []dls.VariableInfo {
	return []dls.VariableInfo{{Name: "x", Param: "x", Min: -2, Max: 2}}
}

func (m *glassSettleMock) InitialState() []float64 { return []float64{0.8} }

func (m *glassSettleMock) Options() dls.Options {
	return dls.Options{MaxIter: 200, Mu: 0.1, Tol: 1e-10, Epsilon: 1e-12}
}

// EvaluateMerit logs every state the solver reads, so the test can walk the
// sequence a cycle produced rather than inspect the wrapper's internals.
func (m *glassSettleMock) EvaluateMerit(x []float64) float64 {
	m.mu.Lock()
	m.events = append(m.events, fmt.Sprintf("eval %.17g", x[0]))
	m.mu.Unlock()
	if m.inGlass {
		return (x[0] - 0.9) * (x[0] - 0.9)
	}
	r := x[0] - 0.2
	return r*r + 10
}

// ComputeResiduals stays consistent with EvaluateMerit (Σ r² == merit), so the
// solver's line search and its merit bookkeeping agree.
func (m *glassSettleMock) ComputeResiduals(x []float64) []float64 {
	if m.inGlass {
		return []float64{x[0] - 0.9}
	}
	return []float64{x[0] - 0.2, math.Sqrt(10)}
}

func (m *glassSettleMock) ComputeConstraints(x []float64) []float64 { return nil }

func (m *glassSettleMock) EnterGlassPhase(x []float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, "enter")
	m.inGlass = true
}

func (m *glassSettleMock) ExitGlassPhase() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, "exit")
	m.inGlass = false
}

// SettlePowerSolve stands in for the curvature write-back: the point the clean
// phase must start from is the glass result plus the compensation, which is
// deliberately neither the raw glass result nor the escape result.
func (m *glassSettleMock) SettlePowerSolve(x []float64) []float64 {
	out := []float64{x[0] + 0.05}
	m.mu.Lock()
	m.events = append(m.events, fmt.Sprintf("settle %.17g", out[0]))
	m.mu.Unlock()
	return out
}

func (m *glassSettleMock) snapshot() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.events...)
}

// eventAt returns the numeric payload of events[i] when it starts with prefix.
func eventAt(events []string, i int, prefix string) (float64, bool) {
	if i < 0 || i >= len(events) || !strings.HasPrefix(events[i], prefix) {
		return 0, false
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(events[i], prefix)), 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func indexOfEvent(events []string, exact string) int {
	for i, e := range events {
		if e == exact {
			return i
		}
	}
	return -1
}

// TestCycleGlassPhaseSettlesPowerIntoCleanStart pins the glass-phase
// sequencing of the escape cycle:
//
//  1. the glass phase is left before it is judged (so the acceptance test reads
//     the solve-off lens the clean phase actually inherits), and
//  2. the power-preserving solve's compensation is written back into the
//     variable vector, and that settled vector — not the raw glass result —
//     is what the clean DLS starts from.
//
// The cycle under test is one pass of Cycle.Run with the glass phase enabled.
func TestCycleGlassPhaseSettlesPowerIntoCleanStart(t *testing.T) {
	cfg := types.EscapeConfig{
		MaxCycles:         1,
		EscapeWorkers:     1,
		DistanceThreshold: 0.1,
		HInitial:          0.5,
		WInitial:          0.5,
		HMult:             2.0,
		WMult:             1.0,
	}
	inner := &glassSettleMock{}
	params := BuildParams(cfg, inner.Variables())
	store := NewStore(params)
	w := NewWrapper(inner, params)
	w.SetGlassPhase(true)

	cycle := NewCycle(w, store, params, cfg.MaxCycles, 0, nil, time.Time{},
		context.Background(), nil, nil, nil, false)
	cycle.Run([]float64{0.8})

	events := inner.snapshot()

	enter := indexOfEvent(events, "enter")
	exit := indexOfEvent(events, "exit")
	settle := -1
	for i, e := range events {
		if strings.HasPrefix(e, "settle ") {
			settle = i
			break
		}
	}
	if enter < 0 {
		t.Fatalf("the glass phase was never entered; events: %v", events)
	}
	if exit < 0 {
		t.Fatalf("the glass phase was never exited; events: %v", events)
	}
	if settle < 0 {
		t.Fatalf("SettlePowerSolve was never called; events: %v", events)
	}
	if exit > settle {
		t.Errorf("the settle (event %d) ran before the glass phase exit (event %d); "+
			"it would be clamped by the phase's Min==Max locks", settle, exit)
	}

	// The acceptance test's own two reads come right after the settle, then the
	// clean DLS's first evaluation. Anything else in between means the cycle
	// moved somewhere the test does not model.
	settleVal, ok := eventAt(events, settle, "settle ")
	if !ok {
		t.Fatalf("settle event has no value: %q", events[settle])
	}
	glassX, ok := eventAt(events, settle-1, "eval ") // acceptable() read the glass result
	if !ok {
		t.Fatalf("expected the glass result evaluation just before the settle, got %q", events[settle-1])
	}
	// Everything after the settle is an *evaluation state*, logged as the x the
	// mock was handed: the acceptance test's two reads (escape result, settled
	// vector) followed by the clean DLS's own starting point.
	escapeX, ok := eventAt(events, settle+1, "eval ")
	if !ok {
		t.Fatalf("expected the post-escape evaluation after the settle, got %q", events[settle+1])
	}
	settledX, ok := eventAt(events, settle+2, "eval ")
	if !ok {
		t.Fatalf("expected the settled evaluation after the settle, got %q", events[settle+2])
	}
	cleanX, ok := eventAt(events, settle+3, "eval ")
	if !ok {
		t.Fatalf("expected the clean DLS start evaluation, got %q", events[settle+3])
	}

	if settleVal == glassX {
		t.Errorf("the settle returned the raw glass result (%v); the test needs the "+
			"compensated point to be distinguishable", settleVal)
	}
	if escapeX == settledX {
		t.Errorf("the acceptance comparison read %v for both the escape result and the "+
			"settled vector; it is vacuous", escapeX)
	}
	if settledX != settleVal {
		t.Errorf("the settled vector was read at %v, want the settle's return %v", settledX, settleVal)
	}
	// The point that matters: the clean DLS must start on the settled vector.
	if cleanX != settledX {
		t.Errorf("the clean DLS started at %v, want the settled vector %v "+
			"(the glass result was %v, the escape result was %v); "+
			"the glass phase's power compensation was dropped",
			cleanX, settledX, glassX, escapeX)
	}
}
