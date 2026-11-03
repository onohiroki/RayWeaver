package main

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/types"
)

// listGlasses renders the glasses table in the given format: glasses used by
// the selected config's surfaces first (first-use order), then unresolved
// keys, then the remaining glass_catalog entries (declaration order). The n
// columns cover every wavelength collected from chief.reference_wavelength
// and all configs' wavelengths (deduplicated, longest first).
func listGlasses(surfaces []types.Surface, input types.Input, gc *glass.Catalog, format string, showCatalog bool) {
	rows := buildGlassRows(surfaces, input, gc, showCatalog)
	wavelengths := collectWavelengths(input)

	switch format {
	case "yaml", "json":
		emitListOutput(format, glassesListOutput{Glasses: rows})
	case "csv":
		fmt.Println("Glasses:")
		header := glassCSVHeader(rows, wavelengths)
		fmt.Println(strings.Join(quoteCSV(header), ","))
		for _, r := range rows {
			cells := []string{r.Name, r.Manufacturer, r.Type}
			ndCell := ""
			if r.ND > 0 {
				ndCell = strconv.FormatFloat(r.ND, 'g', -1, 64)
			}
			vdCell := ""
			if r.VD > 0 {
				vdCell = strconv.FormatFloat(r.VD, 'g', -1, 64)
			}
			cells = append(cells, ndCell, vdCell)
			for k := range wavelengths {
				cell := ""
				if k < len(r.Indices) && r.Indices[k].Index > 0 {
					cell = strconv.FormatFloat(r.Indices[k].Index, 'g', -1, 64)
				}
				cells = append(cells, cell)
			}
			fmt.Println(strings.Join(quoteCSV(cells), ","))
		}
	default:
		fmt.Println("Glasses:")
		if len(rows) == 0 {
			fmt.Println("(no glasses)")
			return
		}
		hasMfr := false
		for _, r := range rows {
			if r.Manufacturer != "" {
				hasMfr = true
				break
			}
		}
		cols := []tableColumn{
			{header: "Name"},
		}
		if hasMfr {
			cols = append(cols, tableColumn{header: "Mfr"})
		}
		cols = append(cols, tableColumn{header: "Type"})
		cols = append(cols, tableColumn{header: "nd", right: true})
		cols = append(cols, tableColumn{header: "vd", right: true})
		for _, wl := range wavelengths {
			cols = append(cols, tableColumn{header: fmt.Sprintf("%.2fnm", wl*1e6), right: true})
		}
		for _, r := range rows {
			cols[0].cells = append(cols[0].cells, r.Name)
			i := 1
			if hasMfr {
				cols[i].cells = append(cols[i].cells, r.Manufacturer)
				i++
			}
			typeCell := r.Type
			if typeCell == "" {
				typeCell = "-"
			}
			cols[i].cells = append(cols[i].cells, typeCell)
			ndCell := "-"
			if r.ND > 0 {
				ndCell = formatTableFloat(r.ND)
			}
			vdCell := "-"
			if r.VD > 0 {
				vdCell = formatTableFloat(r.VD)
			}
			cols[i+1].cells = append(cols[i+1].cells, ndCell)
			cols[i+2].cells = append(cols[i+2].cells, vdCell)
			for k := range wavelengths {
				nCell := "-"
				if k < len(r.Indices) && r.Indices[k].Index > 0 {
					nCell = formatTableFloat(r.Indices[k].Index)
				}
				cols[i+3+k].cells = append(cols[i+3+k].cells, nCell)
			}
		}
		fmt.Print(renderTable(cols))
	}
}

// glassCSVHeader builds the csv header row: name,mfr,type,nd,vd plus one
// column per collected wavelength (the row Indices are aligned to
// collectWavelengths).
func glassCSVHeader(rows []GlassListRow, wavelengths []float64) []string {
	header := []string{"name", "mfr", "type", "nd", "vd"}
	for _, wl := range wavelengths {
		header = append(header, fmt.Sprintf("%.2fnm", wl*1e6))
	}
	return header
}

