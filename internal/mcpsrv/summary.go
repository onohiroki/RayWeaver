package mcpsrv

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

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
	// Commands without a dedicated list target still let the CLI decide what
	// the document means: `list summary` folds the result sections, and the MCP
	// adapter only applies the response-size policy below.
	res, err := s.runner.Run(ctx, Call{Command: "list", Args: []string{"summary", "--format", "json"}, Stdin: doc})
	if err == nil && res.ExitCode == 0 {
		var v any
		if json.Unmarshal(res.Stdout, &v) == nil {
			return fit(v)
		}
	}
	note := "the output is not a YAML mapping"
	if err == nil && res.ExitCode != 0 {
		note = strings.TrimSpace(string(res.Stderr))
	}
	return fit(map[string]any{"note": note})
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
