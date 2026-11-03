package main

import (
	"fmt"
	"strconv"

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
