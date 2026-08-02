package main

import (
	"math"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/optimize"
	"github.com/hiroki/rayweaver/internal/types"
)

// buildOptimizeVariables converts single-config `optimization.variables` into
// the unified Optimizer variable list (surface targets only).
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

// buildMeritTerms converts the first config's merit terms into the unified
// Optimizer term list (single-config escape / evaluate paths).
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
