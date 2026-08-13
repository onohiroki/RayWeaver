package psf

import (
	"math"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/spectral"
	"github.com/hiroki/rayweaver/internal/types"
)

// Options configures a PSF computation.
type Options struct {
	ReferenceSurface int
	NumRays          int
	GridType         types.GridType
	GridSize         int     // image-plane pixels per side (0 = auto)
	HalfWidth        float64 // evaluation half-width mm (0 = auto)
	Polarizations    []string
	// Workers bounds the Huygens-integration and wavefront-tracing parallelism
	// (0 = runtime.NumCPU()).
	Workers int
	// SpectralCurve selects a polychromatic ("white") PSF computation: ""
	// (monochromatic), "D65" or "FLAT". When set, each field's per-wavelength
	// PSFs are combined with the SPD-weighted (and transmittance-weighted)
	// intensity sum.
	SpectralCurve string
	// SpectralEntries overrides SpectralCurve with a custom spectral power
	// distribution (wavelength nm, relative power).
	SpectralEntries []types.SpectralEntry
	// MTFCfg configures the OTF/MTF computation (nil = defaults).
	MTFCfg *types.PSFMTFConfig
	// BestFocus evaluates each field at its best-focus image plane: the plane
	// shift δ minimizing the geometric spot RMS (BestFocusShift) is applied per
	// field before the Huygens integral, removing field-curvature defocus so
	// the peak-ratio Strehl and rms_opd are wavefront-quality numbers. False =
	// evaluate at the fixed image plane (field curvature appears naturally).
	BestFocus bool
	// ConvergeCheck, when enabled, re-evaluates each (field, wavelength,
	// polarization) at a higher ray count (1.5× NumRays) to estimate sampling
	// convergence. The reported grid stays at NumRays; the comparison Strehl
	// populates Result.Converged / StrehlRelChange. Default (false) keeps the
	// internal API fast; the psf command turns it on by default.
	ConvergeCheck bool
	// ConvergeTol is the relative Strehl change threshold used by ConvergeCheck
	// (default 0.10 = 10%).
	ConvergeTol float64
}

// Result is one computed PSF with its analysis summary.
type Result struct {
	FieldIndex     int
	FieldAngle     float64
	Wavelength     float64
	Polarization   string
	Grid           *FieldGrid
	IdealPeak      float64
	Strehl         float64
	FWHMX          float64
	FWHMY          float64
	CentroidX      float64
	CentroidY      float64
	PeakValue      float64
	PeakX          float64
	PeakY          float64
	Encircled50    float64
	AiryRadius     float64
	ImageNA        float64
	SpotRMS        float64
	RMSOPD         float64 // wavefront error RMS relative to the focus reference (mm)
	PVOPD          float64 // wavefront error peak-to-valley (mm)
	Stats          WavefrontStats
	// RawIntensitySum is the unnormalized window energy Σ (I·Δx·Δy) before
	// Normalize.
	RawIntensitySum float64
	// Transmittance is the fraction of the reference-surface power captured
	// in the sampled window: Σ(I·Δx·Δy) / Σ(|E|²·ΔA_ref).
	Transmittance float64
	// SpectralCurve names the SPD used; non-empty for polychromatic results.
	SpectralCurve string
	// BestFocusShift is the applied image-plane shift (mm) when opts.BestFocus
	// was set (0 when evaluated at the fixed plane).
	BestFocusShift float64
	// Converged is true when the sampling-convergence check was enabled and the
	// Strehl at NumRays differed from the higher-ray-count re-evaluation by less
	// than ConvergeTol. StrehlRelChange is the relative change (|s2-s1|/max(s1,eps)).
	// CheckRays is the higher ray count used for the check (0 when disabled).
	Converged       bool
	StrehlRelChange float64
	CheckRays       int
	// Contributions lists each wavelength's weighted share of a
	// polychromatic result (empty for monochromatic results).
	Contributions []WavelengthContribution
	// MTF is the OTF/MTF summary of the result's PSF grid.
	MTF *types.PSFMTFSummary
}

