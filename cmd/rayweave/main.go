package main

import (
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/hiroki/rayweaver/internal/chief"
	"github.com/hiroki/rayweaver/internal/coating"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
	"gopkg.in/yaml.v3"
)

// currentCmd holds the active subcommand so stderr output can be attributed
// to the right pipeline stage (e.g. "rayweave[chief]:").
var currentCmd string

// errOut writes a tagged line to stderr. The tag identifies the subcommand
// that produced the message, which is essential when commands are piped
// (e.g. "rayweave chief | rayweave trace | rayweave plot").
func errOut(format string, args ...any) {
	prefix := "rayweave"
	if currentCmd != "" {
		prefix = "rayweave[" + currentCmd + "]"
	}
	fmt.Fprintf(os.Stderr, prefix+": "+format+"\n", args...)
}

func main() {
	args := os.Args[1:]

	// Help handling — no stdin required
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		cmd := ""
		if len(args) >= 2 {
			cmd = args[1]
		}
		printHelp(cmd)
		return
	}
	if args[0] == "help" {
		cmd := ""
		if len(args) >= 2 {
			cmd = args[1]
		}
		printHelp(cmd)
		return
	}

	// Subcommand help (no stdin required) — check before any flag parsing
	for _, a := range args[1:] {
		if a == "--help" || a == "-h" {
			printHelp(args[0])
			return
		}
	}

	// Parse optimize-specific flags before stdin reading
	optVerbose := false
	optLogFile := ""
	optGlassDir := ""
	subcommand := args[0]
	currentCmd = subcommand
	if subcommand == "optimize" {
		fs := flag.NewFlagSet("optimize", flag.ContinueOnError)
		fs.BoolVar(&optVerbose, "verbose", false, "print per-iteration progress to stderr")
		fs.StringVar(&optLogFile, "log", "", "write per-iteration progress to file (JSONL)")
		fs.StringVar(&optGlassDir, "glass-dir", "", "AGF glass catalog directory")
		fs.Parse(args[1:])
		args = append([]string{"optimize"}, fs.Args()...)
	}

	data, err := readStdin()
	if err != nil {
		errOut("Error reading stdin: %v", err)
		os.Exit(1)
	}

	switch subcommand {
	case "chief":
		runChief(data)
	case "trace":
		runTrace(data)
	case "paraxial":
		runParaxial(data)
	case "tmm":
		runTMM(data)
	case "plot":
		runPlot(data)
	case "optimize":
		runOptimize(data, optVerbose, optLogFile, optGlassDir)
	case "import":
		runImport(data)
	default:
		errOut("Error: unknown subcommand %q", subcommand)
		errOut("Run 'rayweave --help' for usage.")
		os.Exit(1)
	}
}

