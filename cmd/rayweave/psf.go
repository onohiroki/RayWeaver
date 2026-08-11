package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/hiroki/rayweaver/internal/psf"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
	"gopkg.in/yaml.v3"
)

// runPSF implements the `psf` subcommand: per-field polarized ray tracing,
// non-uniform wavefront sampling (Delaunay-triangulated reference surface)
// and a direct vector Huygens integration onto the flat image plane.
//
// The pipeline YAML carries a lightweight summary (psf_results). Full
// structured grids go to --yaml / --csv files so the pipe stays small.
func runPSF(data []byte) {
	fs := flag.NewFlagSet("psf", flag.ExitOnError)
	glassDir := fs.String("glass-dir", "", "AGF glass catalog directory")
	configFlag := fs.String("config", "", "select config by id (multi-config mode)")
	refSurface := fs.Int("ref-surface", 0, "reference surface ID for wavefront sampling (default: last optical surface)")
	gridSize := fs.Int("psf-grid", 0, "image-plane pixels per side (default 64)")
	halfWidth := fs.Float64("psf-width", 0, "evaluation half-width mm (0 = auto from Airy disk + spot)")
	numRays := fs.Int("num-rays", 0, "pupil grid rays (default 400)")
	fieldsFlag := fs.String("fields", "", "comma-separated field indices to compute (default: all)")
	wlFlag := fs.String("wavelengths", "", "comma-separated wavelengths in mm (default: chief wavelengths, else 587.56 nm)")
	polFlag := fs.String("polarization", "", "input polarization: RCP (default) | LCP | X | Y | RCP+LCP (unpolarised average)")
	yamlOut := fs.String("yaml", "", "write full structured PSF data to FILE (index-suffixed per result)")
	csvOut := fs.String("csv", "", "write gnuplot x,y,intensity map to FILE (index-suffixed per result)")
	fs.Parse(os.Args[2:])

	input := parseYAML[types.Input](data)
	if input.Chief == nil {
		errOut("Error: 'chief' section is required (for fields)")
		os.Exit(1)
	}

	gc, _ := loadCatalogs(&input, *glassDir)
	surfaces := configSurfaces(input.Configs, configFlag)
	if len(surfaces) == 0 {
		errOut("Error: no surfaces to process")
		os.Exit(1)
	}
	surface.Precompute(surfaces)
	system := types.System{Surfaces: surfaces, StopSurface: input.Chief.StopSurface}

	fields := chiefFieldDefs(input)
	if *fieldsFlag != "" {
		var kept []types.FieldDef
		for _, tok := range strings.Split(*fieldsFlag, ",") {
			idx, err := strconv.Atoi(strings.TrimSpace(tok))
			if err != nil || idx < 0 || idx >= len(fields) {
				errOut("Error: invalid field index %q", tok)
				os.Exit(1)
			}
			kept = append(kept, fields[idx])
		}
		fields = kept
	}
	if len(fields) == 0 {
		errOut("Error: no fields to compute")
		os.Exit(1)
	}

	var wavelengths []float64
	switch {
	case *wlFlag != "":
		for _, tok := range strings.Split(*wlFlag, ",") {
			wl, err := strconv.ParseFloat(strings.TrimSpace(tok), 64)
			if err != nil {
				errOut("Error: invalid wavelength %q", tok)
				os.Exit(1)
			}
			wavelengths = append(wavelengths, wl)
		}
	case len(input.Chief.Wavelengths) > 0:
		wavelengths = input.Chief.Wavelengths
	default:
		wavelengths = []float64{types.DefaultWavelength}
	}

	polLabels := []string{string(types.PolRCP)}
	if *polFlag != "" {
		polLabels = parsePolLabels(*polFlag)
	}

	opts := psf.Options{
		ReferenceSurface: *refSurface,
		NumRays:          *numRays,
		GridSize:         *gridSize,
		HalfWidth:        *halfWidth,
		Polarizations:    polLabels,
	}
	if input.PSF != nil {
		if opts.ReferenceSurface <= 0 {
			opts.ReferenceSurface = input.PSF.ReferenceSurface
		}
		if opts.GridSize <= 0 {
			opts.GridSize = input.PSF.GridSize
		}
		if opts.HalfWidth <= 0 {
			opts.HalfWidth = input.PSF.HalfWidth
		}
		if opts.NumRays <= 0 {
			opts.NumRays = input.PSF.NumRays
		}
		if len(input.PSF.Wavelengths) > 0 && *wlFlag == "" {
			wavelengths = input.PSF.Wavelengths
		}
	}

	results, err := psf.Compute(system, gc, fields, wavelengths, opts)
	if err != nil {
		errOut("Error: %v", err)
		os.Exit(1)
	}
	if len(results) == 0 {
		errOut("Error: no PSF results computed (check fields/wavelengths/reference surface)")
		os.Exit(1)
	}

	output := types.Output{Input: input}
	for i := range results {
		r := results[i]
		outFile := ""
		if *yamlOut != "" {
			file := suffixFile(*yamlOut, i)
			if err := writePSFYAML(file, &r); err != nil {
				errOut("Error writing %s: %v", file, err)
				os.Exit(1)
			}
			outFile = file
		}
		if *csvOut != "" {
			file := suffixFile(*csvOut, i)
			if err := writePSFCSV(file, &r); err != nil {
				errOut("Error writing %s: %v", file, err)
				os.Exit(1)
			}
		}
		output.PsfResults = append(output.PsfResults, types.PSFResult{
			FieldIndex:        r.FieldIndex,
			FieldAngle:        r.FieldAngle,
			Wavelength:        r.Wavelength,
			Polarization:      r.Polarization,
			StrehlRatio:       r.Strehl,
			FWHMX:             r.FWHMX,
			FWHMY:             r.FWHMY,
			CentroidX:         r.CentroidX,
			CentroidY:         r.CentroidY,
			PeakValue:         r.PeakValue,
			PeakX:             r.PeakX,
			PeakY:             r.PeakY,
			EncircledEnergy50: r.Encircled50,
			AiryRadius:        r.AiryRadius,
			GridSize:          r.Grid.Spec.NX,
			Resolution:        r.Grid.Spec.DX,
			TotalRays:         r.Stats.Total,
			ValidRays:         r.Stats.Valid,
			Vignetted:         r.Stats.Missed,
			OutputFile:        outFile,
		})
	}
	writeYAML(&output)
}

