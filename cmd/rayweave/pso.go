package main

import (
	"os"

	"github.com/hiroki/rayweaver/internal/escape"
	"github.com/hiroki/rayweaver/internal/pso"
	"github.com/hiroki/rayweaver/internal/types"
)

// runPSO runs the PSO escape-function global optimisation. It mirrors runEscape
// but requires optimization.pso and uses a PSO explorer instead of DLS for the
// escape phase. The three-phase cycle (PSO exploration, glass, clean DLS) is
// identical to escape; only the exploration engine differs.
func runPSO(data []byte, glassDir string, verbose bool, logFile string, saveBase string, powerSolve bool, powerSolveSurfaces string, glassVariables bool, keepInfeasible bool, swarmSize int, psoIterations int, constraintPenalty float64) {
	input := parseYAML[types.Input](data)
	setReferenceWavelength(input.Chief)
	if input.Optimization == nil {
		errOut("Error: 'optimization' section is required")
		os.Exit(1)
	}
	if input.Optimization.PSO == nil {
		errOut("Error: 'optimization.pso' section is required")
		os.Exit(1)
	}

	// Apply CLI overrides to the PSO config (CLI wins per 3 principles).
	if swarmSize > 0 {
		input.Optimization.PSO.SwarmSize = swarmSize
	}
	if psoIterations > 0 {
		input.Optimization.PSO.PsoIterations = psoIterations
	}
	if constraintPenalty > 0 {
		input.Optimization.PSO.ConstraintPenalty = constraintPenalty
	}

	// Copy the PSO's embedded escape config to the top-level escape field
	// so the shared cycle infrastructure can use it.
	input.Optimization.Escape = &input.Optimization.PSO.EscapeConfig

	// Resolve the power-preserving glass phase.
	psu := effectivePowerSolve(input, powerSolve, powerSolveSurfaces)
	input.Optimization.PowerSolve = psu

	gc, _ := loadCatalogs(&input, glassDir)
	writeBackGlassDir(&input, glassDir)
	if glassVariables {
		applyGlassVariables(&input, gc)
	}

	// Build the PSO explorer factory. Each worker gets its own Explorer
	// instance with a unique seed derived from the worker ID. The factory
	// receives the shared Progress from runEscapeCore so PSO-internal events
	// flow through the same writers.
	psoCfg := buildPSOConfig(input.Optimization.PSO)
	newExplorer := func(progress *escape.Progress, seed int64) escape.Explorer {
		var psoProgress func(string, map[string]any)
		if progress != nil {
			psoProgress = func(event string, fields map[string]any) {
				progress.Event(event, fields)
			}
		}
		return pso.NewExplorer(psoCfg, seed, psoProgress)
	}

	// Delegate to the shared escape core with the PSO explorer factory.
	runEscapeCore(input, gc, verbose, logFile, saveBase, keepInfeasible, false, newExplorer)
}

// buildPSOConfig translates the PSOConfig (with CLI overrides already applied)
// into a pso.Config, using YAML values directly.
func buildPSOConfig(cfg *types.PSOConfig) pso.Config {
	c := pso.DefaultConfig()
	if cfg.SwarmSize > 0 {
		c.SwarmSize = cfg.SwarmSize
	}
	if cfg.PsoIterations > 0 {
		c.PsoIterations = cfg.PsoIterations
	}
	if cfg.Inertia > 0 {
		c.Inertia = cfg.Inertia
	}
	if cfg.Cognitive > 0 {
		c.Cognitive = cfg.Cognitive
	}
	if cfg.Social > 0 {
		c.Social = cfg.Social
	}
	if cfg.VelocityClamp > 0 {
		c.VelocityClamp = cfg.VelocityClamp
	}
	if cfg.InitSpread > 0 {
		c.InitSpread = cfg.InitSpread
	}
	if cfg.ConstraintPenalty > 0 {
		c.ConstraintPenalty = cfg.ConstraintPenalty
	}
	// Stall params use escape defaults (not from EscapeConfig).
	c.StallWindowFrac = 0.2
	c.StallRelTol = 1e-4
	return c
}
