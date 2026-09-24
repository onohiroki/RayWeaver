package escape

import (
	"context"
	"math"
	"runtime/debug"
	"time"

	"github.com/hiroki/rayweaver/internal/resource"
	"github.com/hiroki/rayweaver/internal/types"
)

// Trend-detection tuning: a heap-slope change of at least this many MB/min in
// either direction counts as a trend change, which forces an immediate
// `resource` event even between the regular report intervals.
const trendSlopeDeltaMBPerMin = 50.0

// exitFallbackWait bounds how long the guard waits for a retired worker to
// actually exit before it falls back to the decision time as the cooldown
// origin, so a stuck worker cannot stall the guard forever.
const exitFallbackWait = 30 * time.Minute

// tightenStep is the multiplicative step of the memory-limit tightening (and
// the inverse restore step).
const tightenStep = 0.8

// resourceGuard monitors the process/system memory state during an escape run
// and relieves pressure in stages: first by tightening the Go memory limit
// (reversible, no worker lost), then by retiring the least-productive worker,
// one at a time with a cooldown measured from the previous worker's actual
// exit. Nothing here mutates the search state: a retired worker finishes its
// current cycle (recording its result), emits worker_retired and returns; the
// shared Store and the result aggregation are unaffected.
type resourceGuard struct {
	cfg        types.ResourceGuardConfig
	progress   *Progress
	cycles     []*Cycle
	retires    []chan struct{}
	reasons    []string
	active     []bool
	pending    []bool // retired but the exit has not been observed yet
	activeN    int
	minWorkers int
}

// newResourceGuard builds the guard when the config enables it (nil otherwise).
func newResourceGuard(cfg *types.ResourceGuardConfig, progress *Progress, cycles []*Cycle, retires []chan struct{}, reasons []string) *resourceGuard {
	if cfg == nil || cfg.Enabled == nil || !*cfg.Enabled {
		return nil
	}
	minW := cfg.MinWorkers
	if minW <= 0 {
		minW = len(cycles) / 2
	}
	if minW < 1 {
		minW = 1
	}
	active := make([]bool, len(cycles))
	for i := range active {
		active[i] = true
	}
	return &resourceGuard{
		cfg:        *cfg,
		progress:   progress,
		cycles:     cycles,
		retires:    retires,
		reasons:    reasons,
		active:     active,
		pending:    make([]bool, len(cycles)),
		activeN:    len(cycles),
		minWorkers: minW,
	}
}

