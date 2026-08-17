package render

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"strconv"
	"strings"

	"golang.org/x/image/vector"

	"github.com/hiroki/rayweaver/internal/types"
)

func LensPNG(cfg Config) ([]byte, error) {
	zPos := computeZPositions(cfg.Surfaces)
	maxSurfZ := maxSurfaceZ(cfg.Surfaces)
	rayPaths := buildRayPaths(cfg.Results, cfg.ChiefRays, cfg.MaxFanRays, cfg.FanInvalid)

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
	leftEdge := firstZ - leftSpan

	minZ := 0.0
	for _, r := range cfg.Results {
		for _, sr := range r.Surfaces {
			if sr.Position.Z < minZ {
				minZ = sr.Position.Z
			}
		}
	}
	if minZ < leftEdge {
		minZ = leftEdge
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

	midZ := (minZ + maxZ) / 2

	img := image.NewRGBA(image.Rect(0, 0, canvasW, canvasH))
	fillWhite(img)

	ras := vector.NewRasterizer(canvasW, canvasH)

	// Axis (rendered first = behind everything)
	// SVG renders axis as "M 0,0 L axisLen,0": world z=0 to z=axisLen.
	axisLen := maxSurfZ * (1 + rightFrac)
	ras.Reset(canvasW, canvasH)
	dashedLine(ras, 0, axisLen, 0, 1.0, scale, midZ, 3.0/scale, 3.0/scale)
	ras.Draw(img, img.Bounds(), image.NewUniform(color.NRGBA{160, 160, 160, 128}), image.Point{})

	// Rays (behind lenses)
	rayWidth := cfg.RayWidth
	if rayWidth <= 0 {
		rayWidth = 0.1
	}
	for _, rp := range rayPaths {
		col := parseRGB(rp.color, 179)
		drawRayPath(ras, img, rp.path, rayWidth, scale, midZ, col)
	}

	// Mirror surfaces (drawn as curved lines above the rays)
	mirrorCol := color.NRGBA{130, 130, 130, 153}
	for _, p := range buildMirrorPaths(cfg.Surfaces, zPos) {
		ras.Reset(canvasW, canvasH)
		drawSagPathFromSVG(ras, p, scale, midZ)
		ras.Draw(img, img.Bounds(), image.NewUniform(mirrorCol), image.Point{})
	}

	// Lens bodies (on top of rays)
	lw := cfg.LensWidth
	if lw <= 0 {
		lw = 0.1
	}
	globalH := globalMaxSemiDiameter(cfg.Surfaces)
	outlineCol := color.NRGBA{180, 180, 180, 191}
	for _, e := range findElements(cfg.Surfaces, globalH) {
		mat := e.r1Surf.Material
		var fill color.NRGBA
		if gi, ok := cfg.GlassMap[mat.String()]; ok {
			fill = glassFill(gi.ND, gi.VD, 191)
		} else {
			fill = color.NRGBA{180, 180, 180, 191}
		}
		drawElemFill(ras, img, e, zPos[e.r1Idx], zPos[e.r2Idx], scale, midZ, fill)
		drawElemOutline(ras, img, e, zPos[e.r1Idx], zPos[e.r2Idx], scale, midZ, lw, outlineCol)
	}

	// Aperture stop marker (drawn on top of the lenses)
	stopCol := color.NRGBA{80, 80, 80, 255}
	for _, p := range buildStopLines(cfg.Surfaces, zPos, cfg.StopSurfaceID) {
		ras.Reset(canvasW, canvasH)
		drawSagPathFromSVG(ras, p, scale, midZ)
		ras.Draw(img, img.Bounds(), image.NewUniform(stopCol), image.Point{})
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func worldPt(z, y, midZ, scale float64) (float32, float32) {
	px := canvasW/2 + (z-midZ)*scale
	py := canvasH/2 - y*scale
	return float32(px), float32(py)
}

func fillWhite(img *image.RGBA) {
	for y := 0; y < canvasH; y++ {
		for x := 0; x < canvasW; x++ {
			img.SetRGBA(x, y, color.RGBA{255, 255, 255, 255})
		}
	}
}

func strokeLine(ras *vector.Rasterizer, z0, y0, z1, y1, width, scale, midZ float64) {
	px0, py0 := worldPt(z0, y0, midZ, scale)
	px1, py1 := worldPt(z1, y1, midZ, scale)
	strokeLinePx(ras, px0, py0, px1, py1, width)
}

func strokeLinePx(ras *vector.Rasterizer, px0, py0, px1, py1 float32, width float64) {
	dx := px1 - px0
	dy := py1 - py0
	segLen := float32(math.Sqrt(float64(dx*dx + dy*dy)))
	if segLen < 0.5 {
		return
	}
	hw := float32(width / 2)
	if hw < 0.5 {
		hw = 0.5
	}
	nx := -dy / segLen * hw
	ny := dx / segLen * hw
	ras.MoveTo(px0+nx, py0+ny)
	ras.LineTo(px0-nx, py0-ny)
	ras.LineTo(px1-nx, py1-ny)
	ras.LineTo(px1+nx, py1+ny)
	ras.ClosePath()
}

func dashedLine(ras *vector.Rasterizer, z0, z1, y, width, scale, midZ float64, onLen, offLen float64) {
	z := z0
	for z < z1 {
		end := z + onLen
		if end > z1 {
			end = z1
		}
		strokeLine(ras, z, y, end, y, width, scale, midZ)
		z = end + offLen
	}
}

func drawRayPath(ras *vector.Rasterizer, img *image.RGBA, svgPath string, width, scale, midZ float64, c color.NRGBA) {
	s := svgPath
	if !strings.HasPrefix(s, "M ") {
		return
	}
	s = s[2:]
	var prevZ, prevY float64
	first := true
	parts := strings.Fields(s)
	for i := 0; i < len(parts); i++ {
		tok := parts[i]
		if tok == "L" {
			continue
		}
		idx := strings.IndexByte(tok, ',')
		if idx < 0 {
			continue
		}
		z := parseF64(tok[:idx])
		y := parseF64(tok[idx+1:])
		if first {
			prevZ, prevY = z, y
			first = false
			continue
		}
		ras.Reset(canvasW, canvasH)
		strokeLine(ras, prevZ, prevY, z, y, width, scale, midZ)
		ras.Draw(img, img.Bounds(), image.NewUniform(c), image.Point{})
		prevZ, prevY = z, y
	}
}

// drawSagPathFromSVG strokes a polyline path (e.g. a mirror surface) given in
// the same "M z,y L z,y ..." format used by the SVG renderer.
func drawSagPathFromSVG(ras *vector.Rasterizer, svgPath string, scale, midZ float64) {
	s := svgPath
	if !strings.HasPrefix(s, "M ") {
		return
	}
	s = s[2:]
	var prevZ, prevY float64
	first := true
	parts := strings.Fields(s)
	for i := 0; i < len(parts); i++ {
		tok := parts[i]
		if tok == "L" {
			continue
		}
		idx := strings.IndexByte(tok, ',')
		if idx < 0 {
			continue
		}
		z := parseF64(tok[:idx])
		y := parseF64(tok[idx+1:])
		if first {
			prevZ, prevY = z, y
			first = false
			continue
		}
		strokeLine(ras, prevZ, prevY, z, y, 1.0, scale, midZ)
		prevZ, prevY = z, y
	}
}

func drawElemFill(ras *vector.Rasterizer, img *image.RGBA, e element, z1, z2, scale, midZ float64, c color.NRGBA) {
	if e.h1 <= 0 || e.h2 <= 0 {
		return
	}
	ee := computeElemEdges(e, z1, z2)
	ras.Reset(canvasW, canvasH)
	px, py := worldPt(z1+ee.sag1h, ee.h1eff, midZ, scale)
	ras.MoveTo(px, py)

	sampleSagPath(ras, e.r1Surf, ee.h1eff, -ee.h1eff, z1, scale, midZ)

	for _, p := range ee.bottomPts {
		px, py := worldPt(p.X, p.Y, midZ, scale)
		ras.LineTo(px, py)
	}

	sampleSagPath(ras, e.r2Surf, -ee.h2eff, ee.h2eff, z2, scale, midZ)

	for _, p := range ee.topPts {
		px, py := worldPt(p.X, p.Y, midZ, scale)
		ras.LineTo(px, py)
	}
	ras.ClosePath()

	ras.Draw(img, img.Bounds(), image.NewUniform(c), image.Point{})
}

func drawElemOutline(ras *vector.Rasterizer, img *image.RGBA, e element, z1, z2, scale, midZ, strokeWidth float64, c color.NRGBA) {
	if e.h1 <= 0 || e.h2 <= 0 {
		return
	}
	ee := computeElemEdges(e, z1, z2)

	ras.Reset(canvasW, canvasH)
	// Left curved surface (top → bottom)
	strokeSagPath(ras, e.r1Surf, ee.h1eff, -ee.h1eff, z1, scale, midZ, strokeWidth)
	// Bottom edge
	for i := 0; i < len(ee.bottomPts)-1; i++ {
		strokeLine(ras, ee.bottomPts[i].X, ee.bottomPts[i].Y, ee.bottomPts[i+1].X, ee.bottomPts[i+1].Y, strokeWidth, scale, midZ)
	}
	// Right curved surface (bottom → top)
	strokeSagPath(ras, e.r2Surf, -ee.h2eff, ee.h2eff, z2, scale, midZ, strokeWidth)
	// Top edge
	for i := 0; i < len(ee.topPts)-1; i++ {
		strokeLine(ras, ee.topPts[i].X, ee.topPts[i].Y, ee.topPts[i+1].X, ee.topPts[i+1].Y, strokeWidth, scale, midZ)
	}
	ras.Draw(img, img.Bounds(), image.NewUniform(c), image.Point{})
}

func strokeSagPath(r *vector.Rasterizer, surf types.Surface, hStart, hEnd, zOffset, scale, midZ, width float64) {
	n := 20
	hPrev := hStart
	sPrev := globalSag(surf, hPrev)
	for i := 1; i <= n; i++ {
		t := float64(i) / float64(n)
		h := hStart + (hEnd-hStart)*t
		s := globalSag(surf, h)
		strokeLine(r, zOffset+sPrev, hPrev, zOffset+s, h, width, scale, midZ)
		hPrev, sPrev = h, s
	}
}

func sampleSagPath(r *vector.Rasterizer, surf types.Surface, hStart, hEnd, zOffset, scale, midZ float64) {
	n := 20
	for i := 1; i <= n; i++ {
		t := float64(i) / float64(n)
		h := hStart + (hEnd-hStart)*t
		s := globalSag(surf, h)
		px, py := worldPt(zOffset+s, h, midZ, scale)
		r.LineTo(px, py)
	}
}

func glassFill(nd, vd float64, alpha uint8) color.NRGBA {
	r := (2.5-nd)/(2.5-1.4)*90 + (100-vd)/(100-20)*90
	g := (2.5-nd)/(2.5-1.4)*150 + (vd-20)/(100-20)*100
	b := (2.5-nd)/(2.5-1.4)*70 + 180
	return color.NRGBA{clampU8(r), clampU8(g), clampU8(b), alpha}
}

func parseRGB(s string, alpha uint8) color.NRGBA {
	var r, g, b uint8
	n, _ := fmt.Sscanf(s, "rgb(%d,%d,%d)", &r, &g, &b)
	if n == 3 {
		return color.NRGBA{r, g, b, alpha}
	}
	return color.NRGBA{100, 100, 180, alpha}
}

func clampU8(v float64) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

func parseF64(s string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}