// WavelengthContribution is one wavelength's weighted share of a
// polychromatic PSF.
type WavelengthContribution struct {
	Wavelength     float64
	SpectralWeight float64 // raw SPD weight
	Transmittance  float64
	PSFEnergy      float64 // weight·(window energy)
	CentroidX      float64
	CentroidY      float64
	Grid           *FieldGrid
	MTF            *types.PSFMTFSummary
}

// polState is one resolved coherent input state.
type polState struct {
	label    string
	jones    types.JonesVector
	combined bool // intensity-averaged with the following sibling (RCP+LCP)
}

func resolvePolStates(labels []string) []polState {
	if len(labels) == 0 {
		return []polState{{label: string(types.PolRCP), jones: types.NewCircularJones(true)}}
	}
	var out []polState
	for _, l := range labels {
		switch types.PolarizationLabel(l) {
		case types.PolLCP:
			out = append(out, polState{label: string(types.PolLCP), jones: types.NewCircularJones(false)})
		case types.PolX:
			out = append(out, polState{label: string(types.PolX), jones: types.NewLinearJones(0)})
		case types.PolY:
			out = append(out, polState{label: string(types.PolY), jones: types.NewLinearJones(90)})
		case types.PolRCPLCP:
			out = append(out,
				polState{label: string(types.PolRCP), jones: types.NewCircularJones(true), combined: true},
				polState{label: string(types.PolLCP), jones: types.NewCircularJones(false), combined: true})
		default: // RCP
			out = append(out, polState{label: string(types.PolRCP), jones: types.NewCircularJones(true)})
		}
	}
	return out
}

// Compute runs the full PSF pipeline: for every (field, wavelength,
// polarization) it samples the entrance pupil, traces the polarized wavefront
// to the reference surface, and integrates it onto the flat image plane via
// the direct vector Huygens integral. When opts.SpectralCurve is set (or
// spectral entries are given), each field's monochromatic PSFs are combined
// into one polychromatic result per polarization state.
func Compute(system types.System, gc *glass.Catalog, fields []types.FieldDef,
	wavelengths []float64, opts Options) ([]Result, error) {
	engine := ray.NewEngine(gc, nil)
	if opts.ReferenceSurface <= 0 {
		opts.ReferenceSurface = DefaultReferenceSurface(system.Surfaces)
	}
	if opts.NumRays <= 0 {
		opts.NumRays = 400
	}
	if opts.GridSize <= 0 {
		opts.GridSize = 64
	}
	if opts.GridType == "" {
		opts.GridType = types.GridPolar
	}
	if len(wavelengths) == 0 {
		wavelengths = []float64{types.DefaultWavelength}
	}
	pols := resolvePolStates(opts.Polarizations)
	planeZ := imagePlaneZ(system.Surfaces)

	white := opts.SpectralCurve != "" || len(opts.SpectralEntries) > 0
	var spdCurve *spectral.Curve
	if white {
		switch {
		case len(opts.SpectralEntries) > 0:
			nm := make([]float64, len(opts.SpectralEntries))
			rel := make([]float64, len(opts.SpectralEntries))
			for i, e := range opts.SpectralEntries {
				nm[i] = e.Wavelength
				rel[i] = e.Relative
			}
			spdCurve = spectral.NewCurve(nm, rel)
		case opts.SpectralCurve == "FLAT":
			l := minFloat(wavelengths)
			h := maxFloat(wavelengths)
			spdCurve = spectral.Flat(l*1e6, h*1e6)
		default: // "D65"
			spdCurve = spectral.D65()
		}
	}

	var results []Result
	for fi, fd := range fields {
		if white {
			results = append(results, whiteField(engine, gc, system, fd, opts, planeZ, wavelengths, fi, spdCurve, pols)...)
			continue
		}
		for _, wl := range wavelengths {
			pg, err := ComputeFieldGrid(system, gc, fd, opts.ReferenceSurface, opts.NumRays, wl, opts.GridType)
			if err != nil || len(pg.GridPoints) == 0 {
				continue
			}
			nImage := imageSpaceIndex(system.Surfaces, opts.ReferenceSurface, wl, gc)
			fieldAngle := angleFromDir(pg.ChiefDir)

			for pi := 0; pi < len(pols); pi++ {
				st := pols[pi]
				if !st.combined {
					r := computeOne(engine, gc, system, pg, fd, opts, planeZ, nImage, wl, fieldAngle, fi, st.label, st.jones)
					if r != nil {
						results = append(results, *r)
					}
					continue
				}
				// Combined RCP+LCP: average the sibling intensities.
				if pi+1 >= len(pols) || !pols[pi+1].combined {
					continue
				}
				st2 := pols[pi+1]
				r := computeCombined(engine, gc, system, pg, fd, opts, planeZ, nImage, wl, fieldAngle, fi, st.jones, st2.jones)
				if r != nil {
					results = append(results, *r)
				}
				pi++
			}
		}
	}
	return results, nil
}

