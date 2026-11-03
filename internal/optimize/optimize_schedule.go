package optimize

import (
	"fmt"
	"math"

	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/types"
)

// meritSchedule is the compiled optimization.merit_schedule: a smooth blend of
// named merit modes whose weights follow a scalar state metric.
type meritSchedule struct {
	metric        string // merit_ratio | iteration | glass_role | spot_diffraction
	curve         string // linear | sigmoid | step
	anchorFrom    float64
	anchorTo      float64
	glassSurfaces []int
	aggregation   string // mean (default) | max
	modes         []scheduleMode
}

type scheduleMode struct {
	name          string
	weightFrom    float64
	weightTo      float64
	backFocusType string // "paraxial" | "wavefront" | ""
}

// SetMeritSchedule installs the conditional merit blend. It computes the
// initial per-mode weights at the initial variable state so the DLS "before"
// merit and the first iteration's base-point residuals share the same blend.
// A nil schedule keeps the fixed per-config merit.
func (o *Optimizer) SetMeritSchedule(s *types.MeritScheduleConfig) {
	if s == nil {
		return
	}
	ms := &meritSchedule{
		metric:        s.Metric,
		curve:         s.Curve,
		anchorFrom:    s.AnchorFrom,
		anchorTo:      s.AnchorTo,
		glassSurfaces: s.GlassSurfaces,
	}
	if ms.metric == "" {
		ms.metric = "merit_ratio"
	}
	if ms.curve == "" {
		ms.curve = "linear"
	}
	ms.aggregation = s.MetricAggregation
	if ms.aggregation == "" {
		ms.aggregation = "mean"
	}
	// Default anchors for spot_diffraction when unset (both 0): the metric is
	// normalised by its initial value so anchor_from=1.0 is the starting point;
	// anchor_to=0.005 corresponds to the geometric spot shrinking to the Airy
	// radius (0.0023 mm / ~0.477 mm for the double-Gauss).
	if ms.metric == "spot_diffraction" && ms.anchorFrom == 0 && ms.anchorTo == 0 {
		ms.anchorFrom = 1.0
		ms.anchorTo = 0.005
	}
	o.modeWeights = make(map[string]float64, len(s.Modes))
	for _, m := range s.Modes {
		if !backFocusTypeValid(m.BackFocusType) {
			Warnf("warning: merit_schedule mode %q back_focus_type %q is not a known solve type (paraxial | wavefront | spot); the static back_focus_solve.type is used", m.Name, m.BackFocusType)
		}
		ms.modes = append(ms.modes, scheduleMode{name: m.Name, weightFrom: m.WeightFrom, weightTo: m.WeightTo, backFocusType: m.BackFocusType})
		o.modeWeights[m.Name] = 0
	}
	o.meritSchedule = ms

	// Initialise the back-focus schedule from the BackFocusSolve config.
	if o.backFocusSolve != nil && o.backFocusSolve.Schedule != nil {
		o.backFocusSchedule = o.backFocusSolve.Schedule
	}
	// Set the initial back-focus type.
	o.updateBackFocusType()

	x0 := o.InitialState()
	// For spot_diffraction, snapshot the initial ratio so subsequent values are
	// normalised (initial = 1.0, improving = <1.0).
	if ms.metric == "spot_diffraction" {
		r, _ := o.spotDiffractionRatioRaw(x0)
		o.initialSpotRatio = r
	}
	// The merit_ratio metric is 1.0 at the initial state by definition (the
	// initialMerit guard below); the other metrics evaluate directly.
	o.setModeWeightsAt(o.scheduleMetric(x0, 0))
	o.initialMerit = o.EvaluateMerit(x0)
}

