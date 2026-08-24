package importer

import (
	"strings"

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

		result[i] = upgradeGlassEntry(e, matched)
	}

	return result
}

// EnhanceGlassEntriesFromAGFMfr upgrades imported glass entries by matching
// against the AGF catalog with manufacturer-priority resolution. Glasses are
// grouped by manufacturer; the search follows mfrOrder (preferred manufacturers
// first), then falls back to remaining manufacturers. When mfrOrder is empty,
// behaviour is identical to EnhanceGlassEntriesFromAGF.
func EnhanceGlassEntriesFromAGFMfr(entries []types.Glass, agfGlasses []types.Glass, mfrOrder []string) []types.Glass {
	if len(agfGlasses) == 0 {
		return entries
	}

	// Group AGF glasses by normalised manufacturer key.
	type mfrGroup struct {
		glasses []types.Glass
		catalog *glass.Catalog
	}
	groups := make(map[string]*mfrGroup)
	for _, g := range agfGlasses {
		mfr := strings.ToUpper(g.Manufacturer)
		if mfr == "" {
			mfr = ""
		}
		grp, ok := groups[mfr]
		if !ok {
			grp = &mfrGroup{}
			groups[mfr] = grp
		}
		grp.glasses = append(grp.glasses, g)
	}

	// Build ordered list of manufacturer keys: mfrOrder first (preserving
	// order), then remaining groups in map iteration order.
	orderedKeys := make([]string, 0, len(groups))
	seen := make(map[string]bool)
	for _, mfr := range mfrOrder {
		upper := strings.ToUpper(mfr)
		if groups[upper] != nil && !seen[upper] {
			orderedKeys = append(orderedKeys, upper)
			seen[upper] = true
		}
	}
	for mfr := range groups {
		if !seen[mfr] {
			orderedKeys = append(orderedKeys, mfr)
		}
	}

	// Build a catalog per group (lazy).
	buildCatalog := func(grp *mfrGroup) {
		if grp.catalog != nil {
			return
		}
		grp.catalog = glass.NewCatalog()
		for _, g := range grp.glasses {
			grp.catalog.Add(g)
		}
	}

	result := make([]types.Glass, len(entries))
	for i, e := range entries {
		mat := e.Label
		if mat == "" {
			result[i] = e
			continue
		}

		matched := false
		for _, mfr := range orderedKeys {
			grp := groups[mfr]
			buildCatalog(grp)
			if g, ok := grp.catalog.Lookup(mat); ok {
				result[i] = upgradeGlassEntry(e, g)
				matched = true
				break
			}
		}
		if !matched {
			result[i] = e
		}
	}

	return result
}

// upgradeGlassEntry copies the catalog glass data onto the imported entry.
func upgradeGlassEntry(e types.Glass, matched *types.Glass) types.Glass {
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
	return upgraded
}