// run samples the resource state on the configured interval, emits `resource`
// events periodically and on trend changes, and relieves pressure by tightening
// the Go memory limit first and then retiring workers. It returns when the run
// context is cancelled or the workers are done.
func (g *resourceGuard) run(ctx context.Context, done <-chan struct{}) {
	check := secondsOr(g.cfg.CheckSeconds, 60)
	report := secondsOr(g.cfg.ReportSeconds, 600)
	cooldown := secondsOr(g.cfg.RetireCooldownSeconds, 600)
	consecutive := g.cfg.Consecutive
	if consecutive <= 0 {
		consecutive = 3
	}
	ceiling := heapCeilingMB(g.cfg.HeapCeilingMB)
	pressureThreshold := g.cfg.PressureLevel
	if pressureThreshold == 0 {
		pressureThreshold = 2
	}
	pressureConsecutive := g.cfg.PressureConsecutive
	if pressureConsecutive <= 0 {
		pressureConsecutive = 2
	}
	compThreshold := g.cfg.CompressionRate

	ticker := time.NewTicker(time.Duration(check * float64(time.Second)))
	defer ticker.Stop()

	var prev resource.Sample
	var prevAt time.Time
	var prevSlope float64
	var lastReport time.Time
	var lastAction time.Time // tighten/restore cooldown origin
	var lastExitAt time.Time // last observed worker exit (retirement cooldown origin)
	var pendingSince time.Time
	breaches := 0
	breachReason := ""
	first := true

	// Memory-limit tightening state. The original limit is read once so a
	// restore can return to it.
	origLimitMB := readMemoryLimitMB()
	limitMB := origLimitMB
	tightened := false

	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
		}
		s := resource.SampleNow()
		now := time.Now()

		var compRate, slope float64
		if !prevAt.IsZero() {
			if dt := now.Sub(prevAt).Seconds(); dt > 0 {
				if s.CompactionActivity >= prev.CompactionActivity {
					compRate = float64(s.CompactionActivity-prev.CompactionActivity) / dt
				}
				slope = (s.HeapSysMB - prev.HeapSysMB) / dt * 60
			}
		}

		// Trigger conditions (OR). The OS memory-pressure level is the primary
		// signal (a healthy system must never retire a worker); the heap ceiling
		// is the high backstop for platforms without an OS pressure signal or
		// when the growth comes from this process alone.
		reason, need := "", 0
		switch {
		case pressureThreshold > 0 && s.PressureLevel >= pressureThreshold:
			reason, need = "memory_pressure", pressureConsecutive
		case s.HeapSysMB > ceiling:
			reason, need = "heap_ceiling", consecutive
		case compThreshold > 0 && compRate > compThreshold:
			reason, need = "compaction_rate", consecutive
		}
		if reason == "" {
			breaches, breachReason = 0, ""
		} else if reason != breachReason {
			breaches, breachReason = 1, reason
		} else {
			breaches++
		}

		trend := ""
		if !first && trendChanged(prev, s, prevSlope, slope) {
			trend = "trend_change"
		}
		if first || trend != "" || now.Sub(lastReport) >= time.Duration(report*float64(time.Second)) {
			g.progress.Event("resource", g.fields(s, compRate, slope, trend))
			lastReport = now
		}
		if !first {
			prevSlope = slope
		}
		first = false

		cooldownDur := time.Duration(cooldown * float64(time.Second))

		// Observe the actual exits of retired workers; the retirement cooldown
		// runs from the last observed exit, with the stuck-worker fallback to
		// the decision time.
		if at, remaining := g.observeExits(); !at.IsZero() {
			lastExitAt = at
			if !remaining {
				pendingSince = time.Time{}
			}
		}

		if reason != "" && breaches >= need {
			origin := lastExitAt
			originKind := "exit"
			if !pendingSince.IsZero() && now.Sub(pendingSince) > exitFallbackWait {
				origin = pendingSince
				originKind = "decision_fallback"
			}
			if origin.IsZero() || now.Sub(origin) >= cooldownDur {
				// Stage 1: tighten the Go memory limit (reversible, no worker
				// lost) before retiring anyone.
				if target, ok := tightenTarget(limitMB, s.HeapSysMB, s.HeapAllocMB); ok {
					before := limitMB
					limitMB = target
					tightened = true
					debug.SetMemoryLimit(mbToBytes(target))
					g.progress.Event("memory_limit", map[string]any{
						"kind":          "tighten",
						"reason":        reason,
						"before_mb":     before,
						"after_mb":      target,
						"live_alloc_mb": s.HeapAllocMB,
						"floor_mb":      tightenFloorMB(s.HeapAllocMB),
						"metrics":       resourceMetrics(),
					})
					breaches = 0
					lastAction = now
				} else if idx, ok := g.selectWorker(); ok {
					// Stage 2: retire the least-productive worker.
					before := g.activeN
					g.reasons[idx] = reason
					close(g.retires[idx])
					g.active[idx] = false
					g.pending[idx] = true
					g.activeN--
					g.progress.Event("worker_retire", map[string]any{
						"worker":              idx,
						"reason":              reason,
						"cycle":               g.cycles[idx].CycleCount(),
						"workers_before":      before,
						"workers_after":       g.activeN,
						"min_workers":         g.minWorkers,
						"cooldown_seconds":    cooldown,
						"cooldown_origin":     originKind,
						"selection_recorded":  g.cycles[idx].Recorded(),
						"selection_best":      g.cycles[idx].BestMerit(),
						"trigger_heap_sys_mb": s.HeapSysMB,
						"trigger_ceiling_mb":  ceiling,
						"trigger_pressure":    s.PressureLevel,
						"trigger_comp_rate":   compRate,
						"trigger_consecutive": breaches,
						"metrics":             resourceMetrics(),
					})
					pendingSince = now
					breaches = 0
				}
			}
		} else if reason == "" && tightened && (lastAction.IsZero() || now.Sub(lastAction) >= cooldownDur) {
			// Pressure eased: restore one step toward the original limit.
			before := limitMB
			target := limitMB / tightenStep
			if origLimitMB <= 0 || target >= origLimitMB {
				target = origLimitMB
				tightened = false
			}
			limitMB = target
			debug.SetMemoryLimit(mbToBytes(target))
			g.progress.Event("memory_limit", map[string]any{
				"kind":      "restore",
				"before_mb": before,
				"after_mb":  target,
				"metrics":   resourceMetrics(),
			})
			lastAction = now
		}

		prev = s
		prevAt = now
	}
}

// observeExits clears the pending flags of workers whose retirement has been
// observed. It returns the time of an observed exit (zero when none happened
// this tick) and whether any pending retirement remains.
func (g *resourceGuard) observeExits() (time.Time, bool) {
	var at time.Time
	remaining := false
	for i, p := range g.pending {
		if !p {
			continue
		}
		if g.cycles[i].Retired() {
			g.pending[i] = false
			at = time.Now()
		} else {
			remaining = true
		}
	}
	return at, remaining
}

