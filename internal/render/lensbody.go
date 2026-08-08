package render

import (
	"fmt"
	"math"
	"strings"

	"github.com/hiroki/rayweaver/internal/types"
)

type element struct {
	r1Idx  int
	r2Idx  int
	r1Surf types.Surface
	r2Surf types.Surface
	h1     float64
	h2     float64
}

func buildLensBodies(surfaces []types.Surface, zPos []float64, globalMaxH float64) []string {
	elems := findElements(surfaces, globalMaxH)
	var out []string
	for _, e := range elems {
		p := buildElemPath(e, zPos[e.r1Idx], zPos[e.r2Idx])
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func computeZPositions(surfaces []types.Surface) []float64 {
	precomputed := false
	for _, s := range surfaces {
		if s.PhysicalZ != 0 {
			precomputed = true
			break
		}
	}
	if precomputed {
		z := make([]float64, len(surfaces))
		for i, s := range surfaces {
			z[i] = s.PhysicalZ
		}
		return z
	}
	z := make([]float64, len(surfaces))
	var acc float64
	for i := 0; i < len(surfaces); i++ {
		z[i] = acc
		acc += surfaces[i].Thickness
	}
	return z
}

func globalMaxSemiDiameter(surfaces []types.Surface) float64 {
	maxH := 1.0
	for _, s := range surfaces {
		if s.Diameter > 0 {
			h := s.Diameter / 2
			if h > maxH {
				maxH = h
			}
		}
	}
	return maxH
}

func findElements(surfaces []types.Surface, globalMaxH float64) []element {
	var elems []element
	for i := 0; i < len(surfaces); {
		if surfaces[i].Material.IsAir() || surfaces[i].Material.Key == "1" {
			i++
			continue
		}
		r1 := i
		r2 := i + 1
		if r2 >= len(surfaces) {
			break
		}
		elems = append(elems, element{
			r1Idx:  r1,
			r2Idx:  r2,
			r1Surf: surfaces[r1],
			r2Surf: surfaces[r2],
			h1:     surfaceSemiDiameter(surfaces[r1], globalMaxH),
			h2:     surfaceSemiDiameter(surfaces[r2], globalMaxH),
		})
		i = r2
	}
	return elems
}

func buildElemPath(e element, z1, z2 float64) string {
	ee := computeElemEdges(e, z1, z2)

	var b strings.Builder

	b.WriteString(surfaceDownPath(e.r1Surf, ee.h1eff, z1))

	b.WriteByte(' ')
	b.WriteString(edgeToPath(ee.bottomPts))

	b.WriteByte(' ')
	b.WriteString(surfaceUpPath(e.r2Surf, ee.h2eff, z2))

	b.WriteByte(' ')
	b.WriteString(edgeToPath(ee.topPts))

	b.WriteString(" Z")
	return b.String()
}

// elemEdge holds the per-surface effective heights, sag values and the
// top/bottom edge polylines of a lens element. topPts runs from the surface-2
// edge to the surface-1 edge; bottomPts runs from the surface-1 edge to the
// surface-2 edge. Both include their endpoints (each polylines' first point
// coincides with where the adjacent curved surface ends).
type elemEdge struct {
	h1eff    float64
	h2eff    float64
	sag1h    float64
	sag1mh   float64
	sag2h    float64
	sag2mh   float64
	topPts    []vec2
	bottomPts []vec2
}

// validAtHeight reports whether the surface's sag is well-defined at height h.
// For non-plane surfaces h must not exceed the radius of curvature (sag(h) is
// NaN beyond it); planes are valid at any height.
func validAtHeight(surf types.Surface, h float64) bool {
	r := math.Abs(surf.Radius())
	return r == 0 || h <= r
}

// computeElemEdges evaluates each surface and builds the top and bottom rim
// geometry. The lens is classified by the sign of the two curvatures:
//
//   - Concave (meniscus) lens — both curvatures non-zero and same sign: the
//     taller surface (larger h) carries a horizontal chamfer of half the Z
//     separation between the two surface edges (parallel to the optical axis);
//     the shorter surface connects to the chamfer end diagonally. Equal-height
//     elements keep a plain horizontal rim. In an extreme meniscus (e.g.
//     fisheye) the taller surface's edge lies at or beyond the shorter
//     surface's edge in Z (towards the image); a forward-pointing chamfer is
//     then impossible and the two edges are joined by a straight diagonal.
//   - Convex lens — opposite signs (or a plane): both surfaces are drawn at
//     the taller surface's height so the rim is a plain horizontal line, the
//     classic drawing style. When that shared height would exceed a surface's
//     radius of curvature (the sag becomes undefined), it falls back to the
//     per-surface heights joined by a straight diagonal.
func computeElemEdges(e element, z1, z2 float64) elemEdge {
	c1 := e.r1Surf.Curvature
	c2 := e.r2Surf.Curvature
	meniscus := c1 != 0 && c2 != 0 && (c1 > 0) == (c2 > 0)

	h1eff, h2eff := e.h1, e.h2
	if !meniscus {
		hShared := math.Max(e.h1, e.h2)
		if validAtHeight(e.r1Surf, hShared) && validAtHeight(e.r2Surf, hShared) {
			h1eff, h2eff = hShared, hShared
		}
	}

	ee := elemEdge{
		h1eff:    h1eff,
		h2eff:    h2eff,
		sag1h:    globalSag(e.r1Surf, h1eff),
		sag1mh:   globalSag(e.r1Surf, -h1eff),
		sag2h:    globalSag(e.r2Surf, h2eff),
		sag2mh:   globalSag(e.r2Surf, -h2eff),
	}

	z1Top := z1 + ee.sag1h
	z2Top := z2 + ee.sag2h
	p1Top := vec2{X: z1Top, Y: h1eff}
	p2Top := vec2{X: z2Top, Y: h2eff}
	p1Bot := vec2{X: z1 + ee.sag1mh, Y: -h1eff}
	p2Bot := vec2{X: z2 + ee.sag2mh, Y: -h2eff}

	if h1eff == h2eff {
		ee.topPts = []vec2{p2Top, p1Top}
		ee.bottomPts = []vec2{p1Bot, p2Bot}
		return ee
	}

	// Convex lens with unequal effective heights (the shared height broke):
	// join the two edges directly with a diagonal.
	if !meniscus {
		ee.topPts = []vec2{p2Top, p1Top}
		ee.bottomPts = []vec2{p1Bot, p2Bot}
		return ee
	}

	// Meniscus with unequal heights: chamfer on the taller surface, diagonal to
	// the shorter surface.
	var hTall, zTall, zShort float64
	if h1eff > h2eff {
		hTall = h1eff
		zTall = z1Top
		zShort = z2Top
	} else {
		hTall = h2eff
		zTall = z2Top
		zShort = z1Top
	}

	// Extreme meniscus (e.g. fisheye): the taller surface's edge lies at or
	// beyond the shorter surface's edge in Z (towards the image), so a
	// forward-pointing chamfer is not possible — join the edges directly.
	if zTall >= zShort {
		ee.topPts = []vec2{p2Top, p1Top}
		ee.bottomPts = []vec2{p1Bot, p2Bot}
		return ee
	}

	chZ := (zTall + zShort) / 2
	ee.topPts = []vec2{p2Top, vec2{X: chZ, Y: hTall}, p1Top}
	ee.bottomPts = []vec2{p1Bot, vec2{X: chZ, Y: -hTall}, p2Bot}
	return ee
}

// edgeToPath appends the polyline as absolute L commands (the caller's current
// point is already the first point of the polyline).
func edgeToPath(pts []vec2) string {
	var b strings.Builder
	for _, p := range pts {
		b.WriteString(fmt.Sprintf("L %.6f,%.6f", p.X, p.Y))
		b.WriteByte(' ')
	}
	return b.String()
}

func buildAirLines(surfaces []types.Surface, zPos []float64) []string {
	var out []string
	for i := 0; i < len(surfaces); i++ {
		if surfaces[i].Diameter <= 0 {
			continue
		}
		mat := surfaces[i].Material
		if mat.IsAir() || mat.Key == "1" {
			h := surfaces[i].Diameter / 2
			x := zPos[i] + globalSag(surfaces[i], h)
			out = append(out, fmt.Sprintf("M %.6f,%.6f L %.6f,%.6f", x, h, x, -h))
		}
	}
	return out
}

func buildStopLines(surfaces []types.Surface, zPos []float64) []string {
	var out []string
	for i := 0; i < len(surfaces); i++ {
		if surfaces[i].Diameter <= 0 {
			continue
		}
		z := zPos[i]
		sag := globalSag(surfaces[i], 0)
		x := z + sag
		h := surfaces[i].Diameter / 2
		tick := h * 0.2
		if tick < 0.5 {
			tick = 0.5
		}
		out = append(out, fmt.Sprintf(`<path class="stop" d="M %.6f,%.6f L %.6f,%.6f M %.6f,%.6f L %.6f,%.6f"/>`,
			x, h, x, h+tick, x, -h, x, -h-tick))
	}
	return out
}

// buildMirrorPaths returns sag-curved paths for reflect (mirror) surfaces.
// They are drawn as a curved line spanning the mirror semi-diameter, with a
// light hatch on the reflective side so the fold is visible.
func buildMirrorPaths(surfaces []types.Surface, zPos []float64) []string {
	var out []string
	for i := 0; i < len(surfaces); i++ {
		s := surfaces[i]
		if !s.Reflects() || s.Diameter <= 0 {
			continue
		}
		h := s.Diameter / 2
		var b strings.Builder
		b.WriteString("M ")
		b.WriteString(f64str(zPos[i] + globalSag(s, h)))
		b.WriteByte(',')
		b.WriteString(f64str(h))
		n := 20
		for j := 1; j <= n; j++ {
			t := float64(j) / float64(n)
			y := h - 2*h*t
			b.WriteString(" L ")
			b.WriteString(f64str(zPos[i] + globalSag(s, y)))
			b.WriteByte(',')
			b.WriteString(f64str(y))
		}
		out = append(out, b.String())
	}
	return out
}