// SetGlassAttraction configures the soft-min potential pulling nd/vd toward
// real catalog glasses. Call after NewOptimizer and before SetMeritSchedule
// (the setter computes initialOpticalMerit which is needed for merit_ratio
// metric).
func (o *Optimizer) SetGlassAttraction(cfg *types.GlassAttractionConfig, catalog *glass.Catalog) {
	if cfg == nil || !cfg.Enabled {
		return
	}
	o.catalogField = glass.BuildCatalogField(catalog)
	if o.catalogField.CatalogFieldCount() == 0 {
		return
	}

	// Resolve defaults.
	o.attractionKernel = cfg.Kernel
	if o.attractionKernel == "" {
		o.attractionKernel = "distance"
	}
	o.attractionSigmaND = cfg.SigmaND
	if o.attractionSigmaND <= 0 {
		o.attractionSigmaND = 0.03
	}
	o.attractionSigmaVD = cfg.SigmaVD
	if o.attractionSigmaVD <= 0 {
		o.attractionSigmaVD = 0.03
	}
	o.attractionWeight = cfg.WeightFrom
	o.attractionWeightFrom = cfg.WeightFrom
	o.attractionWeightTo = cfg.WeightTo
	o.attractionMetric = cfg.Metric
	if o.attractionMetric == "" {
		o.attractionMetric = "merit_ratio"
	}
	o.attractionAnchorFrom = cfg.AnchorFrom
	o.attractionAnchorTo = cfg.AnchorTo
	if o.attractionAnchorFrom == 0 && o.attractionAnchorTo == 0 {
		o.attractionAnchorFrom = 1.0
		o.attractionAnchorTo = 0.2
	}
	o.attractionCurve = cfg.Curve
	if o.attractionCurve == "" {
		o.attractionCurve = "linear"
	}
	o.sensitivityWeighted = cfg.SensitivityWeighted
	o.sensitivityPower = cfg.SensitivityPower
	if o.sensitivityPower == 0 {
		o.sensitivityPower = 1.0
	}
	o.sensitivityScaleMin = cfg.SensitivityScaleMin
	if o.sensitivityScaleMin == 0 {
		o.sensitivityScaleMin = 0.1
	}
	o.sensitivityScaleMax = cfg.SensitivityScaleMax
	if o.sensitivityScaleMax == 0 {
		o.sensitivityScaleMax = 4.0
	}
	o.sensitivityRef = cfg.SensitivityRef
	if o.sensitivityRef == "" {
		o.sensitivityRef = "mean"
	}

	// Initialise per-pair scales to 1.0.
	o.attractionScales = make([]float64, len(o.hullPairs))
	for i := range o.attractionScales {
		o.attractionScales[i] = 1.0
	}
	o.sensitivityEMA = make([]float64, len(o.hullPairs))

	// Compute initialOpticalMerit for merit_ratio metric (exclude attraction
	// and hull so the ratio is clean).
	o.initialOpticalMerit = o.evaluateOpticalMerit(o.InitialState())
}

// attractionScheduleMetric returns the state metric for the attraction weight
// ramp. The metric is normalised by its initial value (anchor_from=1.0) so the
// curve maps to [0,1].
func (o *Optimizer) attractionScheduleMetric(x []float64, iter int) float64 {
	switch o.attractionMetric {
	case "iteration":
		return float64(iter)
	case "run_iteration":
		return float64(o.attractionRunIter)
	default: // merit_ratio
		if o.initialOpticalMerit <= 0 {
			return 1.0
		}
		return o.evaluateOpticalMerit(x) / o.initialOpticalMerit
	}
}

// updateAttractionSensitivity computes the per-pair vd elasticity of the
// optical merit via forward finite differences and applies EMA smoothing.
// The result is normalised to a per-pair scale in [scaleMin, scaleMax].
func (o *Optimizer) updateAttractionSensitivity(x []float64) {
	if len(o.hullPairs) == 0 || o.catalogField == nil {
		return
	}
	emaCoeff := 0.6
	base := o.evaluateOpticalMerit(x)
	if base <= 0 {
		return
	}
	dvd := 0.5 // absolute step in vd units
	for i, p := range o.hullPairs {
		xPlus := make([]float64, len(x))
		copy(xPlus, x)
		xPlus[p.vdIndex] += dvd
		mPlus := o.evaluateOpticalMerit(xPlus)
		s := math.Abs((mPlus-base)/dvd) * x[p.vdIndex] / (base + 1e-30)
		// EMA smoothing.
		if len(o.sensitivityEMA) > i {
			o.sensitivityEMA[i] = emaCoeff*o.sensitivityEMA[i] + (1.0-emaCoeff)*s
			s = o.sensitivityEMA[i]
		}
		o.attractionScales[i] = s
	}
	// Normalise: scale_i = clamp((s_i/s_ref)^power, min, max).
	var sum float64
	n := len(o.attractionScales)
	switch o.sensitivityRef {
	case "max":
		var maxS float64
		for _, s := range o.attractionScales {
			if s > maxS {
				maxS = s
			}
		}
		sum = maxS
	default: // mean
		for _, s := range o.attractionScales {
			sum += s
		}
		sum /= float64(n)
	}
	if sum <= 0 {
		return
	}
	for i, s := range o.attractionScales {
		v := math.Pow(s/sum, o.sensitivityPower)
		if v < o.sensitivityScaleMin {
			v = o.sensitivityScaleMin
		} else if v > o.sensitivityScaleMax {
			v = o.sensitivityScaleMax
		}
		o.attractionScales[i] = v
	}
}

