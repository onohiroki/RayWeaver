package escape

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/types"
)

// The global cycle has one switchable closing phase — the clean DLS that ends
// every cycle — plus the derived UsesBumps flag that says whether the run
// carries escape geometry at all (only the `escape` section supplies it; a run
// without it, i.e. PSO, never does). These tests pin (a) that the switch
// reaches Params from YAML, (b) that the params block reports only what is
// disabled, and (c) that no bump is ever handed to an explorer.

func TestBuildParamsCyclePhaseSwitch(t *testing.T) {
	vars := twoWell{}.Variables()

	p := buildParamsOf(types.EscapeConfig{}, vars)
	if !p.CleanDLS {
		t.Fatalf("defaults: CleanDLS=%v, want true", p.CleanDLS)
	}
	if !p.UsesBumps {
		t.Error("a run with an escape section must report UsesBumps=true")
	}

	off := false
	p = buildParamsOf(types.EscapeConfig{GlobalConfig: types.GlobalConfig{CleanDLS: &off}}, vars)
	if p.CleanDLS {
		t.Error("clean_dls: false not applied")
	}
	if !p.UsesBumps {
		t.Error("UsesBumps must stay on when only clean_dls is set")
	}

	// A run that supplies no tuning (PSO) places no bumps at all.
	p = BuildParams(types.GlobalConfig{}, nil, vars)
	if p.UsesBumps {
		t.Error("a run without tuning must not report bumps")
	}
	if !p.CleanDLS {
		t.Error("clean_dls must stay on for a run without bumps")
	}
}

// TestParamsInfoReportsOnlyDisabledPhases pins the shape of
// global_result.params: the clean DLS is default-on, so a default run must not
// grow a new key, and a disabled one must appear as an explicit false
// (distinguishable from "unset"). The escape bump geometry is reported only
// for a run that carries it.
func TestParamsInfoReportsOnlyDisabledPhases(t *testing.T) {
	p := testParams() // buildParamsOf over DefaultParams: phase on, bumps in use
	info := ParamsInfo(p, 0, 1, 1)
	if info.CleanDLS != nil {
		t.Fatalf("default params block = clean_dls:%v, want omitted", info.CleanDLS)
	}
	if info.HInitial == 0 || info.WSpan == 0 {
		t.Fatalf("params block = h_initial:%v w_span:%v, want the escape geometry reported",
			info.HInitial, info.WSpan)
	}

	p.CleanDLS = false
	info = ParamsInfo(p, 0, 1, 1)
	if info.CleanDLS == nil || *info.CleanDLS {
		t.Errorf("clean_dls = %v, want *false", info.CleanDLS)
	}

	// A run without bumps reports none of the escape geometry, so a PSO
	// params block never mentions h_initial / w_initial / escape_iter_frac.
	p = BuildParams(types.GlobalConfig{}, nil, nil)
	info = ParamsInfo(p, 0, 1, 1)
	if info.HInitial != 0 || info.WInitial != 0 || info.HMult != 0 || info.WMult != 0 ||
		info.EscapeIterFrac != 0 || info.WSpan != 0 {
		t.Errorf("no-bump params block = %+v, want no escape geometry", info)
	}
}

// bumpProbe is an Explorer that optimises nothing: it only records the escape
// function the exploration phase was handed and returns the start point
// unchanged (the clean DLS below is what converges it, so the store still
// receives a minimum). Cycle.Run installs the escapes immediately before
// calling Explore, so EscapeMerit measured here is exactly the bump set the
// phase will push against — a direct read of whether it was handed any.
type bumpProbe struct {
	store *Store

	badModel bool
	atStart  []float64 // EscapeMerit at the exploration start point
	atMinima []float64 // EscapeMerit at each already-recorded minimum
}

