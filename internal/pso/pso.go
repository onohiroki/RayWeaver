// Package pso implements a Particle Swarm Optimizer for lens design global
// optimization. It satisfies the escape.Explorer interface, replacing the
// DLS escape phase with a gradient-free population search.
package pso

import (
	"math"
	"math/rand"
	"runtime"
	"sort"
	"sync"

	"github.com/hiroki/rayweaver/internal/dls"
)

// particleResult holds the per-particle evaluation output.
type particleResult struct {
	f  float64 // escaped merit (fitness)
	tf float64 // true merit (for gbest selection)
}

// evalSwarm evaluates all particles in parallel using GOMAXPROCS goroutines.
// The model's EvaluateMerit is goroutine-safe (applyVariables deep-copies surfaces
// and creates a local cache each call).
//
// When bothFn is non-nil, it is used instead of fitness+innerMeritFn: the
// combined function returns (escapedMerit, innerMerit) in a single ray-trace
// pass, avoiding the 2× EvaluateMerit cost.
//
// When skipConstraints is true, ComputeConstraints is skipped entirely (the
// inner model has no active constraints, so the call would be a no-op that
// still pays for applyVariables + sizeAutoApertures with a nil cache).
func (e *Explorer) evalSwarm(model dls.Model, pos [][]float64, variables []dls.VariableInfo, scales []float64, innerMeritFn func([]float64) float64, bothFn func([]float64) (float64, float64), skipConstraints bool) []particleResult {
	swarmSize := len(pos)
	results := make([]particleResult, swarmSize)

	nw := runtime.GOMAXPROCS(0)
	if nw < 1 {
		nw = 1
	}

	sem := make(chan struct{}, nw)
	var wg sync.WaitGroup

	for i := 0; i < swarmSize; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			xPhys := denormalize(pos[i], variables, scales)
			if bothFn != nil {
				// ① Single ray-trace pass for both escaped and inner merit.
				escaped, inner := bothFn(xPhys)
				// Add constraint penalty if applicable.
				if !skipConstraints && e.cfg.ConstraintPenalty > 0 {
					constraints := model.ComputeConstraints(xPhys)
					for _, c := range constraints {
						escaped += e.cfg.ConstraintPenalty * c * c
					}
				}
				results[i].f = escaped
				results[i].tf = inner
			} else {
				results[i].f = e.fitness(model, xPhys)
				results[i].tf = innerMeritFn(xPhys)
			}
		}(i)
	}
	wg.Wait()

	return results
}

// Config holds PSO hyperparameters.
type Config struct {
	SwarmSize         int
	PsoIterations     int
	Inertia           float64
	Cognitive         float64
	Social            float64
	VelocityClamp     float64
	InitSpread        float64
	ConstraintPenalty float64
	StallWindowFrac   float64
	StallRelTol       float64
}

// Inertia decay bounds. The inertia weight decays linearly from the configured
// (or default) high value down to InertiaLow over the iteration budget.
const (
	// DefaultInertiaHigh is the built-in starting inertia used when the config
	// leaves Inertia unset (0).
	DefaultInertiaHigh = 0.9
	// InertiaLow is the floor of the inertia decay.
	InertiaLow = 0.4
)

// DefaultConfig returns the recommended starting values.
func DefaultConfig() Config {
	return Config{
		SwarmSize:         30,
		PsoIterations:     40,
		Inertia:           0, // 0 = built-in DefaultInertiaHigh (0.9) -> InertiaLow decay
		Cognitive:         1.494,
		Social:            1.494,
		VelocityClamp:     0.2,
		InitSpread:        0.1,
		ConstraintPenalty: 1000,
		StallWindowFrac:   0.2,
		StallRelTol:       1e-4,
	}
}

// Explorer implements escape.Explorer using global-best PSO.
type Explorer struct {
	cfg      Config
	seed     int64
	progress func(event string, fields map[string]any)
}

// NewExplorer creates a PSO explorer. The progress callback receives JSONL
// events (nil disables). Each worker goroutine must create its own Explorer
// to avoid data races.
func NewExplorer(cfg Config, seed int64, progress func(event string, fields map[string]any)) *Explorer {
	if cfg.SwarmSize <= 0 {
		cfg.SwarmSize = 30
	}
	if cfg.PsoIterations <= 0 {
		cfg.PsoIterations = 40
	}
	// Inertia is intentionally left at 0 when unset: the explorer then uses the
	// built-in DefaultInertiaHigh (0.9) -> InertiaLow (0.4) decay.
	if cfg.Cognitive <= 0 {
		cfg.Cognitive = 1.494
	}
	if cfg.Social <= 0 {
		cfg.Social = 1.494
	}
	if cfg.VelocityClamp <= 0 {
		cfg.VelocityClamp = 0.2
	}
	if cfg.InitSpread <= 0 {
		cfg.InitSpread = 0.1
	}
	if cfg.ConstraintPenalty <= 0 {
		cfg.ConstraintPenalty = 1000
	}
	if cfg.StallWindowFrac <= 0 {
		cfg.StallWindowFrac = 0.2
	}
	if cfg.StallRelTol <= 0 {
		cfg.StallRelTol = 1e-4
	}
	return &Explorer{cfg: cfg, seed: seed, progress: progress}
}

