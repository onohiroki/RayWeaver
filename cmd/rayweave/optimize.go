package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/optimize"
	"github.com/hiroki/rayweaver/internal/spectral"
	"github.com/hiroki/rayweaver/internal/types"
)

const (
	defaultMaxIter = 100
	defaultMu      = 1.0
	defaultTol     = 1e-6
	defaultEpsilon = 1e-6
	defaultNumRays = 128
)

// optimizeRun holds the resolved inputs shared by `optimize` and its
// `optimize snap` subcommand: the parsed/echoed input, the per-config
// ConfigInputs and variables, the glass catalog and the resolved solver
// settings.
type optimizeRun struct {
	input            types.Input
	configs          []optimize.ConfigInput
	sharedVars       []types.SharedVariable
	localVars        []types.LocalVariableDef
	gc               *glass.Catalog
	maxIter          int
	tol              float64
	epsilon          float64
	mu               float64
	numRays          int
	apertureMargin   float64
	apertureMarginMM float64
}

// buildConfigInputs converts the document's active configs into the
// optimize.ConfigInput list shared by optimize, escape and pso. Each config
// carries its own surfaces, fields, wavelengths, merit and constraints, with
// the top-level optimization sections as fallbacks. It returns nil when the
// document declares no active config, so the caller can apply its own
// single-config fallback (optimize) or reject the document (global search).
func buildConfigInputs(input types.Input, gc *glass.Catalog) []optimize.ConfigInput {
	var configs []optimize.ConfigInput
	for _, cfg := range input.Configs {
		if !cfg.Active {
			continue
		}
		surfaces := cfg.Surfaces
		if len(surfaces) == 0 {
			errExitf("Error: config %q has no surfaces defined", cfg.ID)
		}

		fields := append([]types.FieldItem(nil), cfg.Fields...)
		if len(fields) == 0 && input.Chief != nil {
			for _, f := range input.Chief.Fields {
				fields = append(fields, types.FieldItem{ID: 0, AngleDeg: f.Angle, Weight: 1.0})
			}
		}

		var wavelengths []types.WavelengthItem
		for _, w := range cfg.Wavelengths {
			wavelengths = append(wavelengths, w)
		}

		ref := effectiveReferenceWavelength(input.Chief)
		resolver := spectral.NewResolver(cfg.Wavelengths, input.Spectrum, ref)
		norm := ""
		if input.Optimization != nil {
			norm = input.Optimization.WavelengthNormalization
		}

		var meritTerms []types.MeritTerm
		if cfg.Merit != nil {
			expanded, err := spectral.ExpandTerms(cfg.Merit.Terms, resolver, norm)
			if err != nil {
				errExitf("Error: config %q: %v", cfg.ID, err)
			}
			meritTerms = expanded
		}
		var meritModes []types.MeritMode
		if len(cfg.MeritModes) > 0 {
			meritModes = make([]types.MeritMode, len(cfg.MeritModes))
			for i, m := range cfg.MeritModes {
				expanded, err := spectral.ExpandTerms(m.Terms, resolver, norm)
				if err != nil {
					errExitf("Error: config %q mode %q: %v", cfg.ID, m.Name, err)
				}
				meritModes[i] = m
				meritModes[i].Terms = expanded
			}
		}

		constraints := input.Optimization.Constraints
		if len(cfg.Constraints) > 0 {
			constraints = cfg.Constraints
		}

		stopSurface := 0
		if input.Chief != nil {
			stopSurface = input.Chief.StopSurface
		}

		configs = append(configs, optimize.ConfigInput{
			ID:                      cfg.ID,
			Weight:                  cfg.Weight,
			StopSurface:             stopSurface,
			RefSurface:              chiefRefSurface(input),
			PupilZ:                  computePupilZ(input, surfaces, gc),
			ReferenceWavelength:     ref,
			Surfaces:                surfaces,
			Fields:                  fields,
			Wavelengths:             wavelengths,
			Spectrum:                input.Spectrum,
			WavelengthNormalization: norm,
			MeritTerms:              meritTerms,
			MeritModes:              meritModes,
			Constraints:             constraints,
			PupilModel:              pupilModelForConfig(input),
			Normalization:           input.Optimization.MeritNormalization,
			RayDefinition:           chiefRayDefinition(input),
			WavefrontReference:      input.Optimization.WavefrontReference,
		})
	}
	return configs
}

// dlsTuning holds the solver settings that optimize, escape and pso resolve
// from optimization.* in exactly the same way.
type dlsTuning struct {
	maxIter          int
	tol              float64
	epsilon          float64
	mu               float64
	numRays          int
	apertureMargin   float64
	apertureMarginMM float64
}

