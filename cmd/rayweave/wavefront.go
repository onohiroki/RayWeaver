package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/hiroki/rayweaver/internal/psf"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
	"github.com/hiroki/rayweaver/internal/wavefront"
	"gopkg.in/yaml.v3"
)

// runWavefront implements the `wavefront` subcommand: per-field polarized ray
// tracing to the reference surface, an always-computed paraboloid fit, a
// best-fit sphere seeded from it, a stabilized Fringe-Zernike decomposition of
// the residual, and (with --best-focus) a weighted image-plane shift applied to
// the output configs' image-plane decenter.
//
// The pipeline YAML carries a lightweight summary (coefficients only). Full
// wavefront data (scattered samples + interpolated map) go to --yaml/--csv
// files referenced by output_file.
//
// Every CLI flag mirrors a wavefront: YAML field; a flag always wins, and the
// effective values are written back into the output's wavefront: section.
func runWavefront(data []byte) {
	fs := flag.NewFlagSet("wavefront", flag.ExitOnError)
	glassDir := fs.String("glass-dir", "", "AGF glass catalog directory")
	configFlag := fs.String("config", "", "select config by id (multi-config mode)")
	refSurface := fs.Int("ref-surface", 0, "reference surface ID for wavefront sampling (default: last optical surface)")
	numRays := fs.Int("num-rays", 0, "pupil grid rays (default 400)")
	fieldsFlag := fs.String("fields", "", "comma-separated field indices to compute (default: all)")
	wlFlag := fs.String("wavelengths", "", "comma-separated wavelengths in mm (default: chief wavelengths, else 587.56 nm)")
	polFlag := fs.String("polarization", "", "input polarization: RCP (default) | LCP | X | Y | RCP+LCP")
	zernikeOrder := fs.Int("zernike-order", 0, "highest Fringe Zernike index to fit (default 15)")
	wfWorkers := fs.Int("wavefront-workers", 0, "per-field task parallelism (default: GOMAXPROCS)")
	mapGrid := fs.Int("map-grid", 0, "wavefront map resolution per side for --csv (default 64)")
	bestFocus := fs.Bool("best-focus", false, "compute the weighted best image-plane shift and apply it to the configs")
	focusWeight := fs.String("focus-weight", "", "best-focus weighting: uniform (default) | custom")
	focusWeights := fs.String("focus-weights", "", "comma-separated per-field weights when --focus-weight custom")
	shiftedLens := fs.String("output-shifted-lens", "", "write the shifted lens document to FILE")
	yamlOut := fs.String("yaml", "", "write full structured wavefront data to FILE (index-suffixed per result)")
	csvOut := fs.String("csv", "", "write gnuplot x,y,opd wavefront map to FILE (index-suffixed per result)")
	fs.Parse(os.Args[2:])

	input := parseYAML[types.Input](data)
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
	selected := selectedFieldIndices(fields, nil, *fieldsFlag)
	if input.Wavefront != nil && *fieldsFlag == "" && len(input.Wavefront.Fields) > 0 {
		selected = input.Wavefront.Fields
	}
	fields = applySelectedFields(fields, selected)
	if len(fields) == 0 {
		errOut("Error: no fields to compute")
		os.Exit(1)
	}

	var wavelengths []float64
	switch {
	case *wlFlag != "":
		wavelengths = parseFloatList(*wlFlag, "wavelength")
	case input.Wavefront != nil && len(input.Wavefront.Wavelengths) > 0:
		wavelengths = input.Wavefront.Wavelengths
	case len(input.Chief.Wavelengths) > 0:
		wavelengths = input.Chief.Wavelengths
	default:
		wavelengths = []float64{types.DefaultWavelength}
	}

	var polLabels []string
	switch {
	case *polFlag != "":
		polLabels = parsePolLabels(*polFlag)
	case input.Wavefront != nil && input.Wavefront.Polarization != "":
		polLabels = parsePolLabels(input.Wavefront.Polarization)
	default:
		polLabels = []string{string(types.PolRCP)}
	}

	refSurfaceID := intOrYAML(*refSurface, wavefrontSetting(input, func(c *types.WavefrontConfig) int { return c.ReferenceSurface }))
	numRaysVal := intOrYAML(*numRays, wavefrontSetting(input, func(c *types.WavefrontConfig) int { return c.NumRays }))
	workers := intOrYAML(*wfWorkers, wavefrontSetting(input, func(c *types.WavefrontConfig) int { return c.Workers }))
	zOrder := intOrYAML(*zernikeOrder, wavefrontSetting(input, func(c *types.WavefrontConfig) int { return c.ZernikeMaxOrder }))
	mapGridVal := intOrYAML(*mapGrid, wavefrontSetting(input, func(c *types.WavefrontConfig) int { return c.MapGrid }))

	opts := wavefront.Options{
		NumRays:          numRaysVal,
		Workers:          workers,
		ZernikeMaxOrder:  zOrder,
		Polarizations:    polLabels,
	}
	opts.BestFocus = resolveBestFocus(input, fs, *bestFocus, *focusWeight, *focusWeights, len(fields))

	result, err := wavefront.Compute(system, gc, fields, wavelengths, opts)
	if err != nil {
		errOut("Error: %v", err)
		os.Exit(1)
	}
	if len(result.Fields) == 0 {
		errOut("Error: no wavefront results computed (check fields/wavelengths/reference surface)")
		os.Exit(1)
	}

	// Apply the best-focus shift to the target configs' image-plane decenter.
	var shiftMM float64
	if result.BestFocus != nil {
		shiftMM = result.BestFocus.ShiftMM
		applyImagePlaneShift(input.Configs, configFlag, refSurfaceID, result.BestFocus.WeightedFocusZ)
		if *shiftedLens != "" {
			if err := writeShiftedLens(*shiftedLens, &input); err != nil {
				errOut("Error writing %s: %v", *shiftedLens, err)
				os.Exit(1)
			}
		}
	}

	// Write the effective options back into the output's wavefront: section.
	writeBackWavefront(&input, wavelengths, selected, polLabels, refSurfaceID, numRaysVal, workers, zOrder, mapGridVal, *bestFocus, *focusWeight, *focusWeights, *shiftedLens)

	output := types.Output{Input: input}
	withOutputMetadata(&output.Input, "wavefront", subcmdArgs())

	wr := &types.WavefrontResult{}
	bestFocusOut := result.BestFocus
	if bestFocusOut != nil {
		wb := &types.WavefrontBestFocus{
			WeightType: bestFocusOut.WeightType,
		}
		wlRef := types.DefaultWavelength
		if len(wavelengths) > 0 {
			wlRef = wavelengths[0]
		}
		wb.PerField = make([]types.WavefrontFocusPerField, len(bestFocusOut.PerField))
		for i, pf := range bestFocusOut.PerField {
			shift := pf.FocusZ - bestFocusOut.ImagePlaneZ
			wb.PerField[i] = types.WavefrontFocusPerField{
				FieldIndex:       pf.FieldIndex,
				ShiftWavelengths: shift / wlRef,
				ShiftMM:          shift,
				Weight:           pf.Weight,
			}
		}
		wb.WeightedAverage = types.WavefrontFocusAverage{ShiftWavelengths: shiftMM / wlRef, ShiftMM: shiftMM}
		wb.ShiftedLens = types.WavefrontShiftedLens{ShiftMM: shiftMM}
		if *shiftedLens != "" {
			wb.ShiftedLens.OutputFile = *shiftedLens
		}
		wr.BestFocus = wb
	}

	for i := range result.Fields {
		r := result.Fields[i]
		outFile := ""
		if *yamlOut != "" {
			file := suffixFile(*yamlOut, i)
			if err := writeWavefrontYAML(file, &r, mapGridVal); err != nil {
				errOut("Error writing %s: %v", file, err)
				os.Exit(1)
			}
			outFile = file
		}
		if *csvOut != "" {
			file := suffixFile(*csvOut, i)
			if err := writeWavefrontCSV(file, &r, mapGridVal); err != nil {
				errOut("Error writing %s: %v", file, err)
				os.Exit(1)
			}
		}
		wr.Fields = append(wr.Fields, wavefrontFieldToTypes(&r, outFile))
	}
	output.WavefrontResults = wr

	writeYAML(&output)
}// resolveBestFocus builds the engine best-focus configuration from the CLI
// flags and the wavefront: YAML section (CLI wins). Returns nil when best
// focus is disabled.
func resolveBestFocus(input types.Input, fs *flag.FlagSet, bf bool, weightFlag, weightsFlag string, numFields int) *wavefront.FocusConfig {
	yamlCfg := (*types.WavefrontBestFocusConfig)(nil)
	if input.Wavefront != nil {
		yamlCfg = input.Wavefront.BestFocus
	}
	enabled := false
	if flagWasSet(fs, "best-focus") {
		enabled = bf
	} else if yamlCfg != nil {
		enabled = yamlCfg.Enabled
	}
	if !enabled {
		return nil
	}

	cfg := &wavefront.FocusConfig{WeightType: "uniform"}
	if flagWasSet(fs, "focus-weight") && weightFlag != "" {
		cfg.WeightType = weightFlag
	} else if yamlCfg != nil && yamlCfg.WeightType != "" {
		cfg.WeightType = yamlCfg.WeightType
	}
	if flagWasSet(fs, "focus-weights") && weightsFlag != "" {
		cfg.CustomWeights = parseFloatList(weightsFlag, "focus weight")
	} else if yamlCfg != nil && len(yamlCfg.CustomWeights) > 0 {
		cfg.CustomWeights = yamlCfg.CustomWeights
	}
	if cfg.WeightType == "custom" && len(cfg.CustomWeights) != numFields {
		errOut("Error: --focus-weights needs %d values, got %d", numFields, len(cfg.CustomWeights))
		os.Exit(1)
	}
	return cfg
}

