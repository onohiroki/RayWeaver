package render

import (
	"fmt"
	"math"
	"strings"

	"github.com/hiroki/rayweaver/internal/types"
)

type rayRenderData struct {
	path       string
	color      string
	fieldAngle float64
}

type angleColorEntry struct {
	angle float64
	color string
}

func buildRayPaths(results []types.RayResult, chiefRays []types.ChiefRayResult) []rayRenderData {
	var out []rayRenderData

	colors := buildAngleColorMap(chiefRays)

	for _, r := range results {
		if len(r.Surfaces) < 2 {
			continue
		}
		path := buildRayPath(r.Surfaces)
		if path == "" {
			continue
		}
		angle := extractFieldAngle(r.ID, chiefRays)
		col := fieldAngleColor(angle, colors)
		out = append(out, rayRenderData{
			path:       path,
			color:      col,
			fieldAngle: angle,
		})
	}

	return out
}

func buildAngleColorMap(chiefRays []types.ChiefRayResult) []angleColorEntry {
	if len(chiefRays) == 0 {
		return nil
	}
	minA, maxA := chiefRays[0].FieldAngle, chiefRays[0].FieldAngle
	for _, cr := range chiefRays {
		if cr.FieldAngle < minA {
			minA = cr.FieldAngle
		}
		if cr.FieldAngle > maxA {
			maxA = cr.FieldAngle
		}
	}
	rangeA := maxA - minA
	var out []angleColorEntry
	for _, cr := range chiefRays {
		var c string
		if rangeA > 0 {
			t := (cr.FieldAngle - minA) / rangeA
			c = hslColor(t)
		} else {
			c = "rgb(100,100,200)"
		}
		out = append(out, angleColorEntry{angle: cr.FieldAngle, color: c})
	}
	return out
}

func buildRayPath(surfaces []types.SurfaceResult) string {
	if len(surfaces) < 2 {
		return ""
	}
	var b strings.Builder
	b.WriteString("M ")
	b.WriteString(f64str(surfaces[0].Position.Z))
	b.WriteByte(',')
	b.WriteString(f64str(surfaces[0].Position.Y))
	for i := 1; i < len(surfaces); i++ {
		b.WriteString(" L ")
		b.WriteString(f64str(surfaces[i].Position.Z))
		b.WriteByte(',')
		b.WriteString(f64str(surfaces[i].Position.Y))
	}
	return b.String()
}

func extractFieldAngle(id string, chiefRays []types.ChiefRayResult) float64 {
	for _, cr := range chiefRays {
		if cr.ChiefRay.ID == id || strings.HasPrefix(id, "chief_") {
			return cr.FieldAngle
		}
	}
	return 0
}

func fieldAngleColor(angle float64, colors []angleColorEntry) string {
	if len(colors) <= 1 {
		return "rgb(100,100,180)"
	}
	for i := 0; i < len(colors)-1; i++ {
		if angle >= colors[i].angle && angle <= colors[i+1].angle {
			ra := colors[i+1].angle - colors[i].angle
			if ra == 0 {
				return colors[i].color
			}
			t := (angle - colors[i].angle) / ra
			return lerpColor(colors[i].color, colors[i+1].color, t)
		}
	}
	if angle <= colors[0].angle {
		return colors[0].color
	}
	return colors[len(colors)-1].color
}

func hslColor(t float64) string {
	r := uint8(math.Round(t * 200))
	g := uint8(math.Round((1-math.Abs(t-0.5)*2)*80 + 80))
	b := uint8(math.Round((1 - t) * 200))
	return fmt.Sprintf("rgb(%d,%d,%d)", r, g, b)
}

func lerpColor(ca, cb string, t float64) string {
	var ar, ag, ab, br, bg, bb uint8
	fmt.Sscanf(ca, "rgb(%d,%d,%d)", &ar, &ag, &ab)
	fmt.Sscanf(cb, "rgb(%d,%d,%d)", &br, &bg, &bb)
	r := uint8(math.Round(float64(ar)*(1-t) + float64(br)*t))
	g := uint8(math.Round(float64(ag)*(1-t) + float64(bg)*t))
	bl := uint8(math.Round(float64(ab)*(1-t) + float64(bb)*t))
	return fmt.Sprintf("rgb(%d,%d,%d)", r, g, bl)
}
