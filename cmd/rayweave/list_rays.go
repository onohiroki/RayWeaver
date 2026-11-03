package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/hiroki/rayweaver/internal/types"
)

// listRays renders the ray trace results from the Output's results[] section.
// When summaryOnly is false (default), per-surface detail is shown whenever
// the results contain surface data.
func listRays(output types.Output, summaryOnly bool, format string) {
	// Filter out empty result slots (trace pre-allocates len(rayList) zero-value
	// entries then appends the real results).
	results := make([]types.RayResult, 0, len(output.Results))
	for _, r := range output.Results {
		if r.ID != "" || r.OPLTotal != 0 || len(r.Surfaces) > 0 {
			results = append(results, r)
		}
	}
	if len(results) == 0 {
		switch format {
		case "yaml", "json":
			emitEmptyList(format, "summary")
		default:
			fmt.Println("Rays: (no ray results)")
		}
		return
	}

	summary := buildRaySummaryRows(results)
	var details []RayDetailRow
	if !summaryOnly {
		details = buildRayDetailRows(results)
	}

	switch format {
	case "yaml", "json":
		out := raysListOutput{Summary: summary, Details: details}
		emitListOutput(format, out)
	case "csv":
		fmt.Println("Ray Summary:")
		fmt.Println("id,wavelength,opl_total,intensity_s,intensity_p,tcum_s,tcum_p,surfaces,transmitted,missed,error")
		for _, r := range summary {
			cells := []string{
				r.ID,
				strconv.FormatFloat(r.Wavelength, 'g', -1, 64),
				strconv.FormatFloat(r.OPLTotal, 'g', -1, 64),
				strconv.FormatFloat(r.IntensityS, 'g', -1, 64),
				strconv.FormatFloat(r.IntensityP, 'g', -1, 64),
				strconv.FormatFloat(r.TcumS, 'g', -1, 64),
				strconv.FormatFloat(r.TcumP, 'g', -1, 64),
				strconv.Itoa(r.Surfaces),
				strconv.Itoa(r.Transmitted),
				strconv.Itoa(r.Missed),
				r.Error,
			}
			fmt.Println(strings.Join(quoteCSV(cells), ","))
		}
		if len(details) > 0 {
			fmt.Println()
			fmt.Println("Ray Detail:")
			fmt.Println("ray_id,surface_id,position_x,position_y,position_z,direction_x,direction_y,direction_z,interaction,thickness,opl,intensity_s,intensity_p,intensity_rs,intensity_rp,jones_re_ex,jones_im_ex,jones_re_ey,jones_im_ey,tcum_s,tcum_p,angle_of_incidence,n1,n2,rs,rp,ts,tp,coating_rs,coating_rp,coating_ts,coating_tp,error_code")
			for _, r := range details {
				cells := []string{
					r.RayID,
					strconv.Itoa(r.SurfaceID),
					strconv.FormatFloat(r.Position[0], 'g', -1, 64),
					strconv.FormatFloat(r.Position[1], 'g', -1, 64),
					strconv.FormatFloat(r.Position[2], 'g', -1, 64),
					strconv.FormatFloat(r.Direction[0], 'g', -1, 64),
					strconv.FormatFloat(r.Direction[1], 'g', -1, 64),
					strconv.FormatFloat(r.Direction[2], 'g', -1, 64),
					r.Interaction,
					strconv.FormatFloat(r.Thickness, 'g', -1, 64),
					strconv.FormatFloat(r.OPL, 'g', -1, 64),
					strconv.FormatFloat(r.IntensityS, 'g', -1, 64),
					strconv.FormatFloat(r.IntensityP, 'g', -1, 64),
				}
				cells = appendOptionalFloat(cells, r.IntensityRs)
				cells = appendOptionalFloat(cells, r.IntensityRp)
				cells = append(cells,
					strconv.FormatFloat(real(r.Jones.Ex), 'g', -1, 64),
					strconv.FormatFloat(imag(r.Jones.Ex), 'g', -1, 64),
					strconv.FormatFloat(real(r.Jones.Ey), 'g', -1, 64),
					strconv.FormatFloat(imag(r.Jones.Ey), 'g', -1, 64),
					strconv.FormatFloat(r.TcumS, 'g', -1, 64),
					strconv.FormatFloat(r.TcumP, 'g', -1, 64),
				)
				cells = appendOptionalFloat(cells, r.AngleOfIncidence)
				cells = appendOptionalFloat(cells, r.N1)
				cells = appendOptionalFloat(cells, r.N2)
				cells = appendOptionalFloat(cells, r.Rs)
				cells = appendOptionalFloat(cells, r.Rp)
				cells = appendOptionalFloat(cells, r.Ts)
				cells = appendOptionalFloat(cells, r.Tp)
				cells = appendOptionalFloat(cells, r.CoatingRs)
				cells = appendOptionalFloat(cells, r.CoatingRp)
				cells = appendOptionalFloat(cells, r.CoatingTs)
				cells = appendOptionalFloat(cells, r.CoatingTp)
				cells = append(cells, r.ErrorCode)
				fmt.Println(strings.Join(quoteCSV(cells), ","))
			}
		}
	default: // "table"
		fmt.Println("Ray Summary:")
		cols := []tableColumn{
			{header: "ID"},
			{header: "λ[mm]"},
			{header: "OPL[mm]", right: true},
			{header: "Is", right: true},
			{header: "Ip", right: true},
			{header: "Tcum s", right: true},
			{header: "Tcum p", right: true},
			{header: "Surf", right: true},
			{header: "Tx", right: true},
			{header: "Miss", right: true},
			{header: "Error"},
		}
		for _, r := range summary {
			cols[0].cells = append(cols[0].cells, r.ID)
			cols[1].cells = append(cols[1].cells, formatTableFloat(r.Wavelength))
			cols[2].cells = append(cols[2].cells, formatTableFloat(r.OPLTotal))
			cols[3].cells = append(cols[3].cells, formatTableFloat(r.IntensityS))
			cols[4].cells = append(cols[4].cells, formatTableFloat(r.IntensityP))
			cols[5].cells = append(cols[5].cells, formatTableFloat(r.TcumS))
			cols[6].cells = append(cols[6].cells, formatTableFloat(r.TcumP))
			cols[7].cells = append(cols[7].cells, strconv.Itoa(r.Surfaces))
			cols[8].cells = append(cols[8].cells, strconv.Itoa(r.Transmitted))
			cols[9].cells = append(cols[9].cells, strconv.Itoa(r.Missed))
			cols[10].cells = append(cols[10].cells, r.Error)
		}
		fmt.Print(renderTable(cols))

		if len(details) > 0 {
			seen := map[string]bool{}
			for _, r := range summary {
				if seen[r.ID] {
					continue
				}
				seen[r.ID] = true
				var rayDetails []RayDetailRow
				for _, d := range details {
					if d.RayID == r.ID {
						rayDetails = append(rayDetails, d)
					}
				}
				fmt.Printf("\nDetail — %s:\n", r.ID)
				printRayDetailTable(rayDetails)
			}
		}
	}
}

