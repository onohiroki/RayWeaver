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
// With --best-focus each field is evaluated at its best-focus image plane
// (the spot-RMS-minimizing shift, as in the wavefront command), so a
// field-curved system's defocus-dominated fixed-plane Strehl is replaced by
// the wavefront-quality number.
//
// The pipeline YAML carries a lightweight summary (psf_results). Full
// structured grids go to --yaml / --csv files so the pipe stays small.
//
// Every CLI flag mirrors a psf: YAML field; a flag always wins, and the
// effective values are written back into the output's psf: section so the
// pipeline reflects what was actually computed.
func runPSF(data []byte) {
	fs := flag.NewFlagSet("psf", flag.ExitOnError)
	glassDir := fs.String("glass-dir", "", "AGF glass catalog directory")
	configFlag := fs.String("config", "", "select config by id (multi-config mode)")
	refSurface := fs.Int("ref-surface", 0, "reference surface ID for wavefront sampling (default: last optical surface)")
	gridSize := fs.Int("psf-grid", 0, "image-plane pixels per side (default 64)")
	halfWidth := fs.Float64("psf-width", 0, "evaluation half-width mm (0 = auto from Airy disk + spot)")
	numRays := fs.Int("num-rays", 0, "pupil grid rays (default 400)")
	fieldsFlag := fs.String("fields", "", "comma-separated field indices to compute (default: all)")
	wlFlag := fs.String("wavelengths", "", "comma-separated wavelengths in mm (default: config wavelengths, else reference wavelength)")
	polFlag := fs.String("polarization", "", "input polarization: RCP (default) | LCP | X | Y | RCP+LCP (unpolarised average)")
	spectralFlag := fs.String("spectral", "", "polychromatic (white) PSF: D65 (default) | FLAT")
	psfWorkers := fs.Int("psf-workers", 0, "Huygens/wavefront parallel workers (default: GOMAXPROCS)")
	maxFreq := fs.Float64("max-freq", 0, "MTF frequency cap in cycles/mm (0 = Nyquist; default psf.mtf_config.max_frequency)")
	yamlOut := fs.String("yaml", "", "write full structured PSF data to FILE (index-suffixed per result)")
	csvOut := fs.String("csv", "", "write gnuplot x,y,intensity map to FILE (index-suffixed per result)")
	bestFocus := fs.Bool("best-focus", false, "evaluate each field at its best-focus image plane (removes field-curvature defocus)")
	fs.String("converge-check", "", "label sampling convergence by re-evaluating each result at a higher ray count (default: on; true|false)")
	convergeTol := fs.Float64("converge-tol", 0, "relative Strehl change threshold for convergence (default 0.10)")
	fs.Parse(os.Args[2:])

	input := parseYAML[types.Input](data)
	setReferenceWavelength(input.Chief)
	if input.Chief == nil {
		errOut("Error: 'chief' section is required (for fields)")
		os.Exit(1)
	}

	gc, _ := loadCatalogs(&input, *glassDir)
	writeBackGlassDir(&input, *glassDir)
	surfaces := configSurfaces(input.Configs, configFlag)
	if len(surfaces) == 0 {
		errOut("Error: no surfaces to process")
		os.Exit(1)
	}
	surface.Precompute(surfaces)
	system := types.System{Surfaces: surfaces, StopSurface: input.Chief.StopSurface}

	fields := chiefFieldDefs(input)
	selected := selectedFieldIndices(fields, input.PSF, *fieldsFlag)
	fields = applySelectedFields(fields, selected)
	if len(fields) == 0 {
		errOut("Error: no fields to compute")
		os.Exit(1)
	}

	// Wavelengths: flag > psf.wavelengths > spectral default grid > selected
	// config wavelengths > the system reference wavelength.
	var wavelengths []float64
	spectralMode := *spectralFlag != "" ||
		(input.PSF != nil && (input.PSF.SpectralCurve != "" || len(input.PSF.SpectralEntries) > 0))
	switch {
	case *wlFlag != "":
		wavelengths = parseFloatList(*wlFlag, "wavelength")
	case input.PSF != nil && len(input.PSF.Wavelengths) > 0:
		wavelengths = input.PSF.Wavelengths
	case spectralMode:
		for wl := 400.0; wl <= 700; wl += 10 {
			wavelengths = append(wavelengths, wl*1e-6)
		}
	case len(configWavelengthValues(input.Configs, configFlag)) > 0:
		wavelengths = configWavelengthValues(input.Configs, configFlag)
	default:
		wavelengths = []float64{effectiveReferenceWavelength(input.Chief)}
	}

	var polLabels []string
	switch {
	case *polFlag != "":
		polLabels = parsePolLabels(*polFlag)
	case input.PSF != nil && input.PSF.Polarization != "":
		polLabels = parsePolLabels(input.PSF.Polarization)
	default:
		polLabels = []string{string(types.PolRCP)}
	}

	var spectralEntries []types.SpectralEntry
	if input.PSF != nil {
		spectralEntries = input.PSF.SpectralEntries
	}
	var spectralCurve string
	if input.PSF != nil && input.PSF.SpectralCurve != "" {
		spectralCurve = input.PSF.SpectralCurve
	}
	if *spectralFlag != "" {
		switch strings.ToUpper(strings.TrimSpace(*spectralFlag)) {
		case "D65":
			spectralCurve = "D65"
		case "FLAT":
			spectralCurve = "FLAT"
		default:
			errOut("Error: invalid --spectral %q (D65 | FLAT)", *spectralFlag)
			os.Exit(1)
		}
	}

	opts := psf.Options{
		ReferenceSurface: *refSurface,
		NumRays:          *numRays,
		GridSize:         *gridSize,
		HalfWidth:        *halfWidth,
		Polarizations:    polLabels,
		Workers:          *psfWorkers,
		SpectralCurve:    spectralCurve,
		SpectralEntries:  spectralEntries,
	}
	if input.PSF != nil {
		opts.ReferenceSurface = intOrYAML(*refSurface, input.PSF.ReferenceSurface)
		opts.GridSize = intOrYAML(*gridSize, input.PSF.GridSize)
		opts.HalfWidth = floatOrYAML(*halfWidth, input.PSF.HalfWidth)
		opts.NumRays = intOrYAML(*numRays, input.PSF.NumRays)
		if opts.Workers <= 0 {
			opts.Workers = input.PSF.Workers
		}
		if opts.GridType == "" {
			opts.GridType = input.PSF.GridType
		}
		if !flagWasSet(fs, "best-focus") {
			opts.BestFocus = input.PSF.BestFocus
		}
		opts.MTFCfg = input.PSF.MTFCfg
		if opts.ConvergeTol <= 0 {
			opts.ConvergeTol = input.PSF.ConvergeTol
		}
	}
	if *bestFocus {
		opts.BestFocus = true
	}
	// Sampling-convergence labelling. The command default is ON; an explicit
	// --converge-check false disables it (CLI wins). `psf.converge_check: false`
	// in the input YAML also disables it; a YAML-absence keeps the default ON.
	cc, ccSet, err := boolFlag(fs, "converge-check")
	if err != nil {
		errOut("Error: %s", err)
		os.Exit(1)
	}
	opts.ConvergeCheck = true
	if input.PSF != nil && !ccSet && input.PSF.ConvergeCheck != nil {
		opts.ConvergeCheck = *input.PSF.ConvergeCheck
	}
	if ccSet {
		opts.ConvergeCheck = cc
	}
	if *convergeTol > 0 {
		opts.ConvergeTol = *convergeTol
	}
	if *maxFreq > 0 {
		if opts.MTFCfg == nil {
			opts.MTFCfg = &types.PSFMTFConfig{}
		}
		opts.MTFCfg.MaxFrequency = *maxFreq
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

	// Write the effective options back into the output's psf: section so the
	// pipeline reflects what was actually computed (flags override YAML).
	writeBackPSF(&input, wavelengths, selected, opts, flagWasSet(fs, "best-focus"))

	output := types.Output{Input: input}
	withOutputMetadata(&output.Input, "psf", subcmdArgs())
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
		wl := r.Wavelength
		if r.SpectralCurve != "" {
			wl = 0
		}
		output.PsfResults = append(output.PsfResults, types.PSFResult{
			FieldIndex:        r.FieldIndex,
			FieldAngle:        r.FieldAngle,
			Wavelength:        wl,
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
			SpectralCurve:     r.SpectralCurve,
			BestFocusShift:    r.BestFocusShift,
			Converged:         convergenceFlag(opts.ConvergeCheck, r.Converged),
			StrehlRelChange:   r.StrehlRelChange,
			CheckRays:         r.CheckRays,
			MTF:               psfMTFSummary(r.MTF),
		})
	}
	writeYAML(&output)
}

