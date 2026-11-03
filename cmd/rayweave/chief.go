package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/hiroki/rayweaver/internal/chief"
	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

func runChief(data []byte) {
	fs := flag.NewFlagSet("chief", flag.ExitOnError)
	clearAperture := fs.Bool("clear-aperture", false, "trace grid rays (all fields) through every surface, compute the maximum radial extent (max |Y|,|X|) at each surface, and set auto_aperture: true surfaces' diameter = 2x that extent + margin")
	clearApertureMarginMM := fs.Float64("clear-aperture-margin-mm", 0.2, "with --clear-aperture, extra clearance added to each side of the beam footprint (mm)")
	clearApertureRays := fs.Int("clear-aperture-rays", 0, "ray count for --clear-aperture beam tracing (0 = use chief.num_rays); use a dense grid for accurate footprints")
	marginalRays := fs.Bool("marginal-rays", false, "from each field's grid points find the rays with max/min image Y (and X for off-axis fields) and append them as marginal rays to the output 'rays' section for piping into trace/plot")
	preserveRays := fs.Bool("preserve-rays", false, "with --clear-aperture, keep the existing 'rays' section instead of replacing it with chief rays, and omit chief_rays from the output (aperture adjustment only)")
	passThrough := fs.Int("pass-through", 0, "constrain chief ray to pass through (0,0,0) center of surface N (overrides YAML pass_through.surface)")
	rayFan := fs.Bool("ray-fan", false, "compute ray fan (transverse aberration) for each field")
	fanPlane := fs.String("fan-plane", "", "fan plane selection: yz | xz (implies --ray-fan)")
	var fanRotation floatList
	fs.Var(&fanRotation, "fan-rotation", "fan plane Z-rotation angle in degrees (implies --ray-fan; 0=XZ, 90=YZ; repeatable or space-separated)")
	wlFlag := fs.Float64("wl", types.DefaultWavelength, "wavelength (mm) for grid ray tracing")
	configFlag := fs.String("config", "", "select config by id (multi-config mode)")
	glassDir := fs.String("glass-dir", "", "AGF glass catalog directory")
	pupilAxialPos := fs.Float64("pupil-model-axial-position", 0, "virtual entrance pupil axial position Z (mm); activates virtual pupil mode")
	pupilDiameter := fs.Float64("pupil-model-diameter", 0, "virtual entrance pupil diameter (mm); activates virtual pupil mode")
	pupilMode := fs.String("pupil-model-mode", "virtual_entrance_pupil", "pupil model mode: virtual_entrance_pupil")
	epd := fs.Float64("epd", 0, "shorthand for --pupil-model-diameter (mm)")
	fnum := fs.Float64("fnum", 0, "F-number; sets pupil diameter = EFL / FNUM")
	epz := fs.Float64("epz", 0, "shorthand for virtual entrance pupil at Z mm (activates virtual_entrance_pupil mode)")
	rayDefinition := fs.String("chief-ray", "", "chief-ray definition: entrance_pupil_centre | centroid | vignetting_centre (empty = chief.chief_ray_definition)")
	fs.Parse(expandFanRotationArgs(os.Args[2:]))
	wlSet := flagWasSet(fs, "wl")

	if *fanPlane != "" && len(fanRotation) > 0 {
		errExitf("Error: --fan-plane and --fan-rotation are mutually exclusive")
	}
	if *fanPlane != "" && *fanPlane != "yz" && *fanPlane != "xz" {
		errExitf("Error: --fan-plane must be 'yz' or 'xz' (got %q)", *fanPlane)
	}

	epdSet := flagWasSet(fs, "epd")
	fnumSet := flagWasSet(fs, "fnum")
	epzSet := flagWasSet(fs, "epz")
	if epdSet && flagWasSet(fs, "pupil-model-diameter") {
		errExitf("Error: --epd and --pupil-model-diameter are mutually exclusive")
	}
	if fnumSet && (epdSet || flagWasSet(fs, "pupil-model-diameter")) {
		errExitf("Error: --fnum and --epd/--pupil-model-diameter are mutually exclusive")
	}
	if epzSet && flagWasSet(fs, "pupil-model-axial-position") {
		errExitf("Error: --epz and --pupil-model-axial-position are mutually exclusive")
	}
	if fnumSet && *fnum <= 0 {
		errExitf("Error: --fnum must be positive (got %g)", *fnum)
	}

	input := parseYAML[types.Input](data)

	if input.Chief == nil {
		errExitf("Error: 'chief' section is required")
	}

	pt := input.Chief.PassThrough
	if *passThrough > 0 {
		if pt == nil {
			pt = &types.PassThroughTarget{Surface: *passThrough, Coordinate: types.Vec3{}}
		} else {
			pt.Surface = *passThrough
		}
		input.Chief.StopSurface = *passThrough
	}

	// Resolve field definitions: the config selected by --config supplies its
	// own fields (a zoom position carries its own field angles / target image
	// heights), falling back to configs[0] and then the top-level chief section
	// — the same order as trace / psf / focus / asphere.
	fields := resolveConfigFieldDefs(input, configFlag)
	if len(fields) == 0 {
		errExitf("Error: 'fields' or 'field_angles' is required")
	}

	// Reference wavelength: --wl wins over chief.reference_wavelength, with
	// the built-in default as the final fallback. Always write the effective
	// value so every pipeline stage sees the same reference.
	wavelength := *wlFlag
	if !wlSet && input.Chief.ReferenceWavelength > 0 {
		wavelength = input.Chief.ReferenceWavelength
	}
	if wavelength <= 0 {
		wavelength = types.DefaultWavelength
	}
	input.Chief.ReferenceWavelength = wavelength
	configWavelengths := configWavelengthValues(input.Configs, configFlag)

	gc, _ := openGlass(&input, *glassDir)

	surfaces := configSurfaces(input.Configs, configFlag)
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
		flagWasSet(fs, "pupil-model-axial-position") || flagWasSet(fs, "pupil-model-diameter") {
		if input.Chief.PupilModel == nil {
			input.Chief.PupilModel = &types.PupilModelConfig{}
		}
		if epzSet {
			input.Chief.PupilModel.Mode = "virtual_entrance_pupil"
			input.Chief.PupilModel.AxialPosition = *epz
		} else {
			input.Chief.PupilModel.Mode = *pupilMode
			if flagWasSet(fs, "pupil-model-axial-position") {
				input.Chief.PupilModel.AxialPosition = *pupilAxialPos
			}
		}
		switch {
		case epdSet:
			input.Chief.PupilModel.Diameter = *epd
		case fnumSet:
			pResult := paraxial.Compute(selectedSys, wavelength, gc, 0, nil)
			if pResult.FocalLength == 0 {
				errExitf("Error: cannot compute EFL for --fnum conversion")
			}
			input.Chief.PupilModel.Diameter = math.Abs(pResult.FocalLength) / *fnum
		case flagWasSet(fs, "pupil-model-diameter"):
			input.Chief.PupilModel.Diameter = *pupilDiameter
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
	if flagWasSet(fs, "chief-ray") {
		input.Chief.ChiefRayDefinition = *rayDefinition
	}
	if err := chief.ValidateRayDefinition(input.Chief.StopSurface, input.Chief.PupilModel, input.Chief.ChiefRayDefinition); err != nil {
		errExitf("Error: %v", err)
	}

	fanCfg := resolveRayFanConfig(*rayFan, *fanPlane, fanRotation)

	// effective dumpMap: --clear-aperture or --marginal-rays implies grid_points
	dumpMap := input.Chief.DumpMap
	if *clearAperture || *marginalRays {
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
		margin := *clearApertureMarginMM
		envEngine := ray.NewEngine(gc, nil)
		surface.Precompute(surfaces)
		path := dls.BuildPath(surfaces)
		envelope := chief.BeamEnvelope(results, envEngine, surfaces, path, wavelength, pol)

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
	if *clearAperture && len(results) > 0 {
		if *clearApertureRays > 0 && *clearApertureRays != input.Chief.NumRays {
			// Re-trace with a denser grid so the beam footprint is accurate.
			results = chief.DetermineChiefRaysGridMode(
				selectedSys, fields, input.Chief.ReferenceSurface, *clearApertureRays,
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
		envelope := chief.BeamEnvelope(results, engine2, surfaces, path, wavelength, pol)

		refID := input.Chief.ReferenceSurface
		stopID := input.Chief.StopSurface
		for i := range surfaces {
			if surfaces[i].ID == refID || (stopID > 0 && surfaces[i].ID == stopID) || !surfaces[i].AutoAperture {
				continue
			}
			if e := envelope[surfaces[i].ID]; e > 0 {
				surfaces[i].Diameter = e*2 + 2**clearApertureMarginMM
			}
		}
	}

	// Build ChiefRayResult for YAML output
	chiefRays := make([]types.ChiefRayResult, len(results))
	for i, r := range results {
		cr := types.ChiefRayResult{
			FieldAngle:    r.FieldAngle,
			ChiefRay:      r.ChiefRay,
			ImageHeight:   r.ImageHeight,
			EntrancePupil: r.EntrancePupil,
			SpotStats:     r.SpotStats,
			RayFan:        r.RayFan,
			Wavelengths:   r.Wavelengths,
			PupilProbe:    r.ProbeOK,
			PupilProbeZ:   r.ProbeZ,
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
	if !*preserveRays {
		input.Rays = &types.RayInput{
			Polarization: pol,
			Rays:         rayList,
		}
	}

	// --- --marginal-rays: extract marginal rays from grid points ---
	if *marginalRays && len(results) > 0 {
		engine := ray.NewEngine(gc, nil)
		input.Rays.Rays = append(input.Rays.Rays, extractMarginalRays(results, input.Chief.StopSurface, engine, wavelength, surfaces, pol)...)
	}

	output := types.Output{
		Input:     input,
		ChiefRays: chiefRays,
	}
	if *preserveRays {
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

// floatList is a repeatable float64 flag value.
type floatList []float64

func (f *floatList) String() string {
	return fmt.Sprint([]float64(*f))
}

func (f *floatList) Set(v string) error {
	fv, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return err
	}
	*f = append(*f, fv)
	return nil
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
