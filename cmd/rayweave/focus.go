package main

import (
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/optimize"
	"github.com/hiroki/rayweaver/internal/psf"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// runFocus implements the `focus` subcommand family: image-plane comparison
// tools layered on the psf engine. Like escape/pso it dispatches a
// sub-subcommand:
//
//	focus mtf   MTF (with Strehl/FWHM) at the requested spatial frequencies,
//	            compared across the file / all-field-best / per-field-best planes
//	focus psf   PSF metrics (Strehl/FWHM/EE50/centroid) across the same planes
//
// The base `psf` command stays the single-plane imaging measurement; `focus` is
// the plane-comparison layer that answers "which image plane meets the target".
func runFocus(data []byte) {
	args := os.Args[2:]
	if len(args) == 0 {
		errOut("Error: focus requires a sub-subcommand: focus mtf | focus psf | focus spot")
		os.Exit(1)
	}
	switch args[0] {
	case "mtf":
		runFocusMTF(data, args[1:])
	case "psf":
		runFocusPSF(data, args[1:])
	case "spot":
		runFocusSpot(data, args[1:])
	default:
		errOut("Error: unknown focus sub-subcommand %q (expected mtf | psf | spot)", args[0])
		os.Exit(1)
	}
}

// focusFlags holds the common parsed flags of a focus sub-subcommand.
type focusFlags struct {
	fs           *flag.FlagSet
	glassDir     string
	configFlag   string
	refSurface   int
	gridSize     int
	numRays      int
	fields       string
	wavelengths  string
	polarization string
	planes       string
	throughFocus string
}

// registerFocusFlags declares the flags shared by `focus mtf` and `focus psf`.
// The caller registers its own extras and then parses.
func registerFocusFlags(fs *flag.FlagSet) *focusFlags {
	fl := &focusFlags{fs: fs}
	fs.StringVar(&fl.glassDir, "glass-dir", "", "AGF glass catalog directory")
	fs.StringVar(&fl.configFlag, "config", "", "select config by id (multi-config mode)")
	fs.IntVar(&fl.refSurface, "ref-surface", 0, "reference surface ID for wavefront sampling (default: last optical surface)")
	fs.IntVar(&fl.gridSize, "psf-grid", 0, "image-plane pixels per side (default 64)")
	fs.IntVar(&fl.numRays, "num-rays", 0, "pupil grid rays (default 400)")
	fs.StringVar(&fl.fields, "fields", "", "comma-separated field indices to compute (default: all)")
	fs.StringVar(&fl.wavelengths, "wavelengths", "", "comma-separated wavelengths in mm (default: config wavelengths, else reference)")
	fs.StringVar(&fl.polarization, "polarization", "", "input polarization: RCP (default) | LCP | X | Y | RCP+LCP")
	fs.StringVar(&fl.planes, "planes", "", "planes to compare: file,all,best (default: all three); with --through-focus: base planes file,all,on_axis (default: file)")
	fs.StringVar(&fl.throughFocus, "through-focus", "", "through-focus scan FROM,STEP,COUNT (mm, number of planes); scans around each base plane in --planes")
	fs.String("converge-check", "", "label sampling convergence by re-evaluating at 1.5x rays (default: off; true|false)")
	return fl
}

// focusYAML is the shared subset of the focus.mtf / focus.psf sections.
type focusYAML struct {
	wavelengths      []float64
	fields           []int
	planes           []string
	numRays          int
	gridSize         int
	polarization     string
	referenceSurface int
	convergeCheck    *bool
	throughFocus     *types.FocusThroughFocus
}

func focusYAMLFromMTF(c *types.FocusMTFConfig) focusYAML {
	if c == nil {
		return focusYAML{}
	}
	return focusYAML{
		wavelengths: c.Wavelengths, fields: c.Fields, planes: c.Planes,
		numRays: c.NumRays, gridSize: c.GridSize, polarization: c.Polarization,
		referenceSurface: c.ReferenceSurface, convergeCheck: c.ConvergeCheck,
		throughFocus: c.ThroughFocus,
	}
}

func focusYAMLFromPSF(c *types.FocusPSFConfig) focusYAML {
	if c == nil {
		return focusYAML{}
	}
	return focusYAML{
		wavelengths: c.Wavelengths, fields: c.Fields, planes: c.Planes,
		numRays: c.NumRays, gridSize: c.GridSize, polarization: c.Polarization,
		referenceSurface: c.ReferenceSurface, convergeCheck: c.ConvergeCheck,
		throughFocus: c.ThroughFocus,
	}
}

func focusYAMLFromSpot(c *types.FocusSpotConfig) focusYAML {
	if c == nil {
		return focusYAML{}
	}
	return focusYAML{
		wavelengths: c.Wavelengths, fields: c.Fields, planes: c.Planes,
		numRays: c.NumRays, polarization: c.Polarization,
		referenceSurface: c.ReferenceSurface, throughFocus: c.ThroughFocus,
	}
}

// focusRun is the resolved state shared by the focus plane evaluations.
type focusRun struct {
	gc             *glass.Catalog
	baseSurfaces   []types.Surface
	stopSurface    int
	refWavelength  float64
	cfgFields      []types.FieldItem
	cfgWavelengths []types.WavelengthItem
	fields         []types.FieldDef
	selected       []int
	wavelengths    []float64
	polLabels      []string
	planes         []string
	psfOpts        psf.Options
}

// focusKey identifies one (field, wavelength, polarization) row.
type focusKey struct {
	fieldIndex int
	wavelength float64
	pol        string
}

// buildFocusRun performs the shared CLI-over-YAML resolution for a focus
// sub-subcommand. y carries the sub-subcommand's YAML section on the common
// shape; fl the parsed flags. planeFn resolves the plane set: the file/all/best
// comparison for a normal run, or the file/all/on_axis base planes for a
// through-focus scan.
func buildFocusRun(input *types.Input, fl *focusFlags, y focusYAML, planeFn func(flagVal string, yamlPlanes []string) ([]string, error)) *focusRun {
	gc, _ := loadCatalogs(input, fl.glassDir)
	writeBackGlassDir(input, fl.glassDir)

	baseSurfaces := configSurfaces(input.Configs, &fl.configFlag)
	if len(baseSurfaces) == 0 {
		errOut("Error: no surfaces to process")
		os.Exit(1)
	}
	surface.Precompute(baseSurfaces)

	stopSurface := 0
	if input.Chief != nil {
		stopSurface = input.Chief.StopSurface
	}
	refWavelength := effectiveReferenceWavelength(input.Chief)

	cfgIdx := 0
	if fl.configFlag != "" {
		i, err := resolveConfig(input.Configs, fl.configFlag)
		if i < 0 {
			errOut("Error: %s", err)
			os.Exit(1)
		}
		cfgIdx = i
	}
	var cfgFields []types.FieldItem
	var cfgWLs []types.WavelengthItem
	if len(input.Configs) > 0 {
		cfgFields = input.Configs[cfgIdx].Fields
		cfgWLs = input.Configs[cfgIdx].Wavelengths
	}
	// A hand-written single-config document carries its fields in chief.fields,
	// not configs[].fields. The all-field back-focus solve needs the field list,
	// so fall back to the chief fields rather than silently solving a zero-field
	// system (which no-ops and leaves the file plane unchanged).
	if len(cfgFields) == 0 {
		cfgFields = fieldItemsFromDefs(chiefFieldDefs(*input))
	}

	fields := chiefFieldDefs(*input)
	selected := focusFieldIndices(fields, y.fields, fl.fields)
	fields = applySelectedFields(fields, selected)
	if len(fields) == 0 {
		errOut("Error: no fields to compute")
		os.Exit(1)
	}

	// Wavelengths: flag > YAML > config wavelengths > the reference.
	var wavelengths []float64
	switch {
	case fl.wavelengths != "":
		wavelengths = parseFloatList(fl.wavelengths, "wavelength")
	case len(y.wavelengths) > 0:
		wavelengths = y.wavelengths
	case len(configWavelengthValues(input.Configs, &fl.configFlag)) > 0:
		wavelengths = configWavelengthValues(input.Configs, &fl.configFlag)
	default:
		wavelengths = []float64{refWavelength}
	}

	var polLabels []string
	switch {
	case fl.polarization != "":
		polLabels = parsePolLabels(fl.polarization)
	case y.polarization != "":
		polLabels = parsePolLabels(y.polarization)
	default:
		polLabels = []string{string(types.PolRCP)}
	}

	planes, err := planeFn(fl.planes, y.planes)
	if err != nil {
		errOut("Error: %s", err)
		os.Exit(1)
	}

	opts := psf.Options{
		ReferenceSurface: intOrYAML(fl.refSurface, y.referenceSurface),
		NumRays:          intOrYAML(fl.numRays, y.numRays),
		GridSize:         intOrYAML(fl.gridSize, y.gridSize),
		Polarizations:    polLabels,
		PupilModel:       pupilModelForConfig(*input),
	}

	// The focus commands default the convergence labelling OFF (the comparison
	// is about the plane, not the sampling); an explicit flag or YAML wins.
	cc, ccSet, ccErr := boolFlag(fl.fs, "converge-check")
	if ccErr != nil {
		errOut("Error: %s", ccErr)
		os.Exit(1)
	}
	switch {
	case ccSet:
		opts.ConvergeCheck = cc
	case y.convergeCheck != nil:
		opts.ConvergeCheck = *y.convergeCheck
	}

	return &focusRun{
		gc:             gc,
		baseSurfaces:   baseSurfaces,
		stopSurface:    stopSurface,
		refWavelength:  refWavelength,
		cfgFields:      cfgFields,
		cfgWavelengths: cfgWLs,
		fields:         fields,
		selected:       selected,
		wavelengths:    wavelengths,
		polLabels:      polLabels,
		planes:         planes,
		psfOpts:        opts,
	}
}

// focusFieldIndices resolves --fields over the section's fields list (flag
// wins), returning the kept global indices.
func focusFieldIndices(fields []types.FieldDef, yamlFields []int, flagVal string) []int {
	spec := flagVal
	if spec == "" && len(yamlFields) > 0 {
		spec = intsToCSV(yamlFields)
	}
	if spec == "" {
		kept := make([]int, len(fields))
		for i := range fields {
			kept[i] = i
		}
		return kept
	}
	seen := make(map[int]bool)
	var kept []int
	for _, tok := range strings.Split(spec, ",") {
		idx, err := strconv.Atoi(strings.TrimSpace(tok))
		if err != nil || idx < 0 || idx >= len(fields) {
			errOut("Error: invalid field index %q", tok)
			os.Exit(1)
		}
		if !seen[idx] {
			kept = append(kept, idx)
			seen[idx] = true
		}
	}
	return kept
}

// parseFocusPlanes resolves the plane selectors (flag wins over YAML),
// returning them in canonical order. An empty spec selects all three.
func parseFocusPlanes(flagVal string, yamlPlanes []string) ([]string, error) {
	spec := flagVal
	if spec == "" {
		spec = strings.Join(yamlPlanes, ",")
	}
	if strings.TrimSpace(spec) == "" {
		return []string{"file", "all", "best"}, nil
	}
	seen := make(map[string]bool)
	for _, tok := range strings.Split(spec, ",") {
		p := strings.ToLower(strings.TrimSpace(tok))
		switch p {
		case "file", "all", "best":
			seen[p] = true
		default:
			return nil, fmt.Errorf("invalid plane %q (want file|all|best)", tok)
		}
	}
	if len(seen) == 0 {
		return []string{"file", "all", "best"}, nil
	}
	ordered := make([]string, 0, 3)
	for _, p := range []string{"file", "all", "best"} {
		if seen[p] {
			ordered = append(ordered, p)
		}
	}
	return ordered, nil
}

// parseThroughFocusPlanes resolves the base-plane list for a through-focus
// scan: file (default) | all | on_axis. The per-field `best` plane is not a
// valid scan base and is rejected.
func parseThroughFocusPlanes(flagVal string, yamlPlanes []string) ([]string, error) {
	spec := flagVal
	if spec == "" {
		spec = strings.Join(yamlPlanes, ",")
	}
	if strings.TrimSpace(spec) == "" {
		return []string{"file"}, nil
	}
	seen := make(map[string]bool)
	for _, tok := range strings.Split(spec, ",") {
		p := strings.ToLower(strings.TrimSpace(tok))
		switch p {
		case "file", "all", "on_axis":
			seen[p] = true
		case "best":
			return nil, fmt.Errorf("through-focus does not support the per-field best plane (use file, all or on_axis)")
		default:
			return nil, fmt.Errorf("invalid through-focus base %q (want file|all|on_axis)", tok)
		}
	}
	if len(seen) == 0 {
		return []string{"file"}, nil
	}
	ordered := make([]string, 0, 3)
	for _, p := range []string{"file", "all", "on_axis"} {
		if seen[p] {
			ordered = append(ordered, p)
		}
	}
	return ordered, nil
}

// parseThroughFocus parses a FROM,STEP,COUNT through-focus spec (mm, mm,
// number of planes).
func parseThroughFocus(spec string) (*types.FocusThroughFocus, error) {
	parts := strings.Split(spec, ",")
	if len(parts) != 3 {
		return nil, fmt.Errorf("--through-focus needs FROM,STEP,COUNT (got %q)", spec)
	}
	from, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	if err != nil {
		return nil, fmt.Errorf("invalid --through-focus FROM %q", parts[0])
	}
	step, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err != nil {
		return nil, fmt.Errorf("invalid --through-focus STEP %q", parts[1])
	}
	count, err := strconv.Atoi(strings.TrimSpace(parts[2]))
	if err != nil {
		return nil, fmt.Errorf("invalid --through-focus COUNT %q", parts[2])
	}
	if count < 1 {
		return nil, fmt.Errorf("--through-focus COUNT must be >= 1 (got %d)", count)
	}
	if count > 1 && step <= 0 {
		return nil, fmt.Errorf("--through-focus STEP must be > 0 when COUNT > 1 (got %g)", step)
	}
	return &types.FocusThroughFocus{From: from, Step: step, Count: count}, nil
}

// resolveThroughFocus resolves the through-focus scan: an explicit flag wins
// over the YAML section; nil means a normal plane comparison.
func resolveThroughFocus(fl *focusFlags, y focusYAML) *types.FocusThroughFocus {
	if strings.TrimSpace(fl.throughFocus) != "" {
		tf, err := parseThroughFocus(fl.throughFocus)
		if err != nil {
			errOut("Error: %s", err)
			os.Exit(1)
		}
		return tf
	}
	return y.throughFocus
}

// throughFocusBaseShift returns the base plane's image-plane shift from the
// file plane (mm): 0 for file, the all-field best-focus shift for all, the
// on-axis best-focus shift for on_axis. The shift is read from the back-focus
// solve applied to a copy, so the run's surfaces are not modified.
func throughFocusBaseShift(run *focusRun, base string) float64 {
	switch base {
	case "all", "on_axis":
	default:
		return 0
	}
	weight := "uniform"
	if base == "on_axis" {
		weight = "on_axis_only"
	}
	surfaces := append([]types.Surface(nil), run.baseSurfaces...)
	before := append([]types.Surface(nil), surfaces...)
	bf := &types.BackFocusSolveConfig{Enabled: true, Type: "wavefront", WeightType: weight,
		NumRays: effectivePSFNumRays(run.psfOpts.NumRays)}
	optimize.ApplyBackFocusSolve(surfaces, bf, "wavefront", run.stopSurface,
		run.refWavelength, run.cfgFields, run.cfgWavelengths, run.gc)
	return maxThicknessDelta(before, surfaces)
}

// tfKeyedPoint is one through-focus point with its (field, wavelength,
// polarization) row key.
type tfKeyedPoint struct {
	key        focusKey
	fieldAngle float64
	point      types.FocusThroughFocusPoint
}

// throughFocusPoints evaluates one image plane (o.PlaneShift) for the metric and
// returns the points keyed by (field, wavelength, polarization). metric is
// "psf" | "mtf" | "spot".
func throughFocusPoints(run *focusRun, system types.System, o psf.Options, metric string, focusMM float64, freqs []float64) []tfKeyedPoint {
	if metric == "spot" {
		spotRes, err := psf.ComputeSpot(system, run.gc, run.fields, run.wavelengths, o)
		if err != nil {
			errOut("Error: %v", err)
			os.Exit(1)
		}
		out := make([]tfKeyedPoint, 0, len(spotRes))
		for _, r := range spotRes {
			out = append(out, tfKeyedPoint{
				key:        focusKey{r.FieldIndex, r.Wavelength, r.Polarization},
				fieldAngle: r.FieldAngle,
				point: types.FocusThroughFocusPoint{
					FocusMM:   focusMM,
					SpotRMS:   r.SpotRMS,
					SpotRMSX:  r.SpotRMSX,
					SpotRMSY:  r.SpotRMSY,
					SpotRMST:  r.SpotRMST,
					SpotRMSS:  r.SpotRMSS,
					CentroidX: r.CentroidX,
					CentroidY: r.CentroidY,
				},
			})
		}
		return out
	}
	results, err := psf.Compute(system, run.gc, run.fields, run.wavelengths, o)
	if err != nil {
		errOut("Error: %v", err)
		os.Exit(1)
	}
	out := make([]tfKeyedPoint, 0, len(results))
	for _, r := range results {
		pt := types.FocusThroughFocusPoint{FocusMM: focusMM, Strehl: r.Strehl}
		if metric == "mtf" {
			pt.Sagittal = mtfAxisValues(r.MTF, true, freqs)
			pt.Tangential = mtfAxisValues(r.MTF, false, freqs)
		} else {
			pt.FWHMX = r.FWHMX
			pt.FWHMY = r.FWHMY
			pt.EncircledEnergy50 = r.Encircled50
			pt.CentroidX = r.CentroidX
			pt.CentroidY = r.CentroidY
		}
		out = append(out, tfKeyedPoint{
			key:        focusKey{r.FieldIndex, r.Wavelength, r.Polarization},
			fieldAngle: r.FieldAngle,
			point:      pt,
		})
	}
	return out
}

// computeThroughFocus runs the through-focus scan: Count planes at
// From + i*Step (mm) around every base plane in run.planes. metric is "psf",
// "mtf" or "spot"; freqs are the MTF frequencies (metric == "mtf").
func computeThroughFocus(run *focusRun, tf *types.FocusThroughFocus, metric string, freqs []float64) *types.FocusThroughFocusComparison {
	opts := run.psfOpts
	if metric == "mtf" {
		opts.MTFCfg = &types.PSFMTFConfig{Frequencies: freqs}
	}
	system := types.System{Surfaces: run.baseSurfaces, StopSurface: run.stopSurface}
	comp := &types.FocusThroughFocusComparison{
		Metric:       metric,
		From:         tf.From,
		Step:         tf.Step,
		Count:        tf.Count,
		Polarization: strings.Join(run.polLabels, ","),
	}
	if metric == "mtf" {
		comp.Frequencies = freqs
	}
	for _, base := range run.planes {
		baseShift := throughFocusBaseShift(run, base)
		scan := types.FocusThroughFocusScan{Base: base, BaseShiftMM: baseShift}
		index := make(map[focusKey]int)
		for i := 0; i < tf.Count; i++ {
			focusMM := tf.From + float64(i)*tf.Step
			o := opts
			o.PlaneShift = baseShift + focusMM
			for _, kp := range throughFocusPoints(run, system, o, metric, focusMM, freqs) {
				idx, ok := index[kp.key]
				if !ok {
					idx = len(scan.Rows)
					index[kp.key] = idx
					scan.Rows = append(scan.Rows, types.FocusThroughFocusRow{
						FieldIndex: kp.key.fieldIndex,
						FieldAngle: kp.fieldAngle,
						Wavelength: kp.key.wavelength,
					})
				}
				scan.Rows[idx].Points = append(scan.Rows[idx].Points, kp.point)
			}
		}
		comp.Scans = append(comp.Scans, scan)
	}
	return comp
}

// focusPlaneLabel maps a plane selector to its output key.
func focusPlaneLabel(p string) string {
	switch p {
	case "all":
		return "focus_plane_all"
	case "best":
		return "best_focus"
	default:
		return "file"
	}
}

// focusPlaneLabels maps the selectors to their output keys.
func focusPlaneLabels(planes []string) []string {
	out := make([]string, len(planes))
	for i, p := range planes {
		out[i] = focusPlaneLabel(p)
	}
	return out
}

// computeFocusPlane runs psf.Compute for one plane convention: the file plane
// as-is, the single all-field best focus (uniform wavefront solve), or each
// field's own best focus. It returns the results and the applied plane shift.
func computeFocusPlane(run *focusRun, plane string) ([]psf.Result, float64, error) {
	surfaces := append([]types.Surface(nil), run.baseSurfaces...)
	opts := run.psfOpts
	shift := 0.0
	switch plane {
	case "all":
		before := append([]types.Surface(nil), surfaces...)
		// Sample the back-focus solve with the same pupil grid the comparison
		// is evaluated at (CLI > YAML > psf default); the solve's coherent-peak
		// objective is sampling-sensitive for aberrated fields.
		bf := &types.BackFocusSolveConfig{Enabled: true, Type: "wavefront", WeightType: "uniform",
			NumRays: effectivePSFNumRays(opts.NumRays)}
		optimize.ApplyBackFocusSolve(surfaces, bf, "wavefront", run.stopSurface,
			run.refWavelength, run.cfgFields, run.cfgWavelengths, run.gc)
		// The solve changes a thickness after its internal Precompute, so the
		// image-plane Z would otherwise stay stale and psf.Compute would trace
		// the unsolved plane.
		surface.Precompute(surfaces)
		shift = maxThicknessDelta(before, surfaces)
	case "best":
		opts.BestFocus = true
	}
	results, err := psf.Compute(
		types.System{Surfaces: surfaces, StopSurface: run.stopSurface},
		run.gc, run.fields, run.wavelengths, opts)
	return results, shift, err
}

// computeFocusPlanes evaluates every requested plane and indexes the results by
// (field, wavelength, polarization). order is the sorted union of the keys and
// shiftAll the image-plane shift the `all` convention applied.
func computeFocusPlanes(run *focusRun) (map[string]map[focusKey]*psf.Result, []focusKey, float64, error) {
	byPlane := make(map[string]map[focusKey]*psf.Result, len(run.planes))
	seen := make(map[focusKey]bool)
	var order []focusKey
	shiftAll := 0.0
	for _, plane := range run.planes {
		results, shift, err := computeFocusPlane(run, plane)
		if err != nil {
			return nil, nil, 0, err
		}
		if plane == "all" {
			shiftAll = shift
		}
		m := make(map[focusKey]*psf.Result, len(results))
		for i := range results {
			r := &results[i]
			k := focusKey{r.FieldIndex, r.Wavelength, r.Polarization}
			m[k] = r
			if !seen[k] {
				seen[k] = true
				order = append(order, k)
			}
		}
		byPlane[plane] = m
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].fieldIndex != order[j].fieldIndex {
			return order[i].fieldIndex < order[j].fieldIndex
		}
		if order[i].wavelength != order[j].wavelength {
			return order[i].wavelength < order[j].wavelength
		}
		return order[i].pol < order[j].pol
	})
	return byPlane, order, shiftAll, nil
}