// resolveDLSTuning applies the built-in DLS defaults, so an unset YAML value
// means the same thing in every entry point. warnMargin reports a below-1.0
// aperture_margin (it clips the pupil grid and stalls convergence); the global
// search used to clamp it silently, and both paths now resolve it here.
func resolveDLSTuning(oc *types.OptimizationConfig, warnMargin bool) dlsTuning {
	t := dlsTuning{
		maxIter:          oc.MaxIter,
		tol:              oc.Tol,
		epsilon:          oc.Epsilon,
		mu:               oc.Mu,
		numRays:          oc.NumRays,
		apertureMargin:   oc.ApertureMargin,
		apertureMarginMM: oc.ApertureMarginMM,
	}
	if t.maxIter <= 0 {
		t.maxIter = defaultMaxIter
	}
	if t.tol <= 0 {
		t.tol = defaultTol
	}
	if t.epsilon <= 0 {
		t.epsilon = defaultEpsilon
	}
	if t.mu <= 0 {
		t.mu = defaultMu
	}
	if t.numRays <= 0 {
		t.numRays = defaultNumRays
	}
	if t.apertureMargin <= 0 {
		t.apertureMargin = 1.0
	}
	if t.apertureMargin < 1.0 {
		if warnMargin {
			errOut("Warning: aperture_margin %.3f < 1.0 is not recommended (pupil grid smaller than the aperture stalls DLS); clamping to 1.0", t.apertureMargin)
		}
		t.apertureMargin = 1.0
	}
	if t.apertureMarginMM <= 0 {
		t.apertureMarginMM = types.DefaultApertureMarginMM
	}
	return t
}

// buildOptimizeRun parses and resolves the input document into the pieces both
// `optimize` and `optimize snap` need. requireMerit controls whether missing
// merit terms are fatal (snap only needs the input for its existing surfaces,
// but shares the same validation for consistency). Returns ok=false when the
// caller should stop (an error has already been reported).
func buildOptimizeRun(data []byte, glassDir, excludeParams string, powerSolve bool, powerSolveSurfaces string, glassVariables, requireMerit bool) (optimizeRun, bool) {
	var run optimizeRun
	input := parseYAML[types.Input](data)
	setReferenceWavelength(input.Chief)

	if input.Optimization == nil {
		errExitf("Error: 'optimization' section is required")
	}
	if input.Optimization.BackFocusSolve != nil {
		input.Optimization.BackFocusSolve.Spectrum = input.Spectrum
	}

	// Resolve the power-preserving solve under the CLI/YAML precedence rule: a
	// CLI surface list wins, else --power-solve turns the YAML list on, else the
	// YAML power_solve section is used as-is. The effective config is echoed
	// into the output (write-back).
	psu := effectivePowerSolve(input, powerSolve, powerSolveSurfaces)
	input.Optimization.PowerSolve = psu

	excluded := map[string]bool{}
	for _, p := range strings.Split(excludeParams, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			excluded[p] = true
		}
	}

	// Drop excluded target params from the echoed variables too, so the
	// output YAML reflects the reduced variable set.
	if len(excluded) > 0 {
		kept := input.Optimization.Variables[:0]
		for _, v := range input.Optimization.Variables {
			if !excluded[v.Target.Param] {
				kept = append(kept, v)
			}
		}
		input.Optimization.Variables = kept
	}

	gc, _ := openGlass(&input, glassDir)

	// --glass-variables auto-generates nd/vd variables for every refractive
	// element (one representative surface each). The merit is left untouched:
	// the user's explicit merit is always preserved. It composes with
	// power_solve so the user can pin the layout while the glasses are the only
	// free variables.
	if glassVariables {
		applyGlassVariables(&input, gc)
	}
	if err := validateSharedGlassVariables(&input, gc); err != nil {
		errExitf("Error: %v", err)
	}

	// Build the per-config optimisation inputs (shared/local variables are
	// read below; the unified Optimizer drives all configs together).
	configs := buildConfigInputs(input, gc)

	// Single-config YAML may omit the configs[] wrapper entirely or use a
	// default config with weight 1.0; fall back to a synthetic config.
	if len(configs) == 0 {
		cfgSurfaces := firstConfigSurfaces(input)
		configs = []optimize.ConfigInput{{
			ID:                  "config1",
			Weight:              1.0,
			StopSurface:         0,
			RefSurface:          chiefRefSurface(input),
			PupilZ:              computePupilZ(input, cfgSurfaces, gc),
			ReferenceWavelength: effectiveReferenceWavelength(input.Chief),
			Surfaces:            cfgSurfaces,
			Fields:              loadFields(input),
			Spectrum:            input.Spectrum,
			Constraints:         input.Optimization.Constraints,
			PupilModel:          pupilModelForConfig(input),
			RayDefinition:       chiefRayDefinition(input),
			WavefrontReference:  input.Optimization.WavefrontReference,
		}}
	}

	// Convert single-config `optimization.variables` into local variables of
	// the first config so one Optimizer handles both modes.
	var sharedVars []types.SharedVariable
	var localVars []types.LocalVariableDef
	if len(configs) == 1 && len(input.Optimization.SharedVariables) == 0 && len(input.Optimization.LocalVariables) == 0 {
		cfgID := configs[0].ID
		for _, v := range input.Optimization.Variables {
			if !v.Active {
				continue
			}
			if excluded[v.Target.Param] {
				continue
			}
			switch v.Target.Type {
			case "surface", "pupil_model":
				localVars = append(localVars, types.LocalVariableDef{
					Name:   v.Name,
					Config: cfgID,
					Target: v.Target,
					Min:    v.Min,
					Max:    v.Max,
					Active: true,
				})
			default:
				continue
			}
		}
	} else {
		sharedVars = input.Optimization.SharedVariables
		localVars = input.Optimization.LocalVariables
	}

	if len(sharedVars) == 0 && len(localVars) == 0 {
		errExitf("Error: no optimization variables defined")
	}

	if requireMerit {
		hasMerit := false
		for _, c := range configs {
			if len(c.MeritTerms) > 0 || len(c.MeritModes) > 0 {
				hasMerit = true
				break
			}
		}
		if !hasMerit {
			errExitf("Error: no merit terms defined (add 'optimization.merit', 'configs[].merit' or 'configs[].merit_modes')")
		}
	}

	tuning := resolveDLSTuning(input.Optimization, true)
	maxIter := tuning.maxIter
	tol := tuning.tol
	epsilon := tuning.epsilon
	mu := tuning.mu
	numRays := tuning.numRays
	apertureMargin := tuning.apertureMargin
	apertureMarginMM := tuning.apertureMarginMM

	writeBackWavelengthNormalization(&input)
	run.input = input
	run.configs = configs
	run.sharedVars = sharedVars
	run.localVars = localVars
	run.gc = gc
	run.maxIter = maxIter
	run.tol = tol
	run.epsilon = epsilon
	run.mu = mu
	run.numRays = numRays
	run.apertureMargin = apertureMargin
	run.apertureMarginMM = apertureMarginMM
	return run, true
}

