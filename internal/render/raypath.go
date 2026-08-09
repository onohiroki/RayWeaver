package render

import (
	"fmt"
	"math"
	"strconv"
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

func buildRayPaths(results []types.RayResult, chiefRays []types.ChiefRayResult, maxFanRays int) []rayRenderData {
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

	out = append(out, buildFanPaths(chiefRays, maxFanRays, colors)...)

	return out
}

// buildFanPaths collects decimated meridional and rotated fan-ray paths for
// the lens diagram. Sagittal fan rays (XZ plane) are skipped because they
// project onto the optical axis in the YZ view.
func buildFanPaths(chiefRays []types.ChiefRayResult, maxFanRays int, colors []angleColorEntry) []rayRenderData {
	var out []rayRenderData
	if maxFanRays <= 0 {
		return out
	}
	for _, cr := range chiefRays {
		if cr.RayFan == nil {
			continue
		}
		col := fieldAngleColor(cr.FieldAngle, colors)
		addFan := func(points []types.FanPoint) {
			for _, fp := range sampleFanPoints(points, maxFanRays) {
				if len(fp.Path) < 2 {
					continue
				}
				path := buildRayPath(fp.Path)
				if path == "" {
					continue
				}
				out = append(out, rayRenderData{
					path:       path,
					color:      col,
					fieldAngle: cr.FieldAngle,
				})
			}
		}
		addFan(cr.RayFan.Meridional)
		for _, rf := range cr.RayFan.Rotated {
			addFan(rf.Points)
		}
	}
	return out
}

// sampleFanPoints uniformly samples up to max points from the fan.
func sampleFanPoints(points []types.FanPoint, max int) []types.FanPoint {
	if max <= 0 || len(points) <= max {
		return points
	}
	step := float64(len(points)) / float64(max)
	out := make([]types.FanPoint, 0, max)
	for i := 0; i < max; i++ {
		idx := int(float64(i) * step)
		if idx >= len(points) {
			idx = len(points) - 1
		}
		out = append(out, points[idx])
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
	if n, ok := marginalFieldIndex(id); ok && n < len(chiefRays) {
		return chiefRays[n].FieldAngle
	}
	for _, cr := range chiefRays {
		// Traced chief rays are renamed "chief_<angle>deg" (%.0f) at emission;
		// the stored ChiefRayResult.ChiefRay keeps its original (empty) ID.
		if cr.ChiefRay.ID == id || id == fmt.Sprintf("chief_%.0fdeg", cr.FieldAngle) {
			return cr.FieldAngle
		}
	}
	return 0
}

// marginalFieldIndex parses a marginal-ray id of the form "marginal_f{N}_..."
// and returns the field index N it belongs to.
func marginalFieldIndex(id string) (int, bool) {
	const p = "marginal_f"
	if !strings.HasPrefix(id, p) {
		return 0, false
	}
	rest := id[len(p):]
	if rest == "" {
		return 0, false
	}
	j := 0
	for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
		j++
	}
	if j == 0 {
		return 0, false
	}
	n, err := strconv.Atoi(rest[:j])
	if err != nil {
		return 0, false
	}
	return n, true
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