// firstFocusResult returns any plane's result for a key (for row metadata).
func firstFocusResult(byPlane map[string]map[focusKey]*psf.Result, k focusKey) *psf.Result {
	for _, plane := range []string{"file", "all", "best"} {
		if r := byPlane[plane][k]; r != nil {
			return r
		}
	}
	return nil
}

// runFocusMTF implements `focus mtf`.
func runFocusMTF(data []byte, args []string) {
	fs := flag.NewFlagSet("focus mtf", flag.ExitOnError)
	fl := registerFocusFlags(fs)
	freqFlag := fs.String("frequencies", "", "comma-separated spatial frequencies in cycles/mm (default: focus.mtf.frequencies, else 50)")
	fs.Parse(args)

	input := parseYAML[types.Input](data)
	setReferenceWavelength(input.Chief)
	if input.Chief == nil {
		errOut("Error: 'chief' section is required (for fields)")
		os.Exit(1)
	}
	var mtfCfg *types.FocusMTFConfig
	if input.Focus != nil {
		mtfCfg = input.Focus.MTF
	}
	y := focusYAMLFromMTF(mtfCfg)
	freqs := focusFrequencies(*freqFlag, mtfCfg)

	if tf := resolveThroughFocus(fl, y); tf != nil {
		run := buildFocusRun(&input, fl, y, parseThroughFocusPlanes)
		comp := computeThroughFocus(run, tf, "mtf", freqs)
		writeBackFocusMTF(&input, run, freqs)
		input.Focus.MTF.ThroughFocus = tf
		output := types.Output{Input: input, FocusComparison: &types.FocusComparison{ThroughFocus: comp}}
		withOutputMetadata(&output.Input, "focus mtf", subcmdArgs())
		writeYAML(&output)
		return
	}

	run := buildFocusRun(&input, fl, y, parseFocusPlanes)
	run.psfOpts.MTFCfg = &types.PSFMTFConfig{Frequencies: freqs}

	byPlane, order, shiftAll, err := computeFocusPlanes(run)
	if err != nil {
		errOut("Error: %v", err)
		os.Exit(1)
	}
	rows := buildFocusMTFRows(byPlane, order, freqs)
	if len(rows) == 0 {
		errOut("Error: no focus comparison rows computed (check fields/wavelengths/planes)")
		os.Exit(1)
	}

	comp := &types.FocusMTFComparison{
		Planes:               focusPlaneLabels(run.planes),
		Frequencies:          freqs,
		FocusPlaneAllShiftMM: shiftAll,
		Polarization:         strings.Join(run.polLabels, ","),
		Rows:                 rows,
	}

	writeBackFocusMTF(&input, run, freqs)
	output := types.Output{Input: input, FocusComparison: &types.FocusComparison{MTF: comp}}
	withOutputMetadata(&output.Input, "focus mtf", subcmdArgs())
	writeYAML(&output)
}