func printHelp(cmd string) {
	switch cmd {
	case "chief":
		fmt.Print(`Usage: rayweave chief [--config ID] [--pass-through N] < system.yaml

Determines chief rays for each field.

Options:
  --config ID          select config by id (multi-config mode)
  --pass-through N     constrain chief ray to pass through (0,0) centre of
                         surface N (overrides YAML pass_through.surface)
  --clear-aperture     trace grid rays (all fields) through every surface and
                         set surfaces[].diameter = 2x max radial extent
                         using entrance-pupil-based beam diameter
  --clear-aperture-margin 2.0   beam diameter multiplier relative to entrance
                         pupil (default 2 = 2× entrance pupil diameter)
  --marginal-rays      extract marginal (max/min) rays from grid points and
                         append them for piping into trace/plot
  --ray-fan            compute ray fan (transverse aberration) for each field
                         (YZ + XZ planes, all fields)
  --fan-plane yz|xz    compute only the YZ (meridional) or XZ (sagittal) fan
                         (implies --ray-fan)
  --fan-rotation DEG   compute fan(s) in planes rotated by DEG around Z
                         (0 = XZ, 90 = YZ; implies --ray-fan; repeatable or
                         space-separated: --fan-rotation 0 45 90)
  --wl 0.00058756      reference wavelength (mm)

Input YAML — chief section:
  fields:
    - angle: 10.0              # field angle (degrees)
      direction: [0, 1]        # [dx, dy] field vector (default [0, 1])
    - image_height: 17.666     # or specify by image height (mm)
    - height: 10.0             # or object height (mm, finite conjugate)
      object_z: -500           #   object plane Z (default -1000)

  reference_surface: 8         # surface ID for centroid/image height
  num_rays: 512                # pupil samples (≈ √n × √n)
  grid_type: polar             # pupil grid: polar | square | hex
  dump_map: false              # output per-ray spot data (grid_points)

  pass_through:                # optional: constrain chief ray to pass
    surface: 3                 #   through a specific surface coordinate
    coordinate: [0, 0, 0]      #   (default [0, 0, 0] = surface centre)

Output: augmented YAML with chief_rays[] section.
  Pipe into "rayweave trace" to trace each chief ray through the system.

Without pass_through, the chief ray passes through the spot centroid on
  the reference surface.  This definition is robust during optimisation
  where the stop may be ill-defined.

With pass_through, the chief ray is defined as the ray from the field
  that passes through the given coordinate on the given surface
  (traditional "stop-centre" definition).

See also: samples/us2645157.yaml
`)
	case "trace":
		fmt.Print(`Usage: rayweave trace < system.yaml

Traces one or more rays through the system.

Input YAML — rays section:
  polarization: [1, 0, 0, 1]    # Jones vector [ReEx, ImEx, ReEy, ImEy]
  rays:
    - id: "my_ray"
      wavelength: 0.00058756    # mm
      initial:
        origin: [0, 0, -100]    # [x, y, z] start point (mm)
        direction: [0, 0.1, 1]  # [dx, dy, dz] direction vector
      path: [0, 1, 2, ..., N]   # surface IDs to trace (0 = object)

  Alternative ray definitions:
    aim: [x, y, z]              # set direction toward target point
    pass_through:
      surface: 5                # find origin (or direction) so the ray
      coordinate: [0, 8.9, 0]   #   passes through (x, y, z) on surface N
      variable: "origin"        #   "origin" (default) or "direction"

Output: YAML with results[] array.  Each result contains
  per-surface data (position, normal, Fresnel coefficients).

The "chief" subcommand outputs YAML that can be piped directly
  into "rayweave trace".
`)
	case "paraxial":
		fmt.Print(`Usage: rayweave paraxial [--config ID] < system.yaml

Performs paraxial (first-order) ray trace and cardinal analysis.

Options:
  --config ID          select config by id (multi-config mode)

Input: standard system YAML (glass_catalog + surfaces)
Optional input:
  paraxial:
    object_height: 10.0        # object height in mm (0 = infinite conjugate)

When piped after "rayweave chief", uses chief_rays[].entrance_pupil
  for entrance pupil data and chief_rays[].field_angle for FoV.

Output: augmented YAML with paraxial_result: section:

  object_space_index: 1        # refractive index in object space
  image_space_index: 1         # refractive index in image space
  entrance_pupil_diameter: ... # mm
  entrance_pupil_location: ... # mm from first surface
  inf_conj_image_space_f/#: ...# f-number (infinite conjugate)
  image_space_f/#: ...         # working f-number
  focal_length: ...            # effective focal length (mm)
  magnification: ...           # lateral mag (finite conjugate)
  minification: ...            # 1/|mag|
  exit_pupil_location: ...     # mm from last surface
  exit_pupil_diameter: ...     # mm
  half_angle_of_view: ...      # degrees
  total_track: ...             # mm (first to last surface)
  first_focal_length: ...      # mm
  first_principal_focus: ...   # mm from first surface
  first_principal_point: ...   # mm from first surface
  second_focal_length: ...     # mm
  second_principal_focus: ...  # BFL in mm from last surface
  second_principal_point: ...  # mm from last surface

See: samples/us2645157.yaml (reference values)
`)
	case "plot":
		fmt.Print(`Usage: rayweave plot [-o file.svg|.png] [--config ID] [--lens-width 0.1] [--ray-width 0.1] < input.yaml

Generates a cross-section drawing (SVG or PNG) of the lens
system with ray paths overlaid.

Options:
  -o, --output file.svg   output file (.svg or .png; default: stdout = SVG)
  --config ID          select a config by id (multi-config mode)
  --lens-width 0.1     lens body stroke width
  --ray-width 0.1      ray path stroke width
  --scale 0            SVG/PNG scale factor (0 = auto)
  --right-margin 20    right-side margin beyond image plane (% of lens length)

Input: YAML with system surfaces + optional results[] and chief_rays[].
Pipe after "rayweave trace" for ray paths:
  cat system.yaml | rayweave chief | rayweave trace | rayweave plot -o lens.svg
  cat system.yaml | rayweave chief | rayweave trace | rayweave plot -o lens.png

In multi-config mode, use --config to select which config to draw:
  cat result.yaml | rayweave plot --config wide -o wide.svg
  cat result.yaml | rayweave plot --config tele -o tele.png

Glass types are colour-coded using the nd/vd values from the
glass_catalog section.  Ray colours follow the field angle
(low = blue, high = red).

Note: PNG output uses golang.org/x/image/vector for rasterization
  with anti-aliasing.  No external tools required.
`)
	case "optimize":
		fmt.Print(`Usage: rayweave optimize [--verbose] [--log FILE] < input.yaml

DLS (Damped Least Squares) optimization of lens surfaces.

Options:
  --verbose        print per-iteration progress to stderr (JSONL)
  --log FILE       write per-iteration progress to FILE (JSONL)

Input YAML — optimization section:

Single-config mode:
  optimization:
    method: dls
    variables:
      - name: s2_curvature
        target:
          type: surface
          id: 2
          param: curvature
        min: -0.2
        max: 0.2
        active: true

Multi-config mode (shared/local variables):
  optimization:
    method: dls
    shared_variables:
      - name: group2_shift
        min: -5.0
        max: 5.0
        active: true
        bindings:
          - config: wide
            id: 3
            param: thickness
            scale: 1.0
            offset: 0.0
    local_variables:
      - name: wide_extra_space
        config: wide
        target:
          type: surface
          id: 4
          param: thickness
        min: 0.1
        max: 50.0
        active: true

Configs each have their own surfaces/fields/wavelengths/merit:
  configs:
    - id: wide
      name: Wide
      weight: 1.0
      active: true
      fields: [...]
      wavelengths: [...]
      surfaces: [...]
      merit:
        type: spot_rms
        terms: [...]
    - id: tele
      name: Tele
      weight: 1.0
      active: true
      fields: [...]
      wavelengths: [...]
      surfaces: [...]
      merit:
        type: spot_rms
        terms: [...]

Merit terms are evaluated per-config and summed weighted by config weight.
A CONF operand selects which config's merit terms are active for each rule.

Output: optimized YAML with updated surface parameters in each config.
 `)
	case "import":
		fmt.Print(`Usage: rayweave import --format zemax < lens.zmx > system.yaml

Imports a lens file from another optical design tool and converts it
to RayWeave YAML (multi-config format).

If the lens data includes field angles, chief rays and marginal rays
are automatically computed so the output can be piped directly into
"rayweave trace" and "rayweave plot".

Options:
  --format zemax     input format (required: zemax | oslo | codev)
  --config-id name   config id (default: "config1")
  --config-name name config display name (default: "Config1")
  --no-chief         skip automatic chief ray computation
  --glass-dir dir    load AGF glass catalog from directory

Supported surface types:
  ZEMAX: STANDARD → sphere, EVENASPH → asphere_polynomial
  OSLO:  SRF (RD/TH/GL/AP/CV) → sphere, NXT format
  CODE V: RDY/THI/GLA/CCY/DIA/STO → sphere, ASP/AD/AE/AF → asphere_polynomial

Examples:
  rayweave import --format zemax < lens.zmx > system.yaml
  rayweave import --format oslo < lens.len | rayweave trace
  rayweave import --format zemax < lens.zmx | rayweave plot -o lens.svg
`)
	case "tmm":
		fmt.Print(`Usage: rayweave tmm < input.yaml

Thin-film coating analysis via transfer-matrix method.

Input YAML:
  n_air: 1.0                    # incident medium refractive index
  n_sub: 1.5                    # substrate refractive index
  theta_deg: 0                  # angle of incidence (degrees)
  lambda: 0.00055               # wavelength (mm)
  layers:
    - thickness: 100            # layer thickness (nm)
      n: 1.38                   # layer refractive index
    - thickness: 150
      n: 1.65

Output: Rs, Ts, Rp, Tp (s- and p-polarisation reflectance/transmittance)
`)
	default:
		fmt.Print(`Usage: rayweave <subcommand> [< input.yaml]

RayWeave — geometric ray tracing and optical design toolkit.

Subcommands:
  chief      Determine chief rays (spot centroid) for each field
  trace      Trace ray(s) through the system
  paraxial   Paraxial (first-order) ray trace
  tmm        Thin-film coating analysis (transfer-matrix method)
  plot       Generate SVG cross-section drawing
  optimize   DLS optimization of lens surfaces
  import     Import ZEMAX/OSLO/CODE V lens files

Use "rayweave help <subcommand>" or "rayweave <subcommand> --help"
  for detailed options and YAML structure.
`)
	}
}