// selectedFieldIndices filters the chief fields by --fields (flag wins) or
// psf.fields, returning the kept global indices.
func selectedFieldIndices(fields []types.FieldDef, pc *types.PSFConfig, flagVal string) []int {
	spec := flagVal
	if spec == "" && pc != nil && len(pc.Fields) > 0 {
		spec = intsToCSV(pc.Fields)
	}
	var kept []int
	if spec == "" {
		for i := range fields {
			kept = append(kept, i)
		}
		return kept
	}
	keptSet := make(map[int]bool)
	for _, tok := range strings.Split(spec, ",") {
		idx, err := strconv.Atoi(strings.TrimSpace(tok))
		if err != nil || idx < 0 || idx >= len(fields) {
			errOut("Error: invalid field index %q", tok)
			os.Exit(1)
		}
		if !keptSet[idx] {
			kept = append(kept, idx)
			keptSet[idx] = true
		}
	}
	return kept
}

// applySelectedFields narrows fields to the kept global indices.
func applySelectedFields(fields []types.FieldDef, kept []int) []types.FieldDef {
	out := make([]types.FieldDef, 0, len(kept))
	for _, idx := range kept {
		out = append(out, fields[idx])
	}
	return out
}

// convergenceFlag returns a *bool reporting the convergence verdict, or nil
// when the check was disabled (so `converged` stays absent from the output).
func convergenceFlag(checked, converged bool) *bool {
	if !checked {
		return nil
	}
	return &converged
}