// runFocusPSF implements `focus psf`.
func runFocusPSF(data []byte, args []string) {
	fs := flag.NewFlagSet("focus psf", flag.ExitOnError)
	fl := registerFocusFlags(fs)
	fs.Parse(args)

	input := parseYAML[types.Input](data)
	setReferenceWavelength(input.Chief)
	if input.Chief == nil {
		errOut("Error: 'chief' section is required (for fields)")
		os.Exit(1)
	}
	var psfCfg *types.FocusPSFConfig
	if input.Focus != nil {
		psfCfg = input.Focus.PSF
	}
	y := focusYAMLFromPSF(psfCfg)

	if tf := resolveThroughFocus(fl, y); tf != nil {
		run := buildFocusRun(&input, fl, y, parseThroughFocusPlanes)
		comp := computeThroughFocus(run, tf, "psf", nil)
		writeBackFocusPSF(&input, run)
		input.Focus.PSF.ThroughFocus = tf
		output := types.Output{Input: input, FocusComparison: &types.FocusComparison{ThroughFocus: comp}}
		withOutputMetadata(&output.Input, "focus psf", subcmdArgs())
		writeYAML(&output)
		return
	}

	run := buildFocusRun(&input, fl, y, parseFocusPlanes)

	byPlane, order, shiftAll, err := computeFocusPlanes(run)
	if err != nil {
		errOut("Error: %v", err)
		os.Exit(1)
	}
	rows := buildFocusPSFRows(byPlane, order)
	if len(rows) == 0 {
		errOut("Error: no focus comparison rows computed (check fields/wavelengths/planes)")
		os.Exit(1)
	}

	comp := &types.FocusPSFComparison{
		Planes:               focusPlaneLabels(run.planes),
		FocusPlaneAllShiftMM: shiftAll,
		Polarization:         strings.Join(run.polLabels, ","),
		Rows:                 rows,
	}

	writeBackFocusPSF(&input, run)
	output := types.Output{Input: input, FocusComparison: &types.FocusComparison{PSF: comp}}
	withOutputMetadata(&output.Input, "focus psf", subcmdArgs())
	writeYAML(&output)
}