// applyImagePlaneShift moves each target config's image plane to the weighted
// best-fit-sphere focus: it computes that config's reference-surface →
// image-plane distance and adds the difference to the image plane's last
// decenter Z shift (creating a decenter when none exists).
func applyImagePlaneShift(configs []types.Config, configFlag *string, refSurfaceID int, weightedFocusZ float64) {
	targets := make([]int, 0, len(configs))
	if configFlag != nil && *configFlag != "" {
		idx, err := resolveConfig(configs, *configFlag)
		if idx < 0 {
			errOut("Error: %s", err)
			os.Exit(1)
		}
		targets = append(targets, idx)
	} else {
		for i := range configs {
			targets = append(targets, i)
		}
	}

	for _, ci := range targets {
		if ci < 0 || ci >= len(configs) {
			continue
		}
		cfg := &configs[ci]
		if len(cfg.Surfaces) < 2 {
			continue
		}
		refID := refSurfaceID
		if refID <= 0 {
			refID = psf.DefaultReferenceSurface(cfg.Surfaces)
		}
		surface.Precompute(cfg.Surfaces)
		z := surface.PhysicalZ(cfg.Surfaces)
		refIdx := -1
		for i, s := range cfg.Surfaces {
			if s.ID == refID {
				refIdx = i
				break
			}
		}
		if refIdx < 0 || refIdx >= len(z)-1 {
			continue
		}
		zImg := z[len(z)-1] - z[refIdx]
		shift := weightedFocusZ - zImg
		applyDecenterZ(&cfg.Surfaces[len(cfg.Surfaces)-1], shift)
	}
}

