package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/hiroki/rayweaver/internal/asphere"
	"github.com/hiroki/rayweaver/internal/clispec"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// runAsphere implements the `asphere` subcommand: it ranks candidate surfaces
// for asphere introduction and estimates initial even-order asphere
// coefficients from the per-field OPD residuals.
func runAsphere(data []byte) {
	fs := flag.NewFlagSet("asphere", flag.ExitOnError)
	flags := clispec.Bind(clispec.Asphere, fs)
	if err := flags.Parse(os.Args[2:]); err != nil {
		errExitf("Error: %v", err)
	}
	configFlag := flags.String("config")
	rings := flags.Int("rings")
	angles := flags.Int("angles")
	tBins := flags.Int("t-bins")
	pupilSamples := flags.Int("pupil-samples")
	sensitivitySamples := flags.Int("sensitivity-samples")
	topK := flags.Int("top-k")
	sagScale := flags.Float("sag-scale")
	scaleProbes := flags.String("scale-probes")
	validate := flags.Bool("validate")
	apply := flags.Bool("apply")
	dlsIter := flags.Int("dls-iter")
	numRays := flags.Int("num-rays")
	diagnostics := flags.String("diagnostics")
	focusSamples := flags.Int("focus-samples")
	focusFans := flags.String("focus-fans")

	input := parseFieldInput(data, "(for fields and stop surface)")

	gc, _ := openGlass(&input, flags.String("glass-dir"))

	surfaces := configSurfaces(input.Configs, &configFlag)
	if len(surfaces) == 0 {
		errExitf("Error: no surfaces to process")
	}

	cfg := asphere.ConfigFromYAML(input.Asphere)
	cfg.CellRings = intOrYAML(rings, cfg.CellRings)
	cfg.CellAngles = intOrYAML(angles, cfg.CellAngles)
	cfg.TBins = intOrYAML(tBins, cfg.TBins)
	cfg.PupilSamplesRadial = intOrYAML(pupilSamples, cfg.PupilSamplesRadial)
	if sensitivitySamples >= 0 {
		cfg.SensitivitySamples = sensitivitySamples
	}
	cfg.TopK = intOrYAML(topK, cfg.TopK)
	cfg.SagScale = floatOrYAML(sagScale, cfg.SagScale)
	if cs, csSet, err := flags.BoolString("calibrate-scale"); err != nil {
		errExitf("Error: %s", err)
	} else if csSet {
		cfg.CalibrateScale = cs
	}
	if scaleProbes != "" {
		cfg.ScaleProbes = parseFloatList(scaleProbes, "scale probe")
	}

	// Focus channel flags: --diagnostics, --focus-fans, --focus-samples, --ranking.
	if input.Asphere == nil {
		input.Asphere = &types.AsphereCandidateConfig{}
	}
	if diagnostics != "" {
		input.Asphere.Diagnostics = parseDiagnostics(diagnostics)
	}
	if focusSamples > 0 {
		input.Asphere.FocusSamples = focusSamples
	}
	if focusFans != "" {
		input.Asphere.FocusFans = parseCommaList(focusFans)
	}

	// Principle 3: echo the flag-won analysis values back into the output's
	// asphere_candidate: section (only for flags actually set).
	writeBackAsphereConfig(&input, cfg, flags)

	// Validation settings: --validate/--apply/--dls-iter/--num-rays (flags)
	// win over asphere_candidate.validate/apply/validation_dls_iter/
	// validation_num_rays (YAML).
	validateEff, applyEff := resolveAsphereValidation(&input, validate, apply, flags)
	dlsIterEff := dlsIter
	if !flags.Set("dls-iter") && input.Asphere != nil && input.Asphere.ValidationDLSIter > 0 {
		dlsIterEff = input.Asphere.ValidationDLSIter
	}
	numRaysEff := numRays
	if !flags.Set("num-rays") && input.Asphere != nil && input.Asphere.ValidationNumRays > 0 {
		numRaysEff = input.Asphere.ValidationNumRays
	}
	writeBackAsphereValidation(&input, validateEff, applyEff, dlsIterEff, numRaysEff, flags)

	fields, err := resolveAsphereFields(input, &configFlag)
	if err != nil {
		errExitf("Error: %v", err)
	}
	wavelengths := resolveAsphereWavelengths(input, &configFlag)
	stopSurface := input.Chief.StopSurface

	// Ensure precompute is fresh for the PhysicalZ / ParaxialRadius data used
	// during footprint generation.
	surface.Precompute(surfaces)

	res := asphere.Run(surfaces, fields, wavelengths, cfg, gc, stopSurface, input.Chief.ReferenceSurface, buildFocusConfig(input))

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
			polarization(input), input.Chief.GridType, input.Chief.PassThrough, chiefRayDefinition(input))
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
				errExitf("Error: --apply requires at least one validated asphere (--dls-iter > 0)")
			}
		}
	}

	output := types.Output{
		Input: input,
		AsphereResult: &types.AsphereCandidateResult{
			Rankings: res.Rankings,
			Profiles: res.Profiles,
			Surfaces: res.Surfaces,
			Warnings: res.Warnings,
		},
	}
	withOutputMetadata(&output.Input, "asphere", subcmdArgs())
	writeYAML(&output)
}

