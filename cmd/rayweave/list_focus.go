package main

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/hiroki/rayweaver/internal/types"
)

// listFocus renders the piped focus_comparison section (from `focus mtf` /
// `focus psf`): the per-(field, wavelength) comparison across the file /
// focus_plane_all / best_focus plane conventions. The MTF and PSF sections are
// auto-detected; normally only one is present. It never re-computes anything.
func listFocus(output types.Output, format string) {
	fc := output.FocusComparison
	if fc == nil || (fc.MTF == nil && fc.PSF == nil && fc.Spot == nil && fc.ThroughFocus == nil) {
		switch format {
		case "yaml":
			os.Stdout.Write([]byte("focus_comparison: {}\n"))
		case "json":
			fmt.Println(`{"focus_comparison":{}}`)
		default:
			fmt.Println("Focus: (no focus comparison; run 'focus mtf' or 'focus psf' first)")
		}
		return
	}

	switch format {
	case "yaml", "json":
		emitListOutput(format, focusListOutput{FocusComparison: fc})
	case "csv":
		first := true
		if fc.MTF != nil {
			writeFocusMTFCSV(fc.MTF)
			first = false
		}
		if fc.PSF != nil {
			if !first {
				fmt.Println()
			}
			writeFocusPSFCSV(fc.PSF)
			first = false
		}
		if fc.Spot != nil {
			if !first {
				fmt.Println()
			}
			writeFocusSpotCSV(fc.Spot)
			first = false
		}
		if fc.ThroughFocus != nil {
			if !first {
				fmt.Println()
			}
			writeFocusThroughFocusCSV(fc.ThroughFocus)
		}
	default: // "table"
		if fc.MTF != nil {
			writeFocusMTFTable(os.Stdout, fc.MTF)
		}
		if fc.PSF != nil {
			if fc.MTF != nil {
				fmt.Println()
			}
			writeFocusPSFTable(os.Stdout, fc.PSF)
		}
		if fc.Spot != nil {
			if fc.MTF != nil || fc.PSF != nil {
				fmt.Println()
			}
			writeFocusSpotTable(os.Stdout, fc.Spot)
		}
		if fc.ThroughFocus != nil {
			if fc.MTF != nil || fc.PSF != nil || fc.Spot != nil {
				fmt.Println()
			}
			writeFocusThroughFocusTable(os.Stdout, fc.ThroughFocus)
		}
	}
}

// writeFocusMTFCSV flattens the MTF comparison to one row per (row, plane),
// with one sagittal/tangential column pair per reported frequency.
func writeFocusMTFCSV(c *types.FocusMTFComparison) {
	fmt.Println("Focus Comparison (mtf):")
	header := []string{
		"field_index", "field_angle", "wavelength", "plane",
		"strehl", "best_focus_shift_mm",
	}
	for _, f := range c.Frequencies {
		header = append(header,
			fmt.Sprintf("mtf_sagittal_%g", f), fmt.Sprintf("mtf_tangential_%g", f))
	}
	fmt.Println(strings.Join(quoteCSV(header), ","))
	for _, row := range c.Rows {
		for _, e := range focusMTFPlaneEntries(row) {
			if e.plane == nil {
				continue
			}
			cells := []string{
				strconv.Itoa(row.FieldIndex),
				strconv.FormatFloat(row.FieldAngle, 'g', -1, 64),
				strconv.FormatFloat(row.Wavelength, 'g', -1, 64),
				e.name,
				strconv.FormatFloat(e.plane.Strehl, 'g', -1, 64),
				strconv.FormatFloat(e.plane.BestFocusShiftMM, 'g', -1, 64),
			}
			for i := range c.Frequencies {
				sag, tan := math.NaN(), math.NaN()
				if i < len(e.plane.Sagittal) {
					sag = e.plane.Sagittal[i]
				}
				if i < len(e.plane.Tangential) {
					tan = e.plane.Tangential[i]
				}
				cells = append(cells,
					strconv.FormatFloat(sag, 'g', -1, 64),
					strconv.FormatFloat(tan, 'g', -1, 64))
			}
			fmt.Println(strings.Join(quoteCSV(cells), ","))
		}
	}
}

// writeFocusPSFCSV flattens the PSF comparison to one row per (row, plane).
func writeFocusPSFCSV(c *types.FocusPSFComparison) {
	fmt.Println("Focus Comparison (psf):")
	fmt.Println(strings.Join(quoteCSV([]string{
		"field_index", "field_angle", "wavelength", "plane",
		"strehl", "fwhm_x", "fwhm_y", "encircled_energy_50",
		"centroid_x", "centroid_y", "best_focus_shift_mm",
	}), ","))
	for _, row := range c.Rows {
		for _, e := range focusPSFPlaneEntries(row) {
			if e.plane == nil {
				continue
			}
			cells := []string{
				strconv.Itoa(row.FieldIndex),
				strconv.FormatFloat(row.FieldAngle, 'g', -1, 64),
				strconv.FormatFloat(row.Wavelength, 'g', -1, 64),
				e.name,
				strconv.FormatFloat(e.plane.Strehl, 'g', -1, 64),
				strconv.FormatFloat(e.plane.FWHMX, 'g', -1, 64),
				strconv.FormatFloat(e.plane.FWHMY, 'g', -1, 64),
				strconv.FormatFloat(e.plane.EncircledEnergy50, 'g', -1, 64),
				strconv.FormatFloat(e.plane.CentroidX, 'g', -1, 64),
				strconv.FormatFloat(e.plane.CentroidY, 'g', -1, 64),
				strconv.FormatFloat(e.plane.BestFocusShiftMM, 'g', -1, 64),
			}
			fmt.Println(strings.Join(quoteCSV(cells), ","))
		}
	}
}

