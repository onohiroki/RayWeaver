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

// listSurfaces renders the surface table of one config in the given format.
// Face 0 (the implicit object plane) is excluded. When the config contains
// aspheric surfaces, an "Asphere Coefficients:" section follows the
// "Surfaces:" section; with no aspheres only Surfaces: is printed. When no
// --config was given and multiple configs carry differing thickness on common
// surface IDs, a final "Thickness Differences:" matrix (rows = configs,
// columns = differing surfaces) is appended.
func listSurfaces(surfaces []types.Surface, gc *glass.Catalog, showCurvature bool, showAutoAperture bool, format string, configs []types.Config, configSelected bool) {
	rows := buildSurfaceRows(surfaces, gc, showCurvature, showAutoAperture)
	aspheres := buildAsphereRows(surfaces)
	var thicknessDiffs []ThicknessDiffRow
	if !configSelected {
		thicknessDiffs = buildThicknessDiffRows(configs)
	}

	switch format {
	case "yaml", "json":
		out := surfacesListOutput{Surfaces: rows, ThicknessDiffs: thicknessDiffs}
		if len(aspheres) > 0 {
			out.AsphereCoefficients = aspheres
		}
		emitListOutput(format, out)
	case "csv":
		fmt.Println("Surfaces:")
		header := []string{"id", "type", "radius", "thickness", "material", "diameter"}
		if showCurvature {
			header = []string{"id", "type", "curvature", "thickness", "material", "diameter"}
		}
		if showAutoAperture {
			header = append(header, "auto_aperture")
		}
		fmt.Println(strings.Join(quoteCSV(header), ","))
		for _, r := range rows {
			radOrCurv := ""
			if showCurvature && r.Curvature != nil {
				radOrCurv = strconv.FormatFloat(*r.Curvature, 'g', -1, 64)
			} else if !showCurvature && r.Radius != nil {
				radOrCurv = strconv.FormatFloat(*r.Radius, 'g', -1, 64)
			}
			dia := ""
			if r.Diameter != 0 {
				dia = strconv.FormatFloat(r.Diameter, 'g', -1, 64)
			}
			cells := []string{
				strconv.Itoa(r.ID), r.Type, radOrCurv,
				strconv.FormatFloat(r.Thickness, 'g', -1, 64),
				r.Material, dia,
			}
			if showAutoAperture {
				aa := ""
				if r.AutoAperture != nil {
					aa = strconv.FormatBool(*r.AutoAperture)
				}
				cells = append(cells, aa)
			}
			fmt.Println(strings.Join(quoteCSV(cells), ","))
		}
		if len(aspheres) > 0 {
			fmt.Println()
			fmt.Println("Asphere Coefficients:")
			maxOrder := asphereMaxOrder(aspheres)
			header := []string{"id", "type", "conic"}
			for order := 4; order <= maxOrder; order += 2 {
				header = append(header, fmt.Sprintf("a%d", order))
			}
			fmt.Println(strings.Join(quoteCSV(header), ","))
			for _, r := range aspheres {
				cells := []string{
					strconv.Itoa(r.ID), r.Type,
					strconv.FormatFloat(r.Conic, 'g', -1, 64),
				}
				for order := 4; order <= maxOrder; order += 2 {
					cell := ""
					if v := asphereCoef(&r, order); v != nil {
						cell = strconv.FormatFloat(*v, 'g', -1, 64)
					}
					cells = append(cells, cell)
				}
				fmt.Println(strings.Join(quoteCSV(cells), ","))
			}
		}
		if len(thicknessDiffs) > 0 {
			ids := thicknessDiffSurfaceIDs(thicknessDiffs)
			header := []string{"config", "name"}
			for _, id := range ids {
				header = append(header, fmt.Sprintf("surface_%d", id))
			}
			fmt.Println()
			fmt.Println("Thickness Differences:")
			fmt.Println(strings.Join(quoteCSV(header), ","))
			for _, ci := range thicknessDiffConfigIndexes(thicknessDiffs) {
				cells := []string{strconv.Itoa(ci), thicknessDiffConfigName(thicknessDiffs, ci)}
				for _, id := range ids {
					cell := ""
					if v, ok := thicknessDiffValue(thicknessDiffs, ci, id); ok {
						cell = strconv.FormatFloat(v, 'g', -1, 64)
					}
					cells = append(cells, cell)
				}
				fmt.Println(strings.Join(quoteCSV(cells), ","))
			}
		}
	default:
		fmt.Println("Surfaces:")
		if len(rows) == 0 {
			fmt.Println("(no surfaces)")
		} else {
			radHeader := "Radius[mm]"
			if showCurvature {
				radHeader = "Curvature[1/mm]"
			}
			cols := []tableColumn{
				{header: "ID", right: true},
				{header: "Type"},
				{header: radHeader, right: true},
				{header: "Thickness[mm]", right: true},
				{header: "Material"},
				{header: "Diameter[mm]", right: true},
			}
			if showAutoAperture {
				cols = append(cols, tableColumn{header: "Auto Ap"})
			}
			for _, r := range rows {
				radCell := "inf"
				if showCurvature && r.Curvature != nil {
					radCell = formatTableFloat(*r.Curvature)
				} else if !showCurvature && r.Radius != nil {
					radCell = formatTableFloat(*r.Radius)
				}
				diaCell := "0"
				if r.Diameter != 0 {
					diaCell = formatTableFloat(r.Diameter)
				}
				cols[0].cells = append(cols[0].cells, strconv.Itoa(r.ID))
				cols[1].cells = append(cols[1].cells, r.Type)
				cols[2].cells = append(cols[2].cells, radCell)
				cols[3].cells = append(cols[3].cells, formatTableFloat(r.Thickness))
				cols[4].cells = append(cols[4].cells, r.Material)
				cols[5].cells = append(cols[5].cells, diaCell)
				if showAutoAperture {
					aaCell := "-"
					if r.AutoAperture != nil {
						aaCell = "fixed"
						if *r.AutoAperture {
							aaCell = "auto"
						}
					}
					cols[6].cells = append(cols[6].cells, aaCell)
				}
			}
			fmt.Print(renderTable(cols))
		}
		if len(aspheres) > 0 {
			maxOrder := asphereMaxOrder(aspheres)
			cols := []tableColumn{
				{header: "ID", right: true},
				{header: "Type"},
				{header: "Conic", right: true},
			}
			for order := 4; order <= maxOrder; order += 2 {
				cols = append(cols, tableColumn{header: fmt.Sprintf("A%d", order), right: true})
			}
			for _, r := range aspheres {
				cols[0].cells = append(cols[0].cells, strconv.Itoa(r.ID))
				cols[1].cells = append(cols[1].cells, r.Type)
				cols[2].cells = append(cols[2].cells, formatTableFloat(r.Conic))
				for i, order := 3, 4; order <= maxOrder; order, i = order+2, i+1 {
					cell := "-"
					if v := asphereCoef(&r, order); v != nil {
						cell = fmt.Sprintf("%.4e", *v)
					}
					cols[i].cells = append(cols[i].cells, cell)
				}
			}
			fmt.Println("\nAsphere Coefficients:")
			fmt.Print(renderTable(cols))
		}
		if len(thicknessDiffs) > 0 {
			ids := thicknessDiffSurfaceIDs(thicknessDiffs)
			cols := []tableColumn{
				{header: "Config", right: true},
				{header: "Name"},
			}
			for _, id := range ids {
				cols = append(cols, tableColumn{header: fmt.Sprintf("Surface %d", id), right: true})
			}
			for _, ci := range thicknessDiffConfigIndexes(thicknessDiffs) {
				cols[0].cells = append(cols[0].cells, strconv.Itoa(ci))
				cols[1].cells = append(cols[1].cells, thicknessDiffConfigName(thicknessDiffs, ci))
				for i, id := range ids {
					cell := ""
					if v, ok := thicknessDiffValue(thicknessDiffs, ci, id); ok {
						cell = formatTableFloat(v)
					}
					cols[2+i].cells = append(cols[2+i].cells, cell)
				}
			}
			fmt.Println("\nThickness Differences:")
			fmt.Print(renderTable(cols))
		}
	}
}

