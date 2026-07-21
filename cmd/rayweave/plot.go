package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/hiroki/rayweaver/internal/render"
	"github.com/hiroki/rayweaver/internal/types"
	"gopkg.in/yaml.v3"
)

func runPlot(data []byte) {
	var output types.Output
	if err := yaml.Unmarshal(data, &output); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing YAML: %v\n", err)
		os.Exit(1)
	}

	var outPath string
	var lensWidth, rayWidth, scaleOverride, rightMarginPct float64
	args := os.Args[2:] // skip "plot"
	fs := flag.NewFlagSet("plot", flag.ExitOnError)
	fs.StringVar(&outPath, "o", "", "output SVG file path (default: stdout)")
	fs.Float64Var(&lensWidth, "lens-width", 0.1, "lens body stroke width (default 0.1)")
	fs.Float64Var(&rayWidth, "ray-width", 0.1, "ray path stroke width (default 0.1)")
	fs.Float64Var(&scaleOverride, "scale", 0, "SVG scale factor (0 = auto)")
	fs.Float64Var(&rightMarginPct, "right-margin", 20, "right-side margin beyond image plane (% of lens length, default 20)")
	fs.Parse(args)

	glassMap := buildGlassMap(output)

	cfg := render.Config{
		Surfaces:       output.System.Surfaces,
		Results:        output.Results,
		ChiefRays:      output.ChiefRays,
		GlassMap:       glassMap,
		LensWidth:      lensWidth,
		RayWidth:       rayWidth,
		ScaleOverride:  scaleOverride,
		RightMarginPct: rightMarginPct,
	}

	svg := render.LensSVG(cfg)

	if outPath != "" {
		if err := os.WriteFile(outPath, []byte(svg), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing SVG: %v\n", err)
			os.Exit(1)
		}
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
