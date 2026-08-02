package main

import (
	"fmt"
	"math"
	"os"

	"github.com/hiroki/rayweaver/internal/chief"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/optimize"
	"github.com/hiroki/rayweaver/internal/types"
	"gopkg.in/yaml.v3"
)

// parseYAML unmarshals a document into a value of type T, exiting on error.
func parseYAML[T any](data []byte) T {
	var out T
	if err := yaml.Unmarshal(data, &out); err != nil {
		errOut("Error parsing YAML: %v", err)
		os.Exit(1)
	}
	return out
}

// writeYAML marshals a value to stdout, exiting on error.
func writeYAML(v any) {
	outData, err := yaml.Marshal(v)
	if err != nil {
		errOut("Error marshaling output: %v", err)
		os.Exit(1)
	}
	os.Stdout.Write(outData)
}

// marginalRaysForField extracts the grid rays with max/min image Y (and X for
// fields with an X direction component) for one chief result and returns them
// as marginal rays.
func marginalRaysForField(fi int, r chief.Result, wavelength float64, path []int, pol types.JonesVector) []types.Ray {
	var maxY, minY *types.GridPoint
	var maxX, minX *types.GridPoint
	hasX := math.Abs(r.FieldDir.X) > 1e-6

	for i := range r.GridPoints {
		gp := &r.GridPoints[i]
		if gp.ImageX == nil || gp.ImageY == nil {
			continue
		}
		y := *gp.ImageY
		if maxY == nil || y > *maxY.ImageY {
			maxY = gp
		}
		if minY == nil || y < *minY.ImageY {
			minY = gp
		}
		if hasX {
			x := *gp.ImageX
			if maxX == nil || x > *maxX.ImageX {
				maxX = gp
			}
			if minX == nil || x < *minX.ImageX {
				minX = gp
			}
		}
	}

	fid := fmt.Sprintf("f%d", fi)
	var rays []types.Ray
	if maxY != nil && *maxY.ImageY != 0 {
		rays = append(rays, types.Ray{
			ID:         fmt.Sprintf("marginal_%s_Yplus", fid),
			Wavelength: wavelength,
			Initial:    types.RayState{Origin: maxY.Origin, Direction: maxY.Direction},
			Path:       path,
			Jones:      pol,
		})
	}
	if minY != nil && *minY.ImageY != 0 {
		rays = append(rays, types.Ray{
			ID:         fmt.Sprintf("marginal_%s_Yminus", fid),
			Wavelength: wavelength,
			Initial:    types.RayState{Origin: minY.Origin, Direction: minY.Direction},
			Path:       path,
			Jones:      pol,
		})
	}
	if hasX {
		if maxX != nil && *maxX.ImageX != 0 {
			rays = append(rays, types.Ray{
				ID:         fmt.Sprintf("marginal_%s_Xplus", fid),
				Wavelength: wavelength,
				Initial:    types.RayState{Origin: maxX.Origin, Direction: maxX.Direction},
				Path:       path,
				Jones:      pol,
			})
		}
		if minX != nil && *minX.ImageX != 0 {
			rays = append(rays, types.Ray{
				ID:         fmt.Sprintf("marginal_%s_Xminus", fid),
				Wavelength: wavelength,
				Initial:    types.RayState{Origin: minX.Origin, Direction: minX.Direction},
				Path:       path,
				Jones:      pol,
			})
		}
	}
	return rays
}

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
