package mcpsrv

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"

	"gopkg.in/yaml.v3"
)

// listSummaryTarget maps a command to the read-only `list` target that already
// knows how to phrase its result. Reusing the CLI's summariser keeps one
// definition of what a document means, instead of a second one here.
var listSummaryTarget = map[string]string{
	"paraxial": "paraxial",
	"trace":    "rays",
	"optimize": "merit",
	"escape":   "global",
	"pso":      "global",
	"chief":    "fields",
	"focus":    "focus",
}

// summarize returns a compact, JSON-safe view of a produced document and
// whether it had to be compacted.
func (s *server) summarize(ctx context.Context, command string, doc []byte) (any, bool) {
	if target, ok := listSummaryTarget[command]; ok {
		args := []string{target, "--format", "json"}
		if command == "trace" {
			args = append(args, "--summary")
		}
		res, err := s.runner.Run(ctx, Call{Command: "list", Args: args, Stdin: doc})
		if err == nil && res.ExitCode == 0 {
			var v any
			if json.Unmarshal(res.Stdout, &v) == nil {
				if command == "chief" {
					attachChiefSpots(doc, v)
				}
				return fit(v)
			}
		}
	}
	return fit(genericSummary(doc))
}

// genericSummary keeps the result-bearing sections and folds the bulk (ray
// paths, pupil grids, per-surface records) into counts and spot statistics.
func genericSummary(doc []byte) any {
	var m map[string]any
	if err := yaml.Unmarshal(doc, &m); err != nil {
		return map[string]any{"note": "the output is not a YAML mapping: " + err.Error()}
	}
	out := map[string]any{}
	for k, v := range m {
		switch k {
		case "metadata", "glass_catalog", "version":
			continue
		case "configs":
			out["configs"] = summariseConfigs(v)
		case "rays":
			out["rays"] = summariseRays(v)
		case "chief_rays":
			out["chief_rays"] = summariseChiefRays(v)
		case "results":
			out["results"] = summariseResults(v)
		default:
			out[k] = v
		}
	}
	return out
}

func summariseConfigs(v any) any {
	arr, ok := v.([]any)
	if !ok {
		return v
	}
	out := make([]any, 0, len(arr))
	for _, c := range arr {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		row := map[string]any{}
		for _, k := range []string{"id", "name", "zoom_position"} {
			if val, ok := cm[k]; ok {
				row[k] = val
			}
		}
		if s, ok := cm["surfaces"].([]any); ok {
			row["surfaces"] = len(s)
		}
		out = append(out, row)
	}
	return out
}

func summariseRays(v any) any {
	if m, ok := v.(map[string]any); ok {
		if arr, ok := m["rays"].([]any); ok {
			return map[string]any{"count": len(arr)}
		}
		return map[string]any{"keys": sortedKeys(m)}
	}
	if arr, ok := v.([]any); ok {
		return map[string]any{"count": len(arr)}
	}
	return v
}

func summariseChiefRays(v any) any {
	arr, ok := v.([]any)
	if !ok {
		return v
	}
	out := make([]any, 0, len(arr))
	for _, c := range arr {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		row := map[string]any{}
		if fa, ok := cm["field_angle"]; ok {
			row["field_angle"] = fa
		}
		if st, ok := cm["spot_stats"].(map[string]any); ok {
			spot := map[string]any{}
			for _, k := range []string{"rms_x", "rms_y", "rms_r", "traced_rays", "total_rays"} {
				if val, ok := st[k]; ok {
					spot[k] = val
				}
			}
			row["spot_stats"] = spot
		}
		if gp, ok := cm["grid_points"].([]any); ok {
			row["grid_points"] = len(gp)
		}
		if pp, ok := cm["pupil_probe"]; ok {
			row["pupil_probe"] = pp
		}
		out = append(out, row)
	}
	return out
}

// summariseResults keeps the scalar fields of a per-ray result and folds the
// per-surface path away: it is the bulk of a trace document and is only needed
// for debugging, which the document resource serves.
func summariseResults(v any) any {
	arr, ok := v.([]any)
	if !ok {
		return v
	}
	out := make([]any, 0, len(arr))
	for _, r := range arr {
		rm, ok := r.(map[string]any)
		if !ok {
			continue
		}
		row := map[string]any{}
		for k, val := range rm {
			switch k {
			case "surfaces":
				if s, ok := val.([]any); ok {
					row["surfaces"] = len(s)
				}
			default:
				switch val.(type) {
				case map[string]any, []any:
					// nested detail (jones matrices, paths) - omit
				default:
					row[k] = val
				}
			}
		}
		out = append(out, row)
	}
	return out
}

func attachChiefSpots(doc []byte, sum any) {
	m, ok := sum.(map[string]any)
	if !ok {
		return
	}
	var raw struct {
		ChiefRays []struct {
			FieldAngle float64        `yaml:"field_angle"`
			SpotStats  map[string]any `yaml:"spot_stats"`
		} `yaml:"chief_rays"`
	}
	if yaml.Unmarshal(doc, &raw) != nil {
		return
	}
	var spots []any
	for _, cr := range raw.ChiefRays {
		row := map[string]any{"field_angle": cr.FieldAngle}
		for _, k := range []string{"rms_x", "rms_y", "rms_r", "traced_rays", "total_rays"} {
			if v, ok := cr.SpotStats[k]; ok {
				row[k] = v
			}
		}
		spots = append(spots, row)
	}
	if len(spots) > 0 {
		m["spot_stats"] = spots
	}
}

// fit compacts v until its JSON fits the summary budget, reporting whether it
// had to be reduced. The full document is always reachable as a resource.
func fit(v any) (any, bool) {
	limits := []struct{ array, depth int }{
		{50, 6}, {20, 5}, {8, 4}, {3, 3},
	}
	for i, l := range limits {
		c := compact(v, l.array, l.depth)
		if b, err := json.Marshal(c); err == nil && len(b) <= 64<<10 {
			return c, i > 0
		}
	}
	if m, ok := v.(map[string]any); ok {
		return map[string]any{
			"keys": sortedKeys(m),
			"note": "the summary is too large; read the document resource for the data",
		}, true
	}
	return map[string]any{"note": "the summary is too large; read the document resource for the data"}, true
}

// compact folds arrays to a head sample plus a count, and replaces the values
// JSON cannot carry (NaN, ±Inf) with their string form.
func compact(v any, maxArray, depth int) any {
	switch t := v.(type) {
	case map[string]any:
		if depth <= 0 {
			return fmt.Sprintf("<%d keys>", len(t))
		}
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = compact(val, maxArray, depth-1)
		}
		return out
	case []any:
		n := len(t)
		head := n
		if head > maxArray {
			head = maxArray
		}
		out := make([]any, 0, head)
		for _, val := range t[:head] {
			out = append(out, compact(val, maxArray, depth-1))
		}
		if n > head {
			return map[string]any{"items": out, "count": n, "omitted": n - head}
		}
		return out
	case float64:
		if math.IsNaN(t) {
			return "NaN"
		}
		if math.IsInf(t, 0) {
			return strconv.FormatFloat(t, 'g', -1, 64)
		}
		return t
	case float32:
		return compact(float64(t), maxArray, depth)
	case json.Number:
		return t
	default:
		return v
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// decodeJSON is a small helper for tools that return a JSON view (list/query).
func decodeJSON(data []byte) (any, bool) {
	dec := json.NewDecoder(bytes.NewReader(data))
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	return v, true
}
