package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/escape"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/optimize"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// runEscape runs the escape-function global optimisation and writes the best
// solution (pipeline-compatible) plus all discovered local minima in the
// escape_result section. When verbose is true, progress events (local minima,
// escape-parameter changes) are reported to stderr as compact abbreviated
// JSONL; --log FILE writes the full JSONL stream to a file. When saveBase is
// non-empty, each discovered minimum is written to saveBase0.yaml,
// saveBase1.yaml, ... (see escapeFileSaver). SIGINT/SIGTERM stops the search
// in three escalating stages (graceful cycle boundary → mid-DLS interrupt →
// force quit), each producing interrupted: true and exit 0 except the last.
func runEscape(data []byte, glassDir string, verbose bool, logFile string, saveBase string) {
	input := parseYAML[types.Input](data)
	if input.Optimization == nil {
		errOut("Error: 'optimization' section is required")
		os.Exit(1)
	}
	if input.Optimization.Escape == nil {
		errOut("Error: 'optimization.escape' section is required")
		os.Exit(1)
	}

	gc, _ := loadCatalogs(&input, glassDir)
	writeBackGlassDir(&input, glassDir)

	progress := escape.NewProgress()
	var logFiles []*os.File
	if verbose {
		progress.AddCompactWriter(os.Stderr)
	}
	if logFile != "" {
		f, err := os.Create(logFile)
		if err != nil {
			errOut("Error creating log file: %v", err)
			os.Exit(1)
		}
		logFiles = append(logFiles, f)
		progress.AddWriter(f)
	}
	defer func() {
		for _, f := range logFiles {
			f.Close()
		}
	}()

	// Three-stage stop on SIGINT/SIGTERM.
	//
	//  1st signal: graceful stop. Cancels the shared context; workers stop at
	//     the next cycle boundary once the running DLS solve completes.
	//     Everything discovered so far is saved and the stdout YAML is still
	//     written (interrupted: true).
	//  2nd signal: hard stop. Closes the mid-solve interrupt channel so the
	//     running DLS aborts within one iteration (best point so far preserved
	//     and saved); the run still completes normally (interrupted: true).
	//  3rd signal: force quit immediately (exit 1).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hardStop := make(chan struct{})
	sigCh := make(chan os.Signal, 3)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		fmt.Fprintf(os.Stderr, "escape: %v received — stopping after the current DLS run and saving results (press Ctrl-C again to interrupt the running DLS)\n", sig)
		progress.Event("interrupt", map[string]any{"signal": sig.String()})
		cancel()
		sig = <-sigCh
		fmt.Fprintf(os.Stderr, "escape: %v received — interrupting the running DLS within the next iteration and saving results (press Ctrl-C again to force quit)\n", sig)
		progress.Event("interrupt_dls", map[string]any{"signal": sig.String()})
		close(hardStop)
		<-sigCh
		fmt.Fprintln(os.Stderr, "escape: force quit")
		os.Exit(1)
	}()

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
		runEscapeMulti(input, gc, progress, saveBase, ctx, hardStop)
		return
	}
	runEscapeSingle(input, gc, progress, saveBase, ctx, hardStop)
}