func (b *bumpProbe) Explore(model dls.Model, x0 []float64) dls.Result {
	w, ok := model.(*Wrapper)
	if !ok {
		b.badModel = true
		return dls.Result{Status: dls.StatusInterrupted}
	}
	b.atStart = append(b.atStart, w.EscapeMerit(x0))
	for _, p := range b.store.All() {
		b.atMinima = append(b.atMinima, w.EscapeMerit(p.X))
	}
	out := append([]float64(nil), x0...)
	merit := w.InnerMerit(out)
	return dls.Result{
		BeforeMerit: merit,
		AfterMerit:  merit,
		Iterations:  1,
		Status:      "converged",
		Variables:   []dls.VariableState{{Name: "x", Param: "x", After: out[0]}},
	}
}

// runBumpProbe runs a whole Cycle.Run with the probe explorer installed — the
// shape of a PSO run — and returns what the probe saw plus the store it filled.
func runBumpProbe(t *testing.T) (*bumpProbe, *Store) {
	t.Helper()
	params := buildParamsOf(rescueTestConfig(3), twoWell{}.Variables())
	store := NewStore(params)
	wrapper := NewWrapper(twoWell{}, params)
	probe := &bumpProbe{store: store}
	cycle := NewCycle(wrapper, store, params, 3, 0, nil, time.Time{},
		context.Background(), nil, nil, probe, false)

	cycle.Run([]float64{0.8})
	return probe, store
}

// TestCycleExplorerSeesNoEscapes pins the split between the two exploration
// paths: a run driven by an Explorer (PSO) is handed no escape points at all,
// even though the section it ran from carries bump geometry, while the store
// keeps growing and drives the new/repeat classification and the output.
func TestCycleExplorerSeesNoEscapes(t *testing.T) {
	probe, store := runBumpProbe(t)
	if probe.badModel {
		t.Fatal("explorer did not receive the *Wrapper")
	}
	if store.Len() < 1 {
		t.Fatalf("store.Len() = %d, want >= 1 (recording must keep working)", store.Len())
	}
	if len(probe.atStart) != 3 {
		t.Fatalf("explored %d phases, want 3", len(probe.atStart))
	}
	if len(probe.atMinima) == 0 {
		t.Fatal("probe never saw a recorded minimum (need >= 2 cycles)")
	}
	for i, v := range probe.atStart {
		if v != 0 {
			t.Fatalf("EscapeMerit at exploration start %d = %v, want 0", i, v)
		}
	}
	for i, v := range probe.atMinima {
		if v != 0 {
			t.Fatalf("EscapeMerit at recorded minimum %d = %v, want 0", i, v)
		}
	}
}

// TestCycleEscapesFor pins the other half of the split: without an explorer the
// exploration phase is handed every point the store recorded, which is what
// pushes the DLS out of a known basin.
func TestCycleEscapesFor(t *testing.T) {
	params := buildParamsOf(rescueTestConfig(1), twoWell{}.Variables())
	store := NewStore(params)
	w := NewWrapper(twoWell{}, params)
	newCycle := func(explorer Explorer) *Cycle {
		return NewCycle(w, store, params, 1, 0, nil, time.Time{},
			context.Background(), nil, nil, explorer, false)
	}

	dlsCycle := newCycle(nil)
	probe := &bumpProbe{store: store}
	explorerCycle := newCycle(probe)

	if got := dlsCycle.escapesFor(); len(got) != 0 {
		t.Fatalf("empty store: escapesFor() = %d points, want 0", len(got))
	}
	store.Add(Point{X: []float64{0.5}, Merit: 1, Status: MinStatusFeasibleLocalMinimum})
	if got := dlsCycle.escapesFor(); len(got) != 1 {
		t.Fatalf("DLS path: escapesFor() = %d points, want 1", len(got))
	}
	if got := explorerCycle.escapesFor(); got != nil {
		t.Fatalf("explorer path: escapesFor() = %d points, want none", len(got))
	}
}

// phaseShiftMock's minimum moves with the escape-cycle phase: Wrapper.SetPhase
// forwards PhaseEscape=0 / PhaseClean=1 through SetEscapePhase, so the mock
// converges to 0.7 during exploration and to 0.2 during the clean solve. The
// recorded x therefore discriminates "the clean DLS ran" (≈0.2) from "the cycle
// recorded the exploration result as-is" (≈0.7).
type phaseShiftMock struct {
	phase float64
}

