package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/hiroki/rayweaver/internal/spectral"
	"github.com/hiroki/rayweaver/internal/types"
)

// listWavelengths renders the selected config's design wavelength table with
// both the declared and the normalised weight, plus the document spectrum.
func listWavelengths(input types.Input, format, configFlag string) {
	idx := 0
	if configFlag != "" {
		i, msg := resolveConfig(input.Configs, configFlag)
		if i < 0 {
			errExitf("Error: %s", msg)
		}
		idx = i
	}
	var cfg *types.Config
	if idx >= 0 && idx < len(input.Configs) {
		cfg = &input.Configs[idx]
	}
	reference := types.DefaultWavelength
	if input.Chief != nil && input.Chief.ReferenceWavelength > 0 {
		reference = input.Chief.ReferenceWavelength
	}

	out := WavelengthListOutput{Reference: reference, Spectrum: input.Spectrum}
	mode := ""
	if input.Chief != nil {
		mode = strings.ToLower(strings.TrimSpace(input.Chief.WavelengthSet))
	}
	if cfg != nil && mode == "" && len(cfg.Wavelengths) > 0 {
		mode = spectral.ModeDesign
	}
	out.Mode = mode
	switch mode {
	case spectral.ModeDesign:
		if cfg != nil {
			out.Resolved = spectral.DesignSamples(cfg.Wavelengths)
		}
	case spectral.ModeSpectral:
		if s, err := spectral.SpectralSamples(input.Spectrum, 0); err == nil {
			out.Resolved = s
		}
	case spectral.ModeCustom:
		if input.Chief != nil {
			if s, err := spectral.CustomSamples(input.Chief.Wavelengths); err == nil {
				out.Resolved = s
			}
		}
	}
	out.ResolvedCount = len(out.Resolved)
	if cfg != nil {
		normalized := spectral.DesignSamples(cfg.Wavelengths)
		excluded := map[float64]bool{}
		for _, v := range spectral.ExcludedDesignValues(cfg.Wavelengths) {
			excluded[v] = true
		}
		positive := false
		for _, w := range cfg.Wavelengths {
			if w.Weight > 0 {
				positive = true
				break
			}
		}
		for _, w := range cfg.Wavelengths {
			raw := w.Weight
			if !positive {
				raw = 1
			}
			row := WavelengthListRow{
				Config:    cfg.ID,
				Value:     w.Value,
				Label:     w.Label,
				RawWeight: raw,
				Included:  w.Value > 0 && !excluded[w.Value],
			}
			for _, s := range normalized {
				if math.Abs(s.Value-w.Value) <= 1e-12 {
					row.Weight = s.Weight
					break
				}
			}
			out.Rows = append(out.Rows, row)
		}
	}

	switch format {
	case "yaml", "json":
		emitListOutput(format, out)
	case "csv":
		fmt.Println("config,value_nm,label,raw_weight,weight,included")
		for _, r := range out.Rows {
			fmt.Println(strings.Join(quoteCSV([]string{
				r.Config,
				strconv.FormatFloat(r.Value*1e6, 'g', -1, 64),
				r.Label,
				strconv.FormatFloat(r.RawWeight, 'g', -1, 64),
				strconv.FormatFloat(r.Weight, 'g', -1, 64),
				strconv.FormatBool(r.Included),
			}), ","))
		}
	default:
		fmt.Printf("Wavelengths (reference %.3f nm):\n", reference*1e6)
		if len(out.Rows) == 0 {
			fmt.Println("(no config wavelengths)")
		} else {
			cols := []tableColumn{
				{header: "Config"},
				{header: "Value[nm]", right: true},
				{header: "Label"},
				{header: "Raw", right: true},
				{header: "Weight", right: true},
				{header: "Included"},
			}
			for _, r := range out.Rows {
				cols[0].cells = append(cols[0].cells, r.Config)
				cols[1].cells = append(cols[1].cells, formatTableFloat(r.Value*1e6))
				label := r.Label
				if label == "" {
					label = "-"
				}
				cols[2].cells = append(cols[2].cells, label)
				cols[3].cells = append(cols[3].cells, formatTableFloat(r.RawWeight))
				cols[4].cells = append(cols[4].cells, formatTableFloat(r.Weight))
				included := "yes"
				if !r.Included {
					included = "no"
				}
				cols[5].cells = append(cols[5].cells, included)
			}
			fmt.Print(renderTable(cols))
		}
		if out.Mode == "" {
			fmt.Println("Set: (none)")
		} else if len(out.Resolved) == 0 {
			fmt.Printf("Set: %s (unresolved)\n", out.Mode)
		} else {
			lo, hi := out.Resolved[0].Value, out.Resolved[0].Value
			for _, s := range out.Resolved[1:] {
				if s.Value < lo {
					lo = s.Value
				}
				if s.Value > hi {
					hi = s.Value
				}
			}
			fmt.Printf("Set: %s, %d samples, %.1f-%.1f nm\n",
				out.Mode, len(out.Resolved), lo*1e6, hi*1e6)
		}
		if input.Spectrum != nil {
			curve := input.Spectrum.Curve
			if curve == "" {
				curve = "D65"
			}
			step := input.Spectrum.Step
			if step <= 0 {
				step = 5
			}
			fmt.Printf("Spectrum: %s, %.0f-%.0f nm, step %.0f nm\n",
				curve, spectrumRange(input.Spectrum)[0], spectrumRange(input.Spectrum)[1], step)
		}
	}
}

func spectrumRange(cfg *types.SpectralConfig) [2]float64 {
	lo, hi := 400.0, 700.0
	if len(cfg.Range) >= 2 && cfg.Range[0] > 0 && cfg.Range[1] > cfg.Range[0] {
		lo, hi = cfg.Range[0], cfg.Range[1]
	}
	return [2]float64{lo, hi}
}
