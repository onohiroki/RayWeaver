package render

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/hiroki/rayweaver/internal/raymath"
	"github.com/hiroki/rayweaver/internal/types"
)

type vec2 struct{ X, Y float64 }

func sagFuncForSurface(surf types.Surface) func(float64) float64 {
	switch surf.Type {
	case types.Sphere:
		return func(h float64) float64 {
			return raymath.PolynomialAsphereSag(h, surf.Radius(), 0, nil)
		}
	case types.AspherePolynomial:
		return func(h float64) float64 {
			return raymath.PolynomialAsphereSag(h, surf.Radius(), surf.Conic, surf.Coefficients)
		}
	case types.AsphereZernike:
		return func(h float64) float64 {
			return raymath.ZernikeAsphereSag(h, surf.Radius(), surf.Conic, surf.Coefficients, surf.NormRadius)
		}
	default:
		return func(float64) float64 { return 0 }
	}
}

func surfaceDownPath(surf types.Surface, h, zOffset float64) string {
	sag := sagFuncForSurface(surf)(h)
	if h <= 0 {
		return fmt.Sprintf("M %.6f,0", zOffset+sag)
	}
	switch surf.Type {
	case types.Sphere:
		if surf.Radius() == 0 {
			return fmt.Sprintf("M %.6f,%.6f L %.6f,%.6f",
				zOffset+sag, h, zOffset+sag, -h)
		}
		r := math.Abs(surf.Radius())
		sweep := "1"
		if surf.Radius() < 0 {
			sweep = "0"
		}
		return fmt.Sprintf("M %.6f,%.6f a %s,%s 0 0 %s 0,-%.6f",
			zOffset+sag, h, f64str(r), f64str(r), sweep, 2*h)
	default:
		return bezierDownAt(surf, h, zOffset)
	}
}

func surfaceUpPath(surf types.Surface, h, zOffset float64) string {
	sag := sagFuncForSurface(surf)(h)
	if h <= 0 {
		return fmt.Sprintf("L %.6f,0", zOffset+sag)
	}
	switch surf.Type {
	case types.Sphere:
		if surf.Radius() == 0 {
			return fmt.Sprintf("L %.6f,%.6f L %.6f,%.6f",
				zOffset+sag, -h, zOffset+sag, h)
		}
		r := math.Abs(surf.Radius())
		sweep := "0"
		if surf.Radius() < 0 {
			sweep = "1"
		}
		return fmt.Sprintf("a %s,%s 0 0 %s 0,%.6f",
			f64str(r), f64str(r), sweep, 2*h)
	default:
		return bezierUpAt(surf, h, zOffset)
	}
}

func f64str(v float64) string {
	return strconv.FormatFloat(v, 'f', 6, 64)
}

func evalSagPoints(surf types.Surface, hMax float64) []vec2 {
	fn := sagFuncForSurface(surf)
	n := 20
	pts := make([]vec2, n+1)
	for i := 0; i <= n; i++ {
		h := hMax * float64(i) / float64(n)
		pts[i] = vec2{X: fn(h), Y: h}
	}
	return pts
}

func evalSagPointsOffset(surf types.Surface, hMax, zOffset float64) []vec2 {
	pts := evalSagPoints(surf, hMax)
	for i := range pts {
		pts[i].X += zOffset
	}
	return pts
}

func bezierDownAt(surf types.Surface, h, zOffset float64) string {
	if h <= 0 {
		sag := sagFuncForSurface(surf)(0)
		return fmt.Sprintf("M %.6f,0", zOffset+sag)
	}
	pts := evalSagPointsOffset(surf, h, zOffset)
	return catmullRomDown(pts)
}

func bezierUpAt(surf types.Surface, h, zOffset float64) string {
	if h <= 0 {
		sag := sagFuncForSurface(surf)(0)
		return fmt.Sprintf("L %.6f,0", zOffset+sag)
	}
	pts := evalSagPointsOffset(surf, h, zOffset)
	return catmullRomUp(pts)
}

func catmullRomDown(pts []vec2) string {
	return catmullRomRange(pts, -1, "M")
}

func catmullRomUp(pts []vec2) string {
	return catmullRomRange(pts, 1, "L")
}

func catmullRomRange(pts []vec2, dir int, firstCmd string) string {
	if len(pts) < 2 {
		return ""
	}
	n := len(pts) - 1
	var all []vec2

	if dir < 0 {
		for i := n; i >= 0; i-- {
			all = append(all, pts[i])
		}
		for i := 1; i <= n; i++ {
			all = append(all, vec2{X: pts[i].X, Y: -pts[i].Y})
		}
	} else {
		for i := n; i >= 0; i-- {
			all = append(all, vec2{X: pts[i].X, Y: -pts[i].Y})
		}
		for i := 1; i <= n; i++ {
			all = append(all, pts[i])
		}
	}

	return catmullRomCurve(all, firstCmd)
}

func catmullRomCurve(pts []vec2, firstCmd string) string {
	if len(pts) < 2 {
		return ""
	}
	var b strings.Builder
	b.WriteString(firstCmd)
	b.WriteByte(' ')
	b.WriteString(f64str(pts[0].X))
	b.WriteByte(',')
	b.WriteString(f64str(pts[0].Y))

	for i := 0; i < len(pts)-1; i++ {
		var p0, p1, p2, p3 vec2
		p1 = pts[i]
		p2 = pts[i+1]
		if i == 0 {
			p0 = p1
		} else {
			p0 = pts[i-1]
		}
		if i+2 >= len(pts) {
			p3 = p2
		} else {
			p3 = pts[i+2]
		}

		c1 := vec2{X: p1.X + (p2.X-p0.X)/6, Y: p1.Y + (p2.Y-p0.Y)/6}
		c2 := vec2{X: p2.X - (p3.X-p1.X)/6, Y: p2.Y - (p3.Y-p1.Y)/6}

		b.WriteString(" C ")
		b.WriteString(f64str(c1.X))
		b.WriteByte(',')
		b.WriteString(f64str(c1.Y))
		b.WriteByte(' ')
		b.WriteString(f64str(c2.X))
		b.WriteByte(',')
		b.WriteString(f64str(c2.Y))
		b.WriteByte(' ')
		b.WriteString(f64str(p2.X))
		b.WriteByte(',')
		b.WriteString(f64str(p2.Y))
	}
	return b.String()
}

func effectiveSemiDiameter(surf types.Surface, nextSurf *types.Surface, globalMaxH float64) float64 {
	h := globalMaxH
	if surf.Diameter > 0 {
		h = surf.Diameter / 2
	}
	if nextSurf != nil && nextSurf.Diameter > 0 {
		nh := nextSurf.Diameter / 2
		if nh > h {
			h = nh
		}
	}
	if h <= 0 {
		h = 1.0
	}
	return h
}
