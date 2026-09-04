package render

import (
	"fmt"
	"math"
	"strings"

	"github.com/hiroki/rayweaver/internal/types"
)

type element struct {
	Index      int
	r1Idx      int
	r2Idx      int
	r1Surf     types.Surface
	r2Surf     types.Surface
	h1         float64
	h2         float64
	r1Cemented bool
	r2Cemented bool
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

// isGlassSurface reports whether the surface bounds a glass medium on its
// exit side (used to detect cemented interfaces between two glass elements).
func isGlassSurface(s types.Surface) bool {
	return !s.Material.IsAir() && s.Material.Key != "1"
}

func findElements(surfaces []types.Surface, globalMaxH float64) []element {
	var elems []element
	elemIdx := 1
	for i := 0; i < len(surfaces); {
		if !isGlassSurface(surfaces[i]) {
			i++
			continue
		}
		r1 := i
		r2 := i + 1
		if r2 >= len(surfaces) {
			break
		}
		elems = append(elems, element{
			Index:      elemIdx,
			r1Idx:      r1,
			r2Idx:      r2,
			r1Surf:     surfaces[r1],
			r2Surf:     surfaces[r2],
			h1:         surfaceSemiDiameter(surfaces[r1], globalMaxH),
			h2:         surfaceSemiDiameter(surfaces[r2], globalMaxH),
			r1Cemented: r1 > 0 && isGlassSurface(surfaces[r1-1]),
			r2Cemented: r2 < len(surfaces) && isGlassSurface(surfaces[r2]),
		})
		elemIdx++
		i = r2
	}
	return elems
}

// CountElements returns how many lens elements findElements detects.
func CountElements(surfaces []types.Surface) int {
	return len(findElements(surfaces, globalMaxSemiDiameter(surfaces)))
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
	h1eff     float64
	h2eff     float64
	sag1h     float64
	sag1mh    float64
	sag2h     float64
	sag2mh    float64
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
// geometry. The lens is classified from the two curvatures alone: a diverging
// (concave) lens has c1 < c2 (edge thickness greater than the center), a
// converging (convex) lens has c1 >= c2. This covers biconvex, biconcave,
// plano and meniscus shapes alike.
//
//   - Concave lens (c1 < c2) or a cemented element: the chamfer carrier (the
//     surface from which the edge chamfer extends) is the cemented surface
//     when the element is part of a cemented pair, otherwise the taller
//     surface — except for negative curvature radii where the mirror-image
//     geometry makes surface 2 the carrier.  The chamfer geometry depends on
//     the curvature sign:
//     * Positive curvature: when the carrier edge is closer to the image
//       plane (zChamfer >= zOther), the edge extends from the carrier in the
//       +Z direction by the edge thickness (minimum 1 mm), drops vertically
//       to the Y-midpoint, then connects diagonally to the other edge.
//       Otherwise the classic midpoint-Z chamfer is used.
//     * Negative curvature (c1 < 0, non-cemented): always uses the chamfer
//       approach.  The carrier is surface 2 and the chamfer extends from
//       z2Top in the −Z direction (toward z1Top) by the edge thickness.
//       Top polyline: p2Top → carrier-Y at chamferZ → Y-midpoint at
//       chamferZ → p1Top (the carrier-Y segment is horizontal, mirroring
//       the positive-curvature case).
//     Equal-height elements keep a plain horizontal rim.
//   - Convex lens (c1 >= c2) with no cemented surface: both surfaces are drawn
//     at the taller surface's height so the rim is a plain horizontal line,
//     the classic drawing style. When that shared height would exceed a
//     surface's radius of curvature (the sag becomes undefined), it falls back
//     to the per-surface heights joined by a straight diagonal.
//
// If the element's two surfaces cross (their axial spacing reaches zero at
// some height), the element is clipped at that height so nothing is drawn
// beyond the crossing.
func computeElemEdges(e element, z1, z2 float64) elemEdge {
	c1 := e.r1Surf.Curvature
	c2 := e.r2Surf.Curvature
	concave := c1 < c2

	h1eff, h2eff := e.h1, e.h2
	// Cemented surfaces are always drawn at their own semi-diameter; only a
	// convex element with two free surfaces may share the taller height.
	if !concave && !e.r1Cemented && !e.r2Cemented {
		hShared := math.Max(e.h1, e.h2)
		if validAtHeight(e.r1Surf, hShared) && validAtHeight(e.r2Surf, hShared) {
			h1eff, h2eff = hShared, hShared
		}
	}

	// Clip the element at the height where its two surfaces first cross.
	if hc, ok := surfaceCrossHeight(e, z1, z2, math.Min(h1eff, h2eff)); ok && hc > 0 {
		h1eff, h2eff = hc, hc
	}

	ee := elemEdge{
		h1eff:  h1eff,
		h2eff:  h2eff,
		sag1h:  globalSag(e.r1Surf, h1eff),
		sag1mh: globalSag(e.r1Surf, -h1eff),
		sag2h:  globalSag(e.r2Surf, h2eff),
		sag2mh: globalSag(e.r2Surf, -h2eff),
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

	// Convex lens with unequal effective heights and no cemented surface (the
	// shared height broke): join the two edges directly with a diagonal.
	if !concave && !e.r1Cemented && !e.r2Cemented {
		ee.topPts = []vec2{p2Top, p1Top}
		ee.bottomPts = []vec2{p1Bot, p2Bot}
		return ee
	}

	// Unequal heights on a concave lens or a cemented element: the chamfer
	// (horizontal, parallel to the optical axis) is placed on the cemented
	// surface when the element is part of a cemented pair, else on the taller
	// surface; the other surface connects diagonally. The chamfer point must
	// always sit at a greater height (in absolute value) than where the
	// diagonal meets the other surface; if a cemented surface would force the
	// chamfer onto the shorter side, connect with a horizontal rim at a
	// shared height instead (the taller height when both surfaces are
	// well-defined there, else the shorter one).
	chamferR1 := h1eff >= h2eff
	if e.r1Cemented != e.r2Cemented {
		chamferR1 = e.r1Cemented
	}
	// Negative curvature radii produce a mirror-image geometry: the chamfer
	// carrier is always surface 2 (R2/Z2) rather than the taller surface.
	if c1 < 0 && e.r1Cemented == e.r2Cemented {
		chamferR1 = false
	}
	var hChamfer, zChamfer, hOther, zOther float64
	if chamferR1 {
		hChamfer, zChamfer = h1eff, z1Top
		hOther, zOther = h2eff, z2Top
	} else {
		hChamfer, zChamfer = h2eff, z2Top
		hOther, zOther = h1eff, z1Top
	}

	if hChamfer < hOther {
		// Chamfer on the shorter side is invalid geometry. Connect with a
		// horizontal rim instead: draw both surfaces at one shared height,
		// preferring the taller height when both surfaces are well-defined
		// there, otherwise the shorter one (always well-defined).
		hShared := math.Min(h1eff, h2eff)
		if hMax := math.Max(h1eff, h2eff); validAtHeight(e.r1Surf, hMax) && validAtHeight(e.r2Surf, hMax) {
			hShared = hMax
		}
		ee.h1eff, ee.h2eff = hShared, hShared
		ee.sag1h = globalSag(e.r1Surf, hShared)
		ee.sag1mh = globalSag(e.r1Surf, -hShared)
		ee.sag2h = globalSag(e.r2Surf, hShared)
		ee.sag2mh = globalSag(e.r2Surf, -hShared)
		zt1, zt2 := z1+ee.sag1h, z2+ee.sag2h
		ee.topPts = []vec2{{X: zt2, Y: hShared}, {X: zt1, Y: hShared}}
		ee.bottomPts = []vec2{{X: z1 + ee.sag1mh, Y: -hShared}, {X: z2 + ee.sag2mh, Y: -hShared}}
		return ee
	}

	// Negative curvature: mirror-image geometry.  The chamfer carrier is
	// surface 2 (swapped above) and the chamfer extends from z2Top toward
	// z1Top (−Z direction) by a fixed 1 mm edge thickness (sag is not
	// considered).  Top polyline: p2Top → carrier-Y at chamferZ →
	// Y-midpoint at chamferZ → p1Top.
	if c1 < 0 && e.r1Cemented == e.r2Cemented {
		chamferZ := zChamfer - 1.0
		chamferY := (h1eff + h2eff) / 2
		ee.topPts = []vec2{p2Top, vec2{X: chamferZ, Y: hChamfer}, vec2{X: chamferZ, Y: chamferY}, p1Top}
		ee.bottomPts = []vec2{p1Bot, vec2{X: chamferZ, Y: -chamferY}, vec2{X: chamferZ, Y: -hChamfer}, p2Bot}
	} else {
		// Positive curvature or cemented element with negative curvature:
		// determine direction and use the existing logic.
		direction := 1.0
		if c1 < 0 {
			direction = -1.0
		}
		if (zChamfer-zOther)*direction >= 0 {
			chamferZ := zChamfer + direction*1.0
			chamferY := (h1eff + h2eff) / 2
			ee.topPts = []vec2{p2Top, vec2{X: chamferZ, Y: chamferY}, vec2{X: chamferZ, Y: hChamfer}, p1Top}
			ee.bottomPts = []vec2{p1Bot, vec2{X: chamferZ, Y: -hChamfer}, vec2{X: chamferZ, Y: -chamferY}, p2Bot}
		} else {
			chZ := (zChamfer + zOther) / 2
			ee.topPts = []vec2{p2Top, vec2{X: chZ, Y: hChamfer}, p1Top}
			ee.bottomPts = []vec2{p1Bot, vec2{X: chZ, Y: -hChamfer}, p2Bot}
		}
	}
	return ee
}

// surfaceCrossHeight returns the height at which the element's two surfaces
// first meet (z1+sag1(h) == z2+sag2(h)) within [0, hmax], and whether such a
// crossing exists. Beyond the crossing the lens body would be
// self-intersecting, so the element is clipped there.
func surfaceCrossHeight(e element, z1, z2, hmax float64) (float64, bool) {
	if hmax <= 0 {
		return 0, false
	}
	n := 64
	var prevH, prevSp float64
	for i := 0; i <= n; i++ {
		h := hmax * float64(i) / float64(n)
		sp := (z2 + globalSag(e.r2Surf, h)) - (z1 + globalSag(e.r1Surf, h))
		if math.IsNaN(sp) {
			return 0, false
		}
		if i == 0 {
			if sp <= 0 {
				return 0, true
			}
			prevH, prevSp = h, sp
			continue
		}
		if sp <= 0 {
			t := prevSp / (prevSp - sp)
			return prevH + t*(h-prevH), true
		}
		prevH, prevSp = h, sp
	}
	return 0, false
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

// buildStopLines returns the aperture-stop marker as SVG path commands: short
// vertical line segments above and below the stop surface's clear aperture
// (at ± half the stop diameter). It only draws a marker when a stop surface is
// defined (stopID > 0) and found among the surfaces; otherwise nothing is
// emitted. The returned paths start with "M " and are wrapped in a
// <path class="stop"> by the SVG renderer or rasterized by the PNG renderer.
func buildStopLines(surfaces []types.Surface, zPos []float64, stopID int) []string {
	if stopID <= 0 {
		return nil
	}
	var out []string
	for i := 0; i < len(surfaces); i++ {
		if surfaces[i].ID != stopID {
			continue
		}
		if surfaces[i].Diameter <= 0 {
			return nil
		}
		z := zPos[i]
		sag := globalSag(surfaces[i], 0)
		x := z + sag
		h := surfaces[i].Diameter / 2
		tick := h * 0.2
		if tick < 0.5 {
			tick = 0.5
		}
		out = append(out, fmt.Sprintf("M %.6f,%.6f L %.6f,%.6f M %.6f,%.6f L %.6f,%.6f",
			x, h, x, h+tick, x, -h, x, -h-tick))
		return out
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
