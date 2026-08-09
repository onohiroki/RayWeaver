package main

import (
	"flag"
	"os"
	"strings"

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
	var configFlag, glassDir string
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
	fs.Parse(args)

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
		Surfaces:       surfaces,
		Results:        output.Results,
		ChiefRays:      output.ChiefRays,
		GlassMap:       glassMap,
		LensWidth:      lensWidth,
		RayWidth:       rayWidth,
		ScaleOverride:  scaleOverride,
		RightMarginPct: rightMarginPct,
		MaxFanRays:     fanRays,
		StopSurfaceID:  output.Chief.StopSurface,
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
		os.Stdout.Write(data)
		return
	}

	svg := render.LensSVG(cfg)

	if outPath != "" {
		if err := os.WriteFile(outPath, []byte(svg), 0644); err != nil {
			errOut("Error writing SVG: %v", err)
			os.Exit(1)
		}
		os.Stdout.Write(data)
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
