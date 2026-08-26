package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/types"
	"gopkg.in/yaml.v3"
)

// SurfaceListRow is one row of the `list surfaces` table. Radius is nil for a
// flat surface (infinite radius) and Curvature is set instead under
// --curvature; the pointers keep flat surfaces visible as null/empty rather
// than a misleading 0.
type SurfaceListRow struct {
	ID        int      `json:"id" yaml:"id"`
	Type      string   `json:"type" yaml:"type"`
	Radius    *float64 `json:"radius,omitempty" yaml:"radius,omitempty"`
	Curvature *float64 `json:"curvature,omitempty" yaml:"curvature,omitempty"`
	Thickness float64  `json:"thickness" yaml:"thickness"`
	Material  string   `json:"material" yaml:"material"`
	Diameter  float64  `json:"diameter" yaml:"diameter"`
}

// AsphereCoefRow is one row of the separate Asphere Coefficients table: the
// conic constant and even-order polynomial coefficients (a4..a12,
// coefficients[0..4]) of an aspheric surface. Nil pointers mean the term is
// not present on that surface. Fixed fields keep the YAML/JSON key order a4 <
// a6 < ... (a map would sort "a10"/"a12" ahead of "a4").
type AsphereCoefRow struct {
	ID    int      `json:"id" yaml:"id"`
	Type  string   `json:"type" yaml:"type"`
	Conic float64  `json:"conic" yaml:"conic"`
	A4    *float64 `json:"a4,omitempty" yaml:"a4,omitempty"`
	A6    *float64 `json:"a6,omitempty" yaml:"a6,omitempty"`
	A8    *float64 `json:"a8,omitempty" yaml:"a8,omitempty"`
	A10   *float64 `json:"a10,omitempty" yaml:"a10,omitempty"`
	A12   *float64 `json:"a12,omitempty" yaml:"a12,omitempty"`
}

// surfacesListOutput is the structured (yaml/json) shape of `list surfaces`:
// the surface table plus, only when aspheres exist, their coefficients.
type surfacesListOutput struct {
	Surfaces            []SurfaceListRow `json:"surfaces" yaml:"surfaces"`
	AsphereCoefficients []AsphereCoefRow `json:"asphere_coefficients,omitempty" yaml:"asphere_coefficients,omitempty"`
}

// runList implements the `list` subcommand: a read-only, human-readable
// listing of the input system's definition data (surfaces today; glasses,
// decenter, reflect planned). It never traces rays and prints formatted
// tables by default, with --yaml/--json/--csv variants for automation.
//
//	rayweave list [--format table|yaml|json|csv] [--config ID]
//	              [--glass-dir DIR] [--curvature] [TARGET...] < input.yaml
func runList(data []byte) {
	args := os.Args[2:]
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	format := fs.String("format", "table", "output format: table | yaml | json | csv")
	configFlag := fs.String("config", "", "select config by id (multi-config mode)")
	glassDir := fs.String("glass-dir", "", "AGF glass catalog directory")
	showCurvature := fs.Bool("curvature", false, "show curvature instead of radius")
	fs.Parse(args)

	switch *format {
	case "table", "yaml", "json", "csv":
	default:
		errOut("Error: unknown --format %q (supported: table, yaml, json, csv)", *format)
		os.Exit(1)
	}

	input := parseYAML[types.Input](data)
	gc, _ := loadCatalogs(&input, *glassDir)
	surfaces := configSurfaces(input.Configs, configFlag)

	targets := fs.Args()
	if len(targets) == 0 {
		targets = []string{"surfaces"}
	}
	printed := map[string]bool{}
	for _, target := range targets {
		if printed[target] {
			continue
		}
		printed[target] = true
		switch target {
		case "surfaces":
			listSurfaces(surfaces, gc, *showCurvature, *format)
		default:
			errOut("Error: unknown list target %q (supported: surfaces)", target)
			os.Exit(1)
		}
	}
}

// listSurfaces renders the surface table of one config in the given format.
// Face 0 (the implicit object plane) is excluded. When the config contains
// aspheric surfaces, an "Asphere Coefficients:" section follows the
// "Surfaces:" section; with no aspheres only Surfaces: is printed.
func listSurfaces(surfaces []types.Surface, gc *glass.Catalog, showCurvature bool, format string) {
	rows := buildSurfaceRows(surfaces, gc, showCurvature)
	aspheres := buildAsphereRows(surfaces)

	switch format {
	case "yaml":
		out := surfacesListOutput{Surfaces: rows}
		if len(aspheres) > 0 {
			out.AsphereCoefficients = aspheres
		}
		outData, err := yaml.Marshal(out)
		if err != nil {
			errOut("Error marshaling list output: %v", err)
			os.Exit(1)
		}
		os.Stdout.Write(outData)
	case "json":
		out := surfacesListOutput{Surfaces: rows}
		if len(aspheres) > 0 {
			out.AsphereCoefficients = aspheres
		}
		outData, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			errOut("Error marshaling list output: %v", err)
			os.Exit(1)
		}
		os.Stdout.Write(outData)
		fmt.Println()
	case "csv":
		fmt.Println("Surfaces:")
		header := []string{"id", "type", "radius", "thickness", "material", "diameter"}
		if showCurvature {
			header = []string{"id", "type", "curvature", "thickness", "material", "diameter"}
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
func buildSurfaceRows(surfaces []types.Surface, gc *glass.Catalog, showCurvature bool) []SurfaceListRow {
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

// formatTableFloat formats a value for fixed-width table cells: 6 decimal
// places in the readable range, scientific notation outside it
// (|x| < 0.001 or |x| > 9999.999999).
func formatTableFloat(v float64) string {
	if v == 0 {
		return "0"
	}
	a := math.Abs(v)
	if a < 1e-3 || a > 9999.999999 {
		return fmt.Sprintf("%.4e", v)
	}
	return fmt.Sprintf("%.6f", v)
}

// tableColumn is one column of a rendered text table.
type tableColumn struct {
	header string
	right  bool
	cells  []string
}

// renderTable lays out columns padded to their widest cell (headers included),
// numeric columns right-aligned, string columns left-aligned, two spaces
// between columns.
func renderTable(cols []tableColumn) string {
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = utf8.RuneCountInString(c.header)
		for _, cell := range c.cells {
			if n := utf8.RuneCountInString(cell); n > widths[i] {
				widths[i] = n
			}
		}
	}
	var sb strings.Builder
	for i, c := range cols {
		if i > 0 {
			sb.WriteString("  ")
		}
		sb.WriteString(padCell(c.header, widths[i], c.right))
	}
	sb.WriteString("\n")
	for r := range cols[0].cells {
		for i, c := range cols {
			if i > 0 {
				sb.WriteString("  ")
			}
			sb.WriteString(padCell(c.cells[r], widths[i], c.right))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// padCell pads s with spaces to width w, aligning right when requested.
func padCell(s string, w int, right bool) string {
	pad := w - utf8.RuneCountInString(s)
	if pad <= 0 {
		return s
	}
	spaces := strings.Repeat(" ", pad)
	if right {
		return spaces + s
	}
	return s + spaces
}
