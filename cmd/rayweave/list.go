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
// Face 0 (the implicit object plane) is excluded.
func listSurfaces(surfaces []types.Surface, gc *glass.Catalog, showCurvature bool, format string) {
	rows := buildSurfaceRows(surfaces, gc, showCurvature)
	switch format {
	case "yaml":
		outData, err := yaml.Marshal(rows)
		if err != nil {
			errOut("Error marshaling list output: %v", err)
			os.Exit(1)
		}
		os.Stdout.Write(outData)
	case "json":
		outData, err := json.MarshalIndent(rows, "", "  ")
		if err != nil {
			errOut("Error marshaling list output: %v", err)
			os.Exit(1)
		}
		os.Stdout.Write(outData)
		fmt.Println()
	case "csv":
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
	default:
		if len(rows) == 0 {
			fmt.Println("(no surfaces)")
			return
		}
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