// Explore runs a global-best PSO swarm in normalized variable space and returns
// the best point found. The model must have escapes set before calling.
func (e *Explorer) Explore(model dls.Model, x0 []float64) dls.Result {
	variables := model.Variables()
	nVars := len(variables)
	swarmSize := e.cfg.SwarmSize
	maxIter := e.cfg.PsoIterations

	// ③ Enable light aperture sizing: reduce hex-grid rays from 256 to
	// numRays for faster PSO exploration.
	if las, ok := model.(interface{ SetLightApertureSizing(bool) }); ok {
		las.SetLightApertureSizing(true)
		defer las.SetLightApertureSizing(false)
	}

	// Compute scales (max-min) for normalization.
	scales := make([]float64, nVars)
	for i, v := range variables {
		scales[i] = v.Max - v.Min
		if scales[i] <= 0 {
			scales[i] = 1.0
		}
	}

	// Normalize x0 to [0, 1].
	x0Norm := make([]float64, nVars)
	for i := range x0Norm {
		x0Norm[i] = (x0[i] - variables[i].Min) / scales[i]
		if x0Norm[i] < 0 {
			x0Norm[i] = 0
		} else if x0Norm[i] > 1 {
			x0Norm[i] = 1
		}
	}

	rng := rand.New(rand.NewSource(e.seed))

	// Initialize swarm positions and velocities.
	pos := make([][]float64, swarmSize)
	vel := make([][]float64, swarmSize)
	pbestPos := make([][]float64, swarmSize)
	pbestFit := make([]float64, swarmSize)

	for i := 0; i < swarmSize; i++ {
		pos[i] = make([]float64, nVars)
		vel[i] = make([]float64, nVars)
		pbestPos[i] = make([]float64, nVars)

		if i == 0 {
			// Particle 0: exactly x0.
			copy(pos[i], x0Norm)
		} else {
			// Particles 1..N-1: x0 + uniform(-spread, +spread), clamped.
			for j := 0; j < nVars; j++ {
				pos[i][j] = x0Norm[j] + (rng.Float64()*2-1)*e.cfg.InitSpread
				if pos[i][j] < 0 {
					pos[i][j] = 0
				} else if pos[i][j] > 1 {
					pos[i][j] = 1
				}
			}
		}
		// Velocities initialized to zero.
		copy(pbestPos[i], pos[i])
	}

	// Detect InnerMerit: when the model (escape Wrapper) exposes the true
	// (unescaped) merit, gbest is selected by true merit so known minima
	// with escape bumps are excluded from the global-best pool.
	innerMeritFn := model.EvaluateMerit // fallback: use escaped merit
	if ime, ok := model.(interface{ InnerMerit([]float64) float64 }); ok {
		innerMeritFn = ime.InnerMerit
	}

	// ① Detect EvaluateMeritBoth: single ray-trace pass for escaped + inner merit.
	var bothFn func([]float64) (float64, float64)
	if bfn, ok := model.(interface{ EvaluateMeritBoth([]float64) (float64, float64) }); ok {
		bothFn = bfn.EvaluateMeritBoth
	}

	// ② Detect HasConstraints: skip redundant ComputeConstraints when the
	// inner model has no active constraints.
	skipConstraints := false
	if hc, ok := model.(interface{ HasConstraints() bool }); ok {
		skipConstraints = !hc.HasConstraints()
	}

	// Evaluate initial swarm.
	// Update pupil at x0 first.
	if pu, ok := model.(dls.PupilUpdater); ok {
		pu.UpdatePupils(denormalize(x0Norm, variables, scales))
	}

	gbestFit := math.MaxFloat64
	gbestTrueFit := math.MaxFloat64
	gbestPos := make([]float64, nVars)
	copy(gbestPos, x0Norm)

	results := e.evalSwarm(model, pos, variables, scales, innerMeritFn, bothFn, skipConstraints)
	for i := 0; i < swarmSize; i++ {
		pbestFit[i] = results[i].f
		if results[i].tf < gbestTrueFit {
			gbestTrueFit = results[i].tf
			gbestFit = results[i].f
			copy(gbestPos, pos[i])
		}
	}

	// Emit pso_init: configuration snapshot at the start of the PSO phase.
	if e.progress != nil {
		e.progress("pso_init", map[string]any{
			"swarm_size":       swarmSize,
			"n_vars":           nVars,
			"pso_iterations":   maxIter,
			"inertia":          e.cfg.Inertia,
			"has_both_fn":      bothFn != nil,
			"skip_constraints": skipConstraints,
			"light_aperture":   true,
		})
	}

	// Stall detection state (sliding window: compare gbest every stallWindow
	// iterations, not every iteration — matching the DLS solver's pattern).
	bestFitWindowAgo := gbestFit
	lastCheckIter := 0
	restarts := 0
	stallWindow := int(float64(maxIter) * e.cfg.StallWindowFrac)
	if stallWindow < 2 {
		stallWindow = 2
	}

	// PSO iteration loop.
	for iter := 0; iter < maxIter; iter++ {
		// Check stop channel (via model.Options().Stop).
		opts := model.Options()
		if stopped(opts.Stop) {
			return e.buildResult(model, variables, scales, gbestPos, gbestFit, iter, "interrupted")
		}

		// Update pupil at global best.
		if pu, ok := model.(dls.PupilUpdater); ok {
			pu.UpdatePupils(denormalize(gbestPos, variables, scales))
		}
		if msu, ok := model.(dls.MeritScheduleUpdater); ok {
			msu.UpdateMeritWeights(denormalize(gbestPos, variables, scales), iter)
		}

		// Inertia weight decays linearly from the configured starting value
		// (default DefaultInertiaHigh = 0.9) down to InertiaLow = 0.4.
		inertiaHigh := e.cfg.Inertia
		if inertiaHigh <= 0 {
			inertiaHigh = DefaultInertiaHigh
		}
		w := inertiaHigh - (inertiaHigh-InertiaLow)*float64(iter)/float64(maxIter)
		if w < InertiaLow {
			w = InertiaLow
		}

		// Phase 1: velocity update + position update (sequential, O(nVars) per particle).
		for i := 0; i < swarmSize; i++ {
			r1 := rng.Float64()
			r2 := rng.Float64()

			for j := 0; j < nVars; j++ {
				vel[i][j] = w*vel[i][j] +
					e.cfg.Cognitive*r1*(pbestPos[i][j]-pos[i][j]) +
					e.cfg.Social*r2*(gbestPos[j]-pos[i][j])

				vmax := e.cfg.VelocityClamp
				if vel[i][j] > vmax {
					vel[i][j] = vmax
				} else if vel[i][j] < -vmax {
					vel[i][j] = -vmax
				}

				pos[i][j] += vel[i][j]
				if pos[i][j] < 0 {
					pos[i][j] = 0
				} else if pos[i][j] > 1 {
					pos[i][j] = 1
				}
			}
		}

		// Phase 2: evaluate all particles in parallel (dominant cost).
		results := e.evalSwarm(model, pos, variables, scales, innerMeritFn, bothFn, skipConstraints)

		// Phase 3: update personal and global bests (sequential, lightweight).
		swarmSum := 0.0
		swarmMin := math.MaxFloat64
		swarmMax := -math.MaxFloat64
		for i := 0; i < swarmSize; i++ {
			if results[i].f < pbestFit[i] {
				pbestFit[i] = results[i].f
				copy(pbestPos[i], pos[i])
			}
			if results[i].tf < gbestTrueFit {
				gbestTrueFit = results[i].tf
				gbestFit = results[i].f
				copy(gbestPos, pos[i])
			}
			swarmSum += results[i].f
			if results[i].f < swarmMin {
				swarmMin = results[i].f
			}
			if results[i].f > swarmMax {
				swarmMax = results[i].f
			}
		}

		// Emit pso_iter every 10 iterations for progress tracking.
		if e.progress != nil && (iter+1)%10 == 0 {
			e.progress("pso_iter", map[string]any{
				"iter":         iter + 1,
				"gbest_merit":  gbestFit,
				"gbest_true":   gbestTrueFit,
				"swarm_mean":   swarmSum / float64(swarmSize),
				"swarm_min":    swarmMin,
				"swarm_max":    swarmMax,
				"restarts":     restarts,
				"phase":        "pso",
			})
		}

		// Stall detection (sliding window: check every stallWindow iterations).
		// On stall, reinitialize the worst 50% of the swarm around gbest
		// and continue exploring instead of returning early.
		if iter-lastCheckIter >= stallWindow {
			if lastCheckIter > 0 {
				denom := math.Abs(bestFitWindowAgo)
				if denom < 1e-10 {
					denom = 1e-10
				}
				relImprove := (bestFitWindowAgo - gbestFit) / denom
				if relImprove < e.cfg.StallRelTol {
					if e.progress != nil {
						e.progress("pso_stall", map[string]any{
							"iter":     iter,
							"merit":    gbestFit,
							"improve":  relImprove,
							"restarts": restarts,
						})
					}
					// Reinitialize worst 50% of swarm around gbest.
					half := swarmSize / 2
					if half < 1 {
						half = 1
					}
					// Build index list sorted by fitness (worst first).
					idx := make([]int, swarmSize)
					for i := range idx {
						idx[i] = i
					}
					sort.Slice(idx, func(a, b int) bool {
						return results[idx[a]].f > results[idx[b]].f
					})
					restartSpread := e.cfg.InitSpread * 2
					if restartSpread > 0.3 {
						restartSpread = 0.3
					}
					for k := 0; k < half; k++ {
						i := idx[k]
						for j := 0; j < nVars; j++ {
							pos[i][j] = gbestPos[j] + (rng.Float64()*2-1)*restartSpread
							if pos[i][j] < 0 {
								pos[i][j] = 0
							} else if pos[i][j] > 1 {
								pos[i][j] = 1
							}
							vel[i][j] = 0
							pbestPos[i][j] = pos[i][j]
						}
					}
					// Re-evaluate reinitialized particles.
					reResults := e.evalSwarm(model, pos, variables, scales, innerMeritFn, bothFn, skipConstraints)
					for k := 0; k < half; k++ {
						i := idx[k]
						results[i] = reResults[i]
						pbestFit[i] = reResults[i].f
						if reResults[i].tf < gbestTrueFit {
							gbestTrueFit = reResults[i].tf
							gbestFit = reResults[i].f
							copy(gbestPos, pos[i])
						}
					}
					restarts++
					// Emit pso_restart event.
					if e.progress != nil {
						e.progress("pso_restart", map[string]any{
							"iter":          iter,
							"restarts":      restarts,
							"gbest_merit":   gbestFit,
							"reinitialized": half,
						})
					}
					// Reset stall window.
					bestFitWindowAgo = gbestFit
					lastCheckIter = iter
					continue
				}
			}
			bestFitWindowAgo = gbestFit
			lastCheckIter = iter
		}
	}

	return e.buildResult(model, variables, scales, gbestPos, gbestFit, maxIter, "max_iterations")
}