// parsePolLabels converts a --polarization flag value into label(s).
func parsePolLabels(s string) []string {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "LCP":
		return []string{string(types.PolLCP)}
	case "X":
		return []string{string(types.PolX)}
	case "Y":
		return []string{string(types.PolY)}
	case "RCP+LCP":
		return []string{string(types.PolRCPLCP)}
	default:
		return []string{string(types.PolRCP)}
	}
}

// suffixFile returns path with "_N" inserted before the extension.
func suffixFile(path string, idx int) string {
	ext := filepath.Ext(path)
	if ext == "" {
		return fmt.Sprintf("%s_%d", path, idx)
	}
	base := strings.TrimSuffix(path, ext)
	return fmt.Sprintf("%s_%d%s", base, idx, ext)
}

// psfYAMLFile is the full structured per-result output written by --yaml.
type psfYAMLFile struct {
	FieldIndex       int       `yaml:"field_index"`
	FieldAngle       float64   `yaml:"field_angle"`
	Wavelength       float64   `yaml:"wavelength"`
	Polarization     string    `yaml:"polarization"`
	StrehlRatio      float64   `yaml:"strehl_ratio"`
	FWHM             [2]float64 `yaml:"fwhm"`
	Centroid         [2]float64 `yaml:"centroid"`
	Peak             [3]float64 `yaml:"peak"`
	EncircledEnergy50 float64  `yaml:"encircled_energy_50"`
	AiryRadius       float64   `yaml:"airy_radius"`
	ImageNA          float64   `yaml:"image_na"`
	SpotRMS          float64   `yaml:"spot_rms"`
	Grid             psfYAMLGrid `yaml:"grid"`
	Intensity        []float64 `yaml:"intensity"`
	ExReal           []float64 `yaml:"ex_real,omitempty"`
	ExImag           []float64 `yaml:"ex_imag,omitempty"`
	EyReal           []float64 `yaml:"ey_real,omitempty"`
	EyImag           []float64 `yaml:"ey_imag,omitempty"`
	EzReal           []float64 `yaml:"ez_real,omitempty"`
	EzImag           []float64 `yaml:"ez_imag,omitempty"`
	EncircledEnergy  psfYAMLEnclosure `yaml:"encircled_energy"`
	Wavefront        psfYAMLWavefront `yaml:"wavefront"`
	Samples          psfYAMLSamples   `yaml:"samples"`
}

