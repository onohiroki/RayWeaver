package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
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
// the surface table plus, only when present, asphere coefficients and
// cross-config thickness differences.
type surfacesListOutput struct {
	Surfaces            []SurfaceListRow   `json:"surfaces" yaml:"surfaces"`
	AsphereCoefficients []AsphereCoefRow   `json:"asphere_coefficients,omitempty" yaml:"asphere_coefficients,omitempty"`
	ThicknessDiffs      []ThicknessDiffRow `json:"thickness_differences,omitempty" yaml:"thickness_differences,omitempty"`
}

// ThicknessDiffRow is one (config, surface) record of the cross-config
// thickness comparison: a surface ID whose thickness differs between configs
// together with its value in one config. Structured output stores these
// self-describing records; table/csv render them as a matrix (rows = configs,
// columns = differing surfaces).
type ThicknessDiffRow struct {
	ConfigIndex int     `json:"config_index" yaml:"config_index"`
	ConfigName  string  `json:"config_name" yaml:"config_name"`
	SurfaceID   int     `json:"surface_id" yaml:"surface_id"`
	Thickness   float64 `json:"thickness" yaml:"thickness"`
}

// GlassIndex is one wavelength's refractive index of a glass.
type GlassIndex struct {
	WavelengthNM float64 `json:"wavelength_nm" yaml:"wavelength_nm"`
	N            float64 `json:"n" yaml:"n"`
}

// GlassListRow is one row of the `list glasses` table. Type is the dispersion
// category (formula name for catalog glasses, "model", "constant", or
// "tabulated"); empty for an unresolved key. ND/VD are the d-line index and
// Abbe number (stored values when present, computed from the dispersion data
// otherwise; zero when unavailable — a constant-index glass has no Abbe
// number). Indices holds n at every collected wavelength (longest first).
type GlassListRow struct {
	Name         string       `json:"name" yaml:"name"`
	Manufacturer string       `json:"manufacturer,omitempty" yaml:"manufacturer,omitempty"`
	Type         string       `json:"type,omitempty" yaml:"type,omitempty"`
	ND           float64      `json:"nd,omitempty" yaml:"nd,omitempty"`
	VD           float64      `json:"vd,omitempty" yaml:"vd,omitempty"`
	Indices      []GlassIndex `json:"indices,omitempty" yaml:"indices,omitempty"`
}

// glassesListOutput is the structured (yaml/json) shape of `list glasses`.
type glassesListOutput struct {
	Glasses []GlassListRow `json:"glasses" yaml:"glasses"`
}

// runList implements the `list` subcommand: a read-only, human-readable
// listing of the input system's definition data (surfaces today; glasses,
// decenter, reflect planned). It never traces rays and prints formatted
// tables by default, with --yaml/--json/--csv variants for automation.
//
//	rayweave list [--format table|yaml|json|csv] [--config ID]
//	              [--glass-dir DIR] [--curvature] [TARGET...] < input.yaml
func runList(data []byte) {
	args := splitFlagsAndPositional(os.Args[2:])
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	format := fs.String("format", "table", "output format: table | yaml | json | csv")
	configFlag := fs.String("config", "", "select config by id (multi-config mode)")
	glassDir := fs.String("glass-dir", "", "AGF glass catalog directory")
	showCurvature := fs.Bool("curvature", false, "show curvature instead of radius")
	fs.Parse(args.flags)

	switch *format {
	case "table", "yaml", "json", "csv":
	default:
		errOut("Error: unknown --format %q (supported: table, yaml, json, csv)", *format)
		os.Exit(1)
	}

	input := parseYAML[types.Input](data)
	gc, _ := loadCatalogs(&input, *glassDir)
	surfaces := configSurfaces(input.Configs, configFlag)

	targets := args.positional
	if len(targets) == 0 {
		targets = []string{"surfaces", "glasses"}
	}
	printed := map[string]bool{}
	first := true
	for _, target := range targets {
		if printed[target] {
			continue
		}
		printed[target] = true
		if !first && (*format == "table" || *format == "csv") {
			fmt.Println()
		}
		first = false
		switch target {
		case "surfaces":
			listSurfaces(surfaces, gc, *showCurvature, *format, input.Configs, *configFlag != "")
		case "glasses":
			listGlasses(surfaces, input, gc, *format)
		default:
			errOut("Error: unknown list target %q (supported: surfaces, glasses)", target)
			os.Exit(1)
		}
	}
}

// flagAndPositional holds the pre-split command line of the list subcommand.
type flagAndPositional struct {
	flags      []string
	positional []string
}

// splitFlagsAndPositional separates value-taking flags from positional target
// arguments so flags may appear after the targets (the stdlib FlagSet stops
// parsing at the first non-flag argument). Boolean flags consume no value;
// --format/--config/--glass-dir consume the following token. `--flag=value`
// forms are single tokens and need no special casing.
func splitFlagsAndPositional(args []string) flagAndPositional {
	var out flagAndPositional
	valueFlags := map[string]bool{
		"-format": true, "--format": true,
		"-config": true, "--config": true,
		"-glass-dir": true, "--glass-dir": true,
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") && a != "-" {
			out.flags = append(out.flags, a)
			if valueFlags[a] && i+1 < len(args) {
				i++
				out.flags = append(out.flags, args[i])
			}
			continue
		}
		out.positional = append(out.positional, a)
	}
	return out
}

