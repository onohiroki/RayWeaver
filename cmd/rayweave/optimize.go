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
	"github.com/hiroki/rayweaver/internal/types"
)

const (
	defaultMaxIter = 100
	defaultMu      = 1.0
	defaultTol     = 1e-6
	defaultEpsilon = 1e-6
	defaultNumRays = 64
)

func runOptimize(data []byte, verbose bool, logFile string, glassDir string, excludeParams string) {
	input := parseYAML[types.Input](data)

	if input.Optimization == nil {
		errOut("Error: 'optimization' section is required")
		os.Exit(1)
	}

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

	gc, _ := loadCatalogs(&input, glassDir)
	writeBackGlassDir(&input, glassDir)

	// Build the per-config optimisation inputs (shared/local variables are
	// read below; the unified Optimizer drives all configs together).
	var configs []optimize.ConfigInput
	for _, cfg := range input.Configs {
		if !cfg.Active {
			continue
		}
		surfaces := cfg.Surfaces
		if len(surfaces) == 0 {
			errOut("Error: config %q has no surfaces defined", cfg.ID)
			os.Exit(1)
		}

		var fields []types.FieldItem
		for _, f := range cfg.Fields {
			fields = append(fields, f)
		}
		if len(fields) == 0 && input.Chief != nil {
			for _, f := range input.Chief.Fields {
				fields = append(fields, types.FieldItem{ID: 0, AngleDeg: f.Angle, Weight: 1.0})
			}
		}

		var wavelengths []types.WavelengthItem
		for _, w := range cfg.Wavelengths {
			wavelengths = append(wavelengths, w)
		}

		var meritTerms []types.MeritTerm
		if cfg.Merit != nil {
			for _, mt := range cfg.Merit.Terms {
				meritTerms = append(meritTerms, mt)
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
			ID:          cfg.ID,
			Weight:      cfg.Weight,
			StopSurface: stopSurface,
			RefSurface:  chiefRefSurface(input),
			PupilZ:      computePupilZ(input, surfaces, gc),
			Surfaces:    surfaces,
			Fields:      fields,
			Wavelengths: wavelengths,
			MeritTerms:  meritTerms,
			Constraints: constraints,
		})
	}

	// Single-config YAML may omit the configs[] wrapper entirely or use a
	// default config with weight 1.0; fall back to a synthetic config.
	if len(configs) == 0 {
		cfgSurfaces := firstConfigSurfaces(input)
		configs = []optimize.ConfigInput{{
			ID:          "config1",
			Weight:      1.0,
			StopSurface: 0,
			RefSurface:  chiefRefSurface(input),
			PupilZ:      computePupilZ(input, cfgSurfaces, gc),
			Surfaces:    cfgSurfaces,
			Fields:      loadFields(input),
			Constraints: input.Optimization.Constraints,
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
			case "surface":
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
		errOut("Error: no optimization variables defined")
		os.Exit(1)
	}

	if len(configs[0].MeritTerms) == 0 {
		errOut("Error: no merit terms defined (add 'optimization.merit' or 'configs[].merit')")
		os.Exit(1)
	}

	maxIter := input.Optimization.MaxIter
	if maxIter <= 0 {
		maxIter = defaultMaxIter
	}
	tol := input.Optimization.Tol
	if tol <= 0 {
		tol = defaultTol
	}
	epsilon := input.Optimization.Epsilon
	if epsilon <= 0 {
		epsilon = defaultEpsilon
	}
	mu := input.Optimization.Mu
	if mu <= 0 {
		mu = defaultMu
	}
	numRays := input.Optimization.NumRays
	if numRays <= 0 {
		numRays = defaultNumRays
	}
	// aperture_margin < 1.0 makes the pupil grid smaller than the aperture,
	// which clips rays at surface edges and stalls DLS convergence. Clamp it.
	apertureMargin := input.Optimization.ApertureMargin
	if apertureMargin <= 0 {
		apertureMargin = 1.0
	}
	if apertureMargin < 1.0 {
		errOut("Warning: aperture_margin %.3f < 1.0 is not recommended (pupil grid smaller than the aperture stalls DLS); clamping to 1.0", apertureMargin)
		apertureMargin = 1.0
	}

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
			errOut("Error creating log file: %v", err)
			os.Exit(1)
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
	hullMargin := 0.02
	hullWeight := 1.0
	if input.Optimization.GlassHull != nil && input.Optimization.GlassHull.Enabled {
		hull = glass.NewDefaultConvexHull()
		if input.Optimization.GlassHull.Margin > 0 {
			hullMargin = input.Optimization.GlassHull.Margin
		}
		if input.Optimization.GlassHull.Weight > 0 {
			hullWeight = input.Optimization.GlassHull.Weight
		}
	}

	opt := optimize.NewMultiOptimizer(configs, sharedVars, localVars, gc, maxIter, mu, tol, epsilon, apertureMargin, numRays, input.Optimization.MuConMax, input.Optimization.JacobianWorkers, logger, hull, hullMargin, hullWeight)

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
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		fmt.Fprintf(os.Stderr, "optimize: %v received — stopping at the current DLS iteration and writing the best result found so far (press Ctrl-C again to force quit)\n", sig)
		close(stop)
		<-sigCh
		fmt.Fprintln(os.Stderr, "optimize: force quit")
		os.Exit(1)
	}()

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
	fmt.Fprintf(os.Stderr, "  Before:      %.6e\n", result.BeforeMerit)
	fmt.Fprintf(os.Stderr, "  After:       %.6e\n", result.AfterMerit)
	if result.BeforeMerit > 0 {
		improvement := (result.BeforeMerit - result.AfterMerit) / result.BeforeMerit * 100
		fmt.Fprintf(os.Stderr, "  Improvement: %.2f%%\n", improvement)
	}

	finalX := finalXFromStates(result.Variables)

	// Re-apply final variable values to all configs' surfaces and materialise
	// optimised glasses.
	configSurfaces, newGlasses := opt.FinalConfigs(finalX)

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

	// Report the final measured value of every active constraint (e.g. the
	// vignetting factor) so callers can gate on what the optimizer enforced.
	output.OptResults = &types.OptimizationResult{
		Status:      result.Status,
		Iterations:  result.Iterations,
		Interrupted: interrupted,
		Constraints: opt.FinalConstraintMeasurements(finalX),
	}

	writeYAML(&output)
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

type constraintInfo struct {
	Residual float64 `json:"residual"`
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
