package render

import (
	"fmt"
	"image/color"
	"math"
	"strings"

	"github.com/hiroki/rayweaver/internal/types"
)

type GlassInfo struct {
	ND float64
	VD float64
}

type Config struct {
	Surfaces         []types.Surface
	Results          []types.RayResult
	ChiefRays        []types.ChiefRayResult
	GlassMap         map[string]GlassInfo
	LensWidth        float64
	RayWidth         float64
	ScaleOverride    float64
	RightMarginPct   float64
	MaxFanRays       int
	FanInvalid       FanInvalidMode
	StopSurfaceID    int
	ElementColors    map[int]string
	AsphereColors    map[int]string
	AsphereColorAll  string
}

const (
	canvasW = 1100.0
	canvasH = 780.0
	margin  = 60.0
)

// resolveAsphereStroke returns the configured stroke colour for an aspheric
// surface (per-surface-ID override first, then the all-aspheres fallback) or
// "" when the surface is not an asphere or no colour applies.
func (c Config) resolveAsphereStroke(s types.Surface) string {
	if s.Type != types.AspherePolynomial && s.Type != types.AsphereZernike {
		return ""
	}
	if col, ok := c.AsphereColors[s.ID]; ok {
		return col
	}
	return c.AsphereColorAll
}

// resolveAsphereStrokeNRGBA mirrors resolveAsphereStroke for the PNG
// rasterizer: it parses the resolved colour string into an NRGBA value.
func (c Config) resolveAsphereStrokeNRGBA(s types.Surface) (color.NRGBA, bool) {
	col := c.resolveAsphereStroke(s)
	if col == "" {
		return color.NRGBA{}, false
	}
	parsed, err := ParseColor(col)
	if err != nil {
		return color.NRGBA{}, false
	}
	return parsed, true
}

