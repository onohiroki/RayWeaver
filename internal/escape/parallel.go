package escape

import (
	"sync"

	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/types"
)

// Result is the outcome of a parallel escape search: every discovered local
// minimum ordered by merit, plus the parameters used.
type Result struct {
	Params    Params
	Minima    []Point
	BestIdx   int
	BestMerit float64
	Escapes   int
	Workers   int
	Cycles    int
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

// ParallelEscape runs the escape loop across numWorkers goroutines, each with
// its own freshly-built model (via the factory) so the shared catalog and
// surface state stay race-free. All workers share one Store.
func ParallelEscape(newModel func() dls.Model, cfg types.EscapeConfig) Result {
	params := BuildParams(cfg, newModel().Variables())

	numWorkers := cfg.NumWorkers
	if numWorkers <= 0 {
		numWorkers = 4
	}
	maxCycles := cfg.MaxCycles
	if maxCycles <= 0 {
		maxCycles = 10
	}

	store := NewStore(params)
	var wg sync.WaitGroup
	totalEscapes := 0
	var escMu sync.Mutex

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			inner := newModel()
			wrapper := NewWrapper(inner, params)
			cycle := NewCycle(wrapper, store, params, maxCycles, seed)

			x0 := inner.InitialState()
			if seed != 0 {
				x0 = cycle.perturb(x0, int(seed), initialPerturb)
			}
			cycle.Run(x0)

			escMu.Lock()
			totalEscapes += cycle.Escaped()
			escMu.Unlock()
		}(int64(i))
	}
	wg.Wait()

	points, _ := store.SortedByMerit()
	res := Result{
		Params:  params,
		Minima:  points,
		Escapes: totalEscapes,
		Workers: numWorkers,
		Cycles:  maxCycles,
		BestIdx: -1,
	}
	if len(points) > 0 {
		res.BestIdx = 0
		res.BestMerit = points[0].Merit
	}
	return res
}
