package main

import (
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/hiroki/rayweaver/internal/chief"
	"github.com/hiroki/rayweaver/internal/coating"
	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/raymath"
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
	optExcludeParams := ""
	subcommand := args[0]
	currentCmd = subcommand
	if subcommand == "optimize" {
		fs := flag.NewFlagSet("optimize", flag.ContinueOnError)
		fs.BoolVar(&optVerbose, "verbose", false, "print per-iteration progress to stderr")
		fs.StringVar(&optLogFile, "log", "", "write per-iteration progress to file (JSONL)")
		fs.StringVar(&optGlassDir, "glass-dir", "", "AGF glass catalog directory")
		fs.StringVar(&optExcludeParams, "exclude-param", "", "comma-separated target param names to drop from the optimization variables (e.g. conic,a4,a6)")
		fs.Parse(args[1:])
		args = append([]string{"optimize"}, fs.Args()...)
	}

	// Escape has two sub-subcommands: run (default) and extract.
	escapeExtractMode := false
	escapeExtractIndex := 0
	optEscapeGlassDir := ""
	optEscapeVerbose := false
	optEscapeLogFile := ""
	optEscapeSaveFile := ""
	if subcommand == "escape" {
		if len(args) >= 2 && args[1] == "extract" {
			escapeExtractMode = true
			escapeExtractIndex = parseEscapeExtractFlags(args[2:])
		} else {
			fs := flag.NewFlagSet("escape", flag.ContinueOnError)
			fs.BoolVar(&optEscapeVerbose, "verbose", false, "print escape progress (local minima, parameter changes) to stderr (JSONL)")
			fs.StringVar(&optEscapeGlassDir, "glass-dir", "", "AGF glass catalog directory")
			fs.StringVar(&optEscapeLogFile, "log", "", "write escape progress to file (JSONL)")
			fs.StringVar(&optEscapeSaveFile, "save", "", "save each discovered local minimum to FILE1.yaml, FILE2.yaml, ...")
			fs.Parse(args[1:])
		}
	}

	data, err := readStdin()
	if err != nil {
		// `query` can evaluate literal expressions / bindings without any
		// document, so an interactive terminal is not an error for it.
		if subcommand != "query" {
			errOut("Error reading stdin: %v", err)
			os.Exit(1)
		}
		data = nil
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
	case "vignette":
		runVignette(data)
	case "optimize":
		runOptimize(data, optVerbose, optLogFile, optGlassDir, optExcludeParams)
	case "escape":
		if escapeExtractMode {
			runEscapeExtract(data, escapeExtractIndex)
		} else {
			runEscape(data, optEscapeGlassDir, optEscapeVerbose, optEscapeLogFile, optEscapeSaveFile)
		}
	case "import":
		runImport(data)
	case "scale":
		runScale(data)
	case "asphere":
		runAsphere(data)
	case "psf":
		runPSF(data)
	case "query":
		runQuery(data)
	default:
		errOut("Error: unknown subcommand %q", subcommand)
		errOut("Run 'rayweave --help' for usage.")
		os.Exit(1)
	}
}