// setModeWeightsAt evaluates the weight curve at the metric value s and stores
// the per-mode weights. It returns the resulting dominant mode without any
// side effects (change counting / logging happen in the callers).
func (o *Optimizer) setModeWeightsAt(s float64) string {
	t := 0.5
	span := o.meritSchedule.anchorTo - o.meritSchedule.anchorFrom
	if math.Abs(span) > 1e-15 {
		t = (s - o.meritSchedule.anchorFrom) / span
		if t < 0 {
			t = 0
		}
		if t > 1 {
			t = 1
		}
	}
	for _, sm := range o.meritSchedule.modes {
		f := scheduleCurve(o.meritSchedule.curve, t)
		o.modeWeights[sm.name] = sm.weightFrom + (sm.weightTo-sm.weightFrom)*f
	}
	return dominantMode(o.modeWeights)
}

// resolveScheduledNumRays returns the effective num_rays from the currently
// active merit modes. It takes the maximum across all configs so that no
// config is under-sampled. When no mode declares num_rays, the base value
// (o.baseNumRays) is returned unchanged.
func (o *Optimizer) resolveScheduledNumRays() int {
	maxRays := o.baseNumRays
	for ci := range o.configs {
		cfg := &o.configs[ci]
		if cfg.meritModeNumRays == nil {
			continue
		}
		for _, sm := range o.meritSchedule.modes {
			w := o.modeWeights[sm.name]
			if w <= 0 {
				continue
			}
			if r, ok := cfg.meritModeNumRays[sm.name]; ok && r > maxRays {
				maxRays = r
			}
		}
	}
	return maxRays
}

// scheduleMetric evaluates the state metric s(x) driving the blend.
func (o *Optimizer) scheduleMetric(x []float64, iter int) float64 {
	switch o.meritSchedule.metric {
	case "iteration":
		return float64(iter)
	case "phase":
		return o.phase
	case "glass_role":
		configSurfaces, tempGC, _ := o.applyVariables(x)
		gc := effectiveGC(o.gc, tempGC)
		total := 0.0
		for ci := range o.configs {
			cfg := &o.configs[ci]
			surfaces := configSurfaces[cfg.id]
			for _, id := range o.meritSchedule.glassSurfaces {
				total += math.Abs(glassRoleForSurface(surfaces, gc, id, o.roleTargets[cfg.id]))
			}
		}
		return total
	case "spot_diffraction":
		r, _ := o.spotDiffractionRatio(x)
		return r
	default: // merit_ratio
		if o.initialMerit == 0 {
			return 1.0
		}
		return o.EvaluateMerit(x) / o.initialMerit
	}
}

// spotDiffractionRatio returns the normalised spot/Airy ratio.  The raw ratio
// (weighted-average geometric spot RMS / Airy radius) is divided by the
// initial ratio snapshot so the metric always starts at 1.0 and drops as the
// geometric spot shrinks toward the diffraction limit.
func (o *Optimizer) spotDiffractionRatio(x []float64) (float64, bool) {
	r, ok := o.spotDiffractionRatioRaw(x)
	if !ok || o.initialSpotRatio <= 0 {
		return r, ok
	}
	return r / o.initialSpotRatio, true
}