// fitness computes the penalized fitness: merit + penalty * sum(c_j^2).
func (e *Explorer) fitness(model dls.Model, xPhys []float64) float64 {
	f := model.EvaluateMerit(xPhys)
	if e.cfg.ConstraintPenalty > 0 {
		constraints := model.ComputeConstraints(xPhys)
		for _, c := range constraints {
			f += e.cfg.ConstraintPenalty * c * c
		}
	}
	return f
}

// buildResult constructs a dls.Result from the global best.
func (e *Explorer) buildResult(model dls.Model, variables []dls.VariableInfo, scales, gbestNorm []float64, gbestFit float64, iterations int, status string) dls.Result {
	gbestPhys := denormalize(gbestNorm, variables, scales)
	// Compute inner merit (without constraints) for AfterMerit.
	afterMerit := model.EvaluateMerit(gbestPhys)

	vars := make([]dls.VariableState, len(variables))
	for i, v := range variables {
		vars[i] = dls.VariableState{
			Name:      v.Name,
			SurfaceID: v.SurfaceID,
			Param:     v.Param,
			Before:    gbestPhys[i],
			After:     gbestPhys[i],
		}
	}

	return dls.Result{
		BeforeMerit: gbestFit,
		AfterMerit:  afterMerit,
		Iterations:  iterations,
		Status:      status,
		Variables:   vars,
	}
}

// denormalize converts from [0, 1] normalized space to physical space.
func denormalize(norm []float64, variables []dls.VariableInfo, scales []float64) []float64 {
	phys := make([]float64, len(norm))
	for i := range phys {
		phys[i] = variables[i].Min + norm[i]*scales[i]
	}
	return phys
}

// stopped checks if the stop channel has been closed.
func stopped(stop <-chan struct{}) bool {
	if stop == nil {
		return false
	}
	select {
	case <-stop:
		return true
	default:
		return false
	}
}