func readStdin() ([]byte, error) {
	stat, err := os.Stdin.Stat()
	if err != nil {
		return nil, err
	}
	if (stat.Mode() & os.ModeCharDevice) != 0 {
		return nil, fmt.Errorf("no input data (pipe YAML into stdin)")
	}
	return io.ReadAll(os.Stdin)
}

func loadCatalogs(input *types.Input, glassDir ...string) (*glass.Catalog, *coating.Catalog) {
	glass.Warnf = errOut
	gc := glass.NewCatalog()
	if input.GlassCatalog == nil {
		input.GlassCatalog = &types.GlassCatalog{}
	}

	for _, g := range input.GlassCatalog.Entries {
		gc.Add(g)
	}

	agfPath := ""
	if len(glassDir) > 0 && glassDir[0] != "" {
		agfPath = glassDir[0]
	} else if input.GlassCatalog.Directory != "" {
		agfPath = input.GlassCatalog.Directory
	}

	if agfPath != "" {
		agfGlasses, err := glass.LoadAGFDir(agfPath)
		if err != nil {
			errOut("Warning: cannot load AGF directory %s: %v", agfPath, err)
		}
		for _, g := range agfGlasses {
			gc.Add(g)
			if !containsGlass(input.GlassCatalog.Entries, g) {
				input.GlassCatalog.Entries = append(input.GlassCatalog.Entries, g)
			}
		}
	}

	if agfPath == "" {
		for _, path := range input.GlassCatalog.Files {
			agfData, err := os.ReadFile(path)
			if err != nil {
				errOut("Warning: cannot read AGF file %s: %v", path, err)
				continue
			}
			glasses, err := glass.ParseAGF(agfData)
			if err != nil {
				errOut("Warning: cannot parse AGF file %s: %v", path, err)
				continue
			}
			for _, g := range glasses {
				gc.Add(g)
				if !containsGlass(input.GlassCatalog.Entries, g) {
					input.GlassCatalog.Entries = append(input.GlassCatalog.Entries, g)
				}
			}
		}
	}

	cc := coating.NewCatalog()
	if input.CoatingCatalog != nil {
		for _, e := range input.CoatingCatalog.Entries {
			cc.Add(e)
		}
	}

	return gc, cc
}

