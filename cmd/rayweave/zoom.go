package main

import (
	"flag"
	"math"
	"os"

	"github.com/hiroki/rayweaver/internal/chief"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// runZoom summarises every active configuration of a multi-config (zoom)
// document in one `zoom_summary` section: the first order of that position, the
// image height each field reaches, its spot RMS and the surviving fraction of
// its prescribed pupil grid. Reading a zoom used to mean looping `paraxial` and
// `chief` per config by hand and pasting the numbers together.
//
// The measurement reuses the document's own machinery instead of re-deriving
// anything: `paraxial.Compute` for the first order, and the same chief grid
// (`chief.DetermineChiefRaysGridMode`) `chief` traces, so the spot and
// vignetting columns are the chief_rays values of that config. It is read-only:
// no diameter is sized, no merit is evaluated, and nothing outside the
// summary's own settings (`--wl`, `--num-rays`) is written back.
func runZoom(data []byte) {
	fs := flag.NewFlagSet("zoom", flag.ExitOnError)
	wlFlag := fs.Float64("wl", 0, "reference wavelength (mm) for the first-order trace and the grids")
	numRaysFlag := fs.Int("num-rays", 0, "pupil-grid rays per field (default: chief.num_rays, else 64)")
	configFlag := fs.String("config", "", "summarise only this config by id (default: every active config)")
	glassDir := fs.String("glass-dir", "", "AGF glass catalog directory")
	fs.Parse(os.Args[2:])

	if flagWasSet(fs, "num-rays") && *numRaysFlag <= 0 {
		errOut("Error: --num-rays must be positive (got %d)", *numRaysFlag)
		os.Exit(1)
	}

	input := parseYAML[types.Input](data)
	if input.Chief == nil {
		input.Chief = &types.ChiefInput{}
	}

	// Reference wavelength: --wl wins over chief.reference_wavelength, with the
	// built-in default last (CLI/YAML rule); the effective value is written back
	// so every pipeline stage sees the same reference.
	wl := types.DefaultWavelength
	if input.Chief.ReferenceWavelength > 0 {
		wl = input.Chief.ReferenceWavelength
	}
	if flagWasSet(fs, "wl") && *wlFlag > 0 {
		wl = *wlFlag
	}
	input.Chief.ReferenceWavelength = wl

	// --num-rays mirrors chief.num_rays, so it is written back only when given:
	// the built-in 64 is a fallback for this run, not a document setting.
	numRays := input.Chief.NumRays
	if flagWasSet(fs, "num-rays") {
		numRays = *numRaysFlag
		input.Chief.NumRays = numRays
	}
	if numRays <= 0 {
		numRays = 64
	}

	objectHeight := 0.0
	if input.Paraxial != nil {
		objectHeight = input.Paraxial.ObjectHeight
	}

	gc, _ := loadCatalogs(&input, *glassDir)
	writeBackGlassDir(&input, *glassDir)

	// Which configurations: the --config selector, else every active one.
	// Surfaces live under configs[].surfaces, so a document without configs has
	// nothing to summarise.
	if len(input.Configs) == 0 {
		errOut("Error: 'configs' is required (a multi-config document declares one config per zoom position)")
		os.Exit(1)
	}
	type target struct {
		id          string
		active      bool
		fields      []types.FieldItem
		wavelengths []float64
		surfaces    []types.Surface
	}
	var targets []target
	if *configFlag != "" {
		idx, err := resolveConfig(input.Configs, *configFlag)
		if idx < 0 {
			errOut("Error: %s", err)
			os.Exit(1)
		}
		cfg := &input.Configs[idx]
		targets = append(targets, target{id: cfg.ID, active: cfg.Active, fields: cfg.Fields,
			wavelengths: wavelengthValues(cfg.Wavelengths), surfaces: cfg.Surfaces})
	} else {
		for i := range input.Configs {
			cfg := &input.Configs[i]
			if !cfg.Active {
				continue
			}
			targets = append(targets, target{id: cfg.ID, active: true, fields: cfg.Fields,
				wavelengths: wavelengthValues(cfg.Wavelengths), surfaces: cfg.Surfaces})
		}
	}
	if len(targets) == 0 {
		errOut("Error: no active config to summarise")
		os.Exit(1)
	}

	pol := polarization(input)
	refSurface := chiefRefSurface(input)
	summary := &types.ZoomSummary{ReferenceWavelength: wl, NumRays: numRays}

	for _, t := range targets {
		surfaces := append([]types.Surface{}, t.surfaces...)
		if len(surfaces) == 0 {
			errOut("Warning: config %q has no surfaces — skipped", t.id)
			continue
		}
		surface.Precompute(surfaces)

		fieldDefs := fieldDefsFromItems(t.fields)
		if len(fieldDefs) == 0 {
			fieldDefs = chiefFieldDefs(input)
		}
		if len(fieldDefs) == 0 {
			errOut("Warning: config %q declares no fields — skipped", t.id)
			continue
		}

		sys := types.System{Surfaces: surfaces, StopSurface: input.Chief.StopSurface}
		results := chief.DetermineChiefRaysGridMode(
			sys,
			fieldDefs,
			refSurface,
			numRays,
			gc,
			pol,
			wl,
			false,
			input.Chief.GridType,
			nil,
			nil,
			t.wavelengths,
			input.Chief.PupilModel,
			input.Chief.NumRings, input.Chief.NumSpokes,
			input.Chief.ChiefRayDefinition,
		)

		// The chief pass already found the entrance pupil of every field
		// (dynamic pupil for a stop-free system), so hand those to paraxial: a
		// stop-free config reports its EPD instead of an empty first order.
		chiefRays := make([]types.ChiefRayResult, 0, len(results))
		for _, r := range results {
			chiefRays = append(chiefRays, types.ChiefRayResult{
				FieldAngle:    r.FieldAngle,
				EntrancePupil: r.EntrancePupil,
				ImageHeight:   r.ImageHeight,
			})
		}
		first := paraxial.Compute(sys, wl, gc, objectHeight, chiefRays)

		row := types.ZoomConfigSummary{
			ID:                       t.id,
			Active:                   t.active,
			FocalLength:              first.FocalLength,
			EntrancePupilDiameter:    first.EntrancePupilDiameter,
			ImageSpaceFNumber:        first.ImageSpaceFNumber,
			InfConjImageSpaceFNumber: first.InfConjImageSpaceFNumber,
			TotalTrack:               first.TotalTrack,
			ImagePlaneZ:              referenceSurfaceZ(surfaces, refSurface),
		}
		for i, r := range results {
			field := types.ZoomFieldSummary{
				FieldIndex:  i,
				AngleDeg:    r.FieldAngle,
				ImageHeight: math.Hypot(r.ImageHeight.X, r.ImageHeight.Y),
			}
			if r.SpotStats != nil {
				field.SpotRMS = r.SpotStats.RMS_R
				field.GridTotal = r.SpotStats.TotalRays
				field.GridSurviving = r.SpotStats.TracedRays
				if field.GridTotal > 0 {
					field.Vignetting = float64(field.GridSurviving) / float64(field.GridTotal)
				}
			}
			row.Fields = append(row.Fields, field)
		}
		summary.Configs = append(summary.Configs, row)
	}

	if len(summary.Configs) == 0 {
		errOut("Error: no config could be summarised")
		os.Exit(1)
	}

	output := types.Output{Input: input, ZoomSummary: summary}
	withOutputMetadata(&output.Input, "zoom", subcmdArgs())
	writeYAML(&output)
}

// wavelengthValues returns a config's wavelength table as plain values.
func wavelengthValues(items []types.WavelengthItem) []float64 {
	var out []float64
	for _, w := range items {
		if w.Value > 0 {
			out = append(out, w.Value)
		}
	}
	return out
}

// referenceSurfaceZ returns the reference surface's vertex Z in the document
// frame (falling back to the last surface when the reference surface is not in
// this config's list), the position psf/focus treat as the image plane.
func referenceSurfaceZ(surfaces []types.Surface, refSurface int) float64 {
	for i := range surfaces {
		if surfaces[i].ID == refSurface {
			return surfaces[i].PhysicalZ
		}
	}
	if len(surfaces) > 0 {
		return surfaces[len(surfaces)-1].PhysicalZ
	}
	return 0
}
