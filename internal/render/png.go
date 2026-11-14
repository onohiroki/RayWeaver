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

// LensPNG rasterizes a single configuration on the fixed one-diagram canvas.
func LensPNG(cfg Config) ([]byte, error) {
	sc := buildScene(cfg)
	vp := singleViewport(sc)
	return pngPanels([]scene{sc}, []viewport{vp})
}

// MultiLensPNG rasterizes several configurations into one image: one shared
// scale, every image plane on a common canvas column, and the panels stacked
// vertically so they never overlap.
func MultiLensPNG(panels []Panel) ([]byte, error) {
	scenes, vps := multiViewports(panels)
	return pngPanels(scenes, vps)
}

func pngPanels(scenes []scene, vps []viewport) ([]byte, error) {
	if len(scenes) == 0 {
		return nil, fmt.Errorf("render: no panels")
	}
	vp := vps[0]

	img := image.NewRGBA(image.Rect(0, 0, vp.w, vp.h))
	fillWhite(img)

	ras := vector.NewRasterizer(vp.w, vp.h)
	for i := range scenes {
		drawScene(ras, img, scenes[i], vps[i])
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// drawScene rasterizes one panel's world-coordinate content through its
// viewport (axis, rays, mirrors, lens bodies, stop marker), in the same order
// the SVG renderer uses.
func drawScene(ras *vector.Rasterizer, img *image.RGBA, sc scene, vp viewport) {
	cfg := sc.cfg

	// Axis (rendered first = behind everything)
	// SVG renders axis as "M 0,0 L axisLen,0": world z=0 to z=axisLen.
	axisLen := sc.maxSurfZ * (1 + sc.rightFrac)
	ras.Reset(vp.w, vp.h)
	dashedLine(ras, 0, axisLen, 0, 1.0, vp, 3.0/vp.scale, 3.0/vp.scale)
	ras.Draw(img, img.Bounds(), image.NewUniform(color.NRGBA{160, 160, 160, 128}), image.Point{})

	// Rays (behind lenses)
	rayWidth := cfg.RayWidth
	if rayWidth <= 0 {
		rayWidth = 0.1
	}
	for _, rp := range sc.rayPaths {
		col := parseRGB(rp.color, 179)
		drawRayPath(ras, img, rp.path, rayWidth, vp, col)
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
		path.WriteString(f64str(sc.zPos[i] + globalSag(s, h)))
		path.WriteByte(',')
		path.WriteString(f64str(h))
		n := 20
		for j := 1; j <= n; j++ {
			t := float64(j) / float64(n)
			y := h - 2*h*t
			path.WriteString(" L ")
			path.WriteString(f64str(sc.zPos[i] + globalSag(s, y)))
			path.WriteByte(',')
			path.WriteString(f64str(y))
		}

		mirrorCol := color.NRGBA{130, 130, 130, 153}
		if parsed, ok := cfg.resolveAsphereStrokeNRGBA(s); ok {
			mirrorCol = parsed
			mirrorCol.A = 153
		}
		ras.Reset(vp.w, vp.h)
		drawSagPathFromSVG(ras, path.String(), vp)
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

		if c, ok := cfg.ElementColors[e.Index]; ok {
			if parsed, err := ParseColor(c); err == nil {
				fill = parsed
				fill.A = 191
			} else {
				fill = color.NRGBA{180, 180, 180, 191}
			}
		} else if gi, ok := cfg.GlassMap[mat.String()]; ok {
			fill = glassFill(gi.ND, gi.VD, 191)
		} else {
			fill = color.NRGBA{180, 180, 180, 191}
		}

		// Aspheric surface edges are stroked in their own colour; the
		// element fill keeps its glass / --element-color value.
		r1Col, ok := cfg.resolveAsphereStrokeNRGBA(e.r1Surf)
		if !ok {
			r1Col = outlineCol
		} else {
			r1Col.A = 255
		}
		r2Col, ok := cfg.resolveAsphereStrokeNRGBA(e.r2Surf)
		if !ok {
			r2Col = outlineCol
		} else {
			r2Col.A = 255
		}

		drawElemFill(ras, img, e, sc.zPos[e.r1Idx], sc.zPos[e.r2Idx], vp, fill)
		drawElemOutline(ras, img, e, sc.zPos[e.r1Idx], sc.zPos[e.r2Idx], vp, lw, outlineCol, r1Col, r2Col)
	}

	// Aperture stop marker (drawn on top of the lenses)
	stopCol := color.NRGBA{80, 80, 80, 255}
	for _, p := range buildStopLines(cfg.Surfaces, sc.zPos, cfg.StopSurfaceID) {
		ras.Reset(vp.w, vp.h)
		drawSagPathFromSVG(ras, p, vp)
		ras.Draw(img, img.Bounds(), image.NewUniform(stopCol), image.Point{})
	}
}

func fillWhite(img *image.RGBA) {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			img.SetRGBA(x, y, color.RGBA{255, 255, 255, 255})
		}
	}
}

