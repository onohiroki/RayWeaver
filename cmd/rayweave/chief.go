package main

import (
	"flag"
	"math"
	"os"
	"strings"

	"github.com/hiroki/rayweaver/internal/chief"
	"github.com/hiroki/rayweaver/internal/clispec"
	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/spectral"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

func runChief(data []byte) {
	fs := flag.NewFlagSet("chief", flag.ExitOnError)
	opts := clispec.Bind(clispec.Chief, fs)
	if err := opts.Parse(expandFanRotationArgs(os.Args[2:])); err != nil {
		errExitf("Error: %v", err)
	}

	fanPlane := opts.String("fan-plane")
	fanRotation := opts.Floats("fan-rotation")
	if fanPlane != "" && len(fanRotation) > 0 {
		errExitf("Error: --fan-plane and --fan-rotation are mutually exclusive")
	}
	if fanPlane != "" && fanPlane != "yz" && fanPlane != "xz" {
		errExitf("Error: --fan-plane must be 'yz' or 'xz' (got %q)", fanPlane)
	}

	epdSet := opts.Set("epd")
	fnumSet := opts.Set("fnum")
	epzSet := opts.Set("epz")
	if opts.Given("epd") && opts.Given("pupil-model-diameter") {
		errExitf("Error: --epd and --pupil-model-diameter are mutually exclusive")
	}
	if fnumSet && epdSet {
		errExitf("Error: --fnum and --epd/--pupil-model-diameter are mutually exclusive")
	}
	if opts.Given("epz") && opts.Given("pupil-model-axial-position") {
		errExitf("Error: --epz and --pupil-model-axial-position are mutually exclusive")
	}
	if fnumSet && opts.Float("fnum") <= 0 {
		errExitf("Error: --fnum must be positive (got %g)", opts.Float("fnum"))
	}

	input := parseYAML[types.Input](data)

	if input.Chief == nil {
		errExitf("Error: 'chief' section is required")
	}
	// Write back the settings the table declares (only the flags that were
	// given; the reference wavelength below is resolved separately because its
	// effective value depends on the document).
	opts.WriteBack(&input)

	pt := input.Chief.PassThrough
	if opts.Int("pass-through") > 0 {
		if pt == nil {
			pt = &types.PassThroughTarget{Surface: opts.Int("pass-through"), Coordinate: types.Vec3{}}
		} else {
			pt.Surface = opts.Int("pass-through")
		}
		input.Chief.StopSurface = opts.Int("pass-through")
	}

	configFlag := opts.String("config")

	// Resolve field definitions: the config selected by --config supplies its
	// own fields (a zoom position carries its own field angles / target image
	// heights), falling back to configs[0] and then the top-level chief section
	// — the same order as trace / psf / focus / asphere.
	fields := resolveConfigFieldDefs(input, &configFlag)
	if len(fields) == 0 {
		errExitf("Error: 'fields' or 'field_angles' is required")
	}

	// Reference wavelength: --wl wins over chief.reference_wavelength, with
	// the built-in default as the final fallback. Always write the effective
	// value so every pipeline stage sees the same reference.
	wavelength := opts.Float("wl")
	if !opts.Set("wl") && input.Chief.ReferenceWavelength > 0 {
		wavelength = input.Chief.ReferenceWavelength
	}
	if wavelength <= 0 {
		wavelength = types.DefaultWavelength
	}
	input.Chief.ReferenceWavelength = wavelength

	// Per-wavelength stats set: CLI > chief.wavelength_set > the config table
	// (or no set when the config declares no wavelengths).
	wavelengthSet := input.Chief.WavelengthSet
	if opts.Set("wavelength-set") {
		wavelengthSet = opts.String("wavelength-set")
	}
	explicit := opts.Floats("wavelengths")
	wavelengthSamples := []types.WavelengthSample(nil)
	switch {
	case len(explicit) > 0:
		for _, wl := range explicit {
			if wl > 0 {
				wavelengthSamples = append(wavelengthSamples, types.WavelengthSample{Value: wl, RawWeight: 1})
			}
		}
		wavelengthSamples = spectral.NormalizeSet(wavelengthSamples)
		wavelengthSet = spectral.ModeCustom
		input.Chief.WavelengthSet = wavelengthSet
		input.Chief.Wavelengths = append([]types.WavelengthSample(nil), wavelengthSamples...)
	case strings.EqualFold(wavelengthSet, spectral.ModeDesign):
		wavelengthSamples = spectral.DesignSamples(selectedConfigWavelengths(input.Configs, configFlag))
		input.Chief.WavelengthSet = spectral.ModeDesign
	case strings.EqualFold(wavelengthSet, spectral.ModeSpectral):
		var err error
		wavelengthSamples, err = spectral.SpectralSamples(input.Spectrum, 0)
		if err != nil {
			errExitf("Error: %v", err)
		}
		input.Chief.WavelengthSet = spectral.ModeSpectral
	case strings.EqualFold(wavelengthSet, spectral.ModeCustom):
		var err error
		wavelengthSamples, err = spectral.CustomSamples(input.Chief.Wavelengths)
		if err != nil {
			errExitf("Error: %v", err)
		}
	case strings.TrimSpace(wavelengthSet) == "":
		if items := selectedConfigWavelengths(input.Configs, configFlag); len(items) > 0 {
			wavelengthSamples = spectral.DesignSamples(items)
		}
	default:
		errExitf("Error: unknown chief.wavelength_set %q (design | spectral | custom)", wavelengthSet)
	}
	configWavelengths := make([]float64, len(wavelengthSamples))
	for i, s := range wavelengthSamples {
		configWavelengths[i] = s.Value
	}
	// Aperture sizing is conservative: cover every declared/evaluated line
	// plus the reference, even a line excluded from the white combination.
	clearWavelengths := append([]float64(nil), wavelength)
	seenClear := map[float64]bool{wavelength: true}
	addClear := func(wl float64) {
		if wl > 0 && !seenClear[wl] {
			seenClear[wl] = true
			clearWavelengths = append(clearWavelengths, wl)
		}
	}
	for _, w := range selectedConfigWavelengths(input.Configs, configFlag) {
		addClear(w.Value)
	}
	for _, s := range wavelengthSamples {
		addClear(s.Value)
	}

	gc, _ := openGlass(&input, opts.String("glass-dir"))

	surfaces := configSurfaces(input.Configs, &configFlag)
	surface.Precompute(surfaces)

	selectedSys := input.System
	selectedSys.Surfaces = surfaces
	selectedSys.StopSurface = input.Chief.StopSurface

	pol := types.NewCircularJones(true)
	if input.Rays != nil {
		pol = input.Rays.Polarization
	}

	// Virtual entrance pupil: CLI flags override YAML pupil_model settings.
	// --epz sets the axial position and activates virtual mode; --epd sets the
	// diameter; --fnum sets the diameter from the paraxial EFL (EPD = EFL/N).
	if epdSet || fnumSet || epzSet ||
		opts.Given("pupil-model-axial-position") || opts.Given("pupil-model-diameter") {
		if input.Chief.PupilModel == nil {
			input.Chief.PupilModel = &types.PupilModelConfig{}
		}
		if epzSet {
			input.Chief.PupilModel.Mode = "virtual_entrance_pupil"
			input.Chief.PupilModel.AxialPosition = opts.Float("epz")
		} else {
			input.Chief.PupilModel.Mode = opts.String("pupil-model-mode")
			if opts.Given("pupil-model-axial-position") {
				input.Chief.PupilModel.AxialPosition = opts.Float("epz")
			}
		}
		switch {
		case epdSet:
			input.Chief.PupilModel.Diameter = opts.Float("epd")
		case fnumSet:
			pResult := paraxial.Compute(selectedSys, wavelength, gc, 0, nil)
			if pResult.FocalLength == 0 {
				errExitf("Error: cannot compute EFL for --fnum conversion")
			}
			input.Chief.PupilModel.Diameter = math.Abs(pResult.FocalLength) / opts.Float("fnum")
		case opts.Given("pupil-model-diameter"):
			input.Chief.PupilModel.Diameter = opts.Float("epd")
		}
	}
	if input.Chief.PupilModel != nil &&
		input.Chief.PupilModel.Mode == "virtual_entrance_pupil" &&
		input.Chief.PupilModel.Diameter <= 0 {
		errExitf("Error: virtual entrance pupil requires a positive diameter (use --epd, --fnum, or pupil_model.diameter)")
	}

	// Chief-ray definition: --chief-ray wins over chief.chief_ray_definition
	// (principle 2), and is written back when given (principle 3). A value the
	// system cannot honour — a prescribed-pupil definition without a stop or a
	// virtual pupil model, or an unknown name — is an error rather than a
	// silent fallback, so a document never means two different things.
	if err := chief.ValidateRayDefinition(input.Chief.StopSurface, input.Chief.PupilModel, input.Chief.ChiefRayDefinition); err != nil {
		errExitf("Error: %v", err)
	}

	fanCfg := resolveRayFanConfig(opts.Bool("ray-fan"), fanPlane, fanRotation)

	// effective dumpMap: --clear-aperture or --marginal-rays implies grid_points
	dumpMap := input.Chief.DumpMap
	if opts.Bool("clear-aperture") || opts.Bool("marginal-rays") {
		dumpMap = true
	}

	results := chief.DetermineChiefRaysGridMode(
		selectedSys,
		fields,
		input.Chief.ReferenceSurface,
		input.Chief.NumRays,
		gc,
		pol,
		wavelength,
		dumpMap,
		input.Chief.GridType,
		pt,
		fanCfg,
		configWavelengths,
		input.Chief.PupilModel,
		input.Chief.NumRings, input.Chief.NumSpokes,
		input.Chief.ChiefRayDefinition,
	)
	requireFieldResolution("", results)

	// --- default clear-aperture: size unset-diameter surfaces ---
	{
		margin := opts.Float("clear-aperture-margin-mm")
		envEngine := ray.NewEngine(gc, nil)
		surface.Precompute(surfaces)
		path := dls.BuildPath(surfaces)
		envelope := chief.BeamEnvelopeMax(results, envEngine, surfaces, path, clearWavelengths, pol)

		refID := input.Chief.ReferenceSurface
		stopID := input.Chief.StopSurface
		for i := range surfaces {
			if surfaces[i].ID == refID || (stopID > 0 && surfaces[i].ID == stopID) {
				continue
			}
			if surfaces[i].Diameter <= 0 {
				surfaces[i].AutoAperture = true
				if e := envelope[surfaces[i].ID]; e > 0 {
					surfaces[i].Diameter = e*2 + 2*margin
				}
			}
		}
	}

	// --- --clear-aperture: scale grid points by entrance-pupil-based radius and set Diameter ---
	if opts.Bool("clear-aperture") && len(results) > 0 {
		if opts.Int("clear-aperture-rays") > 0 && opts.Int("clear-aperture-rays") != input.Chief.NumRays {
			// Re-trace with a denser grid so the beam footprint is accurate.
			results = chief.DetermineChiefRaysGridMode(
				selectedSys, fields, input.Chief.ReferenceSurface, opts.Int("clear-aperture-rays"),
				gc, pol, wavelength, dumpMap, input.Chief.GridType, pt, fanCfg, configWavelengths,
				input.Chief.PupilModel, input.Chief.NumRings, input.Chief.NumSpokes,
				input.Chief.ChiefRayDefinition,
			)
		}
		// The chief grid points already fill the aperture stop, so trace them
		// as-is through every surface to get the true beam envelope.
		engine2 := ray.NewEngine(gc, nil)
		surface.Precompute(surfaces)
		path := dls.BuildPath(surfaces)
		envelope := chief.BeamEnvelopeMax(results, engine2, surfaces, path, clearWavelengths, pol)

		refID := input.Chief.ReferenceSurface
		stopID := input.Chief.StopSurface
		for i := range surfaces {
			if surfaces[i].ID == refID || (stopID > 0 && surfaces[i].ID == stopID) || !surfaces[i].AutoAperture {
				continue
			}
			if e := envelope[surfaces[i].ID]; e > 0 {
				surfaces[i].Diameter = e*2 + 2*opts.Float("clear-aperture-margin-mm")
			}
		}
	}

	// Build ChiefRayResult for YAML output
	chiefRays := make([]types.ChiefRayResult, len(results))
	for i, r := range results {
		white, lateral := chief.SummarizeWavelengths(r.Wavelengths, wavelengthSamples, wavelength)
		cr := types.ChiefRayResult{
			WhiteSpotStats: white,
			LateralColor:   lateral,
			FieldAngle:     r.FieldAngle,
			ChiefRay:       r.ChiefRay,
			ImageHeight:    r.ImageHeight,
			EntrancePupil:  r.EntrancePupil,
			SpotStats:      r.SpotStats,
			// The full-aperture survival, i.e. the vignetting number: SpotStats
			// above may be measured on the reduced effective-pupil grid.
			PrescribedTotal:  r.PrescribedTotal,
			PrescribedTraced: r.PrescribedTraced,
			RayFan:           r.RayFan,
			Wavelengths:      r.Wavelengths,
			PupilProbe:       r.ProbeOK,
			PupilProbeZ:      r.ProbeZ,
			// Nil for a field that was not clipped below the grid's survival
			// threshold; `omitempty` then leaves it out of the pipeline YAML.
			EffectiveVignetting: r.EffectiveVignetting,
		}
		if dumpMap && len(r.GridPoints) > 0 {
			cr.GridPoints = r.GridPoints
		}
		chiefRays[i] = cr
	}

	// The chief rays live in chief_rays[].chief_ray (single source); the rays
	// section carries only the extras needed by `trace` (marginal rays) plus
	// the polarization, so the output stays pipe-compatible without duplicating
	// every chief ray.
	rayList := []types.Ray(nil)
	if !opts.Bool("preserve-rays") {
		input.Rays = &types.RayInput{
			Polarization: pol,
			Rays:         rayList,
		}
	}

	// --- --marginal-rays: extract marginal rays from grid points ---
	if opts.Bool("marginal-rays") && len(results) > 0 {
		engine := ray.NewEngine(gc, nil)
		input.Rays.Rays = append(input.Rays.Rays, extractMarginalRays(results, input.Chief.StopSurface, engine, wavelength, surfaces, pol)...)
	}

	output := types.Output{
		Input:     input,
		ChiefRays: chiefRays,
	}
	if opts.Bool("preserve-rays") {
		output.ChiefRays = nil
	}
	withOutputMetadata(&output.Input, "chief", subcmdArgs())

	stopID := input.Chief.StopSurface
	if stopID > 0 {
		for _, s := range surfaces {
			if s.ID == stopID {
				output.Stop = &types.StopInfo{
					SurfaceID: stopID,
					PhysicalZ: s.PhysicalZ,
					Diameter:  s.Diameter,
				}
				break
			}
		}
	}

	writeYAML(&output)
}

// extractMarginalRays finds the grid rays with max/min image Y (and X for
// fields with an X direction component) and returns them as marginal rays.
// With an aperture stop defined (stopSurfaceID > 0) the vignetted stop-edge is
// used instead, which is correct for off-axis fields. engine (may be nil) drives
// the vignetting bisection.
func extractMarginalRays(results []chief.Result, stopSurfaceID int, engine *ray.Engine, wavelength float64, surfaces []types.Surface, pol types.JonesVector) []types.Ray {
	var rays []types.Ray
	path := dls.BuildPath(surfaces)
	for fi, r := range results {
		rays = append(rays, chief.MarginalRays(fi, r, stopSurfaceID, surfaces, engine, wavelength, path, pol)...)
	}
	return rays
}

// expandFanRotationArgs rewrites "--fan-rotation 0 45 90" into
// "--fan-rotation 0 --fan-rotation 45 --fan-rotation 90" so the standard
// flag package can accumulate all values via a repeatable flag.
func expandFanRotationArgs(args []string) []string {
	var out []string
	i := 0
	for i < len(args) {
		a := args[i]
		if a == "--fan-rotation" || a == "-fan-rotation" {
			i++
			for i < len(args) && !strings.HasPrefix(args[i], "-") {
				out = append(out, "--fan-rotation", args[i])
				i++
			}
			continue
		}
		out = append(out, a)
		i++
	}
	return out
}

// resolveRayFanConfig converts --ray-fan / --fan-plane / --fan-rotation
// into a *types.RayFanConfig. Returns nil when no fan is requested.
func resolveRayFanConfig(rayFan bool, fanPlane string, fanRotation []float64) *types.RayFanConfig {
	angles := []float64(nil)
	switch {
	case len(fanRotation) > 0:
		angles = fanRotation
	case fanPlane == "yz":
		angles = []float64{90}
	case fanPlane == "xz":
		angles = []float64{0}
	case rayFan:
		// Default: both YZ and XZ for all fields.
		angles = []float64{0, 90}
	}
	if len(angles) == 0 {
		return nil
	}
	return &types.RayFanConfig{Angles: angles, NumRays: 256}
}
