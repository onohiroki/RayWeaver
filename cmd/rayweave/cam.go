package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/hiroki/rayweaver/internal/cam"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// runCam solves the first-order cam of a multi-config (zoom) system: the
// airspaces after cam.surfaces, one set per config, so that every position
// reaches its target EFL while the paraxial focus lands on an image plane
// pinned to one fixed Z. The solve is paraxial only — no ray tracing, no merit
// — so it is fast and deterministic, and it is meant to run before optimize
// gives the zoom its aberrations.
//
// The solved airspaces are written back into configs[].surfaces[].thickness,
// the same way `scale` writes back the scaled surfaces, and the residuals are
// reported in a cam_result section.
func runCam(data []byte) {
	fs := flag.NewFlagSet("cam", flag.ExitOnError)
	maxIter := fs.Int("max-iter", 0, "maximum solver iterations (default: cam.max_iterations, else 50)")
	tolerance := fs.Float64("tolerance", 0, "convergence tolerance on the residuals in mm (default: cam.tolerance, else 1e-9)")
	configFlag := fs.String("config", "", "solve only this config by id (default: every active config)")
	wlFlag := fs.Float64("wl", 0, "reference wavelength in mm for the paraxial solve")
	verbose := fs.Bool("verbose", false, "print one line per accepted solver iteration to stderr")
	glassDir := fs.String("glass-dir", "", "AGF glass catalog directory")
	fs.Parse(os.Args[2:])

	if flagWasSet(fs, "max-iter") && *maxIter <= 0 {
		errOut("Error: --max-iter must be positive (got %d)", *maxIter)
		os.Exit(1)
	}
	if flagWasSet(fs, "tolerance") && *tolerance <= 0 {
		errOut("Error: --tolerance must be positive (got %g)", *tolerance)
		os.Exit(1)
	}
	if flagWasSet(fs, "wl") && *wlFlag <= 0 {
		errOut("Error: --wl must be positive (got %g)", *wlFlag)
		os.Exit(1)
	}

	input := parseYAML[types.Input](data)
	if len(input.Configs) == 0 {
		errOut("Error: 'configs' is required (one config per zoom position)")
		os.Exit(1)
	}
	if input.Cam == nil || len(input.Cam.Surfaces) == 0 {
		errOut("Error: 'cam.surfaces' is required (the surface IDs whose following airspace is a cam variable)")
		os.Exit(1)
	}

	// Reference wavelength: --wl wins over chief.reference_wavelength, with the
	// built-in default last; a flag-set value is written back (CLI/YAML rule).
	wavelength := effectiveReferenceWavelength(input.Chief)
	if flagWasSet(fs, "wl") {
		wavelength = *wlFlag
		if input.Chief == nil {
			input.Chief = &types.ChiefInput{}
		}
		input.Chief.ReferenceWavelength = wavelength
	}

	gc, _ := loadCatalogs(&input, *glassDir)
	writeBackGlassDir(&input, *glassDir)

	// Targets: every solved position needs one.
	if len(input.Cam.Targets) == 0 {
		errOut("Error: 'cam.targets' is required (one {config, efl} per solved position)")
		os.Exit(1)
	}
	targets := make(map[string]float64, len(input.Cam.Targets))
	for _, t := range input.Cam.Targets {
		if t.Config == "" {
			errOut("Error: cam.targets[] needs a config id")
			os.Exit(1)
		}
		if t.EFL <= 0 {
			errOut("Error: cam.targets[%s].efl must be positive (got %g)", t.Config, t.EFL)
			os.Exit(1)
		}
		if _, dup := targets[t.Config]; dup {
			errOut("Error: cam.targets names %q twice", t.Config)
			os.Exit(1)
		}
		targets[t.Config] = t.EFL
	}

	// Which positions: the --config selector, else every active one.
	var sel []int
	if *configFlag != "" {
		idx, err := resolveConfig(input.Configs, *configFlag)
		if idx < 0 {
			errOut("Error: %s", err)
			os.Exit(1)
		}
		sel = []int{idx}
	} else {
		for i := range input.Configs {
			if input.Configs[i].Active {
				sel = append(sel, i)
			}
		}
	}
	if len(sel) == 0 {
		errOut("Error: no active config to solve")
		os.Exit(1)
	}
	var missing []string
	for _, i := range sel {
		if _, ok := targets[input.Configs[i].ID]; !ok {
			missing = append(missing, input.Configs[i].ID)
		}
	}
	if len(missing) > 0 {
		errOut("Error: cam.targets is missing an EFL for %s (every solved position needs one)", strings.Join(missing, ", "))
		os.Exit(1)
	}

	// Image plane: cam.image_plane, else the chief reference surface, else the
	// last surface of the first solved config. The Z it is held at defaults to
	// that config's current image-plane vertex: with several positions solved,
	// the first one is the reference the others are moved to (one detector).
	refIdx := sel[0]
	imagePlane := input.Cam.ImagePlane
	if imagePlane <= 0 && input.Chief != nil && input.Chief.ReferenceSurface > 0 {
		imagePlane = input.Chief.ReferenceSurface
	}
	if imagePlane <= 0 {
		if n := len(input.Configs[refIdx].Surfaces); n > 0 {
			imagePlane = input.Configs[refIdx].Surfaces[n-1].ID
		}
	}
	if imagePlane <= 0 {
		errOut("Error: cannot determine the image plane (set cam.image_plane or chief.reference_surface)")
		os.Exit(1)
	}

	imagePlaneZ := input.Cam.ImagePlaneZ
	if imagePlaneZ <= 0 {
		refSurfaces := append([]types.Surface{}, input.Configs[refIdx].Surfaces...)
		surface.Precompute(refSurfaces)
		z, ok := camSurfaceZ(refSurfaces, imagePlane)
		if !ok {
			errOut("Error: image plane %d is not in config %q", imagePlane, input.Configs[refIdx].ID)
			os.Exit(1)
		}
		imagePlaneZ = z
	}

	hold := true
	if input.Cam.HoldImagePlane != nil {
		hold = *input.Cam.HoldImagePlane
	}

	// Resolve the solver settings and echo the effective values back into cam.
	maxIterations := input.Cam.MaxIterations
	if maxIterations <= 0 {
		maxIterations = 50
	}
	if flagWasSet(fs, "max-iter") {
		maxIterations = *maxIter
		input.Cam.MaxIterations = maxIterations
	}
	tol := input.Cam.Tolerance
	if tol <= 0 {
		tol = 1e-9
	}
	if flagWasSet(fs, "tolerance") {
		tol = *tolerance
		input.Cam.Tolerance = tol
	}
	input.Cam.ImagePlane = imagePlane
	input.Cam.ImagePlaneZ = imagePlaneZ
	input.Cam.HoldImagePlane = &hold

	stopSurface := 0
	if input.Chief != nil {
		stopSurface = input.Chief.StopSurface
	}

	opts := cam.Options{
		Surfaces:       input.Cam.Surfaces,
		ImagePlane:     imagePlane,
		ImagePlaneZ:    imagePlaneZ,
		HoldImagePlane: hold,
		StopSurface:    stopSurface,
		Wavelength:     wavelength,
		MaxIterations:  maxIterations,
		Tolerance:      tol,
		MotionWeight:   input.Cam.MotionWeight,
	}
	if *verbose {
		opts.Logf = func(format string, args ...any) { errOut(format, args...) }
	}

	solveConfigs := make([]cam.Config, 0, len(sel))
	for _, i := range sel {
		solveConfigs = append(solveConfigs, cam.Config{
			ID:        input.Configs[i].ID,
			Surfaces:  input.Configs[i].Surfaces,
			TargetEFL: targets[input.Configs[i].ID],
		})
	}

	res, err := cam.Solve(solveConfigs, gc, opts)
	if err != nil {
		errOut("Error: %v", err)
		os.Exit(1)
	}

	// Write the solved airspaces back and report them.
	byID := make(map[string]int, len(input.Configs))
	for i := range input.Configs {
		byID[input.Configs[i].ID] = i
	}
	result := &types.CamResult{
		ImagePlaneZ: imagePlaneZ,
		Wavelength:  wavelength,
		Converged:   res.Converged,
	}
	for _, cr := range res.Configs {
		input.Configs[byID[cr.ID]].Surfaces = cr.Surfaces
		entry := types.CamConfigResult{
			Config:             cr.ID,
			EFLTarget:          cr.EFLTarget,
			EFL:                cr.EFL,
			ImagePlaneZ:        cr.ImagePlaneZ,
			FocusZ:             cr.FocusZ,
			ResidualEFL:        cr.ResidualEFL,
			ResidualFocus:      cr.ResidualFocus,
			ResidualImagePlane: cr.ResidualImagePlane,
			Iterations:         cr.Iterations,
			Converged:          cr.Converged,
		}
		for _, a := range cr.Airspaces {
			entry.Airspaces = append(entry.Airspaces, types.CamAirspace{
				Surface: a.Surface,
				From:    a.From,
				To:      a.To,
				Delta:   a.Delta,
			})
		}
		result.Configs = append(result.Configs, entry)
	}
	// The cam curve as a table: each variable's solved value across the
	// positions, in config order (no fit — the values are the curve).
	if len(res.Configs) >= 2 {
		for k, id := range input.Cam.Surfaces {
			stroke := types.CamStroke{Surface: id}
			for _, cr := range res.Configs {
				stroke.Values = append(stroke.Values, types.CamStrokeValue{
					Config:    cr.ID,
					Thickness: cr.Airspaces[k].To,
				})
			}
			result.Strokes = append(result.Strokes, stroke)
		}
	}

	fmt.Fprintf(os.Stderr, "=== Cam solve: %d position(s) ===\n", len(res.Configs))
	if hold {
		fmt.Fprintf(os.Stderr, "  image plane Z %.6f mm (surface %d), wavelength %.8f mm\n", imagePlaneZ, imagePlane, wavelength)
	} else {
		fmt.Fprintf(os.Stderr, "  image plane free, wavelength %.8f mm\n", wavelength)
	}
	for _, cr := range res.Configs {
		note := ""
		if !cr.Converged {
			note = "  NOT CONVERGED"
		}
		fmt.Fprintf(os.Stderr, "  %-12s EFL %10.4f (target %10.4f)  focus %+9.2e  plane %+9.2e  %2d iters%s\n",
			cr.ID, cr.EFL, cr.EFLTarget, cr.ResidualFocus, cr.ResidualImagePlane, cr.Iterations, note)
		if cr.Converged {
			continue
		}
		errOut("Warning: cam did not converge for %s (EFL off %.3g mm, focus off %.3g mm, image plane off %.3g mm)",
			cr.ID, cr.ResidualEFL, cr.ResidualFocus, cr.ResidualImagePlane)
		if cr.HitFloor {
			where := "with a fixed image plane here"
			if !hold {
				where = "from this layout here"
			}
			errOut("Warning: %s: a cam airspace is at its lower bound — the barrels cannot reach this EFL %s", cr.ID, where)
		}
	}

	output := types.Output{Input: input, CamResult: result}
	withOutputMetadata(&output.Input, "cam", subcmdArgs())
	writeYAML(&output)
}

// camSurfaceZ returns the physical vertex Z of the surface with the given ID.
func camSurfaceZ(surfaces []types.Surface, id int) (float64, bool) {
	for i := range surfaces {
		if surfaces[i].ID == id {
			return surfaces[i].PhysicalZ, true
		}
	}
	return 0, false
}
