package optimize

import (
	"fmt"
	"math"
	"runtime"
	"sort"
	"sync"

	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/psf"
	"github.com/hiroki/rayweaver/internal/types"
	"github.com/hiroki/rayweaver/internal/wavefront"
)

// MeritTerm is a pre-resolved single-config merit term (weights and field
// angle already matched against the fields/wavelengths).
type MeritTerm struct {
	Kind        string
	FieldAngle  float64
	FieldDir    []float64
	FieldWeight float64
	// FieldIndex is the 0-based position of the term's field in the config's
	// Fields list (-1 = the field was not found). The wavefront kinds use it to
	// carry the field's declared vignetting into the pupil-grid clip, and a
	// field declared by image_height is resolved to its angle through it.
	FieldIndex           int
	Wavelength           float64
	ComparisonWavelength float64
	WavWeight            float64
	// Wavelengths carries the resolved set for the aggregate polychromatic
	// kinds and secondary_spectrum; nil for ordinary per-wavelength terms.
	Wavelengths []types.WavelengthSample
	Weight      float64
	Target      float64
	Fraction    float64
	Frequency   float64
	SurfaceSet  []int
	// Scale is an explicit normalization denominator (0 = automatic when
	// optimization.merit_normalization is enabled, else no normalization).
	Scale float64
}

// evaluateOpticalMerit evaluates the merit excluding glass-attraction and
// glass-hull penalties. Used for the merit_ratio metric and sensitivity
// computation.
func (o *Optimizer) evaluateOpticalMerit(x []float64) float64 {
	o.skipAttraction = true
	o.skipHull = true
	defer func() {
		o.skipAttraction = false
		o.skipHull = false
	}()
	return o.EvaluateMerit(x)
}

// UpdateMeritWeights implements dls.MeritScheduleUpdater: recompute the mode
// weights from the state metric at the current x. Called once per DLS iteration
// at the current x, so the weights stay frozen within one iteration and the
// Jacobian matches the merit actually minimised.
func (o *Optimizer) UpdateMeritWeights(x []float64, iter int) {
	// Glass attraction weight ramp (runs independently of merit_schedule).
	if o.catalogField != nil && o.catalogField.CatalogFieldCount() > 0 {
		if o.attractionMetric == "run_iteration" {
			o.attractionRunIter++
		}
		s := o.attractionScheduleMetric(x, iter)
		// Normalise metric to [0,1] via anchor range.
		t := 0.5
		span := o.attractionAnchorTo - o.attractionAnchorFrom
		if math.Abs(span) > 1e-15 {
			t = (s - o.attractionAnchorFrom) / span
			if t < 0 {
				t = 0
			}
			if t > 1 {
				t = 1
			}
		}
		f := scheduleCurve(o.attractionCurve, t)
		o.attractionWeight = o.attractionWeightFrom + (o.attractionWeightTo-o.attractionWeightFrom)*f

		// Sensitivity weighting: per-pair elasticity via finite differences.
		if o.sensitivityWeighted && len(o.hullPairs) > 0 && len(o.attractionScales) > 0 {
			o.updateAttractionSensitivity(x)
		}
	}

	if o.meritSchedule == nil {
		return
	}
	s := o.scheduleMetric(x, iter)
	o.lastMetric = s
	prev := dominantMode(o.modeWeights)
	cur := o.setModeWeightsAt(s)
	if cur != prev {
		o.modeChanges++
		if o.logger != nil {
			if ml, ok := o.logger.(dls.ModeChangeLogger); ok {
				ml.LogModeChange(iter, prev, cur, copyWeights(o.modeWeights), s)
			}
		}
	}
	o.numRays = o.resolveScheduledNumRays()
	o.updateBackFocusType()
}

// OpticalMerit evaluates the merit at x excluding the glass-attraction and
// glass-hull penalties. Used to compare states on optics alone (e.g. the
// before/after cost of snapping to real glass).
func (o *Optimizer) OpticalMerit(x []float64) float64 {
	return o.evaluateOpticalMerit(x)
}

func buildMeritTermFromTypes(t types.MeritTerm, ci ConfigInput, gc *glass.Catalog) meritTerm {
	if (t.Kind == MeritAxialColor || t.Kind == MeritSecondarySpectrum) && t.ComparisonWavelength <= 0 {
		Warnf("warning: merit kind %q needs comparison_wavelength; it evaluates to 0", t.Kind)
	}
	wl := t.Wavelength
	if wl <= 0 {
		wl = effectiveReferenceWavelength(ci.ReferenceWavelength)
	}
	mt := meritTerm{
		kind:                 t.Kind,
		fieldIndex:           -1,
		wavelength:           wl,
		comparisonWavelength: t.ComparisonWavelength,
		weight:               t.Weight,
		target:               t.Target,
		fraction:             t.Fraction,
		frequency:            t.Frequency,
		surfaceSet:           append([]int(nil), t.SurfaceSet...),
		wavelengths:          append([]types.WavelengthSample(nil), t.ResolvedWavelengths...),
		fieldDirX:            0,
		fieldDirY:            1,
		fieldWeight:          1.0,
		wavWeight:            1.0,
	}
	for i, f := range ci.Fields {
		if f.ID == t.Field {
			mt.fieldIndex = i
			if f.AngleDeg != 0 || f.ImageHeight == 0 {
				mt.fieldAngle = f.AngleDeg
			} else {
				mt.useImageHeight = true
				mt.imageHeight = f.ImageHeight
			}
			if f.Weight > 0 {
				mt.fieldWeight = f.Weight
			}
			if dx, dy, ok := fieldDir(f.Direction); ok {
				mt.fieldDirX, mt.fieldDirY = dx, dy
			}
			break
		}
	}
	if t.WavelengthWeight > 0 {
		mt.wavWeight = t.WavelengthWeight
	} else {
		for _, w := range ci.Wavelengths {
			if math.Abs(w.Value-wl) < 1e-12 {
				if w.Weight > 0 {
					mt.wavWeight = w.Weight
				}
				break
			}
		}
	}
	mt.normScale = resolveNormScale(t, ci, gc)
	return mt
}

