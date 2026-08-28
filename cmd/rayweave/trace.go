package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

func runTraceSingle(data []byte) {
	// Strip "single" from os.Args so the flag parser doesn't choke on it.
	args := os.Args[2:]
	for i, a := range args {
		if a == "single" {
			args = append(args[:i], args[i+1:]...)
			break
		}
	}
	fs := flag.NewFlagSet("trace single", flag.ExitOnError)
	originFlag := fs.String("origin", "", "ray origin [x,y,z] mm")
	directionFlag := fs.String("direction", "", "ray direction [dx,dy,dz]")
	aimFlag := fs.String("aim", "", "aim target [x,y,z]")
	angleYZFlag := fs.Float64("angle-yz", 0, "incidence angle in YZ plane (degrees)")
	passThroughFlag := fs.String("pass-through", "", "pass through surface:Y:X")
	pathFlag := fs.String("path", "", "surface path (comma-separated IDs)")
	wavelengthFlag := fs.Float64("wavelength", 0, "wavelength (mm)")
	idFlag := fs.String("id", "", "ray ID (default: trace_single)")
	lenientFlag := fs.Bool("lenient", false, "skip aperture/glass-path checks")
	detailsFlag := fs.Bool("details", false, "add per-surface detail to output YAML")
	verboseFlag := fs.Bool("verbose", false, "print per-surface info to stderr")
	configFlag := fs.String("config", "", "config ID (multi-config mode)")
	glassDir := fs.String("glass-dir", "", "AGF glass catalog directory")
	fs.Parse(args)

	input := parseYAML[types.Input](data)

	// Resolve wavelength: CLI > YAML > chief.reference_wavelength > default.
	wavelength := *wavelengthFlag
	if !flagWasSet(fs, "wavelength") {
		if input.TraceSingle != nil && input.TraceSingle.Wavelength > 0 {
			wavelength = input.TraceSingle.Wavelength
		}
	}
	if wavelength <= 0 {
		wavelength = effectiveReferenceWavelength(input.Chief)
	}

	// Resolve ray ID.
	rayID := *idFlag
	if !flagWasSet(fs, "id") {
		if input.TraceSingle != nil && input.TraceSingle.ID != "" {
			rayID = input.TraceSingle.ID
		}
	}
	if rayID == "" {
		rayID = "trace_single"
	}

	// Resolve lenient.
	lenient := *lenientFlag
	if !flagWasSet(fs, "lenient") && input.TraceSingle != nil {
		lenient = input.TraceSingle.Lenient
	}

	// Resolve details.
	details := *detailsFlag
	if !flagWasSet(fs, "details") && input.TraceSingle != nil {
		details = input.TraceSingle.Details
	}

	// --- Resolve ray specification (CLI > YAML) ---
	rayCfg := input.TraceSingle
	origin, direction, aim, passThrough := resolveRaySpec(fs, rayCfg, originFlag, directionFlag, aimFlag, angleYZFlag, passThroughFlag)

	// --- Resolve path ---
	path := resolvePath(fs, rayCfg, pathFlag, input.Configs, configFlag)

	// --- Build types.Ray ---
	// Default to right-circular polarization (matching the pipeline `trace`
	// default) unless the input `rays` section carries an explicit one.
	pol := types.NewCircularJones(true)
	if input.Rays != nil {
		pol = input.Rays.Polarization
	}
	r := types.Ray{
		ID:         rayID,
		Wavelength: wavelength,
		Path:       path,
		Lenient:    lenient,
		Jones:      pol,
	}
	r.Initial.Origin = origin

	if aim != nil {
		r.Aim = aim
	} else if passThrough != nil {
		r.PassThrough = passThrough
	} else {
		r.Initial.Direction = direction
	}

	// --- Write-back ---
	if anyFlagSet(fs, "origin", "direction", "aim", "angle-yz", "pass-through",
		"path", "wavelength", "id", "lenient", "details") {
		if input.TraceSingle == nil {
			input.TraceSingle = &types.RayTraceConfig{}
		}
		if flagWasSet(fs, "origin") {
			input.TraceSingle.Origin = []float64{origin.X, origin.Y, origin.Z}
		}
		if flagWasSet(fs, "direction") {
			input.TraceSingle.Direction = []float64{direction.X, direction.Y, direction.Z}
		}
		if flagWasSet(fs, "aim") && aim != nil {
			input.TraceSingle.Aim = []float64{aim.X, aim.Y, aim.Z}
		}
		if flagWasSet(fs, "angle-yz") {
			v := *angleYZFlag
			input.TraceSingle.AngleYZ = &v
		}
		if flagWasSet(fs, "pass-through") {
			input.TraceSingle.PassThrough = parseFloatList(*passThroughFlag, "pass-through")
		}
		if flagWasSet(fs, "path") {
			input.TraceSingle.Path = path
		}
		if flagWasSet(fs, "wavelength") {
			input.TraceSingle.Wavelength = wavelength
		}
		if flagWasSet(fs, "id") {
			input.TraceSingle.ID = rayID
		}
		if flagWasSet(fs, "lenient") {
			input.TraceSingle.Lenient = lenient
		}
		if flagWasSet(fs, "details") {
			input.TraceSingle.Details = details
		}
	}
	writeBackGlassDir(&input, *glassDir)

	// --- Trace ---
	gc, cc := loadCatalogs(&input, *glassDir)
	surfaces := configSurfaces(input.Configs, configFlag)
	surface.Precompute(surfaces)

	engine := ray.NewEngine(gc, cc)
	ray.ResolveRay(&r, surfaces, engine)
	result := engine.TraceRay(r, surfaces, details)

	if result.Error != "" {
		errOut("Warning: ray %q error: %s", rayID, result.Error)
	}

	// --- Verbose output (stderr) ---
	if *verboseFlag {
		printTraceSingleVerbose(result)
	}

	// --- Output (overwrite results) ---
	output := types.Output{Input: input, Results: []types.RayResult{result}}
	withOutputMetadata(&output.Input, "trace", subcmdArgs())
	writeYAML(&output)
}