// computeOne computes and analyses the PSF for a single coherent state.
func computeOne(engine *ray.Engine, gc *glass.Catalog, system types.System, pg *PupilGrid, fd types.FieldDef,
	opts Options, planeZ, nImage, wl, fieldAngle float64, fi int, label string, pol types.JonesVector) *Result {
	samples, stats := TraceWavefront(system, engine, pg, fd, opts.ReferenceSurface, wl, pol, opts.Workers)
	if len(samples) < 3 {
		return nil
	}
	evaluateZ := planeZ
	if opts.BestFocus {
		evaluateZ = planeZ + BestFocusShift(samples, planeZ)
	}
	cx, cy, _ := ImagePlaneSpot(samples, evaluateZ)
	center := types.Vec3{X: cx, Y: cy, Z: evaluateZ}
	spec := DefaultImageGrid(samples, center, nImage, wl, evaluateZ, cx, cy, opts.HalfWidth, opts.GridSize)

	// Evaluate actual + ideal in one shared pass (geometry computed once).
	pair := fieldPair{samples: samples, center: center, actual: NewFieldGrid(spec), ideal: NewFieldGrid(spec)}
	computePairs([]fieldPair{pair}, evaluateZ, nImage, wl, spec, opts.Workers)
	r := finishResult(pair.actual, pair.ideal, samples, center, nImage, wl, evaluateZ, fieldAngle, fi, label, stats, samplePower(samples))
	r.BestFocusShift = evaluateZ - planeZ
	r.MTF = ComputeMTF(r.Grid.Intensity, r.Grid.Spec, opts.MTFCfg)
	applyConvergence(r, engine, system, gc, fd, opts, planeZ, nImage, wl, fieldAngle, fi, pol)
	return r
}

