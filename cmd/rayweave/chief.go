package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/hiroki/rayweaver/internal/chief"
	"github.com/hiroki/rayweaver/internal/clispec"
	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/spectral"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// clearApertureTol is the diameter change (mm) below which two sizing passes
// agree: ten microns is far below any mechanical or optical consequence.
const clearApertureTol = 0.01

// chiefSamples is the per-wavelength stats set a chief run applies. design
// samples come from each config's own wavelength table (so every zoom position
// is measured at its lines); fixed samples are the same for every config
// (--wavelengths, chief.wavelengths or the document spectrum).
type chiefSamples struct {
	fixed  []types.WavelengthSample
	design bool
}

func (s chiefSamples) forConfig(items []types.WavelengthItem) []types.WavelengthSample {
	if s.design {
		return spectral.DesignSamples(items)
	}
	return s.fixed
}

// chiefRun holds the settings shared by every config of a chief invocation;
// runConfig carries out the per-config part with that config's surfaces,
// fields and design wavelengths.
type chiefRun struct {
	input      *types.Input
	opts       *clispec.Options
	gc         *glass.Catalog
	pol        types.JonesVector
	pt         *types.PassThroughTarget
	fanCfg     *types.RayFanConfig
	wavelength float64
	samples    chiefSamples
	dumpMap    bool
}

// chiefConfigResult is what one config's chief pass produces for the output.
type chiefConfigResult struct {
	chiefRays []types.ChiefRayResult
	marginal  []types.Ray
	stop      *types.StopInfo
}

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

	if len(input.Configs) == 0 {
		errExitf("Error: no configs to process (define configs[].surfaces)")
	}

	var configList []string
	if opts.Set("configs") {
		configList = splitConfigList(opts.String("configs"))
		if len(configList) == 0 {
			errExitf("Error: --configs needs at least one config id")
		}
	}
	targets, multi, warnings, err := configTargets(
		input.Configs, opts.String("config"), opts.Bool("all-configs"), configList, 0, "--all-configs or --configs")
	if err != nil {
		errExitf("Error: %v", err)
	}
	for _, w := range warnings {
		errOut("Warning: %s", w)
	}
	if multi && fnumSet {
		errExitf("Error: --fnum cannot be combined with --all-configs/--configs (one EFL per document); use --epd or pupil_model.diameter")
	}

	pt := input.Chief.PassThrough
	if opts.Int("pass-through") > 0 {
		if pt == nil {
			pt = &types.PassThroughTarget{Surface: opts.Int("pass-through"), Coordinate: types.Vec3{}}
		} else {
			pt.Surface = opts.Int("pass-through")
		}
		input.Chief.StopSurface = opts.Int("pass-through")
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
	// (or no set when the config declares no wavelengths). A `design` set is
	// resolved per config; the explicit/spectral/custom sets are one list for
	// every config.
	wavelengthSet := input.Chief.WavelengthSet
	if opts.Set("wavelength-set") {
		wavelengthSet = opts.String("wavelength-set")
	}
	explicit := opts.Floats("wavelengths")
	samples := chiefSamples{}
	switch {
	case len(explicit) > 0:
		var list []types.WavelengthSample
		for _, wl := range explicit {
			if wl > 0 {
				list = append(list, types.WavelengthSample{Value: wl, RawWeight: 1})
			}
		}
		samples.fixed = spectral.NormalizeSet(list)
		wavelengthSet = spectral.ModeCustom
		input.Chief.WavelengthSet = wavelengthSet
		input.Chief.Wavelengths = append([]types.WavelengthSample(nil), samples.fixed...)
	case strings.EqualFold(wavelengthSet, spectral.ModeDesign):
		samples.design = true
		input.Chief.WavelengthSet = spectral.ModeDesign
	case strings.EqualFold(wavelengthSet, spectral.ModeSpectral):
		list, err := spectral.SpectralSamples(input.Spectrum, 0)
		if err != nil {
			errExitf("Error: %v", err)
		}
		samples.fixed = list
		input.Chief.WavelengthSet = spectral.ModeSpectral
	case strings.EqualFold(wavelengthSet, spectral.ModeCustom):
		list, err := spectral.CustomSamples(input.Chief.Wavelengths)
		if err != nil {
			errExitf("Error: %v", err)
		}
		samples.fixed = list
	case strings.TrimSpace(wavelengthSet) == "":
		// No set declared: use each config's design table when it has one.
		samples.design = true
	default:
		errExitf("Error: unknown chief.wavelength_set %q (design | spectral | custom)", wavelengthSet)
	}

	gc, _ := openGlass(&input, opts.String("glass-dir"))

	firstSurfaces := input.Configs[targets[0]].Surfaces
	surface.Precompute(firstSurfaces)

	selectedSys := input.System
	selectedSys.Surfaces = firstSurfaces
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

	run := &chiefRun{
		input:      &input,
		opts:       opts,
		gc:         gc,
		pol:        pol,
		pt:         pt,
		fanCfg:     fanCfg,
		wavelength: wavelength,
		samples:    samples,
		dumpMap:    dumpMap,
	}

	// Fields every config falls back to when it declares none: configs[0]'s
	// fields, then the top-level chief section (the order every single-config
	// field command uses).
	fallbackFields := resolveConfigFieldDefs(input, nil)

	preserveRays := opts.Bool("preserve-rays")
	results := make([]chiefConfigResult, len(targets))
	for ti, idx := range targets {
		cfg := &input.Configs[idx]
		fields := fieldDefsFromItems(cfg.Fields)
		if len(fields) == 0 {
			fields = fallbackFields
		}
		label := ""
		if multi {
			label = fmt.Sprintf("config %q", configLabel(input.Configs, idx))
		}
		results[ti] = run.runConfig(cfg, fields, samples.forConfig(cfg.Wavelengths), label)
	}

	// The per-config store is what makes a multi-config plot draw each
	// position's own rays; the top-level sections keep their single-config
	// meaning and describe the first target.
	first := results[0]
	if multi {
		for ti, idx := range targets {
			cfg := &input.Configs[idx]
			if !preserveRays {
				cfg.ChiefRays = results[ti].chiefRays
				// Mirror the top-level rule: without --preserve-rays the rays
				// section holds only what this pass produced.
				cfg.Rays = nil
			}
			if len(results[ti].marginal) > 0 {
				if cfg.Rays == nil {
					cfg.Rays = &types.RayInput{Polarization: pol}
				}
				cfg.Rays.Polarization = pol
				cfg.Rays.Rays = append(cfg.Rays.Rays, results[ti].marginal...)
			}
		}
	}

	// The chief rays live in chief_rays[].chief_ray (single source); the rays
	// section carries only the extras needed by `trace` (marginal rays) plus
	// the polarization, so the output stays pipe-compatible without duplicating
	// every chief ray.
	switch {
	case !preserveRays:
		input.Rays = &types.RayInput{Polarization: pol}
	case input.Rays == nil && len(first.marginal) > 0:
		input.Rays = &types.RayInput{Polarization: pol}
	}
	if len(first.marginal) > 0 {
		input.Rays.Rays = append(input.Rays.Rays, first.marginal...)
	}

	output := types.Output{
		Input:     input,
		ChiefRays: first.chiefRays,
		Stop:      first.stop,
	}
	if preserveRays {
		output.ChiefRays = nil
	}
	withOutputMetadata(&output.Input, "chief", subcmdArgs())

	writeYAML(&output)
}

