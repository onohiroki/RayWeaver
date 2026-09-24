package escape

import (
	"bytes"
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/hiroki/rayweaver/internal/resource"
	"github.com/hiroki/rayweaver/internal/types"
)

// TestResourceGuardSelectWorker verifies the gradual-retirement policy: the
// least-productive worker is chosen (fewest recorded minima, then the worst
// best-merit, then the highest worker ID), never below the min_workers floor.
func TestResourceGuardSelectWorker(t *testing.T) {
	cycles := make([]*Cycle, 4)
	for i := range cycles {
		cycles[i] = &Cycle{workerID: i}
	}
	// w0 is productive (3 records, best 10); w1 has one record (best 50);
	// w2/w3 have one record each (best 90) -> the tie-break picks the higher ID.
	cycles[0].noteRecorded(10)
	cycles[0].noteRecorded(20)
	cycles[0].noteRecorded(30)
	cycles[1].noteRecorded(50)
	cycles[2].noteRecorded(90)
	cycles[3].noteRecorded(90)

	retires := []chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})}
	reasons := make([]string, 4)
	enabled := true
	g := newResourceGuard(&types.ResourceGuardConfig{Enabled: &enabled, MinWorkers: 1}, nil, cycles, retires, reasons)
	if g == nil {
		t.Fatal("newResourceGuard returned nil for an enabled config")
	}
	idx, ok := g.selectWorker()
	if !ok || idx != 3 {
		t.Errorf("selectWorker = %d (ok=%v), want 3 (fewest records, worst merit, highest ID)", idx, ok)
	}

	// The floor: at min_workers no selection happens.
	g.activeN = g.minWorkers
	if _, ok := g.selectWorker(); ok {
		t.Errorf("selectWorker ignored the min_workers floor (%d)", g.minWorkers)
	}

	// A retried worker is never selected again.
	g.activeN = 4
	g.active[3] = false
	if idx, ok := g.selectWorker(); !ok || idx != 2 {
		t.Errorf("selectWorker = %d (ok=%v), want 2 after worker 3 was retired", idx, ok)
	}
}

// TestResourceGuardDefaults verifies the half-of-workers floor default.
func TestResourceGuardDefaults(t *testing.T) {
	cycles := make([]*Cycle, 8)
	for i := range cycles {
		cycles[i] = &Cycle{workerID: i}
	}
	enabled := true
	g := newResourceGuard(&types.ResourceGuardConfig{Enabled: &enabled}, nil, cycles, make([]chan struct{}, 8), make([]string, 8))
	if g == nil || g.minWorkers != 4 {
		t.Fatalf("minWorkers = %v, want 4 (half of 8)", g)
	}
	// A disabled or absent section yields no guard.
	if newResourceGuard(nil, nil, cycles, nil, nil) != nil {
		t.Error("newResourceGuard returned a guard without a config")
	}
	disabled := false
	if newResourceGuard(&types.ResourceGuardConfig{Enabled: &disabled}, nil, cycles, nil, nil) != nil {
		t.Error("newResourceGuard returned a guard for enabled: false")
	}
}

// TestResourceGuardTrendChanged verifies the trend-change detector that forces
// an extra `resource` event between the regular report intervals.
func TestResourceGuardTrendChanged(t *testing.T) {
	if !trendChanged(resource.Sample{PressureLevel: 1}, resource.Sample{PressureLevel: 2}, 0, 0) {
		t.Error("a pressure-level change must be a trend change")
	}
	if !trendChanged(resource.Sample{}, resource.Sample{}, 0, 100) {
		t.Error("a large heap slope must be a trend change")
	}
	if trendChanged(resource.Sample{}, resource.Sample{}, 10, 20) {
		t.Error("a small slope change must not be a trend change")
	}
}

// TestHeapCeilingAuto verifies the backstop ceiling: a configured value wins,
// otherwise it is 30 % of the physical RAM (much looser than the original
// 2048 MB default), falling back to a fixed value when the RAM is unknown.
func TestHeapCeilingAuto(t *testing.T) {
	if got := heapCeilingMB(1234); got != 1234 {
		t.Errorf("heapCeilingMB(1234) = %v, want the configured value", got)
	}
	ram := resource.TotalRAMMB()
	auto := heapCeilingMB(0)
	if ram > 0 {
		if math.Abs(auto-0.30*ram) > 1e-6 {
			t.Errorf("heapCeilingMB(0) = %v, want 30%% of RAM (%v)", auto, 0.30*ram)
		}
		if auto <= 2048 {
			t.Errorf("auto ceiling %v is not looser than the old 2048 MB default", auto)
		}
	} else if auto != 4096 {
		t.Errorf("heapCeilingMB(0) = %v with unknown RAM, want the 4096 fallback", auto)
	}
	t.Logf("RAM=%.0fMB auto heap ceiling=%.0fMB", ram, auto)
}

// TestCycleRetireAtBoundary verifies the safe, cycle-boundary retirement: with
// the retire signal already closed the worker stops before its first cycle,
// reports worker_retired with the reason and the resource metrics, and records
// nothing (its earlier results, if any, stay in the shared store).
func TestCycleRetireAtBoundary(t *testing.T) {
	cfg := types.EscapeConfig{
		MaxCycles: 100, EscapeWorkers: 1, DistanceThreshold: 0.1,
		HInitial: 0.5, WInitial: 0.5, HMult: 2.0, WMult: 1.0,
	}
	params := BuildParams(cfg, twoWell{}.Variables())
	store := NewStore(params)
	wrapper := NewWrapper(twoWell{}, params)
	progress := NewProgress()
	var buf bytes.Buffer
	progress.AddWriter(&buf)

	retire := make(chan struct{})
	reason := "heap_ceiling"
	cycle := NewCycle(wrapper, store, params, cfg.MaxCycles, 0, progress, time.Time{}, context.Background(), nil, nil, nil, false)
	cycle.SetRetire(retire, &reason, false)
	close(retire) // request retirement before the first cycle

	cycle.Run([]float64{0.8})

	if !cycle.Retired() {
		t.Error("cycle.Retired() = false, want true after a retire request")
	}
	if cycle.Recorded() != 0 {
		t.Errorf("recorded = %d, want 0 (retired before the first cycle)", cycle.Recorded())
	}
	out := buf.String()
	if !strings.Contains(out, `"event":"worker_retired"`) {
		t.Fatalf("log has no worker_retired event:\n%s", out)
	}
	if !strings.Contains(out, `"reason":"heap_ceiling"`) {
		t.Errorf("worker_retired event carries no reason:\n%s", out)
	}
	for _, key := range []string{"heap_sys_mb", "pressure_level", "gc_count", "goroutines"} {
		if !strings.Contains(out, `"`+key+`"`) {
			t.Errorf("worker_retired event is missing the resource metric %q:\n%s", key, out)
		}
	}
}