// gridKey identifies a unique pupil-grid trace within a single merit
// evaluation. Within one evaluation the surfaces, frozen pupil, grid geometry
// and per-field angle are constant, so every grid merit term sharing a
// (field, wavelength) traces the identical grid. fieldIndex is part of the key
// because the field also carries the vignetting ellipse: two fields with the
// same angle but different declared vignetting must not share a trace.
type gridKey struct {
	configID   string
	fieldIndex int
	fieldAngle float64
	wavelength float64
}

// evalGridCache is a per-evaluation cache of pupil-grid traces. The DLS solver
// calls ComputeResiduals concurrently for one goroutine per Jacobian column,
// so the cache must be created fresh inside each evaluation call (never stored
// on the Optimizer) to stay race-free; the cached traces are pure functions of
// the evaluation's surfaces and frozen pupil.
type evalGridCache struct {
	spots     map[gridKey][]dls.IPoint
	extents   map[gridKey]map[int]float64
	wavefront map[wfKey]*wavefront.Entry
	// diffraction caches the diffraction-MTF sagittal/tangential pair per
	// (field, wavelength, frequency): one trace + Huygens integration feeds a
	// sag+tan term pair.
	diffraction map[diffractionMTFKey]diffractionMTFValue
	// spotScales memoises the paraxial focus scale B (mm) per (config,
	// wavelength) — a pure function of the evaluation's surfaces — used to turn
	// a spot slope into an equivalent plane shift (spot_flatness /
	// spot_astigmatism).
	spotScales map[flatKey]float64
	// spotMeans memoises the field-weighted mean defocus (mm) per (config,
	// wavelength) that spot_flatness measures against.
	spotMeans map[flatKey]float64
	// whitePSF caches one polychromatic PSF per (config, field, angle,
	// reference surface, wavelength set), shared by the white Strehl and MTF
	// terms. The MTF is derived from the cached grid at the requested
	// frequency, so two frequencies do not repeat the Huygens integral.
	whitePSF map[whitePSFKey]*psf.Result
}

// whitePSFKey identifies one polychromatic PSF evaluation. setKey is a stable
// rendering of the resolved wavelength/weight list; different sets must not
// share a result.
type whitePSFKey struct {
	configID   string
	fieldIndex int
	angle      float64
	refSurface int
	setKey     string
}

// flatKey identifies an across-field combination within one merit evaluation:
// the spot_flatness reference plane is per (config, wavelength).
type flatKey struct {
	configID   string
	wavelength float64
}

// diffractionMTFKey identifies one diffraction-MTF evaluation: the same pupil
// key as a spot grid plus the term's spatial frequency (a second term at
// another frequency needs its own FFT).
type diffractionMTFKey struct {
	gridKey
	frequency float64
}

// diffractionMTFValue is the cached sagittal/tangential pair.
type diffractionMTFValue struct {
	sag, tan float64
}

// wfKey identifies a wavefront analysis within a single merit evaluation.
// Two wavefront terms on the same (field, wavelength, refSurface) share the
// underlying trace when their frozen/dynamic pupil state matches.
type wfKey struct {
	configID   string
	angle      float64
	wavelength float64
	refSurface int
	frozen     bool
	frozenZ    float64
}

func newEvalGridCache() *evalGridCache {
	return &evalGridCache{
		spots:       make(map[gridKey][]dls.IPoint),
		extents:     make(map[gridKey]map[int]float64),
		wavefront:   make(map[wfKey]*wavefront.Entry),
		diffraction: make(map[diffractionMTFKey]diffractionMTFValue),
		spotScales:  make(map[flatKey]float64),
		spotMeans:   make(map[flatKey]float64),
		whitePSF:    make(map[whitePSFKey]*psf.Result),
	}
}

// gridForTerm returns the cached (or freshly traced) pupil grid for a grid
// merit term. A nil cache disables caching. p carries the per-call
// virtual-entrance-pupil values (see applyVariables).
func (o *Optimizer) gridForTerm(cache *evalGridCache, gc *glass.Catalog, surfaces []types.Surface, cfg *config, term *meritTerm, p appliedPupil) []dls.IPoint {
	angle := o.termFieldAngle(cfg, term, surfaces, gc)
	trace := func() []dls.IPoint {
		pupilZ := o.gridCentring(cfg, p, angle)
		points, _ := dls.TraceFieldGrid(gc, surfaces, cfg.stopSurface, pupilZ, angle, []float64{0, 1}, term.wavelength, o.apertureMargin, o.numRays, o.gridRotation, o.gridWorkers(), p.dia, o.fieldVignetting(cfg, term))
		return points
	}
	if cache == nil {
		return trace()
	}
	key := gridKey{configID: cfg.id, fieldIndex: term.fieldIndex, fieldAngle: angle, wavelength: term.wavelength}
	if pts, ok := cache.spots[key]; ok {
		return pts
	}
	pts := trace()
	cache.spots[key] = pts
	return pts
}

