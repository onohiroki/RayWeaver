package main

import (
	"gopkg.in/yaml.v3"
)

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

// listSummary renders the compact, JSON-safe view of a document that the MCP
// server returns as its tool summary. The CLI owns this interpretation: the MCP
// adapter runs `list summary --format json` instead of keeping a second one.
// Sections that carry bulk (configs, rays, chief_rays, results) are folded to
// counts and per-field statistics; every other top-level key is passed through
// unchanged.
func listSummary(data []byte, format string) {
	switch format {
	case "yaml", "json":
		emitListOutput(format, genericSummary(data))
	default:
		errExitf("Error: list summary supports --format yaml|json (got %q)", format)
	}
}
