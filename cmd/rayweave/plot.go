package main

import (
	"flag"
	"fmt"
	"image/color"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

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
`)
			os.Exit(0)
		}
	}

	var outPath string
	var lensWidth, rayWidth, scaleOverride, rightMarginPct float64
	var fanRays int
	var showInvalidFan, clipInvalidFan bool
	var configFlag, glassDir string
	var elementColorStr, asphereColorStr string
	args := os.Args[2:] // skip "plot"
	fs := flag.NewFlagSet("plot", flag.ExitOnError)
	fs.StringVar(&outPath, "o", "", "output file path (.svg or .png; default: stdout = SVG)")
	fs.StringVar(&outPath, "output", "", "alias for -o")
	fs.Float64Var(&lensWidth, "lens-width", 1.5, "lens body stroke width in pixels (default 1.5)")
	fs.Float64Var(&rayWidth, "ray-width", 1.5, "ray path stroke width in pixels (default 1.5)")
	fs.Float64Var(&scaleOverride, "scale", 0, "SVG scale factor (0 = auto)")
	fs.Float64Var(&rightMarginPct, "right-margin", 20, "right-side margin beyond image plane (% of lens length, default 20)")
	fs.IntVar(&fanRays, "fan-rays", 11, "max fan rays drawn per field in the lens diagram (0 = hide fan rays)")
	fs.StringVar(&configFlag, "config", "", "select config by id (multi-config mode)")
	fs.StringVar(&glassDir, "glass-dir", "", "AGF glass catalog directory")
	fs.StringVar(&elementColorStr, "element-color", "", "element fill colors: \"0=#ff0000,2=#00ff00\" (repeatable)")
	fs.StringVar(&asphereColorStr, "asphere-color", "", "aspheric surface edge colors: \"#ff0000\" or \"3=#ff0000,7=#0000ff\" (repeatable)")
	fs.BoolVar(&showInvalidFan, "show-invalid-ray-fan", false, "draw fan rays that carry an error code in full (boolean, default false = off: they are hidden)")
	fs.BoolVar(&clipInvalidFan, "clip-invalid-ray-fan", false, "draw error-coded fan rays only up to the first erroring surface (boolean, default false = off; mutually exclusive with --show-invalid-ray-fan)")

	// Track whether --element-color and --asphere-color were explicitly set on CLI
	elementColorFlagSet := false
	asphereColorFlagSet := false

	// Handle repeated flags by scanning args manually
	var elementColorFlags []string
	var asphereColorFlags []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--element-color" || args[i] == "-element-color" {
			elementColorFlagSet = true
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				elementColorFlags = append(elementColorFlags, args[i+1])
				i++
			}
		} else if args[i] == "--asphere-color" || args[i] == "-asphere-color" {
			asphereColorFlagSet = true
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				asphereColorFlags = append(asphereColorFlags, args[i+1])
				i++
			}
		}
	}
	// Also include the flag-parsed values (first occurrence)
	if elementColorStr != "" {
		elementColorFlags = append([]string{elementColorStr}, elementColorFlags...)
	}
	if asphereColorStr != "" {
		asphereColorFlags = append([]string{asphereColorStr}, asphereColorFlags...)
	}

	fs.Parse(args)

	if showInvalidFan && clipInvalidFan {
		errOut("Error: --show-invalid-ray-fan and --clip-invalid-ray-fan are mutually exclusive")
		os.Exit(1)
	}

	// Parse element colors with CLI-wins semantics:
	// If --element-color was given on CLI, use it (overrides YAML); otherwise
	// fall back to output.Plot.ElementColors from the input YAML.
	elementColors := make(map[int]string)
	if elementColorFlagSet {
		for _, flagVal := range elementColorFlags {
			m, err := render.ParseColorMap(flagVal)
			if err != nil {
				errOut("Error parsing --element-color: %v", err)
				os.Exit(1)
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
	if asphereColorFlagSet {
		var asphereColorAllTemp string
		for _, flagVal := range asphereColorFlags {
			all, byID, err := render.ParseAsphereColorMap(flagVal)
			if err != nil {
				errOut("Error parsing --asphere-color: %v", err)
				os.Exit(1)
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

	if glassDir != "" {
		agfGlasses, err := glass.LoadAGFDir(glassDir)
		if err != nil {
			errOut("Warning: cannot load AGF directory %s: %v", glassDir, err)
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
		errOut("Error: no configs to plot (define configs[].surfaces)")
		os.Exit(1)
	}
	surfaces := output.Configs[0].Surfaces
	if configFlag != "" {
		idx, err := resolveConfig(output.Configs, configFlag)
		if idx < 0 {
			errOut("Error: %s", err)
			os.Exit(1)
		}
		surfaces = output.Configs[idx].Surfaces
	}
	if len(surfaces) == 0 {
		errOut("Error: no surfaces to plot (define configs[].surfaces)")
		os.Exit(1)
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

	cfg := render.Config{
		Surfaces:         surfaces,
		Results:          output.Results,
		ChiefRays:        output.ChiefRays,
		GlassMap:         glassMap,
		LensWidth:        lensWidth,
		RayWidth:         rayWidth,
		ScaleOverride:    scaleOverride,
		RightMarginPct:   rightMarginPct,
		MaxFanRays:       fanRays,
		StopSurfaceID:    output.Chief.StopSurface,
		ElementColors:    elementColors,
		AsphereColors:    asphereColors,
		AsphereColorAll:  asphereColorAll,
	}
	switch {
	case clipInvalidFan:
		cfg.FanInvalid = render.FanInvalidClip
	case showInvalidFan:
		cfg.FanInvalid = render.FanInvalidShow
	default:
		cfg.FanInvalid = render.FanInvalidHide
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
		errOut("Error marshaling output YAML: %v", err)
		os.Exit(1)
	}

	if outPath != "" && strings.HasSuffix(strings.ToLower(outPath), ".png") {
		pngData, err := render.LensPNG(cfg)
		if err != nil {
			errOut("Error rendering PNG: %v", err)
			os.Exit(1)
		}
		if err := os.WriteFile(outPath, pngData, 0644); err != nil {
			errOut("Error writing PNG: %v", err)
			os.Exit(1)
		}
		os.Stdout.Write(outYAML)
		return
	}

	svg := render.LensSVG(cfg)

	if outPath != "" {
		if err := os.WriteFile(outPath, []byte(svg), 0644); err != nil {
			errOut("Error writing SVG: %v", err)
			os.Exit(1)
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
