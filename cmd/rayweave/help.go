package main

import (
	"fmt"
)

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
  --chief-ray NAME     chief-ray definition: entrance_pupil_centre | centroid |
                         vignetting_centre (empty = chief.chief_ray_definition)
  --clear-aperture     trace grid rays (all fields) through every surface and
                         set auto_aperture: true surfaces' diameter = 2x max
                         radial extent + 2x --clear-aperture-margin-mm
  --clear-aperture-margin-mm 0.2   clearance added to each side of the beam
                         footprint (mm, default 0.2)
  --clear-aperture-rays N  ray count for --clear-aperture beam tracing
                         (default: chief.num_rays); use a dense grid for
                         accurate footprints
  --preserve-rays      with --clear-aperture, keep the existing 'rays' section
                         instead of replacing it with chief rays, and omit
                         chief_rays from the output
  --marginal-rays      extract marginal (max/min) rays from grid points and
                         append them for piping into trace/plot
  --ray-fan            compute ray fan (transverse aberration) for each field
                         (YZ + XZ planes, all fields)
  --fan-plane yz|xz    compute only the YZ (meridional) or XZ (sagittal) fan
                         (implies --ray-fan)
  --fan-rotation DEG   compute fan(s) in planes rotated by DEG around Z
                         (0 = XZ, 90 = YZ; implies --ray-fan; repeatable or
                         space-separated: --fan-rotation 0 45 90)
  --epd D              shorthand for --pupil-model-diameter (mm): virtual
                         entrance pupil diameter
  --fnum N             F-number; sets the virtual entrance pupil diameter to
                         the paraxial EFL / N (mutually exclusive with --epd)
  --epz Z              shorthand for a virtual entrance pupil at axial position
                         Z (mm); activates virtual_entrance_pupil mode
  --pupil-model-axial-position Z  virtual entrance pupil Z (mm; activates
                         virtual pupil mode)
  --pupil-model-diameter D        virtual entrance pupil diameter (mm)
  --pupil-model-mode MODE         pupil model mode (default
                         virtual_entrance_pupil)
	  --wl 0.00058756      reference wavelength (mm; overrides chief.reference_wavelength)

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
  wavelength: 0.00058756       # reference wavelength (mm); overridden by --wl

  pass_through:                # optional: constrain chief ray to pass
    surface: 3                 #   through a specific surface coordinate
    coordinate: [0, 0, 0]      #   (default [0, 0, 0] = surface centre)

  pupil_model:                 # optional: virtual entrance pupil
    mode: virtual_entrance_pupil
    axial_position: 12.5       # pupil Z (mm); chief ray of every field passes
                               #   through (0,0,axial_position)
    diameter: 12.5             # entrance pupil diameter (mm)
                               # CLI shorthand: --epz Z + (--epd D | --fnum N)
                               #   where --fnum sets diameter = EFL / N

Output: augmented YAML with chief_rays[] section.
  Pipe into "rayweave trace" to trace each chief ray through the system.

Without pass_through, the chief ray passes through the spot centroid on
  the reference surface.  This definition is robust during optimisation
  where the stop may be ill-defined.

With pass_through, the chief ray is defined as the ray from the field
  that passes through the given coordinate on the given surface
  (traditional "stop-centre" definition).

With pupil_model, the pupil is a fixed virtual plane: every field's chief
  ray passes through its centre, the dynamic-pupil iteration and the
  low-angle probe are skipped, and the grid radius is diameter/2.

See also: samples/us2645157.yaml
`)
	case "trace":
		fmt.Print(`Usage: rayweave trace [FLAGS] < system.yaml
       rayweave trace single [FLAGS] < system.yaml

Traces rays through the system.

Without 'single':
  Traces rays from the input 'rays' section and chief_rays.
  Results are appended to any existing results in the input.

With 'single':
  Traces a single ray specified via CLI flags.
  Results overwrite any existing results.

Single-ray options:
  --origin X,Y,Z        ray origin (mm)
  --direction DX,DY,DZ  ray direction vector
  --aim X,Y,Z           aim target (auto-computes direction)
  --angle-yz DEG        incidence angle in YZ plane (degrees)
  --pass-through S:Y:X  pass through Y,X on surface S
  --path 0,1,2,...      surface path (comma-separated; default: sequential)
  --wavelength MM       wavelength (mm)
  --id NAME             ray ID (default: "trace_single")
  --details             add per-surface detail to output YAML
  --verbose             print per-surface info to stderr

Common options:
  --config ID           select config by id (multi-config mode)
  --glass-dir DIR       AGF glass catalog directory
  --lenient             skip aperture/glass-path checks
  --details             add per-surface detail (AOI, n1/n2, Fresnel) to output YAML

Input YAML — rays section:
  polarization: [1, 0, 0, 1]    # Jones vector [ReEx, ImEx, ReEy, ImEy]
  rays:
    - id: "my_ray"
      wavelength: 0.00058756    # mm
      initial:
        origin: [0, 0, -100]    # [x, y, z] start point (mm)
        direction: [0, 0.1, 1]  # [dx, dy, dz] direction vector
      path: [0, 1, 2, ..., N]   # surface IDs to trace (0 = object)

Input YAML — trace_single section (trace single only; CLI flags win and
  the effective values are written back on output):
  trace_single:
    origin: [0, 5, -100]        # ray origin (mm)
    direction: [0, 0, 1]        # or aim / angle_yz / pass_through
    aim: [0, 2.5, 25]           #   aim target (auto-computes direction)
    angle_yz: 5                 #   incidence angle in YZ plane (degrees)
    pass_through: [5, 2.5, 0]   #   [surface, Y, X] pass-through target
    path: [0, 1, 2, ..., N]     # surface IDs (default: sequential)
    wavelength: 0.00058756      # mm
    id: "my_ray"                # default: trace_single
    lenient: false              # skip aperture/glass-path checks
    details: false              # add per-surface detail to output YAML

Output: pipeline-compatible YAML (results[] section).
  Pipe into 'rayweave plot' for rendering.

Examples:
  rayweave trace single --origin 0,5,-100 --direction 0,0,1 < lens.yaml
  rayweave trace single --origin 0,5,-100 --angle-yz 5 --details < lens.yaml | rayweave plot
  rayweave trace single --origin 0,5,-100 --aim 0,2.5,25 --path 0,1,2,3,4 < lens.yaml
  rayweave chief < lens.yaml | rayweave trace --details | rayweave list rays
`)
	case "paraxial":
		fmt.Print(`Usage: rayweave paraxial [--wl MM] [--config ID] [--glass-dir DIR] < system.yaml

Performs paraxial (first-order) ray trace and cardinal analysis.