// applyDecenterZ adds dz to the surface's last decenter Z shift, appending a
// decenter step when the surface has none.
func applyDecenterZ(s *types.Surface, dz float64) {
	if len(s.Decenter) == 0 {
		s.Decenter = append(s.Decenter, types.DecenterStep{Shift: types.Vec3{Z: dz}})
		return
	}
	last := &s.Decenter[len(s.Decenter)-1]
	last.Shift.Z += dz
}

// writeShiftedLens writes the (image-plane shifted) input document to path as a
// standalone pipeline-compatible lens file.
func writeShiftedLens(path string, input *types.Input) error {
	data, err := yaml.Marshal(input)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// writeBackWavefront stores the effective options into the output wavefront:
// section (CLI wins over YAML, principle 3).
func writeBackWavefront(input *types.Input, wavelengths []float64, selected []int, pols []string,
	refSurfaceID, numRays, workers, zOrder, mapGrid int, bf bool, weightType, weights, shiftedLens string) {
	if input.Wavefront == nil {
		input.Wavefront = &types.WavefrontConfig{}
	}
	w := input.Wavefront
	w.ReferenceSurface = refSurfaceID
	w.NumRays = numRays
	w.Workers = workers
	w.ZernikeMaxOrder = zOrder
	w.MapGrid = mapGrid
	w.Fields = selected
	w.Wavelengths = wavelengths
	w.Polarization = strings.Join(pols, ",")
	if w.BestFocus == nil {
		w.BestFocus = &types.WavefrontBestFocusConfig{}
	}
	w.BestFocus.Enabled = bf
	if weightType != "" {
		w.BestFocus.WeightType = weightType
	}
	if weights != "" {
		w.BestFocus.CustomWeights = parseFloatList(weights, "focus weight")
	}
	w.BestFocus.OutputShiftedLens = shiftedLens
}

// wavefrontFieldToTypes maps an engine field result to the pipeline type.
func wavefrontFieldToTypes(r *wavefront.FieldResult, outFile string) types.WavefrontFieldResult {
	terms := make([]types.WavefrontZernikeTerm, len(r.Zernike.Terms))
	for i, t := range r.Zernike.Terms {
		terms[i] = types.WavefrontZernikeTerm{Index: t.Index, Name: t.Name, Coefficient: t.Coefficient}
	}
	out := types.WavefrontFieldResult{
		FieldIndex:   r.FieldIndex,
		FieldAngle:   r.FieldAngle,
		Wavelength:   r.Wavelength,
		Polarization: r.Polarization,
		Paraboloid: types.WavefrontParaboloid{
			X2: r.Paraboloid.X2, Y2: r.Paraboloid.Y2, XY: r.Paraboloid.XY,
			X: r.Paraboloid.X, Y: r.Paraboloid.Y, Constant: r.Paraboloid.Constant,
			Defocus: r.Paraboloid.Defocus, Astigmatism: r.Paraboloid.Astigmatism,
			Tilt: r.Paraboloid.Tilt, RMSResidual: r.Paraboloid.RMSResidual,
		},
		Sphere: types.WavefrontSphere{
			Radius: r.Sphere.Radius,
			Center: types.Vec3{X: r.Sphere.CenterX, Y: r.Sphere.CenterY, Z: r.Sphere.CenterZ},
			RMSResidual: r.Sphere.RMSResidual,
		},
		Zernike: types.WavefrontZernike{
			Basis:        r.Zernike.Basis,
			MaxOrder:     r.Zernike.MaxOrder,
			RemovedTerms: r.Zernike.RemovedTerms,
			Terms:        terms,
			RMSResidual:  r.Zernike.RMSResidual,
		},
		Statistics: types.WavefrontStatistics{
			RMS: r.Statistics.RMS, PV: r.Statistics.PV, Strehl: r.Statistics.Strehl,
		},
		Samples: types.WavefrontSamples{
			Total: r.Samples.Total, Valid: r.Samples.Valid, Missed: r.Samples.Missed,
		},
		OutputFile: outFile,
	}
	return out
}

// --- file outputs -----------------------------------------------------------

// wavefrontYAMLFile is the full structured per-result output written by --yaml.
type wavefrontYAMLFile struct {
	FieldIndex   int                   `yaml:"field_index"`
	FieldAngle   float64               `yaml:"field_angle"`
	Wavelength   float64               `yaml:"wavelength"`
	Polarization string                `yaml:"polarization"`
	Paraboloid   types.WavefrontParaboloid `yaml:"paraboloid"`
	Sphere       types.WavefrontSphere     `yaml:"best_fit_sphere"`
	Zernike      types.WavefrontZernike    `yaml:"zernike"`
	Statistics   types.WavefrontStatistics `yaml:"statistics"`
	Samples      types.WavefrontSamples    `yaml:"samples"`
	// Scattered is the raw per-sample wavefront data on the reference surface.
	Scattered []wavefrontYAMLSample `yaml:"scattered"`
	// Map is the interpolated residual-OPD grid over the pupil.
	Map wavefrontYAMLMap `yaml:"map"`
	OutputFile string `yaml:"output_file,omitempty"`
}

type wavefrontYAMLSample struct {
	X              float64 `yaml:"x"`
	Y              float64 `yaml:"y"`
	OPL            float64 `yaml:"opl"`
	Residual       float64 `yaml:"residual"`
	SphereResidual float64 `yaml:"sphere_residual"`
}

type wavefrontYAMLMap struct {
	Grid wavefrontYAMLGrid `yaml:"grid"`
	OPD  []float64         `yaml:"opd"`
}

type wavefrontYAMLGrid struct {
	NX int     `yaml:"nx"`
	NY int     `yaml:"ny"`
	X0 float64 `yaml:"x0"`
	Y0 float64 `yaml:"y0"`
	DX float64 `yaml:"dx"`
	DY float64 `yaml:"dy"`
}

func writeWavefrontYAML(path string, r *wavefront.FieldResult, mapGrid int) error {
	g, opd := wavefront.InterpolateResidual(r.Data, mapGrid)
	out := wavefrontYAMLFile{
		FieldIndex:   r.FieldIndex,
		FieldAngle:   r.FieldAngle,
		Wavelength:   r.Wavelength,
		Polarization: r.Polarization,
		Paraboloid:   wavefrontFieldToTypes(r, "").Paraboloid,
		Sphere:       wavefrontFieldToTypes(r, "").Sphere,
		Zernike:      wavefrontFieldToTypes(r, "").Zernike,
		Statistics:   wavefrontFieldToTypes(r, "").Statistics,
		Samples:      wavefrontFieldToTypes(r, "").Samples,
		Scattered:    make([]wavefrontYAMLSample, len(r.Data)),
		Map: wavefrontYAMLMap{
			Grid: wavefrontYAMLGrid{NX: g.NX, NY: g.NY, X0: g.X0, Y0: g.Y0, DX: g.DX, DY: g.DY},
			OPD:  opd,
		},
	}
	for i, s := range r.Data {
		out.Scattered[i] = wavefrontYAMLSample{X: s.X, Y: s.Y, OPL: s.OPL, Residual: s.Residual, SphereResidual: s.SphereResidual}
	}
	data, err := yaml.Marshal(&out)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// writeWavefrontCSV writes the interpolated residual-OPD wavefront map in the
// PSF pm3d convention (blank-line-separated rows).
func writeWavefrontCSV(path string, r *wavefront.FieldResult, mapGrid int) error {
	g, opd := wavefront.InterpolateResidual(r.Data, mapGrid)
	var b strings.Builder
	b.WriteString("x,y,opd\n")
	for j := 0; j < g.NY; j++ {
		for i := 0; i < g.NX; i++ {
			x := g.X0 + (float64(i)+0.5)*g.DX
			y := g.Y0 + (float64(j)+0.5)*g.DY
			wv := opd[j*g.NX+i]
			if math.IsNaN(wv) {
				continue
			}
			fmt.Fprintf(&b, "%.9g,%.9g,%.9g\n", x, y, wv)
		}
		b.WriteString("\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}