func runOptimize(data []byte, verbose bool, logFile string, glassDir string, excludeParams string, powerSolve bool, powerSolveSurfaces string, glassVariables bool) {
	run, ok := buildOptimizeRun(data, glassDir, excludeParams, powerSolve, powerSolveSurfaces, glassVariables, true)
	if !ok {
		return
	}
	input := run.input
	configs := run.configs
	sharedVars := run.sharedVars
	localVars := run.localVars
	gc := run.gc

	maxIter := run.maxIter
	tol := run.tol
	epsilon := run.epsilon
	mu := run.mu
	numRays := run.numRays
	apertureMargin := run.apertureMargin
	apertureMarginMM := run.apertureMarginMM

	var logger dls.Logger
	logWriters := []struct {
		name string
		w    *os.File
	}{}

	if verbose {
		logger = &jsonLogger{w: os.Stderr}
	}

	if logFile != "" {
		f, err := os.Create(logFile)
		if err != nil {
			errExitf("Error creating log file: %v", err)
		}
		logWriters = append(logWriters, struct {
			name string
			w    *os.File
		}{name: logFile, w: f})
		if logger == nil {
			logger = &jsonLogger{w: f}
		} else {
			logger = &multiLogger{loggers: []dls.Logger{logger, &jsonLogger{w: f}}}
		}
	}

	var hull *glass.ConvexHull
	hullMargin, hullWeight := resolveGlassHull(input.Optimization.GlassHull, &hull, gc)

	opt := optimize.NewMultiOptimizer(configs, sharedVars, localVars, input.Optimization.VariableLinks, gc, maxIter, mu, tol, epsilon, apertureMargin, numRays, input.Optimization.MuConMax, input.Optimization.JacobianWorkers, logger, hull, hullMargin, hullWeight, input.Optimization.CentralDiff, input.Optimization.BFGS, input.Optimization.AdaptiveDamping, input.Optimization.RegionActive)
	opt.SetApertureMarginMM(apertureMarginMM)
	if input.Optimization.PowerSolve != nil && input.Optimization.PowerSolve.Enabled {
		opt.SetPowerSolve(input.Optimization.PowerSolve.Surfaces)
	}
	if input.Optimization.BackFocusSolve != nil && input.Optimization.BackFocusSolve.Enabled {
		opt.SetBackFocusSolve(input.Optimization.BackFocusSolve)
	}
	opt.SetDiffractionMTF(input.Optimization.DiffractionMTF)
	if input.Optimization.GlassAttraction != nil {
		opt.SetGlassAttraction(input.Optimization.GlassAttraction, gc)
	}
	applyDegenerate(opt, input.Optimization.Degenerate)

	// Validate the conditional merit schedule and the glass_role kind before
	// running (bad configuration would otherwise silently contribute nothing).
	if input.Optimization.MeritSchedule != nil {
		hasModes := false
		for _, c := range configs {
			if len(c.MeritModes) > 0 {
				hasModes = true
				break
			}
		}
		if !hasModes {
			errExitf("Error: optimization.merit_schedule requires configs[].merit_modes on at least one active config")
		}
		if input.Optimization.MeritSchedule.Metric == "glass_role" && len(input.Optimization.MeritSchedule.GlassSurfaces) == 0 {
			errExitf("Error: optimization.merit_schedule metric 'glass_role' requires glass_surfaces")
		}
		if agg := input.Optimization.MeritSchedule.MetricAggregation; agg != "" && agg != "mean" && agg != "max" {
			errExitf("Error: optimization.merit_schedule metric_aggregation must be 'mean' or 'max'")
		}
	}
	checkGlassRole := func(terms []types.MeritTerm) {
		for _, mt := range terms {
			if mt.Kind == optimize.MeritGlassRole && len(mt.SurfaceSet) == 0 {
				errExitf("Error: glass_role merit term requires surface_set (e.g. [3, 5])")
			}
		}
	}
	for _, c := range configs {
		checkGlassRole(c.MeritTerms)
		for _, m := range c.MeritModes {
			checkGlassRole(m.Terms)
		}
	}
	opt.SetMeritSchedule(input.Optimization.MeritSchedule)

	// Report (and warn about) the field angles the model froze at this start
	// geometry: from here the merit, the constraints and the aperture sizing all
	// measure those fields, whatever the variables do to the geometry.
	freezeFieldReport(&input, opt)

	// Two-stage stop on SIGINT/SIGTERM.
	//
	//  1st signal: graceful stop. Closes the mid-solve stop channel so the
	//     running DLS aborts at the next checkpoint (top of an iteration, after
	//     the pupil update / Jacobian, inside the line search) and returns the
	//     best point found so far with Status "interrupted". The result is
	//     written to stdout as usual (interrupted: true) and the run exits 0.
	//  2nd signal: force quit immediately (exit 1).
	stop := make(chan struct{})
	opt.SetStop(stop)
	stages := newStageRunner(
		func() { close(stop) },
		func() {
			fmt.Fprintln(os.Stderr, "optimize: force quit")
			os.Exit(1)
		},
	)
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		for n := 1; ; n++ {
			sig := <-sigCh
			from, to := stages.request(n)
			if from < 1 && to >= 1 {
				fmt.Fprintf(os.Stderr, "optimize: %v received — stopping at the current DLS iteration and writing the best result found so far (press Ctrl-C again to force quit)\n", sig)
			}
		}
	}()
	// Windows has no SIGTERM, so a detached launcher writes the same stages to
	// a file. On Unix the variable is unset and this starts nothing.
	stopWatch := watchStopFile(os.Getenv(stopFileEnv), func(n int) {
		from, to := stages.request(n)
		if from < 1 && to >= 1 {
			fmt.Fprintln(os.Stderr, "optimize: stop requested — stopping at the current DLS iteration and writing the best result found so far")
		}
	})
	defer stopWatch()

	result := opt.Optimize()

	interrupted := result.Status == dls.StatusInterrupted

	// Emit a per-term merit breakdown so the reported merit value can be
	// reconciled against an external evaluation (e.g. `chief` spot RMS).
	if len(logWriters) > 0 || verbose {
		finalX := finalXFromStates(result.Variables)
		bd := opt.MeritBreakdown(finalX)
		data, _ := json.Marshal(map[string]interface{}{"event": "breakdown", "terms": bd})
		line := string(data)
		if verbose {
			fmt.Fprintln(os.Stderr, line)
		}
		for _, lw := range logWriters {
			fmt.Fprintln(lw.w, line)
		}
	}

	for _, lw := range logWriters {
		lw.w.Close()
	}

	multi := len(configs) > 1
	if multi {
		fmt.Fprintf(os.Stderr, "=== Multi-Config Optimization complete ===\n")
	} else {
		fmt.Fprintf(os.Stderr, "=== Optimization complete ===\n")
	}
	fmt.Fprintf(os.Stderr, "  Status:      %s\n", result.Status)
	if interrupted {
		fmt.Fprintf(os.Stderr, "  Interrupted: true (best result found so far written to stdout)\n")
	}
	fmt.Fprintf(os.Stderr, "  Iterations:  %d\n", result.Iterations)
	fmt.Fprintf(os.Stderr, "  Accepted:    %d (iterations that improved the merit)\n", result.AcceptedSteps)
	fmt.Fprintf(os.Stderr, "  Before:      %.6e\n", result.BeforeMerit)
	fmt.Fprintf(os.Stderr, "  After:       %.6e\n", result.AfterMerit)
	if result.BeforeMerit > 0 {
		improvement := (result.BeforeMerit - result.AfterMerit) / result.BeforeMerit * 100
		fmt.Fprintf(os.Stderr, "  Improvement: %.2f%%\n", improvement)
	}
	if result.Diagnostic != "" {
		fmt.Fprintf(os.Stderr, "  WARNING: DLS stalled — %s\n", result.Diagnostic)
	}

	finalX := finalXFromStates(result.Variables)

	// Refresh the merit-blend weights at the final point so the reported
	// active_mode / mode_weights reflect the result, not the last iteration's
	// frozen state. No-op when no schedule is configured.
	opt.UpdateMeritWeights(finalX, result.Iterations)

	// Re-apply final variable values to all configs' surfaces and materialise
	// optimised glasses.
	configSurfaces, newGlasses := opt.FinalConfigs(finalX)

	// Write the optimised virtual entrance pupil back into the chief section
	// (no-op when no pupil model is in use).
	pupilModels := opt.FinalPupilModels(finalX)
	if len(input.Configs) > 0 {
		if pm, ok := pupilModels[input.Configs[0].ID]; ok {
			if input.Chief == nil {
				input.Chief = &types.ChiefInput{}
			}
			pmCopy := pm
			input.Chief.PupilModel = &pmCopy
		}
	}

	// Warn about constraints that could not be satisfied (e.g. unreachable
	// targets). The optimization itself still optimises the objective.
	if violations := opt.FinalConstraintViolations(finalX, 0.1); len(violations) > 0 {
		fmt.Fprintf(os.Stderr, "  WARNING: constraint(s) not satisfied (target may be unreachable):\n")
		for _, v := range violations {
			if v.Config != "" {
				fmt.Fprintf(os.Stderr, "    %s (config %q, kind=%s measure=%s): residual=%.4g\n",
					v.ID, v.Config, v.Kind, v.Measure, v.Residual)
			} else {
				fmt.Fprintf(os.Stderr, "    %s (kind=%s measure=%s): residual=%.4g\n",
					v.ID, v.Kind, v.Measure, v.Residual)
			}
		}
	}

	// Warn about fields whose pupil grid collapsed. A spot-only merit is
	// computed over the surviving rays, so clipping a field away lowers its
	// residual; the warning surfaces the dead corner instead of shipping it
	// silently. Warning only — the merit values are untouched.
	opt.WarnFieldThroughput(finalX)

	// Warn about fixed clear apertures the beam leaves: the same incentive,
	// but the cause is a diameter nothing sizes (auto_aperture off), and
	// naming it saves the "raise every diameter and trim afterwards" detour.
	opt.WarnBeamClipping(finalX)

	// Apply final auto_aperture diameters.
	finalAps := opt.FinalApertures(finalX)
	for cfgID, apMap := range finalAps {
		surfaces, ok := configSurfaces[cfgID]
		if !ok {
			continue
		}
		for i := range surfaces {
			if d, ok := apMap[surfaces[i].ID]; ok {
				surfaces[i].Diameter = d
			}
		}
	}

	// Update input configs with optimized surfaces.
	for i := range input.Configs {
		if cfg, ok := configSurfaces[input.Configs[i].ID]; ok {
			input.Configs[i].Surfaces = cfg
		}
	}

	if input.GlassCatalog == nil {
		input.GlassCatalog = &types.GlassCatalog{}
	}
	for _, g := range newGlasses {
		input.GlassCatalog.Entries = append(input.GlassCatalog.Entries, g)
	}

	output := types.Output{
		Input: input,
	}
	withOutputMetadata(&output.Input, "optimize", subcmdArgs())

	// Report the final measured value of every active constraint (e.g. the
	// vignetting factor) so callers can gate on what the optimizer enforced.
	optResults := &types.OptimizationResult{
		Status:      result.Status,
		Iterations:  result.Iterations,
		Interrupted: interrupted,
		Constraints: opt.FinalConstraintMeasurements(finalX),
	}
	if active, weights, changes, metricVal, effNumRays := opt.MeritScheduleState(); active != "" {
		optResults.ActiveMode = active
		optResults.ModeWeights = weights
		optResults.ModeChanges = changes
		if metricVal != 0 {
			optResults.MetricValue = metricVal
		}
		if effNumRays > 0 {
			optResults.EffectiveNumRays = effNumRays
		}
	}
	if ga := opt.GlassAttractionDiagnostics(finalX); ga != nil {
		optResults.GlassAttraction = ga
	}
	if bf := opt.BackFocusDiagnostics(); bf != nil {
		optResults.BackFocus = bf
	}
	// Report the spot-slope decomposition the spot_flatness /
	// spot_astigmatism terms optimised (per-field focus and astigmatism in
	// mm), so the run's field curvature can be read straight from the result.
	if rows := opt.SpotFlatnessDiagnostics(finalX); len(rows) > 0 {
		optResults.SpotFlatness = rows
	}
	output.OptResults = optResults

	writeYAML(&output)
}