// runFocusSpot implements `focus spot`: the geometric (ray) spot RMS and its
// x/y and tangential/sagittal decompositions, compared across the plane
// conventions (or a through-focus scan). It needs no Huygens integral.
func runFocusSpot(data []byte, args []string) {
	fs := flag.NewFlagSet("focus spot", flag.ExitOnError)
	fl := registerFocusFlags(fs)
	fs.Parse(args)

	input := parseYAML[types.Input](data)
	setReferenceWavelength(input.Chief)
	if input.Chief == nil {
		errOut("Error: 'chief' section is required (for fields)")
		os.Exit(1)
	}
	var spotCfg *types.FocusSpotConfig
	if input.Focus != nil {
		spotCfg = input.Focus.Spot
	}
	y := focusYAMLFromSpot(spotCfg)

	if tf := resolveThroughFocus(fl, y); tf != nil {
		run := buildFocusRun(&input, fl, y, parseThroughFocusPlanes)
		comp := computeThroughFocus(run, tf, "spot", nil)
		writeBackFocusSpot(&input, run)
		input.Focus.Spot.ThroughFocus = tf
		output := types.Output{Input: input, FocusComparison: &types.FocusComparison{ThroughFocus: comp}}
		withOutputMetadata(&output.Input, "focus spot", subcmdArgs())
		writeYAML(&output)
		return
	}

	run := buildFocusRun(&input, fl, y, parseFocusPlanes)

	byPlane, order, shiftAll, err := computeFocusSpotPlanes(run)
	if err != nil {
		errOut("Error: %v", err)
		os.Exit(1)
	}
	rows := buildFocusSpotRows(byPlane, order)
	if len(rows) == 0 {
		errOut("Error: no focus comparison rows computed (check fields/wavelengths/planes)")
		os.Exit(1)
	}

	comp := &types.FocusSpotComparison{
		Planes:               focusPlaneLabels(run.planes),
		FocusPlaneAllShiftMM: shiftAll,
		Polarization:         strings.Join(run.polLabels, ","),
		Rows:                 rows,
	}

	writeBackFocusSpot(&input, run)
	output := types.Output{Input: input, FocusComparison: &types.FocusComparison{Spot: comp}}
	withOutputMetadata(&output.Input, "focus spot", subcmdArgs())
	writeYAML(&output)
}

