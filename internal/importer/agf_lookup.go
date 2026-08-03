package importer

import (
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/types"
)

// EnhanceGlassEntriesFromAGF upgrades imported glass entries (which carry only
// a material label) to full catalog glasses by matching the label against the
// AGF catalog. Matching goes through glass.Catalog.Lookup, so CODE V-style
// spellings (hyphen/underscore stripped, manufacturer-suffixed) resolve the
// same way as at runtime.
func EnhanceGlassEntriesFromAGF(entries, agfGlasses []types.Glass) []types.Glass {
	if len(agfGlasses) == 0 {
		return entries
	}

	gc := glass.NewCatalog()
	for _, g := range agfGlasses {
		gc.Add(g)
	}

	result := make([]types.Glass, len(entries))
	for i, e := range entries {
		mat := e.Label
		if mat == "" {
			result[i] = e
			continue
		}

		matched, ok := gc.Lookup(mat)
		if !ok {
			result[i] = e
			continue
		}

		upgraded := e
		upgraded.Type = types.GlassTypeCatalog
		upgraded.DispersionFormula = matched.DispersionFormula
		upgraded.Coefficients = make([]float64, len(matched.Coefficients))
		copy(upgraded.Coefficients, matched.Coefficients)
		upgraded.Name = matched.Name
		upgraded.Manufacturer = matched.Manufacturer
		upgraded.WavelengthMin = matched.WavelengthMin
		upgraded.WavelengthMax = matched.WavelengthMax
		upgraded.Aliases = make([]string, len(matched.Aliases))
		copy(upgraded.Aliases, matched.Aliases)
		if matched.ND != 0 {
			upgraded.ND = matched.ND
		}
		if matched.VD != 0 {
			upgraded.VD = matched.VD
		}
		upgraded.RefractiveIndices = nil
		result[i] = upgraded
	}

	return result
}