type psfYAMLGrid struct {
	NX int     `yaml:"nx"`
	NY int     `yaml:"ny"`
	X0 float64 `yaml:"x0"`
	Y0 float64 `yaml:"y0"`
	DX float64 `yaml:"dx"`
	DY float64 `yaml:"dy"`
}

type psfYAMLEnclosure struct {
	Radius   []float64 `yaml:"radius"`
	Fraction []float64 `yaml:"fraction"`
}

type psfYAMLWavefront struct {
	RMSOPD float64 `yaml:"rms_opd"`
	PVOPD  float64 `yaml:"pv_opd"`
}

type psfYAMLSamples struct {
	Total  int `yaml:"total"`
	Valid  int `yaml:"valid"`
	Missed int `yaml:"missed"`
}

func writePSFYAML(path string, r *psf.Result) error {
	g := r.Grid
	n := len(g.Intensity)
	exR := make([]float64, n)
	exI := make([]float64, n)
	eyR := make([]float64, n)
	eyI := make([]float64, n)
	ezR := make([]float64, n)
	ezI := make([]float64, n)
	for i := range g.Intensity {
		exR[i], exI[i] = real(g.Ex[i]), imag(g.Ex[i])
		eyR[i], eyI[i] = real(g.Ey[i]), imag(g.Ey[i])
		ezR[i], ezI[i] = real(g.Ez[i]), imag(g.Ez[i])
	}

	const eePoints = 64
	half := math.Max(math.Abs(g.Spec.X0), math.Abs(g.Spec.Y0))
	if half <= 0 {
		half = 1
	}
	eeR := make([]float64, eePoints)
	eeF := make([]float64, eePoints)
	cx, cy := g.Centroid()
	for k := 0; k < eePoints; k++ {
		rad := half * float64(k+1) / float64(eePoints)
		eeR[k] = rad
		eeF[k] = g.EncircledEnergy(cx, cy, rad)
	}

	out := psfYAMLFile{
		FieldIndex:        r.FieldIndex,
		FieldAngle:        r.FieldAngle,
		Wavelength:        r.Wavelength,
		Polarization:      r.Polarization,
		StrehlRatio:       r.Strehl,
		FWHM:              [2]float64{r.FWHMX, r.FWHMY},
		Centroid:          [2]float64{r.CentroidX, r.CentroidY},
		Peak:              [3]float64{r.PeakValue, r.PeakX, r.PeakY},
		EncircledEnergy50: r.Encircled50,
		AiryRadius:        r.AiryRadius,
		ImageNA:           r.ImageNA,
		SpotRMS:           r.SpotRMS,
		Grid: psfYAMLGrid{
			NX: g.Spec.NX, NY: g.Spec.NY,
			X0: g.Spec.X0, Y0: g.Spec.Y0,
			DX: g.Spec.DX, DY: g.Spec.DY,
		},
		Intensity: g.Intensity,
		ExReal:    exR, ExImag: exI,
		EyReal: eyR, EyImag: eyI,
		EzReal: ezR, EzImag: ezI,
		EncircledEnergy: psfYAMLEnclosure{Radius: eeR, Fraction: eeF},
		Wavefront: psfYAMLWavefront{
			RMSOPD: r.RMSOPD,
			PVOPD:  r.PVOPD,
		},
		Samples: psfYAMLSamples{
			Total:  r.Stats.Total,
			Valid:  r.Stats.Valid,
			Missed: r.Stats.Missed,
		},
	}
	data, err := yaml.Marshal(&out)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func writePSFCSV(path string, r *psf.Result) error {
	g := r.Grid
	var b strings.Builder
	b.WriteString("x,y,intensity\n")
	// A blank line between rows marks each scan line for gnuplot pm3d.
	for j := 0; j < g.Spec.NY; j++ {
		for i := 0; i < g.Spec.NX; i++ {
			x := g.Spec.X0 + (float64(i)+0.5)*g.Spec.DX
			y := g.Spec.Y0 + (float64(j)+0.5)*g.Spec.DY
			fmt.Fprintf(&b, "%.9g,%.9g,%.9g\n", x, y, g.Intensity[j*g.Spec.NX+i])
		}
		b.WriteString("\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