// computeFocusSpotPlane evaluates one plane convention for the geometric spot:
// file (as-is), all (the single all-field best-focus plane shift), or best
// (each field's own coherent-peak best focus).
func computeFocusSpotPlane(run *focusRun, plane string) ([]psf.SpotResult, float64, error) {
	opts := run.psfOpts
	shift := 0.0
	switch plane {
	case "all":
		shift = throughFocusBaseShift(run, "all")
		opts.PlaneShift = shift
	case "best":
		opts.BestFocus = true
	}
	results, err := psf.ComputeSpot(
		types.System{Surfaces: run.baseSurfaces, StopSurface: run.stopSurface},
		run.gc, run.fields, run.wavelengths, opts)
	return results, shift, err
}

// computeFocusSpotPlanes evaluates every requested plane and indexes the
// results by (field, wavelength, polarization).
func computeFocusSpotPlanes(run *focusRun) (map[string]map[focusKey]*psf.SpotResult, []focusKey, float64, error) {
	byPlane := make(map[string]map[focusKey]*psf.SpotResult, len(run.planes))
	seen := make(map[focusKey]bool)
	var order []focusKey
	shiftAll := 0.0
	for _, plane := range run.planes {
		results, shift, err := computeFocusSpotPlane(run, plane)
		if err != nil {
			return nil, nil, 0, err
		}
		if plane == "all" {
			shiftAll = shift
		}
		m := make(map[focusKey]*psf.SpotResult, len(results))
		for i := range results {
			r := &results[i]
			k := focusKey{r.FieldIndex, r.Wavelength, r.Polarization}
			m[k] = r
			if !seen[k] {
				seen[k] = true
				order = append(order, k)
			}
		}
		byPlane[plane] = m
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].fieldIndex != order[j].fieldIndex {
			return order[i].fieldIndex < order[j].fieldIndex
		}
		if order[i].wavelength != order[j].wavelength {
			return order[i].wavelength < order[j].wavelength
		}
		return order[i].pol < order[j].pol
	})
	return byPlane, order, shiftAll, nil
}

