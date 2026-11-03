package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/hiroki/rayweaver/internal/chief"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// listFields renders the field-of-view definitions with both input (YAML) and
// computed (real-ray chief trace) values. The chief trace is run internally
// so computed image heights and recovered angles are always available.
func listFields(input types.Input, format string, glassDir, configFlag string) {
	if input.Chief == nil {
		switch format {
		case "yaml", "json":
			emitEmptyList(format, "fields")
		default:
			fmt.Println("Fields:")
			fmt.Println("(no fields)")
		}
		return
	}

	// Resolve field definitions exactly as runChief does: the --config
	// selected position supplies its own fields, then configs[0], then the
	// top-level chief section.
	fields := resolveConfigFieldDefs(input, &configFlag)
	if len(fields) == 0 {
		switch format {
		case "yaml", "json":
			emitEmptyList(format, "fields")
		default:
			fmt.Println("Fields:")
			fmt.Println("(no fields)")
		}
		return
	}

	// Run chief trace to get computed values against the same configuration
	// and glass catalog the other targets use.
	gc, _ := openGlass(&input, glassDir)
	surfaces := configSurfaces(input.Configs, &configFlag)
	surface.Precompute(surfaces)

	wavelength := setReferenceWavelength(input.Chief)

	selectedSys := input.System
	selectedSys.Surfaces = surfaces
	selectedSys.StopSurface = input.Chief.StopSurface

	pol := polarization(input)

	results := chief.DetermineChiefRaysGridMode(
		selectedSys,
		fields,
		input.Chief.ReferenceSurface,
		input.Chief.NumRays,
		gc,
		pol,
		wavelength,
		false,
		input.Chief.GridType,
		input.Chief.PassThrough,
		nil,
		nil,
		input.Chief.PupilModel,
		input.Chief.NumRings, input.Chief.NumSpokes,
		chiefRayDefinition(input),
	)
	warnFieldResolution("", results)

	// Build rows.
	rows := make([]FieldListRow, len(fields))
	for i, fd := range fields {
		row := FieldListRow{Index: i + 1}

		// Direction.
		dir := fd.Direction
		if len(dir) == 0 {
			dir = []float64{0, 1}
		}
		row.Direction = dir

		// Classify type and set input values.
		switch {
		case math.Abs(fd.ImageHeight) > 1e-12:
			row.Type = "image_height"
			v := fd.ImageHeight
			row.InputImageHeight = &v
		case math.Abs(fd.Height) > 1e-12:
			row.Type = "height"
			v := fd.Height
			row.InputHeight = &v
			if fd.ObjectZ != 0 {
				oz := fd.ObjectZ
				row.InputObjectZ = &oz
			}
		default:
			row.Type = "angle"
			v := fd.Angle
			row.InputAngle = &v
		}

		// Computed values from chief trace.
		if i < len(results) {
			r := results[i]
			if row.Type != "height" {
				// FieldAngle is not set for height-mode fields.
				ca := r.FieldAngle
				row.ComputedAngle = &ca
			}
			ih := r.ImageHeight.Y
			row.ComputedImageHeight = &ih
		}

		rows[i] = row
	}

	switch format {
	case "yaml", "json":
		emitListOutput(format, fieldsListOutput{Fields: rows})
	case "csv":
		fmt.Println("Fields:")
		fmt.Println("index,type,input_angle,computed_angle,input_image_height,computed_image_height,input_height,input_object_z,direction")
		for _, r := range rows {
			cells := []string{
				strconv.Itoa(r.Index),
				r.Type,
				optionalFloatCSV(r.InputAngle),
				optionalFloatCSV(r.ComputedAngle),
				optionalFloatCSV(r.InputImageHeight),
				optionalFloatCSV(r.ComputedImageHeight),
				optionalFloatCSV(r.InputHeight),
				optionalFloatCSV(r.InputObjectZ),
				fmt.Sprintf("%v", r.Direction),
			}
			fmt.Println(strings.Join(quoteCSV(cells), ","))
		}
	default: // "table"
		fmt.Println("Fields:")
		cols := []tableColumn{
			{header: "#", right: true},
			{header: "Type"},
			{header: "Angle[deg]", right: true},
			{header: "Image Height[mm]", right: true},
			{header: "Height[mm]", right: true},
			{header: "Object Z[mm]", right: true},
			{header: "Direction"},
		}
		for _, r := range rows {
			cols[0].cells = append(cols[0].cells, strconv.Itoa(r.Index))
			cols[1].cells = append(cols[1].cells, r.Type)
			// Show input value first, computed value in parentheses when different.
			angleCell := formatFieldCell(r.InputAngle, r.ComputedAngle)
			ihCell := formatFieldCell(r.InputImageHeight, r.ComputedImageHeight)
			hCell := formatOptionalFloat(r.InputHeight)
			ozCell := formatOptionalFloat(r.InputObjectZ)
			cols[2].cells = append(cols[2].cells, angleCell)
			cols[3].cells = append(cols[3].cells, ihCell)
			cols[4].cells = append(cols[4].cells, hCell)
			cols[5].cells = append(cols[5].cells, ozCell)
			cols[6].cells = append(cols[6].cells, fmt.Sprintf("%v", r.Direction))
		}
		fmt.Print(renderTable(cols))
	}
}

// formatFieldCell renders a field value showing input and computed when both
// present. When they match (within tolerance), only one value is shown.
func formatFieldCell(input, computed *float64) string {
	if input == nil && computed == nil {
		return "-"
	}
	if input == nil {
		return formatTableFloat(*computed)
	}
	if computed == nil {
		return formatTableFloat(*input)
	}
	if math.Abs(*input-*computed) < 1e-9 {
		return formatTableFloat(*input)
	}
	return fmt.Sprintf("%s (%s)", formatTableFloat(*input), formatTableFloat(*computed))
}

// formatOptionalFloat formats a pointer float for table cells.
func formatOptionalFloat(v *float64) string {
	if v == nil {
		return "-"
	}
	return formatTableFloat(*v)
}

// optionalFloatCSV formats a pointer float for CSV cells.
func optionalFloatCSV(v *float64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatFloat(*v, 'g', -1, 64)
}