// runOptimizeSnap implements `optimize snap`: it does not run DLS. It reads a
// document (typically the output of `optimize` or `escape extract`), replaces
// every declared nd/vd glass variable with the nearest real catalog glass, and
// reports the change in the (glass-attraction/hull-excluded) optical merit.
func runOptimizeSnap(data []byte, glassDir string) {
	run, ok := buildOptimizeRun(data, glassDir, "", false, "", false, true)
	if !ok {
		return
	}
	input := run.input

	var hull *glass.ConvexHull
	hullMargin, hullWeight := resolveGlassHull(input.Optimization.GlassHull, &hull, run.gc)
	opt := optimize.NewMultiOptimizer(run.configs, run.sharedVars, run.localVars, input.Optimization.VariableLinks, run.gc,
		run.maxIter, run.mu, run.tol, run.epsilon, run.apertureMargin, run.numRays,
		input.Optimization.MuConMax, input.Optimization.JacobianWorkers, nil,
		hull, hullMargin, hullWeight, input.Optimization.CentralDiff, input.Optimization.BFGS,
		input.Optimization.AdaptiveDamping, input.Optimization.RegionActive)
	opt.SetApertureMarginMM(run.apertureMarginMM)

	// Record the field angles the model froze at this start geometry (see
	// freezeFieldReport); snap evaluates the same fields the solve would.
	freezeFieldReport(&input, opt)

	if !opt.HasGlassPairs() {
		errExitf("Error: optimize snap found no declared nd/vd glass variables to snap")
	}

	x := opt.InitialState()
	opt.UpdatePupils(x)
	before := opt.OpticalMerit(x)

	snappedX, pairs := opt.SnapVariables(x)
	if len(pairs) == 0 {
		errExitf("Error: optimize snap could not resolve any catalog glasses")
	}
	opt.UpdatePupils(snappedX)
	after := opt.OpticalMerit(snappedX)

	// Apply the snapped state to the output surfaces.
	configSurfaces, newGlasses := opt.FinalConfigs(snappedX)
	finalAps := opt.FinalApertures(snappedX)
	for cfgID, apMap := range finalAps {
		surfaces, ok := configSurfaces[cfgID]
		if !ok {
			continue
		}
		for i := range surfaces {
			if d, ok := apMap[surfaces[i].ID]; ok {
				surfaces[i].Diameter = d
			}
		}
	}
	for i := range input.Configs {
		if cfg, ok := configSurfaces[input.Configs[i].ID]; ok {
			input.Configs[i].Surfaces = cfg
		}
	}
	if input.GlassCatalog == nil {
		input.GlassCatalog = &types.GlassCatalog{}
	}
	for _, g := range newGlasses {
		input.GlassCatalog.Entries = append(input.GlassCatalog.Entries, g)
	}

	output := types.Output{Input: input}
	withOutputMetadata(&output.Input, "optimize", subcmdArgs())

	cost := after - before
	costPct := 0.0
	if before != 0 {
		costPct = 100 * cost / before
	}
	output.OptResults = &types.OptimizationResult{
		Status: "snapped",
		Snap: &types.SnapResult{
			BeforeMerit: before,
			AfterMerit:  after,
			Cost:        cost,
			CostPct:     costPct,
			Pairs:       pairs,
		},
	}

	// Human-readable summary on stderr.
	fmt.Fprintf(os.Stderr, "=== optimize snap complete ===\n")
	fmt.Fprintf(os.Stderr, "  Before merit: %.6e\n", before)
	fmt.Fprintf(os.Stderr, "  After merit:  %.6e\n", after)
	fmt.Fprintf(os.Stderr, "  Cost:         %+.6e (%.3f%%)\n", cost, costPct)
	for _, p := range pairs {
		fmt.Fprintf(os.Stderr, "  S%d: nd %.5f vd %.3f -> %s (nd %.5f vd %.3f), d=%.4f\n",
			p.SurfaceID, p.FromND, p.FromVD, p.Name, p.ToND, p.ToVD, p.Distance)
	}

	writeYAML(&output)
}

