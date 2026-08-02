package main

import (
	"flag"
	"os"
	"strings"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/render"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
	"gopkg.in/yaml.v3"
)

func runPlot(data []byte) {
	var output types.Output
	if err := yaml.Unmarshal(data, &output); err != nil {
		errOut("Error parsing YAML: %v", err)
		os.Exit(1)
	}

	var outPath string
	var lensWidth, rayWidth, scaleOverride, rightMarginPct float64
	var fanRays int
	var configFlag, glassDir string
	args := os.Args[2:] // skip "plot"
	fs := flag.NewFlagSet("plot", flag.ExitOnError)
	fs.StringVar(&outPath, "o", "", "output file path (.svg or .png; default: stdout = SVG)")
	fs.StringVar(&outPath, "output", "", "alias for -o")
	fs.Float64Var(&lensWidth, "lens-width", 0.1, "lens body stroke width (default 0.1)")
	fs.Float64Var(&rayWidth, "ray-width", 0.1, "ray path stroke width (default 0.1)")
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

	glassMap := buildGlassMap(output)

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

func buildGlassMap(output types.Output) map[string]render.GlassInfo {
	m := make(map[string]render.GlassInfo)
	if output.GlassCatalog == nil {
		return m
	}
	for _, g := range output.GlassCatalog.Entries {
		if g.Name != "" {
			m[g.Name] = render.GlassInfo{ND: g.ND, VD: g.VD}
		}
		for _, alias := range g.Aliases {
			m[alias] = render.GlassInfo{ND: g.ND, VD: g.VD}
		}
	}
	return m
}