// buildFocusSpotRows tabulates the per-plane spot metrics for every key.
func buildFocusSpotRows(byPlane map[string]map[focusKey]*psf.SpotResult, order []focusKey) []types.FocusSpotRow {
	rows := make([]types.FocusSpotRow, 0, len(order))
	for _, k := range order {
		var ref *psf.SpotResult
		for _, plane := range []string{"file", "all", "best"} {
			if r := byPlane[plane][k]; r != nil {
				ref = r
				break
			}
		}
		if ref == nil {
			continue
		}
		rows = append(rows, types.FocusSpotRow{
			FieldIndex:    k.fieldIndex,
			FieldAngle:    ref.FieldAngle,
			Wavelength:    k.wavelength,
			File:          focusSpotPlane(byPlane["file"][k]),
			FocusPlaneAll: focusSpotPlane(byPlane["all"][k]),
			BestFocus:     focusSpotPlane(byPlane["best"][k]),
		})
	}
	return rows
}

func focusSpotPlane(r *psf.SpotResult) *types.FocusSpotPlane {
	if r == nil {
		return nil
	}
	return &types.FocusSpotPlane{
		SpotRMS:          r.SpotRMS,
		SpotRMSX:         r.SpotRMSX,
		SpotRMSY:         r.SpotRMSY,
		SpotRMST:         r.SpotRMST,
		SpotRMSS:         r.SpotRMSS,
		CentroidX:        r.CentroidX,
		CentroidY:        r.CentroidY,
		BestFocusShiftMM: r.BestFocusShift,
	}
}

// focusFrequencies resolves the reported spatial frequencies: the flag, else
// the YAML list, else the 50 lp/mm default.
func focusFrequencies(flagVal string, cfg *types.FocusMTFConfig) []float64 {
	if strings.TrimSpace(flagVal) != "" {
		return parseFloatList(flagVal, "frequency")
	}
	if cfg != nil && len(cfg.Frequencies) > 0 {
		return cfg.Frequencies
	}
	return []float64{50}
}

// buildFocusMTFRows tabulates the per-plane MTF for every key.
func buildFocusMTFRows(byPlane map[string]map[focusKey]*psf.Result, order []focusKey, freqs []float64) []types.FocusMTFRow {
	rows := make([]types.FocusMTFRow, 0, len(order))
	for _, k := range order {
		ref := firstFocusResult(byPlane, k)
		if ref == nil {
			continue
		}
		rows = append(rows, types.FocusMTFRow{
			FieldIndex:    k.fieldIndex,
			FieldAngle:    ref.FieldAngle,
			Wavelength:    k.wavelength,
			File:          focusMTFPlane(byPlane["file"][k], freqs),
			FocusPlaneAll: focusMTFPlane(byPlane["all"][k], freqs),
			BestFocus:     focusMTFPlane(byPlane["best"][k], freqs),
		})
	}
	return rows
}

func focusMTFPlane(r *psf.Result, freqs []float64) *types.FocusMTFPlane {
	if r == nil {
		return nil
	}
	return &types.FocusMTFPlane{
		Strehl:           r.Strehl,
		BestFocusShiftMM: r.BestFocusShift,
		Sagittal:         mtfAxisValues(r.MTF, true, freqs),
		Tangential:       mtfAxisValues(r.MTF, false, freqs),
	}
}

// mtfAxisValues samples the evaluated MTF of one axis at each frequency.
func mtfAxisValues(m *types.PSFMTFSummary, sagittal bool, freqs []float64) []float64 {
	out := make([]float64, len(freqs))
	if m == nil {
		for i := range out {
			out[i] = math.NaN()
		}
		return out
	}
	axis := m.Tangential
	if sagittal {
		axis = m.Sagittal
	}
	for i, f := range freqs {
		out[i] = mtfAtFrequency(axis, f)
	}
	return out
}

// mtfAtFrequency returns the evaluated MTF at f (NaN when f was not evaluated).
func mtfAtFrequency(axis types.PSFMTFAxis, f float64) float64 {
	tol := 1e-9 * math.Max(1, math.Abs(f))
	for _, p := range axis.Evaluated {
		if math.Abs(p.Frequency-f) <= tol {
			return p.MTF
		}
	}
	return math.NaN()
}

// buildFocusPSFRows tabulates the per-plane PSF metrics for every key.
func buildFocusPSFRows(byPlane map[string]map[focusKey]*psf.Result, order []focusKey) []types.FocusPSFRow {
	rows := make([]types.FocusPSFRow, 0, len(order))
	for _, k := range order {
		ref := firstFocusResult(byPlane, k)
		if ref == nil {
			continue
		}
		rows = append(rows, types.FocusPSFRow{
			FieldIndex:    k.fieldIndex,
			FieldAngle:    ref.FieldAngle,
			Wavelength:    k.wavelength,
			File:          focusPSFPlane(byPlane["file"][k]),
			FocusPlaneAll: focusPSFPlane(byPlane["all"][k]),
			BestFocus:     focusPSFPlane(byPlane["best"][k]),
		})
	}
	return rows
}

