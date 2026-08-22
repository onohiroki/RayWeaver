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
	tBins := fs.Int("t-bins", 0, "beam-frame tangential bins (default 8)")
	pupilSamples := fs.Int("pupil-samples", 0, "pupil grid radial samples (default 21)")
	sensitivitySamples := fs.Int("sensitivity-samples", -1, "sensitivity trace radial samples (default 9; 0 = disable, use analytic proxy)")
	topK := fs.Int("top-k", 0, "number of top-ranked surfaces to fit (default 3)")
	sagScale := fs.Float64("sag-scale", 0, "initial sag scale alpha (default 0.2)")
	fs.String("calibrate-scale", "", "derive each candidate's embedded asphere scale from the measured ray-trace response (default: on; true|false)")
	scaleProbes := fs.String("scale-probes", "", "comma-separated scales to verify instead of the quadratic estimate (e.g. 0.1,0.25,0.5,1.0)")
	validate := fs.Bool("validate", false, "run a short DLS per fitted surface to verify the asphere improves the merit")
	apply := fs.Bool("apply", false, "insert the top-ranked DLS-validated asphere onto its surface (implies --validate) and output the modified system")
	dlsIter := fs.Int("dls-iter", 20, "DLS iterations per validated surface (with --validate)")
	numRays := fs.Int("num-rays", 0, "pupil grid rays for validation DLS (default 64)")
	fs.Parse(os.Args[2:])

	input := parseYAML[types.Input](data)

	if input.Chief == nil {
		errOut("Error: 'chief' section is required (for fields and stop surface)")
		os.Exit(1)
	}

	gc, _ := loadCatalogs(&input, *glassDir)
	writeBackGlassDir(&input, *glassDir)

	surfaces := configSurfaces(input.Configs, configFlag)
	if len(surfaces) == 0 {
		errOut("Error: no surfaces to process")
		os.Exit(1)
	}

	cfg := asphere.ConfigFromYAML(input.Asphere)
	cfg.CellRings = intOrYAML(*rings, cfg.CellRings)
	cfg.CellAngles = intOrYAML(*angles, cfg.CellAngles)
	cfg.TBins = intOrYAML(*tBins, cfg.TBins)
	cfg.PupilSamplesRadial = intOrYAML(*pupilSamples, cfg.PupilSamplesRadial)
	if *sensitivitySamples >= 0 {
		cfg.SensitivitySamples = *sensitivitySamples
	}
	cfg.TopK = intOrYAML(*topK, cfg.TopK)
	cfg.SagScale = floatOrYAML(*sagScale, cfg.SagScale)
	if cs, csSet, err := boolFlag(fs, "calibrate-scale"); err != nil {
		errOut("Error: %s", err)
		os.Exit(1)
	} else if csSet {
		cfg.CalibrateScale = cs
	}
	if *scaleProbes != "" {
		cfg.ScaleProbes = parseFloatList(*scaleProbes, "scale probe")
	}

	// Principle 3: echo the flag-won analysis values back into the output's
	// asphere_candidate: section (only for flags actually set).
	writeBackAsphereConfig(&input, cfg, fs)

	// Validation settings: --validate/--apply/--dls-iter/--num-rays (flags)
	// win over asphere_candidate.validate/apply/validation_dls_iter/
	// validation_num_rays (YAML).
	validateEff, applyEff := resolveAsphereValidation(&input, *validate, *apply, fs)
	dlsIterEff := *dlsIter
	if !flagWasSet(fs, "dls-iter") && input.Asphere != nil && input.Asphere.ValidationDLSIter > 0 {
		dlsIterEff = input.Asphere.ValidationDLSIter
	}
	numRaysEff := *numRays
	if !flagWasSet(fs, "num-rays") && input.Asphere != nil && input.Asphere.ValidationNumRays > 0 {
		numRaysEff = input.Asphere.ValidationNumRays
	}
	writeBackAsphereValidation(&input, validateEff, applyEff, dlsIterEff, numRaysEff, fs)

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

	// Phase 4: optionally validate each fitted top-K asphere with a short DLS.
	if (validateEff || applyEff) && dlsIterEff > 0 {
		validateFields := asphereFieldsToItems(fields)
		nr := numRaysEff
		if nr <= 0 {
			nr = 64
		}
		pupilZ := computePupilZ(input, surfaces, gc)
		validations := validateAspheres(surfaces, res.Rankings, gc, cfg.TopK, dlsIterEff, nr,
			stopSurface, input.Chief.ReferenceSurface, pupilZ, validateFields, wavelengths,
			polarization(input), input.Chief.GridType, input.Chief.PassThrough)
		for i := range res.Rankings {
			if v, ok := validations[res.Rankings[i].SurfaceID]; ok {
				res.Rankings[i].Validation = v
			}
		}
		// --apply: insert the top validated asphere's DLS-solved coefficients
		// back into the input system so the output is pipeline-compatible
		// (asphere --validate --apply | chief | trace | plot).
		if applyEff {
			if v := bestValidatedAsphere(res.Rankings); v != nil {
				applyAsphereToConfigs(&input.Configs, v.SurfaceID, v.Coefficients)
			} else {
				errOut("Error: --apply requires at least one validated asphere (--dls-iter > 0)")
				os.Exit(1)
			}
		}
	}

	output := types.Output{
		Input: input,
		AsphereResult: &types.AsphereCandidateResult{
			Rankings: res.Rankings,
			Profiles: res.Profiles,
			Warnings: res.Warnings,
		},
	}
	withOutputMetadata(&output.Input, "asphere", subcmdArgs())
	writeYAML(&output)
}