func containsGlass(entries []types.Glass, g types.Glass) bool {
	key := types.ResolveGlassKey(g)
	for _, e := range entries {
		if types.ResolveGlassKey(e) == key {
			return true
		}
	}
	return false
}

func runChief(data []byte) {
	fs := flag.NewFlagSet("chief", flag.ExitOnError)
	clearAperture := fs.Bool("clear-aperture", false, "trace grid rays (all fields, at entrance-pupil-based beam diameter) through every surface, compute the maximum radial extent (max |Y|,|X|) at each surface, and set surfaces[].diameter = 2x that extent")
	clearApertureMargin := fs.Float64("clear-aperture-margin", 2.0, "beam diameter multiplier relative to entrance pupil (default 2 = 2× entrance pupil diameter)")
	marginalRays := fs.Bool("marginal-rays", false, "from each field's grid points find the rays with max/min image Y (and X for off-axis fields) and append them as marginal rays to the output 'rays' section for piping into trace/plot")
	passThrough := fs.Int("pass-through", 0, "constrain chief ray to pass through (0,0,0) center of surface N (overrides YAML pass_through.surface)")
	rayFan := fs.Bool("ray-fan", false, "compute ray fan (transverse aberration) for each field")
	fanPlane := fs.String("fan-plane", "", "fan plane selection: yz | xz (implies --ray-fan)")
	var fanRotation floatList
	fs.Var(&fanRotation, "fan-rotation", "fan plane Z-rotation angle in degrees (implies --ray-fan; 0=XZ, 90=YZ; repeatable or space-separated)")
	wlFlag := fs.Float64("wl", 0.00058756, "wavelength (mm) for grid ray tracing")
	configFlag := fs.String("config", "", "select config by id (multi-config mode)")
	glassDir := fs.String("glass-dir", "", "AGF glass catalog directory")
	fs.Parse(expandFanRotationArgs(os.Args[2:]))

	if *fanPlane != "" && len(fanRotation) > 0 {
		errOut("Error: --fan-plane and --fan-rotation are mutually exclusive")
		os.Exit(1)
	}
	if *fanPlane != "" && *fanPlane != "yz" && *fanPlane != "xz" {
		errOut("Error: --fan-plane must be 'yz' or 'xz' (got %q)", *fanPlane)
		os.Exit(1)
	}

	var input types.Input
	if err := yaml.Unmarshal(data, &input); err != nil {
		errOut("Error parsing YAML: %v", err)
		os.Exit(1)
	}

	if input.Chief == nil {
		errOut("Error: 'chief' section is required")
		os.Exit(1)
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

	// Resolve field definitions
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

	wavelength := *wlFlag

	gc, _ := loadCatalogs(&input, *glassDir)

	surfaces := selectSurfaces(input.System.Surfaces, input.Configs, configFlag)
	// system.surfaces is a read-only compatibility fallback; never write to it.
	// When --clear-aperture mutates diameters, promote legacy system.surfaces
	// into configs[0].surfaces first so the output echo carries no system.surfaces.
	if *clearAperture && *configFlag == "" && len(input.System.Surfaces) > 0 {
		if len(input.Configs) == 0 {
			input.Configs = []types.Config{{
				ID:     "config1",
				Name:   "Config1",
				Weight: 1.0,
				Active: true,
			}}
		}
		if len(input.Configs[0].Surfaces) == 0 {
			input.Configs[0].Surfaces = input.System.Surfaces
			input.System.Surfaces = nil
			surfaces = input.Configs[0].Surfaces
		}
	}
	surface.Precompute(surfaces)

	selectedSys := input.System
	selectedSys.Surfaces = surfaces

	pol := types.NewCircularJones(true)
	if input.Rays != nil {
		pol = input.Rays.Polarization
	}

	fanCfg := resolveRayFanConfig(*rayFan, *fanPlane, fanRotation)

	results := chief.DetermineChiefRaysGrid(
		selectedSys,
		fields,
		input.Chief.ReferenceSurface,
		input.Chief.NumRays,
		gc,
		pol,
		wavelength,
		input.Chief.DumpMap,
		input.Chief.GridType,
		pt,
		fanCfg,
		input.Chief.Wavelengths,
	)

	// --- --clear-aperture: scale grid points by entrance-pupil-based radius and set Diameter ---
	if *clearAperture && len(results) > 0 {
		// Compute entrance pupil diameter via paraxial
		chiefRayResults := make([]types.ChiefRayResult, len(results))
		for i, r := range results {
			chiefRayResults[i] = types.ChiefRayResult{
				FieldAngle:    r.FieldAngle,
				ChiefRay:      r.ChiefRay,
				ImageHeight:   r.ImageHeight,
				EntrancePupil: r.EntrancePupil,
			}
		}
		paraxResult := paraxial.Compute(selectedSys, wavelength, gc, 0, chiefRayResults)

		// Determine beam radius = (entrance pupil radius) × margin
		var newRadius float64
		if paraxResult.EntrancePupilDiameter > 0 {
			newRadius = (paraxResult.EntrancePupilDiameter / 2) * (*clearApertureMargin)
		}
		oldRadius := chief.FindMinApertureRadius(surfaces)
		if newRadius <= 0 {
			newRadius = oldRadius
		}

		// Precompute scale factor (constant for all grid points)
		useScale := oldRadius > 0 && newRadius > 0
		var scale float64
		if useScale {
			scale = newRadius / oldRadius
		}

		engine2 := ray.NewEngine(gc, nil)
		surface.Precompute(surfaces)
		path := buildPath(surfaces)

		surfIDtoIdx := make(map[int]int)
		for i, s := range surfaces {
			surfIDtoIdx[s.ID] = i
		}

		perSurfaceMaxY := make([]float64, len(surfaces))

		for _, r := range results {
			for _, gp := range r.GridPoints {
				if gp.ImageX == nil {
					continue
				}
				origin := gp.Origin
				dir := gp.Direction
				if useScale && r.FieldHeight > 0 {
					var t float64
					if math.Abs(dir.X) > 1e-12 {
						t = (gp.PupilX - origin.X) / dir.X
					} else {
						t = (gp.PupilY - origin.Y) / dir.Y
					}
					zStart := origin.Z + dir.Z*t
					aim := types.Vec3{X: gp.PupilX * scale, Y: gp.PupilY * scale, Z: zStart}
					dir = types.Vec3{
						X: aim.X - origin.X,
						Y: aim.Y - origin.Y,
						Z: aim.Z - origin.Z,
					}.Normalize()
				} else if useScale {
					fc := r.ChiefRay.Initial.Origin
					origin.X = fc.X + (gp.PupilX-fc.X)*scale
					origin.Y = fc.Y + (gp.PupilY-fc.Y)*scale
				}
				ray := types.Ray{
					Wavelength: wavelength,
					Initial:    types.RayState{Origin: origin, Direction: dir},
					Path:       path,
					Jones:      pol,
				}
				tr := engine2.TraceRay(ray, surfaces)
				if tr.Error != "" {
					continue
				}
				for _, sr := range tr.Surfaces {
					idx, ok := surfIDtoIdx[sr.SurfaceID]
					if !ok {
						continue
					}
					ay := math.Abs(sr.Position.Y)
					ax := math.Abs(sr.Position.X)
					if ay > perSurfaceMaxY[idx] {
						perSurfaceMaxY[idx] = ay
					}
					if ax > perSurfaceMaxY[idx] {
						perSurfaceMaxY[idx] = ax
					}
				}
			}
		}

		refID := input.Chief.ReferenceSurface
		for i := range surfaces {
			if surfaces[i].ID == refID {
				continue
			}
			if perSurfaceMaxY[i] > 0 {
				computedDiam := perSurfaceMaxY[i] * 2
				if computedDiam > surfaces[i].Diameter {
					surfaces[i].Diameter = computedDiam
				}
			}
		}
	}

	// Build ChiefRayResult for YAML output
	chiefRays := make([]types.ChiefRayResult, len(results))
	dumpMap := input.Chief.DumpMap
	for i, r := range results {
		cr := types.ChiefRayResult{
			FieldAngle:    r.FieldAngle,
			ChiefRay:      r.ChiefRay,
			ImageHeight:   r.ImageHeight,
			EntrancePupil: r.EntrancePupil,
			SpotStats:     r.SpotStats,
			RayFan:        r.RayFan,
			Wavelengths:   r.Wavelengths,
		}
		if dumpMap && len(r.GridPoints) > 0 {
			cr.GridPoints = r.GridPoints
		}
		chiefRays[i] = cr
	}

	// Convert chief rays into a rays section so the output can be piped into trace
	rayList := make([]types.Ray, len(chiefRays))
	for i, cr := range chiefRays {
		rayList[i] = cr.ChiefRay
		rayList[i].ID = fmt.Sprintf("chief_%.0fdeg", cr.FieldAngle)
	}
	input.Rays = &types.RayInput{
		Polarization: pol,
		Rays:         rayList,
	}

	// --- --marginal-rays: extract marginal rays from grid points ---
	if *marginalRays && len(results) > 0 {
		input.Rays.Rays = append(input.Rays.Rays, extractMarginalRays(results, wavelength, surfaces, pol)...)
	}

	output := types.Output{
		Input:     input,
		ChiefRays: chiefRays,
	}

	outData, err := yaml.Marshal(&output)
	if err != nil {
		errOut("Error marshaling output: %v", err)
		os.Exit(1)
	}
	os.Stdout.Write(outData)
}

// buildPath replicates chief.buildPath for marginal ray construction.
func buildPath(surfaces []types.Surface) []int {
	p := []int{0}
	for _, s := range surfaces {
		if s.ID > 0 {
			p = append(p, s.ID)
		}
	}
	return p
}

// extractMarginalRays finds the grid rays with max/min image Y (and X for
// fields with an X direction component) and returns them as marginal rays.
func extractMarginalRays(results []chief.Result, wavelength float64, surfaces []types.Surface, pol types.JonesVector) []types.Ray {
	var rays []types.Ray
	for fi, r := range results {
		var maxY, minY *types.GridPoint
		var maxX, minX *types.GridPoint
		hasX := math.Abs(r.FieldDir.X) > 1e-6

		for i := range r.GridPoints {
			gp := &r.GridPoints[i]
			if gp.ImageX == nil || gp.ImageY == nil {
				continue
			}
			y := *gp.ImageY
			if maxY == nil || y > *maxY.ImageY {
				maxY = gp
			}
			if minY == nil || y < *minY.ImageY {
				minY = gp
			}
			if hasX {
				x := *gp.ImageX
				if maxX == nil || x > *maxX.ImageX {
					maxX = gp
				}
				if minX == nil || x < *minX.ImageX {
					minX = gp
				}
			}
		}

		fid := fmt.Sprintf("f%d", fi)
		path := buildPath(surfaces)

		if maxY != nil && *maxY.ImageY != 0 {
			rays = append(rays, types.Ray{
				ID:         fmt.Sprintf("marginal_%s_Yplus", fid),
				Wavelength: wavelength,
				Initial:    types.RayState{Origin: maxY.Origin, Direction: maxY.Direction},
				Path:       path,
				Jones:      pol,
			})
		}
		if minY != nil && *minY.ImageY != 0 {
			rays = append(rays, types.Ray{
				ID:         fmt.Sprintf("marginal_%s_Yminus", fid),
				Wavelength: wavelength,
				Initial:    types.RayState{Origin: minY.Origin, Direction: minY.Direction},
				Path:       path,
				Jones:      pol,
			})
		}
		if hasX {
			if maxX != nil && *maxX.ImageX != 0 {
				rays = append(rays, types.Ray{
					ID:         fmt.Sprintf("marginal_%s_Xplus", fid),
					Wavelength: wavelength,
					Initial:    types.RayState{Origin: maxX.Origin, Direction: maxX.Direction},
					Path:       path,
					Jones:      pol,
				})
			}
			if minX != nil && *minX.ImageX != 0 {
				rays = append(rays, types.Ray{
					ID:         fmt.Sprintf("marginal_%s_Xminus", fid),
					Wavelength: wavelength,
					Initial:    types.RayState{Origin: minX.Origin, Direction: minX.Direction},
					Path:       path,
					Jones:      pol,
				})
			}
		}
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

// resolveConfig finds the config whose id matches val (by string id or 0-based index).
// Returns the index into configs, or -1 + error message.
func resolveConfig(configs []types.Config, val string) (int, string) {
	// Try numeric index first
	if idx, err := strconv.Atoi(val); err == nil && idx >= 0 && idx < len(configs) {
		return idx, ""
	}
	// Fall back to string id
	for i := range configs {
		if configs[i].ID == val {
			return i, ""
		}
	}
	return -1, fmt.Sprintf("config %q not found", val)
}

// selectSurfaces resolves which surface list to use:
//   - if configFlag is set, returns the matching config's surfaces
//   - else if system.surfaces is empty but configs exist, returns configs[0].surfaces
//   - otherwise returns system.surfaces
//
// system.surfaces is a read-only compatibility fallback for hand-written YAML;
// surface data is never written to it. Canonical storage is configs[].surfaces.
func selectSurfaces(sysSurfaces []types.Surface, configs []types.Config, configFlag *string) []types.Surface {
	if *configFlag != "" {
		idx, err := resolveConfig(configs, *configFlag)
		if idx < 0 {
			errOut("Error: %s", err)
			os.Exit(1)
		}
		return configs[idx].Surfaces
	}
	if len(sysSurfaces) == 0 && len(configs) > 0 && len(configs[0].Surfaces) > 0 {
		return configs[0].Surfaces
	}
	return sysSurfaces
}

func runTrace(data []byte) {
	args := os.Args[2:]
	fs := flag.NewFlagSet("trace", flag.ExitOnError)
	configFlag := fs.String("config", "", "select config by id (multi-config mode)")
	glassDir := fs.String("glass-dir", "", "AGF glass catalog directory")
	traceVerbose := fs.Bool("verbose", false, "print per-ray trace info to stderr")
	fs.Parse(args)

	var input types.Input
	if err := yaml.Unmarshal(data, &input); err != nil {
		errOut("Error parsing YAML: %v", err)
		os.Exit(1)
	}

	if input.Rays == nil || len(input.Rays.Rays) == 0 {
		errOut("Error: 'rays' section is required")
		os.Exit(1)
	}

	gc, cc := loadCatalogs(&input, *glassDir)
	surfaces := selectSurfaces(input.System.Surfaces, input.Configs, configFlag)
	surface.Precompute(surfaces)

	engine := ray.NewEngine(gc, cc)

	// Preserve chief_rays from input data if present
	var chiefRays []types.ChiefRayResult
	var temp struct {
		ChiefRays []types.ChiefRayResult `yaml:"chief_rays"`
	}
	if err := yaml.Unmarshal(data, &temp); err == nil {
		chiefRays = temp.ChiefRays
	}

	output := types.Output{
		Input:     input,
		Results:   make([]types.RayResult, 0, len(input.Rays.Rays)),
		ChiefRays: chiefRays,
	}

	for i := range input.Rays.Rays {
		r := &input.Rays.Rays[i]
		r.Jones = input.Rays.Polarization
		ray.ResolveRay(r, surfaces, engine)
		result := engine.TraceRay(*r, surfaces)
		if result.Error != "" {
			errMsg := fmt.Sprintf("{\"ray\":%q,\"error\":%q,\"error_code\":%q}\n", r.ID, result.Error, result.ErrorCode)
			if *traceVerbose {
				fmt.Fprint(os.Stderr, errMsg)
			} else {
				errOut("Warning: ray %q error: %s", r.ID, result.Error)
			}
		}
		output.Results = append(output.Results, result)
	}

	outData, err := yaml.Marshal(&output)
	if err != nil {
		errOut("Error marshaling output: %v", err)
		os.Exit(1)
	}
	os.Stdout.Write(outData)
}

func runParaxial(data []byte) {
	args := os.Args[2:]
	fs := flag.NewFlagSet("paraxial", flag.ExitOnError)
	configFlag := fs.String("config", "", "select config by id (multi-config mode)")
	glassDir := fs.String("glass-dir", "", "AGF glass catalog directory")
	fs.Parse(args)

	var input types.Input
	if err := yaml.Unmarshal(data, &input); err != nil {
		errOut("Error parsing YAML: %v", err)
		os.Exit(1)
	}

	gc, _ := loadCatalogs(&input, *glassDir)

	surfaces := selectSurfaces(input.System.Surfaces, input.Configs, configFlag)
	surface.Precompute(surfaces)

	selectedSys := input.System
	selectedSys.Surfaces = surfaces

	wavelength := 0.00058756
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

	result := paraxial.Compute(selectedSys, wavelength, gc, objectHeight, chiefRays)

	output := types.Output{
		Input:          input,
		ParaxialResult: &result,
	}

	outData, err := yaml.Marshal(&output)
	if err != nil {
		errOut("Error marshaling output: %v", err)
		os.Exit(1)
	}
	os.Stdout.Write(outData)
}

func runTMM(data []byte) {
	var input types.TMMInput
	if err := yaml.Unmarshal(data, &input); err != nil {
		errOut("Error parsing YAML: %v", err)
		os.Exit(1)
	}

	// Resolve layer refractive indices: try glass_catalog, then direct n:
	if input.GlassCatalog != nil {
		gc := glass.NewCatalog()
		for _, g := range input.GlassCatalog.Entries {
			gc.Add(g)
		}
		for i := range input.Layers {
			if input.Layers[i].N == 0 && input.Layers[i].Material != "" {
				n, err := gc.RefractiveIndex(input.Layers[i].Material, input.Lambda)
				if err == nil {
					input.Layers[i].N = n
				}
			}
		}
	}

	thetaRad := input.ThetaDeg * 3.141592653589793 / 180.0
	result := coating.ComputeTMM(input.NAir, input.NSub, input.Layers, input.Lambda, thetaRad)

	output := types.TMMOutput{
		Input: input,
		Rs:    result.Rs,
		Ts:    result.Ts,
		Rp:    result.Rp,
		Tp:    result.Tp,
	}

	outData, err := yaml.Marshal(&output)
	if err != nil {
		errOut("Error marshaling output: %v", err)
		os.Exit(1)
	}
	os.Stdout.Write(outData)
}