func focusPSFPlane(r *psf.Result) *types.FocusPSFPlane {
	if r == nil {
		return nil
	}
	return &types.FocusPSFPlane{
		Strehl:            r.Strehl,
		FWHMX:             r.FWHMX,
		FWHMY:             r.FWHMY,
		EncircledEnergy50: r.Encircled50,
		CentroidX:         r.CentroidX,
		CentroidY:         r.CentroidY,
		BestFocusShiftMM:  r.BestFocusShift,
	}
}

// writeBackFocusMTF stores the effective `focus mtf` options so the pipeline
// reflects what was actually computed (flags override YAML).
func writeBackFocusMTF(input *types.Input, run *focusRun, freqs []float64) {
	if input.Focus == nil {
		input.Focus = &types.FocusConfig{}
	}
	if input.Focus.MTF == nil {
		input.Focus.MTF = &types.FocusMTFConfig{}
	}
	c := input.Focus.MTF
	c.Wavelengths = run.wavelengths
	c.Frequencies = freqs
	c.Fields = run.selected
	c.Planes = run.planes
	c.Polarization = strings.Join(run.polLabels, ",")
	c.ReferenceSurface = run.psfOpts.ReferenceSurface
	c.NumRays = run.psfOpts.NumRays
	c.GridSize = run.psfOpts.GridSize
	c.ConvergeCheck = &run.psfOpts.ConvergeCheck
}

// writeBackFocusPSF stores the effective `focus psf` options.
func writeBackFocusPSF(input *types.Input, run *focusRun) {
	if input.Focus == nil {
		input.Focus = &types.FocusConfig{}
	}
	if input.Focus.PSF == nil {
		input.Focus.PSF = &types.FocusPSFConfig{}
	}
	c := input.Focus.PSF
	c.Wavelengths = run.wavelengths
	c.Fields = run.selected
	c.Planes = run.planes
	c.Polarization = strings.Join(run.polLabels, ",")
	c.ReferenceSurface = run.psfOpts.ReferenceSurface
	c.NumRays = run.psfOpts.NumRays
	c.GridSize = run.psfOpts.GridSize
	c.ConvergeCheck = &run.psfOpts.ConvergeCheck
}

// writeBackFocusSpot stores the effective `focus spot` options.
func writeBackFocusSpot(input *types.Input, run *focusRun) {
	if input.Focus == nil {
		input.Focus = &types.FocusConfig{}
	}
	if input.Focus.Spot == nil {
		input.Focus.Spot = &types.FocusSpotConfig{}
	}
	c := input.Focus.Spot
	c.Wavelengths = run.wavelengths
	c.Fields = run.selected
	c.Planes = run.planes
	c.Polarization = strings.Join(run.polLabels, ",")
	c.ReferenceSurface = run.psfOpts.ReferenceSurface
	c.NumRays = run.psfOpts.NumRays
}

// focusSpotPlaneEntries lists the present plane blocks of a spot row.
func focusSpotPlaneEntries(row types.FocusSpotRow) []struct {
	name  string
	plane *types.FocusSpotPlane
} {
	return []struct {
		name  string
		plane *types.FocusSpotPlane
	}{
		{"file", row.File},
		{"focus_plane_all", row.FocusPlaneAll},
		{"best_focus", row.BestFocus},
	}
}

// focusMTFPlaneEntries lists the present plane blocks of a row in canonical
// order, with their output names.
func focusMTFPlaneEntries(row types.FocusMTFRow) []struct {
	name  string
	plane *types.FocusMTFPlane
} {
	return []struct {
		name  string
		plane *types.FocusMTFPlane
	}{
		{"file", row.File},
		{"focus_plane_all", row.FocusPlaneAll},
		{"best_focus", row.BestFocus},
	}
}

func focusPSFPlaneEntries(row types.FocusPSFRow) []struct {
	name  string
	plane *types.FocusPSFPlane
} {
	return []struct {
		name  string
		plane *types.FocusPSFPlane
	}{
		{"file", row.File},
		{"focus_plane_all", row.FocusPlaneAll},
		{"best_focus", row.BestFocus},
	}
}

// writeFocusMTFTable writes a human-readable MTF comparison to w. It is used by
// `list focus` (w = stdout); the `focus` command itself no longer prints a
// table (its stdout stays a clean pipeline document).
func writeFocusMTFTable(w io.Writer, c *types.FocusMTFComparison) {
	fmt.Fprintf(w, "focus mtf: MTF comparison (planes: %s, polarization: %s)\n",
		strings.Join(c.Planes, ", "), c.Polarization)
	if c.FocusPlaneAllShiftMM != 0 {
		fmt.Fprintf(w, "  all-field best-focus shift: %.6f mm\n", c.FocusPlaneAllShiftMM)
	}
	header := fmt.Sprintf("  %-4s %-7s %-9s %-16s %-8s", "fld", "angle", "wl(nm)", "plane", "strehl")
	for _, f := range c.Frequencies {
		header += fmt.Sprintf("  %-14s", fmt.Sprintf("MTF%.0f(sag/tan)", f))
	}
	fmt.Fprintln(w, header)
	for _, row := range c.Rows {
		for _, e := range focusMTFPlaneEntries(row) {
			if e.plane == nil {
				continue
			}
			line := fmt.Sprintf("  %-4d %-7.2f %-9.1f %-16s %-8.4f",
				row.FieldIndex, row.FieldAngle, row.Wavelength*1e6, e.name, e.plane.Strehl)
			for i := range c.Frequencies {
				sag, tan := math.NaN(), math.NaN()
				if i < len(e.plane.Sagittal) {
					sag = e.plane.Sagittal[i]
				}
				if i < len(e.plane.Tangential) {
					tan = e.plane.Tangential[i]
				}
				line += fmt.Sprintf("  %-14s", fmt.Sprintf("%.3f/%.3f", sag, tan))
			}
			fmt.Fprintln(w, line)
		}
	}
}