// resolveRaySpec resolves the ray specification from CLI flags or YAML config.
func resolveRaySpec(fs *flag.FlagSet, cfg *types.RayTraceConfig,
	originFlag, directionFlag, aimFlag *string, angleYZFlag *float64,
	passThroughFlag *string,
) (origin types.Vec3, direction types.Vec3, aim *types.Vec3, passThrough *types.PassThroughTarget) {

	// --- Origin ---
	originStr := *originFlag
	if !flagWasSet(fs, "origin") && cfg != nil && len(cfg.Origin) >= 3 {
		originStr = fmt.Sprintf("%f,%f,%f", cfg.Origin[0], cfg.Origin[1], cfg.Origin[2])
	}
	if originStr != "" {
		parts := parseFloatList(originStr, "origin")
		if len(parts) >= 3 {
			origin = types.Vec3{X: parts[0], Y: parts[1], Z: parts[2]}
		}
	}

	// --- Direction / Aim / AngleYZ / PassThrough ---
	directionStr := *directionFlag
	aimStr := *aimFlag
	angleYZ := *angleYZFlag
	passThroughStr := *passThroughFlag

	if !flagWasSet(fs, "direction") && cfg != nil && len(cfg.Direction) >= 3 {
		directionStr = fmt.Sprintf("%f,%f,%f", cfg.Direction[0], cfg.Direction[1], cfg.Direction[2])
	}
	if !flagWasSet(fs, "aim") && cfg != nil && len(cfg.Aim) >= 3 {
		aimStr = fmt.Sprintf("%f,%f,%f", cfg.Aim[0], cfg.Aim[1], cfg.Aim[2])
	}
	if !flagWasSet(fs, "angle-yz") && cfg != nil && cfg.AngleYZ != nil {
		angleYZ = *cfg.AngleYZ
	}
	if !flagWasSet(fs, "pass-through") && cfg != nil && len(cfg.PassThrough) >= 3 {
		passThroughStr = fmt.Sprintf("%d:%f:%f", int(cfg.PassThrough[0]), cfg.PassThrough[1], cfg.PassThrough[2])
	}

	// Determine which method is active (CLI wins; aim > direction > angle-yz > pass-through).
	switch {
	case flagWasSet(fs, "aim") || (cfg != nil && len(cfg.Aim) >= 3 && !flagWasSet(fs, "direction") && !flagWasSet(fs, "angle-yz")):
		parts := parseFloatList(aimStr, "aim")
		if len(parts) >= 3 {
			v := types.Vec3{X: parts[0], Y: parts[1], Z: parts[2]}
			aim = &v
		}
	case flagWasSet(fs, "direction") || (cfg != nil && len(cfg.Direction) >= 3 && !flagWasSet(fs, "angle-yz")):
		parts := parseFloatList(directionStr, "direction")
		if len(parts) >= 3 {
			direction = types.Vec3{X: parts[0], Y: parts[1], Z: parts[2]}
		}
	case flagWasSet(fs, "angle-yz") || (cfg != nil && cfg.AngleYZ != nil):
		rad := angleYZ * math.Pi / 180.0
		direction = types.Vec3{X: 0, Y: math.Sin(rad), Z: math.Cos(rad)}
	case flagWasSet(fs, "pass-through") || (cfg != nil && len(cfg.PassThrough) >= 3):
		rad := angleYZ * math.Pi / 180.0
		direction = types.Vec3{X: 0, Y: math.Sin(rad), Z: math.Cos(rad)}
		parts := parseFloatList(passThroughStr, "pass-through")
		if len(parts) >= 3 {
			surfaceID := int(parts[0])
			passThrough = &types.PassThroughTarget{
				Surface:    surfaceID,
				Coordinate: types.Vec3{Y: parts[1], X: parts[2]},
			}
		}
	default:
		direction = types.Vec3{X: 0, Y: 0, Z: 1}
	}

	return origin, direction, aim, passThrough
}