// runConfig runs the chief pass, aperture sizing and marginal-ray extraction
// for one configuration. label names the config in field-resolution errors
// ("" in single-config mode, which keeps the historical message).
func (r *chiefRun) runConfig(cfg *types.Config, fields []types.FieldDef, samples []types.WavelengthSample, label string) chiefConfigResult {
	input := r.input
	opts := r.opts
	surfaces := cfg.Surfaces
	surface.Precompute(surfaces)

	sys := input.System
	sys.Surfaces = surfaces
	sys.StopSurface = input.Chief.StopSurface

	configWavelengths := make([]float64, len(samples))
	for i, s := range samples {
		configWavelengths[i] = s.Value
	}
	// Aperture sizing is conservative: cover every declared/evaluated line
	// plus the reference, even a line excluded from the white combination.
	clearWavelengths := append([]float64(nil), r.wavelength)
	seenClear := map[float64]bool{r.wavelength: true}
	addClear := func(wl float64) {
		if wl > 0 && !seenClear[wl] {
			seenClear[wl] = true
			clearWavelengths = append(clearWavelengths, wl)
		}
	}
	for _, w := range cfg.Wavelengths {
		addClear(w.Value)
	}
	for _, s := range samples {
		addClear(s.Value)
	}

	results := chief.DetermineChiefRaysGridMode(
		sys,
		fields,
		input.Chief.ReferenceSurface,
		input.Chief.NumRays,
		r.gc,
		r.pol,
		r.wavelength,
		r.dumpMap,
		input.Chief.GridType,
		r.pt,
		r.fanCfg,
		configWavelengths,
		input.Chief.PupilModel,
		input.Chief.NumRings, input.Chief.NumSpokes,
		input.Chief.ChiefRayDefinition,
	)
	requireFieldResolution(label, results)

	// --- default clear-aperture: size unset-diameter surfaces ---
	{
		margin := opts.Float("clear-aperture-margin-mm")
		envEngine := ray.NewEngine(r.gc, nil)
		surface.Precompute(surfaces)
		path := dls.BuildPath(surfaces)
		envelope := chief.BeamEnvelopeMax(results, envEngine, surfaces, path, clearWavelengths, r.pol)

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
		clearRays := input.Chief.NumRays
		if opts.Int("clear-aperture-rays") > 0 && opts.Int("clear-aperture-rays") != input.Chief.NumRays {
			// Use a denser grid so the beam footprint is accurate.
			clearRays = opts.Int("clear-aperture-rays")
		}
		refID := input.Chief.ReferenceSurface
		stopID := input.Chief.StopSurface
		margin := opts.Float("clear-aperture-margin-mm")

		// Size, re-trace, size again. The grid centring follows the surviving
		// bundle (the chief aim is the traced centroid), so a nearly dead field
		// starts centred on the few rays that got through and re-centres once
		// the sized apertures let its own beam pass. One pass would leave those
		// fields slightly under-sized; the sizes settle in two or three.
		for pass := 0; pass < 4; pass++ {
			if pass > 0 || clearRays != input.Chief.NumRays {
				results = chief.DetermineChiefRaysGridMode(
					types.System{Surfaces: surfaces, StopSurface: stopID},
					fields, refID, clearRays, r.gc, r.pol, r.wavelength, r.dumpMap,
					input.Chief.GridType, r.pt, r.fanCfg, configWavelengths,
					input.Chief.PupilModel, input.Chief.NumRings, input.Chief.NumSpokes,
					input.Chief.ChiefRayDefinition,
				)
			}
			// The chief grid points already fill the aperture stop, so trace
			// them as-is through every surface to get the true beam envelope.
			surface.Precompute(surfaces)
			path := dls.BuildPath(surfaces)
			envelope := chief.BeamEnvelopeMax(results, ray.NewEngine(r.gc, nil), surfaces, path, clearWavelengths, r.pol)

			changed := false
			for i := range surfaces {
				if surfaces[i].ID == refID || (stopID > 0 && surfaces[i].ID == stopID) || !surfaces[i].AutoAperture {
					continue
				}
				e := envelope[surfaces[i].ID]
				if e <= 0 {
					continue
				}
				if d := e*2 + 2*margin; math.Abs(d-surfaces[i].Diameter) > clearApertureTol {
					changed = true
					surfaces[i].Diameter = d
				}
			}
			if !changed {
				break
			}
		}
	}

	// Build ChiefRayResult for YAML output
	chiefRays := make([]types.ChiefRayResult, len(results))
	for i, res := range results {
		white, lateral := chief.SummarizeWavelengths(res.Wavelengths, samples, r.wavelength)
		cr := types.ChiefRayResult{
			WhiteSpotStats: white,
			LateralColor:   lateral,
			FieldAngle:     res.FieldAngle,
			ChiefRay:       res.ChiefRay,
			ImageHeight:    res.ImageHeight,
			EntrancePupil:  res.EntrancePupil,
			SpotStats:      res.SpotStats,
			// The full-aperture survival, i.e. the vignetting number: SpotStats
			// above may be measured on the reduced effective-pupil grid.
			PrescribedTotal:  res.PrescribedTotal,
			PrescribedTraced: res.PrescribedTraced,
			RayFan:           res.RayFan,
			Wavelengths:      res.Wavelengths,
			PupilProbe:       res.ProbeOK,
			PupilProbeZ:      res.ProbeZ,
			// Nil for a field that was not clipped below the grid's survival
			// threshold; `omitempty` then leaves it out of the pipeline YAML.
			EffectiveVignetting: res.EffectiveVignetting,
		}
		if r.dumpMap && len(res.GridPoints) > 0 {
			cr.GridPoints = res.GridPoints
		}
		chiefRays[i] = cr
	}

	var marginal []types.Ray
	if opts.Bool("marginal-rays") && len(results) > 0 {
		engine := ray.NewEngine(r.gc, nil)
		marginal = extractMarginalRays(results, input.Chief.StopSurface, engine, r.wavelength, surfaces, r.pol)
	}

	var stop *types.StopInfo
	stopID := input.Chief.StopSurface
	if stopID > 0 {
		for _, s := range surfaces {
			if s.ID == stopID {
				stop = &types.StopInfo{
					SurfaceID: s.ID,
					PhysicalZ: s.PhysicalZ,
					Diameter:  s.Diameter,
				}
				break
			}
		}
	}

	return chiefConfigResult{chiefRays: chiefRays, marginal: marginal, stop: stop}
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