// selectWorker returns the least-productive still-active worker: fewest
// recorded minima, then the worst best-merit, then the highest worker ID
// (deterministic). It refuses to go below the min_workers floor.
func (g *resourceGuard) selectWorker() (int, bool) {
	if g.activeN <= g.minWorkers {
		return 0, false
	}
	best := -1
	for i := range g.cycles {
		if !g.active[i] {
			continue
		}
		if best < 0 {
			best = i
			continue
		}
		ri, rb := g.cycles[i].Recorded(), g.cycles[best].Recorded()
		if ri != rb {
			if ri < rb {
				best = i
			}
			continue
		}
		mi, mb := g.cycles[i].BestMerit(), g.cycles[best].BestMerit()
		if mi != mb {
			if mi > mb { // worse merit -> retire this one
				best = i
			}
			continue
		}
		if i > best {
			best = i
		}
	}
	return best, best >= 0
}

// fields builds the `resource` event payload.
func (g *resourceGuard) fields(s resource.Sample, compRate, slope float64, note string) map[string]any {
	f := map[string]any{
		"cycle":                 g.maxCycle(),
		"workers":               g.activeN,
		"heap_alloc_mb":         s.HeapAllocMB,
		"heap_sys_mb":           s.HeapSysMB,
		"heap_inuse_mb":         s.HeapInuseMB,
		"goroutines":            s.Goroutines,
		"gc_count":              s.GCCount,
		"gc_pause_ms":           s.GCPauseMS,
		"cpu_sec":               s.CPUSeconds,
		"heap_slope_mb_per_min": slope,
		"pressure_level":        s.PressureLevel,
		"memory_level":          s.MemoryLevel,
		"compaction_rate_per_s": compRate,
	}
	if note != "" {
		f["note"] = note
	}
	return f
}

// maxCycle reports the highest cycle index any worker has started.
func (g *resourceGuard) maxCycle() int {
	max := 0
	for _, c := range g.cycles {
		if n := c.CycleCount(); n > max {
			max = n
		}
	}
	return max
}

// trendChanged reports whether the resource state broke trend between two
// samples: the OS pressure level changed, or the heap slope moved materially.
func trendChanged(prev, cur resource.Sample, prevSlope, slope float64) bool {
	if prev.PressureLevel != 0 && cur.PressureLevel != 0 && cur.PressureLevel != prev.PressureLevel {
		return true
	}
	if math.Abs(slope) > trendSlopeDeltaMBPerMin && math.Abs(slope-prevSlope) > trendSlopeDeltaMBPerMin {
		return true
	}
	return false
}

// secondsOr returns v when positive, else the default.
func secondsOr(v, def float64) float64 {
	if v > 0 {
		return v
	}
	return def
}

// heapCeilingMB resolves the heap-ceiling trigger: the configured value, else
// 30 % of the physical RAM (falling back to 4096 MB when the RAM is unknown).
// The ceiling is a backstop behind the OS memory-pressure signal, so it sits
// high enough not to retire workers while the system is healthy.
func heapCeilingMB(configured float64) float64 {
	if configured > 0 {
		return configured
	}
	if ram := resource.TotalRAMMB(); ram > 0 {
		return 0.30 * ram
	}
	return 4096
}

// tightenFloorMB is the lowest memory limit the guard will set: 1.5× the live
// heap, so the GC cannot be pushed into a thrashing limit below the live set.
func tightenFloorMB(liveAllocMB float64) float64 {
	floor := 1.5 * liveAllocMB
	if floor < 64 {
		floor = 64
	}
	return floor
}

// tightenTarget proposes the next memory-limit value: tightenStep of the
// current limit (or of the current heap when the limit is unlimited), never
// below tightenFloorMB. ok is false when there is no room left to tighten, in
// which case the guard falls through to retiring a worker.
func tightenTarget(curLimitMB, heapSysMB, liveAllocMB float64) (float64, bool) {
	floor := tightenFloorMB(liveAllocMB)
	base := curLimitMB
	if base <= 0 {
		base = heapSysMB // first tightening from unlimited: step down from the heap
	}
	target := base * tightenStep
	if target < floor {
		target = floor
	}
	if curLimitMB > 0 && target >= curLimitMB {
		return 0, false
	}
	if target <= 0 {
		return 0, false
	}
	return target, true
}

// readMemoryLimitMB returns the current Go memory limit in MB (0 = unlimited)
// without changing it.
func readMemoryLimitMB() float64 {
	prev := debug.SetMemoryLimit(-1)
	debug.SetMemoryLimit(prev)
	if prev <= 0 {
		return 0
	}
	return bytesToMB(prev)
}

// mbToBytes converts a limit in MB to the debug.SetMemoryLimit argument
// (a value <= 0 means unlimited).
func mbToBytes(mb float64) int64 {
	if mb <= 0 {
		return -1
	}
	return int64(mb * (1 << 20))
}

// bytesToMB converts bytes to MB.
func bytesToMB(b int64) float64 { return float64(b) / (1 << 20) }
