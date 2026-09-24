package escape

import (
	"context"
	"math"
	"time"

	"github.com/hiroki/rayweaver/internal/resource"
	"github.com/hiroki/rayweaver/internal/types"
)

// Trend-detection tuning: a heap-slope change of at least this many MB/min in
// either direction counts as a trend change, which forces an immediate
// `resource` event even between the regular report intervals.
const trendSlopeDeltaMBPerMin = 50.0

// resourceGuard monitors the process/system memory state during an escape run
// and retires the least-productive worker, one at a time with a cooldown, when
// pressure builds up. It is the safety net for the cases where the Go heap is
// not bounded by GOMEMLIMIT or the pressure comes from other processes.
//
// Nothing here mutates the search state: a retired worker finishes its current
// cycle (recording its result), emits worker_retired and returns; the shared
// Store and the result aggregation are unaffected.
type resourceGuard struct {
	cfg        types.ResourceGuardConfig
	progress   *Progress
	cycles     []*Cycle
	retires    []chan struct{}
	reasons    []string
	active     []bool
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
		activeN:    len(cycles),
		minWorkers: minW,
	}
}

// run samples the resource state on the configured interval, emits `resource`
// events periodically and on trend changes, and retires workers gradually when
// the guard triggers. It returns when the run context is cancelled.
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
	var lastRetire time.Time
	breaches := 0
	breachReason := ""
	first := true

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

		if reason != "" && breaches >= need &&
			(lastRetire.IsZero() || now.Sub(lastRetire) >= time.Duration(cooldown*float64(time.Second))) {
			if idx, ok := g.selectWorker(); ok {
				before := g.activeN
				g.reasons[idx] = reason
				close(g.retires[idx])
				g.active[idx] = false
				g.activeN--
				g.progress.Event("worker_retire", map[string]any{
					"worker":              idx,
					"reason":              reason,
					"cycle":               g.cycles[idx].CycleCount(),
					"workers_before":      before,
					"workers_after":       g.activeN,
					"min_workers":         g.minWorkers,
					"cooldown_seconds":    cooldown,
					"selection_recorded":  g.cycles[idx].Recorded(),
					"selection_best":      g.cycles[idx].BestMerit(),
					"trigger_heap_sys_mb": s.HeapSysMB,
					"trigger_ceiling_mb":  ceiling,
					"trigger_pressure":    s.PressureLevel,
					"trigger_comp_rate":   compRate,
					"trigger_consecutive": breaches,
					"metrics":             resourceMetrics(),
				})
				lastRetire = now
				breaches = 0
			}
		}

		prev = s
		prevAt = now
	}
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
