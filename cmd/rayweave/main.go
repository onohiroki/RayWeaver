package main

import (
	"flag"
	"fmt"
	"io"
	"math"
	"os"

	"github.com/hiroki/rayweaver/internal/chief"
	"github.com/hiroki/rayweaver/internal/coating"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
	"gopkg.in/yaml.v3"
)

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

	// Parse optimize-specific flags before stdin reading
	optVerbose := false
	optLogFile := ""
	subcommand := args[0]
	if subcommand == "optimize" {
		fs := flag.NewFlagSet("optimize", flag.ContinueOnError)
		fs.BoolVar(&optVerbose, "verbose", false, "print per-iteration progress to stderr")
		fs.StringVar(&optLogFile, "log", "", "write per-iteration progress to file (JSONL)")
		fs.Parse(args[1:])
		args = append([]string{"optimize"}, fs.Args()...)
	}

	data, err := readStdin()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading stdin: %v\n", err)
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
		runOptimize(data, optVerbose, optLogFile)
	default:
		fmt.Fprintf(os.Stderr, "Error: unknown subcommand %q\n", subcommand)
		fmt.Fprintf(os.Stderr, "Run 'rayweave --help' for usage.\n")
		os.Exit(1)
	}
}

func printHelp(cmd string) {
	switch cmd {
	case "chief":
		fmt.Print(`Usage: rayweave chief < system.yaml

Determines chief rays (spot centroid) for each field.

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

Output: augmented YAML with chief_rays[] section.
  Pipe into "rayweave trace" to trace each chief ray through the system.

Chief ray = ray passing through the spot centroid on the reference
  surface (not the stop centre).  This definition is robust during
  optimisation where the stop may be ill-defined.

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
		fmt.Print(`Usage: rayweave paraxial < system.yaml

Performs paraxial (first-order) ray trace and cardinal analysis.

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
		fmt.Print(`Usage: rayweave plot [-o file.svg] [--lens-width 0.1] [--ray-width 0.1] < input.yaml

Generates an SVG cross-section drawing of the lens system
with ray paths overlaid.

Options:
  -o file.svg           output SVG file (default: stdout)
  --lens-width 0.1      lens body stroke width
  --ray-width 0.1       ray path stroke width
  --scale 0             SVG scale factor (0 = auto)
  --right-margin 20     right-side margin beyond image plane (% of lens length)

Input: YAML with system surfaces + optional results[] and chief_rays[].
  Pipe after "rayweave trace" for ray paths:
    cat system.yaml | rayweave chief | rayweave trace | rayweave plot -o lens.svg

  Or with a standalone trace result:
    cat system.yaml | rayweave trace | rayweave plot -o lens.svg

Glass types are colour-coded using the nd/vd values from the
glass_catalog section.  Ray colours follow the field angle
(low = blue, high = red).
`)
	case "optimize":
		fmt.Print(`Usage: rayweave optimize [--verbose] [--log FILE] < input.yaml

DLS (Damped Least Squares) optimization of lens surfaces.

Options:
  --verbose        print per-iteration progress to stderr (JSONL)
  --log FILE       write per-iteration progress to FILE (JSONL)

Input YAML — optimization section:
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

Merit terms are read from configs[0].merit (see help for details).

Output: optimized YAML with updated surface parameters.
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

func loadCatalogs(input *types.Input) (*glass.Catalog, *coating.Catalog) {
	gc := glass.NewCatalog()
	if input.GlassCatalog != nil {
		for _, g := range input.GlassCatalog.Entries {
			gc.Add(g)
		}
		for _, path := range input.GlassCatalog.Files {
			agfData, err := os.ReadFile(path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: cannot read AGF file %s: %v\n", path, err)
				continue
			}
			glasses, err := glass.ParseAGF(agfData)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: cannot parse AGF file %s: %v\n", path, err)
				continue
			}
			for _, g := range glasses {
				gc.Add(g)
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

func runChief(data []byte) {
	fs := flag.NewFlagSet("chief", flag.ExitOnError)
	clearAperture := fs.Bool("clear-aperture", false, "compute clear aperture diameters from grid ray extents and set system.surfaces[].diameter")
	marginalRays := fs.Bool("marginal-rays", false, "add marginal rays (max/min Y, and X if applicable) to output rays")
	fs.Parse(os.Args[2:])

	var input types.Input
	if err := yaml.Unmarshal(data, &input); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing YAML: %v\n", err)
		os.Exit(1)
	}

	if input.Chief == nil {
		fmt.Fprintf(os.Stderr, "Error: 'chief' section is required\n")
		os.Exit(1)
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
		fmt.Fprintf(os.Stderr, "Error: 'fields' or 'field_angles' is required\n")
		os.Exit(1)
	}

	wavelength := 0.00058756

	gc, _ := loadCatalogs(&input)
	surface.Precompute(input.System.Surfaces)

	pol := types.NewCircularJones(true)
	if input.Rays != nil {
		pol = input.Rays.Polarization
	}

	results := chief.DetermineChiefRaysGrid(
		input.System,
		fields,
		input.Chief.ReferenceSurface,
		input.Chief.NumRays,
		gc,
		pol,
		wavelength,
		input.Chief.DumpMap,
		input.Chief.GridType,
	)

	// --- --clear-aperture: trace grid points and set Diameter on all surfaces ---
	if *clearAperture && len(results) > 0 {
		engine2 := ray.NewEngine(gc, nil)
		surface.Precompute(input.System.Surfaces)

		path := []int{0}
		for _, s := range input.System.Surfaces {
			if s.ID > 0 {
				path = append(path, s.ID)
			}
		}

		surfIDtoIdx := make(map[int]int)
		for i, s := range input.System.Surfaces {
			surfIDtoIdx[s.ID] = i
		}

		perSurfaceMaxY := make([]float64, len(input.System.Surfaces))

		for _, r := range results {
			for _, gp := range r.GridPoints {
				if gp.ImageX == nil {
					continue
				}
				ray := types.Ray{
					Wavelength: wavelength,
					Initial:    types.RayState{Origin: gp.Origin, Direction: gp.Direction},
					Path:       path,
					Jones:      pol,
				}
				tr := engine2.TraceRay(ray, input.System.Surfaces)
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

		for i := range input.System.Surfaces {
			if perSurfaceMaxY[i] > 0 {
				input.System.Surfaces[i].Diameter = perSurfaceMaxY[i] * 2
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
		for fi, r := range results {
			var maxY, minY *types.GridPoint
			var maxX, minX *types.GridPoint
			hasX := math.Abs(r.FieldDir.X) > 1e-6 && math.Abs(r.FieldDir.Y) > 1e-6

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

			if maxY != nil && *maxY.ImageY != 0 {
				input.Rays.Rays = append(input.Rays.Rays, types.Ray{
					ID:         fmt.Sprintf("marginal_%s_Yplus", fid),
					Wavelength: wavelength,
					Initial:    types.RayState{Origin: maxY.Origin, Direction: maxY.Direction},
					Path:       buildPath(input.System.Surfaces),
					Jones:      pol,
				})
			}
			if minY != nil && *minY.ImageY != 0 {
				input.Rays.Rays = append(input.Rays.Rays, types.Ray{
					ID:         fmt.Sprintf("marginal_%s_Yminus", fid),
					Wavelength: wavelength,
					Initial:    types.RayState{Origin: minY.Origin, Direction: minY.Direction},
					Path:       buildPath(input.System.Surfaces),
					Jones:      pol,
				})
			}
			if hasX {
				if maxX != nil && *maxX.ImageX != 0 {
					input.Rays.Rays = append(input.Rays.Rays, types.Ray{
						ID:         fmt.Sprintf("marginal_%s_Xplus", fid),
						Wavelength: wavelength,
						Initial:    types.RayState{Origin: maxX.Origin, Direction: maxX.Direction},
						Path:       buildPath(input.System.Surfaces),
						Jones:      pol,
					})
				}
				if minX != nil && *minX.ImageX != 0 {
					input.Rays.Rays = append(input.Rays.Rays, types.Ray{
						ID:         fmt.Sprintf("marginal_%s_Xminus", fid),
						Wavelength: wavelength,
						Initial:    types.RayState{Origin: minX.Origin, Direction: minX.Direction},
						Path:       buildPath(input.System.Surfaces),
						Jones:      pol,
					})
				}
			}
		}
	}

	if !*clearAperture && !*marginalRays {
		input.Chief = nil
	}

	output := types.Output{
		Input:     input,
		ChiefRays: chiefRays,
	}

	outData, err := yaml.Marshal(&output)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshaling output: %v\n", err)
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

func runTrace(data []byte) {
	var input types.Input
	if err := yaml.Unmarshal(data, &input); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing YAML: %v\n", err)
		os.Exit(1)
	}

	if input.Rays == nil || len(input.Rays.Rays) == 0 {
		fmt.Fprintf(os.Stderr, "Error: 'rays' section is required\n")
		os.Exit(1)
	}

	gc, cc := loadCatalogs(&input)
	surface.Precompute(input.System.Surfaces)

	engine := ray.NewEngine(gc, cc)

	output := types.Output{
		Input:   input,
		Results: make([]types.RayResult, 0, len(input.Rays.Rays)),
	}

	for i := range input.Rays.Rays {
		r := &input.Rays.Rays[i]
		r.Jones = input.Rays.Polarization
		ray.ResolveRay(r, input.System.Surfaces, engine)
		result := engine.TraceRay(*r, input.System.Surfaces)
		output.Results = append(output.Results, result)
	}

	outData, err := yaml.Marshal(&output)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshaling output: %v\n", err)
		os.Exit(1)
	}
	os.Stdout.Write(outData)
}

func runParaxial(data []byte) {
	var input types.Input
	if err := yaml.Unmarshal(data, &input); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing YAML: %v\n", err)
		os.Exit(1)
	}

	gc, _ := loadCatalogs(&input)
	surface.Precompute(input.System.Surfaces)

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

	result := paraxial.Compute(input.System, wavelength, gc, objectHeight, chiefRays)

	output := types.Output{
		Input:          input,
		ParaxialResult: &result,
	}

	outData, err := yaml.Marshal(&output)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshaling output: %v\n", err)
		os.Exit(1)
	}
	os.Stdout.Write(outData)
}

func runTMM(data []byte) {
	var input types.TMMInput
	if err := yaml.Unmarshal(data, &input); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing YAML: %v\n", err)
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
		fmt.Fprintf(os.Stderr, "Error marshaling output: %v\n", err)
		os.Exit(1)
	}
	os.Stdout.Write(outData)
}
