package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/hiroki/rayweaver/internal/spectral"
	"github.com/hiroki/rayweaver/internal/types"
)

// resolveConfig finds the config whose id matches val (by string id or 0-based index).
// Returns the index into configs, or -1 + error message.
func resolveConfig(configs []types.Config, val string) (int, string) {
	// Try numeric index first
	if idx, err := strconv.Atoi(val); err == nil && idx >= 0 && idx < len(configs) {
		return idx, ""
	}
	// Fall back to string id
	for i := range configs {
		if configs[i].ID == val {
			return i, ""
		}
	}
	return -1, fmt.Sprintf("config %q not found", val)
}

// configWavelengthValues returns the selected config's wavelength table as
// plain values for multi-wavelength analysis commands.
func configWavelengthValues(configs []types.Config, configFlag *string) []float64 {
	idx := 0
	if configFlag != nil && *configFlag != "" {
		var err string
		idx, err = resolveConfig(configs, *configFlag)
		if idx < 0 {
			errExitf("Error: %s", err)
		}
	}
	if idx < 0 || idx >= len(configs) {
		return nil
	}
	values := make([]float64, 0, len(configs[idx].Wavelengths))
	for _, wavelength := range configs[idx].Wavelengths {
		if wavelength.Value > 0 {
			values = append(values, wavelength.Value)
		}
	}
	return values
}

// configWavelengthSamples returns the selected config's wavelength table as a
// normalised sample set (labels and weights preserved).
func configWavelengthSamples(configs []types.Config, configFlag *string) []types.WavelengthSample {
	idx := 0
	if configFlag != nil && *configFlag != "" {
		var msg string
		idx, msg = resolveConfig(configs, *configFlag)
		if idx < 0 {
			errExitf("Error: %s", msg)
		}
	}
	if idx < 0 || idx >= len(configs) {
		return nil
	}
	return spectral.DesignSamples(configs[idx].Wavelengths)
}

func effectiveReferenceWavelength(chief *types.ChiefInput) float64 {
	if chief != nil && chief.ReferenceWavelength > 0 {
		return chief.ReferenceWavelength
	}
	return types.DefaultWavelength
}

func setReferenceWavelength(chief *types.ChiefInput) float64 {
	wavelength := effectiveReferenceWavelength(chief)
	if chief != nil {
		chief.ReferenceWavelength = wavelength
	}
	return wavelength
}

// configSurfaces resolves the surface list for a command:
//   - if configFlag is set, returns the matching config's surfaces
//   - otherwise returns configs[0].surfaces
//
// Canonical surface storage is configs[].surfaces.
func configSurfaces(configs []types.Config, configFlag *string) []types.Surface {
	if *configFlag != "" {
		idx, err := resolveConfig(configs, *configFlag)
		if idx < 0 {
			errExitf("Error: %s", err)
		}
		return configs[idx].Surfaces
	}
	if len(configs) > 0 {
		return configs[0].Surfaces
	}
	return nil
}

// configTargets resolves a config selection shared by plot, chief and trace:
// --config selects one config, --configs selects an explicit list (ids or
// 0-based indexes, in the given order, duplicates dropped) and multi selects
// the active configs in document order, falling back to every config when none
// is active. limit > 0 caps the implicit multi selection (plot draws at most 5
// by default); an explicit list is never capped. multiLabel names the flag(s)
// that turn multi selection on, for the error message when one is combined with
// --config.
//
// The returned warnings cover configs without surfaces (skipped) and an
// implicit selection truncated by the limit. In single-config mode exactly one
// index is returned.
func configTargets(configs []types.Config, configFlag string, multi bool, list []string, limit int, multiLabel string) ([]int, bool, []string, error) {
	multiMode := multi || len(list) > 0
	if configFlag != "" && multiMode {
		return nil, false, nil, fmt.Errorf("--config and %s are mutually exclusive", multiLabel)
	}

	if !multiMode {
		idx := 0
		if configFlag != "" {
			i, msg := resolveConfig(configs, configFlag)
			if i < 0 {
				return nil, false, nil, fmt.Errorf("%s", msg)
			}
			idx = i
		}
		if len(configs[idx].Surfaces) == 0 {
			return nil, false, nil, fmt.Errorf("no surfaces to process (define configs[].surfaces)")
		}
		return []int{idx}, false, nil, nil
	}

	candidates := make([]int, 0, len(configs))
	if len(list) > 0 {
		seen := make(map[int]bool, len(list))
		for _, token := range list {
			i, msg := resolveConfig(configs, token)
			if i < 0 {
				return nil, true, nil, fmt.Errorf("%s", msg)
			}
			if seen[i] {
				continue
			}
			seen[i] = true
			candidates = append(candidates, i)
		}
	} else {
		for i := range configs {
			if configs[i].Active {
				candidates = append(candidates, i)
			}
		}
		if len(candidates) == 0 {
			for i := range configs {
				candidates = append(candidates, i)
			}
		}
	}

	var warnings []string
	indices := make([]int, 0, len(candidates))
	for _, i := range candidates {
		if len(configs[i].Surfaces) == 0 {
			warnings = append(warnings, fmt.Sprintf("config %q has no surfaces — skipped", configLabel(configs, i)))
			continue
		}
		indices = append(indices, i)
	}
	if len(list) == 0 && limit > 0 && len(indices) > limit {
		warnings = append(warnings, fmt.Sprintf("%d configs selected; using the first %d (use --configs to choose)", len(indices), limit))
		indices = indices[:limit]
	}
	if len(indices) == 0 {
		return nil, true, warnings, fmt.Errorf("no config to process")
	}
	return indices, true, warnings, nil
}

// hasPerConfigRays reports whether the document carries the per-config ray
// pipeline (chief --all-configs): any config with its own chief_rays or rays.
func hasPerConfigRays(configs []types.Config) bool {
	for i := range configs {
		if len(configs[i].Results) > 0 || len(configs[i].ChiefRays) > 0 {
			return true
		}
	}
	return false
}

// splitConfigList parses a --configs value: comma-separated config ids or
// 0-based indexes, surrounding whitespace ignored.
func splitConfigList(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// configLabel names a config in warnings, falling back to its document
// position when the config declares no id.
func configLabel(configs []types.Config, idx int) string {
	if idx >= 0 && idx < len(configs) && configs[idx].ID != "" {
		return configs[idx].ID
	}
	return fmt.Sprintf("#%d", idx)
}
