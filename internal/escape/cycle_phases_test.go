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

// The escape cycle has two independently switchable phases — the escape-function
// bumps of the exploration phase and the clean DLS that closes the cycle. These
// tests pin (a) that both switches reach Params from YAML independently, (b)
// that the params block reports only what is disabled, and (c) the two run-time
// effects: no bump is ever handed to the exploration phase, and no clean solve
// ever runs.

func TestBuildParamsCyclePhaseSwitches(t *testing.T) {
	vars := twoWell{}.Variables()

	p := BuildParams(types.EscapeConfig{}, vars)
	if !p.EscapeBumps || !p.CleanDLS {
		t.Fatalf("defaults: EscapeBumps=%v CleanDLS=%v, want true/true", p.EscapeBumps, p.CleanDLS)
	}

	off := false
	p = BuildParams(types.EscapeConfig{EscapeBumps: &off}, vars)
	if p.EscapeBumps {
		t.Error("escape_bumps: false not applied")
	}
	if !p.CleanDLS {
		t.Error("clean_dls must stay on when only escape_bumps is set")
	}

	p = BuildParams(types.EscapeConfig{CleanDLS: &off}, vars)
	if p.CleanDLS {
		t.Error("clean_dls: false not applied")
	}
	if !p.EscapeBumps {
		t.Error("escape_bumps must stay on when only clean_dls is set")
	}

	on := true
	p = BuildParams(types.EscapeConfig{EscapeBumps: &on, CleanDLS: &on}, vars)
	if !p.EscapeBumps || !p.CleanDLS {
		t.Errorf("explicit true: EscapeBumps=%v CleanDLS=%v, want true/true", p.EscapeBumps, p.CleanDLS)
	}
}

// TestParamsInfoReportsOnlyDisabledPhases pins the shape of
// escape_result.params: both switches are default-on, so a default run must
// not grow two new keys, and a disabled one must appear as an explicit false
// (distinguishable from "unset").
func TestParamsInfoReportsOnlyDisabledPhases(t *testing.T) {
	p := testParams() // BuildParams over DefaultParams: both phases on
	info := ParamsInfo(p, 0, 1, 1)
	if info.EscapeBumps != nil || info.CleanDLS != nil {
		t.Fatalf("default params block = escape_bumps:%v clean_dls:%v, want both omitted",
			info.EscapeBumps, info.CleanDLS)
	}

	p.EscapeBumps = false
	info = ParamsInfo(p, 0, 1, 1)
	if info.EscapeBumps == nil || *info.EscapeBumps {
		t.Errorf("escape_bumps = %v, want *false", info.EscapeBumps)
	}
	if info.CleanDLS != nil {
		t.Errorf("clean_dls = %v, want nil (still on)", info.CleanDLS)
	}

	p.EscapeBumps = true
	p.CleanDLS = false
	info = ParamsInfo(p, 0, 1, 1)
	if info.CleanDLS == nil || *info.CleanDLS {
		t.Errorf("clean_dls = %v, want *false", info.CleanDLS)
	}
	if info.EscapeBumps != nil {
		t.Errorf("escape_bumps = %v, want nil (still on)", info.EscapeBumps)
	}
}

// bumpProbe is an Explorer that optimises nothing: it only records the escape
// function the exploration phase was handed and returns the start point
// unchanged (the clean DLS below is what converges it, so the store still
// receives a minimum). Cycle.Run installs the escapes immediately before
// calling Explore, so EscapeMerit measured here is exactly the bump set the
// phase will push against — a direct read of whether escape_bumps was obeyed.
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

// runBumpProbe runs a whole Cycle.Run with the probe explorer installed and
// returns what the probe saw plus the store it filled.
func runBumpProbe(t *testing.T, bumps bool) (*bumpProbe, *Store) {
	t.Helper()
	cfg := types.EscapeConfig{
		MaxCycles:         3,
		EscapeWorkers:     1,
		DistanceThreshold: 0.1,
		HInitial:          0.5,
		WInitial:          0.5,
		HMult:             2.0,
		WMult:             1.0,
	}
	if !bumps {
		off := false
		cfg.EscapeBumps = &off
	}
	params := BuildParams(cfg, twoWell{}.Variables())
	store := NewStore(params)
	wrapper := NewWrapper(twoWell{}, params)
	probe := &bumpProbe{store: store}
	cycle := NewCycle(wrapper, store, params, cfg.MaxCycles, 0, nil, time.Time{},
		context.Background(), nil, nil, probe, false)

	cycle.Run([]float64{0.8})
	return probe, store
}

func TestCycleEscapeBumpsDisabled(t *testing.T) {
	// Control: with the bumps on, the second exploration phase must sit on the
	// bump raised over the minimum the first cycle recorded.
	probe, store := runBumpProbe(t, true)
	if probe.badModel {
		t.Fatal("explorer did not receive the *Wrapper")
	}
	if store.Len() < 1 {
		t.Fatalf("store.Len() = %d, want >= 1", store.Len())
	}
	if len(probe.atMinima) == 0 {
		t.Fatal("probe never saw a recorded minimum (need >= 2 cycles)")
	}
	for i, v := range probe.atMinima {
		if v <= 0 {
			t.Fatalf("bumps on: EscapeMerit at recorded minimum %d = %v, want > 0", i, v)
		}
	}

	// With escape_bumps false the store keeps growing (the exploration point is
	// still classified and recorded) but no bump is ever handed to the phase.
	probe, store = runBumpProbe(t, false)
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
			t.Fatalf("bumps off: EscapeMerit at exploration start %d = %v, want 0", i, v)
		}
	}
	for i, v := range probe.atMinima {
		if v != 0 {
			t.Fatalf("bumps off: EscapeMerit at recorded minimum %d = %v, want 0", i, v)
		}
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
		MaxCycles:         1,
		EscapeWorkers:     1,
		DistanceThreshold: 0.1,
		HInitial:          0.5,
		WInitial:          0.5,
		HMult:             2.0,
		WMult:             1.0,
	}
	if !clean {
		off := false
		cfg.CleanDLS = &off
	}
	inner := &phaseShiftMock{}
	params := BuildParams(cfg, inner.Variables())
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
