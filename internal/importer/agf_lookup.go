package importer

import (
	"strings"

	"github.com/hiroki/rayweaver/internal/types"
)

func EnhanceGlassEntriesFromAGF(entries []types.Glass, agfGlasses []types.Glass, format string) []types.Glass {
	if len(agfGlasses) == 0 {
		return entries
	}

	lookup := buildAGFLookup(agfGlasses)

	result := make([]types.Glass, len(entries))
	for i, e := range entries {
		mat := e.Label
		if mat == "" {
			result[i] = e
			continue
		}

		var matched *types.Glass

		if format == "codev" {
			matched = lookupCodeV(lookup, mat)
		} else {
			if g, ok := lookup[mat]; ok {
				matched = g
			}
		}

		if matched != nil {
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
		} else {
			result[i] = e
		}
	}

	return result
}

func buildAGFLookup(glasses []types.Glass) map[string]*types.Glass {
	m := make(map[string]*types.Glass)
	for i := range glasses {
		g := &glasses[i]
		addLookupKey(m, g.Name, g)
		for _, alias := range g.Aliases {
			addLookupKey(m, alias, g)
		}
	}
	return m
}

func addLookupKey(m map[string]*types.Glass, key string, g *types.Glass) {
	m[key] = g
	norm := normalizeGlassName(key)
	if norm != key {
		m[norm] = g
	}
}

func normalizeGlassName(name string) string {
	s := strings.ReplaceAll(name, "-", "")
	s = strings.ReplaceAll(s, "_", "")
	return strings.ToUpper(s)
}

func lookupCodeV(lookup map[string]*types.Glass, name string) *types.Glass {
	if g, ok := lookup[name]; ok {
		return g
	}
	norm := normalizeGlassName(name)
	if g, ok := lookup[norm]; ok {
		return g
	}
	if strings.Contains(name, "_") {
		parts := strings.SplitN(name, "_", 2)
		prefix := parts[0]
		wantMfr := strings.ToUpper(parts[1])

		if g, ok := lookup[prefix]; ok {
			if mfrMatch(g, wantMfr) {
				return g
			}
		}
		normPrefix := normalizeGlassName(prefix)
		if g, ok := lookup[normPrefix]; ok {
			if mfrMatch(g, wantMfr) {
				return g
			}
		}
	}
	return nil
}

func mfrMatch(g *types.Glass, wantMfr string) bool {
	if wantMfr == "" {
		return true
	}
	actual := strings.ToUpper(g.Manufacturer)
	return actual == wantMfr
}