// collectWavelengths gathers the analysis wavelengths from the input:
// chief.reference_wavelength plus every config's wavelengths, deduplicated
// within a small tolerance and sorted longest first. Falls back to the d line
// when the input carries no wavelengths at all.
func collectWavelengths(input types.Input) []float64 {
	var wl []float64
	if input.Chief != nil && input.Chief.ReferenceWavelength > 0 {
		wl = append(wl, input.Chief.ReferenceWavelength)
	}
	for _, cfg := range input.Configs {
		for _, w := range cfg.Wavelengths {
			if w.Value > 0 {
				wl = append(wl, w.Value)
			}
		}
	}
	const tol = 1e-12 // mm (= 1e-6 nm): near-equal wavelengths collapse to one
	sort.Float64s(wl)
	var deduped []float64
	for i, v := range wl {
		if i == 0 || math.Abs(v-deduped[len(deduped)-1]) > tol {
			deduped = append(deduped, v)
		}
	}
	for l, r := 0, len(deduped)-1; l < r; l, r = l+1, r-1 {
		deduped[l], deduped[r] = deduped[r], deduped[l]
	}
	if len(deduped) == 0 {
		return []float64{types.DefaultWavelength}
	}
	return deduped
}

// buildGlassRows assembles the glasses table rows: surfaces' catalog glasses
// in first-use order, then unresolved material keys, then the remaining
// glass_catalog entries (YAML/AGF declaration order). Each resolvable row
// carries n at every collected wavelength.
func buildGlassRows(surfaces []types.Surface, input types.Input, gc *glass.Catalog, showCatalog bool) []GlassListRow {
	wavelengths := collectWavelengths(input)
	seenKeys := map[string]bool{}
	unresolvedSeen := map[string]bool{}
	var rows []GlassListRow

	modelDedupKey := func(g *types.Glass) string {
		return "model:" + strconv.FormatFloat(g.ND, 'g', -1, 64) + ":" + strconv.FormatFloat(g.VD, 'g', -1, 64)
	}

	appendResolved := func(g *types.Glass) {
		// For catalog glasses, resolve through the catalog to pick up the
		// full AGF data (dispersion formula, coefficients) when the YAML
		// entry is incomplete (e.g. key-only override).
		resolved := g
		if g.Type == types.GlassTypeCatalog || (g.Type == "" && g.Key != "") {
			if lookup, ok := gc.Lookup(types.ResolveGlassKey(*g)); ok {
				resolved = lookup
			}
		}

		// Dedup key: same resolved display name, manufacturer,
		// type and nd/vd means the same glass — show once.
		// The exception is a catalog entry that resolves to a
		// different type (e.g. a catalog override resolving to
		// a tabulated glass): these are distinct rows.
		var dedupKey string
		switch {
		case resolved.Type == types.GlassTypeModel:
			dedupKey = modelDedupKey(resolved)
		default:
			nd, vd, _ := glass.NDVD(resolved)
			dedupKey = strings.Join([]string{
				glassDisplayName(resolved),
				resolved.Manufacturer,
				glassTypeString(resolved),
				strconv.FormatFloat(nd, 'g', -1, 64),
				strconv.FormatFloat(vd, 'g', -1, 64),
			}, "|")
			// A catalog entry resolving to a different type
			// (e.g. a catalog override for a glass that exists
			// only as tabulated in the catalog) is a distinct row.
			if g.Type == types.GlassTypeCatalog && resolved.Type != g.Type {
				dedupKey += "|" + string(g.Type)
			}
		}
		if dedupKey == "" || seenKeys[dedupKey] {
			return
		}
		seenKeys[dedupKey] = true

		row := GlassListRow{
			Name:         glassDisplayName(resolved),
			Manufacturer: resolved.Manufacturer,
			Type:         glassTypeString(resolved),
		}
		if nd, vd, ok := glass.NDVD(resolved); ok {
			row.ND = nd
			row.VD = vd
		}
		for _, wl := range wavelengths {
			idx := GlassIndex{WavelengthNM: wl * 1e6}
			var n float64
			var err error
			switch {
			case resolved.Type == types.GlassTypeModel:
				n, err = glass.CalcRefractiveIndex(resolved, wl)
			case resolved.Type == types.GlassTypeTabulated && len(resolved.RefractiveIndices) > 0:
				n, err = glass.CalcRefractiveIndex(resolved, wl)
			default:
				mat := types.Material{Key: types.ResolveGlassKey(*resolved)}
				n, err = gc.RefractiveIndex(mat, wl)
			}
			if err != nil {
				glass.Warnf("list[glasses]: cannot compute %q at %.2fnm: %v", dedupKey, wl*1e6, err)
			} else {
				idx.Index = n
			}
			row.Indices = append(row.Indices, idx)
		}
		rows = append(rows, row)
	}

	for _, s := range surfaces {
		if s.ID == 0 {
			continue
		}
		if s.Material.HasModel() && !s.Material.HasKey() {
			dk := "model:" + strconv.FormatFloat(s.Material.ND, 'g', -1, 64) + ":" + strconv.FormatFloat(s.Material.VD, 'g', -1, 64)
			if !seenKeys[dk] {
				seenKeys[dk] = true
				row := GlassListRow{
					Name: fmt.Sprintf("%.5f:%.2f", s.Material.ND, s.Material.VD),
					Type: "model",
					ND:   s.Material.ND,
					VD:   s.Material.VD,
				}
				mat := types.Material{ND: s.Material.ND, VD: s.Material.VD}
				for _, wl := range wavelengths {
					idx := GlassIndex{WavelengthNM: wl * 1e6}
					if n, err := gc.RefractiveIndex(mat, wl); err != nil {
						glass.Warnf("list[glasses]: cannot compute model glass at %.2fnm: %v", wl*1e6, err)
					} else {
						idx.Index = n
					}
					row.Indices = append(row.Indices, idx)
				}
				rows = append(rows, row)
			}
			continue
		}
		if !s.Material.HasKey() {
			continue
		}
		g, ok := gc.Lookup(s.Material.Key)
		if !ok {
			if !unresolvedSeen[s.Material.Key] {
				unresolvedSeen[s.Material.Key] = true
				rows = append(rows, GlassListRow{Name: s.Material.Key})
				errOut("Warning: glass %q not found in the catalog", s.Material.Key)
			}
			continue
		}
		appendResolved(g)
	}
	if showCatalog && input.GlassCatalog != nil {
		for i := range input.GlassCatalog.Entries {
			appendResolved(&input.GlassCatalog.Entries[i])
		}
	}
	return rows
}

// glassDisplayName picks a glass's display name: name, else key/label.
func glassDisplayName(g *types.Glass) string {
	switch {
	case g.Name != "":
		return g.Name
	case g.Key != "":
		return g.Key
	case g.Label != "":
		return g.Label
	}
	return types.ResolveGlassKey(*g)
}

// glassTypeString maps a glass onto its dispersion category label: the formula
// name for catalog glasses (e.g. sellmeier_1), "constant" for a fixed index,
// "model" for nd/vd-based computation and "tabulated" for index tables.
func glassTypeString(g *types.Glass) string {
	switch g.Type {
	case types.GlassTypeModel:
		return "model"
	case types.GlassTypeTabulated:
		return "tabulated"
	}
	// Catalog glass: an absent formula with stored nd/vd falls back to the
	// model-glass computation inside CalcRefractiveIndex.
	f := g.DispersionFormula
	if f == "" {
		if g.ND > 0 {
			return "model"
		}
		return ""
	}
	return string(f)
}
