package main

import (
	"flag"
	"fmt"
	"image/color"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/hiroki/rayweaver/internal/clispec"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/render"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

func runPlot(data []byte) {
	output := parseYAML[types.Output](data)

	// Handle --help before flag parsing (Go's flag treats --help specially)
	for _, arg := range os.Args[2:] {
		if arg == "--help" || arg == "-h" {
			fmt.Fprintf(os.Stderr, `Usage: rayweave plot [-o file.svg|.png] [flags] < input.yaml

Generates a cross-section drawing (SVG or PNG) of the lens
system with ray paths overlaid.

Options:
  -o, --output file.svg|.png   output file (default: stdout = SVG)
  --config ID                  select a config by id (multi-config mode)
  --lens-width 1.5             lens body stroke width in pixels
  --ray-width 1.5              ray path stroke width in pixels
  --scale 0                    SVG/PNG scale factor (0 = auto)
  --right-margin 20            right-side margin beyond image plane (%% of lens length)
  --fan-rays 11                max fan rays drawn per field (0 = hide fan rays)
  --show-invalid-rays          draw rays with error codes in full (boolean)
  --clip-invalid-rays          draw error-coded rays up to first error (boolean)
  --glass-dir DIR              AGF glass catalog directory
  --element-color "1=#ff0000"  element fill colors by 1-based index (repeatable;
	1 = first glass element; ignored if index out of range)
--asphere-color "#ff0000"    aspheric surface edge colors: single or per-surface-ID
	 map (repeatable; the sag curve line, not the fill;
	 surface IDs are 1-based; ignored if surface is not aspheric;
	 --asphere-color with no surface IDs sets a global color;
	 if given without aspheric surfaces warns and is ignored)

Color formats: #rrggbb, #rgb, rgb(r,g,b), rgba(r,g,b,a), CSS named colors

Invalid rays: a ray whose path records an error code on any surface
  (aperture_stop, missed_surface, total_internal_reflection, glass-path
  violations) is hidden by default, whether it is a traced result ray or a
  fan ray. --show-invalid-rays keeps the full path; --clip-invalid-rays stops
  it at the first erroring surface. The two flags are mutually exclusive, are
  enabled by giving them alone (--flag) or disabled with --flag=false. The old
  spellings --show-invalid-ray-fan / --clip-invalid-ray-fan remain aliases.

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
			os.Exit(0)
		}
	}

	args := os.Args[2:] // skip "plot"
	fs := flag.NewFlagSet("plot", flag.ExitOnError)
	opts := clispec.Bind(clispec.Plot, fs)
	if err := opts.Parse(args); err != nil {
		errExitf("Error: %v", err)
	}

	showInvalidRays := opts.Bool("show-invalid-rays")
	clipInvalidRays := opts.Bool("clip-invalid-rays")
	if showInvalidRays && clipInvalidRays {
		errExitf("Error: --show-invalid-rays and --clip-invalid-rays are mutually exclusive")
	}

	// Parse element colors with CLI-wins semantics:
	// If --element-color was given on CLI, use it (overrides YAML); otherwise
	// fall back to output.Plot.ElementColors from the input YAML.
	elementColors := make(map[int]string)
	if opts.Set("element-color") {
		for _, flagVal := range opts.Strings("element-color") {
			m, err := render.ParseColorMap(flagVal)
			if err != nil {
				errExitf("Error parsing --element-color: %v", err)
			}
			for k, v := range m {
				elementColors[k] = render.ColorToHex(v)
			}
		}
	} else {
		// Use values from input YAML
		if output.Plot != nil && len(output.Plot.ElementColors) > 0 {
			elementColors = output.Plot.ElementColors
		}
	}

	// Parse asphere colors with CLI-wins semantics:
	// If --asphere-color was given on CLI, use it (overrides YAML); otherwise
	// fall back to output.Plot.AsphereColors / AsphereColorAll from the input YAML.
	var asphereColorAll string
	asphereColors := make(map[int]string)
	if opts.Set("asphere-color") {
		var asphereColorAllTemp string
		for _, flagVal := range opts.Strings("asphere-color") {
			all, byID, err := render.ParseAsphereColorMap(flagVal)
			if err != nil {
				errExitf("Error parsing --asphere-color: %v", err)
			}
			if all != (color.NRGBA{}) {
				asphereColorAllTemp = render.ColorToHex(all)
			}
			for k, v := range byID {
				asphereColors[k] = render.ColorToHex(v)
			}
		}
		asphereColorAll = asphereColorAllTemp
	} else {
		// Use values from input YAML
		if output.Plot != nil {
			if len(output.Plot.AsphereColors) > 0 {
				asphereColors = output.Plot.AsphereColors
			}
			if output.Plot.AsphereColorAll != "" {
				asphereColorAll = output.Plot.AsphereColorAll
			}
		}
	}

	if opts.String("glass-dir") != "" {
		agfGlasses, err := glass.LoadAGFDir(opts.String("glass-dir"))
		if err != nil {
			errOut("Warning: cannot load AGF directory %s: %v", opts.String("glass-dir"), err)
		} else {
			if output.GlassCatalog == nil {
				output.GlassCatalog = &types.GlassCatalog{}
			}
			for _, g := range agfGlasses {
				if !containsGlass(output.GlassCatalog.Entries, g) {
					output.GlassCatalog.Entries = append(output.GlassCatalog.Entries, g)
				}
			}
		}
	}

	if len(output.Configs) == 0 {
		errExitf("Error: no configs to plot (define configs[].surfaces)")
	}
	surfaces := output.Configs[0].Surfaces
	if opts.String("config") != "" {
		idx, err := resolveConfig(output.Configs, opts.String("config"))
		if idx < 0 {
			errExitf("Error: %s", err)
		}
		surfaces = output.Configs[idx].Surfaces
	}
	if len(surfaces) == 0 {
		errExitf("Error: no surfaces to plot (define configs[].surfaces)")
	}

	surface.Precompute(surfaces)

	// ── Warning: out-of-range element colors ──────────────────────
	elemCount := render.CountElements(surfaces)
	for idx := range elementColors {
		if idx < 1 || idx > elemCount {
			errOut("Warning: --element-color %d=... ignored: no element %d (elements are numbered 1..%d)", idx, idx, elemCount)
		}
	}

	// ── Warning: asphere-color surface checks ─────────────────────
	hasAspherical := false
	for _, s := range surfaces {
		if s.Type == types.AspherePolynomial || s.Type == types.AsphereZernike {
			hasAspherical = true
			break
		}
	}

	if len(asphereColors) > 0 || asphereColorAll != "" {
		// Check each specified surface ID
		for surfID := range asphereColors {
			for _, s := range surfaces {
				if s.ID == surfID {
					if !(s.Type == types.AspherePolynomial || s.Type == types.AsphereZernike) {
						errOut("Warning: --asphere-color %d=... ignored: surface %d is not an aspheric surface in this config", surfID, surfID)
					}
					break
				}
			}
		}
		if asphereColorAll != "" && !hasAspherical {
			errOut("Warning: --asphere-color %s ignored: this config has no aspheric surfaces", asphereColorAll)
		}
	}

	glassMap := buildGlassMap(output, surfaces)

	stopSurfaceID := 0
	if output.Chief != nil {
		stopSurfaceID = output.Chief.StopSurface
	}

	cfg := render.Config{
		Surfaces:        surfaces,
		Results:         output.Results,
		ChiefRays:       output.ChiefRays,
		GlassMap:        glassMap,
		LensWidth:       opts.Float("lens-width"),
		RayWidth:        opts.Float("ray-width"),
		ScaleOverride:   opts.Float("scale"),
		RightMarginPct:  opts.Float("right-margin"),
		MaxFanRays:      opts.Int("fan-rays"),
		StopSurfaceID:   stopSurfaceID,
		ElementColors:   elementColors,
		AsphereColors:   asphereColors,
		AsphereColorAll: asphereColorAll,
	}
	switch {
	case clipInvalidRays:
		cfg.InvalidRays = render.InvalidRayClip
	case showInvalidRays:
		cfg.InvalidRays = render.InvalidRayShow
	default:
		cfg.InvalidRays = render.InvalidRayHide
	}

	// Write back effective values to output YAML
	if len(elementColors) > 0 || len(asphereColors) > 0 || asphereColorAll != "" {
		if output.Plot == nil {
			output.Plot = &types.PlotConfig{}
		}
		output.Plot.ElementColors = elementColors
		output.Plot.AsphereColors = asphereColors
		output.Plot.AsphereColorAll = asphereColorAll
	}

	outYAML, err := yamlMarshal(output)
	if err != nil {
		errExitf("Error marshaling output YAML: %v", err)
	}

	if opts.String("output") != "" && strings.HasSuffix(strings.ToLower(opts.String("output")), ".png") {
		pngData, err := render.LensPNG(cfg)
		if err != nil {
			errExitf("Error rendering PNG: %v", err)
		}
		if err := os.WriteFile(opts.String("output"), pngData, 0644); err != nil {
			errExitf("Error writing PNG: %v", err)
		}
		os.Stdout.Write(outYAML)
		return
	}

	svg := render.LensSVG(cfg)

	if opts.String("output") != "" {
		if err := os.WriteFile(opts.String("output"), []byte(svg), 0644); err != nil {
			errExitf("Error writing SVG: %v", err)
		}
		os.Stdout.Write(outYAML)
	} else {
		os.Stdout.Write([]byte(svg))
	}
}

// buildGlassMap resolves the materials actually used on surfaces through the
// glass catalog. Resolving via glass.Catalog.Lookup applies the same
// CODE V-style name normalization (hyphen/underscore stripping, manufacturer
// suffix) as the trace/chief/optimize subcommands, so glasses referenced by
// e.g. "LLAL12" or "LLAL12_OHARA" are colored just like their AGF spellings.
func buildGlassMap(output types.Output, surfaces []types.Surface) map[string]render.GlassInfo {
	gc := glass.NewCatalog()
	if output.GlassCatalog != nil {
		for _, g := range output.GlassCatalog.Entries {
			gc.Add(g)
		}
	}

	m := make(map[string]render.GlassInfo)
	for _, s := range surfaces {
		mat := s.Material
		if mat.IsAir() {
			continue
		}
		key := mat.String()
		if _, seen := m[key]; seen {
			continue
		}
		nd, vd := mat.ND, mat.VD
		if mat.HasKey() {
			g, ok := gc.Lookup(mat.Key)
			if !ok {
				continue
			}
			nd, vd, _ = glass.NDVD(g)
		}
		m[key] = render.GlassInfo{ND: nd, VD: vd}
	}
	return m
}

func yamlMarshal(v interface{}) ([]byte, error) {
	return yaml.Marshal(v)
}
