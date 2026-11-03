package main

import (
	"flag"
	"os"

	"github.com/hiroki/rayweaver/internal/clispec"
	"github.com/hiroki/rayweaver/internal/types"
	"github.com/hiroki/rayweaver/internal/vignette"
)

func runVignette(data []byte) {
	fs := flag.NewFlagSet("vignette", flag.ExitOnError)
	opts := clispec.Bind(clispec.Vignette, fs)
	if err := opts.Parse(os.Args[2:]); err != nil {
		errExitf("Error: %v", err)
	}
	configFlag := opts.String("config")

	input := parseYAML[types.Input](data)

	if input.Chief == nil {
		errExitf("Error: 'chief' section is required")
	}

	// Resolve effective settings. The reference wavelength is system-wide and is
	// shared with chief and paraxial.
	vcfg := input.Vignette
	if vcfg == nil {
		vcfg = &types.VignetteConfig{}
	}
	iterationsEff := opts.Int("iterations")
	if !opts.Set("iterations") && vcfg.Iterations > 0 {
		iterationsEff = vcfg.Iterations
	}
	minGlassPathEff := opts.Float("min-glass-path")
	if !opts.Set("min-glass-path") && vcfg.MinGlassPath > 0 {
		minGlassPathEff = vcfg.MinGlassPath
	}
	marginMMEff := opts.Float("margin-mm")
	if !opts.Set("margin-mm") && vcfg.MarginMM > 0 {
		marginMMEff = vcfg.MarginMM
	}
	wlEff := effectiveReferenceWavelength(input.Chief)
	if opts.Set("wl") && opts.Float("wl") > 0 {
		wlEff = opts.Float("wl")
	}
	input.Chief.ReferenceWavelength = wlEff
	// Principle 3: the table writes the given settings back into vignette:; the
	// reference wavelength is shared with chief/paraxial and is always written.
	opts.WriteBack(&input)
	if opts.Set("wl") {
		input.Chief.ReferenceWavelength = wlEff
		// Preserve the pre-table output shape: any of these flags used to
		// materialise the (possibly empty) vignette section.
		if input.Vignette == nil {
			input.Vignette = &types.VignetteConfig{}
		}
	}

	gc, _ := openGlass(&input, opts.String("glass-dir"))

	surfaces := surfacesFor(input.Configs, &configFlag)

	// Snapshot the input auto_aperture diameters for the before/after report.
	beforeByID := make(map[int]float64, len(surfaces))
	for _, s := range surfaces {
		beforeByID[s.ID] = s.Diameter
	}

	// Resolve field definitions like `chief`: the selected config's own fields
	// win (see resolveConfigFieldDefs).
	fields := resolveConfigFieldDefs(input, &configFlag)
	if len(fields) == 0 {
		errExitf("Error: 'fields' or 'field_angles' is required")
	}

	pol := types.NewCircularJones(true)
	if input.Rays != nil {
		pol = input.Rays.Polarization
	}

	res := vignette.Run(surfaces, vignette.Options{
		Fields:        fields,
		RefSurface:    input.Chief.ReferenceSurface,
		StopSurface:   input.Chief.StopSurface,
		NumRays:       input.Chief.NumRays,
		GridType:      input.Chief.GridType,
		Wavelength:    wlEff,
		MinGlassPath:  minGlassPathEff,
		MarginMM:      marginMMEff,
		Iterations:    iterationsEff,
		RayDefinition: chiefRayDefinition(input),
	}, gc)

	// Write back the settled diameters and applied min_glass_path values.
	resByID := make(map[int]types.Surface, len(res.Surfaces))
	for _, s := range res.Surfaces {
		resByID[s.ID] = s
	}
	for i := range surfaces {
		if s, ok := resByID[surfaces[i].ID]; ok {
			surfaces[i].Diameter = s.Diameter
			surfaces[i].MinGlassPath = s.MinGlassPath
		}
	}

	// A field the solve dropped (its declared image_height is not the one the
	// system forms) is reported, not silently missing from chief_rays and
	// vignetting_result.fields.
	warnFieldSkips("", res.FieldSolutions)

	chiefRays := make([]types.ChiefRayResult, len(res.ChiefRays))
	rayList := make([]types.Ray, 0, 2*len(res.Fields))
	for i, r := range res.ChiefRays {
		cr := types.ChiefRayResult{
			FieldAngle:    r.FieldAngle,
			ChiefRay:      r.ChiefRay,
			ImageHeight:   r.ImageHeight,
			EntrancePupil: r.EntrancePupil,
			ExitPupil:     r.ExitPupil,
			SpotStats:     r.SpotStats,
			PupilProbe:    r.ProbeOK,
			PupilProbeZ:   r.ProbeZ,
			// Nil for a field that was not clipped below the grid's survival
			// threshold; `omitempty` then leaves it out of the pipeline YAML.
			EffectiveVignetting: r.EffectiveVignetting,
		}
		if input.Chief.DumpMap && len(r.GridPoints) > 0 {
			cr.GridPoints = r.GridPoints
		}
		chiefRays[i] = cr
	}

	// Chief rays live in chief_rays[].chief_ray (single source); the rays
	// section carries only the marginal rays (plus polarization) so the output
	// stays pipe-compatible without duplicating every chief ray.
	for _, f := range res.Fields {
		if f.MarginalUpper != nil {
			rayList = append(rayList, *f.MarginalUpper)
		}
		if f.MarginalLower != nil {
			rayList = append(rayList, *f.MarginalLower)
		}
	}

	input.Rays = &types.RayInput{
		Polarization: pol,
		Rays:         rayList,
	}

	vr := &types.VignettingResult{
		Iterations:   res.Iterations,
		MinGlassPath: res.MinGlassPath,
		StopSurface:  res.StopSurface,
		Fields:       make([]types.VignettingField, len(res.Fields)),
	}
	for i, f := range res.Fields {
		vr.Fields[i] = f.VignettingField
	}
	for _, s := range res.Surfaces {
		if s.AutoAperture {
			vr.Diameters = append(vr.Diameters, types.DiameterState{
				SurfaceID: s.ID,
				Before:    beforeByID[s.ID],
				After:     s.Diameter,
			})
		}
	}

	output := types.Output{
		Input:      input,
		ChiefRays:  chiefRays,
		Vignetting: vr,
	}
	withOutputMetadata(&output.Input, "vignette", subcmdArgs())

	writeYAML(&output)
}