// spotDiffractionRatioRaw computes the un-normalised ratio of the weighted-
// average geometric spot RMS to the diffraction-limited Airy radius
// (0.61·λ/NA) across all configs and fields.  The second return value reports
// whether any valid data was found (all configs with at least one traced grid).
func (o *Optimizer) spotDiffractionRatioRaw(x []float64) (float64, bool) {
	configSurfaces, tempGC, pupils := o.applyVariables(x)
	gc := effectiveGC(o.gc, tempGC)

	var weightedSum, totalWeight float64
	maxRatio := 0.0
	hasData := false

	for ci := range o.configs {
		cfg := &o.configs[ci]
		surfaces := configSurfaces[cfg.id]

		wl := effectiveReferenceWavelength(cfg.referenceWavelength)
		if len(cfg.wavelengths) > 0 {
			wl = cfg.wavelengths[0].Value
		}
		if wl <= 0 {
			continue
		}

		// Image-space NA from paraxial (infinite conjugate).
		sys := types.System{Surfaces: surfaces, StopSurface: cfg.stopSurface}
		pr := paraxial.Compute(sys, wl, gc, 0, nil)
		na := pr.ImageSpaceNA
		if na <= 0 {
			na = pr.InfConjImageSpaceNA
		}
		if na <= 0 {
			continue
		}
		airy := 0.61 * wl / na
		if airy <= 0 {
			continue
		}

		// Collect field angles.
		type fieldEntry struct {
			angle  float64
			weight float64
			vig    *types.VignettingDef
		}
		var fields []fieldEntry
		for fi := range cfg.fields {
			fields = append(fields, fieldEntry{
				angle:  o.fieldSizingAngle(cfg, fi, surfaces, gc, wl),
				weight: cfg.fields[fi].Weight,
				vig:    cfg.fields[fi].Vignetting,
			})
		}
		if len(fields) == 0 {
			// Fallback: use angles from grid merit terms.
			seen := make(map[float64]bool)
			for ti := range cfg.meritTerms {
				term := &cfg.meritTerms[ti]
				if !isTraceKind(term.kind) {
					continue
				}
				a := o.termFieldAngle(cfg, term, surfaces, gc)
				if !seen[a] {
					seen[a] = true
					fields = append(fields, fieldEntry{angle: a, weight: 1.0, vig: o.fieldVignetting(cfg, term)})
				}
			}
		}
		if len(fields) == 0 {
			continue
		}

		cfgWeight := cfg.weight
		if cfgWeight <= 0 {
			cfgWeight = 1.0
		}

		for _, fe := range fields {
			p := pupils[cfg.id]
			pupilZ := o.gridCentring(cfg, p, fe.angle)
			points, _ := dls.TraceFieldGrid(gc, surfaces, cfg.stopSurface, pupilZ,
				fe.angle, []float64{0, 1}, wl, o.apertureMargin, o.numRays,
				o.gridRotation, o.gridWorkers(), p.dia, fe.vig)

			rms := dls.ComputeSpotRMS(points)
			if rms <= 0 || rms >= 1e6 {
				// Degenerate grid (0 valid rays or fully vignetted field)
				// → skip this field from the average rather than polluting
				// the metric with a 1e3 fallback.
				continue
			}
			ratio := rms / airy
			rw := cfgWeight * fe.weight
			weightedSum += rw * ratio
			totalWeight += rw
			if ratio > maxRatio {
				maxRatio = ratio
			}
			hasData = true
		}
	}

	if !hasData {
		// No valid grid data → strongly aberration-limited fallback.
		return 1e3, false
	}
	if o.meritSchedule.aggregation == "max" {
		return maxRatio, true
	}
	// Default: weighted mean.
	if totalWeight <= 0 {
		return maxRatio, true
	}
	return weightedSum / totalWeight, true
}

// scheduleCurve maps the normalised metric t ∈ [0,1] to a blend fraction.
func scheduleCurve(curve string, t float64) float64 {
	switch curve {
	case "sigmoid":
		return 1.0 / (1.0 + math.Exp(-10.0*(t-0.5)))
	case "step":
		if t < 0.5 {
			return 0
		}
		return 1
	default: // linear
		return t
	}
}