func strokeLine(ras *vector.Rasterizer, z0, y0, z1, y1, width float64, vp viewport) {
	px0, py0 := vp.pt(z0, y0)
	px1, py1 := vp.pt(z1, y1)
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

func dashedLine(ras *vector.Rasterizer, z0, z1, y, width float64, vp viewport, onLen, offLen float64) {
	z := z0
	for z < z1 {
		end := z + onLen
		if end > z1 {
			end = z1
		}
		strokeLine(ras, z, y, end, y, width, vp)
		z = end + offLen
	}
}

func drawRayPath(ras *vector.Rasterizer, img *image.RGBA, svgPath string, width float64, vp viewport, c color.NRGBA) {
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
		ras.Reset(vp.w, vp.h)
		strokeLine(ras, prevZ, prevY, z, y, width, vp)
		ras.Draw(img, img.Bounds(), image.NewUniform(c), image.Point{})
		prevZ, prevY = z, y
	}
}

// drawSagPathFromSVG strokes a polyline path (e.g. a mirror surface) given in
// the same "M z,y L z,y ..." format used by the SVG renderer.
func drawSagPathFromSVG(ras *vector.Rasterizer, svgPath string, vp viewport) {
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
		strokeLine(ras, prevZ, prevY, z, y, 1.0, vp)
		prevZ, prevY = z, y
	}
}

func drawElemFill(ras *vector.Rasterizer, img *image.RGBA, e element, z1, z2 float64, vp viewport, c color.NRGBA) {
	if e.h1 <= 0 || e.h2 <= 0 {
		return
	}
	ee := computeElemEdges(e, z1, z2)
	ras.Reset(vp.w, vp.h)
	px, py := vp.pt(z1+ee.sag1h, ee.h1eff)
	ras.MoveTo(px, py)

	sampleSagPath(ras, e.r1Surf, ee.h1eff, -ee.h1eff, z1, vp)

	for _, p := range ee.bottomPts {
		px, py := vp.pt(p.X, p.Y)
		ras.LineTo(px, py)
	}

	sampleSagPath(ras, e.r2Surf, -ee.h2eff, ee.h2eff, z2, vp)

	for _, p := range ee.topPts {
		px, py := vp.pt(p.X, p.Y)
		ras.LineTo(px, py)
	}
	ras.ClosePath()

	ras.Draw(img, img.Bounds(), image.NewUniform(c), image.Point{})
}

// drawElemOutline strokes the element's rim. Each curved surface is drawn in
// its own colour (r1Col / r2Col — the asphere colour when that surface is an
// asphere, otherwise the shared outline colour), while the top/bottom edge
// polylines always use the outline colour.
func drawElemOutline(ras *vector.Rasterizer, img *image.RGBA, e element, z1, z2 float64, vp viewport, strokeWidth float64, c color.NRGBA, r1Col, r2Col color.NRGBA) {
	if e.h1 <= 0 || e.h2 <= 0 {
		return
	}
	ee := computeElemEdges(e, z1, z2)

	// Left curved surface (top → bottom)
	ras.Reset(vp.w, vp.h)
	strokeSagPath(ras, e.r1Surf, ee.h1eff, -ee.h1eff, z1, vp, strokeWidth)
	ras.Draw(img, img.Bounds(), image.NewUniform(r1Col), image.Point{})

	// Bottom + top edges
	ras.Reset(vp.w, vp.h)
	for i := 0; i < len(ee.bottomPts)-1; i++ {
		strokeLine(ras, ee.bottomPts[i].X, ee.bottomPts[i].Y, ee.bottomPts[i+1].X, ee.bottomPts[i+1].Y, strokeWidth, vp)
	}
	for i := 0; i < len(ee.topPts)-1; i++ {
		strokeLine(ras, ee.topPts[i].X, ee.topPts[i].Y, ee.topPts[i+1].X, ee.topPts[i+1].Y, strokeWidth, vp)
	}
	ras.Draw(img, img.Bounds(), image.NewUniform(c), image.Point{})

	// Right curved surface (bottom → top)
	ras.Reset(vp.w, vp.h)
	strokeSagPath(ras, e.r2Surf, -ee.h2eff, ee.h2eff, z2, vp, strokeWidth)
	ras.Draw(img, img.Bounds(), image.NewUniform(r2Col), image.Point{})
}

func strokeSagPath(r *vector.Rasterizer, surf types.Surface, hStart, hEnd, zOffset float64, vp viewport, width float64) {
	n := 20
	hPrev := hStart
	sPrev := globalSag(surf, hPrev)
	for i := 1; i <= n; i++ {
		t := float64(i) / float64(n)
		h := hStart + (hEnd-hStart)*t
		s := globalSag(surf, h)
		strokeLine(r, zOffset+sPrev, hPrev, zOffset+s, h, width, vp)
		hPrev, sPrev = h, s
	}
}

func sampleSagPath(r *vector.Rasterizer, surf types.Surface, hStart, hEnd, zOffset float64, vp viewport) {
	n := 20
	for i := 1; i <= n; i++ {
		t := float64(i) / float64(n)
		h := hStart + (hEnd-hStart)*t
		s := globalSag(surf, h)
		px, py := vp.pt(zOffset+s, h)
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
