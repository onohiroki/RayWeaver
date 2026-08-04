package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
	"github.com/hiroki/rayweaver/internal/vignette"
)

func runVignette(data []byte) {
	fs := flag.NewFlagSet("vignette", flag.ExitOnError)
	iterations := fs.Int("iterations", 0, "number of diameter/pupil passes (default 3)")
	minGlassPath := fs.Float64("min-glass-path", 0.5, "minimum glass path (edge thickness) below which a ray fails, applied to every glass element (mm)")
	marginMM := fs.Float64("margin-mm", 0.2, "clearance added to each side of the beam footprint when sizing auto_aperture surfaces (mm)")
	wlFlag := fs.Float64("wl", types.DefaultWavelength, "wavelength (mm) for grid ray tracing")
	configFlag := fs.String("config", "", "select config by id (multi-config mode)")
	glassDir := fs.String("glass-dir", "", "AGF glass catalog directory")
	fs.Parse(os.Args[2:])

	input := parseYAML[types.Input](data)

	if input.Chief == nil {
		errOut("Error: 'chief' section is required")
		os.Exit(1)
	}

	gc, _ := loadCatalogs(&input, *glassDir)

	surfaces := configSurfaces(input.Configs, configFlag)
	if len(surfaces) == 0 {
		errOut("Error: no surfaces to process")
		os.Exit(1)
	}
	surface.Precompute(surfaces)

	// Snapshot the input auto_aperture diameters for the before/after report.
	beforeByID := make(map[int]float64, len(surfaces))
	for _, s := range surfaces {
		beforeByID[s.ID] = s.Diameter
	}

	// Resolve field definitions like `chief`.
	var fields []types.FieldDef
	if len(input.Chief.Fields) > 0 {
		fields = input.Chief.Fields
	} else {
		for _, a := range input.Chief.FieldAngles {
			fields = append(fields, types.FieldDef{Angle: a, Direction: []float64{0, 1}})
		}
	}
	if len(fields) == 0 {
		errOut("Error: 'fields' or 'field_angles' is required")
		os.Exit(1)
	}

	pol := types.NewCircularJones(true)
	if input.Rays != nil {
		pol = input.Rays.Polarization
	}

	res := vignette.Run(surfaces, vignette.Options{
		Fields:       fields,
		RefSurface:   input.Chief.ReferenceSurface,
		StopSurface:  input.Chief.StopSurface,
		NumRays:      input.Chief.NumRays,
		GridType:     input.Chief.GridType,
		Wavelength:   *wlFlag,
		MinGlassPath: *minGlassPath,
		MarginMM:     *marginMM,
		Iterations:   *iterations,
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

	path := dls.BuildPath(surfaces)

	chiefRays := make([]types.ChiefRayResult, len(res.ChiefRays))
	rayList := make([]types.Ray, 0, len(res.ChiefRays)+2*len(res.Fields))
	for i, r := range res.ChiefRays {
		cr := types.ChiefRayResult{
			FieldAngle:    r.FieldAngle,
			ChiefRay:      r.ChiefRay,
			ImageHeight:   r.ImageHeight,
			EntrancePupil: r.EntrancePupil,
			ExitPupil:     r.ExitPupil,
			SpotStats:     r.SpotStats,
		}
		if input.Chief.DumpMap && len(r.GridPoints) > 0 {
			cr.GridPoints = r.GridPoints
		}
		chiefRays[i] = cr

		chiefRay := r.ChiefRay
		chiefRay.ID = fmt.Sprintf("chief_%.0fdeg", r.FieldAngle)
		chiefRay.Path = path
		chiefRay.Jones = pol
		rayList = append(rayList, chiefRay)
	}

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

	writeYAML(&output)
}