// buildRaySummaryRows builds the summary rows from RayResult slice.
func buildRaySummaryRows(results []types.RayResult) []RaySummaryRow {
	rows := make([]RaySummaryRow, 0, len(results))
	for _, r := range results {
		row := RaySummaryRow{
			ID:         r.ID,
			Wavelength: r.Wavelength,
			OPLTotal:   r.OPLTotal,
			IntensityS: r.IntensityS,
			IntensityP: r.IntensityP,
			Surfaces:   len(r.Surfaces),
			Error:      r.Error,
		}
		// Final cumulative transmittance: multiply each surface's single-surface
		// intensity transmittance (intensity 1 at the object plane; surface 0 and
		// ideal mirrors contribute 1 and are skipped).
		var cumS, cumP float64 = 1, 1
		for _, s := range r.Surfaces {
			switch s.Interaction {
			case types.Transmit:
				row.Transmitted++
			case types.Missed:
				row.Missed++
			}
			if s.SurfaceID != 0 && s.IntensityS > 0 && s.IntensityS <= 1 {
				cumS *= s.IntensityS
				cumP *= s.IntensityP
			}
		}
		row.TcumS = cumS
		row.TcumP = cumP
		rows = append(rows, row)
	}
	return rows
}

// buildRayDetailRows builds the flattened per-surface detail rows.
func buildRayDetailRows(results []types.RayResult) []RayDetailRow {
	var rows []RayDetailRow
	for _, r := range results {
		rayID := r.ID
		if rayID == "" {
			rayID = "unnamed"
		}
		// Cumulative transmittance from the entrance (incident intensity = 1):
		// multiply each surface's single-surface intensity transmittance.
		var cumS, cumP float64 = 1, 1
		for _, s := range r.Surfaces {
			row := RayDetailRow{
				RayID:       rayID,
				SurfaceID:   s.SurfaceID,
				Position:    [3]float64{s.Position.X, s.Position.Y, s.Position.Z},
				Direction:   [3]float64{s.Direction.X, s.Direction.Y, s.Direction.Z},
				Interaction: string(s.Interaction),
				Thickness:   s.Thickness,
				OPL:         s.OPL,
				IntensityS:  s.IntensityS,
				IntensityP:  s.IntensityP,
				Jones:       s.Jones,
				ErrorCode:   s.ErrorCode,
			}
			// Surface 0 (object plane) and ideal fold mirrors have intensity 1
			// (no transmittance to accumulate); only accumulate physical
			// refractive surfaces.
			if s.SurfaceID != 0 && s.IntensityS > 0 && s.IntensityS <= 1 {
				cumS *= s.IntensityS
				cumP *= s.IntensityP
			}
			row.TcumS = cumS
			row.TcumP = cumP
			if s.IntensityRs != nil {
				v := *s.IntensityRs
				row.IntensityRs = &v
			}
			if s.IntensityRp != nil {
				v := *s.IntensityRp
				row.IntensityRp = &v
			}
			if s.AngleOfIncidence != nil {
				v := *s.AngleOfIncidence
				row.AngleOfIncidence = &v
			}
			if s.N1 != nil {
				v := *s.N1
				row.N1 = &v
			}
			if s.N2 != nil {
				v := *s.N2
				row.N2 = &v
			}
			if s.Rs != nil {
				v := *s.Rs
				row.Rs = &v
			}
			if s.Rp != nil {
				v := *s.Rp
				row.Rp = &v
			}
			if s.Ts != nil {
				v := *s.Ts
				row.Ts = &v
			}
			if s.Tp != nil {
				v := *s.Tp
				row.Tp = &v
			}
			if s.CoatingRs != nil {
				v := *s.CoatingRs
				row.CoatingRs = &v
			}
			if s.CoatingRp != nil {
				v := *s.CoatingRp
				row.CoatingRp = &v
			}
			if s.CoatingTs != nil {
				v := *s.CoatingTs
				row.CoatingTs = &v
			}
			if s.CoatingTp != nil {
				v := *s.CoatingTp
				row.CoatingTp = &v
			}
			rows = append(rows, row)
		}
	}
	return rows
}

