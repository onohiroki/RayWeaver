package main

import (
	"flag"
	"os"

	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
	"gopkg.in/yaml.v3"
)

func runParaxial(data []byte) {
	args := os.Args[2:]
	fs := flag.NewFlagSet("paraxial", flag.ExitOnError)
	wlFlag := fs.Float64("wl", 0, "reference wavelength (mm) for paraxial calculation")
	configFlag := fs.String("config", "", "select config by id (multi-config mode)")
	glassDir := fs.String("glass-dir", "", "AGF glass catalog directory")
	fs.Parse(args)

	input := parseYAML[types.Input](data)

	gc, _ := openGlass(&input, *glassDir)

	surfaces := configSurfaces(input.Configs, configFlag)
	surface.Precompute(surfaces)

	selectedSys := input.System
	selectedSys.Surfaces = surfaces
	if input.Chief != nil {
		selectedSys.StopSurface = input.Chief.StopSurface
	}

	wavelength := types.DefaultWavelength
	if input.Chief != nil && input.Chief.ReferenceWavelength > 0 {
		wavelength = input.Chief.ReferenceWavelength
	}
	if flagWasSet(fs, "wl") && *wlFlag > 0 {
		wavelength = *wlFlag
	}
	if input.Chief == nil {
		input.Chief = &types.ChiefInput{}
	}
	input.Chief.ReferenceWavelength = wavelength
	objectHeight := 0.0
	if input.Paraxial != nil {
		objectHeight = input.Paraxial.ObjectHeight
	}

	// Collect chief_rays if present; re-parse to get them
	// (they are ignored by Input unmarshal, so we need a second pass)
	var chiefRays []types.ChiefRayResult
	var temp struct {
		ChiefRays []types.ChiefRayResult `yaml:"chief_rays"`
	}
	if err := yaml.Unmarshal(data, &temp); err == nil {
		chiefRays = temp.ChiefRays
	}

	// Stop-free (dynamic-pupil) systems have no explicit stop surface for the
	// paraxial pupil trace; derive the entrance pupil from the dynamic-pupil
	// chief pass (selected config's fields under --config) so EPD is reported
	// even when the document carries no chief_rays.
	if selectedSys.StopSurface <= 0 && len(chiefRays) == 0 {
		pupil, err := dynamicPupilForInput(input, configFlag, surfaces, gc)
		if err != nil {
			// The pupil is derived from the first field only, and paraxial
			// reports no field of its own, so an unreachable image_height is a
			// warning here (chief/zoom/list fields are where it must stop).
			errOut("Warning: %v", err)
		}
		if pupil != nil {
			chiefRays = append(chiefRays, types.ChiefRayResult{EntrancePupil: pupil})
		}
	}

	result := paraxial.Compute(selectedSys, wavelength, gc, objectHeight, chiefRays)

	output := types.Output{
		Input:          input,
		ParaxialResult: &result,
	}
	withOutputMetadata(&output.Input, "paraxial", subcmdArgs())

	writeYAML(&output)
}