// dominantMode returns the mode name with the largest weight (ties keep the
// first in map iteration order; stable enough for change counting).
// dominantMode returns the mode with the largest weight. Ties are broken
// deterministically by the lexicographically smallest mode name: iterating the
// map and keeping the first strict maximum picked an arbitrary winner when two
// weights were equal (or crossed), which made currentBackFocusType and
// opt_results.active_mode non-reproducible across identical runs.
func dominantMode(weights map[string]float64) string {
	best := ""
	bestW := math.Inf(-1)
	for name, w := range weights {
		if w > bestW || (w == bestW && name < best) {
			best, bestW = name, w
		}
	}
	return best
}

func copyWeights(src map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// MeritScheduleState returns the active (largest-weight) mode, the final
// per-mode weights, the number of dominant-mode transitions, the last
// evaluated metric value, and the effective num_rays. The zero value is
// returned when no schedule is configured.
func (o *Optimizer) MeritScheduleState() (string, map[string]float64, int, float64, int) {
	if o.meritSchedule == nil {
		return "", nil, 0, 0, o.baseNumRays
	}
	return dominantMode(o.modeWeights), copyWeights(o.modeWeights), o.modeChanges, o.lastMetric, o.numRays
}

// GlassAttractionDiagnostics returns the final glass-attraction state for
// reporting. Nil when attraction is not configured. Reported whenever a
// catalog field was built (even at weight 0), so a zero-weight run can serve
// as an unconstrained baseline while still exposing the nearest-glass
// distances.
func (o *Optimizer) GlassAttractionDiagnostics(x []float64) *types.GlassAttractionResult {
	if o.catalogField == nil {
		return nil
	}
	res := &types.GlassAttractionResult{Weight: o.attractionWeight}
	for i, pair := range o.hullPairs {
		key, nnd, nvd, r2 := glass.NearestNamed(o.catalogField, x[pair.ndIndex], x[pair.vdIndex])
		scale := 1.0
		if i < len(o.attractionScales) {
			scale = o.attractionScales[i]
		}
		name := pair.name
		if name == "" && pair.ndIndex < len(o.variables) {
			name = fmt.Sprintf("surface %d", o.variables[pair.ndIndex].SurfaceID)
		}
		res.Pairs = append(res.Pairs, types.GlassAttractionPairResult{
			Name:       name,
			ND:         x[pair.ndIndex],
			VD:         x[pair.vdIndex],
			NearestKey: key,
			NearestND:  nnd,
			NearestVD:  nvd,
			Distance:   math.Sqrt(r2),
			Scale:      scale,
		})
	}
	return res
}

// scheduledTerm is one effective term of a config with the mode weight folded
// into its scale (1.0 when no schedule is active).
type scheduledTerm struct {
	term  *meritTerm
	scale float64
	mode  string
}

// scheduledTerms returns the effective terms of a config. During the glass
// phase (glassMeritActive) it returns the installed colour-only glass merit
// terms at scale 1.0, so both EvaluateMerit and ComputeResiduals switch to the
// chromatic objective exactly. Otherwise, with a schedule, the terms come from
// the config's merit_modes, each scaled by its (frozen) mode weight; a config
// without merit_modes keeps its fixed merit terms at scale 1.
func (o *Optimizer) scheduledTerms(cfg *config) []scheduledTerm {
	if o.glassMeritActive {
		terms := o.glassMerit[cfg.id]
		out := make([]scheduledTerm, len(terms))
		for i := range terms {
			out[i] = scheduledTerm{term: &terms[i], scale: 1.0, mode: "glass"}
		}
		return out
	}
	if o.meritSchedule == nil {
		out := make([]scheduledTerm, len(cfg.meritTerms))
		for i := range cfg.meritTerms {
			out[i] = scheduledTerm{term: &cfg.meritTerms[i], scale: 1.0}
		}
		return out
	}
	var out []scheduledTerm
	for _, sm := range o.meritSchedule.modes {
		w := o.modeWeights[sm.name]
		if w <= 0 {
			continue
		}
		terms, ok := cfg.meritModes[sm.name]
		if !ok {
			continue
		}
		for i := range terms {
			out = append(out, scheduledTerm{term: &terms[i], scale: w, mode: sm.name})
		}
	}
	return out
}
