package main

import (
	"flag"
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

	var outPath string
	var lensWidth, rayWidth, scaleOverride, rightMarginPct float64
	var fanRays int
	var showInvalidFan, clipInvalidFan bool
	var configFlag, glassDir string
	var elementColorFlags, asphereColorFlags []string
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
	fs.Func("element-color", "element fill colors: \"0=#ff0000,2=#00ff00\" (repeatable)", func(s string) error {
		elementColorFlags = append(elementColorFlags, s)
		return nil
	})
	fs.Func("asphere-color", "asphere colors: \"#ff0000\" or \"3=#ff0000,7=#0000ff\" (repeatable)", func(s string) error {
		asphereColorFlags = append(asphereColorFlags, s)
		return nil
	})
	fs.BoolVar(&showInvalidFan, "show-invalid-ray-fan", false, "draw fan rays that carry an error code in full (boolean, default false = off: they are hidden)")
	fs.BoolVar(&clipInvalidFan, "clip-invalid-ray-fan", false, "draw error-coded fan rays only up to the first erroring surface (boolean, default false = off; mutually exclusive with --show-invalid-ray-fan)")
	fs.Parse(args)

	if showInvalidFan && clipInvalidFan {
		errOut("Error: --show-invalid-ray-fan and --clip-invalid-ray-fan are mutually exclusive")
		os.Exit(1)
	}

	// Parse element colors
	elementColors := make(map[int]string)
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

	// Parse asphere colors
	var asphereColorAll string
	asphereColors := make(map[int]string)
	for _, flagVal := range asphereColorFlags {
		all, byID, err := render.ParseAsphereColorMap(flagVal)
		if err != nil {
			errOut("Error parsing --asphere-color: %v", err)
			os.Exit(1)
		}
		if all != (color.NRGBA{}) {
			asphereColorAll = render.ColorToHex(all)
		}
		for k, v := range byID {
			asphereColors[k] = render.ColorToHex(v)
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