// writeBackAsphereConfig echoes the flag-won analysis settings into the
// output's asphere_candidate: section (principle 3 of the CLI/YAML rule).
// Only flags actually given are written back; untouched YAML stays as-is.
func writeBackAsphereConfig(input *types.Input, cfg asphere.Config, fs *flag.FlagSet) {
	if !anyFlagSet(fs, "rings", "angles", "t-bins", "pupil-samples", "sensitivity-samples",
		"top-k", "sag-scale", "calibrate-scale", "scale-probes") {
		return
	}
	if input.Asphere == nil {
		input.Asphere = &types.AsphereCandidateConfig{}
	}
	if flagWasSet(fs, "rings") {
		input.Asphere.CellRings = cfg.CellRings
	}
	if flagWasSet(fs, "angles") {
		input.Asphere.CellAngles = cfg.CellAngles
	}
	if flagWasSet(fs, "t-bins") {
		input.Asphere.TBins = cfg.TBins
	}
	if flagWasSet(fs, "pupil-samples") {
		input.Asphere.PupilSamplesRadial = cfg.PupilSamplesRadial
	}
	if flagWasSet(fs, "sensitivity-samples") {
		v := cfg.SensitivitySamples
		input.Asphere.SensitivitySamples = &v
	}
	if flagWasSet(fs, "top-k") {
		input.Asphere.TopK = cfg.TopK
	}
	if flagWasSet(fs, "sag-scale") {
		input.Asphere.SagScale = cfg.SagScale
	}
	if flagWasSet(fs, "calibrate-scale") {
		v := cfg.CalibrateScale
		input.Asphere.CalibrateScale = &v
	}
	if flagWasSet(fs, "scale-probes") {
		input.Asphere.ScaleProbes = cfg.ScaleProbes
	}
}

// resolveAsphereValidation resolves whether the Phase-4 validation (and its
// --apply insertion) runs: --validate/--apply (flags) win over
// asphere_candidate.validate/apply (YAML). --apply implies --validate.
func resolveAsphereValidation(input *types.Input, validateFlag, applyFlag bool, fs *flag.FlagSet) (validate, apply bool) {
	validate = validateFlag
	apply = applyFlag
	if !flagWasSet(fs, "validate") && !flagWasSet(fs, "apply") &&
		input.Asphere != nil && input.Asphere.Validate != nil {
		validate = *input.Asphere.Validate
	}
	if !flagWasSet(fs, "apply") && input.Asphere != nil && input.Asphere.Apply != nil {
		apply = *input.Asphere.Apply
	}
	if apply {
		validate = true
	}
	return validate, apply
}

// writeBackAsphereValidation echoes the flag-won validation settings into the
// output's asphere_candidate: section (principle 3). Only flags actually
// given are written back.
func writeBackAsphereValidation(input *types.Input, validate, apply bool, dlsIter, numRays int, fs *flag.FlagSet) {
	if !anyFlagSet(fs, "validate", "apply", "dls-iter", "num-rays") {
		return
	}
	if input.Asphere == nil {
		input.Asphere = &types.AsphereCandidateConfig{}
	}
	if flagWasSet(fs, "validate") {
		v := validate
		input.Asphere.Validate = &v
	}
	if flagWasSet(fs, "apply") {
		v := apply
		input.Asphere.Apply = &v
	}
	if flagWasSet(fs, "dls-iter") {
		input.Asphere.ValidationDLSIter = dlsIter
	}
	if flagWasSet(fs, "num-rays") {
		input.Asphere.ValidationNumRays = numRays
	}
}

// asphereFieldsToItems converts asphere analysis fields into types.FieldItem
// for the validation DLS merit terms.
func asphereFieldsToItems(fields []asphere.Field) []types.FieldItem {
	var out []types.FieldItem
	for _, f := range fields {
		out = append(out, types.FieldItem{ID: f.ID, AngleDeg: f.Angle, Weight: f.Weight})
	}
	return out
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

// bestValidatedAsphere returns the highest-ranked ranking whose validation
// produced DLS-solved coefficients, or nil if none validated.
func bestValidatedAsphere(rankings []types.AsphereSurfaceScore) *types.AsphereValidation {
	for _, r := range rankings {
		if r.Validation != nil && r.Validation.Coefficients != (types.AsphereCoeffs{}) {
			return r.Validation
		}
	}
	return nil
}

// applyAsphereToConfigs converts surface surfaceID in every config into an
// even-order polynomial asphere carrying the DLS-validated coefficients, with
// the conic left at zero (the validation's isolation choice). Precompute is
// refreshed so downstream PhysicalZ / ParaxialRadius data stays consistent.
func applyAsphereToConfigs(configs *[]types.Config, surfaceID int, coeffs types.AsphereCoeffs) {
	for i := range *configs {
		for j := range (*configs)[i].Surfaces {
			if (*configs)[i].Surfaces[j].ID == surfaceID {
				(*configs)[i].Surfaces[j].Type = types.AspherePolynomial
				(*configs)[i].Surfaces[j].Conic = 0
				(*configs)[i].Surfaces[j].Coefficients = asphereCoefficientSlice(coeffs)
			}
		}
		if len((*configs)[i].Surfaces) > 0 {
			surface.Precompute((*configs)[i].Surfaces)
		}
	}
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
