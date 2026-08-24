package exporter

import (
	"fmt"
	"math"

	"github.com/hiroki/rayweaver/internal/types"
)

// exportWavelengths returns the selected config's wavelength table and the
// 1-based index of the system reference wavelength. A config without a table
// is exported as a single reference wavelength.
func exportWavelengths(input *types.Input, cfg *types.Config) ([]types.WavelengthItem, int, error) {
	reference := types.DefaultWavelength
	if input.Chief != nil && input.Chief.ReferenceWavelength > 0 {
		reference = input.Chief.ReferenceWavelength
	}
	items := cfg.Wavelengths
	if len(items) == 0 {
		return []types.WavelengthItem{{ID: 0, Value: reference, Weight: 1}}, 1, nil
	}
	for i, item := range items {
		if item.Value > 0 && math.Abs(item.Value-reference) <= 1e-12 {
			return items, i + 1, nil
		}
	}
	return nil, 0, fmt.Errorf("reference wavelength %.12g is not present in config %q wavelength table", reference, cfg.ID)
}