// computeCombined computes the PSF for two coherent states (RCP and LCP) and
// averages their intensities incoherently. The two wavefronts are traced
// concurrently, then their actual/ideal grids are evaluated together through a
// single shared row-parallel pool.
func computeCombined(engine *ray.Engine, gc *glass.Catalog, system types.System, pg *PupilGrid, fd types.FieldDef,
	opts Options, planeZ, nImage, wl, fieldAngle float64, fi int, pol1, pol2 types.JonesVector) *Result {
	type wfRes struct {
		samples []WavefrontSample
		stats   WavefrontStats
	}
	ch := make(chan wfRes, 2)
	go func() {
		s, st := TraceWavefront(system, engine, pg, fd, opts.ReferenceSurface, wl, pol1, opts.Workers)
		ch <- wfRes{s, st}
	}()
	go func() {
		s, st := TraceWavefront(system, engine, pg, fd, opts.ReferenceSurface, wl, pol2, opts.Workers)
		ch <- wfRes{s, st}
	}()
	r1 := <-ch
	r2 := <-ch
	if len(r1.samples) < 3 || len(r2.samples) < 3 {
		return nil
	}
	evaluateZ := planeZ
	if opts.BestFocus {
		evaluateZ = planeZ + BestFocusShift(r1.samples, planeZ)
	}
	cx, cy, _ := ImagePlaneSpot(r1.samples, evaluateZ)
	center := types.Vec3{X: cx, Y: cy, Z: evaluateZ}
	spec := DefaultImageGrid(r1.samples, center, nImage, wl, evaluateZ, cx, cy, opts.HalfWidth, opts.GridSize)

	pairs := []fieldPair{
		{samples: r1.samples, center: center, actual: NewFieldGrid(spec), ideal: NewFieldGrid(spec)},
		{samples: r2.samples, center: center, actual: NewFieldGrid(spec), ideal: NewFieldGrid(spec)},
	}
	computePairs(pairs, evaluateZ, nImage, wl, spec, opts.Workers)

	// Incoherent sum of intensities.
	comb := NewFieldGrid(spec)
	idealComb := NewFieldGrid(spec)
	for i := range comb.Intensity {
		comb.Intensity[i] = pairs[0].actual.Intensity[i] + pairs[1].actual.Intensity[i]
		idealComb.Intensity[i] = pairs[0].ideal.Intensity[i] + pairs[1].ideal.Intensity[i]
	}
	stats := r1.stats
	stats.Total += r2.stats.Total
	stats.Valid += r2.stats.Valid
	stats.Missed += r2.stats.Missed
	r := finishResult(comb, idealComb, r1.samples, center, nImage, wl, evaluateZ, fieldAngle, fi,
		string(types.PolRCPLCP), stats, samplePower(r1.samples)+samplePower(r2.samples))
	r.BestFocusShift = evaluateZ - planeZ
	r.MTF = ComputeMTF(r.Grid.Intensity, r.Grid.Spec, opts.MTFCfg)
	applyConvergenceCombined(r, engine, system, gc, fd, opts, planeZ, nImage, wl, fieldAngle, fi, pol1, pol2)
	return r
}

// checkRayCount returns a strictly higher ray count used for the convergence
// re-evaluation: 1.5× NumRays rounded up, at least NumRays+1.
func checkRayCount(n int) int {
	if n <= 0 {
		n = 1
	}
	hi := int(math.Ceil(1.5 * float64(n)))
	if hi <= n {
		hi = n + 1
	}
	return hi
}

// applyConvergence fills a result's sampling-convergence fields by re-evaluating
// the same coherent state at a higher ray count. No-op when opts.ConvergeCheck
// is false (the reported grid stays at NumRays; the check only labels
// reliability). threshold defaults to ConvergeTol (10%).
func applyConvergence(r *Result, engine *ray.Engine, system types.System, gc *glass.Catalog,
	fd types.FieldDef, opts Options, planeZ, nImage, wl, fieldAngle float64, fi int, pol types.JonesVector) {
	if !opts.ConvergeCheck {
		return
	}
	checkRays := checkRayCount(opts.NumRays)
	co := opts
	co.NumRays = checkRays
	co.ConvergeCheck = false
	pg, err := ComputeFieldGrid(system, gc, fd, opts.ReferenceSurface, checkRays, wl, opts.GridType)
	if err != nil || len(pg.GridPoints) == 0 {
		r.Converged = false
		r.CheckRays = checkRays
		r.StrehlRelChange = 1.0
		return
	}
	r2 := computeOne(engine, gc, system, pg, fd, co, planeZ, nImage, wl, fieldAngle, fi, r.Polarization, pol)
	setConvergence(r, r2, checkRays, opts.ConvergeTol)
}

// applyConvergenceCombined is applyConvergence for an RCP+LCP (incoherently
// averaged) result.
func applyConvergenceCombined(r *Result, engine *ray.Engine, system types.System, gc *glass.Catalog,
	fd types.FieldDef, opts Options, planeZ, nImage, wl, fieldAngle float64, fi int, pol1, pol2 types.JonesVector) {
	if !opts.ConvergeCheck {
		return
	}
	checkRays := checkRayCount(opts.NumRays)
	co := opts
	co.NumRays = checkRays
	co.ConvergeCheck = false
	pg, err := ComputeFieldGrid(system, gc, fd, opts.ReferenceSurface, checkRays, wl, opts.GridType)
	if err != nil || len(pg.GridPoints) == 0 {
		r.Converged = false
		r.CheckRays = checkRays
		r.StrehlRelChange = 1.0
		return
	}
	r2 := computeCombined(engine, gc, system, pg, fd, co, planeZ, nImage, wl, fieldAngle, fi, pol1, pol2)
	setConvergence(r, r2, checkRays, opts.ConvergeTol)
}

