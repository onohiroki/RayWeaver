package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/hiroki/rayweaver/internal/asphere"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// runAsphere implements the `asphere` subcommand: it ranks candidate surfaces
// for asphere introduction and estimates initial even-order asphere
// coefficients from the per-field OPD residuals.
func runAsphere(data []byte) {
	fs := flag.NewFlagSet("asphere", flag.ExitOnError)
	glassDir := fs.String("glass-dir", "", "AGF glass catalog directory")
	configFlag := fs.String("config", "", "select config by id (multi-config mode)")
	rings := fs.Int("rings", 0, "polar cell radial rings (default 8)")
	angles := fs.Int("angles", 0, "polar cell angular sectors (default 16)")
	pupilSamples := fs.Int("pupil-samples", 0, "pupil grid radial samples (default 21)")
	topK := fs.Int("top-k", 0, "number of top-ranked surfaces to fit (default 3)")
	sagScale := fs.Float64("sag-scale", 0, "initial sag scale alpha (default 0.2)")
	fs.Parse(os.Args[2:])

	input := parseYAML[types.Input](data)

	if input.Chief == nil {
		errOut("Error: 'chief' section is required (for fields and stop surface)")
		os.Exit(1)
	}

	gc, _ := loadCatalogs(&input, *glassDir)

	surfaces := configSurfaces(input.Configs, configFlag)
	if len(surfaces) == 0 {
		errOut("Error: no surfaces to process")
		os.Exit(1)
	}

	cfg := asphere.ConfigFromYAML(input.Asphere)
	if *rings > 0 {
		cfg.CellRings = *rings
	}
	if *angles > 0 {
		cfg.CellAngles = *angles
	}
	if *pupilSamples > 0 {
		cfg.PupilSamplesRadial = *pupilSamples
	}
	if *topK > 0 {
		cfg.TopK = *topK
	}
	if *sagScale != 0 {
		cfg.SagScale = *sagScale
	}

	fields, err := resolveAsphereFields(input, configFlag)
	if err != nil {
		errOut("Error: %v", err)
		os.Exit(1)
	}
	wavelengths := resolveAsphereWavelengths(input, configFlag)
	stopSurface := input.Chief.StopSurface

	// Ensure precompute is fresh for the PhysicalZ / ParaxialRadius data used
	// during footprint generation.
	surface.Precompute(surfaces)

	res := asphere.Run(surfaces, fields, wavelengths, cfg, gc, stopSurface, input.Chief.ReferenceSurface)

	output := types.Output{
		Input: input,
		AsphereResult: &types.AsphereCandidateResult{
			Rankings: res.Rankings,
			Warnings: res.Warnings,
		},
	}
	writeYAML(&output)
}

// resolveAsphereFields returns the analysis fields from the per-config fields
// (with their weights), else the top-level chief fields (weight 1 each).
func resolveAsphereFields(input types.Input, configFlag *string) ([]asphere.Field, error) {
	if configFlag != nil && *configFlag != "" {
		if idx, _ := resolveConfig(input.Configs, *configFlag); idx >= 0 {
			if fields := asphereFieldsFromItems(input.Configs[idx].Fields); len(fields) > 0 {
				return fields, nil
			}
		}
	}
	if len(input.Configs) > 0 {
		if fields := asphereFieldsFromItems(input.Configs[0].Fields); len(fields) > 0 {
			return fields, nil
		}
	}
	if input.Chief == nil {
		return nil, fmt.Errorf("'fields' or 'field_angles' is required (chief section)")
	}
	if len(input.Chief.Fields) > 0 {
		var out []asphere.Field
		for i, f := range input.Chief.Fields {
			out = append(out, asphere.Field{ID: i + 1, Angle: f.Angle, Weight: 1, Direction: f.Direction})
		}
		return out, nil
	}
	var out []asphere.Field
	for i, a := range input.Chief.FieldAngles {
		out = append(out, asphere.Field{ID: i + 1, Angle: a, Weight: 1, Direction: []float64{0, 1}})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("'fields' or 'field_angles' is required (chief section)")
	}
	return out, nil
}

func asphereFieldsFromItems(items []types.FieldItem) []asphere.Field {
	if len(items) == 0 {
		return nil
	}
	out := make([]asphere.Field, len(items))
	for i, f := range items {
		out[i] = asphere.Field{ID: f.ID, Angle: f.AngleDeg, Weight: f.Weight}
		if out[i].Weight == 0 {
			out[i].Weight = 1
		}
	}
	return out
}

// resolveAsphereWavelengths returns the analysis wavelengths from the chief
// section, else the selected config's wavelengths, else the default.
func resolveAsphereWavelengths(input types.Input, configFlag *string) []float64 {
	if input.Chief != nil && len(input.Chief.Wavelengths) > 0 {
		return input.Chief.Wavelengths
	}
	var items []types.WavelengthItem
	if configFlag != nil && *configFlag != "" {
		if idx, _ := resolveConfig(input.Configs, *configFlag); idx >= 0 {
			items = input.Configs[idx].Wavelengths
		}
	} else if len(input.Configs) > 0 {
		items = input.Configs[0].Wavelengths
	}
	var out []float64
	for _, w := range items {
		out = append(out, w.Value)
	}
	if len(out) == 0 {
		out = []float64{types.DefaultWavelength}
	}
	return out
}