// precomputeGrids traces all grid merit terms for cfg in parallel, storing the
// results in cache. This avoids redundant traces when multiple terms share the
// same (field, wavelength) key, and the parallel trace overlaps the CPU-bound
// ray-tracing across cores. The caller must have already called
// sizeAutoApertures (which may also populate cache.extents).
func (o *Optimizer) precomputeGrids(cfg *config, surfaces []types.Surface, gc *glass.Catalog, cache *evalGridCache, p appliedPupil) {
	// Collect unique grid keys from the scheduled terms.
	type traceJob struct {
		key   gridKey
		angle float64
		wl    float64
		vig   *types.VignettingDef
	}
	seen := make(map[gridKey]bool)
	var jobs []traceJob
	enqueue := func(t *meritTerm) {
		angle := o.termFieldAngle(cfg, t, surfaces, gc)
		key := gridKey{configID: cfg.id, fieldIndex: t.fieldIndex, fieldAngle: angle, wavelength: t.wavelength}
		if seen[key] {
			return
		}
		seen[key] = true
		jobs = append(jobs, traceJob{key: key, angle: angle, wl: t.wavelength, vig: o.fieldVignetting(cfg, t)})
	}
	for _, st := range o.scheduledTerms(cfg) {
		if !isGridTraceKind(st.term.kind) {
			continue
		}
		enqueue(st.term)
		// spot_flatness measures its own field against the mean over ALL of the
		// config's fields at the same wavelength, so those grids are needed as
		// well: pre-trace them here (in parallel) rather than letting the mean
		// pull them one at a time from inside the term evaluation.
		if st.term.kind == dls.MeritSpotFlatness {
			for i := range cfg.fields {
				t := flatnessFieldTerm(cfg, st.term, i)
				enqueue(&t)
			}
		}
		// The polychromatic spot kinds trace one grid per wavelength sample.
		if st.term.kind == MeritPolychromaticSpot || st.term.kind == MeritPolychromaticEE {
			for _, s := range st.term.wavelengths {
				t := *st.term
				t.wavelength = s.Value
				enqueue(&t)
			}
		}
	}
	if len(jobs) == 0 {
		return
	}

	// Trace in parallel with bounded concurrency.
	workers := o.gridWorkers()
	if workers > len(jobs) {
		workers = len(jobs)
	}
	ch := make(chan traceJob, len(jobs))
	for _, j := range jobs {
		ch <- j
	}
	close(ch)

	var mu sync.Mutex
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range ch {
				gridTraceSem <- struct{}{}
				pupilZ := o.gridCentring(cfg, p, job.angle)
				points, _ := dls.TraceFieldGrid(gc, surfaces, cfg.stopSurface, pupilZ, job.angle, []float64{0, 1}, job.wl, o.apertureMargin, o.numRays, o.gridRotation, 1, p.dia, job.vig)
				<-gridTraceSem
				mu.Lock()
				cache.spots[job.key] = points
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
}

// isGridKind reports whether the merit kind is evaluated by evaluateGridKind
// from the pupil-grid spot statistics. The legacy empty kind means spot_rms.
// The wavefront-shift / pair-phase kinds also consume a grid trace but have
// their own evaluators (evaluateKindTerm), so they are NOT grid kinds —
// listing them here routed them to evaluateGridKind, whose switch has no case
// for them and silently evaluated them as spot_rms.
func isGridKind(kind string) bool {
	switch kind {
	case "", dls.MeritSpotRMS, dls.MeritSpotRMST, dls.MeritSpotRMSS,
		dls.MeritSpotRMSWorst, dls.MeritSpotWeighted, dls.MeritSpotEERadius,
		dls.MeritSpotFlatness, dls.MeritSpotAstigmatism,
		MeritPolychromaticSpot, MeritPolychromaticEE:
		return true
	}
	return false
}

// isGridTraceKind reports whether the merit kind needs a pupil-grid trace,
// either directly (the grid/spot kinds) or through a dedicated grid-consuming
// evaluator (the wavefront shift / pair-phase kinds). It is the predicate for
// the grid precompute, which fills the shared spot cache.
func isGridTraceKind(kind string) bool {
	switch kind {
	case dls.MeritWavefrontShiftSag, dls.MeritWavefrontShiftTan, dls.MeritWavefrontPairPhase,
		MeritPupilFill:
		return true
	}
	return isGridKind(kind)
}

// isTraceKind reports whether a merit kind consumes a per-field pupil trace —
// the shared grid machinery or the diffraction-MTF tracer, which samples to
// the reference surface instead of the image plane and therefore must not go
// through the grid precompute. It is the predicate for the angle-fallback
// loops, which must see every trace-consuming term even when a config carries
// no field list.
func isTraceKind(kind string) bool {
	return isGridTraceKind(kind) || isDiffractionKind(kind)
}

// evaluateGridKind traces the pupil grid for a grid merit term and returns the
// term's value (the metric itself). spot_rms is the legacy metric; the new
// kinds use the flux-weighted spot statistics. The term's target is applied by
// the caller. A grid with no valid rays returns the bounded degenerate penalty
// (o.spotDegenerate) instead of the legacy 1e6 sentinel, so a fully clipped
// off-axis beam pushes the solver without exploding the merit.
func (o *Optimizer) evaluateGridKind(cfg *config, term *meritTerm, surfaces []types.Surface, gc *glass.Catalog, cache *evalGridCache, p appliedPupil) float64 {
	var points []dls.IPoint
	if term.kind != MeritPolychromaticSpot && term.kind != MeritPolychromaticEE {
		points = o.gridForTerm(cache, gc, surfaces, cfg, term, p)
	}
	var val float64
	switch term.kind {
	case dls.MeritSpotRMST:
		val, _ = dls.ComputeSpotAxisRMS(points, term.fieldDirX, term.fieldDirY)
	case dls.MeritSpotRMSS:
		_, val = dls.ComputeSpotAxisRMS(points, term.fieldDirX, term.fieldDirY)
	case dls.MeritSpotRMSWorst:
		rmsT, rmsS := dls.ComputeSpotAxisRMS(points, term.fieldDirX, term.fieldDirY)
		if rmsT > rmsS {
			val = rmsT
		} else {
			val = rmsS
		}
	case dls.MeritSpotWeighted:
		val = dls.ComputeSpotWeightedRMS(points)
	case dls.MeritSpotEERadius:
		val = dls.ComputeSpotEERadius(points, term.fraction)
	case dls.MeritSpotFlatness:
		fit := o.spotFitFromPoints(cfg, term, points, surfaces, gc, cache)
		if !fit.ok {
			return o.spotDegenerate
		}
		val = fit.defocusMM - o.spotFlatMean(cfg, term, fit.defocusMM, surfaces, gc, cache, p)
	case dls.MeritSpotAstigmatism:
		fit := o.spotFitFromPoints(cfg, term, points, surfaces, gc, cache)
		if !fit.ok {
			return o.spotDegenerate
		}
		val = fit.astigMM
	case MeritPolychromaticSpot, MeritPolychromaticEE:
		groups, weights := o.polychromaticSpotGroups(cfg, term, surfaces, gc, cache, p)
		var ok bool
		if term.kind == MeritPolychromaticEE {
			val, ok = dls.WhiteSpotEERadius(groups, weights, term.fraction)
		} else {
			val, ok = dls.WhiteSpotRMS(groups, weights)
		}
		if !ok {
			return o.spotDegenerate
		}
	default:
		val = dls.ComputeSpotRMS(points)
	}
	if val >= 1e6 {
		return o.spotDegenerate
	}
	return val
}

// polychromaticSpotGroups traces (or fetches from the evaluation cache) the
// pupil grid of every wavelength in an aggregate polychromatic spot term. A
// term without a resolved set falls back to its single wavelength, so a
// hand-written term still evaluates.
func (o *Optimizer) polychromaticSpotGroups(cfg *config, term *meritTerm, surfaces []types.Surface, gc *glass.Catalog, cache *evalGridCache, p appliedPupil) ([][]dls.IPoint, []float64) {
	if len(term.wavelengths) == 0 {
		return [][]dls.IPoint{o.gridForTerm(cache, gc, surfaces, cfg, term, p)}, []float64{1}
	}
	groups := make([][]dls.IPoint, 0, len(term.wavelengths))
	weights := make([]float64, 0, len(term.wavelengths))
	for _, s := range term.wavelengths {
		t := *term
		t.wavelength = s.Value
		groups = append(groups, o.gridForTerm(cache, gc, surfaces, cfg, &t, p))
		weights = append(weights, s.Weight)
	}
	return groups, weights
}

// spotFit is one (field, wavelength) spot-slope decomposition expressed as
// equivalent longitudinal focus quantities in mm: the field's own best-focus
// shift from the delivered image plane and the astigmatic line-foci deviation.
type spotFit struct {
	defocusMM float64
	astigMM   float64
	residual  float64
	rays      int
	ok        bool
}

// spotFitFromPoints converts a traced pupil grid's spot-slope decomposition
// (dls.ComputeSpotDecomp) into the equivalent longitudinal focus quantities:
// dz = slope * B with the paraxial focus scale B (see spotFocusScale). A
// degenerate fit (too few surviving rays, effectively collinear pupil) or an
// unresolvable scale yields ok = false, which the caller reports as the
// bounded degenerate penalty like every other grid kind.
func (o *Optimizer) spotFitFromPoints(cfg *config, term *meritTerm, points []dls.IPoint, surfaces []types.Surface, gc *glass.Catalog, cache *evalGridCache) spotFit {
	d := dls.ComputeSpotDecomp(points)
	if !d.OK {
		return spotFit{}
	}
	b := o.spotFocusScale(cfg, surfaces, gc, term.wavelength, cache)
	if !(b > 0) {
		return spotFit{}
	}
	return spotFit{
		defocusMM: d.Defocus * b,
		astigMM:   d.Astig * b,
		residual:  d.Residual,
		rays:      d.Count,
		ok:        true,
	}
}

// spotFitFor is spotFitFromPoints for an arbitrary term of the config: it
// traces (or fetches from the evaluation cache) that term's pupil grid first.
// The across-field mean of spot_flatness and the diagnostics both need the fit
// of fields that may carry no merit term of their own.
func (o *Optimizer) spotFitFor(cfg *config, term *meritTerm, surfaces []types.Surface, gc *glass.Catalog, cache *evalGridCache, p appliedPupil) spotFit {
	points := o.gridForTerm(cache, gc, surfaces, cfg, term, p)
	return o.spotFitFromPoints(cfg, term, points, surfaces, gc, cache)
}

// spotFocusScale returns the paraxial focus scale B = R_marginal/NA' (mm): the
// distance over which the entrance-pupil bundle converges to the paraxial
// focus, and therefore the conversion from a spot slope (mm of image
// displacement per mm of entrance-pupil coordinate) to the equivalent
// longitudinal plane shift. A plane displaced by dz moves a ray entering the
// pupil at height rho by dz*rho*NA'/R, so dz = slope*R/NA'.
//
// R and NA' come from the same paraxial marginal ray: paraxial.Compute defines
// ImageSpaceNA as EntrancePupilDiameter/(2*|EFL|), so B evaluates to |EFL|;
// the ratio form is kept because it stays correct should the NA ever become
// the true finite-conjugate marginal angle. A virtual entrance pupil is
// resolved exactly like airyRadiusMM does (EPD = diameter, NA = EPD/(2*EFL)),
// so the scale follows the pupil the grid is actually launched from. The
// focal length is the last resort; 0 means no scale is available and the term
// reports the degenerate penalty.
func (o *Optimizer) spotFocusScale(cfg *config, surfaces []types.Surface, gc *glass.Catalog, wavelength float64, cache *evalGridCache) float64 {
	wl := wavelength
	if wl <= 0 {
		wl = effectiveReferenceWavelength(cfg.referenceWavelength)
	}
	if wl <= 0 || len(surfaces) == 0 {
		return 0
	}
	key := flatKey{configID: cfg.id, wavelength: wl}
	if cache != nil {
		if b, ok := cache.spotScales[key]; ok {
			return b
		}
	}
	if gc == nil {
		gc = glass.NewCatalog()
	}
	pr := paraxial.Compute(types.System{Surfaces: surfaces, StopSurface: cfg.stopSurface}, wl, gc, 0, nil)
	na := pr.ImageSpaceNA
	if na <= 0 {
		na = pr.InfConjImageSpaceNA
	}
	epd := pr.EntrancePupilDiameter
	efl := math.Abs(pr.FocalLength)
	if cfg.pupilModel != nil && cfg.pupilModel.Mode == "virtual_entrance_pupil" && cfg.pupilModel.Diameter > 0 {
		epd = cfg.pupilModel.Diameter
		if efl > 1e-9 {
			na = epd / (2 * efl)
		}
	}
	b := 0.0
	if na > 0 && epd > 0 {
		b = (epd / 2) / na
	} else if efl > 1e-9 {
		b = efl
	}
	if cache != nil {
		cache.spotScales[key] = b
	}
	return b
}

// spotFlatMean returns the field-weighted mean defocus (mm) across a config's
// fields at the term's wavelength: the reference plane spot_flatness measures
// against, so a defocus common to every field — the part back_focus_solve owns
// — cancels and only the field-to-field spread (field curvature) is pushed.
// Fields whose grid cannot be fitted are skipped. A config that declares no
// fields, or whose fields are all unanalyzable while the term's own field is
// undeclared, falls back to the term's own defocus, which makes the
// mean-subtracted value 0: with a single field there is no curvature to
// measure.
func (o *Optimizer) spotFlatMean(cfg *config, term *meritTerm, own float64, surfaces []types.Surface, gc *glass.Catalog, cache *evalGridCache, p appliedPupil) float64 {
	key := flatKey{configID: cfg.id, wavelength: term.wavelength}
	if cache != nil {
		if m, ok := cache.spotMeans[key]; ok {
			return m
		}
	}
	var sum, wsum float64
	for i := range cfg.fields {
		t := flatnessFieldTerm(cfg, term, i)
		fit := o.spotFitFor(cfg, &t, surfaces, gc, cache, p)
		if !fit.ok {
			continue
		}
		sum += t.fieldWeight * fit.defocusMM
		wsum += t.fieldWeight
	}
	mean := own
	if wsum > 0 {
		mean = sum / wsum
		if cache != nil {
			// Only the config-field mean is shared: the own-field fallback
			// depends on the asking term.
			cache.spotMeans[key] = mean
		}
	}
	return mean
}

// spotSlopeWavelengths returns, in declaration order, every wavelength a
// config's spot_flatness / spot_astigmatism terms use (fixed merit terms and
// every merit mode alike), so the diagnostics cover what the run optimised.
func (o *Optimizer) spotSlopeWavelengths(cfg *config) []float64 {
	var wls []float64
	seen := make(map[float64]bool)
	scan := func(terms []meritTerm) {
		for i := range terms {
			k := terms[i].kind
			if k != dls.MeritSpotFlatness && k != dls.MeritSpotAstigmatism {
				continue
			}
			wl := terms[i].wavelength
			if seen[wl] {
				continue
			}
			seen[wl] = true
			wls = append(wls, wl)
		}
	}
	scan(cfg.meritTerms)
	for _, terms := range cfg.meritModes {
		scan(terms)
	}
	return wls
}

// SpotFlatnessDiagnostics evaluates the spot-slope decomposition for every
// (config, field, wavelength) a spot_flatness / spot_astigmatism merit term
// covers, at the given variable state: the exact numbers the terms see,
// reported in mm (equivalent focus deviation from the delivered image plane,
// its mean-subtracted flatness, the astigmatic line-foci deviation and the
// unexplained residual) so a run's field curvature can be read off — and
// compared with a gate's through-focus measurement — without re-measuring.
// Returns nil when the merit declares neither kind.
func (o *Optimizer) SpotFlatnessDiagnostics(x []float64) []types.SpotFlatnessRow {
	type need struct {
		cfg *config
		wls []float64
	}
	var needs []need
	for ci := range o.configs {
		cfg := &o.configs[ci]
		wls := o.spotSlopeWavelengths(cfg)
		if len(wls) == 0 || len(cfg.fields) == 0 {
			continue
		}
		needs = append(needs, need{cfg: cfg, wls: wls})
	}
	if len(needs) == 0 {
		return nil
	}

	configSurfaces, tempGC, pupils := o.applyVariables(x)
	gc := effectiveGC(o.gc, tempGC)
	cache := newEvalGridCache()
	var rows []types.SpotFlatnessRow
	for _, n := range needs {
		cfg := n.cfg
		surfaces := configSurfaces[cfg.id]
		p := pupils[cfg.id]
		o.restoreDiameters(cfg, surfaces)
		o.sizeAutoApertures(cfg, surfaces, gc, cache, p)
		for _, wl := range n.wls {
			base := meritTerm{kind: dls.MeritSpotFlatness, fieldIndex: -1, wavelength: wl}
			mean := o.spotFlatMean(cfg, &base, 0, surfaces, gc, cache, p)
			for fi := range cfg.fields {
				t := flatnessFieldTerm(cfg, &base, fi)
				fit := o.spotFitFor(cfg, &t, surfaces, gc, cache, p)
				row := types.SpotFlatnessRow{
					Config:     cfg.id,
					Field:      cfg.fields[fi].ID,
					AngleDeg:   o.termFieldAngle(cfg, &t, surfaces, gc),
					Wavelength: wl,
					OK:         fit.ok,
					Rays:       fit.rays,
				}
				if fit.ok {
					row.DefocusMM = fit.defocusMM
					row.FlatnessMM = fit.defocusMM - mean
					row.AstigMM = fit.astigMM
					row.ResidualMM = fit.residual
				}
				rows = append(rows, row)
			}
		}
	}
	return rows
}

// gridWorkers returns the number of goroutines for grid-ray parallelism. The
// Jacobian column loop already runs o.workers goroutines per residual call, so
// the grid workers are capped so the two levels do not oversubscribe the CPU.
func (o *Optimizer) gridWorkers() int {
	w := runtime.GOMAXPROCS(0) / o.workers
	if w < 1 {
		w = 1
	}
	return w
}

// gridTraceSem bounds the number of concurrently running pupil-grid traces
// across every Optimizer in the process. An escape run keeps global_workers
// independent Optimizers alive and each precomputeGrids fans out gridWorkers
// traces, so without a global cap the in-flight allocation load scales as
// global_workers × gridWorkers (8 × 10 = 80 in the broad campaign, because
// gridWorkers = GOMAXPROCS / jacobian_workers is maximal at
// jacobian_workers = 1). That load made the Go heap target ratchet up into the
// GBs (measured: heap goal 0.3 → 1.5 GB within minutes, RSS saturating around
// 8 GB). The traces are CPU-bound, so capping at GOMAXPROCS keeps the cores
// busy while bounding the in-flight memory and the GC pressure.
var gridTraceSem = make(chan struct{}, runtime.GOMAXPROCS(0))

// BeginJacobian computes the auto-aperture sizing once at the base variable
// state so that the expensive column evaluations reuse the cached diameters
// instead of re-tracing beam extents. The model calls this with the physical
// (denormalised) variable vector before ComputeResiduals(x).
func (o *Optimizer) BeginJacobian(x []float64) {
	if len(o.initialDiameters) == 0 {
		return
	}
	configSurfaces, tempGC, pupils := o.applyVariables(x)
	gc := effectiveGC(o.gc, tempGC)
	o.reuseDiameters = make(map[string]map[int]float64, len(o.configs))
	for ci := range o.configs {
		cfg := &o.configs[ci]
		surfaces := configSurfaces[cfg.id]
		p := pupils[cfg.id]
		o.restoreDiameters(cfg, surfaces)
		o.sizeAutoApertures(cfg, surfaces, gc, nil, p)
		m := make(map[int]float64)
		for i := range surfaces {
			if surfaces[i].AutoAperture {
				m[surfaces[i].ID] = surfaces[i].Diameter
			}
		}
		o.reuseDiameters[cfg.id] = m
	}
	o.jacobianActive = true
}

// EndJacobian releases the cached auto-aperture diameters and resets the flag.
func (o *Optimizer) EndJacobian() {
	o.jacobianActive = false
	o.reuseDiameters = nil
}

func (o *Optimizer) EvaluateMerit(x []float64) float64 {
	configSurfaces, tempGC, pupils := o.applyVariables(x)
	gc := effectiveGC(o.gc, tempGC)

	merit := 0.0
	cache := newEvalGridCache()
	for ci := range o.configs {
		cfg := &o.configs[ci]
		surfaces := configSurfaces[cfg.id]
		p := pupils[cfg.id]

		o.restoreDiameters(cfg, surfaces)
		o.sizeAutoApertures(cfg, surfaces, gc, cache, p)
		o.precomputeGrids(cfg, surfaces, gc, cache, p)

		cfgMerit := 0.0
		for _, st := range o.scheduledTerms(cfg) {
			term := st.term
			if isGridKind(term.kind) {
				val := o.evaluateGridKind(cfg, term, surfaces, gc, cache, p)
				diff := (val - term.residualTarget()) / term.normDivisor()
				cfgMerit += st.scale * term.weight * term.fieldWeight * term.wavWeight * diff * diff
			} else {
				val := o.evaluateKindTerm(cfg, term, surfaces, gc, cache, p)
				diff := (val - term.residualTarget()) / term.normDivisor()
				cfgMerit += st.scale * term.weight * term.fieldWeight * term.wavWeight * diff * diff
			}
		}
		merit += cfg.weight * cfgMerit
	}

	if o.hull != nil && !o.skipHull {
		hf := o.hullPhaseFactor()
		for _, pair := range o.hullPairs {
			merit += hf * o.hull.Penalty(x[pair.ndIndex], x[pair.vdIndex], o.hullMargin, o.hullWeight)
		}
	}
	if o.catalogField != nil && o.catalogField.CatalogFieldCount() > 0 && !o.skipAttraction && o.attractionWeight > 0 {
		for i, pair := range o.hullPairs {
			w := o.attractionWeight
			if i < len(o.attractionScales) {
				w *= o.attractionScales[i]
			}
			merit += glass.Penalty(o.catalogField, x[pair.ndIndex], x[pair.vdIndex], o.attractionSigmaND, o.attractionSigmaVD, 0, w, o.attractionKernel)
		}
	}
	return merit
}

// MeritBreakdown evaluates the merit at x and returns the contribution of
// each merit term (and the objective total), so the value reported by DLS can
// be reconciled against an external evaluation (e.g. `chief` spot RMS).
func (o *Optimizer) MeritBreakdown(x []float64) map[string]float64 {
	configSurfaces, tempGC, pupils := o.applyVariables(x)
	gc := effectiveGC(o.gc, tempGC)

	out := make(map[string]float64)
	objTotal := 0.0
	cache := newEvalGridCache()
	for ci := range o.configs {
		cfg := &o.configs[ci]
		surfaces := configSurfaces[cfg.id]
		p := pupils[cfg.id]

		o.restoreDiameters(cfg, surfaces)
		o.sizeAutoApertures(cfg, surfaces, gc, cache, p)

		for _, st := range o.scheduledTerms(cfg) {
			term := st.term
			var contrib float64
			if isGridKind(term.kind) {
				val := o.evaluateGridKind(cfg, term, surfaces, gc, cache, p)
				diff := (val - term.residualTarget()) / term.normDivisor()
				contrib = st.scale * term.weight * term.fieldWeight * term.wavWeight * diff * diff
			} else {
				val := o.evaluateKindTerm(cfg, term, surfaces, gc, cache, p)
				diff := (val - term.residualTarget()) / term.normDivisor()
				contrib = st.scale * term.weight * term.fieldWeight * term.wavWeight * diff * diff
			}
			kind := term.kind
			if kind == "" {
				kind = dls.MeritSpotRMS
			}
			key := fmt.Sprintf("config:%s %s(f%.1f,%.6f)", cfg.id, kind, o.termFieldAngle(cfg, term, surfaces, gc), term.wavelength)
			if st.mode != "" {
				key = fmt.Sprintf("config:%s [%s] %s(f%.1f,%.6f)", cfg.id, st.mode, kind, o.termFieldAngle(cfg, term, surfaces, gc), term.wavelength)
			}
			out[key] = contrib
			objTotal += cfg.weight * contrib
		}
	}

	// Mirror EvaluateMerit's hull and glass-attraction contributions so the
	// breakdown total reconciles with the merit the solver reports (the
	// --verbose breakdown previously disagreed with the DLS merit whenever the
	// hull or the attraction was active).
	if o.hull != nil && !o.skipHull {
		hf := o.hullPhaseFactor()
		hullPenalty := 0.0
		for _, pair := range o.hullPairs {
			hullPenalty += hf * o.hull.Penalty(x[pair.ndIndex], x[pair.vdIndex], o.hullMargin, o.hullWeight)
		}
		out["hull"] = hullPenalty
		objTotal += hullPenalty
	}
	if o.catalogField != nil && o.catalogField.CatalogFieldCount() > 0 && !o.skipAttraction && o.attractionWeight > 0 {
		attraction := 0.0
		for i, pair := range o.hullPairs {
			w := o.attractionWeight
			if i < len(o.attractionScales) {
				w *= o.attractionScales[i]
			}
			attraction += glass.Penalty(o.catalogField, x[pair.ndIndex], x[pair.vdIndex], o.attractionSigmaND, o.attractionSigmaVD, 0, w, o.attractionKernel)
		}
		out["glass_attraction"] = attraction
		objTotal += attraction
	}
	out["objective_total"] = objTotal
	return out
}

func (o *Optimizer) ComputeResiduals(x []float64) []float64 {
	configSurfaces, tempGC, pupils := o.applyVariables(x)
	gc := effectiveGC(o.gc, tempGC)

	var allR []float64
	cache := newEvalGridCache()
	for ci := range o.configs {
		cfg := &o.configs[ci]
		surfaces := configSurfaces[cfg.id]
		p := pupils[cfg.id]

		o.restoreDiameters(cfg, surfaces)
		o.sizeAutoApertures(cfg, surfaces, gc, cache, p)
		o.precomputeGrids(cfg, surfaces, gc, cache, p)

		for _, st := range o.scheduledTerms(cfg) {
			term := st.term
			w := math.Sqrt(cfg.weight*st.scale*term.weight*term.fieldWeight*term.wavWeight) / term.normDivisor()
			if isGridKind(term.kind) {
				val := o.evaluateGridKind(cfg, term, surfaces, gc, cache, p)
				allR = append(allR, w*(val-term.residualTarget()))
			} else {
				val := o.evaluateKindTerm(cfg, term, surfaces, gc, cache, p)
				allR = append(allR, w*(val-term.residualTarget()))
			}
		}
	}

	if o.hull != nil && !o.skipHull {
		// sqrt of the phase factor so that residual² equals the scaled merit
		// contribution added by EvaluateMerit/MeritBreakdown.
		hf := math.Sqrt(o.hullPhaseFactor())
		for _, pair := range o.hullPairs {
			allR = append(allR, hf*o.hull.Residual(x[pair.ndIndex], x[pair.vdIndex], o.hullMargin, o.hullWeight))
		}
	}
	if o.catalogField != nil && o.catalogField.CatalogFieldCount() > 0 && !o.skipAttraction && o.attractionWeight > 0 {
		for i, pair := range o.hullPairs {
			w := o.attractionWeight
			if i < len(o.attractionScales) {
				w *= o.attractionScales[i]
			}
			allR = append(allR, glass.Residual(o.catalogField, x[pair.ndIndex], x[pair.vdIndex], o.attractionSigmaND, o.attractionSigmaVD, 0, w, o.attractionKernel))
		}
	}
	return allR
}

// WarnBeamClipping reports the clear apertures the prescribed beam leaves at x,
// as a deficit in mm per surface.
//
// A fixed (non-auto_aperture) diameter is the user's prescription, so nothing
// sizes it to the beam: the merit grid simply loses the rays it clips, which
// lowers a spot-only residual (the same incentive the G4 field warning
// describes) without anyone seeing the cause. The envelope is measured with
// aperture and glass-path checks disabled at margin 1.0 — the physical marginal
// beam, not the merit grid's deliberately inflated launch — on a dense grid of
// the field's prescribed (vignetted) pupil, the measurement the beam_clearance
// constraint and chief --clear-aperture use, for every config field and every
// sizing wavelength. Auto-aperture surfaces are checked too (the sizing should
// have covered them; a miss is worth knowing) and the stop is skipped: it
// defines the beam. A config that already constrains beam_clearance /
// beam_diameter is left alone — the user is managing the clearance there, and
// the constraint reporting already names a violation.
func (o *Optimizer) WarnBeamClipping(x []float64) {
	type finding struct {
		config   string
		field    int
		angle    float64
		surface  int
		diameter float64
		needs    float64
	}
	configSurfaces, tempGC, pupils := o.applyVariables(x)
	gc := effectiveGC(o.gc, tempGC)
	// The envelope grid must reach at least the merit grid's outermost ring,
	// which lies at rings/(rings+1) of the launched radius, while a hex grid
	// reaches 1-1/n: n >= rings+1 keeps the envelope inside that ring. 128 rays
	// (n = 12) does so for every merit grid up to 121 rays.
	rays := o.extentRays(128)
	var findings []finding
	for ci := range o.configs {
		cfg := &o.configs[ci]
		if len(cfg.fields) == 0 || configGuardsBeamClearance(cfg) {
			continue
		}
		surfaces := configSurfaces[cfg.id]
		p := pupils[cfg.id]
		wls := apertureSizingWavelengths(cfg)
		for fi := range cfg.fields {
			f := &cfg.fields[fi]
			angle := o.fieldSizingAngle(cfg, fi, surfaces, gc, wls[0])
			pupilZ := o.gridCentring(cfg, p, angle)
			worst := make(map[int]float64)
			for _, wl := range wls {
				env := dls.TraceFieldGridExtentsVignetted(gc, surfaces, cfg.stopSurface, pupilZ, angle, []float64{0, 1}, wl, 1.0, rays, o.gridRotation, o.gridWorkers(), p.dia, f.Vignetting)
				for id, e := range env {
					if e > worst[id] {
						worst[id] = e
					}
				}
			}
			for i := range surfaces {
				s := &surfaces[i]
				if s.ID == cfg.stopSurface || s.Diameter <= 0 {
					continue
				}
				if deficit := worst[s.ID] - s.Diameter/2; deficit > beamClipTolerance {
					findings = append(findings, finding{
						config: cfg.id, field: fi, angle: angle,
						surface: s.ID, diameter: s.Diameter, needs: worst[s.ID],
					})
				}
			}
		}
	}
	if len(findings) == 0 {
		return
	}
	sort.SliceStable(findings, func(i, j int) bool {
		return findings[i].needs-findings[i].diameter/2 > findings[j].needs-findings[j].diameter/2
	})
	Warnf("warning: the prescribed beam leaves the clear aperture of a surface, so the merit grid loses those rays and a spot-only residual is lowered by the clipping (the stop is exempt):")
	shown := 0
	for _, f := range findings {
		if shown == beamClipWarnMax {
			Warnf("  (+%d more surface/field pairs)", len(findings)-shown)
			break
		}
		Warnf("  config %q field %d (%.2f deg): surface %d has diameter %.4f mm, the beam needs %.4f mm (over by %.4f mm) — raise the diameter, enable auto_aperture on it, or add a beam_clearance constraint",
			f.config, f.field, f.angle, f.surface, f.diameter, 2*f.needs, f.needs-f.diameter/2)
		shown++
	}
}

// configGuardsBeamClearance reports whether cfg already constrains the beam
// clearance at a surface, in which case WarnBeamClipping stays quiet.
func configGuardsBeamClearance(cfg *config) bool {
	for _, c := range cfg.constraints {
		if c.Measure == types.MeasureBeamClearance || c.Measure == types.MeasureBeamDiameter {
			return true
		}
	}
	return false
}
