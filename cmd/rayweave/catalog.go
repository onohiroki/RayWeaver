package main

import (
	"os"
	"sort"
	"strings"

	"github.com/hiroki/rayweaver/internal/coating"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/optimize"
	"github.com/hiroki/rayweaver/internal/types"
)

func loadCatalogs(input *types.Input, glassDir ...string) (*glass.Catalog, *coating.Catalog) {
	// escape/pso route these sinks into their progress stream (see
	// newEscapeProgress) *before* the catalog is loaded, so an AGF warning
	// raised here lands in the JSONL capture instead of being the one
	// plain-text line of it. The stream owns them until its finish runs; every
	// other command gets the tagged stderr writer.
	if !warnSinksRouted {
		glass.Warnf = errOut
		// Optimisation warnings (a back-focus solve that could not be
		// evaluated, ...) go through the tagged stderr writer so a piped run
		// still shows which stage reported them.
		optimize.Warnf = errOut
	}
	gc := glass.NewCatalog()
	if input.GlassCatalog == nil {
		input.GlassCatalog = &types.GlassCatalog{}
	}

	for _, g := range input.GlassCatalog.Entries {
		// Catalog-type entries without a dispersion formula are metadata
		// overrides (name, label, manufacturer); do not register them in
		// the catalog so that the full AGF data is used instead.
		if g.Type == types.GlassTypeCatalog && g.DispersionFormula == "" && len(g.Coefficients) == 0 {
			continue
		}
		gc.Add(g)
	}

	agfPath := ""
	if len(glassDir) > 0 && glassDir[0] != "" {
		agfPath = glassDir[0]
	} else if input.GlassCatalog.Directory != "" {
		agfPath = input.GlassCatalog.Directory
	}

	// Glass keys actually referenced by the lens surfaces. An external AGF
	// catalog is registered into the emitted glass_catalog only for these,
	// keeping the piped YAML small even when the catalog carries thousands of
	// glasses. The runtime catalog still receives every AGF glass so lookups
	// (aliases, manufacturer suffixes, moulding grades) resolve identically.
	referenced := referencedGlassKeys(input)

	if agfPath != "" {
		agfGlasses, err := glass.LoadAGFDir(agfPath)
		if err != nil {
			errOut("Warning: cannot load AGF directory %s: %v", agfPath, err)
		}
		needed := neededAGFKeys(referenced, agfGlasses)

		// When manufacturer_order is set (or default applies), add AGF glasses
		// in priority order so that the preferred manufacturer's glass wins
		// for duplicate names.
		mfrOrder := input.GlassCatalog.ManufacturerOrder
		if len(mfrOrder) == 0 {
			mfrOrder = glass.DefaultManufacturerOrder
		}
		{
			ordered := orderAGFGlassesByManufacturer(agfGlasses, mfrOrder)
			for _, g := range ordered {
				key := types.ResolveGlassKey(g)
				// AGF catalog-type glasses always enter the catalog,
				// overwriting YAML tabulated entries with the same key
				// so that ray-trace commands get the full formula data.
				// The YAML tabulated entry stays in GlassCatalog.Entries
				// for display by 'list glasses'.
				if existing, ok := gc.Lookup(key); ok && existing.Type == g.Type {
					continue
				}
				gc.Add(g)
				if needed[key] && !containsGlass(input.GlassCatalog.Entries, g) {
					input.GlassCatalog.Entries = append(input.GlassCatalog.Entries, g)
				}
			}
		}
	}

	if agfPath == "" {
		var allGlasses []types.Glass
		for _, path := range input.GlassCatalog.Files {
			agfData, err := os.ReadFile(path)
			if err != nil {
				errOut("Warning: cannot read AGF file %s: %v", path, err)
				continue
			}
			glasses, err := glass.ParseAGF(agfData)
			if err != nil {
				errOut("Warning: cannot parse AGF file %s: %v", path, err)
				continue
			}
			allGlasses = append(allGlasses, glasses...)
		}
		needed := neededAGFKeys(referenced, allGlasses)
		for _, g := range allGlasses {
			gc.Add(g)
			if needed[types.ResolveGlassKey(g)] && !containsGlass(input.GlassCatalog.Entries, g) {
				input.GlassCatalog.Entries = append(input.GlassCatalog.Entries, g)
			}
		}
	}

	cc := coating.NewCatalog()
	if input.CoatingCatalog != nil {
		for _, e := range input.CoatingCatalog.Entries {
			cc.Add(e)
		}
		// Resolve material-name layer references against the glass catalog so
		// ray tracing and the tmm command use the real index, never the 1.5
		// computePol fallback.
		cc.ResolveAll(gc, types.DefaultWavelength)
	}

	return gc, cc
}

func containsGlass(entries []types.Glass, g types.Glass) bool {
	for _, e := range entries {
		if glassEqual(g, e) {
			return true
		}
	}
	return false
}

func glassEqual(a, b types.Glass) bool {
	if a.Type == types.GlassTypeModel && b.Type == types.GlassTypeModel {
		return a.ND == b.ND && a.VD == b.VD
	}
	return types.ResolveGlassKey(a) == types.ResolveGlassKey(b)
}

// referencedGlassKeys returns the set of glass catalog keys referenced by any
// config's surfaces (material.key only). These are the only glasses a lens
// needs resolved at runtime, so only they may be registered into the emitted
// glass_catalog from an external AGF catalog.
func referencedGlassKeys(input *types.Input) map[string]bool {
	set := make(map[string]bool)
	for i := range input.Configs {
		for _, s := range input.Configs[i].Surfaces {
			if s.Material.HasKey() {
				set[s.Material.Key] = true
			}
		}
	}
	return set
}

// neededAGFKeys resolves every referenced glass key against the AGF glasses
// using the same Catalog.Lookup normalization as runtime (hyphen/underscore
// stripping, manufacturer suffix, moulding grade) and returns the set of AGF
// glass keys that back a referenced material.
func neededAGFKeys(referenced map[string]bool, agfGlasses []types.Glass) map[string]bool {
	if len(referenced) == 0 || len(agfGlasses) == 0 {
		return nil
	}
	agfGc := glass.NewCatalog()
	for _, g := range agfGlasses {
		agfGc.Add(g)
	}
	needed := make(map[string]bool)
	for key := range referenced {
		if g, ok := agfGc.Lookup(key); ok {
			if resolved := types.ResolveGlassKey(*g); resolved != "" {
				needed[resolved] = true
			}
		}
	}
	return needed
}

// orderAGFGlassesByManufacturer returns agfGlasses sorted so that glasses from
// manufacturers listed in mfrOrder come first (in that order), followed by
// glasses from unlisted manufacturers. This ensures the preferred
// manufacturer's glass wins when Catalog.Add is called for duplicate names.
func orderAGFGlassesByManufacturer(agfGlasses []types.Glass, mfrOrder []string) []types.Glass {
	if len(mfrOrder) == 0 {
		return agfGlasses
	}
	priority := make(map[string]int, len(mfrOrder))
	for i, mfr := range mfrOrder {
		priority[strings.ToUpper(mfr)] = i
	}
	sorted := make([]types.Glass, len(agfGlasses))
	copy(sorted, agfGlasses)
	sort.SliceStable(sorted, func(i, j int) bool {
		oi, oki := priority[strings.ToUpper(sorted[i].Manufacturer)]
		oj, okj := priority[strings.ToUpper(sorted[j].Manufacturer)]
		if oki && okj {
			return oi < oj
		}
		return oki && !okj
	})
	return sorted
}
