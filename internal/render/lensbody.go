package render

import (
	"fmt"
	"strings"

	"github.com/hiroki/rayweaver/internal/types"
)

type element struct {
	r1Idx  int
	r2Idx  int
	r1Surf types.Surface
	r2Surf types.Surface
	h      float64
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
	z := make([]float64, len(surfaces))
	var acc float64
	for i := 0; i < len(surfaces); i++ {
		z[i] = acc
		if i < len(surfaces) {
			acc += surfaces[i].Thickness
		}
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
		if surfaces[i].Material == "" || surfaces[i].Material == "1" || strings.EqualFold(surfaces[i].Material, "air") {
			i++
			continue
		}
		r1 := i
		r2 := i + 1
		if r2 >= len(surfaces) {
			break
		}
		h := effectiveSemiDiameter(surfaces[r1], &surfaces[r2], globalMaxH)
		elems = append(elems, element{
			r1Idx:  r1,
			r2Idx:  r2,
			r1Surf: surfaces[r1],
			r2Surf: surfaces[r2],
			h:      h,
		})
		i = r2
	}
	return elems
}

func buildElemPath(e element, z1, z2 float64) string {
	sag2 := sagFuncForSurface(e.r2Surf)(e.h)
	x2 := z2 + sag2

	var b strings.Builder

	b.WriteString(surfaceDownPath(e.r1Surf, e.h, z1))

	b.WriteByte(' ')
	b.WriteString(fmt.Sprintf("L %.6f,%.6f", x2, -e.h))

	b.WriteByte(' ')
	b.WriteString(surfaceUpPath(e.r2Surf, e.h, z2))

	b.WriteString(" Z")
	return b.String()
}

func buildAirLines(surfaces []types.Surface, zPos []float64) []string {
	var out []string
	for i := 0; i < len(surfaces); i++ {
		if surfaces[i].Diameter <= 0 {
			continue
		}
		mat := surfaces[i].Material
		if mat == "" || mat == "1" || strings.EqualFold(mat, "air") {
			h := surfaces[i].Diameter / 2
			sag := sagFuncForSurface(surfaces[i])(h)
			x := zPos[i] + sag
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
		sag := sagFuncForSurface(surfaces[i])(0)
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