// setConvergence computes the relative Strehl change between the reported
// result and the higher-ray-count re-evaluation and labels convergence.
func setConvergence(r *Result, r2 *Result, checkRays int, tol float64) {
	r.CheckRays = checkRays
	if r2 == nil {
		r.Converged = false
		r.StrehlRelChange = 1.0
		return
	}
	s1, s2 := r.Strehl, r2.Strehl
	rel := 0.0
	den := math.Max(math.Abs(s1), math.Abs(s2))
	if den > 1e-12 {
		rel = math.Abs(s2-s1) / den
	} else if s1 != s2 {
		rel = 1.0
	}
	if tol <= 0 {
		tol = 0.10
	}
	r.StrehlRelChange = rel
	r.Converged = rel <= tol
}

// whiteField computes the polychromatic PSF for one field. Every polarization
// state in pols yields one result; a combined state (2 entries, RCP+LCP) is
// averaged incoherently per wavelength on the shared image grid. All
// wavelengths share one evaluation window (centred on the reference
// wavelength's spot, sized to the largest diffraction + spot envelope) so the
// SPD-weighted intensities can be summed sample by sample.
func whiteField(engine *ray.Engine, gc *glass.Catalog, system types.System, fd types.FieldDef,
	opts Options, planeZ float64, wavelengths []float64, fi int, spdCurve *spectral.Curve,
	pols []polState) []Result {
	var results []Result
	for pi := 0; pi < len(pols); pi++ {
		st := pols[pi]
		var group []polState
		if st.combined {
			if pi+1 >= len(pols) || !pols[pi+1].combined {
				continue
			}
			group = []polState{st, pols[pi+1]}
			pi++
		} else {
			group = []polState{st}
		}
		if r := whiteGroup(engine, gc, system, fd, opts, planeZ, wavelengths, fi, spdCurve, group); r != nil {
			results = append(results, *r)
		}
	}
	return results
}

