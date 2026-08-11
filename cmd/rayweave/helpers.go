package main

import (
	"flag"
	"math"
	"os"

	"github.com/hiroki/rayweaver/internal/chief"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/optimize"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
	"gopkg.in/yaml.v3"
)

// flagWasSet reports whether name was explicitly given on the command line.
// It distinguishes "flag not given" from a flag's default value, which the
// CLI/YAML precedence rule needs so an unset flag never overrides YAML.
func flagWasSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}

// writeBackGlassDir records the --glass-dir CLI value into the output YAML's
// glass_catalog.directory (principle 3 of the CLI/YAML rule). It is a no-op
// when the flag was not given, so an unset flag leaves the YAML untouched.
func writeBackGlassDir(input *types.Input, dir string) {
	if dir == "" {
		return
	}
	if input.GlassCatalog == nil {
		input.GlassCatalog = &types.GlassCatalog{}
	}
	input.GlassCatalog.Directory = dir
}

// parseYAML unmarshals a document into a value of type T, exiting on error.
func parseYAML[T any](data []byte) T {
	var out T
	if err := yaml.Unmarshal(data, &out); err != nil {
		errOut("Error parsing YAML: %v", err)
		os.Exit(1)
	}
	return out
}

// chiefRefSurface returns the chief reference surface ID (0 when the chief
// section is absent), used to seed the dynamic-pupil recomputation during
// optimisation.
func chiefRefSurface(input types.Input) int {
	if input.Chief == nil {
		return 0
	}
	return input.Chief.ReferenceSurface
}

// computePupilZ returns the entrance pupil Z used to centre grid traces for one
// config's initial surfaces: the explicit stop surface Z, else the dynamic
// pupil from a chief pass over the initial surfaces, else 0. It seeds the
// optimiser's grid centring (the pupil is recomputed during Phase-2 runs).
func computePupilZ(input types.Input, surfaces []types.Surface, gc *glass.Catalog) float64 {
	if input.Chief == nil {
		return 0
	}
	if input.Chief.StopSurface > 0 {
		for _, s := range surfaces {
			if s.ID == input.Chief.StopSurface {
				return s.PhysicalZ
			}
		}
	}
	pupil := dynamicEntrancePupil(surfaces, chiefFieldDefs(input), input.Chief.ReferenceSurface, input.Chief.NumRays, gc, polarization(input), input.Chief.GridType, input.Chief.PassThrough)
	if pupil != nil {
		return pupil.Center.Z
	}
	return 0
}

// polarization returns the ray polarization from the input rays section, or a
// default circular Jones vector.
func polarization(input types.Input) types.JonesVector {
	if input.Rays != nil {
		return input.Rays.Polarization
	}
	return types.NewCircularJones(true)
}

// chiefFieldDefs returns chief field definitions from the top-level chief
// section (explicit fields, else the angle list), or nil when absent.
func chiefFieldDefs(input types.Input) []types.FieldDef {
	if input.Chief == nil {
		return nil
	}
	if len(input.Chief.Fields) > 0 {
		return input.Chief.Fields
	}
	var fields []types.FieldDef
	for _, a := range input.Chief.FieldAngles {
		fields = append(fields, types.FieldDef{Angle: a, Direction: []float64{0, 1}})
	}
	return fields
}

// fieldDefsFromItems converts per-config field items into chief field
// definitions for the dynamic-pupil pass.
func fieldDefsFromItems(items []types.FieldItem) []types.FieldDef {
	if len(items) == 0 {
		return nil
	}
	out := make([]types.FieldDef, len(items))
	for i, f := range items {
		out[i] = types.FieldDef{
			Angle:       f.AngleDeg,
			ImageHeight: f.ImageHeight,
			Direction:   []float64{0, 1},
		}
	}
	return out
}

// dynamicEntrancePupil runs the dynamic-pupil chief pass over surfaces with the
// given fields and reference surface and returns the first field's entrance
// pupil (nil when none is found). It derives the pupil for stop-free systems,
// which have no aperture stop to trace from.
func dynamicEntrancePupil(surfaces []types.Surface, fields []types.FieldDef, refSurface, numRays int, gc *glass.Catalog, pol types.JonesVector, gridType types.GridType, passThrough *types.PassThroughTarget) *types.Pupil {
	if len(fields) == 0 || len(surfaces) == 0 || refSurface <= 0 {
		return nil
	}
	surface.Precompute(surfaces)
	results := chief.DetermineChiefRaysGrid(
		types.System{Surfaces: surfaces},
		fields, refSurface, numRays, gc, pol,
		types.DefaultWavelength, false, gridType, passThrough, nil, nil,
	)
	for _, r := range results {
		if r.EntrancePupil != nil {
			return r.EntrancePupil
		}
	}
	return nil
}

// dynamicPupilForInput returns the dynamic entrance pupil for the selected
// system: the per-config fields when --config is set, else the top-level chief
// fields. Nil when the chief section or a reference surface is absent.
func dynamicPupilForInput(input types.Input, configFlag *string, surfaces []types.Surface, gc *glass.Catalog) *types.Pupil {
	if input.Chief == nil || input.Chief.ReferenceSurface <= 0 {
		return nil
	}
	fields := chiefFieldDefs(input)
	if configFlag != nil && *configFlag != "" {
		if idx, _ := resolveConfig(input.Configs, *configFlag); idx >= 0 {
			if cfgFields := fieldDefsFromItems(input.Configs[idx].Fields); len(cfgFields) > 0 {
				fields = cfgFields
			}
		}
	}
	return dynamicEntrancePupil(surfaces, fields, input.Chief.ReferenceSurface, input.Chief.NumRays, gc, polarization(input), types.GridPolar, nil)
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