// writeFocusPSFTable writes a human-readable PSF comparison to w (see
// writeFocusMTFTable).
func writeFocusPSFTable(w io.Writer, c *types.FocusPSFComparison) {
	fmt.Fprintf(w, "focus psf: PSF comparison (planes: %s, polarization: %s)\n",
		strings.Join(c.Planes, ", "), c.Polarization)
	if c.FocusPlaneAllShiftMM != 0 {
		fmt.Fprintf(w, "  all-field best-focus shift: %.6f mm\n", c.FocusPlaneAllShiftMM)
	}
	fmt.Fprintf(w, "  %-4s %-7s %-9s %-16s %-8s %-9s %-9s %-10s %-10s %-10s\n",
		"fld", "angle", "wl(nm)", "plane", "strehl", "fwhm_x", "fwhm_y", "ee50", "centroid_x", "centroid_y")
	for _, row := range c.Rows {
		for _, e := range focusPSFPlaneEntries(row) {
			if e.plane == nil {
				continue
			}
			fmt.Fprintf(w, "  %-4d %-7.2f %-9.1f %-16s %-8.4f %-9.5f %-9.5f %-10.5f %-10.4f %-10.4f\n",
				row.FieldIndex, row.FieldAngle, row.Wavelength*1e6, e.name,
				e.plane.Strehl, e.plane.FWHMX, e.plane.FWHMY,
				e.plane.EncircledEnergy50, e.plane.CentroidX, e.plane.CentroidY)
		}
	}
}

// writeFocusSpotTable writes a human-readable geometric-spot comparison to w.
// Six metric columns: the scalar RMS radius, its tangential/sagittal and x/y
// decompositions, and the combined centroid (all mm).
func writeFocusSpotTable(w io.Writer, c *types.FocusSpotComparison) {
	fmt.Fprintf(w, "focus spot: spot RMS comparison (planes: %s, polarization: %s, mm)\n",
		strings.Join(c.Planes, ", "), c.Polarization)
	if c.FocusPlaneAllShiftMM != 0 {
		fmt.Fprintf(w, "  all-field best-focus shift: %.6f mm\n", c.FocusPlaneAllShiftMM)
	}
	fmt.Fprintf(w, "  %-4s %-7s %-9s %-16s %-9s %-9s %-9s %-9s %-9s %-18s\n",
		"fld", "angle", "wl(nm)", "plane", "spot_rms", "rms_t", "rms_s", "rms_x", "rms_y", "centroid")
	for _, row := range c.Rows {
		for _, e := range focusSpotPlaneEntries(row) {
			if e.plane == nil {
				continue
			}
			fmt.Fprintf(w, "  %-4d %-7.2f %-9.1f %-16s %-9.5f %-9.5f %-9.5f %-9.5f %-9.5f %-18s\n",
				row.FieldIndex, row.FieldAngle, row.Wavelength*1e6, e.name,
				e.plane.SpotRMS, e.plane.SpotRMST, e.plane.SpotRMSS,
				e.plane.SpotRMSX, e.plane.SpotRMSY,
				fmt.Sprintf("%.4f,%.4f", e.plane.CentroidX, e.plane.CentroidY))
		}
	}
}

// throughFocusBaseNames lists the scan bases in order (file, all, on_axis).
func throughFocusBaseNames(c *types.FocusThroughFocusComparison) string {
	names := make([]string, len(c.Scans))
	for i, s := range c.Scans {
		names[i] = s.Base
	}
	return strings.Join(names, ", ")
}

// writeFocusThroughFocusTable writes a human-readable through-focus scan to w
// (see writeFocusMTFTable).
func writeFocusThroughFocusTable(w io.Writer, c *types.FocusThroughFocusComparison) {
	fmt.Fprintf(w, "focus %s: through-focus (from %.3f, step %.3f, count %d, bases: %s, polarization: %s)\n",
		c.Metric, c.From, c.Step, c.Count, throughFocusBaseNames(c), c.Polarization)
	if c.Metric == "mtf" {
		header := fmt.Sprintf("  %-8s %-4s %-7s %-9s %-9s %-8s", "base", "fld", "angle", "wl(nm)", "focus_mm", "strehl")
		for _, f := range c.Frequencies {
			header += fmt.Sprintf("  %-14s", fmt.Sprintf("MTF%.0f(sag/tan)", f))
		}
		fmt.Fprintln(w, header)
		for _, s := range c.Scans {
			for _, row := range s.Rows {
				for _, p := range row.Points {
					line := fmt.Sprintf("  %-8s %-4d %-7.2f %-9.1f %-9.3f %-8.4f",
						s.Base, row.FieldIndex, row.FieldAngle, row.Wavelength*1e6, p.FocusMM, p.Strehl)
					for i := range c.Frequencies {
						sag, tan := math.NaN(), math.NaN()
						if i < len(p.Sagittal) {
							sag = p.Sagittal[i]
						}
						if i < len(p.Tangential) {
							tan = p.Tangential[i]
						}
						line += fmt.Sprintf("  %-14s", fmt.Sprintf("%.3f/%.3f", sag, tan))
					}
					fmt.Fprintln(w, line)
				}
			}
		}
		return
	}
	if c.Metric == "spot" {
		fmt.Fprintf(w, "  %-8s %-4s %-7s %-9s %-9s %-9s %-9s %-9s %-9s %-9s %-18s\n",
			"base", "fld", "angle", "wl(nm)", "focus_mm", "spot_rms", "rms_t", "rms_s", "rms_x", "rms_y", "centroid")
		for _, s := range c.Scans {
			for _, row := range s.Rows {
				for _, p := range row.Points {
					fmt.Fprintf(w, "  %-8s %-4d %-7.2f %-9.1f %-9.3f %-9.5f %-9.5f %-9.5f %-9.5f %-9.5f %-18s\n",
						s.Base, row.FieldIndex, row.FieldAngle, row.Wavelength*1e6, p.FocusMM,
						p.SpotRMS, p.SpotRMST, p.SpotRMSS, p.SpotRMSX, p.SpotRMSY,
						fmt.Sprintf("%.4f,%.4f", p.CentroidX, p.CentroidY))
				}
			}
		}
		return
	}
	fmt.Fprintf(w, "  %-8s %-4s %-7s %-9s %-9s %-8s %-9s %-9s %-10s %-10s %-10s\n",
		"base", "fld", "angle", "wl(nm)", "focus_mm", "strehl", "fwhm_x", "fwhm_y", "ee50", "centroid_x", "centroid_y")
	for _, s := range c.Scans {
		for _, row := range s.Rows {
			for _, p := range row.Points {
				fmt.Fprintf(w, "  %-8s %-4d %-7.2f %-9.1f %-9.3f %-8.4f %-9.5f %-9.5f %-10.5f %-10.4f %-10.4f\n",
					s.Base, row.FieldIndex, row.FieldAngle, row.Wavelength*1e6, p.FocusMM, p.Strehl,
					p.FWHMX, p.FWHMY, p.EncircledEnergy50, p.CentroidX, p.CentroidY)
			}
		}
	}
}