Options:
  --wl MM              reference wavelength in mm (default: the document's
                       chief.reference_wavelength, else 0.00058756); written
                       back into chief.reference_wavelength
  --config ID          select config by id (multi-config mode)
  --glass-dir DIR      AGF glass catalog directory

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
		fmt.Print(`Usage: rayweave plot [-o file.svg|.png] [flags] < input.yaml

Generates a cross-section drawing (SVG or PNG) of the lens
system with ray paths overlaid.

Options:
  -o, --output file.svg|.png   output file (default: stdout = SVG)
  --config ID                  select a config by id (multi-config mode)
  --lens-width 1.5             lens body stroke width in pixels
  --ray-width 1.5              ray path stroke width in pixels
  --scale 0                    SVG/PNG scale factor (0 = auto)
  --right-margin 20            right-side margin beyond image plane (% of lens length)
  --fan-rays 11                max fan rays drawn per field (0 = hide fan rays)
  --show-invalid-ray-fan       draw fan rays with error codes in full (boolean)
  --clip-invalid-ray-fan       draw error-coded fan rays up to first error (boolean)
  --glass-dir DIR              AGF glass catalog directory
  --element-color "1=#ff0000"  element fill colors by 1-based index (repeatable;
	1 = first glass element; ignored if index out of range)
  --asphere-color "#ff0000"    aspheric surface edge colors: single or per-surface-ID
	 map (repeatable; the sag curve line, not the fill;
	 surface IDs are 1-based; ignored if surface is not aspheric;
	 --asphere-color with no surface IDs sets a global color;
	 if given without aspheric surfaces warns and is ignored)

Color formats: #rrggbb, #rgb, rgb(r,g,b), rgba(r,g,b,a), CSS named colors

Invalid fan rays: a fan ray whose path records an error code on any surface
  (aperture_stop, missed_surface, total_internal_reflection, glass-path
  violations) is hidden by default. --show-invalid-ray-fan keeps the full
  path; --clip-invalid-ray-fan stops it at the first erroring surface. The
  two flags are mutually exclusive, are enabled by giving them alone (--flag)
  or disabled with --flag=false, and only affect fan rays.

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

Warnings (stderr, exit 0, rendering continues):
  --element-color out-of-range: index outside 1..N where N is the number
    of lens elements detected. The warning identifies the invalid index.
  --asphere-color non-asphere: surface ID specified does not correspond to
    an aspheric surface in the current config. The warning identifies the
    surface ID.
  --asphere-color no-asphere: --asphere-color was given but this config
    has no aspheric surfaces at all. The global color value is ignored.
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

Optional input YAML — vignette section (flags override, effective values are
written back into the output):
  vignette:
    iterations: 3        # diameter/pupil passes
    min_glass_path: 0.5  # minimum glass path (edge thickness) per element (mm)
    margin_mm: 0.2       # beam-footprint clearance per side (mm)
    wavelength: 0.00058756 # reference wavelength (mm)

Output: YAML with updated configs[].surfaces[].diameter, chief_rays[] with
per-field entrance_pupil / exit_pupil (dynamic), rays[] = marginal rays, and a
vignetting_result: report (per-field vignetting, entrance/exit pupil Z,
bound lower/upper envelope, marginal heights, auto_aperture diameters
before/after). Pipe into trace then plot:

  rayweave vignette < lens.yaml | rayweave trace | rayweave plot -o out.png
`)
	case "optimize":
		fmt.Print(`Usage: rayweave optimize [--verbose] [--log FILE] < input.yaml
       rayweave optimize snap [--glass-dir DIR] < optimized.yaml

DLS (Damped Least Squares) optimization of lens surfaces.

Sub-subcommand:
  snap             replace every declared nd/vd glass variable with the nearest
                   real catalog glass (no DLS) and report the optical-merit cost.
                   Intended for an already-optimized document (e.g. the output
                   of 'optimize' or 'escape extract').

Options:
  --verbose        print per-iteration progress to stderr (JSONL)
  --log FILE       write per-iteration progress to FILE (JSONL)
  --glass-dir DIR  AGF glass catalog directory
  --exclude-param LIST  comma-separated target param names to drop from the
                   optimization variables (e.g. conic,a4,a6)
  --cpuprofile FILE  write a CPU profile (pprof) to FILE
  --memprofile FILE  write a heap profile (pprof) to FILE at exit
  --gorprofile FILE  write a goroutine profile to FILE at exit
  --gogc N           GC target percentage (0 = 400 for compute commands)

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
immediately (exit 1). A detached launcher may request the same stages through
the file named by $RAYWEAVE_STOP_FILE (one stage number per line); rwrun and
the MCP server use that on Windows, where SIGTERM does not exist.
 `)
	case "escape":
		fmt.Print(`Usage: rayweave escape [--verbose] [--log FILE] [--save FILE]
                     [--keep-infeasible] [--stream BOOL] [--power-solve]
                     [--power-solve-surfaces SURFACES]
                     [--glass-variables] [--glass-dir DIR]
                     [--clean-dls BOOL] < input.yaml
       rayweave escape extract --index N < escape-output.yaml

Escape-function global optimisation (Ishiki-Ono style). DLS repeatedly
converges to local minima; after each convergence a smooth bump is added to
the merit function around that minimum, pushing the next DLS run out of the
valley to discover other local minima.

Options:
  --verbose        print escape progress to stderr as compact JSONL (abbreviated
                   values: fixed key order, floats as 6-digit exponent, wall
                   clock as HH:MM:SS, elapsed as HH:MM,
                   status/signal/timed_out/interrupted/reason omitted; the
                   minimum classification, file names and infeasible basins are
                   kept, so "list global" reads a --verbose capture)
  --log FILE       write the full JSONL progress stream to FILE (same fields,
                   keys ordered as the compact form)
  --debug          emit all DLS-internal JSONL events (iter, final,
                   adaptive_damping, mode_change) plus enriched cycle diagnostics
                   (iterations, before/after merit, escape count, nearest distance,
                   failures). Combines with --verbose/--log for full visibility.
  --save FILE      save each discovered local minimum to FILE0.yaml, FILE1.yaml,
                   ... (discovery order). When a minimum is improved, the
                   current FILE N.yaml is renamed to FILE N.<version>.yaml and
                   the better point is written as FILE N.yaml. Writes are
                   atomic, so a killed process never loses already-found minima.
  --keep-infeasible include infeasible basins in stdout YAML and --save files
                   (default: feasible minima only)
  --mem-limit VALUE  cap the Go heap: 2GiB, 2048MiB, 2048 (MB) or -1
                   (unlimited); default optimization.mem_limit_mb
  --stream BOOL     stream the pipeline document to stdout as the run progresses
                   (default on): the settled top-level part is written first,
                   then one global_result.minima entry per discovered solution,
                   so a run killed mid-way still leaves a readable document.
                   --stream=false (or optimization.escape.stream: false)
                   restores the one-shot write performed after the run.
  --clean-dls BOOL  run the clean DLS that closes each cycle (default true).
                   --clean-dls false records the exploration result as-is (or
                   the glass-phase output when power_solve ran) and skips the
                   post-glass retry and the glass-hull rescue re-solve with it.
  --power-solve    insert the power-preserving glass phase between each escape
                   and clean DLS (holds element powers fixed while the glasses
                   are rebalanced)
  --power-solve-surfaces SURFACES
                   comma-separated surface IDs whose curvature is recomputed
                   to hold the element thin-lens power (with --power-solve;
                   also enables the glass phase)
  --glass-variables auto-generate nd/vd optimization variables for every
                   refractive lens element (the merit is left unchanged)
  --glass-dir DIR    AGF glass catalog directory
  --cpuprofile FILE  write a CPU profile (pprof) to FILE
  --memprofile FILE  write a heap profile (pprof) to FILE at exit
  --gorprofile FILE  write a goroutine profile to FILE at exit
  --gogc N           GC target percentage (0 = 400 for compute commands)

A SIGINT/SIGTERM stops the search in three stages: the first signal waits for
the current DLS run to finish (everything saved, interrupted: true, exit 0); the
second interrupts the running DLS within one iteration, preserving its best
point so far (interrupted: true, exit 0); the third force-quits immediately
(exit 1). A detached launcher may request the same stages through the file
named by $RAYWEAVE_STOP_FILE (one stage number per line); rwrun and the MCP
server use that on Windows, where SIGTERM does not exist.

Sub-commands:
  escape (default)       run the global optimisation loop
  escape extract --index N   extract local minimum N as a clean lens YAML

Input YAML — optimization.escape section:
   optimization:
     method: dls
     variables: [...]          # same variable definitions as 'optimize'
     escape:
       max_cycles: 10          # DLS cycles per worker
       global_workers: 4       # top-level parallel goroutines (default 4)
       max_seconds: 0          # soft shared wall-clock budget in seconds (0 = unlimited)
       distance_threshold: 0.1 # normalised distance to call a point "new"
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
       clean_dls: true         # clean DLS closing each cycle (false = record the
                               # exploration result as-is; also skips the clean
                               # retry and the hull-rescue re-solve)

The DLS solve inside each worker parallelises the Jacobian across jacobian_workers
(optimization.jacobian_workers). Under the escape command, an unset
jacobian_workers defaults to 2 instead of GOMAXPROCS; with global_workers > 1 the
total goroutines are global_workers * jacobian_workers, so set jacobian_workers: 1
with many global workers to avoid oversubscription.

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
"rayweave trace"/"rayweave plot"), plus an global_result section listing
every discovered local minimum with its full surfaces.

  rayweave escape < input.yaml | rayweave trace | rayweave plot
  rayweave escape extract --index 1 < escape-output.yaml > min1.yaml
 `)
	case "pso":
		fmt.Print(`Usage: rayweave pso [--verbose] [--log FILE] [--save FILE]
                     [--keep-infeasible] [--stream BOOL] [--power-solve]
                     [--power-solve-surfaces SURFACES]
                     [--glass-variables] [--glass-dir DIR]
                     [--swarm-size N] [--pso-iterations N]
                     [--constraint-penalty W] [--gbest SCALE]
                     [--clean-dls BOOL] < input.yaml
       rayweave pso extract --index N < pso-output.yaml

Particle-swarm global optimisation: a plain multi-start search with no
escape-function bumps. Identical three-phase cycle to "escape"
(exploration -> glass -> clean DLS), but the first phase runs a swarm
instead of DLS, in normalized variable space with a static penalty for
constraint violations. escape and pso are the two global-optimisation
methods: they share the cycle budget, the worker pool, the deduplication
and the validation (global_workers, max_cycles, distance_threshold, ...)
and each keeps its own YAML section.

The swarm ranks its personal best on the penalised fitness
(merit + constraint_penalty * sum(c^2)); its global best is ranked on the
plain merit (--gbest merit, the default) or on the same penalised fitness
(--gbest penalised). With "merit" the swarm explores the merit landscape
and the cycle's DLS repairs the constraints; with "penalised" an infeasible
particle can never become gbest on a lower raw merit.

Options:
  --verbose        print PSO progress to stderr as compact JSONL
  --log FILE       write the full JSONL progress stream to FILE
  --save FILE      save each discovered local minimum to FILE0.yaml, FILE1.yaml, ...
  --keep-infeasible include infeasible basins in stdout YAML (default: discard)
  --mem-limit VALUE  cap the Go heap: 2GiB, 2048MiB, 2048 (MB) or -1
                   (unlimited); default optimization.mem_limit_mb
  --stream BOOL     stream the pipeline document to stdout as the run progresses (default on); --stream=false restores the one-shot write after the run
  --clean-dls BOOL  run the clean DLS that closes each cycle (default true).
                   --clean-dls false records the PSO result as-is (or the
                   glass-phase output when power_solve ran) and skips the
                   post-glass retry and the glass-hull rescue re-solve with it.
  --power-solve    insert the power-preserving glass phase between each PSO and clean DLS
  --power-solve-surfaces SURFACES
                   comma-separated surface IDs for the power-preserving solve
  --glass-variables auto-generate nd/vd optimization variables for every
                   refractive lens element
  --glass-dir DIR    AGF glass catalog directory
  --swarm-size N     number of particles per worker (overrides optimization.pso.swarm_size)
  --pso-iterations N PSO iterations per cycle (overrides optimization.pso.pso_iterations)
  --constraint-penalty W constraint penalty weight (overrides optimization.pso.constraint_penalty)
  --gbest SCALE     scale the global best is ranked on: merit (default, plain
                   merit; constraints are repaired by the cycle's DLS) or
                   penalised (merit + constraint penalty)
                   (overrides optimization.pso.gbest)
  --cpuprofile FILE  write a CPU profile (pprof) to FILE
  --memprofile FILE  write a heap profile (pprof) to FILE at exit
  --gorprofile FILE  write a goroutine profile to FILE at exit
  --gogc N           GC target percentage (0 = 400 for compute commands)

Sub-commands:
  pso (default)       run the PSO global optimisation loop
  pso extract --index N   extract local minimum N as a clean lens YAML

Input YAML — optimization.pso section:
   optimization:
     method: dls
     variables: [...]          # same variable definitions as 'optimize'
     pso:
       max_cycles: 10          # cycles per worker
       global_workers: 4       # top-level parallel goroutines (default 4)
       max_seconds: 0          # soft shared wall-clock budget in seconds (0 = unlimited)
       distance_threshold: 0.1
       variable_weights:
         curvature: 1000
         thickness: 1
       initial_perturb: 0.05
       swarm_size: 30          # particles per worker
       pso_iterations: 40      # PSO iterations per cycle
       inertia: 0.9            # starting velocity inertia (linear decay inertia->0.4; 0 = 0.9)
       cognitive: 1.494        # personal-best coefficient (c1)
       social: 1.494           # global-best coefficient (c2)
       velocity_clamp: 0.2     # max velocity as fraction of range
       init_spread: 0.1        # initial swarm spread as fraction of range
       constraint_penalty: 1000
       clean_dls: true          # clean DLS closing each cycle (false = record the
                                # PSO result as-is; also skips the clean retry and
                                # the hull-rescue re-solve)

Output: best solution in configs[].surfaces (pipeline-compatible with
"rayweave trace"/"rayweave plot"), plus an global_result section listing
every discovered local minimum with its full surfaces.

  rayweave pso < input.yaml | rayweave trace | rayweave plot
  rayweave pso extract --index 1 < pso-output.yaml > min1.yaml
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
  CODE V: RDY/THI/GLA/K/CIR/STO → sphere, ASP/A..J → asphere_polynomial

Examples:
  rayweave import --format zemax < lens.zmx > system.yaml
  rayweave import --format oslo < lens.len | rayweave trace
  rayweave import --format zemax < lens.zmx | rayweave plot -o lens.svg
`)
	case "export":
		fmt.Print(`Usage: rayweave export --format zemax|codev|oslo [--config ID] < system.yaml > out.zmx|.seq|.len

Exports a RayWeaver system back out as a native lens file for another optical
design tool. This is the inverse of "rayweave import". The output is plain
text written to stdout (foreign format). With -o/--output FILE the foreign
format is written to FILE instead and the input YAML passes through to stdout,
so the pipeline keeps flowing (like "rayweave plot -o").

Options:
  --format zemax|codev|oslo   output format; when -o/--output is given and
                        --format is omitted, the format is inferred from the
                        file extension (.zmx / .seq / .len)
  -o, --output FILE   write the foreign format to FILE and pass the input YAML
                        through to stdout; without it the foreign format goes
                        to stdout
  --config ID         export a single config; otherwise ZEMAX and CODE V
                        export every config (as ZEMAX multi-config MNUM/CONFIG/
                        THIC/SDIA, CODE V zoom positions ZOOM n + ZOO rows) and
                        OSLO exports config 0
  --nd-vd             CODE V: write every glass as its inline nd:vd model form
                        instead of the NAME_MANUFACTURER catalog name
  --glass-dir DIR     AGF glass catalog directory (for resolving glass
                        names / OSLO model-glass indices)

Config / format mapping:
  ZEMAX  all configs -> multi-config (base geometry + THIC/SDIA thickness &
         diameter overrides; per-config curvature differences are warned)
  CODE V all configs -> zoom positions (ZOOM n + ZOO RDY/THI/K/CIR/A..J rows;
         per-config vignetting as ZOO VUY/VLY/VUX/VLX F<n> rows)
  OSLO   single config only (NXT format)

Surface features:
  sphere / conic / even polynomial asphere, catalog + model glasses, fields,
  wavelengths, aperture stop, diameters, per-surface decenters
  (ZEMAX COORDBRK, CODE V DAR), ZEMAX/CODE V vignetting.

CODE V glass names follow the NAME_MANUFACTURER convention (uppercase, no
  hyphens/underscores in the glass name; the manufacturer comes from the glass
  catalog and is omitted when unknown). --nd-vd writes "nd:vd" model glasses
  instead.

Limitations (reported on stderr):
  folded mirrors are exported as transmit surfaces (no unfolding)
  Zernike aspheres export as sphere + conic
  OSLO cannot represent conics/aspheres/decenters (exported as spheres)
  multi-config per-config curvature differences cannot be expressed in ZEMAX
    THIC/SDIA (warned)

Examples:
  rayweave export --format zemax < system.yaml > out.zmx
  rayweave export -o out.zmx < system.yaml | rayweave plot -o lens.svg
  rayweave export --format codev < system.yaml | rayweave import --format codev
  rayweave export --format oslo --config config1 < system.yaml > out.len
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
  --efl TARGET     target effective focal length (mm, required unless
                   scale.efl is set in the input YAML)
  --config ID      select config by id (multi-config mode); its EFL sets the
                     scale factor applied to every config

Input YAML — scale section (optional; --efl overrides, the effective value is
written back into the output):
  scale:
    efl: 50.0        # target effective focal length (mm)

Example:
  rayweave scale --efl 50 < ref25.yaml | rayweave optimize > optimized.yaml
`)
	case "cam":
		printHelpText(`Usage: rayweave cam [--config ID] [--wl MM] [--max-iter N] [--tolerance T] < system.yaml

First-order cam solve of a multi-config (zoom) system. For every position the
airspaces declared in cam.surfaces are solved on the paraxial trace alone so
that
  1. the position reaches its target EFL,
  2. the paraxial focus lands on the image plane, and
  3. the image plane stays at one fixed Z (unless hold_image_plane: false).
No ray tracing and no merit: the solve is deterministic and takes milliseconds,
so it lays the zoom out before optimize gives it its aberrations. The extra
freedom left when there are more airspaces than conditions is spent on the
smallest layout change from the input (minimum-motion cam).

Solved thicknesses are written back into configs[].surfaces[].thickness, the
same way 'rayweave scale' writes back its scaled surfaces, and reported in a
cam_result section.

Options:
  --config ID          solve only this config (a selector, not a setting)
  --wl MM              reference wavelength in mm for the paraxial solve
                       (default: chief.reference_wavelength, else 0.00058756);
                       written back into chief.reference_wavelength
  --max-iter N         maximum solver iterations (default: cam.max_iterations,
                       else 50); written back into cam.max_iterations
  --tolerance T        convergence tolerance on the residuals in mm (default:
                       cam.tolerance, else 1e-9); written back into cam.tolerance
  --verbose            print one line per accepted iteration to stderr
  --glass-dir DIR      AGF glass catalog directory

Input YAML — cam section:
  cam:
    surfaces: [3, 5, 8, 12]      # the *following* airspace of each ID is solved
    image_plane: 17              # default: chief.reference_surface, else last surface
    image_plane_z: 110.0         # default: the first solved config's image-plane Z
    hold_image_plane: true       # default true
    targets:
      - {config: config0, efl: 28.0}
      - {config: config1, efl: 44.0}
      - {config: config2, efl: 70.0}
    max_iterations: 50           # optional
    tolerance: 1.0e-9            # optional
    motion_weight: 0             # optional; >0 trades residual for less motion

The image plane must be the last surface of every solved config. A position
that cannot be solved (too few cam airspaces for the conditions, or a geometry
that would need a negative airspace) is reported as not converged with its
residuals instead of being silently accepted.

Example:
  rayweave cam < zoom.yaml | rayweave zoom | rayweave query --each
      'zoom_summary.configs[]:id,focal_length,image_plane_z'
`)
	case "asphere":
		fmt.Print(`Usage: rayweave asphere [--t-bins 8] [--pupil-samples 21] [--top-k 3] [--sag-scale 0.2] < system.yaml

Ranks the candidate surfaces for asphere introduction and estimates safe initial
even-order asphere coefficients (conic + A4..A12) from the per-field OPD
residuals. The score rewards surfaces where a rotationally-symmetric asphere can
simultaneously correct the shared (common) OPD across fields while penalising
inter-field conflict, manufacturing difficulty and optimisation instability.

Options:
  --rings N               polar cell radial rings (default 8)
  --angles N              polar cell angular sectors (default 16)
  --t-bins N              beam-frame tangential bins (default 8)
  --pupil-samples N       pupil grid radial samples (default 21)
  --sensitivity-samples N sensitivity trace radial samples (default 9; 0 = analytic proxy)
  --top-k N               number of top-ranked surfaces to fit (default 3)
  --sag-scale α           initial sag scale (default 0.2; try 0.05..0.5)
  --calibrate-scale BOOL  derive each candidate's embedded scale from the measured
                          ray-trace response (on by default; --calibrate-scale false
                          falls back to the fixed sag_scale)
  --scale-probes LIST     verify these scales instead of the quadratic estimate
                          (e.g. "0.1,0.25,0.5,1.0")
  --validate              run a short DLS per fitted surface to verify the asphere improves the merit
  --apply                 insert the top-ranked DLS-validated asphere onto its surface and
                          output the modified system (implies --validate). Pipeline friendly:
                          asphere --validate --apply < lens.yaml | chief | trace | plot
  --dls-iter N            DLS iterations per validated surface (default 20, with --validate)
  --num-rays N            pupil grid rays for validation DLS (default 64)
  --diagnostics LIST      extra diagnostics: opd (OPD map), focus (T/S focus channel)
                          (empty = none; see diagnostics: in asphere_candidate)
  --focus-samples N       rays per T/S fan for the focus channel (default 17)
  --focus-fans LIST       focus channel fans: tangential,sagittal (default both)
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
    calibrate_scale: true       # per-surface scale from the measured response
    scale_probes: []            # explicit scales to verify ([] = quadratic)
    t_bins: 8
    pupil_samples_radial: 21
    sensitivity_samples: 9      # 0 = analytic proxy only
    remove_tilt: true
    remove_defocus: false
    top_k: 3
    min_rays_per_cell: 3
    validate: false             # run the short-DLS validation (--validate)
    apply: false                # insert the top validated asphere (--apply)
    validation_dls_iter: 20     # --dls-iter
    validation_num_rays: 64     # --num-rays
    diagnostics: []             # opd | focus (extra output channels)
    focus_samples: 17           # rays per T/S fan (focus channel)
    focus_fans: []              # empty = tangential + sagittal
    cell_rings: 8               # polar cell rings (--rings)
    cell_angles: 16             # polar cell sectors (--angles)
     score_weights: {common: 0.30, unique: 0.10, fit: 0.20, sensitivity: 0.15,
                     conflict: 0.10, asym: 0.10, manufacturing: 0.05}

Flags override the asphere_candidate: values; the effective (flag-won) values
are written back into the output's asphere_candidate: section (CLI/YAML rule).

Piston is always removed (per-field OPD referenced to the field mean), so
there is no piston knob. The former remove_piston / max_sag / max_slope_deg /
max_curvature_variation keys were removed from the document schema and are
ignored.

Output: YAML with an asphere_candidate_result: section (rankings with
coefficients, scaled_coefficients, calibrated_coefficients, sensitivity with
the calibrated_scale/merit/improvement, and, with --validate, a
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
  --spectral S        polychromatic ("white") PSF: D65 (default) | FLAT
  --psf-workers N     parallel workers for the Huygens integral and wavefront
                         tracing (default: GOMAXPROCS)
  --max-freq N        MTF frequency cap in cycles/mm (default:
                         psf.mtf_config.max_frequency, else the Nyquist)
  --mtf-spectral S    MTF polychromatic weighting: D65 (default) | FLAT
                         (independent of PSF spectral)
  --mtf-combination S MTF combination method: intensity (default) | otf
  --best-focus        evaluate each field at its best-focus image plane
                         (the shift maximizing the coherent PSF peak, seeded by
                         the geometric spot-RMS focus), so a field-curved
                         system's defocus-dominated fixed-plane Strehl is
                         replaced by the wavefront-quality number
  --focus-plane ''|all  evaluate at a common image plane instead of the input
                         one: 'all' moves it to the single all-field best focus
                         (the uniform-weighted wavefront back-focus solve) and
                         reports the applied shift on stderr
  --converge-check BOOL  label sampling convergence by re-evaluating each result
                         at a higher ray count (on by default; --converge-check
                         false skips the 1.5x re-evaluation)
  --converge-tol T     relative Strehl change threshold for --converge-check
                         (default 0.10)
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
    spectral_curve: D65
    best_focus: false
    mtf_config:
      max_frequency: 200
      spectral_curve: D65
      combination_method: otf

Output: augmented YAML with a lightweight psf_results[] summary (Strehl,
FWHM, centroid, encircled energy, Airy radius, sampling counts). Full grids
are written to --yaml/--csv files referenced by output_file.

Notes:
  - The default reference surface is the last optical surface (the surface
    before the image plane). The wavefront is sampled there and propagated to
    the fixed flat image plane; field curvature and defocus therefore appear
    naturally in the PSF.
  - --best-focus (or psf.best_focus) evaluates each field at its best-focus
    image plane instead: the plane shift that minimizes the geometric spot RMS
    is applied per field before the Huygens integral, removing field-curvature
    defocus. Use it when the fixed-plane Strehl is dominated by focus, e.g. to
    compare against the wavefront command's best-focus rms/strehl. The applied
    shift is reported per result as best_focus_shift_mm.
  - The image grid is auto-enlarged when the requested grid would leave the
    Airy core smaller than a pixel (fast or aberrated systems, where the
    geometric spot dominates the window): the peak-ratio Strehl is only
    meaningful when the diffraction-limited reference core is resolved, so the
    grid is raised until the pixel pitch is <= Airy radius/2. The effective
    grid size is reported in psf_results[].grid_size.
  - For strongly aberrated fields the PSF is a coherent speckle pattern whose
    peak (and hence Strehl) is sensitive to the pupil sampling. Increase
    --num-rays (e.g. 900..1600) for reliable off-axis metrics.
  - The reported wavefront rms_opd/pv_opd are referenced to the best-fit
    sphere (piston + tilt + defocus removed), the standard wavefront
    aberration definition.
`)
	case "focus":
		fmt.Print(`Usage: rayweave focus <mtf|psf|spot> [flags] < input.yaml

Image-plane comparison: evaluate the same system at up to three focus
conventions and report the requested metric for each, so it is clear which
image plane the design actually delivers.

Planes (--planes file,all,best; default: all three):
  file   the image plane as written in the input (no solve)
  all    the single all-field best focus (uniform-weighted wavefront solve),
         the plane a saved escape/optimize minimum carries
  best   each field's own best focus (removes field-curvature defocus)

Through-focus (--through-focus FROM,STEP,COUNT): replaces the plane comparison
with a scan of COUNT planes at FROM + i*STEP (mm). --planes then selects the
base plane(s) the scan is measured from: file (default) | all | on_axis (the
on-axis best focus); the per-field best base is rejected. One scan is produced
per base; focus_mm is relative to that base.

Sub-subcommands:
  focus mtf   MTF (with Strehl) at the requested spatial frequencies,
              tabulated per (field, wavelength) x plane.
  focus psf   PSF metrics (Strehl, FWHM, encircled energy, centroid) per
              (field, wavelength) x plane.
  focus spot  geometric (ray) spot RMS and its tangential/sagittal (rms_t,
              rms_s) and x/y (rms_x, rms_y) decompositions per plane. Needs no
              Huygens integral, so it is much cheaper than focus psf.

The base psf command remains the single-plane imaging measurement; focus
is the plane-comparison layer on top of the same engine.

Options (all sub-subcommands):
  --wavelengths W1,...  wavelengths in mm (default: config wavelengths,
                          else the chief reference wavelength)
  --fields I1,I2,...    field indices to compare (default: all)
  --planes LIST         file,all,best (default: all three); with
                          --through-focus: base planes file,all,on_axis
                          (default: file)
  --through-focus F,S,N through-focus scan FROM,STEP,COUNT (mm, number of
                          planes); scans around each base plane in --planes
  --num-rays N          pupil grid rays (default 400)
  --psf-grid N          image-plane pixels per side (mtf/psf; default 64)
  --ref-surface N       reference surface ID for wavefront sampling
                          (default: the last optical surface)
  --polarization S      RCP (default) | LCP | X | Y | RCP+LCP
  --converge-check BOOL label sampling convergence (mtf/psf; default: off)
  --config ID           select config by id (multi-config mode)
  --glass-dir DIR       AGF glass catalog directory
  focus mtf only:
  --frequencies F1,...  spatial frequencies in cycles/mm (default: 50)

Input YAML — focus section (optional; flags override):
  focus:
    mtf:
      wavelengths: [0.0004861, 0.0005876, 0.0006563]
      frequencies: [30, 50, 80]
      planes: [file, all, best]
    psf:
      planes: [all, best]
      through_focus: {from: 0.0, step: 0.01, count: 10}
    spot:
      planes: [file, all, best]

Output: augmented YAML with a focus_comparison section holding the mtf, psf or
spot table (rows = field x wavelength, each with the file / focus_plane_all /
best_focus plane blocks), or a through_focus scan when --through-focus is
given. No table is printed to stderr; pipe the output into "rayweave list
focus" for a human-readable comparison.
`)
	case "wavefront":
		fmt.Print(`Usage: rayweave wavefront [flags] < pipeline.yaml

Wavefront analysis on the reference surface (default: the last optical
surface): per-field polarized ray tracing → always-computed paraboloid fit
(ax² + by² + cxy + dx + ey + f) → best-focus sphere (geometric spot RMS along
the image-plane normal) → stabilized Fringe-Zernike decomposition of the
low-order-removed residual → weighted best-image-plane shift.

The pipeline YAML carries a lightweight wavefront_result summary (coefficients
and RMS/PV/Strehl only). Full wavefront data (scattered samples + interpolated
residual-OPD map) go to --yaml/--csv files referenced by output_file.

Options:
  --ref-surface N      reference surface ID for wavefront sampling
                         (default: the last optical surface)
  --num-rays N         pupil grid rays (default 400)
  --fields I1,I2,...   field indices to compute (default: all)
  --wavelengths W1,...   wavelengths in mm (default: chief wavelengths,
                         else 587.56 nm)
  --polarization S     RCP (default) | LCP | X | Y | RCP+LCP
  --zernike-order N    highest Fringe Zernike index to fit (default 15)
  --wavefront-workers N  per-field task parallelism (default: GOMAXPROCS)
  --map-grid N         wavefront map resolution per side for --csv (default 64)
  --best-focus         compute the weighted best image-plane shift and apply it
                         to the output configs' image-plane decenter
  --focus-weight T     best-focus weighting: uniform (default) | custom
  --focus-weights W1,...  per-field weights when --focus-weight custom
  --output-shifted-lens FILE  write the shifted lens document to FILE
  --yaml FILE          write full structured data (scattered samples + map) to
                         FILE, one index-suffixed file per result
  --csv FILE           write a gnuplot x,y,opd wavefront map to FILE, one
                         index-suffixed file per result
  --config ID          select config by id (multi-config mode)
  --glass-dir DIR      AGF glass catalog directory

Input YAML — wavefront section (optional; flags override):
  wavefront:
    reference_surface: 7
    num_rays: 400
    fields: [0, 1]
    wavelengths: [0.00058756]
    polarization: "RCP"
    zernike_max_order: 15
    workers: 8
    map_grid: 64
    best_focus:
      enabled: true
      weight_type: "uniform"   # uniform | custom
      custom_weights: []
      output_shifted_lens: "shifted.yaml"

Output: augmented YAML with a wavefront_result (per-field paraboloid,
best-fit-sphere, stabilized Fringe-Zernike, statistics; plus best_focus when
enabled). With --best-focus the image plane of each output config is moved to
the weighted best-fit-sphere focus by adjusting its decenter Z shift, so the
output can be piped straight into psf (or trace/plot) for a best-focus
PSF.

Notes:
  - statistics.rms/pv/strehl are referenced to the best-fit sphere (piston +
    tilt + defocus removed, astigmatism retained) and match psf --best-focus's
    rms_opd/pv_opd and Strehl. Strehl is the exact peak-ratio
    |<e^{i(2pi/lambda)W}>|^2 (pupil-area-weighted coherent average of the
    residual wavefront W), which stays meaningful for residual RMS beyond the
    Marechal limit (~0.2 lambda) where exp(-(2π·σ/λ)²) collapses towards 0.
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

YAML/JSONL selector with in-memory edits: prints one plain-text value per
invocation (the value the demo scripts used to fetch with python3 + PyYAML).
Works on any YAML on stdin, including output of the other subcommands.

SELECTOR is an expression; paths are a subset of it:
  paraxial_result.focal_length
  chief_rays[0].spot_stats.rms_r
  chief_rays[field_angle=0].spot_stats.rms_r     (filter by equality)
  configs[id=config1].surfaces[id=2].thickness
  results[].surfaces[interaction=REFLECT].intensity_s
  .                    the whole document (like jq .)
  .ray                 shorthand for ray (leading dot is stripped)

Expressions also support arithmetic (+ - * / %), comparisons, &&/||/!,
{...}/[...] literals, and the functions abs sqrt pow min max sin cos tan
asin acos atan atan2 radians degrees exp log floor ceil round len has.

Output modes:
  default   scalar -> raw text line; a single-element array unwraps; a
            larger array prints as [a b c]
  --yaml    serialize the result as YAML (subtree dump, records, lists)
  --json    serialize the result as JSON
  --csv     PATH:col1,col2,... -> CSV rows, skipping rows with missing
            columns (add --csv-header for a header; --csv-keep-all to keep all rows)
  --gate    evaluate EXPR, print the value and exit 0/1 by truthiness

Iteration and aggregates:
  --each 'PATH:col1,col2,...'   one row per element (format with --printf)
  --count PATH   non-null element count   --len PATH   array length
  --sum PATH     sum of numeric values    --product PATH  product
  --stdev PATH   population standard deviation of numeric values

Bindings (evaluated in order; later ones can reference earlier ones):
  --set VAR=EXPR   a PATH, a number, or an arithmetic expression
  --yaml --set a=... --set b=...        emit a {a:.., b:..} record

Mutations (in-memory deep copy; repeatable, later edits see earlier ones):
  --edit 'PATH = VALUE'        set (auto-creates intermediate maps)
  --edit 'PATH |= EXPR'        update with . bound to current value
  --edit 'ARR += VALUE'        append to array (also ARR[] += VALUE)
  --edit 'ARR -= INDEX'        remove array element at INDEX
  --edit 'del PATH'            delete map key or array element
  PATH is dot/index chain (a.b[0].c); VALUE/EXPR is any expression.
  Example: rayweave query --yaml --edit 'surfaces[0].thickness = 5.5' '.' < lens.yaml
  See docs/query.md §10 for details.

Input:
  YAML on stdin (default). With --jsonl read one JSON object per line
  (e.g. "optimize --log" or "escape --log" output); --where EXPR keeps
  matching lines, --first uses the first match instead of the last, and
  --count '[]' counts the matching lines.

Options:
  --default STR    value printed when a scalar is missing/null (default -1)
  --default-num STR numeric default for missing/null --each columns (e.g. NaN, 0)
  --printf FMT     Go fmt format string (e.g. '%.4f') for the output value
  --printf-nan-empty with --printf, replace NaN values with width-matched spaces
  -r, --raw        raw text output (the default for scalars)
  --expr EXPR      same as the positional SELECTOR
  --edit EXPR      mutation expression (repeatable; see Mutations above)
  --set VAR=EXPR   bind variable (repeatable)

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
  echo 'a: {b: 1}' | rayweave query --yaml --edit 'a.b |= . + 1' '.'
  rayweave query --each 'surfaces[]:id,radius,thickness' \
    --default-num NaN --printf-nan-empty --printf '%d,%8.4f,%8.4f' < lens.yaml

See docs/query.md for the full manual.
`)
	case "zoom":
		printHelpText(`Usage: rayweave zoom [--wl MM] [--num-rays N] [--config ID] < system.yaml

Summarises every active configuration of a multi-config (zoom) system in one
zoom_summary section: the first order of that position, each field's image
height, its spot RMS and the surviving fraction of its prescribed pupil grid.
Read-only — nothing is sized and no merit is evaluated — so it answers "where
are the zoom positions?" without looping paraxial and chief by hand.

Options:
  --wl MM              reference wavelength in mm for the first-order trace and
                       the grids (default: chief.reference_wavelength, else
                       0.00058756); written back into chief.reference_wavelength
  --num-rays N         pupil-grid rays per field (default: chief.num_rays, else
                       64); written back into chief.num_rays when given
  --config ID          summarise only this config (a selector, not a setting)
  --glass-dir DIR      AGF glass catalog directory

Output: augmented YAML with a zoom_summary: section:

  zoom_summary:
    reference_wavelength: 0.00058756
    num_rays: 64
    configs:
      - id: config0            # one row per config (zoom position)
        active: true
        focal_length: ...      # paraxial, same value paraxial_result reports
        entrance_pupil_diameter: ...
        image_space_f_number: ...       # working f-number
        inf_conj_image_space_f_number: ...  # design f-number
        total_track: ...       # first surface to image plane (mm)
        image_plane_z: ...     # reference surface vertex Z (document frame)
        fields:
          - field_index: 0
            angle_deg: 0
            image_height: 0
            spot_rms: ...      # mm, chief_rays[].spot_stats.rms_r
            vignetting: 1      # surviving / launched grid rays
            grid_total: 64
            grid_surviving: 64

Examples:
  rayweave zoom < zoom.yaml
  rayweave zoom --config config2 --num-rays 128 < zoom.yaml
  rayweave zoom < zoom.yaml | rayweave query --each 'zoom_summary.configs[]:id,focal_length,image_plane_z'
`)
	default:
		fmt.Print(`Usage: rayweave <subcommand> [< input.yaml]

RayWeave — geometric ray tracing and optical design toolkit.

Subcommands:
  chief      Determine chief rays (spot centroid) for each field
  trace      Trace ray(s) through the system
  paraxial   Paraxial (first-order) ray trace
  zoom       Per-config summary of a multi-config system (first order, spot,
             vignetting)
  tmm        Thin-film coating analysis (transfer-matrix method)
  vignette   Iteratively settle vignetting and auto_aperture diameters
  plot       Generate SVG cross-section drawing
  optimize   DLS optimization of lens surfaces
  escape     Escape-function global optimization (multiple local minima)
  pso        PSO global optimization (particle swarm, no escape bumps)
  scale      Scale a system so its EFL equals --efl TARGET
  cam        First-order cam solve of a zoom (airspaces, fixed image plane)
  asphere    Rank surfaces for asphere introduction, estimate initial coefficients
  psf        Point-spread function via direct vector Huygens integration
  focus      Image-plane comparison of MTF/PSF across focus conventions
  wavefront  Wavefront analysis (paraboloid, best-fit sphere, Fringe Zernike, best focus)
  import     Import system from ZEMAX ZMX / CODE V SEQ / OSLO LEN
  export     Export system to ZEMAX ZMX / CODE V SEQ / OSLO LEN
  query      YAML/JSONL selector with in-memory edits
  clean      Strip calculation results, keeping only configuration
  list       Read-only listing of system data (surfaces, glasses, rays)

Use "rayweave help <subcommand>" or "rayweave <subcommand> --help"
  for detailed options and YAML structure.

Companion binaries (same module):
  rwrun         Start a long run detached and stop it with SIGTERM twice
  rayweave-mcp  Serve these subcommands to AI agents over MCP (stdio)

Manual: docs/rwrun.md, docs/mcp.md.
`)
	case "list":
		fmt.Print(`Usage: rayweave list [--format table|yaml|json|csv] [--config ID]
                   [--glass-dir DIR] [--curvature] [--auto-aperture] [--all-glasses]
                   [--roles] [--summary] [TARGET...] < input.yaml

Read-only listing of an optical system's definition data. Unlike the pipeline
subcommands, list never traces rays and prints formatted tables by default,
not pipeline YAML.

Targets (space-separated, flags may appear before or after them;
default: surfaces, glasses, paraxial and fields — the keyword "default"
expands to this set in place, e.g. "list default merit" shows the default
targets followed by merit; the keyword "all" expands to all nine targets
and implies --auto-aperture, --all-glasses, and --roles):
  surfaces   surface table of the selected config (object plane 0 excluded)
              With --auto-aperture, also shows the per-surface auto_aperture
              state (auto = resized by vignette/clear-aperture/optimize,
              fixed = never resized)
  glasses    refractive-index table of the glasses used by the selected
             config's surfaces (first-use order) plus unresolved keys.
             With --all-glasses, also includes the remaining glass_catalog entries
  paraxial   first-order paraxial properties (EFL, F/#, NA, EPD, BFL, etc.)
             With --roles, also includes the per-element glass-role table
  fields     field-of-view definitions from chief.fields[] (or configs[].fields[]
             as fallback). Shows each field's type (angle, image_height, height)
             with input values from the YAML and computed values (actual angle,
             image height) from the real-ray chief trace. When input and computed
             values match, only one is shown; when they differ (e.g. image_height
             mode where the angle is solved iteratively), both are shown.
  rays       ray trace results from results[] section (requires trace output).
             Shows a summary table (ID, λ, OPL, Is/Ip, final cumulative
             transmittance Tcum s/p, surface/transmit/miss counts) plus
             per-surface detail (position, direction, interaction, OPL,
             per-surface intensity Is/Ip, Jones vector, cumulative transmittance
             from entrance (Tcum s/p), and with --details data: reflection
             intensity Irs/Irp, angle of incidence, n1/n2, Fresnel coefficients)
             when the data contains surface results. Use --summary to show only
             the summary table.
  merit      merit function definition from configs[].merit, configs[].merit_modes
             and configs[].constraints. Shows merit terms (kind, field, wavelength,
             weight, target), merit modes with term counts, and constraints.
             When piped from optimize/escape, also shows the optimization result
             (status, iterations, merit).
  optimization optimization configuration from optimization[]: solver settings
             (method, max_iter, tol, mu, ...), variables (per-config,
             shared with bindings, local), the merit_schedule section, and the
             sub-configs (escape, pso, glass_hull, degenerate, power_solve,
             region_active, adaptive_damping). Constraints are not shown here —
             the effective constraint display lives in the merit target (per-
             config constraints, else inherited from optimization.constraints).
              When piped from optimize, also shows the optimization result
              (status, iterations, active mode, metric value).
  global     global optimisation results for either global method (escape or
             pso). Reads either the global_result section of a pipeline
             document or, when the input is detected to be an escape/pso JSONL
             run log (a --log file or a captured --verbose stream), the
             equivalent information reconstructed from the log: the run
             parameters (escape-only ones listed for an escape run), the run
             aggregate (workers, cycles, explorations, minima, run elapsed),
             the local minima (merit-sorted rank and classification), and a
             worker table with each worker's completion state (completed /
             timeout / interrupted / retired), cycles reached, exploration and
             recording counts, best merit,
             elapsed time and retirement reason. It also lists the run-level
             log events (Log Events): the signals the process received with the
             stage they stopped at (interrupt / interrupt_dls / force_quit), the
             resource-guard actions and samples, and errors. Infeasible basins
             are a separate list (a document shows them only with
             --keep-infeasible; a run log always has them) and never count as
             the best solution. From a pipeline document the per-minimum file
             names and element powers are shown as well (file directory when all
             minima share the same path); a run log recovers the file names
             from the log itself — both the --log file and a captured --verbose
             stream — and the minimum classification with it.
  focus      image-plane comparison results from focus_comparison (requires
             focus mtf / focus psf / focus spot output). Auto-detects the mtf,
             psf, spot or through_focus sub-section and renders the per-(field,
             wavelength) x plane table (file / focus_plane_all / best_focus), the
             through-focus scan (base x focus_mm), plus the MTF/PSF/spot metrics.

Options:
  --format table|yaml|json|csv   output format (default table)
  --config ID                    select config by id (multi-config mode)
  --glass-dir DIR                AGF glass catalog directory
  --curvature                    show curvature instead of radius
  --auto-aperture                for surfaces: show the auto_aperture state
                                 column (auto/fixed)
  --all-glasses                 for glasses: show all glasses, including
                                 glass_catalog entries not used by any surface
  --roles                        for paraxial: also show element roles table
  --summary                      for rays: show only summary (no per-surface detail)

surfaces columns: ID, Type, Radius[mm] (or Curvature[1/mm] with --curvature),
Thickness[mm], Material, Diameter[mm], and with --auto-aperture an Auto Ap
column (auto/fixed; raw auto_aperture bool in yaml/json/csv). A flat surface
has no finite radius: it is shown as "inf" in tables and null/empty in
yaml/json/csv.

When the config contains aspheric surfaces (asphere_polynomial /
asphere_zernike), an "Asphere Coefficients:" section follows the Surfaces:
section with one row per asphere: ID, Type, Conic, and the even-order
coefficients A4..A12 present on that surface. With no aspheres the section
is omitted entirely; structured output carries it under
asphere_coefficients only when non-empty.

Without --config and with multiple configs whose common surface IDs carry
different thickness across configs, a final "Thickness Differences:"
section compares them: one row per config (Config index + Name), one column
per differing surface headed Surface <ID>. The section is skipped when
--config is given or no differences exist; structured output carries it
under thickness_differences only when non-empty.

Material column: AIR, resolved catalog glass name (+ manufacturer when known),
inline model glass as nd:vd, or the raw key when it does not resolve in the
catalog.

glasses columns: Name, Mfr (only when a glass carries a manufacturer), Type,
nd and vd (the d-line index and Abbe number: stored values when present,
computed from the dispersion data otherwise; vd is "-" for constant-index
glasses), and one n column per collected wavelength (longest first).
Wavelengths come
from chief.reference_wavelength and every config's wavelengths, deduplicated;
the d line is used when the input carries none. Type labels: the dispersion
formula name for catalog glasses, "constant" for a fixed index, "model" for
nd/vd-based glasses, "tabulated" for index tables, "-" for keys that do not
resolve in the catalog (their nd/vd/n cells are empty; a warning is printed).

paraxial shows first-order optical properties: EFL, BFL, F/#, NA, entrance/exit
pupil diameter and location, half-angle of view, total track, and magnification
(for finite conjugates). With --roles, a per-element glass-role table is
appended showing surface IDs, thin-lens power, chromatic weight, role
(dominant/compensating/neutral), and target vd/nd.

Examples:
  rayweave list < lens.yaml
  rayweave list --config wide < lens.yaml
  rayweave list --curvature --format csv < lens.yaml
  rayweave list surfaces glasses < lens.yaml
  rayweave list paraxial < lens.yaml
  rayweave list paraxial --roles < lens.yaml
  rayweave escape --log run.jsonl < lens.yaml > out.yaml
  rayweave list global < run.jsonl
  rayweave trace single --origin 0,5,-100 --angle-yz 5 < lens.yaml | rayweave list rays
  rayweave chief | rayweave trace | rayweave list rays --summary
  rayweave focus psf --planes all,best < lens.yaml | rayweave list focus
  rayweave focus mtf --frequencies 30,50,80 < lens.yaml | rayweave list focus --format csv
`)
	case "clean":
		fmt.Print(`Usage: rayweave clean [--verbose] < pipeline.yaml

Strips calculation results from a pipeline document, keeping only
configuration settings. The metadata.tool section is overwritten with
this tool's own identity.

Removed fields: chief_rays, results, paraxial_result, opt_results,
global_result, global_minimum, vignetting_result,
asphere_candidate_result, psf_results, focus_comparison,
wavefront_result.

The "stop" section is preserved (it may be hand-entered as input).

Options:
  --verbose   print removed fields as JSONL to stderr

Examples:
  rayweave chief < lens.yaml | rayweave clean > clean.yaml
  rayweave optimize < lens.yaml | rayweave clean --verbose > clean.yaml
`)
	}
}
