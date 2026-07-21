package render

import (
	"fmt"
	"math"
	"strings"

	"github.com/hiroki/rayweaver/internal/types"
)

type GlassInfo struct {
	ND float64
	VD float64
}

type Config struct {
	Surfaces   []types.Surface
	Results    []types.RayResult
	ChiefRays  []types.ChiefRayResult
	GlassMap   map[string]GlassInfo
	LensWidth  float64
	RayWidth   float64
}

const (
	pageWidth  = 297.0
	pageHeight = 210.0
	margin     = 15.0
)

func LensSVG(cfg Config) string {
	zPos := computeZPositions(cfg.Surfaces)
	totalZ := computeTotalZ(cfg.Surfaces)
	maxY := computeMaxY(cfg.Surfaces, cfg.Results)

	scale := computeScale(totalZ, maxY)

	// lens bodies rendered below in the loop with glass color lookup
	rayPaths := buildRayPaths(cfg.Results, cfg.ChiefRays)

	var b strings.Builder

	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	b.WriteString(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%.0fmm" height="%.0fmm" viewBox="0 0 %.0f %.0f">`,
		pageWidth, pageHeight, pageWidth, pageHeight))

	lw := 0.4
	if cfg.LensWidth > 0 {
		lw = cfg.LensWidth
	}
	rw := 0.3
	if cfg.RayWidth > 0 {
		rw = cfg.RayWidth
	}
	b.WriteString("<defs><style>\n")
	b.WriteString(fmt.Sprintf(".lens-body{stroke:rgb(180,180,180);stroke-width:%.1f;opacity:0.75}\n", lw))
	b.WriteString(".air-line{stroke:rgb(130,130,130);stroke-width:0.3;opacity:0.6}\n")
	b.WriteString(fmt.Sprintf(".ray{fill:none;stroke-width:%.1f;opacity:0.7}\n", rw))
	b.WriteString(".axis{stroke:rgb(160,160,160);stroke-width:0.3;stroke-dasharray:3,3;opacity:0.5}\n")
	b.WriteString(".stop{stroke:rgb(80,80,80);stroke-width:0.6}\n")
	b.WriteString("</style></defs>")

	// Main group: translate to center-left, scale 1,-1 for optical coordinates
	b.WriteString(fmt.Sprintf(`<g transform="translate(%.0f,%.0f) scale(1,-1)">`,
		margin, pageHeight/2))

	// Apply uniform scale of the lens system
	b.WriteString(fmt.Sprintf(`<g transform="scale(%.6f)">`, scale))

	// Optical axis
	axisLen := totalZ * 1.2
	b.WriteString(fmt.Sprintf(`<path class="axis" d="M 0,0 L %.6f,0"/>`, axisLen))

	// Lens bodies (colored by glass type)
	globalH := globalMaxSemiDiameter(cfg.Surfaces)
	for _, e := range findElements(cfg.Surfaces, globalH) {
		mat := e.r1Surf.Material
		var fill string
		if gi, ok := cfg.GlassMap[mat]; ok {
			fill = GlassSVGFill(gi.ND, gi.VD)
		} else {
			fill = "rgb(180,180,180)"
		}
		path := buildElemPath(e, zPos[e.r1Idx], zPos[e.r2Idx])
		if path != "" {
			b.WriteString(fmt.Sprintf(`<path class="lens-body" d="%s" fill="%s"/>`, path, fill))
		}
	}

	// Rays
	for _, r := range rayPaths {
		b.WriteString(fmt.Sprintf(`<path class="ray" d="%s" stroke="%s"/>`, r.path, r.color))
	}

	b.WriteString("</g></g></svg>")
	return b.String()
}

func computeTotalZ(surfaces []types.Surface) float64 {
	var acc float64
	for i := 0; i < len(surfaces)-1; i++ {
		acc += surfaces[i].Thickness
	}
	if acc <= 0 {
		acc = 10
	}
	return acc
}

func computeMaxY(surfaces []types.Surface, results []types.RayResult) float64 {
	maxY := 1.0
	for _, s := range surfaces {
		if s.Diameter > 0 {
			h := s.Diameter / 2
			if h > maxY {
				maxY = h
			}
		}
	}
	if len(surfaces) == 0 {
		return maxY
	}
	z0 := 0.0
	z1 := 0.0
	for i := 0; i < len(surfaces)-1; i++ {
		z1 += surfaces[i].Thickness
	}
	for _, r := range results {
		for _, sr := range r.Surfaces {
			z := sr.Position.Z
			if z < z0 || z > z1 {
				continue
			}
			y := math.Abs(sr.Position.Y)
			if y > maxY {
				maxY = y
			}
		}
	}
	return maxY
}

func computeScale(totalZ, maxY float64) float64 {
	availW := pageWidth - 2*margin
	availH := pageHeight - 2*margin
	scaleX := availW / totalZ
	scaleY := availH / (2 * maxY)
	s := scaleX
	if scaleY < s {
		s = scaleY
	}
	if s <= 0 {
		s = 1
	}
	return s
}