// writeBackAsphereConfig echoes the flag-won analysis settings into the
// output's asphere_candidate: section (principle 3 of the CLI/YAML rule).
// Only flags actually given are written back; untouched YAML stays as-is.
func writeBackAsphereConfig(input *types.Input, cfg asphere.Config, flags *clispec.Options) {
	if !flags.AnySet("rings", "angles", "t-bins", "pupil-samples", "sensitivity-samples",
		"top-k", "sag-scale", "calibrate-scale", "scale-probes",
		"diagnostics", "focus-samples", "focus-fans") {
		return
	}
	if input.Asphere == nil {
		input.Asphere = &types.AsphereCandidateConfig{}
	}
	if flags.Set("rings") {
		input.Asphere.CellRings = cfg.CellRings
	}
	if flags.Set("angles") {
		input.Asphere.CellAngles = cfg.CellAngles
	}
	if flags.Set("t-bins") {
		input.Asphere.TBins = cfg.TBins
	}
	if flags.Set("pupil-samples") {
		input.Asphere.PupilSamplesRadial = cfg.PupilSamplesRadial
	}
	if flags.Set("sensitivity-samples") {
		v := cfg.SensitivitySamples
		input.Asphere.SensitivitySamples = &v
	}
	if flags.Set("top-k") {
		input.Asphere.TopK = cfg.TopK
	}
	if flags.Set("sag-scale") {
		input.Asphere.SagScale = cfg.SagScale
	}
	if flags.Set("calibrate-scale") {
		v := cfg.CalibrateScale
		input.Asphere.CalibrateScale = &v
	}
	if flags.Set("scale-probes") {
		input.Asphere.ScaleProbes = cfg.ScaleProbes
	}
	if flags.Set("diagnostics") {
		// --diagnostics is handled by buildFocusConfig; write back here for YAML round-trip.
		// The value was already applied to input.Asphere.Diagnostics by the caller.
	}
	if flags.Set("focus-samples") {
		// Already applied to input.Asphere.FocusSamples by the caller.
	}
	if flags.Set("focus-fans") {
		// Already applied to input.Asphere.FocusFans by the caller.
	}
}

// resolveAsphereValidation resolves whether the Phase-4 validation (and its
// --apply insertion) runs: --validate/--apply (flags) win over
// asphere_candidate.validate/apply (YAML). --apply implies --validate.
func resolveAsphereValidation(input *types.Input, validateFlag, applyFlag bool, flags *clispec.Options) (validate, apply bool) {
	validate = validateFlag
	apply = applyFlag
	if !flags.Set("validate") && !flags.Set("apply") &&
		input.Asphere != nil && input.Asphere.Validate != nil {
		validate = *input.Asphere.Validate
	}
	if !flags.Set("apply") && input.Asphere != nil && input.Asphere.Apply != nil {
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
func writeBackAsphereValidation(input *types.Input, validate, apply bool, dlsIter, numRays int, flags *clispec.Options) {
	if !flags.AnySet("validate", "apply", "dls-iter", "num-rays") {
		return
	}
	if input.Asphere == nil {
		input.Asphere = &types.AsphereCandidateConfig{}
	}
	if flags.Set("validate") {
		v := validate
		input.Asphere.Validate = &v
	}
	if flags.Set("apply") {
		v := apply
		input.Asphere.Apply = &v
	}
	if flags.Set("dls-iter") {
		input.Asphere.ValidationDLSIter = dlsIter
	}
	if flags.Set("num-rays") {
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

// resolveAsphereWavelengths returns the selected config's analysis wavelengths,
// falling back to the system reference wavelength.
func resolveAsphereWavelengths(input types.Input, configFlag *string) []float64 {
	if out := configWavelengthValues(input.Configs, configFlag); len(out) > 0 {
		return out
	}
	return []float64{effectiveReferenceWavelength(input.Chief)}
}

// buildFocusConfig builds the focus channel configuration from CLI flags and
// YAML settings. Returns nil when focus diagnostics are not requested.
func buildFocusConfig(input types.Input) *asphere.FocusConfig {
	cfg := asphere.DefaultFocusConfig()
	enabled := false

	// YAML diagnostics
	if input.Asphere != nil {
		for _, d := range input.Asphere.Diagnostics {
			if d == "focus" {
				enabled = true
			}
		}
	}

	if !enabled {
		return nil
	}

	if input.Asphere != nil {
		if input.Asphere.FocusSamples > 0 {
			cfg.NumSamples = input.Asphere.FocusSamples
		}
		if len(input.Asphere.FocusFans) > 0 {
			cfg.Tangential = false
			cfg.Sagittal = false
			for _, f := range input.Asphere.FocusFans {
				switch f {
				case "tangential":
					cfg.Tangential = true
				case "sagittal":
					cfg.Sagittal = true
				}
			}
		}
	}
	return &cfg
}

// parseDiagnostics splits a comma-separated diagnostics string (e.g. "opd,focus").
func parseDiagnostics(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseCommaList splits a comma-separated string.
func parseCommaList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