// printHelpText writes a help string verbatim (wrapping fmt.Print so go vet's
// printf check does not mistake format directives in the text for a Printf).
func printHelpText(s string) {
	fmt.Print(s)
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
                         set auto_aperture: true surfaces' diameter = 2x max
                         radial extent + 2x --clear-aperture-margin-mm
  --clear-aperture-margin-mm 0.2   clearance added to each side of the beam
                         footprint (mm, default 0.2)
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

Options:
  --config ID          select config by id (multi-config mode)
  --lenient            trace rays leniently: skip aperture and glass-path
                          checks, and continue past missed surfaces and TIR
                          instead of stopping. Missed/TIR surfaces are recorded
                          per-surface with their interaction set to MISSED/REFLECT.
  --verbose            print per-ray trace errors as JSONL to stderr

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
		fmt.Print(`Usage: rayweave plot [-o file.svg|.png] [--config ID] [--lens-width 1.5] [--ray-width 1.5] < input.yaml

Generates a cross-section drawing (SVG or PNG) of the lens
system with ray paths overlaid.

Options:
  -o, --output file.svg   output file (.svg or .png; default: stdout = SVG)
  --config ID          select a config by id (multi-config mode)
  --lens-width 1.5     lens body stroke width in pixels
  --ray-width 1.5      ray path stroke width in pixels
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
	case "vignette":
		fmt.Print(`Usage: rayweave vignette [--iterations 3] [--min-glass-path 0.5] [--margin-mm 0.2] < system.yaml

Iteratively settles surface diameters and per-field vignetting using the
dynamic pupil (per-field entrance/exit pupil from the chief-ray crossings,
no physical stop required).

Options:
  --iterations 3        number of diameter/pupil passes (default 3)
  --min-glass-path 0.5  minimum glass path (edge thickness) below which a ray
                          fails, applied to every glass element (mm)
  --margin-mm 0.2       clearance added to each side of the beam footprint
                          when sizing auto_aperture surfaces (mm)
  --wl 0.00058756       reference wavelength (mm)
  --config ID           select config by id (multi-config mode)
  --glass-dir DIR       AGF glass catalog directory

Input: standard system YAML. The chief section supplies fields / grid. Only
surfaces marked auto_aperture: true are re-sized; auto_aperture: false
surfaces are fixed limiters (the aperture never moves). Rays are vignetted by
the glass-path (edge-thickness) check, fixed-surface apertures, and field 0's
marginal-ray envelope at each field's entrance-pupil plane.

Output: YAML with updated configs[].surfaces[].diameter, chief_rays[] with
per-field entrance_pupil / exit_pupil (dynamic), rays[] = marginal rays, and a
vignetting_result: report (per-field vignetting, entrance/exit pupil Z,
bound lower/upper envelope, marginal heights, auto_aperture diameters
before/after). Pipe into trace then plot:

  rayweave vignette < lens.yaml | rayweave trace | rayweave plot -o out.png
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
    jacobian_workers: 8       # parallel Jacobian goroutines (default GOMAXPROCS)
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

A SIGINT/SIGTERM stops the solve in two stages: the first signal interrupts
the running DLS within one iteration, preserving its best point found so far
(written to stdout with interrupted: true, exit 0); the second force-quits
immediately (exit 1).
 `)
	case "escape":
		fmt.Print(`Usage: rayweave escape [--verbose] [--log FILE] [--save FILE] [--glass-dir DIR] < input.yaml
       rayweave escape extract --index N < escape-output.yaml

Escape-function global optimisation (Ishiki-Ono style). DLS repeatedly
converges to local minima; after each convergence a smooth bump is added to
the merit function around that minimum, pushing the next DLS run out of the
valley to discover other local minima.

Options:
  --verbose        print escape progress to stderr as JSONL (local minima
                   found, escape-parameter changes, per-cycle DLS status);
                   every event carries a wall-clock time and elapsed seconds
  --log FILE       write the same JSONL progress stream to FILE
  --save FILE      save each discovered local minimum to FILE1.yaml, FILE2.yaml,
                   ... (discovery order). When a minimum is improved, the
                   current FILE N.yaml is renamed to FILE N.<version>.yaml and
                   the better point is written as FILE N.yaml. Writes are
                   atomic, so a killed process never loses already-found minima.
  --glass-dir DIR    AGF glass catalog directory

A SIGINT/SIGTERM stops the search in three stages: the first signal waits for
the current DLS run to finish (everything saved, interrupted: true, exit 0); the
second interrupts the running DLS within one iteration, preserving its best
point so far (interrupted: true, exit 0); the third force-quits immediately
(exit 1).

Sub-commands:
  escape (default)       run the global optimisation loop
  escape extract --index N   extract local minimum N as a clean lens YAML

Input YAML — optimization.escape section:
   optimization:
     method: dls
     variables: [...]          # same variable definitions as 'optimize'
     escape:
       max_cycles: 10          # DLS cycles per worker
       escape_workers: 4       # top-level parallel goroutines (default 4)
       max_seconds: 0          # soft shared wall-clock budget in seconds (0 = unlimited)
       distance_threshold: 0.1 # normalised distance to call a point "new"
       h_initial: 0.1          # escape bump height
       w_initial: 0.5          # escape bump width
       h_mult: 2.0             # strengthen factor when a minimum repeats
       w_mult: 1.3             # widen factor when a minimum repeats
       variable_weights:       # optional per-param weight (default 1.0)
         curvature: 1000
         thickness: 1
         nd: 10
         vd: 1
       # Optional execution tuning (defaults shown):
       escape_iter_frac: 0.333 # escape-phase MaxIter as a fraction of full budget
       w_span: 2.0             # per-worker W scaling span: W*(1 + i/(N-1)*(w_span-1))
       stall_window_frac: 0.2  # stalled-early-stop window as a fraction of MaxIter
       stall_rel_tol: 0.0001   # stalled-early-stop relative merit threshold
       stall_early_stop: true  # stalled-early-stop in the escape phase (clean phase never stalls)
       initial_perturb: 0.05   # normalised spread of parallel-worker start points

The DLS solve inside each worker parallelises the Jacobian across jacobian_workers
(optimization.jacobian_workers). Under the escape command, an unset
jacobian_workers defaults to 2 instead of GOMAXPROCS; with escape_workers > 1 the
total goroutines are escape_workers * jacobian_workers, so set jacobian_workers: 1
with many escape workers to avoid oversubscription.

Escape parameters act in the normalised variable space: each variable is
scaled by its min..max range. Variables with min == max are excluded from the
escape distance. distance_threshold is a fraction of the normalised range
(default 0.1).

The stalled-early-stop shortens an escape-phase DLS: once the best merit has
not improved by at least stall_rel_tol (relative) over a stall_window_frac
window of iterations, the solver returns converged_stalled with the best point
found. The clean re-optimisation phase always runs the full budget so a slow
late-stage improvement is not cut off. Set stall_early_stop: false to disable
this entirely.

Output: best solution in configs[].surfaces (pipeline-compatible with
"rayweave trace"/"rayweave plot"), plus an escape_result section listing
every discovered local minimum with its full surfaces.

  rayweave escape < input.yaml | rayweave trace | rayweave plot
  rayweave escape extract --index 1 < escape-output.yaml > min1.yaml
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
	case "scale":
		fmt.Print(`Usage: rayweave scale --efl TARGET [--config ID] < system.yaml > scaled.yaml

Scales a system so its effective focal length equals TARGET (mm). A uniform
scale of every length (radii, thicknesses, diameters, asphere coefficients and
normalization radii) by s = TARGET / current_EFL scales the EFL exactly by s,
preserving the f-number and the normalized aberration balance.

Useful for building a starting point at a target focal length before
optimizing (e.g. a 25 mm patent lens scaled to a 50 mm standard).

Options:
  --efl TARGET     target effective focal length (mm, required)
  --config ID      select config by id (multi-config mode); its EFL sets the
                     scale factor applied to every config

Example:
  rayweave scale --efl 50 < ref25.yaml | rayweave optimize > optimized.yaml
`)
	case "asphere":
		fmt.Print(`Usage: rayweave asphere [--rings 8] [--angles 16] [--pupil-samples 21] [--top-k 3] [--sag-scale 0.2] < system.yaml

Ranks the candidate surfaces for asphere introduction and estimates safe initial
even-order asphere coefficients (conic + A4..A12) from the per-field OPD
residuals. The score rewards surfaces where a rotationally-symmetric asphere can
simultaneously correct the shared (common) OPD across fields while penalising
inter-field conflict, manufacturing difficulty and optimisation instability.

Options:
  --rings N               polar cell radial rings (default 8)
  --angles N              polar cell angular sectors (default 16)
  --pupil-samples N       pupil grid radial samples (default 21)
  --sensitivity-samples N sensitivity trace radial samples (default 9; 0 = analytic proxy)
  --top-k N               number of top-ranked surfaces to fit (default 3)
  --sag-scale α           initial sag scale (default 0.2; try 0.05..0.5)
  --validate              run a short DLS per fitted surface to verify the asphere improves the merit
  --apply                 insert the top-ranked DLS-validated asphere onto its surface and
                          output the modified system (implies --validate). Pipeline friendly:
                          asphere --validate --apply < lens.yaml | chief | trace | plot
  --dls-iter N            DLS iterations per validated surface (default 20, with --validate)
  --num-rays N            pupil grid rays for validation DLS (default 64)
  --config ID             select config by id (multi-config mode)
  --glass-dir DIR         AGF glass catalog directory

Input: standard system YAML with a chief section (fields + optional
stop_surface). Candidate surfaces default to every non-mirror surface; restrict
with the asphere_candidate: section:

  asphere_candidate:
    candidate_surfaces: [2, 4, 6, 8]
    max_even_order: 10          # 8 -> A4..A8, 10 -> A4..A10, 12 -> A4..A12
    include_conic: true
    preserve_vertex_curvature: true
    sag_scale: 0.2              # safe starting scale (try 0.05..0.5)
    cell_rings: 8
    cell_angles: 16
    pupil_samples_radial: 21
    sensitivity_samples: 9      # 0 = analytic proxy only
    remove_tilt: true
    remove_defocus: false
    top_k: 3
    min_rays_per_cell: 3
    score_weights: {common: 0.35, unique: 0.15, fit: 0.20, sensitivity: 0.15,
                     conflict: 0.10, manufacturing: 0.05}

Piston is always removed (per-field OPD referenced to the field mean);
remove_piston is accepted but has no effect. max_sag / max_slope_deg /
max_curvature_variation are accepted for forward-compatibility but unused.

Output: YAML with an asphere_candidate_result: section (rankings with
coefficients, scaled_coefficients, sensitivity and, with --validate, a
validation block per fitted surface reporting the before/after short-DLS
merit and the DLS-solved coefficients, plus opd_profiles: each candidate
surface's per-field mean OPD across the footprint radius for the
OPD-overlap comparison). Pipe into optimize to apply:
  rayweave asphere < lens.yaml | rayweave optimize > optimized.yaml
  rayweave asphere --validate < lens.yaml | rayweave optimize > optimized.yaml
  rayweave asphere --validate --apply < lens.yaml | rayweave chief | rayweave trace | rayweave plot

See docs/asphere.md and docs/methods/asphere-candidates.md for details.
`)
	case "psf":
		fmt.Print(`Usage: rayweave psf [--ref-surface N] [--psf-grid 64] [--psf-width W] [--num-rays 400] < input.yaml

Computes the point-spread function on the flat image plane for each field and
wavelength via direct vector Huygens integration:
  per-field polarized ray tracing → non-uniform wavefront samples (Delaunay-
  triangulated reference surface) → vector Huygens integral → PSF(x,y).
No FFT; works for fisheye and other strongly non-paraxial systems. The default
input polarization is right-handed circular (RCP); RCP+LCP gives the
polarization-averaged (unpolarised) PSF.

Options:
  --ref-surface N     reference surface ID for wavefront sampling
                        (default: the last optical surface)
  --psf-grid N        image-plane pixels per side (default 64)
  --psf-width W       evaluation half-width in mm (default: auto from Airy
                        disk and geometric spot)
  --num-rays N        pupil grid rays (default 400 ≈ 20×20 polar)
  --fields I1,I2,...  field indices to compute (default: all)
  --wavelengths W1,...  wavelengths in mm (default: chief wavelengths,
                        else 587.56 nm)
  --polarization S    RCP (default) | LCP | X | Y | RCP+LCP
  --psf-workers N     parallel workers for the Huygens integral and wavefront
                        tracing (default: GOMAXPROCS)
  --yaml FILE         write full structured data (intensity, Ex/Ey/Ez,
                        encircled energy, wavefront OPD) to FILE, one
                        index-suffixed file per result
  --csv FILE          write a gnuplot x,y,intensity map to FILE, one
                        index-suffixed file per result
  --config ID         select config by id (multi-config mode)
  --glass-dir DIR     AGF glass catalog directory

Input YAML — psf section (optional; flags override):
  psf:
    reference_surface: 7
    grid_size: 64
    half_width: 0.01
    num_rays: 400
    huygens_workers: 8
    fields: [0, 1]
    wavelengths: [0.00058756, 0.00048613]
    polarization: "RCP+LCP"

Output: augmented YAML with a lightweight psf_results[] summary (Strehl,
FWHM, centroid, encircled energy, Airy radius, sampling counts). Full grids
are written to --yaml/--csv files referenced by output_file.

Notes:
  - The default reference surface is the last optical surface (the surface
    before the image plane). The wavefront is sampled there and propagated to
    the fixed flat image plane; field curvature and defocus therefore appear
    naturally in the PSF.
  - For strongly aberrated fields the PSF is a coherent speckle pattern whose
    peak (and hence Strehl) is sensitive to the pupil sampling. Increase
    --num-rays (e.g. 900..1600) for reliable off-axis metrics.
  - The reported wavefront rms_opd/pv_opd are referenced to the best-fit
    sphere (piston + tilt + defocus removed), the standard wavefront
    aberration definition.
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
	case "query":
		printHelpText(`Usage: rayweave query [flags] [SELECTOR] < input.yaml

Read-only YAML/JSONL selector: prints one plain-text value per invocation
(the value the demo scripts used to fetch with python3 + PyYAML). Works on
any YAML on stdin, including output of the other subcommands.

SELECTOR is an expression; paths are a subset of it:
  paraxial_result.focal_length
  chief_rays[0].spot_stats.rms_r
  chief_rays[field_angle=0].spot_stats.rms_r     (filter by equality)
  configs[id=config1].surfaces[id=2].thickness
  results[].surfaces[interaction=REFLECT].intensity_s

Expressions also support arithmetic (+ - * / %), comparisons, &&/||/!,
{...}/[...] literals, and the functions abs sqrt pow min max sin cos tan
asin acos atan atan2 radians degrees exp log floor ceil round len has.

Output modes:
  default   scalar -> raw text line; a single-element array unwraps; a
            larger array prints as [a b c]
  --yaml    serialize the result as YAML (subtree dump, records, lists)
  --json    serialize the result as JSON
  --csv     PATH:col1,col2,... -> CSV rows, skipping rows with missing
            columns (add --csv-header for a header)
  --gate    evaluate EXPR, print the value and exit 0/1 by truthiness

Iteration and aggregates:
  --each 'PATH:col1,col2,...'   one row per element (format with --printf)
  --count PATH   non-null element count   --len PATH   array length
  --sum PATH     sum of numeric values    --product PATH  product
  --stdev PATH   population standard deviation of numeric values

Bindings (evaluated in order; later ones can reference earlier ones):
  --set VAR=EXPR   a PATH, a number, or an arithmetic expression
  --yaml --set a=... --set b=...        emit a {a:.., b:..} record

Input:
  YAML on stdin (default). With --jsonl read one JSON object per line
  (e.g. "optimize --log" or "escape --log" output); --where EXPR keeps
  matching lines, --first uses the first match instead of the last, and
  --count '[]' counts the matching lines.

Options:
  --default STR    value printed when a scalar is missing/null (default -1)
  --printf FMT     Go fmt format string (e.g. '%.4f') for the output value
  -r, --raw        raw text output (the default for scalars)
  --expr EXPR      same as the positional SELECTOR

Examples:
  rayweave paraxial < lens.yaml | rayweave query -r paraxial_result.focal_length
  rayweave chief < lens.yaml \
    | rayweave query --expr '100*(ih-efl*tan(radians(a)))/(efl*tan(radians(a)))' \
        --set ih=chief_rays[1].image_height[1] --set a=chief_rays[1].field_angle \
        --set efl=50.0
  rayweave optimize --log opt.jsonl < lens.yaml > out.yaml
  rayweave query --jsonl --where 'has("status")' -r status < opt.jsonl
  rayweave query --jsonl --where 'event=="breakdown"' \
    --each 'terms:key,value' --printf '  %s: %.6e' < opt.jsonl
  rayweave query --count 'chief_rays[0].grid_points[].image_x' < chief.yaml
  rayweave query --len 'chief_rays[0].grid_points' < chief.yaml
  rayweave query --gate 'abs(efl-50.0)<=0.01' --set efl=paraxial_result.focal_length

See docs/query.md for the full manual.
`)
	default:
		fmt.Print(`Usage: rayweave <subcommand> [< input.yaml]

RayWeave — geometric ray tracing and optical design toolkit.

Subcommands:
  chief      Determine chief rays (spot centroid) for each field
  trace      Trace ray(s) through the system
  paraxial   Paraxial (first-order) ray trace
  tmm        Thin-film coating analysis (transfer-matrix method)
  vignette   Iteratively settle vignetting and auto_aperture diameters
  plot       Generate SVG cross-section drawing
  optimize   DLS optimization of lens surfaces
  escape     Escape-function global optimization (multiple local minima)
  scale      Scale a system so its EFL equals --efl TARGET
  asphere    Rank surfaces for asphere introduction, estimate initial coefficients
  psf        Point-spread function via direct vector Huygens integration
  import     Import ZEMAX/OSLO/CODE V lens files
  query      Read-only YAML/JSONL selector (replace python3/PyYAML in demos)

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

	// Glass keys actually referenced by the lens surfaces. An external AGF
	// catalog is registered into the emitted glass_catalog only for these,
	// keeping the piped YAML small even when the catalog carries thousands of
	// glasses. The runtime catalog still receives every AGF glass so lookups
	// (aliases, manufacturer suffixes, moulding grades) resolve identically.
	referenced := referencedGlassKeys(input)

	if agfPath != "" {
		agfGlasses, err := glass.LoadAGFDir(agfPath)
		if err != nil {
			errOut("Warning: cannot load AGF directory %s: %v", agfPath, err)
		}
		needed := neededAGFKeys(referenced, agfGlasses)
		for _, g := range agfGlasses {
			gc.Add(g)
			if needed[types.ResolveGlassKey(g)] && !containsGlass(input.GlassCatalog.Entries, g) {
				input.GlassCatalog.Entries = append(input.GlassCatalog.Entries, g)
			}
		}
	}

	if agfPath == "" {
		var allGlasses []types.Glass
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
			allGlasses = append(allGlasses, glasses...)
		}
		needed := neededAGFKeys(referenced, allGlasses)
		for _, g := range allGlasses {
			gc.Add(g)
			if needed[types.ResolveGlassKey(g)] && !containsGlass(input.GlassCatalog.Entries, g) {
				input.GlassCatalog.Entries = append(input.GlassCatalog.Entries, g)
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

// referencedGlassKeys returns the set of glass catalog keys referenced by any
// config's surfaces (material.key only). These are the only glasses a lens
// needs resolved at runtime, so only they may be registered into the emitted
// glass_catalog from an external AGF catalog.
func referencedGlassKeys(input *types.Input) map[string]bool {
	set := make(map[string]bool)
	for i := range input.Configs {
		for _, s := range input.Configs[i].Surfaces {
			if s.Material.HasKey() {
				set[s.Material.Key] = true
			}
		}
	}
	return set
}

// neededAGFKeys resolves every referenced glass key against the AGF glasses
// using the same Catalog.Lookup normalization as runtime (hyphen/underscore
// stripping, manufacturer suffix, moulding grade) and returns the set of AGF
// glass keys that back a referenced material.
func neededAGFKeys(referenced map[string]bool, agfGlasses []types.Glass) map[string]bool {
	if len(referenced) == 0 || len(agfGlasses) == 0 {
		return nil
	}
	agfGc := glass.NewCatalog()
	for _, g := range agfGlasses {
		agfGc.Add(g)
	}
	needed := make(map[string]bool)
	for key := range referenced {
		if g, ok := agfGc.Lookup(key); ok {
			if resolved := types.ResolveGlassKey(*g); resolved != "" {
				needed[resolved] = true
			}
		}
	}
	return needed
}

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
	fs.Parse(expandFanRotationArgs(os.Args[2:]))

	if *fanPlane != "" && len(fanRotation) > 0 {
		errOut("Error: --fan-plane and --fan-rotation are mutually exclusive")
		os.Exit(1)
	}
	if *fanPlane != "" && *fanPlane != "yz" && *fanPlane != "xz" {
		errOut("Error: --fan-plane must be 'yz' or 'xz' (got %q)", *fanPlane)
		os.Exit(1)
	}

	input := parseYAML[types.Input](data)

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

	surfaces := configSurfaces(input.Configs, configFlag)
	surface.Precompute(surfaces)

	selectedSys := input.System
	selectedSys.Surfaces = surfaces
	selectedSys.StopSurface = input.Chief.StopSurface

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
		if *clearApertureRays > 0 && *clearApertureRays != input.Chief.NumRays {
			// Re-trace with a denser grid so the beam footprint is accurate.
			results = chief.DetermineChiefRaysGrid(
				selectedSys, fields, input.Chief.ReferenceSurface, *clearApertureRays,
				gc, pol, wavelength, false, input.Chief.GridType, pt, fanCfg, input.Chief.Wavelengths,
			)
		}
		// The chief grid points already fill the aperture stop, so trace them
		// as-is through every surface to get the true beam envelope.
		engine2 := ray.NewEngine(gc, nil)
		surface.Precompute(surfaces)
		path := dls.BuildPath(surfaces)

		surfIDtoIdx := make(map[int]int)
		for i, s := range surfaces {
			surfIDtoIdx[s.ID] = i
		}

		type gridJob struct {
			origin    types.Vec3
			direction types.Vec3
		}
		var jobs []gridJob
		for _, r := range results {
			for _, gp := range r.GridPoints {
				if gp.ImageX == nil {
					continue
				}
				jobs = append(jobs, gridJob{origin: gp.Origin, direction: gp.Direction})
			}
		}

		workers := runtime.GOMAXPROCS(0)
		if workers > len(jobs) {
			workers = len(jobs)
		}
		if workers < 1 {
			workers = 1
		}
		perWorkerMax := make([][]float64, workers)
		for w := 0; w < workers; w++ {
			perWorkerMax[w] = make([]float64, len(surfaces))
		}
		var wg sync.WaitGroup
		jobCh := make(chan int)
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				local := perWorkerMax[w]
				for i := range jobCh {
					gp := jobs[i]
					ray := types.Ray{
						Wavelength: wavelength,
						Initial:    types.RayState{Origin: gp.origin, Direction: gp.direction},
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
						if ay > local[idx] {
							local[idx] = ay
						}
						if ax > local[idx] {
							local[idx] = ax
						}
					}
				}
			}(w)
		}
		for i := range jobs {
			jobCh <- i
		}
		close(jobCh)
		wg.Wait()

		perSurfaceMaxY := make([]float64, len(surfaces))
		for w := 0; w < workers; w++ {
			for idx, e := range perWorkerMax[w] {
				if e > perSurfaceMaxY[idx] {
					perSurfaceMaxY[idx] = e
				}
			}
		}

		refID := input.Chief.ReferenceSurface
		stopID := input.Chief.StopSurface
		for i := range surfaces {
			if surfaces[i].ID == refID || (stopID > 0 && surfaces[i].ID == stopID) || !surfaces[i].AutoAperture {
				continue
			}
			if perSurfaceMaxY[i] > 0 {
				surfaces[i].Diameter = perSurfaceMaxY[i]*2 + 2**clearApertureMarginMM
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

// configSurfaces resolves the surface list for a command:
//   - if configFlag is set, returns the matching config's surfaces
//   - otherwise returns configs[0].surfaces
//
// Canonical surface storage is configs[].surfaces.
func configSurfaces(configs []types.Config, configFlag *string) []types.Surface {
	if *configFlag != "" {
		idx, err := resolveConfig(configs, *configFlag)
		if idx < 0 {
			errOut("Error: %s", err)
			os.Exit(1)
		}
		return configs[idx].Surfaces
	}
	if len(configs) > 0 {
		return configs[0].Surfaces
	}
	return nil
}

func runTrace(data []byte) {
	args := os.Args[2:]
	fs := flag.NewFlagSet("trace", flag.ExitOnError)
	configFlag := fs.String("config", "", "select config by id (multi-config mode)")
	glassDir := fs.String("glass-dir", "", "AGF glass catalog directory")
	traceVerbose := fs.Bool("verbose", false, "print per-ray trace info to stderr")
	traceLenient := fs.Bool("lenient", false, "trace rays leniently: skip aperture/glass-path checks, continue past missed surfaces and TIR")
	fs.Parse(args)

	input := parseYAML[types.Input](data)

	gc, cc := loadCatalogs(&input, *glassDir)
	surfaces := configSurfaces(input.Configs, configFlag)
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

	// The ray list to trace: the `rays` section (marginal / user rays) plus
	// the per-field chief rays from `chief_rays` (see gatherTraceRays). Chief
	// rays are the authoritative copy, so `trace` must accept a chief output
	// whose rays section only carries the extras.
	pol := types.NewCircularJones(true)
	if input.Rays != nil {
		pol = input.Rays.Polarization
	}
	rayList := gatherTraceRays(input.Rays, chiefRays, pol)
	if len(rayList) == 0 {
		errOut("Error: 'rays' or 'chief_rays' section is required")
		os.Exit(1)
	}

	output := types.Output{
		Input:     input,
		Results:   make([]types.RayResult, len(rayList)),
		ChiefRays: chiefRays,
	}

	results := make([]types.RayResult, len(rayList))
	var errorsMu sync.Mutex
	var wg sync.WaitGroup
	workers := runtime.GOMAXPROCS(0)
	if workers > len(rayList) {
		workers = len(rayList)
	}
	jobs := make(chan int)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				r := &rayList[i]
				r.Jones = pol
				if *traceLenient {
					r.Lenient = true
				}
				ray.ResolveRay(r, surfaces, engine)
				result := engine.TraceRay(*r, surfaces)
				if result.Error != "" {
					errMsg := fmt.Sprintf("{\"ray\":%q,\"error\":%q,\"error_code\":%q}\n", r.ID, result.Error, result.ErrorCode)
					errorsMu.Lock()
					if *traceVerbose {
						fmt.Fprint(os.Stderr, errMsg)
					} else {
						errOut("Warning: ray %q error: %s", r.ID, result.Error)
					}
					errorsMu.Unlock()
				}
				results[i] = result
			}
		}()
	}
	for i := range rayList {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	output.Results = results

	writeYAML(&output)
}

// gatherTraceRays returns the merged ray list to trace: the input `rays`
// section plus the per-field chief rays from `chief_rays`. Chief rays are
// identified in the rays section by the synthetic "chief_<angle>deg" ids, so a
// duplicate copy there is not traced twice.
func gatherTraceRays(rays *types.RayInput, chiefRays []types.ChiefRayResult, pol types.JonesVector) []types.Ray {
	total := 0
	if rays != nil {
		total += len(rays.Rays)
	}
	total += len(chiefRays)
	rayList := make([]types.Ray, 0, total)

	seen := make(map[string]bool, len(chiefRays))
	if rays != nil {
		for _, r := range rays.Rays {
			if r.ID != "" {
				seen[r.ID] = true
			}
			rayList = append(rayList, r)
		}
	}
	for _, cr := range chiefRays {
		id := fmt.Sprintf("chief_%.0fdeg", cr.FieldAngle)
		if seen[id] {
			continue
		}
		r := cr.ChiefRay
		r.ID = id
		r.Jones = pol
		rayList = append(rayList, r)
	}
	return rayList
}

func runParaxial(data []byte) {
	args := os.Args[2:]
	fs := flag.NewFlagSet("paraxial", flag.ExitOnError)
	configFlag := fs.String("config", "", "select config by id (multi-config mode)")
	glassDir := fs.String("glass-dir", "", "AGF glass catalog directory")
	fs.Parse(args)

	input := parseYAML[types.Input](data)

	gc, _ := loadCatalogs(&input, *glassDir)

	surfaces := configSurfaces(input.Configs, configFlag)
	surface.Precompute(surfaces)

	selectedSys := input.System
	selectedSys.Surfaces = surfaces
	if input.Chief != nil {
		selectedSys.StopSurface = input.Chief.StopSurface
	}

	wavelength := types.DefaultWavelength
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

	// Stop-free (dynamic-pupil) systems have no explicit stop surface for the
	// paraxial pupil trace; derive the entrance pupil from the dynamic-pupil
	// chief pass (selected config's fields under --config) so EPD is reported
	// even when the document carries no chief_rays.
	if selectedSys.StopSurface <= 0 && len(chiefRays) == 0 {
		if pupil := dynamicPupilForInput(input, configFlag, surfaces, gc); pupil != nil {
			chiefRays = append(chiefRays, types.ChiefRayResult{EntrancePupil: pupil})
		}
	}

	result := paraxial.Compute(selectedSys, wavelength, gc, objectHeight, chiefRays)

	output := types.Output{
		Input:          input,
		ParaxialResult: &result,
	}

	writeYAML(&output)
}

func runTMM(data []byte) {
	input := parseYAML[types.TMMInput](data)

	// Resolve layer refractive indices: try glass_catalog, then direct n:
	if input.GlassCatalog != nil {
		gc := glass.NewCatalog()
		for _, g := range input.GlassCatalog.Entries {
			gc.Add(g)
		}
		for i := range input.Layers {
			if input.Layers[i].N == 0 && input.Layers[i].Material != "" {
				n, err := gc.RefractiveIndex(types.ParseMaterial(input.Layers[i].Material), input.Lambda)
				if err == nil {
					input.Layers[i].N = n
				}
			}
		}
	}

	thetaRad := raymath.DegToRad(input.ThetaDeg)
	result := coating.ComputeTMM(input.NAir, input.NSub, input.Layers, input.Lambda, thetaRad)

	output := types.TMMOutput{
		Input: input,
		Rs:    result.Rs,
		Ts:    result.Ts,
		Rp:    result.Rp,
		Tp:    result.Tp,
	}

	writeYAML(&output)
}
