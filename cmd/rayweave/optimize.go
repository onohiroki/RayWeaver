package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/optimize"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
	"gopkg.in/yaml.v3"
)

func runOptimize(data []byte, verbose bool, logFile string) {
	var input types.Input
	if err := yaml.Unmarshal(data, &input); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing YAML: %v\n", err)
		os.Exit(1)
	}

	if input.Optimization == nil {
		fmt.Fprintf(os.Stderr, "Error: 'optimization' section is required\n")
		os.Exit(1)
	}

	gc, _ := loadCatalogs(&input)

	surfaces := input.System.Surfaces
	if len(input.Configs) > 0 && len(input.Configs[0].Surfaces) > 0 {
		surfaces = input.Configs[0].Surfaces
	}
	surface.Precompute(surfaces)

	variables := buildOptimizeVariables(input.Optimization, gc)
	if len(variables) == 0 {
		fmt.Fprintf(os.Stderr, "Error: no optimization variables defined\n")
		os.Exit(1)
	}

	meritTerms := buildMeritTerms(input)

	if len(meritTerms) == 0 {
		fmt.Fprintf(os.Stderr, "Error: no merit terms defined (add 'optimization.merit' or 'configs[].merit')\n")
		os.Exit(1)
	}


	var logger optimize.Logger
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
			fmt.Fprintf(os.Stderr, "Error creating log file: %v\n", err)
			os.Exit(1)
		}
		logWriters = append(logWriters, struct {
			name string
			w    *os.File
		}{name: logFile, w: f})
		if logger == nil {
			logger = &jsonLogger{w: f}
		} else {
			logger = &multiLogger{loggers: []optimize.Logger{logger, &jsonLogger{w: f}}}
		}
	}

	cfg := optimize.Config{
		Surfaces:     surfaces,
		Variables:    variables,
		MeritTerms:   meritTerms,
		GlassCatalog: gc,
		Logger:       logger,
		MaxIter:      input.Optimization.MaxIter,
		Tol:          input.Optimization.Tol,
		Epsilon:      input.Optimization.Epsilon,
	}

	opt := optimize.NewOptimizer(cfg)
	result := opt.Optimize()

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
	input.System.Surfaces = outputSurfaces

	for _, g := range newGlasses {
		input.GlassCatalog.Entries = append(input.GlassCatalog.Entries, g)
	}

	output := types.Output{
		Input: input,
	}

	outData, err := yaml.Marshal(&output)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshaling output: %v\n", err)
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
				if mt.Kind != "spot_rms" {
					continue
				}
				var fieldAngle, fieldWeight float64
				for _, f := range cfg.Fields {
					if f.ID == mt.Field {
						fieldAngle = f.AngleDeg
						fieldWeight = f.Weight
						break
					}
				}
				var wavWeight float64
				for _, w := range cfg.Wavelengths {
					if math.Abs(w.Value-mt.Wavelength) < 1e-12 {
						wavWeight = w.Weight
						break
					}
				}
				if fieldWeight == 0 {
					fieldWeight = 1.0
				}
				if wavWeight == 0 {
					wavWeight = 1.0
				}
				terms = append(terms, optimize.MeritTerm{
					FieldAngle:  fieldAngle,
					FieldDir:    []float64{0, 1},
					FieldWeight: fieldWeight,
					Wavelength:  mt.Wavelength,
					WavWeight:   wavWeight,
					Weight:      mt.Weight,
				})
			}
		}
	}

	if len(terms) == 0 && input.Optimization != nil {
		for _, v := range input.Optimization.Variables {
			_ = v
		}
	}

	return terms
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
		nd, vd     float64
		hasND, hasVD bool
		origLabel  string
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

type iterLog struct {
	Iter        int       `json:"iter"`
	Merit       float64   `json:"merit"`
	Improvement float64   `json:"improvement"`
	StepNorm    float64   `json:"step_norm"`
	Variables   []float64 `json:"variables"`
}

type finalLog struct {
	Iter      int       `json:"iter"`
	Merit     float64   `json:"merit"`
	StepNorm  float64   `json:"step_norm"`
	Variables []float64 `json:"variables"`
	Status    string    `json:"status"`
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

func (j *jsonLogger) LogIter(iter int, merit, improvement, stepNorm float64, variables []float64) {
	entry := iterLog{
		Iter:        iter,
		Merit:       safeF(merit),
		Improvement: safeF(improvement),
		StepNorm:    safeF(stepNorm),
		Variables:   safeVars(variables),
	}
	data, err := json.Marshal(entry)
	if err != nil {
		fmt.Fprintf(j.w, "ERR iter=%d: %v\n", iter, err)
		return
	}
	fmt.Fprintln(j.w, string(data))
}

func (j *jsonLogger) LogFinal(iter int, status string, merit float64, stepNorm float64, variables []float64) {
	entry := finalLog{
		Iter:      iter,
		Merit:     safeF(merit),
		Variables: safeVars(variables),
		Status:    status,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		fmt.Fprintf(j.w, "ERR final: %v\n", err)
		return
	}
	fmt.Fprintln(j.w, string(data))
}

type multiLogger struct {
	loggers []optimize.Logger
}

func (m *multiLogger) LogIter(iter int, merit, improvement, stepNorm float64, variables []float64) {
	for _, l := range m.loggers {
		l.LogIter(iter, merit, improvement, stepNorm, variables)
	}
}

func (m *multiLogger) LogFinal(iter int, status string, merit float64, stepNorm float64, variables []float64) {
	for _, l := range m.loggers {
		l.LogFinal(iter, status, merit, stepNorm, variables)
	}
}