// writeFocusThroughFocusCSV flattens a through-focus scan to one row per
// (base, row, focus plane).
func writeFocusThroughFocusCSV(c *types.FocusThroughFocusComparison) {
	fmt.Printf("Focus Comparison (through-focus %s):\n", c.Metric)
	header := []string{"base", "base_shift_mm", "field_index", "field_angle", "wavelength", "focus_mm"}
	switch c.Metric {
	case "mtf":
		header = append(header, "strehl")
		for _, f := range c.Frequencies {
			header = append(header, fmt.Sprintf("mtf_sagittal_%g", f), fmt.Sprintf("mtf_tangential_%g", f))
		}
	case "spot":
		header = append(header, "spot_rms", "spot_rms_t", "spot_rms_s", "spot_rms_x", "spot_rms_y", "centroid_x", "centroid_y")
	default:
		header = append(header, "strehl", "fwhm_x", "fwhm_y", "encircled_energy_50", "centroid_x", "centroid_y")
	}
	fmt.Println(strings.Join(quoteCSV(header), ","))
	for _, s := range c.Scans {
		for _, row := range s.Rows {
			for _, p := range row.Points {
				cells := []string{
					s.Base,
					strconv.FormatFloat(s.BaseShiftMM, 'g', -1, 64),
					strconv.Itoa(row.FieldIndex),
					strconv.FormatFloat(row.FieldAngle, 'g', -1, 64),
					strconv.FormatFloat(row.Wavelength, 'g', -1, 64),
					strconv.FormatFloat(p.FocusMM, 'g', -1, 64),
				}
				switch c.Metric {
				case "mtf":
					cells = append(cells, strconv.FormatFloat(p.Strehl, 'g', -1, 64))
					for i := range c.Frequencies {
						sag, tan := math.NaN(), math.NaN()
						if i < len(p.Sagittal) {
							sag = p.Sagittal[i]
						}
						if i < len(p.Tangential) {
							tan = p.Tangential[i]
						}
						cells = append(cells,
							strconv.FormatFloat(sag, 'g', -1, 64),
							strconv.FormatFloat(tan, 'g', -1, 64))
					}
				case "spot":
					cells = append(cells,
						strconv.FormatFloat(p.SpotRMS, 'g', -1, 64),
						strconv.FormatFloat(p.SpotRMST, 'g', -1, 64),
						strconv.FormatFloat(p.SpotRMSS, 'g', -1, 64),
						strconv.FormatFloat(p.SpotRMSX, 'g', -1, 64),
						strconv.FormatFloat(p.SpotRMSY, 'g', -1, 64),
						strconv.FormatFloat(p.CentroidX, 'g', -1, 64),
						strconv.FormatFloat(p.CentroidY, 'g', -1, 64))
				default:
					cells = append(cells,
						strconv.FormatFloat(p.Strehl, 'g', -1, 64),
						strconv.FormatFloat(p.FWHMX, 'g', -1, 64),
						strconv.FormatFloat(p.FWHMY, 'g', -1, 64),
						strconv.FormatFloat(p.EncircledEnergy50, 'g', -1, 64),
						strconv.FormatFloat(p.CentroidX, 'g', -1, 64),
						strconv.FormatFloat(p.CentroidY, 'g', -1, 64))
				}
				fmt.Println(strings.Join(quoteCSV(cells), ","))
			}
		}
	}
}

// writeFocusSpotCSV flattens the spot comparison to one row per (row, plane).
func writeFocusSpotCSV(c *types.FocusSpotComparison) {
	fmt.Println("Focus Comparison (spot):")
	fmt.Println(strings.Join(quoteCSV([]string{
		"field_index", "field_angle", "wavelength", "plane",
		"spot_rms", "spot_rms_t", "spot_rms_s", "spot_rms_x", "spot_rms_y",
		"centroid_x", "centroid_y", "best_focus_shift_mm",
	}), ","))
	for _, row := range c.Rows {
		for _, e := range focusSpotPlaneEntries(row) {
			if e.plane == nil {
				continue
			}
			cells := []string{
				strconv.Itoa(row.FieldIndex),
				strconv.FormatFloat(row.FieldAngle, 'g', -1, 64),
				strconv.FormatFloat(row.Wavelength, 'g', -1, 64),
				e.name,
				strconv.FormatFloat(e.plane.SpotRMS, 'g', -1, 64),
				strconv.FormatFloat(e.plane.SpotRMST, 'g', -1, 64),
				strconv.FormatFloat(e.plane.SpotRMSS, 'g', -1, 64),
				strconv.FormatFloat(e.plane.SpotRMSX, 'g', -1, 64),
				strconv.FormatFloat(e.plane.SpotRMSY, 'g', -1, 64),
				strconv.FormatFloat(e.plane.CentroidX, 'g', -1, 64),
				strconv.FormatFloat(e.plane.CentroidY, 'g', -1, 64),
				strconv.FormatFloat(e.plane.BestFocusShiftMM, 'g', -1, 64),
			}
			fmt.Println(strings.Join(quoteCSV(cells), ","))
		}
	}
}