func (m *phaseShiftMock) target() float64 {
	if m.phase >= 0.5 {
		return 0.2
	}
	return 0.7
}

func (m *phaseShiftMock) Variables() []dls.VariableInfo {
	return []dls.VariableInfo{{Name: "x", Param: "x", Min: 0, Max: 1}}
}

func (m *phaseShiftMock) InitialState() []float64 { return []float64{0.9} }

func (m *phaseShiftMock) Options() dls.Options {
	return dls.Options{MaxIter: 200, Mu: 0.1, Tol: 1e-12, Epsilon: 1e-14}
}

func (m *phaseShiftMock) SetEscapePhase(v float64) { m.phase = v }

func (m *phaseShiftMock) EvaluateMerit(x []float64) float64 {
	d := x[0] - m.target()
	return d*d + 10
}

func (m *phaseShiftMock) ComputeResiduals(x []float64) []float64 {
	return []float64{x[0] - m.target(), math.Sqrt(10)}
}

func (m *phaseShiftMock) ComputeConstraints(x []float64) []float64 { return nil }

// runPhaseShift runs one full cycle and returns the recorded x together with
// the "clean_dls" cycle events the run emitted.
func runPhaseShift(t *testing.T, clean bool) (x float64, accepted, skipped int) {
	t.Helper()
	cfg := types.EscapeConfig{
		GlobalConfig: types.GlobalConfig{
			MaxCycles:         1,
			GlobalWorkers:     1,
			DistanceThreshold: 0.1,
		},
		HInitial: 0.5,
		WInitial: 0.5,
		HMult:    2.0,
		WMult:    1.0,
	}
	if !clean {
		off := false
		cfg.CleanDLS = &off
	}
	inner := &phaseShiftMock{}
	params := buildParamsOf(cfg, inner.Variables())
	store := NewStore(params)
	wrapper := NewWrapper(inner, params)

	var buf bytes.Buffer
	prog := NewProgress()
	prog.AddWriter(&buf)

	cycle := NewCycle(wrapper, store, params, cfg.MaxCycles, 0, prog, time.Time{},
		context.Background(), nil, nil, nil, false)
	cycle.Run([]float64{0.9})

	if store.Len() != 1 {
		t.Fatalf("store.Len() = %d, want 1", store.Len())
	}
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var ev struct {
			Event  string `json:"event"`
			Phase  string `json:"phase"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("bad JSONL line %q: %v", line, err)
		}
		if ev.Event != "cycle" || ev.Phase != "clean_dls" {
			continue
		}
		switch ev.Status {
		case "accepted":
			accepted++
		case "skipped":
			skipped++
		}
	}
	return store.All()[0].X[0], accepted, skipped
}

// TestCycleCleanDLSDisabled pins the second switch: with clean_dls false the
// cycle records the exploration result instead of the gradient-refined point,
// and reports the clean phase as skipped rather than solving it.
func TestCycleCleanDLSDisabled(t *testing.T) {
	x, accepted, skipped := runPhaseShift(t, true)
	if math.Abs(x-0.2) > 1e-6 {
		t.Errorf("clean_dls on: recorded x = %v, want 0.2 (clean DLS result)", x)
	}
	if accepted == 0 {
		t.Error("clean_dls on: no accepted clean_dls event")
	}
	if skipped != 0 {
		t.Errorf("clean_dls on: %d skipped clean_dls events, want 0", skipped)
	}

	x, accepted, skipped = runPhaseShift(t, false)
	if math.Abs(x-0.7) > 1e-6 {
		t.Errorf("clean_dls off: recorded x = %v, want 0.7 (exploration result)", x)
	}
	if skipped == 0 {
		t.Error("clean_dls off: no skipped clean_dls event")
	}
	if accepted != 0 {
		t.Errorf("clean_dls off: %d accepted clean_dls events, want 0", accepted)
	}
}
