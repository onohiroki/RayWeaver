// Package pso implements a Particle Swarm Optimizer for lens design global
// optimization. It satisfies the escape.Explorer interface, replacing the
// DLS escape phase with a gradient-free population search.
package pso

import (
	"math"
	"math/rand"

	"github.com/hiroki/rayweaver/internal/dls"
)

// Config holds PSO hyperparameters.
type Config struct {
	SwarmSize        int
	PsoIterations    int
	Inertia          float64
	Cognitive        float64
	Social           float64
	VelocityClamp    float64
	InitSpread       float64
	ConstraintPenalty float64
	StallWindowFrac  float64
	StallRelTol      float64
}

// DefaultConfig returns the recommended starting values.
func DefaultConfig() Config {
	return Config{
		SwarmSize:        30,
		PsoIterations:    40,
		Inertia:          0.729,
		Cognitive:        1.494,
		Social:           1.494,
		VelocityClamp:    0.2,
		InitSpread:       0.1,
		ConstraintPenalty: 1000,
		StallWindowFrac:  0.2,
		StallRelTol:      1e-4,
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
	if cfg.Inertia <= 0 {
		cfg.Inertia = 0.729
	}
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

	// Evaluate initial swarm.
	// Update pupil at x0 first.
	if pu, ok := model.(dls.PupilUpdater); ok {
		pu.UpdatePupils(denormalize(x0Norm, variables, scales))
	}

	gbestFit := math.MaxFloat64
	gbestPos := make([]float64, nVars)
	copy(gbestPos, x0Norm)

	for i := 0; i < swarmSize; i++ {
		xPhys := denormalize(pos[i], variables, scales)
		f := e.fitness(model, xPhys)
		pbestFit[i] = f
		if f < gbestFit {
			gbestFit = f
			copy(gbestPos, pos[i])
		}
	}

	// Stall detection state.
	bestFitWindowAgo := gbestFit
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

		// Inertia weight linearly decays from 0.9 to 0.4.
		w := 0.9 - 0.5*float64(iter)/float64(maxIter)
		if e.cfg.Inertia > 0 {
			w = e.cfg.Inertia * (0.9 - 0.4) / 0.729 * (0.9 - 0.5*float64(iter)/float64(maxIter))
			if w < 0.4 {
				w = 0.4
			}
		}

		for i := 0; i < swarmSize; i++ {
			r1 := rng.Float64()
			r2 := rng.Float64()

			for j := 0; j < nVars; j++ {
				vel[i][j] = w*vel[i][j] +
					e.cfg.Cognitive*r1*(pbestPos[i][j]-pos[i][j]) +
					e.cfg.Social*r2*(gbestPos[j]-pos[i][j])

				// Clamp velocity.
				vmax := e.cfg.VelocityClamp
				if vel[i][j] > vmax {
					vel[i][j] = vmax
				} else if vel[i][j] < -vmax {
					vel[i][j] = -vmax
				}

				pos[i][j] += vel[i][j]

				// Clamp position to [0, 1].
				if pos[i][j] < 0 {
					pos[i][j] = 0
				} else if pos[i][j] > 1 {
					pos[i][j] = 1
				}
			}

			// Evaluate.
			xPhys := denormalize(pos[i], variables, scales)
			f := e.fitness(model, xPhys)

			// Update personal best.
			if f < pbestFit[i] {
				pbestFit[i] = f
				copy(pbestPos[i], pos[i])
			}

			// Update global best.
			if f < gbestFit {
				gbestFit = f
				copy(gbestPos, pos[i])
			}
		}

		// Stall detection.
		if iter >= stallWindow {
			relImprove := (bestFitWindowAgo - gbestFit) / (math.Abs(bestFitWindowAgo) + 1e-12)
			if relImprove < e.cfg.StallRelTol {
				if e.progress != nil {
					e.progress("pso_stall", map[string]any{
						"iter":    iter,
						"merit":   gbestFit,
						"improve": relImprove,
					})
				}
				return e.buildResult(model, variables, scales, gbestPos, gbestFit, iter+1, "converged")
			}
			bestFitWindowAgo = gbestFit
		} else if iter == stallWindow-1 {
			bestFitWindowAgo = gbestFit
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
