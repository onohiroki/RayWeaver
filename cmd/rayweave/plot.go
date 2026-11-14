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

// defaultMultiConfigLimit is how many configs `plot --multi-config` draws when
// no config selection is given. An explicit --configs list is drawn as given.
const defaultMultiConfigLimit = 5

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
  --config ID                  select a config by id (single-config mode)
  --multi-config               draw several configs stacked vertically
  --configs "id1,id2,..."      configs to draw, by id or 0-based index
                               (comma-separated; implies --multi-config)
  --lens-width 1.5             lens body stroke width in pixels
  --ray-width 1.5              ray path stroke width in pixels
  --scale 0                    SVG/PNG scale factor (0 = auto; shared by every
                               config in multi-config mode)
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

Single config: use --config to select which config to draw:
  cat result.yaml | rayweave plot --config wide -o wide.svg
  cat result.yaml | rayweave plot --config tele -o tele.png

Multi-config: --multi-config draws the first 5 active configs (document order,
falling back to every config when none is active) as stacked cross-sections;
--configs selects them explicitly:
  cat result.yaml | rayweave plot --multi-config -o zoom.svg
  cat result.yaml | rayweave plot --configs wide,mid,tele -o zoom.png

  Every config shares one scale, the image planes are aligned on one vertical
  line (chief.reference_surface, else the last surface), and the panels are
  stacked so they never overlap. results[] / chief_rays[] hold one config's
  rays only, so multi-config mode draws the lens cross-sections without rays;
  use --config ID for a ray-overlaid single plot.

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

	var configList []string
	if opts.Set("configs") {
		configList = splitConfigList(opts.String("configs"))
		if len(configList) == 0 {
			errExitf("Error: --configs needs at least one config id")
		}
	}

	indices, multiMode, warnings, err := plotTargets(
		output.Configs, opts.String("config"), opts.Bool("multi-config"), configList, defaultMultiConfigLimit)
	if err != nil {
		errExitf("Error: %v", err)
	}
	for _, w := range warnings {
		errOut("Warning: %s", w)
	}
	if multiMode && (len(output.Results) > 0 || len(output.ChiefRays) > 0) {
		errOut("Warning: multi-config plot draws lens cross-sections only; results[]/chief_rays[] are not drawn (use --config ID for a ray-overlaid single plot)")
	}

	invalidRays := render.InvalidRayHide
	switch {
	case clipInvalidRays:
		invalidRays = render.InvalidRayClip
	case showInvalidRays:
		invalidRays = render.InvalidRayShow
	}

	stopSurfaceID := 0
	if output.Chief != nil {
		stopSurfaceID = output.Chief.StopSurface
	}
	refSurface := chiefRefSurface(output.Input)

	// Per-config drawings. The element/asphere checks below run over every
	// panel, because a multi-config plot can mix configs with different
	// element counts and different aspheres.
	maxElemCount := 0
	surfaceIDs := make(map[int]bool)
	asphereIDs := make(map[int]bool)
	panels := make([]render.Panel, 0, len(indices))
	for _, idx := range indices {
		surfaces := output.Configs[idx].Surfaces
		surface.Precompute(surfaces)
		if n := render.CountElements(surfaces); n > maxElemCount {
			maxElemCount = n
		}
		for _, s := range surfaces {
			surfaceIDs[s.ID] = true
			if s.Type == types.AspherePolynomial || s.Type == types.AsphereZernike {
				asphereIDs[s.ID] = true
			}
		}

		cfg := render.Config{
			Surfaces:        surfaces,
			GlassMap:        buildGlassMap(output, surfaces),
			LensWidth:       opts.Float("lens-width"),
			RayWidth:        opts.Float("ray-width"),
			ScaleOverride:   opts.Float("scale"),
			RightMarginPct:  opts.Float("right-margin"),
			MaxFanRays:      opts.Int("fan-rays"),
			StopSurfaceID:   stopSurfaceID,
			ElementColors:   elementColors,
			AsphereColors:   asphereColors,
			AsphereColorAll: asphereColorAll,
			InvalidRays:     invalidRays,
		}
		if !multiMode {
			// results[] and chief_rays[] belong to one config only, so they are
			// drawn in single-config mode alone (see the warning above).
			cfg.Results = output.Results
			cfg.ChiefRays = output.ChiefRays
		}
		panels = append(panels, render.Panel{
			Config:      cfg,
			ImagePlaneZ: referenceSurfaceZ(surfaces, refSurface),
		})
	}

	// ── Warning: out-of-range element colors ──────────────────────
	for idx := range elementColors {
		if idx < 1 || idx > maxElemCount {
			errOut("Warning: --element-color %d=... ignored: no element %d (elements are numbered 1..%d)", idx, idx, maxElemCount)
		}
	}

	// ── Warning: asphere-color surface checks ─────────────────────
	if len(asphereColors) > 0 || asphereColorAll != "" {
		// Check each specified surface ID: it must exist somewhere and be
		// aspheric there, otherwise the colour would never be used.
		for surfID := range asphereColors {
			if surfaceIDs[surfID] && !asphereIDs[surfID] {
				errOut("Warning: --asphere-color %d=... ignored: surface %d is not an aspheric surface in this config", surfID, surfID)
			}
		}
		if asphereColorAll != "" && len(asphereIDs) == 0 {
			errOut("Warning: --asphere-color %s ignored: this config has no aspheric surfaces", asphereColorAll)
		}
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
		var pngData []byte
		if multiMode {
			pngData, err = render.MultiLensPNG(panels)
		} else {
			pngData, err = render.LensPNG(panels[0].Config)
		}
		if err != nil {
			errExitf("Error rendering PNG: %v", err)
		}
		if err := os.WriteFile(opts.String("output"), pngData, 0644); err != nil {
			errExitf("Error writing PNG: %v", err)
		}
		os.Stdout.Write(outYAML)
		return
	}

	svg := ""
	if multiMode {
		svg = render.MultiLensSVG(panels)
	} else {
		svg = render.LensSVG(panels[0].Config)
	}

	if opts.String("output") != "" {
		if err := os.WriteFile(opts.String("output"), []byte(svg), 0644); err != nil {
			errExitf("Error writing SVG: %v", err)
		}
		os.Stdout.Write(outYAML)
	} else {
		os.Stdout.Write([]byte(svg))
	}
}