// "default on" rule: the real-glass convex hull is applied unless the user sets
// optimization.glass_hull.enabled: false explicitly. A nil or enabled config
// yields a hull (out is set), with margin/weight falling back to their defaults
// when unset; an explicit disabled config yields no hull (out stays nil). The
// hull only constrains nd/vd glass variables (hullPairs), so a run without
// glass variables is unaffected even with the hull active.
//
// Two penalty-shape knobs are resolved onto the hull itself:
//
//   - guard_band (default 0.01, 0 disables): the penalty aims at a hull shrunk
//     by this fraction of the hull radius, so the DLS balance point rests that
//     far inside the true hull (feasibility stays judged on the true hull).
//   - exploration_weight_factor (default 0.1): scales the whole hull-penalty term
//     during the escape exploration phase so the search can cross the hull
//     barrier; the glass/clean phases keep full strength.
//
// The geometry is selected by glass_hull.source:
//
//   - "union" (default, or empty): the built-in full real-glass hull unioned
//     with every loaded catalogue glass (inline entries and AGF from
//     --glass-dir). The constraint is never tighter than the real-glass region.
//   - "builtin": the built-in full real-glass hull only (catalogue ignored).
//   - "catalog": the loaded catalogue's hull only, falling back to "builtin"
//     when fewer than 3 hull vertices result.
//   - "explicit": the configured (nd, vd) points only, falling back to
//     "builtin" (with a warning) when fewer than 3 valid points are given.
func resolveGlassHull(cfg *types.GlassHullConfig, out **glass.ConvexHull, gc *glass.Catalog) (margin, weight float64) {
	if cfg != nil && !cfg.Enabled {
		return 0, 0
	}
	source := "union"
	if cfg != nil && strings.TrimSpace(cfg.Source) != "" {
		source = strings.ToLower(strings.TrimSpace(cfg.Source))
	}
	switch source {
	case "builtin":
		*out = glass.NewBuiltinConvexHull()
	case "catalog":
		*out = glass.NewDefaultConvexHull(gc)
	case "explicit":
		pts := make([]glass.Point2D, 0, len(cfg.Glasses))
		for _, g := range cfg.Glasses {
			pts = append(pts, glass.Point2D{ND: g.ND, VD: g.VD})
		}
		if h := glass.NewConvexHullFromPoints(pts); h != nil {
			*out = h
		} else {
			errOut("Warning: glass_hull source 'explicit' needs at least 3 valid (nd, vd) glasses; falling back to 'builtin'")
			*out = glass.NewBuiltinConvexHull()
		}
	case "union":
		*out = glass.NewUnionConvexHull(gc)
	default:
		errOut("Warning: unknown glass_hull source %q; using 'union'", cfg.Source)
		*out = glass.NewUnionConvexHull(gc)
	}
	margin = 0.02
	weight = 1.0
	if cfg != nil {
		if cfg.Margin > 0 {
			margin = cfg.Margin
		}
		if cfg.Weight > 0 {
			weight = cfg.Weight
		}
	}
	if *out != nil {
		// Guard band: default on (0.01 of the hull radius); an explicit 0
		// disables it. Escape factor: default 0.1; an explicit value is
		// honored (0 falls back to the same default).
		guard := 0.01
		if cfg != nil && cfg.GuardBand != nil && *cfg.GuardBand >= 0 {
			guard = *cfg.GuardBand
		}
		factor := 0.1
		if cfg != nil && cfg.ExplorationWeightFactor != nil && *cfg.ExplorationWeightFactor > 0 {
			factor = *cfg.ExplorationWeightFactor
		}
		(*out).SetGuardBand(guard)
		(*out).SetExplorationWeightFactor(factor)
	}
	return margin, weight
}