// listSurfaces renders the surface table of one config in the given format.
// Face 0 (the implicit object plane) is excluded. When the config contains
// aspheric surfaces, an "Asphere Coefficients:" section follows the
// "Surfaces:" section; with no aspheres only Surfaces: is printed. When no
// --config was given and multiple configs carry differing thickness on common
// surface IDs, a final "Thickness Differences:" matrix (rows = configs,
// columns = differing surfaces) is appended.
func listSurfaces(surfaces []types.Surface, gc *glass.Catalog, showCurvature bool, format string, configs []types.Config, configSelected bool) {
	rows := buildSurfaceRows(surfaces, gc, showCurvature)
	aspheres := buildAsphereRows(surfaces)
	var thicknessDiffs []ThicknessDiffRow
	if !configSelected {
		thicknessDiffs = buildThicknessDiffRows(configs)
	}

	switch format {
	case "yaml":
		out := surfacesListOutput{Surfaces: rows, ThicknessDiffs: thicknessDiffs}
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
		out := surfacesListOutput{Surfaces: rows, ThicknessDiffs: thicknessDiffs}
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

// listGlasses renders the glasses table in the given format: glasses used by
// the selected config's surfaces first (first-use order), then unresolved
// keys, then the remaining glass_catalog entries (declaration order). The n
// columns cover every wavelength collected from chief.reference_wavelength
// and all configs' wavelengths (deduplicated, longest first).
func listGlasses(surfaces []types.Surface, input types.Input, gc *glass.Catalog, format string) {
	rows := buildGlassRows(surfaces, input, gc)
	wavelengths := collectWavelengths(input)

	switch format {
	case "yaml":
		outData, err := yaml.Marshal(glassesListOutput{Glasses: rows})
		if err != nil {
			errOut("Error marshaling list output: %v", err)
			os.Exit(1)
		}
		os.Stdout.Write(outData)
	case "json":
		outData, err := json.MarshalIndent(glassesListOutput{Glasses: rows}, "", "  ")
		if err != nil {
			errOut("Error marshaling list output: %v", err)
			os.Exit(1)
		}
		os.Stdout.Write(outData)
		fmt.Println()
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
				if k < len(r.Indices) && r.Indices[k].N > 0 {
					cell = strconv.FormatFloat(r.Indices[k].N, 'g', -1, 64)
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
				if k < len(r.Indices) && r.Indices[k].N > 0 {
					nCell = formatTableFloat(r.Indices[k].N)
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
func buildGlassRows(surfaces []types.Surface, input types.Input, gc *glass.Catalog) []GlassListRow {
	wavelengths := collectWavelengths(input)
	seenKeys := map[string]bool{}
	unresolvedSeen := map[string]bool{}
	var rows []GlassListRow

	modelDedupKey := func(g *types.Glass) string {
		return "model:" + strconv.FormatFloat(g.ND, 'g', -1, 64) + ":" + strconv.FormatFloat(g.VD, 'g', -1, 64)
	}

	appendResolved := func(g *types.Glass) {
		var dedupKey string
		switch {
		case g.Type == types.GlassTypeModel:
			dedupKey = modelDedupKey(g)
		default:
			dedupKey = types.ResolveGlassKey(*g)
		}
		if dedupKey == "" || seenKeys[dedupKey] {
			return
		}
		seenKeys[dedupKey] = true

		// For catalog glasses, resolve through the catalog to pick up the
		// full AGF data (dispersion formula, coefficients) when the YAML
		// entry is incomplete (e.g. key-only override).
		resolved := g
		if g.Type == types.GlassTypeCatalog || (g.Type == "" && g.Key != "") {
			if lookup, ok := gc.Lookup(types.ResolveGlassKey(*g)); ok {
				resolved = lookup
			}
		}

		row := GlassListRow{
			Name:         glassDisplayName(resolved),
			Manufacturer: resolved.Manufacturer,
			Type:         glassTypeString(resolved),
		}
		if nd, vd, ok := glass.NDVD(resolved); ok {
			row.ND = nd
			row.VD = vd
		}
		var mat types.Material
		if resolved.Type == types.GlassTypeModel {
			mat = types.Material{ND: resolved.ND, VD: resolved.VD}
		} else {
			mat = types.Material{Key: types.ResolveGlassKey(*resolved)}
		}
		for _, wl := range wavelengths {
			idx := GlassIndex{WavelengthNM: wl * 1e6}
			if n, err := gc.RefractiveIndex(mat, wl); err != nil {
				glass.Warnf("list[glasses]: cannot compute %q at %.2fnm: %v", dedupKey, wl*1e6, err)
			} else {
				idx.N = n
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
						idx.N = n
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
	if input.GlassCatalog != nil {
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