// writeBackPSF stores the effective options into the output psf: section.
func writeBackPSF(input *types.Input, wavelengths []float64, selected []int, opts psf.Options, bestFocusSet bool) {
	if input.PSF == nil {
		input.PSF = &types.PSFConfig{}
	}
	ps := input.PSF
	ps.ReferenceSurface = opts.ReferenceSurface
	ps.GridSize = opts.GridSize
	ps.HalfWidth = opts.HalfWidth
	ps.NumRays = opts.NumRays
	ps.Workers = opts.Workers
	ps.Polarization = strings.Join(opts.Polarizations, ",")
	ps.Wavelengths = wavelengths
	ps.Fields = selected
	ps.SpectralCurve = opts.SpectralCurve
	ps.SpectralEntries = opts.SpectralEntries
	// Only echo best_focus when the flag set it or the YAML requested it
	// (never inject the false default into every output).
	if bestFocusSet || opts.BestFocus {
		ps.BestFocus = opts.BestFocus
	}
	ps.GridType = opts.GridType
	// Convergence labelling defaults to ON for this command, so always reflect
	// the effective state (enables turning it off via --converge-check false).
	ps.ConvergeCheck = &opts.ConvergeCheck
	if opts.ConvergeTol > 0 {
		ps.ConvergeTol = opts.ConvergeTol
	}
	ps.MTFCfg = opts.MTFCfg
}

// psfMTFSummary returns a pipeline-light MTF summary (thresholds +
// user-evaluated frequencies only; the full curves go to --yaml files).
func psfMTFSummary(m *types.PSFMTFSummary) *types.PSFMTFSummary {
	if m == nil {
		return nil
	}
	s := &types.PSFMTFSummary{}
	s.Sagittal.Thresholds = m.Sagittal.Thresholds
	s.Sagittal.Evaluated = m.Sagittal.Evaluated
	s.Tangential.Thresholds = m.Tangential.Thresholds
	s.Tangential.Evaluated = m.Tangential.Evaluated
	return s
}

func intsToCSV(v []int) string {
	parts := make([]string, len(v))
	for i, n := range v {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ",")
}