// resolvePath resolves the surface path from CLI flag or YAML config.
func resolvePath(fs *flag.FlagSet, cfg *types.RayTraceConfig, pathFlag *string,
	configs []types.Config, configFlag *string,
) []int {
	pathStr := *pathFlag
	if !flagWasSet(fs, "path") && cfg != nil && len(cfg.Path) > 0 {
		pathStr = intSliceToString(cfg.Path)
	}
	if pathStr != "" {
		return parseIntList(pathStr, "path")
	}
	// Default: sequential [0, 1, ..., N]
	surfaces := configSurfaces(configs, configFlag)
	path := make([]int, len(surfaces)+1)
	for i := range path {
		path[i] = i
	}
	return path
}

// printTraceSingleVerbose prints per-surface detail to stderr.
func printTraceSingleVerbose(result types.RayResult) {
	for _, sr := range result.Surfaces {
		interact := string(sr.Interaction)
		surfID := sr.SurfaceID
		y := sr.Position.Y
		z := sr.Position.Z

		if sr.AngleOfIncidence != nil && sr.N1 != nil && sr.N2 != nil {
			n1 := *sr.N1
			n2 := *sr.N2
			aoi := *sr.AngleOfIncidence
			if sr.CoatingRs != nil {
				fmt.Fprintf(os.Stderr, "  s%-2d  %9s  y=%8.4f  z=%8.4f  \u03b8=%6.2f\u00b0  n1=%.3f  n2=%.3f  coating_Rs=%.4f  Rp=%.4f  Ts=%.4f  Tp=%.4f\n",
					surfID, interact, y, z, aoi, n1, n2,
					*sr.CoatingRs, *sr.CoatingRp, *sr.CoatingTs, *sr.CoatingTp)
			} else if sr.Rs != nil {
				fmt.Fprintf(os.Stderr, "  s%-2d  %9s  y=%8.4f  z=%8.4f  \u03b8=%6.2f\u00b0  n1=%.3f  n2=%.3f  Rs=%.4f  Rp=%.4f  Ts=%.4f  Tp=%.4f\n",
					surfID, interact, y, z, aoi, n1, n2,
					*sr.Rs, *sr.Rp, *sr.Ts, *sr.Tp)
			}
		} else {
			fmt.Fprintf(os.Stderr, "  s%-2d  %9s  y=%8.4f  z=%8.4f\n", surfID, interact, y, z)
		}
	}
	fmt.Fprintf(os.Stderr, "  OPL_total = %.4f mm  Is=%.4f  Ip=%.4f\n",
		result.OPLTotal, result.IntensityS, result.IntensityP)
}

// parseIntList parses a comma-separated list of ints.
func parseIntList(s, name string) []int {
	parts := strings.Split(s, ",")
	var result []int
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v, err := strconv.Atoi(p)
		if err != nil {
			errOut("Error: invalid %s value %q: %v", name, p, err)
			os.Exit(1)
		}
		result = append(result, v)
	}
	return result
}

// intSliceToString formats an int slice as a comma-separated string.
func intSliceToString(vals []int) string {
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, ",")
}
