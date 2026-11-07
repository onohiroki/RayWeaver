package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/hiroki/rayweaver/internal/clispec"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/spectral"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
	"gopkg.in/yaml.v3"
)

func runParaxial(data []byte) {
	fs := flag.NewFlagSet("paraxial", flag.ExitOnError)
	opts := clispec.Bind(clispec.Paraxial, fs)
	if err := opts.Parse(os.Args[2:]); err != nil {
		errExitf("Error: %v", err)
	}
	configFlag := opts.String("config")

	input := parseYAML[types.Input](data)

	gc, _ := openGlass(&input, opts.String("glass-dir"))

	surfaces := configSurfaces(input.Configs, &configFlag)
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
	if opts.Set("wl") && opts.Float("wl") > 0 {
		wavelength = opts.Float("wl")
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
		pupil, err := dynamicPupilForInput(input, &configFlag, surfaces, gc)
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

	// Optional colour sweep: a resolved wavelength set is evaluated with the
	// same system, so the per-wavelength EFL/BFL and the axial/secondary
	// spectrum all come from one paraxial model.
	samples, set, explicit, err := paraxialSweepSamples(input, opts, configFlag)
	if err != nil {
		errExitf("Error: %v", err)
	}
	if len(samples) > 0 {
		result.Wavelengths, result.Color = paraxialSweep(selectedSys, gc, objectHeight, chiefRays, wavelength, samples)
		if input.Paraxial == nil {
			input.Paraxial = &types.ParaxialInput{}
		}
		if len(explicit) > 0 {
			input.Paraxial.Wavelengths = append([]float64(nil), explicit...)
			input.Paraxial.WavelengthSet = ""
		} else {
			input.Paraxial.Wavelengths = nil
			input.Paraxial.WavelengthSet = set
		}
	}

	output := types.Output{
		Input:          input,
		ParaxialResult: &result,
	}
	withOutputMetadata(&output.Input, "paraxial", subcmdArgs())

	writeYAML(&output)
}

// paraxialSweepSamples resolves the requested colour sweep. CLI flags win over
// the paraxial YAML section; an explicit list wins over a set name.
func paraxialSweepSamples(input types.Input, opts *clispec.Options, configFlag string) ([]types.WavelengthSample, string, []float64, error) {
	explicit := opts.Floats("wavelengths")
	set := strings.TrimSpace(opts.String("wavelength-set"))
	if !opts.Set("wavelengths") && input.Paraxial != nil {
		explicit = input.Paraxial.Wavelengths
	}
	if !opts.Set("wavelength-set") && input.Paraxial != nil {
		set = input.Paraxial.WavelengthSet
	}
	if len(explicit) > 0 {
		set = ""
	}
	if len(explicit) > 0 {
		items := make([]types.WavelengthSample, 0, len(explicit))
		for _, wl := range explicit {
			if wl > 0 {
				items = append(items, types.WavelengthSample{Value: wl, RawWeight: 1})
			}
		}
		if len(items) == 0 {
			return nil, "", nil, nil
		}
		return spectral.NormalizeSet(items), "", explicit, nil
	}
	switch strings.ToLower(set) {
	case "":
		return nil, "", nil, nil
	case spectral.ModeDesign:
		design := spectral.DesignSamples(selectedConfigWavelengths(input.Configs, configFlag))
		if len(design) == 0 {
			return nil, "", nil, fmt.Errorf("wavelength_set: design has no config wavelengths")
		}
		return design, spectral.ModeDesign, nil, nil
	case spectral.ModeSpectral:
		spectralSamples, err := spectral.SpectralSamples(input.Spectrum, 0)
		if err != nil {
			return nil, "", nil, err
		}
		return spectralSamples, spectral.ModeSpectral, nil, nil
	case spectral.ModeCustom:
		return nil, "", nil, fmt.Errorf("wavelength_set: custom needs an explicit wavelengths list")
	default:
		return nil, "", nil, fmt.Errorf("unknown wavelength_set %s (design | spectral | custom)", set)
	}
}

// paraxialSweep evaluates the system at every sample and derives the axial
// colour shifts, the secondary spectrum and the weighted white EFL/BFL.
type paraxialSweepRow struct {
	sample types.WavelengthSample
	result types.ParaxialResult
}

func paraxialSweep(sys types.System, gc *glass.Catalog, objectHeight float64, chiefRays []types.ChiefRayResult, ref float64, samples []types.WavelengthSample) ([]types.ParaxialWavelength, *types.ParaxialColor) {
	rows := make([]paraxialSweepRow, len(samples))
	for i, s := range samples {
		rows[i] = paraxialSweepRow{sample: s, result: paraxial.Compute(sys, s.Value, gc, objectHeight, chiefRays)}
	}
	refResult := paraxial.Compute(sys, ref, gc, objectHeight, chiefRays)
	out := make([]types.ParaxialWavelength, len(rows))
	color := &types.ParaxialColor{Reference: ref}
	for i, r := range rows {
		out[i] = types.ParaxialWavelength{
			Value:                r.sample.Value,
			Label:                r.sample.Label,
			Weight:               r.sample.Weight,
			FocalLength:          r.result.FocalLength,
			SecondPrincipalFocus: r.result.SecondPrincipalFocus,
			ImageSpaceFNumber:    r.result.ImageSpaceFNumber,
			ImageSpaceNA:         r.result.ImageSpaceNA,
		}
		color.AxialColor = append(color.AxialColor, types.ParaxialColorShift{
			Value:   r.sample.Value,
			Label:   r.sample.Label,
			ShiftMM: r.result.SecondPrincipalFocus - refResult.SecondPrincipalFocus,
		})
		color.WhiteFocalLength += r.sample.Weight * r.result.FocalLength
		color.WhiteBackFocal += r.sample.Weight * r.result.SecondPrincipalFocus
	}
	color.SecondarySpectrumMM = secondarySpectrumMM(rows)
	return out, color
}

// secondarySpectrumMM is the largest departure of a sweep's BFL from the
// straight line through the F and C samples (identified by label, else by the
// standard wavelengths). Zero when either line is absent.
func secondarySpectrumMM(rows []paraxialSweepRow) float64 {
	fF, fC := math.NaN(), math.NaN()
	wlF, wlC := 0.0, 0.0
	for _, r := range rows {
		label := strings.ToUpper(strings.TrimSpace(r.sample.Label))
		switch {
		case label == "F" || (label == "" && math.Abs(r.sample.Value-0.00048613) < 1e-9):
			wlF, fF = r.sample.Value, r.result.SecondPrincipalFocus
		case label == "C" || (label == "" && math.Abs(r.sample.Value-0.00065627) < 1e-9):
			wlC, fC = r.sample.Value, r.result.SecondPrincipalFocus
		}
	}
	if math.IsNaN(fF) || math.IsNaN(fC) || math.Abs(wlC-wlF) < 1e-15 {
		return 0
	}
	slope := (fC - fF) / (wlC - wlF)
	worst := 0.0
	for _, r := range rows {
		line := fF + slope*(r.sample.Value-wlF)
		if d := math.Abs(r.result.SecondPrincipalFocus - line); d > worst {
			worst = d
		}
	}
	return worst
}

func selectedConfigWavelengths(configs []types.Config, configFlag string) []types.WavelengthItem {
	idx := 0
	if configFlag != "" {
		i, msg := resolveConfig(configs, configFlag)
		if i < 0 {
			errExitf("Error: %s", msg)
		}
		idx = i
	}
	if idx < 0 || idx >= len(configs) {
		return nil
	}
	return configs[idx].Wavelengths
}