func LensSVG(cfg Config) string {
	zPos := computeZPositions(cfg.Surfaces)
	maxSurfZ := maxSurfaceZ(cfg.Surfaces)
	rayPaths := buildRayPaths(cfg.Results, cfg.ChiefRays, cfg.MaxFanRays, cfg.FanInvalid)

	// Compute display Z span: if object plane is far, cap the
	// object-side extent to min(lensLength, backFocalLength).
	firstZ := zPos[0]
	lastZ := zPos[len(zPos)-1]
	lensLen := lastZ - firstZ
	bfl := 0.0
	if len(cfg.Surfaces) >= 2 {
		bfl = cfg.Surfaces[len(cfg.Surfaces)-2].Thickness
	}
	if bfl <= 0 {
		bfl = lensLen
	}
	leftSpan := lensLen
	if bfl < leftSpan {
		leftSpan = bfl
	}
	candidateMinZ := firstZ - leftSpan

	minZ := 0.0
	for _, r := range cfg.Results {
		for _, sr := range r.Surfaces {
			if sr.Position.Z < minZ {
				minZ = sr.Position.Z
			}
		}
	}
	if minZ < candidateMinZ {
		minZ = candidateMinZ
	}

	rightFrac := 0.2
	if cfg.RightMarginPct > 0 {
		rightFrac = cfg.RightMarginPct / 100.0
	}
	maxZ := maxSurfZ * (1 + rightFrac)
	zSpan := maxZ - minZ
	if zSpan <= 0 {
		zSpan = maxSurfZ
	}

	maxY := computeMaxYInRange(cfg.Surfaces, cfg.Results, minZ, maxZ)
	scale := computeScale(zSpan, maxY)
	if cfg.ScaleOverride > 0 {
		scale = cfg.ScaleOverride
	}

	var b strings.Builder

	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	b.WriteString(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%.0f" height="%.0f" viewBox="0 0 %.0f %.0f">`,
		canvasW, canvasH, canvasW, canvasH))

	lw := 1.5
	if cfg.LensWidth > 0 {
		lw = cfg.LensWidth
	}
	rw := 1.5
	if cfg.RayWidth > 0 {
		rw = cfg.RayWidth
	}
	b.WriteString("<defs><style>\n")
	b.WriteString(fmt.Sprintf(".lens-body{stroke:rgb(180,180,180);stroke-width:%f;opacity:0.75}\n", lw/scale))
	b.WriteString(fmt.Sprintf(".lens-edge{fill:none;stroke-width:%f}\n", lw/scale))
	b.WriteString(fmt.Sprintf(".air-line{fill:none;stroke:rgb(130,130,130);stroke-width:%f;opacity:0.6}\n", 1.0/scale))
	b.WriteString(fmt.Sprintf(".ray{fill:none;stroke-width:%f;opacity:0.7}\n", rw/scale))
	b.WriteString(fmt.Sprintf(".axis{fill:none;stroke:rgb(160,160,160);stroke-width:%f;stroke-dasharray:%f,%f;opacity:0.5}\n", 1.0/scale, 3.0/scale, 3.0/scale))
	b.WriteString(fmt.Sprintf(".stop{fill:none;stroke:rgb(80,80,80);stroke-width:%f}\n", 2.0/scale))
	b.WriteString("</style></defs>")

	// Main group: center the full content span (minZ..maxZ) in the viewport
	midZ := (minZ + maxZ) / 2
	b.WriteString(fmt.Sprintf(`<g transform="translate(%.0f,%.0f) scale(1,-1)">`,
		canvasW/2, canvasH/2))

	// Apply uniform scale and shift midZ to origin
	b.WriteString(fmt.Sprintf(`<g transform="scale(%.6f) translate(%.6f,0)">`, scale, -midZ))

	// Optical axis
	axisLen := maxSurfZ * (1 + rightFrac)
	b.WriteString(fmt.Sprintf(`<path class="axis" d="M 0,0 L %.6f,0"/>`, axisLen))

	// Rays (rendered first so they appear behind lenses)
	for _, r := range rayPaths {
		b.WriteString(fmt.Sprintf(`<path class="ray" d="%s" stroke="%s"/>`, r.path, r.color))
	}

	// Mirror surfaces (drawn as curved lines above the rays)
	for i := 0; i < len(cfg.Surfaces); i++ {
		s := cfg.Surfaces[i]
		if !s.Reflects() || s.Diameter <= 0 {
			continue
		}
		h := s.Diameter / 2
		var path strings.Builder
		path.WriteString("M ")
		path.WriteString(f64str(zPos[i] + globalSag(s, h)))
		path.WriteByte(',')
		path.WriteString(f64str(h))
		n := 20
		for j := 1; j <= n; j++ {
			t := float64(j) / float64(n)
			y := h - 2*h*t
			path.WriteString(" L ")
			path.WriteString(f64str(zPos[i] + globalSag(s, y)))
			path.WriteByte(',')
			path.WriteString(f64str(y))
		}

		stroke := "rgb(130,130,130)"
		if col, ok := cfg.resolveAsphereStrokeNRGBA(s); ok {
			stroke = ColorToHex(col)
		}
		b.WriteString(fmt.Sprintf(`<path class="air-line" d="%s" stroke="%s"/>`, path.String(), stroke))
	}

	// Lens bodies (colored by glass type, drawn on top of rays)
	globalH := globalMaxSemiDiameter(cfg.Surfaces)
	for _, e := range findElements(cfg.Surfaces, globalH) {
		mat := e.r1Surf.Material
		var fill string
		if c, ok := cfg.ElementColors[e.Index]; ok {
			fill = c
		} else if gi, ok := cfg.GlassMap[mat.String()]; ok {
			fill = GlassSVGFill(gi.ND, gi.VD)
		} else {
			fill = "rgb(180,180,180)"
		}

		z1 := zPos[e.r1Idx]
		z2 := zPos[e.r2Idx]
		path := buildElemPath(e, z1, z2)
		if path != "" {
			b.WriteString(fmt.Sprintf(`<path class="lens-body" d="%s" fill="%s"/>`, path, fill))
		}

		// Aspheric surface edges are stroked on top of the body in their own
		// colour; the element fill keeps its glass / --element-color value.
		if col := cfg.resolveAsphereStroke(e.r1Surf); col != "" {
			ee := computeElemEdges(e, z1, z2)
			b.WriteString(fmt.Sprintf(`<path class="lens-edge" d="%s" stroke="%s"/>`,
				surfaceProfilePath(e.r1Surf, ee.h1eff, z1), col))
		}
		if col := cfg.resolveAsphereStroke(e.r2Surf); col != "" {
			ee := computeElemEdges(e, z1, z2)
			b.WriteString(fmt.Sprintf(`<path class="lens-edge" d="%s" stroke="%s"/>`,
				surfaceProfilePath(e.r2Surf, ee.h2eff, z2), col))
		}
	}

	// Aperture stop marker (drawn on top of the lenses)
	for _, p := range buildStopLines(cfg.Surfaces, zPos, cfg.StopSurfaceID) {
		b.WriteString(fmt.Sprintf(`<path class="stop" d="%s"/>`, p))
	}

	b.WriteString("</g></g></svg>")
	return b.String()
}

// maxSurfaceZ returns the largest physical Z reached by any surface vertex,
// using the fold-aware physical positions so folded (mirror) systems are sized
// correctly.
func maxSurfaceZ(surfaces []types.Surface) float64 {
	zPos := computeZPositions(surfaces)
	var maxZ float64
	for _, z := range zPos {
		if z > maxZ {
			maxZ = z
		}
	}
	if maxZ <= 0 {
		maxZ = 10
	}
	return maxZ
}

func computeMaxYInRange(surfaces []types.Surface, results []types.RayResult, minZ, maxZ float64) float64 {
	maxY := 1.0
	for _, s := range surfaces {
		if s.Diameter > 0 {
			h := s.Diameter / 2
			if h > maxY {
				maxY = h
			}
		}
	}
	for _, r := range results {
		for _, sr := range r.Surfaces {
			if sr.Position.Z < minZ || sr.Position.Z > maxZ {
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

func computeScale(zSpan, maxY float64) float64 {
	targetW := canvasW * 0.8
	availH := canvasH - 2*margin
	scaleX := targetW / zSpan
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