func parseFloatList(s, what string) []float64 {
	var out []float64
	for _, tok := range strings.Split(s, ",") {
		v, err := strconv.ParseFloat(strings.TrimSpace(tok), 64)
		if err != nil {
			errOut("Error: invalid %s %q", what, tok)
			os.Exit(1)
		}
		out = append(out, v)
	}
	return out
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
	FieldIndex        int                   `yaml:"field_index"`
	FieldAngle        float64               `yaml:"field_angle"`
	Wavelength        float64               `yaml:"wavelength,omitempty"`
	Polarization      string                `yaml:"polarization"`
	SpectralCurve     string                `yaml:"spectral_curve,omitempty"`
	StrehlRatio       float64               `yaml:"strehl_ratio"`
	FWHM              [2]float64            `yaml:"fwhm"`
	Centroid          [2]float64            `yaml:"centroid"`
	Peak              [3]float64            `yaml:"peak"`
	EncircledEnergy50 float64               `yaml:"encircled_energy_50"`
	AiryRadius        float64               `yaml:"airy_radius"`
	ImageNA           float64               `yaml:"image_na"`
	SpotRMS           float64               `yaml:"spot_rms"`
	BestFocusShift    float64               `yaml:"best_focus_shift_mm,omitempty"`
	Transmittance     float64               `yaml:"transmittance,omitempty"`
	RawEnergy         float64               `yaml:"raw_energy,omitempty"`
	Grid              psfYAMLGrid           `yaml:"grid"`
	Intensity         []float64             `yaml:"intensity"`
	ExReal            []float64             `yaml:"ex_real,omitempty"`
	ExImag            []float64             `yaml:"ex_imag,omitempty"`
	EyReal            []float64             `yaml:"ey_real,omitempty"`
	EyImag            []float64             `yaml:"ey_imag,omitempty"`
	EzReal            []float64             `yaml:"ez_real,omitempty"`
	EzImag            []float64             `yaml:"ez_imag,omitempty"`
	EncircledEnergy   psfYAMLEnclosure      `yaml:"encircled_energy"`
	Wavefront         psfYAMLWavefront      `yaml:"wavefront"`
	Samples           psfYAMLSamples        `yaml:"samples"`
	MTF               *types.PSFMTFSummary  `yaml:"mtf,omitempty"`
	Contributions     []psfYAMLContribution `yaml:"wavelength_contributions,omitempty"`
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

type psfYAMLContribution struct {
	Wavelength     float64              `yaml:"wavelength"`
	SpectralWeight float64              `yaml:"spectral_weight"`
	Transmittance  float64              `yaml:"transmittance,omitempty"`
	PSFEnergy      float64              `yaml:"psf_energy"`
	Centroid       [2]float64           `yaml:"centroid"`
	Grid           psfYAMLGrid          `yaml:"grid"`
	Intensity      []float64            `yaml:"intensity"`
	MTF            *types.PSFMTFSummary `yaml:"mtf,omitempty"`
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

	wl := r.Wavelength
	if r.SpectralCurve != "" {
		wl = 0
	}
	out := psfYAMLFile{
		FieldIndex:        r.FieldIndex,
		FieldAngle:        r.FieldAngle,
		Wavelength:        wl,
		Polarization:      r.Polarization,
		SpectralCurve:     r.SpectralCurve,
		StrehlRatio:       r.Strehl,
		FWHM:              [2]float64{r.FWHMX, r.FWHMY},
		Centroid:          [2]float64{r.CentroidX, r.CentroidY},
		Peak:              [3]float64{r.PeakValue, r.PeakX, r.PeakY},
		EncircledEnergy50: r.Encircled50,
		AiryRadius:        r.AiryRadius,
		ImageNA:           r.ImageNA,
		SpotRMS:           r.SpotRMS,
		BestFocusShift:    r.BestFocusShift,
		Transmittance:     r.Transmittance,
		RawEnergy:         r.RawIntensitySum,
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
		MTF:           r.MTF,
		Contributions: contributionsToYAML(r.Contributions),
	}
	data, err := yaml.Marshal(&out)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func contributionsToYAML(cs []psf.WavelengthContribution) []psfYAMLContribution {
	if len(cs) == 0 {
		return nil
	}
	out := make([]psfYAMLContribution, len(cs))
	for i, c := range cs {
		out[i] = psfYAMLContribution{
			Wavelength:     c.Wavelength,
			SpectralWeight: c.SpectralWeight,
			Transmittance:  c.Transmittance,
			PSFEnergy:      c.PSFEnergy,
			Centroid:       [2]float64{c.CentroidX, c.CentroidY},
			Grid: psfYAMLGrid{
				NX: c.Grid.Spec.NX, NY: c.Grid.Spec.NY,
				X0: c.Grid.Spec.X0, Y0: c.Grid.Spec.Y0,
				DX: c.Grid.Spec.DX, DY: c.Grid.Spec.DY,
			},
			Intensity: c.Grid.Intensity,
			MTF:       c.MTF,
		}
	}
	return out
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