// whiteGroup combines the given polarization group's per-wavelength
// monochromatic PSFs into one polychromatic result. group holds one coherent
// state or the two members of an RCP+LCP average.
func whiteGroup(engine *ray.Engine, gc *glass.Catalog, system types.System, fd types.FieldDef,
	opts Options, planeZ float64, wavelengths []float64, fi int, spdCurve *spectral.Curve,
	group []polState) *Result {

	type traceData struct {
		wl       float64
		nImage   float64
		chiefDir types.Vec3
		samples  [][]WavefrontSample
		stats    []WavefrontStats
	}
	var tds []traceData
	for _, wl := range wavelengths {
		if spdCurve.Weight(wl) <= 0 {
			continue
		}
		pg, err := ComputeFieldGrid(system, gc, fd, opts.ReferenceSurface, opts.NumRays, wl, opts.GridType)
		if err != nil || len(pg.GridPoints) == 0 {
			continue
		}
		nImage := imageSpaceIndex(system.Surfaces, opts.ReferenceSurface, wl, gc)
		samples := make([][]WavefrontSample, len(group))
		stats := make([]WavefrontStats, len(group))
		ok := false
		for p := range group {
			s, st := TraceWavefront(system, engine, pg, fd, opts.ReferenceSurface, wl, group[p].jones, opts.Workers)
			samples[p] = s
			stats[p] = st
			if len(s) >= 3 {
				ok = true
			}
		}
		if !ok {
			continue
		}
		tds = append(tds, traceData{wl: wl, nImage: nImage, chiefDir: pg.ChiefDir, samples: samples, stats: stats})
	}
	if len(tds) == 0 {
		return nil
	}

	// Reference wavelength: the first traced one. Common window centred on
	// its spot, sized to the largest diffraction + geometric-spot envelope
	// across all wavelengths.
	refIdx := firstValidPol(tds[0].samples)
	if refIdx < 0 {
		return nil
	}
	ref := tds[0]
	evaluateZ := planeZ
	if opts.BestFocus {
		evaluateZ = planeZ + BestFocusShift(ref.samples[refIdx], planeZ)
	}
	cx, cy, _ := ImagePlaneSpot(ref.samples[refIdx], evaluateZ)
	center := types.Vec3{X: cx, Y: cy, Z: evaluateZ}
	half := 0.0
	if opts.HalfWidth > 0 {
		half = math.Max(opts.HalfWidth, 5e-3)
	} else {
		for _, td := range tds {
			for _, s := range td.samples {
				if len(s) < 3 {
					continue
				}
				na := ComputeImageNA(s, center, td.nImage)
				_, _, rms := ImagePlaneSpot(s, evaluateZ)
				h := math.Max(4*AiryRadius(td.wl, na), 3*rms)
				if h > half {
					half = h
				}
			}
		}
	}
	if half < 5e-3 {
		half = 5e-3
	}
	spec := DefaultImageGrid(ref.samples[refIdx], center, ref.nImage, ref.wl, evaluateZ, cx, cy, half, opts.GridSize)

	label := group[0].label
	if len(group) == 2 {
		label = string(types.PolRCPLCP)
	}

	whiteGrid := NewFieldGrid(spec)
	idealWhite := NewFieldGrid(spec)
	var contributions []WavelengthContribution
	stats := WavefrontStats{}
	var refPowerTotal, windowPowerTotal, whiteRawSum float64

	for _, td := range tds {
		// Evaluate each polarization on the shared grid, then combine
		// incoherently (group of 1 or 2).
		act := NewFieldGrid(spec)
		ide := NewFieldGrid(spec)
		for p := range group {
			if len(td.samples[p]) < 3 {
				continue
			}
			pair := fieldPair{samples: td.samples[p], center: center, actual: NewFieldGrid(spec), ideal: NewFieldGrid(spec)}
			computePairs([]fieldPair{pair}, evaluateZ, td.nImage, td.wl, spec, opts.Workers)
			for idx := range act.Intensity {
				act.Intensity[idx] += pair.actual.Intensity[idx]
				ide.Intensity[idx] += pair.ideal.Intensity[idx]
			}
		}

		w := spdCurve.Weight(td.wl)
		rp := 0.0
		for p := range group {
			rp += samplePower(td.samples[p])
		}
		wp := gridWindowPower(act)
		tau := 0.0
		if rp > 0 {
			tau = wp / rp
		}
		weight := w * tau
		for idx := range whiteGrid.Intensity {
			whiteGrid.Intensity[idx] += weight * act.Intensity[idx]
			idealWhite.Intensity[idx] += weight * ide.Intensity[idx]
		}
		refPowerTotal += w * rp
		windowPowerTotal += w * wp
		whiteRawSum += weight * wp
		for p := range group {
			stats.Total += td.stats[p].Total
			stats.Valid += td.stats[p].Valid
			stats.Missed += td.stats[p].Missed
		}
		cxw, cyw := act.Centroid()
		contributions = append(contributions, WavelengthContribution{
			Wavelength:     td.wl,
			SpectralWeight: w,
			Transmittance:  tau,
			PSFEnergy:      weight * wp,
			CentroidX:      cxw,
			CentroidY:      cyw,
			Grid:           act,
			MTF:            ComputeMTF(act.Intensity, spec, opts.MTFCfg),
		})
	}
	if len(contributions) == 0 {
		return nil
	}

	actualPeak, _, _ := whiteGrid.Peak()
	idealPeak, _, _ := idealWhite.Peak()
	strehl := 0.0
	if idealPeak > 0 {
		strehl = actualPeak / idealPeak
	}

	na := ComputeImageNA(ref.samples[refIdx], center, ref.nImage)
	_, _, spotRMS := ImagePlaneSpot(ref.samples[refIdx], evaluateZ)
	rmsOPD, pvOPD := wavefrontOPD(ref.samples[refIdx], center, ref.nImage)

	whiteGrid.Normalize()
	peakVal, peakX, peakY := whiteGrid.Peak()
	wcx, wcy := whiteGrid.Centroid()
	fx, fy := whiteGrid.FWHM()
	ee50 := whiteGrid.RadiusForEnergy(wcx, wcy, 0.5)

	transmittance := 0.0
	if refPowerTotal > 0 {
		transmittance = windowPowerTotal / refPowerTotal
	}

	return &Result{
		FieldIndex:      fi,
		FieldAngle:      angleFromDir(ref.chiefDir),
		Wavelength:      ref.wl,
		Polarization:    label,
		Grid:            whiteGrid,
		IdealPeak:       idealPeak,
		Strehl:          strehl,
		FWHMX:           fx,
		FWHMY:           fy,
		CentroidX:       wcx,
		CentroidY:       wcy,
		PeakValue:       peakVal,
		PeakX:           peakX,
		PeakY:           peakY,
		Encircled50:     ee50,
		AiryRadius:      AiryRadius(ref.wl, na),
		ImageNA:         na,
		SpotRMS:         spotRMS,
		RMSOPD:          rmsOPD,
		PVOPD:           pvOPD,
		Stats:           stats,
		RawIntensitySum: whiteRawSum,
		Transmittance:   transmittance,
		SpectralCurve:   opts.SpectralCurve,
		BestFocusShift:  evaluateZ - planeZ,
		Contributions:   contributions,
		MTF:             ComputeMTF(whiteGrid.Intensity, spec, opts.MTFCfg),
	}
}

