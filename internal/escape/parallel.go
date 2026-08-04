package escape

import (
	"context"
	"sync"
	"time"

	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/types"
)

// Result is the outcome of a parallel escape search: every discovered local
// minimum ordered by merit, plus the parameters used.
type Result struct {
	Params      Params
	Minima      []Point
	MinimaIdx   []int // discovery-order store index of each minimum in Minima
	BestIdx     int
	BestMerit   float64
	Escapes     int
	Workers     int
	Cycles      int
	MaxSeconds  float64
	TimedOut    bool
	Interrupted bool
}

// RunOptions configures a parallel escape search.
type RunOptions struct {
	// Progress is the JSONL reporter (nil disables verbose reporting).
	Progress *Progress
	// OnRecord is called when a minimum is recorded or improved (nil disables).
	OnRecord RecordHandler
	// Context cancels the search: workers stop at the next cycle boundary
	// once the current DLS run finishes (nil = run to completion).
	Context context.Context
}

// BuildParams derives the escape parameters from the YAML config and the
// model's variable definitions. Variables with Min == Max are excluded from
// the escape distance; per-variable weights come from VariableWeights keyed
// by variable parameter name (curvature, thickness, nd, vd, ...).
func BuildParams(cfg types.EscapeConfig, variables []dls.VariableInfo) Params {
	p := DefaultParams()
	if cfg.HInitial != 0 {
		p.H = cfg.HInitial
	}
	if cfg.WInitial != 0 {
		p.W = cfg.WInitial
	}
	if cfg.HMult != 0 {
		p.HMult = cfg.HMult
	}
	if cfg.WMult != 0 {
		p.WMult = cfg.WMult
	}
	if cfg.DistanceThreshold != 0 {
		p.Dt = cfg.DistanceThreshold
	}
	for _, m := range []struct {
		src float64
		dst *float64
	}{
		{cfg.EscapeIterFrac, &p.EscapeIterFrac},
		{cfg.WSpan, &p.WSpan},
		{cfg.StallWindowFrac, &p.StallWindowFrac},
		{cfg.StallRelTol, &p.StallRelTol},
		{cfg.InitialPerturb, &p.InitialPerturb},
	} {
		if m.src > 0 {
			*m.dst = m.src
		}
	}
	if cfg.StallEarlyStop != nil {
		p.StallEarlyStop = *cfg.StallEarlyStop
	}

	p.Weights = make([]float64, len(variables))
	p.Scales = make([]float64, len(variables))
	for i, v := range variables {
		p.Scales[i] = v.Max - v.Min
		if p.Scales[i] <= 0 {
			p.Scales[i] = 1.0
		}
		if v.Min != v.Max {
			p.Active = append(p.Active, i)
		}
		if cfg.VariableWeights != nil {
			p.Weights[i] = cfg.VariableWeights[v.Param]
		}
		if p.Weights[i] == 0 {
			p.Weights[i] = 1.0
		}
	}
	return p
}

// ParallelEscape runs the escape loop across escapeWorkers goroutines, each with
// its own freshly-built model (via the factory) so the shared catalog and
// surface state stay race-free. All workers share one Store. opts.Progress may
// be nil to disable verbose reporting; opts.Context cancels the search.
func ParallelEscape(newModel func() dls.Model, cfg types.EscapeConfig, opts RunOptions) Result {
	params := BuildParams(cfg, newModel().Variables())

	numWorkers := cfg.EscapeWorkers
	if numWorkers <= 0 {
		numWorkers = 4
	}
	maxCycles := cfg.MaxCycles
	if maxCycles <= 0 {
		maxCycles = 10
	}

	// Soft wall-clock budget shared by all workers. A zero value disables the
	// limit; expiry is checked between DLS runs, so a running solve always
	// finishes (overshoot is bounded by one DLS run).
	var deadline time.Time
	if cfg.MaxSeconds > 0 {
		deadline = time.Now().Add(time.Duration(cfg.MaxSeconds * float64(time.Second)))
	}

	progress := opts.Progress
	progress.Event("params", map[string]any{
		"h":                  params.H,
		"w":                  params.W,
		"h_mult":             params.HMult,
		"w_mult":             params.WMult,
		"distance_threshold": params.Dt,
	})
	start := map[string]any{"workers": numWorkers, "max_cycles": maxCycles}
	if !deadline.IsZero() {
		start["max_seconds"] = cfg.MaxSeconds
	}
	progress.Event("start", start)

	store := NewStore(params)
	store.SetOnRecord(opts.OnRecord)
	var wg sync.WaitGroup
	totalEscapes := 0
	timedOut := false
	var escMu sync.Mutex

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			inner := newModel()
			workerParams := params
			wSpan := workerParams.WSpan
			if wSpan <= 0 {
				wSpan = 2.0
			}
			if numWorkers > 1 {
				workerParams.W = workerParams.W * (1 + float64(seed)/float64(numWorkers-1)*(wSpan-1))
			}
			wrapper := NewWrapper(inner, workerParams)
			cycle := NewCycle(wrapper, store, workerParams, maxCycles, seed, progress, deadline, opts.Context)

			x0 := inner.InitialState()
			if seed != 0 {
				x0 = cycle.perturb(x0, int(seed), workerParams.InitialPerturb)
			}
			cycle.Run(x0)
			progress.Event("worker_done", map[string]any{
				"worker":      int(seed),
				"escaped":     cycle.Escaped(),
				"recorded":    cycle.Recorded(),
				"interrupted": cycle.Interrupted(),
				"timed_out":   cycle.StoppedByTime(),
			})

			escMu.Lock()
			totalEscapes += cycle.Escaped()
			if cycle.StoppedByTime() {
				timedOut = true
			}
			escMu.Unlock()
		}(int64(i))
	}
	wg.Wait()

	points, idxs := store.SortedByMerit()
	res := Result{
		Params:      params,
		Minima:      points,
		MinimaIdx:   idxs,
		Escapes:     totalEscapes,
		Workers:     numWorkers,
		Cycles:      maxCycles,
		MaxSeconds:  cfg.MaxSeconds,
		TimedOut:    timedOut,
		Interrupted: opts.Context != nil && opts.Context.Err() != nil,
		BestIdx:     -1,
	}
	if len(points) > 0 {
		res.BestIdx = 0
		res.BestMerit = points[0].Merit
	}
	return res
}
