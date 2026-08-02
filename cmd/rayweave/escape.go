package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/escape"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/optimize"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
	"gopkg.in/yaml.v3"
)

// runEscape runs the escape-function global optimisation and writes the best
// solution (pipeline-compatible) plus all discovered local minima in the
// escape_result section.
func runEscape(data []byte, glassDir string) {
	var input types.Input
	if err := yaml.Unmarshal(data, &input); err != nil {
		errOut("Error parsing YAML: %v", err)
		os.Exit(1)
	}
	if input.Optimization == nil {
		errOut("Error: 'optimization' section is required")
		os.Exit(1)
	}
	if input.Optimization.Escape == nil {
		errOut("Error: 'optimization.escape' section is required")
		os.Exit(1)
	}

	gc, _ := loadCatalogs(&input, glassDir)

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
		runEscapeMulti(input, gc)
		return
	}
	runEscapeSingle(input, gc)
}

func runEscapeSingle(input types.Input, gc *glass.Catalog) {
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
		MaxIter:        input.Optimization.MaxIter,
		Tol:            input.Optimization.Tol,
		Epsilon:        input.Optimization.Epsilon,
		NumRays:        input.Optimization.NumRays,
		ApertureMargin: apertureMargin,
		MuConMax:       input.Optimization.MuConMax,
		Workers:        input.Optimization.JacobianWorkers,
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

	// Each worker builds an isolated Optimizer so concurrent merit
	// evaluations never share mutable glass-override state.
	factory := func() dls.Model {
		cfgCopy := cfg
		cfgCopy.Variables = append([]optimize.Variable{}, cfg.Variables...)
		cfgCopy.Surfaces = append([]types.Surface{}, cfg.Surfaces...)
		return optimize.NewOptimizer(cfgCopy)
	}

	res := escape.ParallelEscape(factory, *input.Optimization.Escape)

	// Build the escape_result minima against the pristine original surfaces.
	minima := make([]types.EscapeMinimum, len(res.Minima))
	for i, p := range res.Minima {
		surf, _ := applyEscapeX(surfaces, variables, p.X, gc)
		minima[i] = types.EscapeMinimum{
			Index:     i,
			Merit:     p.Merit,
			Surfaces:  surf,
			Variables: buildSingleVarStates(variables, p.X),
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

func runEscapeMulti(input types.Input, gc *glass.Catalog) {
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
	muConMax := input.Optimization.MuConMax

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

	factory := func() dls.Model {
		configsCopy := make([]optimize.ConfigInput, len(configs))
		copy(configsCopy, configs)
		return optimize.NewMultiOptimizer(configsCopy, sharedVars, localVars, gc, maxIter, mu, tol, epsilon, apertureMargin, numRays, muConMax, input.Optimization.JacobianWorkers, nil, hull, hullMargin, hullWeight)
	}

	res := escape.ParallelEscape(factory, *input.Optimization.Escape)

	// Pristine template of each config's surfaces, used to materialise every
	// minimum independently of the best-solution write below.
	template := make([]types.Config, len(input.Configs))
	copy(template, input.Configs)

	minima := make([]types.EscapeMinimum, len(res.Minima))
	for i, p := range res.Minima {
		configSurfaces := applyEscapeMulti(template, input.Optimization, p.X)
		var cfgs []types.Config
		for ci := range template {
			if s, ok := configSurfaces[template[ci].ID]; ok {
				c := template[ci]
				c.Surfaces = s
				cfgs = append(cfgs, c)
			}
		}
		minima[i] = types.EscapeMinimum{
			Index:     i,
			Merit:     p.Merit,
			Configs:   cfgs,
			Variables: buildMultiVarStates(input.Optimization, p.X),
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

	escResult := assembleEscapeResult(res, minima)
	reportEscape(res)
	writeEscapeOutput(input, escResult)
}

// assembleEscapeResult wraps the minima list with the report metadata.
func assembleEscapeResult(res escape.Result, minima []types.EscapeMinimum) *types.EscapeResult {
	return &types.EscapeResult{
		BestIndex: res.BestIdx,
		BestMerit: res.BestMerit,
		Params: types.EscapeParamsInfo{
			HInitial:          res.Params.H,
			WInitial:          res.Params.W,
			HMult:             res.Params.HMult,
			WMult:             res.Params.WMult,
			DistanceThreshold: res.Params.Dt,
			MaxCycles:         res.Cycles,
			EscapeWorkers:     res.Workers,
		},
		Minima: minima,
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
		if ai, ok := escapeAsphereCoefIndex(v.Param); ok {
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
			escapeSetSurfaceParam(&result[idx], v.Param, val)
		}
	}

	surface.Precompute(result)

	newGlasses := applyGlassOverrides(&result, variables, x, gc)
	return result, newGlasses
}

// applyGlassOverrides rewrites surface materials for optimised nd/vd model
// glasses, mirroring the single-config optimize output behaviour.
func applyGlassOverrides(result *[]types.Surface, variables []optimize.Variable, x []float64, gc *glass.Catalog) []types.Glass {
	type glassAccum struct {
		nd, vd       float64
		hasND, hasVD bool
		origLabel    string
	}
	glassMap := map[string]*glassAccum{}
	for i, v := range variables {
		if v.Param != "nd" && v.Param != "vd" {
			continue
		}
		idx := dls.SurfaceIndex(*result, v.SurfaceID)
		if idx < 0 || (*result)[idx].Material == "" {
			continue
		}
		key := (*result)[idx].Material
		acc, ok := glassMap[key]
		if !ok {
			acc = &glassAccum{}
			if gc != nil {
				if g, ok2 := gc.Lookup(key); ok2 {
					acc.origLabel = g.Label
					acc.nd = g.ND
					acc.vd = g.VD
					acc.hasND = true
					acc.hasVD = true
				}
			}
			glassMap[key] = acc
		}
		switch v.Param {
		case "nd":
			acc.nd = x[i]
			acc.hasND = true
		case "vd":
			acc.vd = x[i]
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
		for i := range *result {
			if (*result)[i].Material == origKey {
				(*result)[i].Material = newKey
			}
		}
	}
	return newGlasses
}

// escapeAsphereCoefIndex maps an asphere coefficient param name to its index.
func escapeAsphereCoefIndex(param string) (int, bool) {
	switch param {
	case "a4", "coefficient_0":
		return 0, true
	case "a6", "coefficient_1":
		return 1, true
	case "a8", "coefficient_2":
		return 2, true
	case "a10", "coefficient_3":
		return 3, true
	case "a12", "coefficient_4":
		return 4, true
	}
	return 0, false
}

// escapeSetSurfaceParam sets a single surface parameter by name.
func escapeSetSurfaceParam(s *types.Surface, param string, val float64) {
	switch param {
	case "curvature":
		s.Curvature = val
	case "conic":
		s.Conic = val
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
			idx := surfaceIDIndex(surfaces, b.ID)
			if idx < 0 {
				continue
			}
			scale := b.Scale
			if scale == 0 {
				scale = 1.0
			}
			escapeSetSurfaceParam(&surfaces[idx], b.Param, scale*val+b.Offset)
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
		idx := surfaceIDIndex(surfaces, lv.Target.ID)
		if idx < 0 {
			continue
		}
		escapeSetSurfaceParam(&surfaces[idx], lv.Target.Param, val)
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

func surfaceIDIndex(surfaces []types.Surface, id int) int {
	for i, s := range surfaces {
		if s.ID == id {
			return i
		}
	}
	return -1
}

// reportEscape prints a concise summary to stderr (never stdout, so the YAML
// pipeline stays intact).
func reportEscape(res escape.Result) {
	fmt.Fprintf(os.Stderr, "=== Escape complete ===\n")
	fmt.Fprintf(os.Stderr, "  Workers:   %d\n", res.Workers)
	fmt.Fprintf(os.Stderr, "  Cycles:    %d\n", res.Cycles)
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
	output := types.Output{
		Input:        input,
		EscapeResult: escResult,
	}
	outData, err := yaml.Marshal(&output)
	if err != nil {
		errOut("Error marshaling output: %v", err)
		os.Exit(1)
	}
	os.Stdout.Write(outData)
}

// runEscapeExtract pulls one local minimum out of a previous escape output
// and emits a clean lens YAML with that minimum as the top-level solution.
func runEscapeExtract(data []byte, index int) {
	var output types.Output
	if err := yaml.Unmarshal(data, &output); err != nil {
		errOut("Error parsing YAML: %v", err)
		os.Exit(1)
	}
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

	outData, err := yaml.Marshal(&output)
	if err != nil {
		errOut("Error marshaling output: %v", err)
		os.Exit(1)
	}
	os.Stdout.Write(outData)
}

// parseEscapeExtractFlags parses `--index N` for the extract subcommand.
func parseEscapeExtractFlags(args []string) int {
	fs := flag.NewFlagSet("escape extract", flag.ContinueOnError)
	index := fs.Int("index", 0, "local minimum index to extract")
	fs.Parse(args)
	return *index
}