// finishResult analyses the (raw) intensity grid and fills the summary. The
// grid is normalized in place. refPower is the reference-surface power the
// window fraction is measured against.
func finishResult(grid, ideal *FieldGrid, samples []WavefrontSample,
	center types.Vec3, nImage, wl, planeZ, fieldAngle float64, fi int, label string, stats WavefrontStats, refPower float64) *Result {
	rawSum := gridWindowPower(grid)
	idealPeak, _, _ := ideal.Peak()
	actualPeak, _, _ := grid.Peak()
	strehl := 0.0
	if idealPeak > 0 {
		strehl = actualPeak / idealPeak
	}
	na := ComputeImageNA(samples, center, nImage)
	_, _, spotRMS := ImagePlaneSpot(samples, planeZ)
	rmsOPD, pvOPD := wavefrontOPD(samples, center, nImage)

	grid.Normalize()
	peakVal, peakX, peakY := grid.Peak()
	cx, cy := grid.Centroid()
	fx, fy := grid.FWHM()
	ee50 := grid.RadiusForEnergy(cx, cy, 0.5)
	transmittance := 0.0
	if refPower > 0 {
		transmittance = rawSum / refPower
	}

	return &Result{
		FieldIndex:      fi,
		FieldAngle:      fieldAngle,
		Wavelength:      wl,
		Polarization:    label,
		Grid:            grid,
		IdealPeak:       idealPeak,
		Strehl:          strehl,
		FWHMX:           fx,
		FWHMY:           fy,
		CentroidX:       cx,
		CentroidY:       cy,
		PeakValue:       peakVal,
		PeakX:           peakX,
		PeakY:           peakY,
		Encircled50:     ee50,
		AiryRadius:      AiryRadius(wl, na),
		ImageNA:         na,
		SpotRMS:         spotRMS,
		RMSOPD:          rmsOPD,
		PVOPD:           pvOPD,
		Stats:           stats,
		RawIntensitySum: rawSum,
		Transmittance:   transmittance,
		MTF:             ComputeMTF(grid.Intensity, grid.Spec, nil),
	}
}

// samplePower is the total power incident on the reference surface:
// Σ |E|²·ΔA over the wavefront samples.
func samplePower(samples []WavefrontSample) float64 {
	var p float64
	for _, s := range samples {
		p += s.Intensity * s.Area
	}
	return p
}

// gridWindowPower is the unnormalized window energy Σ (I·Δx·Δy).
func gridWindowPower(g *FieldGrid) float64 {
	if g == nil || g.Spec.DX <= 0 || g.Spec.DY <= 0 {
		return 0
	}
	var p float64
	for _, v := range g.Intensity {
		p += v
	}
	return p * g.Spec.DX * g.Spec.DY
}