// firstConfigSurfaces returns the surfaces of the first config (used when no
// active config has surfaces, e.g. hand-written single-config YAML that omits
// the configs[] wrapper).
func firstConfigSurfaces(input types.Input) []types.Surface {
	if len(input.Configs) > 0 {
		return input.Configs[0].Surfaces
	}
	return nil
}

func finalXFromStates(states []optimize.VariableState) []float64 {
	x := make([]float64, len(states))
	for i, st := range states {
		x[i] = st.After
	}
	return x
}

func loadFields(input types.Input) []types.FieldItem {
	if len(input.Configs) > 0 {
		if len(input.Configs[0].Fields) > 0 {
			return input.Configs[0].Fields
		}
	}
	if input.Chief != nil {
		var fields []types.FieldItem
		for _, f := range input.Chief.Fields {
			fields = append(fields, types.FieldItem{
				ID:       0,
				AngleDeg: f.Angle,
				Weight:   1.0,
			})
		}
		return fields
	}
	return nil
}

type jsonLogger struct {
	w *os.File
}

func safeF(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

func safeVars(v []float64) []float64 {
	s := make([]float64, len(v))
	for i, x := range v {
		s[i] = safeF(x)
	}
	return s
}

func (j *jsonLogger) LogIter(iter int, merit, improvement, stepNorm float64, variables []float64, constraints []dls.ConstraintState) {
	entry := iterLog{
		Iter:        iter,
		Merit:       safeF(merit),
		Improvement: safeF(improvement),
		StepNorm:    safeF(stepNorm),
		Variables:   safeVars(variables),
	}
	if len(constraints) > 0 {
		entry.Constraints = make([]constraintInfo, len(constraints))
		for i, cs := range constraints {
			entry.Constraints[i] = constraintInfo{Residual: cs.Residual}
		}
	}
	data, err := json.Marshal(entry)
	if err != nil {
		fmt.Fprintf(j.w, "ERR iter=%d: %v\n", iter, err)
		return
	}
	fmt.Fprintln(j.w, string(data))
}

func (j *jsonLogger) LogFinal(iter int, status string, merit float64, stepNorm float64, variables []float64, constraints []dls.ConstraintState) {
	entry := finalLog{
		Iter:      iter,
		Merit:     safeF(merit),
		StepNorm:  safeF(stepNorm),
		Variables: safeVars(variables),
		Status:    status,
	}
	if len(constraints) > 0 {
		entry.Constraints = make([]constraintInfo, len(constraints))
		for i, cs := range constraints {
			entry.Constraints[i] = constraintInfo{Residual: cs.Residual}
		}
	}
	data, err := json.Marshal(entry)
	if err != nil {
		fmt.Fprintf(j.w, "ERR final: %v\n", err)
		return
	}
	fmt.Fprintln(j.w, string(data))
}

func (j *jsonLogger) LogModeChange(iter int, from, to string, weights map[string]float64, metric float64) {
	entry := modeChangeLog{
		Event:   "mode_change",
		Iter:    iter,
		From:    from,
		To:      to,
		Weights: weights,
		Metric:  metric,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		fmt.Fprintf(j.w, "ERR mode_change: %v\n", err)
		return
	}
	fmt.Fprintln(j.w, string(data))
}

func (j *jsonLogger) LogDamping(iter int, mu, ref float64, summary dls.DampingSummary) {
	entry := dampingLog{
		Event:   "adaptive_damping",
		Iter:    iter,
		Mu:      safeF(mu),
		Ref:     safeF(ref),
		Classes: summary.Classes,
		DMin:    safeF(summary.DMin),
		DMax:    safeF(summary.DMax),
		DMean:   safeF(summary.DMean),
	}
	data, err := json.Marshal(entry)
	if err != nil {
		fmt.Fprintf(j.w, "ERR damping: %v\n", err)
		return
	}
	fmt.Fprintln(j.w, string(data))
}

type multiLogger struct {
	loggers []dls.Logger
}

func (m *multiLogger) LogIter(iter int, merit, improvement, stepNorm float64, variables []float64, constraints []dls.ConstraintState) {
	for _, l := range m.loggers {
		l.LogIter(iter, merit, improvement, stepNorm, variables, constraints)
	}
}

func (m *multiLogger) LogFinal(iter int, status string, merit float64, stepNorm float64, variables []float64, constraints []dls.ConstraintState) {
	for _, l := range m.loggers {
		l.LogFinal(iter, status, merit, stepNorm, variables, constraints)
	}
}

func (m *multiLogger) LogModeChange(iter int, from, to string, weights map[string]float64, metric float64) {
	for _, l := range m.loggers {
		if ml, ok := l.(dls.ModeChangeLogger); ok {
			ml.LogModeChange(iter, from, to, weights, metric)
		}
	}
}

func (m *multiLogger) LogDamping(iter int, mu, ref float64, summary dls.DampingSummary) {
	for _, l := range m.loggers {
		if dl, ok := l.(dls.DampingLogger); ok {
			dl.LogDamping(iter, mu, ref, summary)
		}
	}
}

type constraintInfo struct {
	Residual float64 `json:"residual"`
}

type modeChangeLog struct {
	Event   string             `json:"event"`
	Iter    int                `json:"iter"`
	From    string             `json:"from"`
	To      string             `json:"to"`
	Weights map[string]float64 `json:"weights"`
	Metric  float64            `json:"metric,omitempty"`
}

type dampingLog struct {
	Event   string                           `json:"event"`
	Iter    int                              `json:"iteration"`
	Mu      float64                          `json:"mu"`
	Ref     float64                          `json:"sensitivity_reference"`
	Classes map[string]dls.DampingClassStats `json:"classes"`
	DMin    float64                          `json:"d_min"`
	DMax    float64                          `json:"d_max"`
	DMean   float64                          `json:"d_mean"`
}

type iterLog struct {
	Iter        int              `json:"iter"`
	Merit       float64          `json:"merit"`
	Improvement float64          `json:"improvement"`
	StepNorm    float64          `json:"step_norm"`
	Constraints []constraintInfo `json:"constraints,omitempty"`
	Variables   []float64        `json:"variables"`
}

type finalLog struct {
	Iter        int              `json:"iter"`
	Merit       float64          `json:"merit"`
	StepNorm    float64          `json:"step_norm"`
	Constraints []constraintInfo `json:"constraints,omitempty"`
	Variables   []float64        `json:"variables"`
	Status      string           `json:"status"`
}