// buildAsphereRows collects the aspheric surfaces (asphere_polynomial /
// asphere_zernike) of one config into coefficient-table rows. coefficients[i]
// holds the even-order term a(4+2i); absent trailing terms stay nil.
func buildAsphereRows(surfaces []types.Surface) []AsphereCoefRow {
	rows := make([]AsphereCoefRow, 0, 4)
	for _, s := range surfaces {
		if s.ID == 0 || (s.Type != types.AspherePolynomial && s.Type != types.AsphereZernike) {
			continue
		}
		row := AsphereCoefRow{
			ID:    s.ID,
			Type:  string(s.Type),
			Conic: s.Conic,
		}
		for i, c := range s.Coefficients {
			if i < 0 || i >= 5 {
				break
			}
			v := c
			switch i {
			case 0:
				row.A4 = &v
			case 1:
				row.A6 = &v
			case 2:
				row.A8 = &v
			case 3:
				row.A10 = &v
			case 4:
				row.A12 = &v
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// asphereCoef returns the row's coefficient of the given even order (4..12),
// or nil when that term is not set on the surface.
func asphereCoef(r *AsphereCoefRow, order int) *float64 {
	switch order {
	case 4:
		return r.A4
	case 6:
		return r.A6
	case 8:
		return r.A8
	case 10:
		return r.A10
	case 12:
		return r.A12
	}
	return nil
}

// asphereMaxOrder returns the highest even-order coefficient present across
// all rows (minimum 4, so at least an A4 column exists).
func asphereMaxOrder(rows []AsphereCoefRow) int {
	maxOrder := 4
	for i := range rows {
		for _, order := range []int{4, 6, 8, 10, 12} {
			if v := asphereCoef(&rows[i], order); v != nil && order > maxOrder {
				maxOrder = order
			}
		}
	}
	return maxOrder
}

// buildSurfaceRows converts the selected config's surfaces into listing rows.
// An absent surface type defaults to sphere (the parse-time zero value).
// When showAutoAperture is set, each row carries the surface's auto_aperture
// state; otherwise the field stays nil (absent from structured output).
func buildSurfaceRows(surfaces []types.Surface, gc *glass.Catalog, showCurvature bool, showAutoAperture bool) []SurfaceListRow {
	rows := make([]SurfaceListRow, 0, len(surfaces))
	for _, s := range surfaces {
		if s.ID == 0 {
			continue
		}
		row := SurfaceListRow{
			ID:        s.ID,
			Type:      string(s.Type),
			Thickness: s.Thickness,
			Material:  surfaceMaterialString(s.Material, gc),
			Diameter:  s.Diameter,
		}
		if row.Type == "" {
			row.Type = "sphere"
		}
		if showCurvature {
			c := s.Curvature
			row.Curvature = &c
		} else if s.Curvature != 0 {
			r := 1.0 / s.Curvature
			row.Radius = &r
		}
		if showAutoAperture {
			aa := s.AutoAperture
			row.AutoAperture = &aa
		}
		rows = append(rows, row)
	}
	return rows
}

// surfaceMaterialString renders a surface's material for the Material column:
// AIR, the resolved catalog glass name (+ manufacturer), an inline model glass
// as nd:vd (nd only when vd is unset/zero), or the raw key when the reference
// does not resolve in the catalog.
func surfaceMaterialString(mat types.Material, gc *glass.Catalog) string {
	if mat.IsAir() {
		return "AIR"
	}
	if mat.HasKey() {
		name := mat.Key
		if g, ok := gc.Lookup(mat.Key); ok {
			if g.Name != "" {
				name = g.Name
			} else if g.Key != "" {
				name = g.Key
			}
			if g.Manufacturer != "" {
				return name + " " + g.Manufacturer
			}
		}
		return name
	}
	if mat.HasModel() {
		if mat.VD != 0 {
			return fmt.Sprintf("%.4f:%.2f", mat.ND, mat.VD)
		}
		return fmt.Sprintf("%.4f", mat.ND)
	}
	return "AIR"
}

// thicknessTol is the thickness equality tolerance for the cross-config
// comparison; differences within it count as identical (float noise).
const thicknessTol = 1e-9

// buildThicknessDiffRows compares the surface thickness of every config and
// returns one record per (config, differing surface). Only surface IDs that
// appear in all configs are compared; a surface qualifies when its thickness
// differs between any two configs beyond thicknessTol. Rows are ordered by
// surface (first-seen order), then config index. Nil when fewer than two
// configs or no differences exist.
func buildThicknessDiffRows(configs []types.Config) []ThicknessDiffRow {
	if len(configs) < 2 {
		return nil
	}
	thickness := map[int][]float64{}
	present := map[int][]bool{}
	var order []int
	for ci := range configs {
		for _, s := range configs[ci].Surfaces {
			if s.ID == 0 {
				continue
			}
			if _, seen := thickness[s.ID]; !seen {
				order = append(order, s.ID)
				thickness[s.ID] = make([]float64, len(configs))
				present[s.ID] = make([]bool, len(configs))
			}
			if !present[s.ID][ci] {
				thickness[s.ID][ci] = s.Thickness
				present[s.ID][ci] = true
			}
		}
	}
	var rows []ThicknessDiffRow
	for _, id := range order {
		vals := thickness[id]
		pr := present[id]
		differs := false
		for i := range vals {
			if !pr[i] {
				differs = false
				break
			}
			if i > 0 && math.Abs(vals[i]-vals[0]) > thicknessTol {
				differs = true
			}
		}
		if !differs {
			continue
		}
		for ci := range configs {
			rows = append(rows, ThicknessDiffRow{
				ConfigIndex: ci,
				ConfigName:  configDisplayName(configs[ci]),
				SurfaceID:   id,
				Thickness:   vals[ci],
			})
		}
	}
	return rows
}

// configDisplayName picks a config's display name: name, else id, else "-".
func configDisplayName(c types.Config) string {
	if c.Name != "" {
		return c.Name
	}
	if c.ID != "" {
		return c.ID
	}
	return "-"
}

// thicknessDiffSurfaceIDs returns the distinct differing surface IDs in
// first-seen row order.
func thicknessDiffSurfaceIDs(rows []ThicknessDiffRow) []int {
	seen := map[int]bool{}
	var ids []int
	for _, r := range rows {
		if !seen[r.SurfaceID] {
			seen[r.SurfaceID] = true
			ids = append(ids, r.SurfaceID)
		}
	}
	return ids
}

// thicknessDiffConfigIndexes returns the distinct config indexes in ascending
// order (rows are generated per surface in config order, so first-seen order
// is already sorted).
func thicknessDiffConfigIndexes(rows []ThicknessDiffRow) []int {
	seen := map[int]bool{}
	var idxs []int
	for _, r := range rows {
		if !seen[r.ConfigIndex] {
			seen[r.ConfigIndex] = true
			idxs = append(idxs, r.ConfigIndex)
		}
	}
	sort.Ints(idxs)
	return idxs
}

// thicknessDiffConfigName returns the display name recorded for one config.
func thicknessDiffConfigName(rows []ThicknessDiffRow, configIndex int) string {
	for _, r := range rows {
		if r.ConfigIndex == configIndex {
			return r.ConfigName
		}
	}
	return "-"
}

// thicknessDiffValue looks up one (config, surface) matrix cell.
func thicknessDiffValue(rows []ThicknessDiffRow, configIndex, surfaceID int) (float64, bool) {
	for _, r := range rows {
		if r.ConfigIndex == configIndex && r.SurfaceID == surfaceID {
			return r.Thickness, true
		}
	}
	return 0, false
}