// firstValidPol returns the index of the first polarization group member with
// a usable (≥3) sample set, or -1.
func firstValidPol(samples [][]WavefrontSample) int {
	for i, s := range samples {
		if len(s) >= 3 {
			return i
		}
	}
	return -1
}

func minFloat(a []float64) float64 {
	if len(a) == 0 {
		return 0
	}
	m := a[0]
	for _, v := range a[1:] {
		if v < m {
			m = v
		}
	}
	return m
}

func maxFloat(a []float64) float64 {
	if len(a) == 0 {
		return 0
	}
	m := a[0]
	for _, v := range a[1:] {
		if v > m {
			m = v
		}
	}
	return m
}

// wavefrontOPD computes the OPD of each sample relative to the perfect
// converging sphere to the given focus: OPD_j = OPL_j + n·|q_j - focus|.
// A best-fit reference sphere (piston + tilt + defocus) is then subtracted so
// the reported values are the true wavefront aberration, not the absolute
// optical path (which for an angle-based field is dominated by the launch
// geometry). Returns the residual RMS and peak-to-valley in mm.
func wavefrontOPD(samples []WavefrontSample, focus types.Vec3, nImage float64) (rms, pv float64) {
	opds := make([]float64, len(samples))
	for i, s := range samples {
		opds[i] = s.OPL + nImage*s.Position.Subtract(focus).Length()
	}

	// Least-squares fit of opd = a + b·x + c·y + d·(x²+y²) on the reference
	// surface. The residual is the wavefront aberration.
	type row struct{ x, y, r2, o float64 }
	n := len(opds)
	if n == 0 {
		return 0, 0
	}
	rows := make([]row, n)
	for i, s := range samples {
		rows[i] = row{s.Position.X, s.Position.Y, s.Position.X*s.Position.X + s.Position.Y*s.Position.Y, opds[i]}
	}
	var m [4][5]float64 // augmented normal matrix
	for _, r := range rows {
		cols := []float64{1, r.x, r.y, r.r2}
		for i := 0; i < 4; i++ {
			for j := 0; j < 4; j++ {
				m[i][j] += cols[i] * cols[j]
			}
			m[i][4] += cols[i] * r.o
		}
	}
	coef, ok := solve4x4(m)
	if !ok {
		return 0, 0
	}
	minV, maxV := math.Inf(1), math.Inf(-1)
	sumSq := 0.0
	for _, r := range rows {
		fit := coef[0] + coef[1]*r.x + coef[2]*r.y + coef[3]*r.r2
		d := r.o - fit
		sumSq += d * d
		if d < minV {
			minV = d
		}
		if d > maxV {
			maxV = d
		}
	}
	rms = math.Sqrt(sumSq / float64(n))
	pv = maxV - minV
	return rms, pv
}

// solve4x4 solves a 4x4 linear system given as an augmented matrix
// [A|b] (m[i][4] is the right-hand side). Returns false when singular.
func solve4x4(m [4][5]float64) ([4]float64, bool) {
	for col := 0; col < 4; col++ {
		best := col
		for r := col + 1; r < 4; r++ {
			if math.Abs(m[r][col]) > math.Abs(m[best][col]) {
				best = r
			}
		}
		m[col], m[best] = m[best], m[col]
		piv := m[col][col]
		if math.Abs(piv) < 1e-15 {
			return [4]float64{}, false
		}
		for r := 0; r < 4; r++ {
			if r == col {
				continue
			}
			f := m[r][col] / piv
			for c := col; c < 5; c++ {
				m[r][c] -= f * m[col][c]
			}
		}
	}
	var x [4]float64
	for i := 0; i < 4; i++ {
		x[i] = m[i][4] / m[i][i]
	}
	return x, true
}

// angleFromDir returns the field angle (degrees) of a direction relative to
// the optical axis.
func angleFromDir(d types.Vec3) float64 {
	perp := math.Hypot(d.X, d.Y)
	return math.Atan2(perp, d.Z) * 180 / math.Pi
}