func runEscapeSingle(input types.Input, gc *glass.Catalog, progress *escape.Progress, saveBase string, ctx context.Context, hardStop <-chan struct{}) {
	var surfaces []types.Surface
	if len(input.Configs) > 0 {
		surfaces = input.Configs[0].Surfaces
	}
	if len(surfaces) == 0 {
		errOut("Error: no surfaces defined (add 'configs[0].surfaces')")
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

	fields := loadFields(input)
	constraints := input.Optimization.Constraints
	if len(input.Configs) > 0 && len(input.Configs[0].Constraints) > 0 {
		constraints = input.Configs[0].Constraints
	}

	apertureMargin := input.Optimization.ApertureMargin
	if apertureMargin <= 0 {
		apertureMargin = 1.0
	}
	if apertureMargin < 1.0 {
		apertureMargin = 1.0
	}
	apertureMarginMM := input.Optimization.ApertureMarginMM
	if apertureMarginMM <= 0 {
		apertureMarginMM = 0.2
	}

	stopSurface := 0
	if input.Chief != nil {
		stopSurface = input.Chief.StopSurface
	}

	workers := input.Optimization.JacobianWorkers
	if workers <= 0 {
		workers = 2
	}

	cfg := optimize.Config{
		Surfaces:         surfaces,
		Variables:        variables,
		MeritTerms:       meritTerms,
		Fields:           fields,
		Constraints:      constraints,
		GlassCatalog:     gc,
		StopSurface:      stopSurface,
		RefSurface:       chiefRefSurface(input),
		PupilZ:           computePupilZ(input, surfaces, gc),
		MaxIter:          input.Optimization.MaxIter,
		Tol:              input.Optimization.Tol,
		Epsilon:          input.Optimization.Epsilon,
		NumRays:          input.Optimization.NumRays,
		ApertureMargin:   apertureMargin,
		ApertureMarginMM: apertureMarginMM,
		MuConMax:         input.Optimization.MuConMax,
		Workers:          workers,
	}
	if dg := input.Optimization.Degenerate; dg != nil {
		cfg.SpotDegenerate = dg.SpotValue
		cfg.OPDDegenerate = dg.OPDValue
		cfg.WavefrontDegenerate = dg.WavefrontValue
	}
	cfg.HullMargin, cfg.HullWeight = resolveGlassHull(input.Optimization.GlassHull, &cfg.Hull)
	if cfg.Hull != nil {
		if cfg.HullMargin <= 0 {
			cfg.HullMargin = 0.02
		}
		if cfg.HullWeight <= 0 {
			cfg.HullWeight = 1.0
		}
	}

	// Each worker builds an isolated Optimizer so concurrent merit
	// evaluations never share mutable glass-override state.
	factory := func() dls.Model {
		cfgCopy := cfg
		cfgCopy.Variables = append([]optimize.Variable{}, cfg.Variables...)
		cfgCopy.Surfaces = append([]types.Surface{}, cfg.Surfaces...)
		return optimize.NewOptimizer(cfgCopy)
	}

	var onRecord escape.RecordHandler
	var saver *escapeFileSaver
	if saveBase != "" {
		saver = newEscapeFileSaver(saveBase, func(p escape.Point) types.Input {
			return materializeSingleInput(input, surfaces, variables, p.X, gc)
		})
		onRecord = saver.record
	}

	// Design fingerprint for the distinct-minimum criterion: the thin-lens
	// element powers at a variable vector. When fingerprint_distance_threshold
	// is set, two candidates close in variable space are still distinct minima
	// when their element-power fingerprints differ.
	fingerprint := func(x []float64) []float64 {
		surf, _ := applyEscapeX(surfaces, variables, x, gc)
		return paraxial.ElementPowers(surf, paraxial.DLine, gc)
	}

	res := escape.ParallelEscape(factory, *input.Optimization.Escape, escape.RunOptions{
		Progress:    progress,
		OnRecord:    onRecord,
		Fingerprint: fingerprint,
		Context:     ctx,
		HardStop:    hardStop,
	})
	progress.Event("done", map[string]any{
		"workers":     res.Workers,
		"cycles":      res.Cycles,
		"escapes":     res.Escapes,
		"minima":      len(res.Minima),
		"best_merit":  safeF(res.BestMerit),
		"timed_out":   res.TimedOut,
		"interrupted": res.Interrupted,
	})
	if saver != nil && saver.err != nil {
		errOut("escape: error saving minima: %v", saver.err)
	}

	// Build the escape_result minima against the pristine original surfaces.
	cfgID := "config1"
	if len(input.Configs) > 0 && input.Configs[0].ID != "" {
		cfgID = input.Configs[0].ID
	}

	var saveStem, saveExt string
	if saveBase != "" {
		saveStem, saveExt = splitSaveBase(saveBase)
	}

	minima := make([]types.EscapeMinimum, len(res.Minima))
	for i, p := range res.Minima {
		surf, newGlasses := applyEscapeX(surfaces, variables, p.X, gc)
		// Register the optimised nd/vd model glasses so the element powers use
		// the values at this minimum, not the original catalogue entries.
		for _, g := range newGlasses {
			gc.Add(g)
		}
		minima[i] = types.EscapeMinimum{
			Index:     i,
			Merit:     p.Merit,
			Surfaces:  surf,
			Variables: buildSingleVarStates(variables, p.X),
			Features: []types.ConfigFeatures{{
				ID:            cfgID,
				ElementPowers: paraxial.ElementPowers(surf, paraxial.DLine, gc),
			}},
		}
		if saveBase != "" {
			minima[i].File = fmt.Sprintf("%s%d%s", saveStem, res.MinimaIdx[i], saveExt)
		}
	}

	// Apply the best solution to the top-level surfaces (pipeline-compatible).
	bestSurfaces := make([]types.Surface, len(surfaces))
	copy(bestSurfaces, surfaces)
	var bestGlasses []types.Glass
	if len(res.Minima) > 0 {
		bestSurfaces, bestGlasses = applyEscapeX(surfaces, variables, res.Minima[res.BestIdx].X, gc)
	}

	if len(input.Configs) == 0 {
		input.Configs = []types.Config{{
			ID:     "config1",
			Name:   "Config1",
			Weight: 1.0,
			Active: true,
		}}
	}
	input.Configs[0].Surfaces = bestSurfaces

	for _, g := range bestGlasses {
		input.GlassCatalog.Entries = append(input.GlassCatalog.Entries, g)
	}

	escResult := assembleEscapeResult(res, minima)
	reportEscape(res)
	writeEscapeOutput(input, escResult)
}

func runEscapeMulti(input types.Input, gc *glass.Catalog, progress *escape.Progress, saveBase string, ctx context.Context, hardStop <-chan struct{}) {
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
	if apertureMargin < 1.0 {
		apertureMargin = 1.0
	}
	apertureMarginMM := input.Optimization.ApertureMarginMM
	if apertureMarginMM <= 0 {
		apertureMarginMM = 0.2
	}
	muConMax := input.Optimization.MuConMax

	jacobianWorkers := input.Optimization.JacobianWorkers
	if jacobianWorkers <= 0 {
		jacobianWorkers = 2
	}

	var hull *glass.ConvexHull
	hullMargin, hullWeight := resolveGlassHull(input.Optimization.GlassHull, &hull)

	factory := func() dls.Model {
		configsCopy := make([]optimize.ConfigInput, len(configs))
		copy(configsCopy, configs)
		opt := optimize.NewMultiOptimizer(configsCopy, sharedVars, localVars, gc, maxIter, mu, tol, epsilon, apertureMargin, numRays, muConMax, jacobianWorkers, nil, hull, hullMargin, hullWeight)
		opt.SetApertureMarginMM(apertureMarginMM)
		applyDegenerate(opt, input.Optimization.Degenerate)
		return opt
	}

	var onRecord escape.RecordHandler
	var saver *escapeFileSaver
	if saveBase != "" {
		saver = newEscapeFileSaver(saveBase, func(p escape.Point) types.Input {
			return materializeMultiInput(input, input.Optimization, p.X)
		})
		onRecord = saver.record
	}

	// Pristine template of each config's surfaces, used to materialise every
	// minimum (and the fingerprint) independently of the best-solution write.
	template := make([]types.Config, len(input.Configs))
	copy(template, input.Configs)

	// Design fingerprint across every config: concatenated thin-lens element
	// powers at a variable vector.
	fingerprint := func(x []float64) []float64 {
		m := applyEscapeMulti(template, input.Optimization, x)
		var fp []float64
		for _, cfg := range template {
			if s, ok := m[cfg.ID]; ok {
				fp = append(fp, paraxial.ElementPowers(s, paraxial.DLine, gc)...)
			}
		}
		return fp
	}

	res := escape.ParallelEscape(factory, *input.Optimization.Escape, escape.RunOptions{
		Progress:    progress,
		OnRecord:    onRecord,
		Fingerprint: fingerprint,
		Context:     ctx,
		HardStop:    hardStop,
	})
	progress.Event("done", map[string]any{
		"workers":     res.Workers,
		"cycles":      res.Cycles,
		"escapes":     res.Escapes,
		"minima":      len(res.Minima),
		"best_merit":  safeF(res.BestMerit),
		"timed_out":   res.TimedOut,
		"interrupted": res.Interrupted,
	})
	if saver != nil && saver.err != nil {
		errOut("escape: error saving minima: %v", saver.err)
	}

	var saveStem, saveExt string
	if saveBase != "" {
		saveStem, saveExt = splitSaveBase(saveBase)
	}

	minima := make([]types.EscapeMinimum, len(res.Minima))
	for i, p := range res.Minima {
		configSurfaces := applyEscapeMulti(template, input.Optimization, p.X)
		var cfgs []types.Config
		var features []types.ConfigFeatures
		for ci := range template {
			if s, ok := configSurfaces[template[ci].ID]; ok {
				c := template[ci]
				c.Surfaces = s
				cfgs = append(cfgs, c)
				features = append(features, types.ConfigFeatures{
					ID:            template[ci].ID,
					ElementPowers: paraxial.ElementPowers(s, paraxial.DLine, gc),
				})
			}
		}
		minima[i] = types.EscapeMinimum{
			Index:     i,
			Merit:     p.Merit,
			Configs:   cfgs,
			Variables: buildMultiVarStates(input.Optimization, p.X),
			Features:  features,
		}
		if saveBase != "" {
			minima[i].File = fmt.Sprintf("%s%d%s", saveStem, res.MinimaIdx[i], saveExt)
		}
	}

	// Apply the best solution to the top-level configs.
	if len(res.Minima) > 0 {
		best := res.Minima[res.BestIdx]
		bestSurfaces := applyEscapeMulti(template, input.Optimization, best.X)
		for i := range input.Configs {
			if s, ok := bestSurfaces[input.Configs[i].ID]; ok {
				input.Configs[i].Surfaces = s
			}
		}
	}

	escapeResult := assembleEscapeResult(res, minima)
	reportEscape(res)
	writeEscapeOutput(input, escapeResult)
}

// assembleEscapeResult wraps the minima list with the report metadata.
func assembleEscapeResult(res escape.Result, minima []types.EscapeMinimum) *types.EscapeResult {
	return &types.EscapeResult{
		BestIndex: res.BestIdx,
		BestMerit: res.BestMerit,
		Params: types.EscapeParamsInfo{
			HInitial:                     res.Params.H,
			WInitial:                     res.Params.W,
			HMult:                        res.Params.HMult,
			WMult:                        res.Params.WMult,
			DistanceThreshold:            res.Params.Dt,
			FingerprintDistanceThreshold: res.Params.DtFp,
			MaxCycles:                    res.Cycles,
			EscapeWorkers:                res.Workers,
			MaxSeconds:                   res.MaxSeconds,
			EscapeIterFrac:               res.Params.EscapeIterFrac,
			WSpan:                        res.Params.WSpan,
			StallWindowFrac:              res.Params.StallWindowFrac,
			StallRelTol:                  res.Params.StallRelTol,
			StallEarlyStop:               boolPtr(res.Params.StallEarlyStop),
			InitialPerturb:               res.Params.InitialPerturb,
		},
		TimedOut:    res.TimedOut,
		Interrupted: res.Interrupted,
		Minima:      minima,
	}
}

// buildSingleVarStates lists the variable values at a point for single-config
// mode.
func buildSingleVarStates(variables []optimize.Variable, x []float64) []types.EscapeVarState {
	states := make([]types.EscapeVarState, len(variables))
	for i, v := range variables {
		states[i] = types.EscapeVarState{
			Name:  v.Name,
			Surf:  v.SurfaceID,
			Param: v.Param,
			After: x[i],
		}
	}
	return states
}

// applyEscapeX applies a flat variable vector to a copy of the surfaces,
// producing the full lens prescription at that point.
func applyEscapeX(surfaces []types.Surface, variables []optimize.Variable, x []float64, gc *glass.Catalog) ([]types.Surface, []types.Glass) {
	result := make([]types.Surface, len(surfaces))
	copy(result, surfaces)

	for i, v := range variables {
		val := x[i]
		if ai, ok := optimize.AsphereCoefIndex(v.Param); ok {
			idx := dls.SurfaceIndex(result, v.SurfaceID)
			if idx < 0 {
				continue
			}
			for len(result[idx].Coefficients) <= ai {
				result[idx].Coefficients = append(result[idx].Coefficients, 0)
			}
			result[idx].Coefficients[ai] = val
			continue
		}
		switch v.Param {
		case "curvature", "conic", "thickness", "diameter", "radius":
			idx := dls.SurfaceIndex(result, v.SurfaceID)
			if idx < 0 {
				continue
			}
			optimize.SetSurfaceParam(&result[idx], v.Param, val)
		case "nd", "vd":
			// Inline model gas (no catalogue key): apply directly to the
			// surface material. Keyed materials are handled by
			// applyGlassOverrides below.
			idx := dls.SurfaceIndex(result, v.SurfaceID)
			if idx < 0 {
				continue
			}
			m := &result[idx].Material
			if !m.HasKey() {
				if v.Param == "nd" {
					m.ND = val
				} else {
					m.VD = val
				}
			}
		}
	}

	surface.Precompute(result)

	newGlasses := applyGlassOverrides(&result, variables, x, gc)
	return result, newGlasses
}

// applyGlassOverrides rewrites surface materials for optimised nd/vd model
// glasses, mirroring the single-config optimize output behaviour.
func applyGlassOverrides(result *[]types.Surface, variables []optimize.Variable, x []float64, gc *glass.Catalog) []types.Glass {
	optimize.MaterializeGlassEntries(variables, x, gc,
		func(v optimize.Variable) (string, bool) {
			idx := dls.SurfaceIndex(*result, v.SurfaceID)
			if idx < 0 || !(*result)[idx].Material.HasKey() {
				return "", false
			}
			return (*result)[idx].Material.Key, true
		},
		func(origKey string, nd, vd float64) {
			for i := range *result {
				if (*result)[i].Material.HasKey() && (*result)[i].Material.Key == origKey {
					(*result)[i].Material = types.Material{ND: nd, VD: vd}
				}
			}
		})
	return nil
}

// applyEscapeMulti projects a flat variable vector onto all configs' surfaces.
// configs is the pristine template (unmodified); the optimization section
// supplies the shared/local variable definitions.
func applyEscapeMulti(configs []types.Config, opt *types.OptimizationConfig, x []float64) map[string][]types.Surface {
	result := make(map[string][]types.Surface)
	for ci := range configs {
		s := make([]types.Surface, len(configs[ci].Surfaces))
		copy(s, configs[ci].Surfaces)
		result[configs[ci].ID] = s
	}

	var varIdx int
	for _, sv := range opt.SharedVariables {
		if !sv.Active {
			continue
		}
		val := x[varIdx]
		varIdx++
		for _, b := range sv.Bindings {
			surfaces, ok := result[b.Config]
			if !ok {
				continue
			}
			idx := dls.SurfaceIndex(surfaces, b.ID)
			if idx < 0 {
				continue
			}
			scale := b.Scale
			if scale == 0 {
				scale = 1.0
			}
			optimize.SetSurfaceParam(&surfaces[idx], b.Param, scale*val+b.Offset)
		}
	}
	for _, lv := range opt.LocalVariables {
		if !lv.Active {
			continue
		}
		val := x[varIdx]
		varIdx++
		surfaces, ok := result[lv.Config]
		if !ok {
			continue
		}
		idx := dls.SurfaceIndex(surfaces, lv.Target.ID)
		if idx < 0 {
			continue
		}
		optimize.SetSurfaceParam(&surfaces[idx], lv.Target.Param, val)
	}

	for _, cfg := range configs {
		if s, ok := result[cfg.ID]; ok {
			surface.Precompute(s)
		}
	}
	return result
}

// buildMultiVarStates lists variable values at a point for multi-config mode,
// in the same shared-then-local order as the variable vector.
func buildMultiVarStates(opt *types.OptimizationConfig, x []float64) []types.EscapeVarState {
	var states []types.EscapeVarState
	var varIdx int
	for _, sv := range opt.SharedVariables {
		if !sv.Active {
			continue
		}
		var param string
		if len(sv.Bindings) > 0 {
			param = sv.Bindings[0].Param
		}
		states = append(states, types.EscapeVarState{
			Name:  sv.Name,
			Param: param,
			After: x[varIdx],
		})
		varIdx++
	}
	for _, lv := range opt.LocalVariables {
		if !lv.Active {
			continue
		}
		states = append(states, types.EscapeVarState{
			Name:   lv.Name,
			Config: lv.Config,
			Surf:   lv.Target.ID,
			Param:  lv.Target.Param,
			After:  x[varIdx],
		})
		varIdx++
	}
	return states
}

// reportEscape prints a concise summary to stderr (never stdout, so the YAML
// pipeline stays intact).
func reportEscape(res escape.Result) {
	fmt.Fprintf(os.Stderr, "=== Escape complete ===\n")
	fmt.Fprintf(os.Stderr, "  Workers:   %d\n", res.Workers)
	fmt.Fprintf(os.Stderr, "  Cycles:    %d\n", res.Cycles)
	if res.MaxSeconds > 0 {
		fmt.Fprintf(os.Stderr, "  Time budget: %.3gs (reached: %v)\n", res.MaxSeconds, res.TimedOut)
	}
	if res.Interrupted {
		fmt.Fprintf(os.Stderr, "  Interrupted: true (signal received; discovered minima saved)\n")
	}
	fmt.Fprintf(os.Stderr, "  Escapes:   %d\n", res.Escapes)
	fmt.Fprintf(os.Stderr, "  Minima:    %d\n", len(res.Minima))
	for i, p := range res.Minima {
		mark := " "
		if i == res.BestIdx {
			mark = "*"
		}
		fmt.Fprintf(os.Stderr, "    %s[%d] merit=%.6e\n", mark, i, p.Merit)
	}
}

// writeEscapeOutput writes the final YAML to stdout.
func writeEscapeOutput(input types.Input, escResult *types.EscapeResult) {
	withOutputMetadata(&input, "escape", subcmdArgs())
	output := types.Output{
		Input:        input,
		EscapeResult: escResult,
	}
	writeYAML(&output)
}

// runEscapeExtract pulls one local minimum out of a previous escape output
// and emits a clean lens YAML with that minimum as the top-level solution.
func runEscapeExtract(data []byte, index int) {
	output := parseYAML[types.Output](data)
	if output.EscapeResult == nil {
		errOut("Error: input has no 'escape_result' section (was it produced by 'rayweave escape'?)")
		os.Exit(1)
	}
	if index < 0 || index >= len(output.EscapeResult.Minima) {
		errOut("Error: index %d out of range (minima count: %d)", index, len(output.EscapeResult.Minima))
		os.Exit(1)
	}
	min := output.EscapeResult.Minima[index]

	if len(min.Configs) > 0 {
		for i := range output.Configs {
			for _, mc := range min.Configs {
				if output.Configs[i].ID == mc.ID {
					output.Configs[i].Surfaces = mc.Surfaces
				}
			}
		}
	} else if len(min.Surfaces) > 0 {
		if len(output.Configs) == 0 {
			output.Configs = []types.Config{{
				ID:     "config1",
				Name:   "Config1",
				Weight: 1.0,
				Active: true,
			}}
		}
		output.Configs[0].Surfaces = min.Surfaces
	} else {
		errOut("Error: local minimum %d has no surface data", index)
		os.Exit(1)
	}

	withOutputMetadata(&output.Input, "escape extract", subcmdArgs())
	writeYAML(&output)
}

// boolPtr returns a pointer to b (nil when b is false), so the escape config
// can distinguish "explicitly off" from "defaulted on" in the output report.
func boolPtr(b bool) *bool {
	if !b {
		return nil
	}
	return &b
}

// parseEscapeExtractFlags parses `--index N` for the extract subcommand.
func parseEscapeExtractFlags(args []string) int {
	fs := flag.NewFlagSet("escape extract", flag.ContinueOnError)
	index := fs.Int("index", 0, "local minimum index to extract")
	fs.Parse(args)
	return *index
}