// printRayDetailTable renders one ray's per-surface detail as a table.
func printRayDetailTable(rayDetails []RayDetailRow) {
	if len(rayDetails) == 0 {
		return
	}
	detailCols := []tableColumn{
		{header: "Surf", right: true},
		{header: "x", right: true},
		{header: "y", right: true},
		{header: "z", right: true},
		{header: "dx", right: true},
		{header: "dy", right: true},
		{header: "dz", right: true},
		{header: "Interact"},
		{header: "OPL[mm]", right: true},
		{header: "Is", right: true},
		{header: "Ip", right: true},
		{header: "Jones"},
		{header: "Tcum s", right: true},
		{header: "Tcum p", right: true},
	}
	hasDetail := false
	for i := range rayDetails {
		if rayDetails[i].AngleOfIncidence != nil || rayDetails[i].N1 != nil {
			hasDetail = true
			break
		}
	}
	if hasDetail {
		detailCols = append(detailCols,
			tableColumn{header: "Irs", right: true},
			tableColumn{header: "Irp", right: true},
			tableColumn{header: "θ[°]", right: true},
			tableColumn{header: "n1", right: true},
			tableColumn{header: "n2", right: true},
			tableColumn{header: "Rs", right: true},
			tableColumn{header: "Rp", right: true},
			tableColumn{header: "Ts", right: true},
			tableColumn{header: "Tp", right: true},
		)
	}
	hasErr := false
	for i := range rayDetails {
		if rayDetails[i].ErrorCode != "" {
			hasErr = true
			break
		}
	}
	if hasErr {
		detailCols = append(detailCols, tableColumn{header: "Err"})
	}
	for _, d := range rayDetails {
		detailCols[0].cells = append(detailCols[0].cells, strconv.Itoa(d.SurfaceID))
		detailCols[1].cells = append(detailCols[1].cells, formatTableFloat(d.Position[0]))
		detailCols[2].cells = append(detailCols[2].cells, formatTableFloat(d.Position[1]))
		detailCols[3].cells = append(detailCols[3].cells, formatTableFloat(d.Position[2]))
		detailCols[4].cells = append(detailCols[4].cells, formatTableFloat(d.Direction[0]))
		detailCols[5].cells = append(detailCols[5].cells, formatTableFloat(d.Direction[1]))
		detailCols[6].cells = append(detailCols[6].cells, formatTableFloat(d.Direction[2]))
		detailCols[7].cells = append(detailCols[7].cells, d.Interaction)
		detailCols[8].cells = append(detailCols[8].cells, formatTableFloat(d.OPL))
		detailCols[9].cells = append(detailCols[9].cells, formatTableFloat(d.IntensityS))
		detailCols[10].cells = append(detailCols[10].cells, formatTableFloat(d.IntensityP))
		detailCols[11].cells = append(detailCols[11].cells, formatJones(d.Jones))
		detailCols[12].cells = append(detailCols[12].cells, formatTableFloat(d.TcumS))
		detailCols[13].cells = append(detailCols[13].cells, formatTableFloat(d.TcumP))
		if hasDetail {
			detailCols[14].cells = appendOptionalFloatCells(detailCols[14].cells, d.IntensityRs)
			detailCols[15].cells = appendOptionalFloatCells(detailCols[15].cells, d.IntensityRp)
			detailCols[16].cells = appendOptionalFloatCells(detailCols[16].cells, d.AngleOfIncidence)
			detailCols[17].cells = appendOptionalFloatCells(detailCols[17].cells, d.N1)
			detailCols[18].cells = appendOptionalFloatCells(detailCols[18].cells, d.N2)
			detailCols[19].cells = appendOptionalFloatCells(detailCols[19].cells, d.Rs)
			detailCols[20].cells = appendOptionalFloatCells(detailCols[20].cells, d.Rp)
			detailCols[21].cells = appendOptionalFloatCells(detailCols[21].cells, d.Ts)
			detailCols[22].cells = appendOptionalFloatCells(detailCols[22].cells, d.Tp)
		}
		if hasErr {
			idx := 14
			if hasDetail {
				idx = 23
			}
			detailCols[idx].cells = append(detailCols[idx].cells, d.ErrorCode)
		}
	}
	fmt.Print(renderTable(detailCols))
}

// formatJones renders a Jones vector as two complex numbers "reEx±imExi
// reEy±imEyi" (matching the YAML component order [ReEx, ImEx, ReEy, ImEy]).
func formatJones(j types.JonesVector) string {
	return formatComplexPart(real(j.Ex), imag(j.Ex)) + " " + formatComplexPart(real(j.Ey), imag(j.Ey))
}

// formatComplexPart renders "re±imi" with a sign-aware imaginary term.
func formatComplexPart(re, im float64) string {
	sign := "+"
	if im < 0 {
		sign = "-"
		im = -im
	}
	return formatTableFloat(re) + sign + formatTableFloat(im) + "i"
}

// appendOptionalFloat appends a float cell (formatted or "-") to a CSV cell slice.
func appendOptionalFloat(cells []string, v *float64) []string {
	if v != nil {
		return append(cells, strconv.FormatFloat(*v, 'g', -1, 64))
	}
	return append(cells, "")
}

// appendOptionalFloatCells appends a float cell to table column cells.
func appendOptionalFloatCells(cells []string, v *float64) []string {
	if v != nil {
		return append(cells, formatTableFloat(*v))
	}
	return append(cells, "-")
}