// plotTargets resolves which configs a plot draws and whether multi-config mode
// is on. It returns the config indexes in drawing order, the mode, and the
// non-fatal warnings (configs without surfaces, truncation to the default
// limit). Single-config mode always returns exactly one index.
func plotTargets(configs []types.Config, configFlag string, multi bool, list []string, limit int) ([]int, bool, []string, error) {
	multiMode := multi || len(list) > 0
	if configFlag != "" && multiMode {
		return nil, false, nil, fmt.Errorf("--config and --multi-config/--configs are mutually exclusive")
	}
	if limit <= 0 {
		limit = defaultMultiConfigLimit
	}

	if !multiMode {
		idx := 0
		if configFlag != "" {
			i, msg := resolveConfig(configs, configFlag)
			if i < 0 {
				return nil, false, nil, fmt.Errorf("%s", msg)
			}
			idx = i
		}
		if len(configs[idx].Surfaces) == 0 {
			return nil, false, nil, fmt.Errorf("no surfaces to plot (define configs[].surfaces)")
		}
		return []int{idx}, false, nil, nil
	}

	candidates := make([]int, 0, len(configs))
	if len(list) > 0 {
		seen := make(map[int]bool, len(list))
		for _, token := range list {
			i, msg := resolveConfig(configs, token)
			if i < 0 {
				return nil, true, nil, fmt.Errorf("%s", msg)
			}
			if seen[i] {
				continue
			}
			seen[i] = true
			candidates = append(candidates, i)
		}
	} else {
		// Document order, active configs first; a document that never sets
		// `active` still plots every config.
		for i := range configs {
			if configs[i].Active {
				candidates = append(candidates, i)
			}
		}
		if len(candidates) == 0 {
			for i := range configs {
				candidates = append(candidates, i)
			}
		}
	}

	var warnings []string
	indices := make([]int, 0, len(candidates))
	for _, i := range candidates {
		if len(configs[i].Surfaces) == 0 {
			warnings = append(warnings, fmt.Sprintf("config %q has no surfaces — skipped", plotConfigLabel(configs, i)))
			continue
		}
		indices = append(indices, i)
	}
	if len(list) == 0 && len(indices) > limit {
		warnings = append(warnings, fmt.Sprintf("%d configs to draw; drawing the first %d (use --configs to choose)", len(indices), limit))
		indices = indices[:limit]
	}
	if len(indices) == 0 {
		return nil, true, warnings, fmt.Errorf("no config to draw")
	}
	return indices, true, warnings, nil
}

// splitConfigList parses the --configs value: comma-separated config ids or
// 0-based indexes, surrounding whitespace ignored.
func splitConfigList(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// plotConfigLabel names a config in warnings, falling back to its document
// position when the config declares no id.
func plotConfigLabel(configs []types.Config, idx int) string {
	if idx >= 0 && idx < len(configs) && configs[idx].ID != "" {
		return configs[idx].ID
	}
	return fmt.Sprintf("#%d", idx)
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
