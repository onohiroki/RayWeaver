package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/hiroki/rayweaver/internal/chief"
	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/importer"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

func runImport(data []byte) {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	format := fs.String("format", "", "zemax|oslo|codev")
	configID := fs.String("config-id", "config1", "")
	configName := fs.String("config-name", "Config1", "")
	noChief := fs.Bool("no-chief", false, "skip chief ray computation")
	glassDir := fs.String("glass-dir", "", "AGF glass catalog directory")
	fs.Parse(os.Args[2:])

	if *format == "" {
		errOut("Error: --format is required (zemax|oslo|codev)")
		os.Exit(1)
	}

	var result *importer.ParseResult
	var err error
	switch *format {
	case "zemax":
		result, err = importer.ParseZemax(string(data))
	case "oslo":
		result, err = importer.ParseOslo(string(data))
	case "codev":
		result, err = importer.ParseCodeV(string(data))
	default:
		errOut("Error: unknown format %q", *format)
		os.Exit(1)
	}
	if err != nil {
		errOut("Error: parse error: %v", err)
		os.Exit(1)
	}
	if len(result.Surfaces) == 0 {
		errOut("Error: no surfaces found in input")
		os.Exit(1)
	}

	if *glassDir != "" {
		agfGlasses, err := glass.LoadAGFDir(*glassDir)
		if err != nil {
			errOut("Error loading AGF files: %v", err)
			os.Exit(1)
		}
		result.GlassEntries = importer.EnhanceGlassEntriesFromAGF(
			result.GlassEntries, agfGlasses,
		)
	}

	lastID := result.Surfaces[len(result.Surfaces)-1].ID

	surfaces := result.Surfaces
	surface.Precompute(surfaces)

	gc := glass.NewCatalog()
	for _, g := range result.GlassEntries {
		gc.Add(g)
	}

	stopSurface := result.StopSurface
	if stopSurface <= 0 {
		stopSurface = surfaces[0].ID
	}

	if result.FNO > 0 {
		hasDiam := false
		for _, s := range surfaces {
			if s.Diameter > 0 {
				hasDiam = true
				break
			}
		}
		if !hasDiam {
			wl := firstWavelength(result.Wavelengths)
			pr := paraxial.Compute(types.System{Surfaces: surfaces}, wl, gc, 0, nil)
			if pr.FocalLength > 0 {
				epDiam := pr.FocalLength / result.FNO
				for i := range surfaces {
					if surfaces[i].ID == stopSurface {
						surfaces[i].Diameter = epDiam
						break
					}
				}
			}
		}
	}

	configIdx := importer.ConfigIndexes(result)
	if len(configIdx) == 0 {
		configIdx = []int{0}
	}

	// Per-config ray tracking: chief rays are resolved on the first config
	// (representative); every created config carries its own surfaces,
	// fields and wavelengths so downstream `chief`/`trace`/`optimize`
	// commands select a config with `--config`.
	var configs []types.Config
	for _, c := range configIdx {
		var cfgSurfaces []types.Surface
		if c == 0 {
			cfgSurfaces = surfaces
		} else {
			cfgSurfaces = importer.ConfigSurfaceSet(result, c)
		}
		cfgID := *configID
		cfgName := *configName
		if c != 0 {
			cfgID = fmt.Sprintf("config%d", c)
			cfgName = fmt.Sprintf("Config%d", c)
		}
		configs = append(configs, types.Config{
			ID:          cfgID,
			Name:        cfgName,
			Weight:      1.0,
			Active:      true,
			Fields:      result.Fields,
			Wavelengths: result.Wavelengths,
			RayPaths: []types.RayPath{{
				ObjectSurface: 0,
				ImageSurface:  lastID,
				StopSurface:   stopSurface,
			}},
			Surfaces: cfgSurfaces,
			Merit: &types.MeritFunction{
				Type: "spot_rms",
				Terms: []types.MeritTerm{{
					Kind:       "spot_rms",
					Field:      0,
					Wavelength: firstWavelength(result.Wavelengths),
					Weight:     1.0,
				}},
			},
		})
	}

	output := types.Input{
		Version: 1,
		GlassCatalog: &types.GlassCatalog{
			Entries: result.GlassEntries,
		},
		System: types.System{},
		Optimization: &types.OptimizationConfig{
			Method:          "dls",
			MaxIter:         0,
			Tol:             1e-6,
			Epsilon:         1e-6,
			SharedVariables: []types.SharedVariable{},
			LocalVariables:  []types.LocalVariableDef{},
			Constraints:     []types.ConstraintOperand{},
		},
		Chief: &types.ChiefInput{
			ReferenceSurface: lastID,
			StopSurface:      stopSurface,
			NumRays:          128,
		},
		Configs: configs,
	}

	outputOut := types.Output{
		Input: output,
	}

	// Chief rays are computed on the representative (first) config.
	chiefSurfaces := configs[0].Surfaces
	chiefFields := make([]types.FieldDef, len(result.Fields))
	for i, f := range result.Fields {
		chiefFields[i] = types.FieldDef{
			Angle:       f.AngleDeg,
			ImageHeight: f.ImageHeight,
		}
	}

	if !*noChief && len(result.Fields) > 0 {
		wavelength := firstWavelength(result.Wavelengths)
		pol := types.NewCircularJones(true)

		pt := &types.PassThroughTarget{
			Surface:    stopSurface,
			Coordinate: types.Vec3{},
		}

		selectedSys := types.System{Surfaces: chiefSurfaces}
		surface.Precompute(chiefSurfaces)

		chiefResults := chief.DetermineChiefRaysGrid(
			selectedSys,
			chiefFields,
			lastID,
			128,
			gc,
			pol,
			wavelength,
			true,
			types.GridPolar,
			pt,
			nil,
			nil,
		)

		outputOut.ChiefRays = make([]types.ChiefRayResult, len(chiefResults))
		rayList := make([]types.Ray, 0, len(chiefResults)*3)
		path := dls.BuildPath(chiefSurfaces)

		for fi, r := range chiefResults {
			cr := types.ChiefRayResult{
				FieldAngle:    r.FieldAngle,
				ChiefRay:      r.ChiefRay,
				ImageHeight:   r.ImageHeight,
				EntrancePupil: r.EntrancePupil,
				SpotStats:     r.SpotStats,
			}
			if len(r.GridPoints) > 0 {
				cr.GridPoints = r.GridPoints
			}
			outputOut.ChiefRays[fi] = cr

			chiefRay := r.ChiefRay
			chiefRay.ID = fmt.Sprintf("chief_%.0fdeg", r.FieldAngle)
			chiefRay.Path = path
			chiefRay.Jones = pol
			rayList = append(rayList, chiefRay)

			rayList = append(rayList, chief.MarginalRays(fi, r, stopSurface, surfaces, wavelength, path, pol)...)
		}

		outputOut.Rays = &types.RayInput{
			Polarization: pol,
			Rays:         rayList,
		}

		outputOut.Chief.ReferenceSurface = lastID
		outputOut.Chief.StopSurface = stopSurface
		outputOut.Chief.NumRays = 128
		outputOut.Chief.Fields = chiefFields
		outputOut.Chief.PassThrough = pt
	}

	writeYAML(&outputOut)
}

func firstWavelength(wavelengths []types.WavelengthItem) float64 {
	for _, w := range wavelengths {
		if w.Primary && w.Value > 0 {
			return w.Value
		}
	}
	for _, w := range wavelengths {
		if w.Value > 0 {
			return w.Value
		}
	}
	return types.DefaultWavelength
}
