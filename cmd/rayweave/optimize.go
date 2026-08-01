package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"

	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/multiopt"
	"github.com/hiroki/rayweaver/internal/optimize"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
	"gopkg.in/yaml.v3"
)

const (
	defaultMaxIter = 100
	defaultMu      = 1.0
	defaultTol     = 1e-6
	defaultEpsilon = 1e-6
	defaultNumRays = 64
)

func runOptimize(data []byte, verbose bool, logFile string, glassDir string) {
	var input types.Input
	if err := yaml.Unmarshal(data, &input); err != nil {
		errOut("Error parsing YAML: %v", err)
		os.Exit(1)
	}

	if input.Optimization == nil {
		errOut("Error: 'optimization' section is required")
		os.Exit(1)
	}

	gc, _ := loadCatalogs(&input, glassDir)

	// Detect multi-config mode: shared_variables exist, or multiple configs with merits
	isMultiConfig := len(input.Optimization.SharedVariables) > 0 || len(input.Optimization.LocalVariables) > 0
	if !isMultiConfig {
		for _, cfg := range input.Configs {
			if cfg.Merit != nil {
				isMultiConfig = true
				break
			}
		}
	}

	if isMultiConfig && len(input.Configs) > 1 {
		runMultiConfigOptimize(input, gc, verbose, logFile)
		return
	}

	// Single-config: use configs[0].surfaces if available, otherwise system.surfaces
	surfaces := input.System.Surfaces
	if len(input.Configs) > 0 && len(input.Configs[0].Surfaces) > 0 {
		surfaces = input.Configs[0].Surfaces
	}
	if len(surfaces) == 0 {
		errOut("Error: no surfaces defined (add 'system.surfaces' or 'configs[0].surfaces')")
		os.Exit(1)
	}
	surface.Precompute(surfaces)

	variables := buildOptimizeVariables(input.Optimization, gc)
	if len(variables) == 0 {
		errOut("Error: no optimization variables defined")
		os.Exit(1)
	}

	meritTerms := buildMeritTerms(input)

	if len(meritTerms) == 0 {
		errOut("Error: no merit terms defined (add 'optimization.merit' or 'configs[].merit')")
		os.Exit(1)
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

	fields := loadFields(input)

	constraints := input.Optimization.Constraints
	if len(input.Configs) > 0 && len(input.Configs[0].Constraints) > 0 {
		constraints = input.Configs[0].Constraints
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

	stopSurface := 0
	if input.Chief != nil {
		stopSurface = input.Chief.StopSurface
	}

	cfg := optimize.Config{
		Surfaces:       surfaces,
		Variables:      variables,
		MeritTerms:     meritTerms,
		Fields:         fields,
		Constraints:    constraints,
		GlassCatalog:   gc,
		StopSurface:    stopSurface,
		Logger:         logger,
		MaxIter:        input.Optimization.MaxIter,
		Tol:            input.Optimization.Tol,
		Epsilon:        input.Optimization.Epsilon,
		NumRays:        input.Optimization.NumRays,
		ApertureMargin: apertureMargin,
		MuConMax:       input.Optimization.MuConMax,
	}

	if input.Optimization.GlassHull != nil && input.Optimization.GlassHull.Enabled {
		cfg.Hull = glass.NewDefaultConvexHull()
		cfg.HullMargin = input.Optimization.GlassHull.Margin
		cfg.HullWeight = input.Optimization.GlassHull.Weight
		if cfg.HullMargin <= 0 {
			cfg.HullMargin = 0.02
		}
		if cfg.HullWeight <= 0 {
			cfg.HullWeight = 1.0
		}
	}

	opt := optimize.NewOptimizer(cfg)
	result := opt.Optimize()

	// Emit a per-term merit breakdown so the reported merit value can be
	// reconciled against an external evaluation (e.g. `chief` spot RMS).
	if len(logWriters) > 0 || verbose {
		xFinal := make([]float64, len(result.Variables))
		for i, vs := range result.Variables {
			xFinal[i] = vs.After
		}
		bd := opt.MeritBreakdown(xFinal)
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

	fmt.Fprintf(os.Stderr, "=== Optimization complete ===\n")
	fmt.Fprintf(os.Stderr, "  Status:      %s\n", result.Status)
	fmt.Fprintf(os.Stderr, "  Iterations:  %d\n", result.Iterations)
	fmt.Fprintf(os.Stderr, "  Before:      %.6e\n", result.BeforeMerit)
	fmt.Fprintf(os.Stderr, "  After:       %.6e\n", result.AfterMerit)
	if result.BeforeMerit > 0 {
		improvement := (result.BeforeMerit - result.AfterMerit) / result.BeforeMerit * 100
		fmt.Fprintf(os.Stderr, "  Improvement: %.2f%%\n", improvement)
	}

	outputSurfaces, newGlasses := applyVariableStates(surfaces, result.Variables, gc)

	xFinal := make([]float64, len(result.Variables))
	for i, vs := range result.Variables {
		xFinal[i] = vs.After
	}

	// Warn about constraints that could not be satisfied (e.g. unreachable
	// targets). The optimization itself still optimises the objective.
	if violations := opt.FinalConstraintViolations(xFinal, 0.1); len(violations) > 0 {
		fmt.Fprintf(os.Stderr, "  WARNING: constraint(s) not satisfied (target may be unreachable):\n")
		for _, v := range violations {
			fmt.Fprintf(os.Stderr, "    %s (kind=%s measure=%s): residual=%.4g\n",
				v.ID, v.Kind, v.Measure, v.Residual)
		}
	}

	finalAps := opt.FinalApertures(xFinal)
	for i := range outputSurfaces {
		if d, ok := finalAps[outputSurfaces[i].ID]; ok {
			outputSurfaces[i].Diameter = d
		}
	}

	// Surface data lives in configs[].surfaces; system.surfaces is a read-only
	// compatibility fallback and is never written.
	if len(input.Configs) == 0 {
		input.Configs = []types.Config{{
			ID:     "config1",
			Name:   "Config1",
			Weight: 1.0,
			Active: true,
		}}
	}
	input.Configs[0].Surfaces = outputSurfaces
	input.System.Surfaces = nil

	for _, g := range newGlasses {
		input.GlassCatalog.Entries = append(input.GlassCatalog.Entries, g)
	}

	output := types.Output{
		Input: input,
	}

	outData, err := yaml.Marshal(&output)
	if err != nil {
		errOut("Error marshaling output: %v", err)
		os.Exit(1)
	}
	os.Stdout.Write(outData)
}

func buildOptimizeVariables(opt *types.OptimizationConfig, gc *glass.Catalog) []optimize.Variable {
	var variables []optimize.Variable
	for _, v := range opt.Variables {
		if !v.Active {
			continue
		}

		switch v.Target.Type {
		case "surface":
			var min, max float64 = v.Min, v.Max
			if min == 0 && max == 0 {
				switch v.Target.Param {
				case "curvature":
					min, max = -0.5, 0.5
				case "conic":
					min, max = -1.0, 1.0
				case "a4", "coefficient_0":
					min, max = -1e-2, 1e-2
				case "a6", "coefficient_1":
					min, max = -1e-3, 1e-3
				case "a8", "coefficient_2":
					min, max = -1e-4, 1e-4
				case "a10", "coefficient_3", "a12", "coefficient_4":
					min, max = -1e-5, 1e-5
				case "thickness":
					min, max = 0.1, 50.0
				case "nd":
					min, max = 1.4, 1.9
				case "vd":
					min, max = 20.0, 80.0
				}
			}
			variables = append(variables, optimize.Variable{
				Name:      v.Name,
				SurfaceID: v.Target.ID,
				Param:     v.Target.Param,
				Min:       min,
				Max:       max,
			})
		default:
			continue
		}
	}
	return variables
}

func buildMeritTerms(input types.Input) []optimize.MeritTerm {
	var terms []optimize.MeritTerm

	if len(input.Configs) > 0 {
		cfg := input.Configs[0]
		if cfg.Merit != nil {
			for _, mt := range cfg.Merit.Terms {
				kind := mt.Kind
				if kind == "" {
					kind = "spot_rms"
				}

				var fieldAngle, fieldWeight float64
				for _, f := range cfg.Fields {
					if f.ID == mt.Field {
						fieldAngle = f.AngleDeg
						fieldWeight = f.Weight
						break
					}
				}
				if fieldWeight == 0 {
					fieldWeight = 1.0
				}

				var wavWeight float64
				for _, w := range cfg.Wavelengths {
					if math.Abs(w.Value-mt.Wavelength) < 1e-12 {
						wavWeight = w.Weight
						break
					}
				}
				if wavWeight == 0 {
					wavWeight = 1.0
				}

				terms = append(terms, optimize.MeritTerm{
					Kind:        kind,
					FieldAngle:  fieldAngle,
					FieldDir:    []float64{0, 1},
					FieldWeight: fieldWeight,
					Wavelength:  mt.Wavelength,
					Wavelength2: mt.Wavelength2,
					WavWeight:   wavWeight,
					Weight:      mt.Weight,
					Target:      mt.Target,
				})
			}
		}
	}

	return terms
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

func applyVariableStates(surfaces []types.Surface, states []optimize.VariableState, gc *glass.Catalog) ([]types.Surface, []types.Glass) {
	result := make([]types.Surface, len(surfaces))
	copy(result, surfaces)

	for _, st := range states {
		switch st.Param {
		case "curvature", "thickness":
			for i := range result {
				if result[i].ID == st.SurfaceID {
					switch st.Param {
					case "curvature":
						result[i].Curvature = st.After
					case "thickness":
						result[i].Thickness = st.After
					}
					break
				}
			}
		}
	}

	surface.Precompute(result)

	type glassAccum struct {
		nd, vd       float64
		hasND, hasVD bool
		origLabel    string
	}
	glassMap := map[string]*glassAccum{}
	for _, st := range states {
		if st.Param != "nd" && st.Param != "vd" || st.GlassName == "" {
			continue
		}
		acc, ok := glassMap[st.GlassName]
		if !ok {
			acc = &glassAccum{}
			if gc != nil {
				if g, ok2 := gc.Lookup(st.GlassName); ok2 {
					acc.origLabel = g.Label
					acc.nd = g.ND
					acc.vd = g.VD
					acc.hasND = true
					acc.hasVD = true
				}
			}
			glassMap[st.GlassName] = acc
		}
		switch st.Param {
		case "nd":
			acc.nd = st.After
			acc.hasND = true
		case "vd":
			acc.vd = st.After
			acc.hasVD = true
		}
	}

	var newGlasses []types.Glass
	for origKey, acc := range glassMap {
		if !acc.hasND || !acc.hasVD {
			continue
		}
		g := types.Glass{
			Type: types.GlassTypeModel,
			ND:   acc.nd,
			VD:   acc.vd,
		}
		if acc.origLabel != "" {
			g.Label = acc.origLabel
		}
		newKey := types.ResolveGlassKey(g)
		newGlasses = append(newGlasses, g)

		for i := range result {
			if result[i].Material == origKey {
				result[i].Material = newKey
			}
		}
	}

	return result, newGlasses
}

func runMultiConfigOptimize(input types.Input, gc *glass.Catalog, verbose bool, logFile string) {
	var configs []multiopt.ConfigInput
	for _, cfg := range input.Configs {
		if !cfg.Active {
			continue
		}
		// Note: configs[].ray_paths is intentionally ignored here. Ray paths
		// describe the object→stop→image ordering for rendering/plotting only
		// (see internal/render); the optimizer determines the aperture from the
		// surface diameters and its own chief-ray grid.
		surfaces := cfg.Surfaces
		if len(surfaces) == 0 {
			errOut("Error: config %q has no surfaces defined", cfg.ID)
			os.Exit(1)
		}

		var fields []types.FieldItem
		for _, f := range cfg.Fields {
			fields = append(fields, f)
		}
		if len(fields) == 0 && len(input.Chief.Fields) > 0 {
			for _, f := range input.Chief.Fields {
				fields = append(fields, types.FieldItem{
					ID:       0,
					AngleDeg: f.Angle,
					Weight:   1.0,
				})
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

	configs = append(configs, multiopt.ConfigInput{
		ID:          cfg.ID,
		Weight:      cfg.Weight,
		StopSurface: stopSurface,
			Surfaces:    surfaces,
			Fields:      fields,
			Wavelengths: wavelengths,
			MeritTerms:  meritTerms,
			Constraints: constraints,
		})
	}

	if len(configs) == 0 {
		errOut("Error: no active configs found")
		os.Exit(1)
	}

	sharedVars := input.Optimization.SharedVariables
	localVars := input.Optimization.LocalVariables

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
	apertureMargin := input.Optimization.ApertureMargin
	if apertureMargin <= 0 {
		apertureMargin = 1.0
	}
	// aperture_margin < 1.0 makes the pupil grid smaller than the aperture,
	// which clips rays at surface edges and stalls DLS convergence. Clamp it.
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
		logger = &jsonMultiLogger{w: os.Stderr}
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
			logger = &jsonMultiLogger{w: f}
		} else {
			logger = &multiMultiLogger{loggers: []dls.Logger{logger, &jsonMultiLogger{w: f}}}
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

	opt := multiopt.New(configs, sharedVars, localVars, gc, maxIter, mu, tol, epsilon, apertureMargin, numRays, input.Optimization.MuConMax, logger, hull, hullMargin, hullWeight)
	result := opt.Optimize()

	// Emit a per-term merit breakdown so the reported merit value can be
	// reconciled against an external evaluation (e.g. `chief` spot RMS).
	if len(logWriters) > 0 || verbose {
		finalX := getFinalX(opt, result.Variables)
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

	fmt.Fprintf(os.Stderr, "=== Multi-Config Optimization complete ===\n")
	fmt.Fprintf(os.Stderr, "  Status:      %s\n", result.Status)
	fmt.Fprintf(os.Stderr, "  Iterations:  %d\n", result.Iterations)
	fmt.Fprintf(os.Stderr, "  Before:      %.6e\n", result.BeforeMerit)
	fmt.Fprintf(os.Stderr, "  After:       %.6e\n", result.AfterMerit)
	if result.BeforeMerit > 0 {
		improvement := (result.BeforeMerit - result.AfterMerit) / result.BeforeMerit * 100
		fmt.Fprintf(os.Stderr, "  Improvement: %.2f%%\n", improvement)
	}

	// Re-apply final variable values to all configs' surfaces
	finalX := getFinalX(opt, result.Variables)
	configSurfaces := applyMultiVars(input, finalX, gc)

	// Warn about constraints that could not be satisfied (e.g. unreachable
	// targets). The optimization itself still optimises the objective.
	if violations := opt.FinalConstraintViolations(finalX, 0.1); len(violations) > 0 {
		fmt.Fprintf(os.Stderr, "  WARNING: constraint(s) not satisfied (target may be unreachable):\n")
		for _, v := range violations {
			fmt.Fprintf(os.Stderr, "    %s (config %q, kind=%s measure=%s): residual=%.4g\n",
				v.ID, v.Config, v.Kind, v.Measure, v.Residual)
		}
	}

	// Apply final auto_aperture diameters
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

	// Update input configs with optimized surfaces
	for i := range input.Configs {
		if cfg, ok := configSurfaces[input.Configs[i].ID]; ok {
			input.Configs[i].Surfaces = cfg
		}
	}

	output := types.Output{
		Input: input,
	}

	outData, err := yaml.Marshal(&output)
	if err != nil {
		errOut("Error marshaling output: %v", err)
		os.Exit(1)
	}
	os.Stdout.Write(outData)
}

func applyMultiVars(input types.Input, x []float64, gc *glass.Catalog) map[string][]types.Surface {
	result := make(map[string][]types.Surface)

	// First pass: copy all config surfaces into result (one copy per config)
	for ci := range input.Configs {
		cfg := &input.Configs[ci]
		if len(cfg.Surfaces) == 0 {
			errOut("Error: config %q has no surfaces defined", cfg.ID)
			os.Exit(1)
		}
		s := make([]types.Surface, len(cfg.Surfaces))
		copy(s, cfg.Surfaces)
		result[cfg.ID] = s
	}

	// Second pass: apply all shared variable bindings in-place
	var varIdx int
	for _, sv := range input.Optimization.SharedVariables {
		if !sv.Active {
			continue
		}
		val := 0.0
		if varIdx < len(x) {
			val = x[varIdx]
		}
		varIdx++

		for _, b := range sv.Bindings {
			scale := b.Scale
			if scale == 0 {
				scale = 1.0
			}
			applied := scale*val + b.Offset

			surfaces, ok := result[b.Config]
			if !ok {
				continue
			}
			for i := range surfaces {
				if surfaces[i].ID == b.ID {
					setSurfaceParam(&surfaces[i], b.Param, applied)
					break
				}
			}
		}
	}

	// Third pass: apply all local variables in-place
	for _, lv := range input.Optimization.LocalVariables {
		if !lv.Active {
			continue
		}
		val := 0.0
		if varIdx < len(x) {
			val = x[varIdx]
		}
		varIdx++

		surfaces, ok := result[lv.Config]
		if !ok {
			continue
		}
		for i := range surfaces {
			if surfaces[i].ID == lv.Target.ID {
				setSurfaceParam(&surfaces[i], lv.Target.Param, val)
				break
			}
		}
	}

	// Fourth pass: precompute all surfaces
	for _, cfg := range input.Configs {
		if s, ok := result[cfg.ID]; ok {
			surface.Precompute(s)
		}
	}

	return result
}

func setSurfaceParam(s *types.Surface, param string, val float64) {
	switch param {
	case "curvature":
		s.Curvature = val
	case "thickness":
		s.Thickness = val
	case "diameter":
		s.Diameter = val
	case "radius":
		if val == 0 {
			s.Curvature = 0
		} else {
			s.Curvature = 1.0 / val
		}
	}
}

func getFinalX(opt *multiopt.MultiOptimizer, states []multiopt.VariableState) []float64 {
	// We need the final x from the optimizer's internal state.
	// For now, reconstruct from the VariableState After values in order.
	var x []float64
	for _, st := range states {
		x = append(x, st.After)
	}
	return x
}

type jsonMultiLogger struct {
	w *os.File
}

func (j *jsonMultiLogger) LogIter(iter int, merit, improvement, stepNorm float64, variables []float64, constraints []dls.ConstraintState) {
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

func (j *jsonMultiLogger) LogFinal(iter int, status string, merit float64, stepNorm float64, variables []float64, constraints []dls.ConstraintState) {
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

type multiMultiLogger struct {
	loggers []dls.Logger
}

func (m *multiMultiLogger) LogIter(iter int, merit, improvement, stepNorm float64, variables []float64, constraints []dls.ConstraintState) {
	for _, l := range m.loggers {
		l.LogIter(iter, merit, improvement, stepNorm, variables, constraints)
	}
}

func (m *multiMultiLogger) LogFinal(iter int, status string, merit float64, stepNorm float64, variables []float64, constraints []dls.ConstraintState) {
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
