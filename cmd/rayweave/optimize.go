package main

import (
	"fmt"
	"math"
	"os"

	"github.com/hiroki/rayweaver/internal/optimize"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
	"gopkg.in/yaml.v3"
)

func runOptimize(data []byte) {
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

	variables := buildOptimizeVariables(input.Optimization, surfaces)
	if len(variables) == 0 {
		fmt.Fprintf(os.Stderr, "Error: no optimization variables defined\n")
		os.Exit(1)
	}

	meritTerms := buildMeritTerms(input)

	if len(meritTerms) == 0 {
		fmt.Fprintf(os.Stderr, "Error: no merit terms defined (add 'optimization.merit' or 'configs[].merit')\n")
		os.Exit(1)
	}

	cfg := optimize.Config{
		Surfaces:     surfaces,
		Variables:    variables,
		MeritTerms:   meritTerms,
		GlassCatalog: gc,
	}

	opt := optimize.NewOptimizer(cfg)
	result := opt.Optimize()

	fmt.Fprintf(os.Stderr, "=== Optimization complete ===\n")
	fmt.Fprintf(os.Stderr, "  Status:      %s\n", result.Status)
	fmt.Fprintf(os.Stderr, "  Iterations:  %d\n", result.Iterations)
	fmt.Fprintf(os.Stderr, "  Before:      %.6e\n", result.BeforeMerit)
	fmt.Fprintf(os.Stderr, "  After:       %.6e\n", result.AfterMerit)
	if result.BeforeMerit > 0 {
		improvement := (result.BeforeMerit - result.AfterMerit) / result.BeforeMerit * 100
		fmt.Fprintf(os.Stderr, "  Improvement: %.2f%%\n", improvement)
	}

	outputSurfaces := applyVariableStates(surfaces, result.Variables)
	input.System.Surfaces = outputSurfaces

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

func buildOptimizeVariables(opt *types.OptimizationConfig, surfaces []types.Surface) []optimize.Variable {
	var variables []optimize.Variable
	for _, v := range opt.Variables {
		if !v.Active {
			continue
		}
		if v.Target.Type != "surface" {
			continue
		}
		variables = append(variables, optimize.Variable{
			Name:      v.Name,
			SurfaceID: v.Target.ID,
			Param:     v.Target.Param,
			Min:       v.Min,
			Max:       v.Max,
		})
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

func applyVariableStates(surfaces []types.Surface, states []optimize.VariableState) []types.Surface {
	result := make([]types.Surface, len(surfaces))
	copy(result, surfaces)

	for _, st := range states {
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

	surface.Precompute(result)
	return result
}
