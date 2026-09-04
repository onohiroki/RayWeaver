package optimize

import (
	"fmt"
	"math"
	"runtime"
	"sync"

	"github.com/hiroki/rayweaver/internal/chief"
	"github.com/hiroki/rayweaver/internal/constraint"
	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/raymath"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// Config is the single-configuration optimisation input. It is an adapter
// over the unified Optimizer: the configuration becomes config "config1" and
// the variables become local variables of that config.
type Config struct {
	Surfaces         []types.Surface
	Variables        []Variable
	MeritTerms       []MeritTerm
	Fields           []types.FieldItem
	Constraints      []types.ConstraintOperand
	GlassCatalog     *glass.Catalog
	CoatingCatalog   interface{}
	StopSurface      int
	RefSurface       int
	PupilZ           float64
	MaxIter          int
	Mu               float64
	Tol              float64
	Epsilon          float64
	NumRays          int
	ApertureMargin   float64
	ApertureMarginMM float64
	MuConMax         float64
	Workers          int
	Logger           dls.Logger
	CentralDiff      bool
	BFGS             bool
	Hull             *glass.ConvexHull
	HullMargin       float64
	HullWeight       float64
	// Bounded penalties for merit terms that cannot be evaluated (a pupil
	// grid with no valid rays, or a failed wavefront fit). Defaults:
	// spot 0.1, opd 0.01, wavefront 0.001 (mm). The legacy 1e6 sentinel
	// would contribute weight·1e12 to the merit and stall the DLS line search.
	SpotDegenerate      float64
	OPDDegenerate       float64
	WavefrontDegenerate float64
	// PowerSolveSurfaces are the surface IDs whose curvature is recomputed so
	// each containing element's thin-lens power stays at its initial value
	// (optimization.power_solve.surfaces). Empty disables the solve.
	PowerSolveSurfaces []int
	// MeritModes defines named merit modes for conditional merit scheduling.
	// When non-empty, SetMeritSchedule activates mode blending.
	MeritModes []types.MeritMode
	// RegionActive configures the Okudaira Region Active Method (nil = disabled).
	RegionActive *types.RegionActiveConfig
	// AdaptiveDamping configures per-variable adaptive damping (nil = legacy μI).
	AdaptiveDamping *types.AdaptiveDampingConfig
}

// ConfigInput describes one configuration (zoom position) of a
// multi-configuration optimisation.
type ConfigInput struct {
	ID                  string
	Weight              float64
	StopSurface         int
	RefSurface          int
	PupilZ              float64
	ReferenceWavelength float64
	Surfaces            []types.Surface
	Fields              []types.FieldItem
	Wavelengths         []types.WavelengthItem
	MeritTerms          []types.MeritTerm
	MeritModes          []types.MeritMode
	Constraints         []types.ConstraintOperand
	RegionActive        *types.RegionActiveConfig
}

func effectiveReferenceWavelength(wavelength float64) float64 {
	if wavelength > 0 {
		return wavelength
	}
	return types.DefaultWavelength
}

// Variable is one optimisation variable. It binds one component of the
// variable vector to either:
//   - a single surface parameter of one config (Config, SurfaceID, Param), or
//   - many surface parameters across configs via scale/offset bindings
//     (IsShared + Bindings).
type Variable struct {
	Name      string
	SurfaceID int
	GlassName string
	Param     string
	Min       float64
	Max       float64
	IsShared  bool
	Bindings  []types.SharedVariableBinding
	Config    string
}

// MeritTerm is a pre-resolved single-config merit term (weights and field
// angle already matched against the fields/wavelengths).
type MeritTerm struct {
	Kind        string
	FieldAngle  float64
	FieldDir    []float64
	FieldWeight float64
	// FieldIndex is the term's field index in the config's field list
	// (-1 = unset). The wavefront kinds use it to carry the field's declared
	// vignetting into the pupil-grid clip.
	FieldIndex  int
	Wavelength  float64
	Wavelength2 float64
	WavWeight   float64
	Weight      float64
	Target      float64
	Fraction    float64
	SurfaceSet  []int
}

type Result struct {
	BeforeMerit float64
	AfterMerit  float64
	Iterations  int
	Status      string
	Variables   []VariableState
}

type VariableState struct {
	Name      string
	Config    string
	SurfaceID int
	GlassName string
	Param     string
	Before    float64
	After     float64
}

type glassPair struct {
	ndIndex int
	vdIndex int
	name    string
}

// powerSolveEntry binds a solve surface to the thin-lens power it must
// preserve during optimisation.
type powerSolveEntry struct {
	solveID   int
	targetPhi float64
}

// config is the internal per-configuration state of the unified Optimizer.
type config struct {
	id                  string
	weight              float64
	stopSurface         int
	refSurface          int
	pupilZ              float64
	referenceWavelength float64
	// pupilZs holds the per-field dynamic entrance pupil Z keyed by field
	// angle (degrees), refreshed by UpdatePupils. Grids for off-axis fields
	// must be centred on their own pupil, not the field-0 one.
	pupilZs     map[float64]float64
	fieldDefs   []types.FieldDef
	surfaces    []types.Surface
	fields      []types.FieldItem
	wavelengths []types.WavelengthItem
	meritTerms  []meritTerm
	// meritModes holds the config's named merit-mode term lists (from
	// configs[].merit_modes). Nil when the config uses its fixed merit.
	meritModes  map[string][]meritTerm
	// meritModeNumRays holds the per-mode num_rays override (from
	// configs[].merit_modes[].num_rays). Nil when no mode declares num_rays.
	meritModeNumRays map[string]int
	constraints      []types.ConstraintOperand
}

// meritTerm is the unified merit term: weights and (for angle fields) the
// field angle are resolved at construction; image-height fields are converted
// to an angle per evaluation because they depend on the current surfaces.
type meritTerm struct {
	kind           string
	fieldAngle     float64
	useImageHeight bool
	imageHeight    float64
	fieldWeight    float64
	wavelength     float64
	wavelength2    float64
	wavWeight      float64
	weight         float64
	target         float64
	// surfaceSet identifies the glass surfaces a kind operates on
	// (glass_role uses surfaceSet[0] as the element's glass surface).
	surfaceSet []int
	// fieldDirX/fieldDirY is the field's image-plane azimuth unit vector,
	// used by the tangential/sagittal spot kinds (spot_rms_t / _s / _worst).
	// Defaults to the Y axis (0, 1).
	fieldDirX float64
	fieldDirY float64
	// fieldIndex is the index of the term's field in the config's field list
	// (-1 = unset). The wavefront kinds use it to carry the field's declared
	// vignetting (and direction) into the pupil-grid clip.
	fieldIndex int
	// fraction is the encircled-energy fraction for spot_ee_radius (default 0.8).
	fraction float64
}

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
	name       string
	weightFrom float64
	weightTo   float64
}

// glassVDForSurface returns the Abbe number of the material on the surface with
// the given ID, resolved through the (possibly in-flight) catalog for keyed
// materials and read inline for model glasses. Returns 0 for air/unknown.
func glassVDForSurface(surfaces []types.Surface, gc *glass.Catalog, id int) float64 {
	for i := range surfaces {
		if surfaces[i].ID != id {
			continue
		}
		m := surfaces[i].Material
		if m.HasModel() && !m.HasKey() {
			return m.VD
		}
		if m.HasKey() {
			if g, ok := gc.Lookup(m.Key); ok {
				return g.VD
			}
		}
		return 0
	}
	return 0
}

// glassNDForSurface returns the refractive index of the material on the surface
// with the given ID, resolved like glassVDForSurface. Returns 0 for
// air/unknown.
func glassNDForSurface(surfaces []types.Surface, gc *glass.Catalog, id int) float64 {
	for i := range surfaces {
		if surfaces[i].ID != id {
			continue
		}
		m := surfaces[i].Material
		if m.HasModel() && !m.HasKey() {
			return m.ND
		}
		if m.HasKey() {
			if g, ok := gc.Lookup(m.Key); ok {
				return g.ND
			}
		}
		return 0
	}
	return 0
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
		ms.modes = append(ms.modes, scheduleMode{name: m.Name, weightFrom: m.WeightFrom, weightTo: m.WeightTo})
		o.modeWeights[m.Name] = 0
	}
	o.meritSchedule = ms

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

// UpdateMeritWeights implements dls.MeritScheduleUpdater: recompute the mode
// weights from the state metric at the current x. Called once per DLS iteration
// at the current x, so the weights stay frozen within one iteration and the
// Jacobian matches the merit actually minimised.
func (o *Optimizer) UpdateMeritWeights(x []float64, iter int) {
	if o.meritSchedule == nil {
		return
	}
	s := o.scheduleMetric(x, iter)
	o.lastMetric = s
	prev := dominantMode(o.modeWeights)
	cur := o.setModeWeightsAt(s)
	if cur != prev {
		o.modeChanges++
	}
	o.numRays = o.resolveScheduledNumRays()
	if o.logger != nil {
		if ml, ok := o.logger.(dls.ModeLogger); ok {
			ml.LogModeWeights(iter, copyWeights(o.modeWeights), s)
		}
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
	case "glass_role":
		configSurfaces, tempGC := o.applyVariables(x)
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
	configSurfaces, tempGC := o.applyVariables(x)
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
		}
		var fields []fieldEntry
		for fi := range cfg.fields {
			fields = append(fields, fieldEntry{
				angle:  o.fieldSizingAngle(cfg, &cfg.fields[fi], surfaces, gc, wl),
				weight: cfg.fields[fi].Weight,
			})
		}
		if len(fields) == 0 {
			// Fallback: use angles from grid merit terms.
			seen := make(map[float64]bool)
			for ti := range cfg.meritTerms {
				term := &cfg.meritTerms[ti]
				if !isGridKind(term.kind) {
					continue
				}
				a := o.termFieldAngle(cfg, term, surfaces, gc)
				if !seen[a] {
					seen[a] = true
					fields = append(fields, fieldEntry{angle: a, weight: 1.0})
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
			pupilZ := cfg.pupilZ
			if cfg.pupilZs != nil {
				if z, ok := cfg.pupilZs[fe.angle]; ok {
					pupilZ = z
				}
			}
			points, _ := dls.TraceFieldGrid(gc, surfaces, cfg.stopSurface, pupilZ,
				fe.angle, []float64{0, 1}, wl, o.apertureMargin, o.numRays,
				o.gridRotation, o.gridWorkers())

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
func dominantMode(weights map[string]float64) string {
	best := ""
	bestW := math.Inf(-1)
	for name, w := range weights {
		if w > bestW {
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

// resolvePupilZ returns the pupil Z used to centre grid traces for a config:
// the explicit stop surface Z when one is set, otherwise the caller-provided
// dynamic pupil Z. The stop surface is never inferred.
func resolvePupilZ(surfaces []types.Surface, stopSurface int, pupilZ float64) float64 {
	if stopSurface > 0 {
		sc := append([]types.Surface{}, surfaces...)
		surface.Precompute(sc)
		for _, s := range sc {
			if s.ID == stopSurface {
				return s.PhysicalZ
			}
		}
	}
	return pupilZ
}

// fieldDefsFromItems converts the per-config field items into chief field
// definitions for the dynamic-pupil pass.
func fieldDefsFromItems(items []types.FieldItem) []types.FieldDef {
	if len(items) == 0 {
		return nil
	}
	out := make([]types.FieldDef, len(items))
	for i, f := range items {
		out[i] = types.FieldDef{
			Angle:       f.AngleDeg,
			ImageHeight: f.ImageHeight,
			Direction:   []float64{0, 1},
		}
	}
	return out
}

// normalizeDir returns the unit vector of a non-degenerate 2D direction,
// falling back to the Y axis.
func normalizeDir(dir []float64) (float64, float64) {
	dx, dy := dir[0], dir[1]
	norm := math.Hypot(dx, dy)
	if norm == 0 {
		return 0, 1
	}
	return dx / norm, dy / norm
}

// fieldDir resolves a field's image-plane azimuth to a unit vector. It returns
// ok=false when the field carries no explicit direction (the caller keeps the
// Y-axis default).
func fieldDir(dir []float64) (float64, float64, bool) {
	if len(dir) < 2 {
		return 0, 1, false
	}
	dx, dy := normalizeDir(dir)
	return dx, dy, true
}

// UpdatePupils re-derives each config's dynamic entrance pupil at the current
// variable state x and stores it as the grid centring for the rest of the DLS
// iteration (the solver calls it once per iteration). The pupil therefore
// follows the lens during optimisation — the aperture position moves — while
// staying frozen within one iteration so the base-point and Jacobian residual
// evaluations share the same grid centring.
func (o *Optimizer) UpdatePupils(x []float64) {
	configSurfaces, tempGC := o.applyVariables(x)
	gc := effectiveGC(o.gc, tempGC)
	o.updateGlassRoles(configSurfaces, gc)
	pol := types.NewCircularJones(true)

	for ci := range o.configs {
		cfg := &o.configs[ci]
		if cfg.refSurface <= 0 || len(cfg.fieldDefs) == 0 {
			continue
		}
		surfaces := configSurfaces[cfg.id]
		results := chief.DetermineChiefRaysGrid(
			types.System{Surfaces: surfaces, StopSurface: cfg.stopSurface},
			cfg.fieldDefs, cfg.refSurface, o.numRays, gc, pol,
			effectiveReferenceWavelength(cfg.referenceWavelength), false, types.GridPolar, nil, nil, nil,
		)
		for i, r := range results {
			if r.EntrancePupil == nil {
				continue
			}
			if cfg.pupilZs == nil {
				cfg.pupilZs = make(map[float64]float64)
			}
			if i < len(cfg.fieldDefs) {
				cfg.pupilZs[cfg.fieldDefs[i].Angle] = r.EntrancePupil.Center.Z
			}
			if i == 0 {
				cfg.pupilZ = r.EntrancePupil.Center.Z
			}
		}
	}
}

// updateGlassRoles recomputes each config's per-surface glass-role
// classification (paraxial.GlassRoles) at the current variable state and
// freezes it for the rest of the DLS iteration. Called at the top of
// UpdatePupils, so the role assignment follows the lens (element powers and
// marginal-ray heights move with the variables) while staying constant within
// one iteration — keeping the base-point and Jacobian glass_role residuals
// consistent.
func (o *Optimizer) updateGlassRoles(configSurfaces map[string][]types.Surface, gc *glass.Catalog) {
	if o.roleTargets == nil {
		o.roleTargets = make(map[string]map[int]paraxial.ElementRole)
	}
	for ci := range o.configs {
		cfg := &o.configs[ci]
		surfaces := configSurfaces[cfg.id]
		roles := paraxial.GlassRoles(surfaces, gc)
		frozen := make(map[int]paraxial.ElementRole, len(roles))
		for _, r := range roles {
			for _, id := range r.SurfaceIDs {
				frozen[id] = r
			}
		}
		o.roleTargets[cfg.id] = frozen
	}
}

type Optimizer struct {
	configs          []config
	variables        []Variable
	gc               *glass.Catalog
	glassOverrides   map[string]*types.Glass
	initialDiameters map[string]float64
	maxIter          int
	mu               float64
	tol              float64
	epsilon          float64
	numRays          int
	baseNumRays      int
	apertureMargin   float64
	apertureMarginMM float64
	muConMax         float64
	workers          int
	gridRotation     float64
	logger           dls.Logger
	centralDiff      bool
	bfgs             bool
	adaptiveDamping  *types.AdaptiveDampingConfig
	hull             *glass.ConvexHull
	hullMargin       float64
	hullWeight       float64
	hullPairs        []glassPair
	// roleTargets holds the per-config, per-surface glass-role classification
	// (paraxial.ElementRole) frozen at the top of each DLS iteration by
	// updateGlassRoles, so the base-point and Jacobian residuals share one role
	// assignment — the same frozen-per-iteration convention as the pupil.
	roleTargets map[string]map[int]paraxial.ElementRole
	// Bounded penalties for merit terms that cannot be evaluated. Defaults:
	// spot 0.1, opd 0.01, wavefront 0.001 (mm).
	spotDegenerate      float64
	opdDegenerate       float64
	wavefrontDegenerate float64
	// Conditional merit blend (optimization.merit_schedule). Nil keeps the
	// fixed per-config merit. When set, modeWeights holds the frozen per-mode
	// weights recomputed at the top of every DLS iteration.
	meritSchedule *meritSchedule
	modeWeights   map[string]float64
	initialMerit    float64
	initialSpotRatio float64 // initial spot_diffraction ratio for normalisation
	modeChanges     int
	lastMetric      float64
	// stop, when set, is forwarded into dls.Options.Stop so the solver aborts
	// mid-solve (returning the best point found so far with Status
	// "interrupted") once the channel is closed. nil disables interruption.
	stop <-chan struct{}
	// powerSolve holds the per-config power-preserving solve entries: for each
	// listed solve surface, the target thin-lens power (snapshotted from the
	// config's initial surfaces). applyPowerSolve reconciles those curvatures
	// after every variable application so the element powers stay constant
	// (optimization.power_solve).
	powerSolve map[string][]powerSolveEntry
	// powerSolveEnabled gates applyPowerSolve. It is normally true once set;
	// the escape glass phase toggles it so the power-preserving solve applies
	// only during the dedicated glass phase while the escape/clean phases run
	// with full curvature freedom.
	powerSolveEnabled bool
	// glassMerit holds the per-config merit terms used during the glass phase
	// (colour-only LCA/TCA). glassMeritActive routes scheduledTerms to them.
	glassMerit       map[string][]meritTerm
	glassMeritActive bool
	// pinnedVars remembers the original Min/Max of variables locked by
	// EnterGlassPhase, so ExitGlassPhase can restore them exactly.
	pinnedMin []float64
	pinnedMax []float64
	// regionActive holds the Okudaira Region Active Method state. Nil when
	// the method is disabled (all constraints are treated as always active,
	// legacy behaviour). When non-nil, per-config constraint states track
	// the active/inactive flag and Lagrange multiplier for each constraint.
	regionActiveCfg *types.RegionActiveConfig
	regionActive    []*regionActiveState // per-config region-active state
}

// regionActiveState is the per-config mutable state for the Region Active
// Method. It holds the flattened list of constraint states across all configs.
type regionActiveState struct {
	configID string
	states   []constraint.RegionActiveState
}

// SetPowerSolveEnabled toggles the power-preserving hard solve. The escape
// glass phase enables it so the element powers stay constant while only the
// glass dispersions are free; the other escape phases disable it to keep the
// full curvature freedom. A false value leaves the (empty) powerSolve map in
// place so EnterGlassPhase can re-enable it.
func (o *Optimizer) SetPowerSolveEnabled(enabled bool) {
	o.powerSolveEnabled = enabled
}

// SetGlassMerit installs the per-config merit terms used during the glass
// phase (colour-only axial/lateral chromatic aberration). It is inactive until
// EnterGlassPhase activates it. Passing empty terms (or nil) removes the
// installed glass merit for the config.
func (o *Optimizer) SetGlassMerit(configID string, terms []types.MeritTerm) {
	if o.glassMerit == nil {
		o.glassMerit = make(map[string][]meritTerm)
	}
	if len(terms) == 0 {
		delete(o.glassMerit, configID)
		return
	}
	mt := make([]meritTerm, 0, len(terms))
	for _, t := range terms {
		tm := meritTerm{
			kind:        t.Kind,
			fieldDirX:   0,
			fieldDirY:   1,
			fieldIndex:  -1,
			wavelength:  t.Wavelength,
			wavelength2: t.Wavelength2,
			weight:      t.Weight,
			target:      t.Target,
			fraction:    t.Fraction,
			fieldWeight: 1.0,
			wavWeight:   1.0,
		}
		// Resolve the term's field angle from the config's fields (angle or
		// image height), matching buildMeritTermFromTypes.
		if c := findConfigByID(o.configs, configID); c != nil {
			for i, f := range c.fields {
				if f.ID == t.Field {
					tm.fieldIndex = i
					if f.AngleDeg != 0 || f.ImageHeight == 0 {
						tm.fieldAngle = f.AngleDeg
					} else {
						tm.useImageHeight = true
						tm.imageHeight = f.ImageHeight
					}
					if f.Weight > 0 {
						tm.fieldWeight = f.Weight
					}
					break
				}
			}
		}
		mt = append(mt, tm)
	}
	o.glassMerit[configID] = mt
}

// EnterGlassPhase starts the glass phase: it enables the power-preserving
// solve, locks every variable except the glass dispersions (nd/vd) to its
// current value x (so only the glasses move), and routes the merit to the
// installed glass (colour-only) terms. x is the physical variable vector at
// the start of the phase (the escape-DLS result). The variable dimension is
// unchanged, so the escape store/distance semantics stay intact.
func (o *Optimizer) EnterGlassPhase(x []float64) {
	o.powerSolveEnabled = true
	o.lockNonGlassVariables(x)
	o.glassMeritActive = true
}

// ExitGlassPhase ends the glass phase: it disables the power-preserving solve,
// restores every variable's original Min/Max (full curvature freedom), and
// restores the ordinary merit. Called before the clean DLS.
func (o *Optimizer) ExitGlassPhase() {
	o.powerSolveEnabled = false
	o.unlockVariables()
	o.glassMeritActive = false
}

// lockNonGlassVariables pins every variable not in the glass-free set (nd/vd)
// to its current value x[i] by setting Min==Max, remembering the originals for
// ExitGlassPhase. DLS keeps a Min==Max variable at its fixed value (solver
// clamps the scale to 1.0).
func (o *Optimizer) lockNonGlassVariables(x []float64) {
	o.pinnedMin = make([]float64, len(o.variables))
	o.pinnedMax = make([]float64, len(o.variables))
	for i := range o.variables {
		o.pinnedMin[i] = o.variables[i].Min
		o.pinnedMax[i] = o.variables[i].Max
		v := &o.variables[i]
		if isGlassParam(v.Param) {
			continue
		}
		val := x[i]
		v.Min = val
		v.Max = val
	}
}

func (o *Optimizer) unlockVariables() {
	if o.pinnedMin == nil {
		return
	}
	for i := range o.variables {
		if o.pinnedMin[i] == o.pinnedMax[i] && isGlassParam(o.variables[i].Param) {
			continue
		}
		o.variables[i].Min = o.pinnedMin[i]
		o.variables[i].Max = o.pinnedMax[i]
	}
	o.pinnedMin = nil
	o.pinnedMax = nil
}

// isGlassParam reports whether a variable parameter belongs to the glass-free
// set that stays active during the glass phase.
func isGlassParam(param string) bool {
	return param == "nd" || param == "vd"
}

// SetStop wires the mid-solve stop channel into the DLS options returned by
// Options. Closing it asks the running solve to abort at the next checkpoint
// (top of an iteration, after the pupil update / Jacobian, inside the line
// search) and return its best-so-far state instead of a converged result.
func (o *Optimizer) SetStop(stop <-chan struct{}) {
	o.stop = stop
}

// SetApertureMarginMM sets the physical clearance (mm) added to each
// auto_aperture final diameter (matching chief --clear-aperture-margin-mm).
// Values <= 0 keep the default 0.2 mm.
func (o *Optimizer) SetApertureMarginMM(mm float64) {
	if mm > 0 {
		o.apertureMarginMM = mm
	}
}

// SetDegenerate overrides the bounded penalties for merit terms that cannot
// be evaluated (pupil grid with no valid rays, or a failed wavefront fit).
// Non-positive values keep the built-in defaults (spot 0.1, opd 0.01,
// wavefront 0.001 mm). See the DegenerateConfig YAML section.
func (o *Optimizer) SetDegenerate(spot, opd, wavefront float64) {
	if spot > 0 {
		o.spotDegenerate = spot
	}
	if opd > 0 {
		o.opdDegenerate = opd
	}
	if wavefront > 0 {
		o.wavefrontDegenerate = wavefront
	}
}

// SetPowerSolve enables the power-preserving hard solve for the given solve
// surface IDs (optimization.power_solve.surfaces). For each listed surface the
// thin-lens power of the element containing it is snapshotted from the config's
// initial surfaces as the target, and applyPowerSolve recomputes that surface's
// curvature after every variable application so the power stays fixed. Surfaces
// not part of a refractive lens element are silently skipped. An empty list is
// a no-op. The snapshot is per-config so a multi-config zoom pins each config's
// own element powers.
func (o *Optimizer) SetPowerSolve(solveSurfaces []int) {
	if len(solveSurfaces) == 0 {
		return
	}
	m := make(map[string][]powerSolveEntry, len(o.configs))
	// The targets come from a curvature-based element-power snapshot (the
	// initial surfaces have not been Precomputed), matching the power the solve
	// preserves.
	for ci := range o.configs {
		cfg := &o.configs[ci]
		for _, id := range solveSurfaces {
			phi, ok := powerTargetForSurface(cfg.surfaces, o.gc, id)
			if !ok {
				continue
			}
			m[cfg.id] = append(m[cfg.id], powerSolveEntry{solveID: id, targetPhi: phi})
		}
	}
	o.powerSolve = m
	// The solve is active for a plain optimize run; the escape glass phase
	// toggles it (SetPowerSolveEnabled) so it applies only during that phase.
	o.powerSolveEnabled = true
}

// SetAdaptiveDamping configures per-variable adaptive damping. nil disables
// adaptive damping (legacy μI behaviour).
func (o *Optimizer) SetAdaptiveDamping(cfg *types.AdaptiveDampingConfig) {
	o.adaptiveDamping = cfg
}

// powerTargetForSurface returns the current thin-lens power (curvature-based)
// of the refractive element containing surface id (for use as a power-preserve
// target), with a boolean reporting whether the surface belongs to a solvable
// element. Mirrors report not-ok so they are never pinned.
func powerTargetForSurface(surfaces []types.Surface, gc *glass.Catalog, id int) (float64, bool) {
	for _, s := range surfaces {
		if s.ID == id && s.Reflects() {
			return 0, false
		}
	}
	return paraxial.ElementPowerCurvature(surfaces, gc, id), true
}

// NewOptimizer builds a single-configuration Optimizer (backward-compatible
// entry point).
func NewOptimizer(cfg Config) *Optimizer {
	c := config{
		id:          "config1",
		weight:      1.0,
		stopSurface: cfg.StopSurface,
		refSurface:  cfg.RefSurface,
		pupilZ:      resolvePupilZ(cfg.Surfaces, cfg.StopSurface, cfg.PupilZ),
		fieldDefs:   fieldDefsFromItems(cfg.Fields),
		surfaces:    cfg.Surfaces,
		fields:      cfg.Fields,
		constraints: cfg.Constraints,
	}
	for _, t := range cfg.MeritTerms {
		dx, dy := 0.0, 1.0
		if len(t.FieldDir) >= 2 {
			dx, dy = normalizeDir(t.FieldDir)
		}
		c.meritTerms = append(c.meritTerms, meritTerm{
			kind:        t.Kind,
			fieldAngle:  t.FieldAngle,
			fieldDirX:   dx,
			fieldDirY:   dy,
			fieldIndex:  t.FieldIndex,
			fieldWeight: t.FieldWeight,
			wavelength:  t.Wavelength,
			wavelength2: t.Wavelength2,
			wavWeight:   t.WavWeight,
			weight:      t.Weight,
			target:      t.Target,
			fraction:    t.Fraction,
			surfaceSet:  append([]int(nil), t.SurfaceSet...),
		})
	}
	if len(cfg.MeritModes) > 0 {
		c.meritModes = make(map[string][]meritTerm, len(cfg.MeritModes))
		for _, m := range cfg.MeritModes {
			var terms []meritTerm
			for _, t := range m.Terms {
				// Resolve field angle/direction from the config fields.
				var fieldAngle, fieldWeight float64
				var fieldDirX, fieldDirY float64 = 0.0, 1.0
				for _, f := range cfg.Fields {
					if f.ID == t.Field {
						fieldAngle = f.AngleDeg
						fieldWeight = f.Weight
						if len(f.Direction) >= 2 {
							fieldDirX, fieldDirY = normalizeDir(f.Direction)
						}
						break
					}
				}
				if fieldWeight == 0 {
					fieldWeight = 1.0
				}
				terms = append(terms, meritTerm{
					kind:        t.Kind,
					fieldAngle:  fieldAngle,
					fieldDirX:   fieldDirX,
					fieldDirY:   fieldDirY,
					fieldIndex:  t.Field,
					fieldWeight: fieldWeight,
					wavelength:  t.Wavelength,
					wavelength2: t.Wavelength2,
					wavWeight:   1.0,
					weight:      t.Weight,
					target:      t.Target,
					fraction:    t.Fraction,
					surfaceSet:  append([]int(nil), t.SurfaceSet...),
				})
			}
			c.meritModes[m.Name] = terms
		}
	}
	variables := make([]Variable, len(cfg.Variables))
	for i, v := range cfg.Variables {
		variables[i] = Variable{
			Name:      v.Name,
			SurfaceID: v.SurfaceID,
			GlassName: v.GlassName,
			Param:     v.Param,
			Min:       v.Min,
			Max:       v.Max,
			Config:    "config1",
		}
	}
	opt := newOptimizer(
		[]config{c}, variables, cfg.GlassCatalog,
		cfg.MaxIter, cfg.Mu, cfg.Tol, cfg.Epsilon, cfg.ApertureMargin, cfg.NumRays,
		cfg.MuConMax, cfg.Workers, cfg.Logger, cfg.Hull, cfg.HullMargin, cfg.HullWeight,
		cfg.CentralDiff, cfg.BFGS, cfg.AdaptiveDamping, cfg.RegionActive,
	)
	opt.SetApertureMarginMM(cfg.ApertureMarginMM)
	opt.SetDegenerate(cfg.SpotDegenerate, cfg.OPDDegenerate, cfg.WavefrontDegenerate)
	opt.SetPowerSolve(cfg.PowerSolveSurfaces)
	return opt
}

// NewMultiOptimizer builds a unified Optimizer over one or more configs,
// with shared variables (one x driving many bindings) and local variables
// (one x driving a single surface of one config).
func NewMultiOptimizer(configs []ConfigInput, sharedVars []types.SharedVariable, localVars []types.LocalVariableDef, gc *glass.Catalog, maxIter int, mu, tol, epsilon, apertureMargin float64, numRays int, muConMax float64, workers int, logger dls.Logger, hull *glass.ConvexHull, hullMargin, hullWeight float64, centralDiff, bfgs bool, adaptiveDamping *types.AdaptiveDampingConfig, raCfg *types.RegionActiveConfig) *Optimizer {
	internal := make([]config, len(configs))
	for i, ci := range configs {
		c := config{
			id:          ci.ID,
			weight:      ci.Weight,
			stopSurface: ci.StopSurface,
			refSurface:  ci.RefSurface,
			pupilZ:      resolvePupilZ(ci.Surfaces, ci.StopSurface, ci.PupilZ),
			fieldDefs:   fieldDefsFromItems(ci.Fields),
			surfaces:    ci.Surfaces,
			fields:      ci.Fields,
			wavelengths: ci.Wavelengths,
			constraints: ci.Constraints,
		}
		if len(ci.MeritModes) > 0 {
			c.meritModes = make(map[string][]meritTerm, len(ci.MeritModes))
			c.meritModeNumRays = make(map[string]int, len(ci.MeritModes))
			for _, m := range ci.MeritModes {
				var terms []meritTerm
				for _, t := range m.Terms {
					terms = append(terms, buildMeritTermFromTypes(t, ci))
				}
				c.meritModes[m.Name] = terms
				if m.NumRays > 0 {
					c.meritModeNumRays[m.Name] = m.NumRays
				}
			}
		}
		for _, t := range ci.MeritTerms {
			c.meritTerms = append(c.meritTerms, buildMeritTermFromTypes(t, ci))
		}
		internal[i] = c
	}

	var variables []Variable
	for si, sv := range sharedVars {
		if !sv.Active {
			continue
		}
		name := sv.Name
		if name == "" {
			name = fmt.Sprintf("shared_%d", si)
		}
		variables = append(variables, Variable{
			Name:     name,
			IsShared: true,
			Bindings: sv.Bindings,
			Min:      sv.Min,
			Max:      sv.Max,
		})
	}
	for li, lv := range localVars {
		if !lv.Active {
			continue
		}
		name := lv.Name
		if name == "" {
			name = fmt.Sprintf("local_%d", li)
		}
		variables = append(variables, Variable{
			Name:      name,
			SurfaceID: lv.Target.ID,
			Param:     lv.Target.Param,
			Min:       lv.Min,
			Max:       lv.Max,
			Config:    lv.Config,
		})
	}

	return newOptimizer(
		internal, variables, gc,
		maxIter, mu, tol, epsilon, apertureMargin, numRays, muConMax,
		workers, logger, hull, hullMargin, hullWeight,
		centralDiff, bfgs, adaptiveDamping, raCfg,
	)
}

func buildMeritTermFromTypes(t types.MeritTerm, ci ConfigInput) meritTerm {
	mt := meritTerm{
		kind:        t.Kind,
		fieldIndex:  -1,
		wavelength:  t.Wavelength,
		wavelength2: t.Wavelength2,
		weight:      t.Weight,
		target:      t.Target,
		fraction:    t.Fraction,
		surfaceSet:  append([]int(nil), t.SurfaceSet...),
		fieldDirX:   0,
		fieldDirY:   1,
		fieldWeight: 1.0,
		wavWeight:   1.0,
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
	for _, w := range ci.Wavelengths {
		if math.Abs(w.Value-t.Wavelength) < 1e-12 {
			if w.Weight > 0 {
				mt.wavWeight = w.Weight
			}
			break
		}
	}
	return mt
}

func newOptimizer(configs []config, variables []Variable, gc *glass.Catalog, maxIter int, mu, tol, epsilon, apertureMargin float64, numRays int, muConMax float64, workers int, logger dls.Logger, hull *glass.ConvexHull, hullMargin, hullWeight float64, centralDiff, bfgs bool, adaptiveDamping *types.AdaptiveDampingConfig, raCfg *types.RegionActiveConfig) *Optimizer {
	if maxIter <= 0 {
		maxIter = 100
	}
	if mu <= 0 {
		mu = 1.0
	}
	if tol <= 0 {
		tol = 1e-6
	}
	if epsilon <= 0 {
		epsilon = 1e-6
	}
	if numRays <= 0 {
		numRays = 64
	}
	// aperture_margin < 1.0 makes the pupil grid smaller than the aperture,
	// which clips rays at surface edges and stalls DLS convergence.
	if apertureMargin < 1.0 {
		apertureMargin = 1.0
	}
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}

	// Config weights default to 1.0 so a single-config YAML that omits
	// configs[].weight still contributes its full merit.
	for i := range configs {
		if configs[i].weight <= 0 {
			configs[i].weight = 1.0
		}
	}

	variablesCopy := make([]Variable, len(variables))
	copy(variablesCopy, variables)

	glassOverrides := make(map[string]*types.Glass)
	for i := range variablesCopy {
		v := &variablesCopy[i]
		if v.IsShared {
			continue
		}
		if v.Param == "nd" || v.Param == "vd" {
			cfg := findConfigByID(configs, v.Config)
			if cfg == nil {
				continue
			}
			key := resolveGlassKeyFromSurface(cfg.surfaces, v.SurfaceID)
			v.GlassName = key
			if key != "" {
				if g, ok := gc.Lookup(key); ok {
					// Strip the key from the surface material: convert the
					// keyed catalog reference to an inline model glass so
					// applyVariables uses the direct nd/vd path and the
					// output YAML carries inline model values.
					for i := range cfg.surfaces {
						if cfg.surfaces[i].ID == v.SurfaceID && cfg.surfaces[i].Material.HasKey() {
							cfg.surfaces[i].Material = types.Material{ND: g.ND, VD: g.VD}
							break
						}
					}
					cp := *g
					cp.Label = key
					if cp.Type == types.GlassTypeCatalog {
						cp.Type = types.GlassTypeModel
						cp.DispersionFormula = ""
					}
					glassOverrides[key] = &cp
				}
			}
		}
	}

	initialDiameters := make(map[string]float64)
	for ci := range configs {
		cfg := &configs[ci]
		for _, s := range cfg.surfaces {
			if s.AutoAperture {
				initialDiameters[cfgSurfKey(cfg.id, s.ID)] = s.Diameter
			}
		}
	}

	hullPairs := buildHullPairs(variablesCopy)

	opt := &Optimizer{
		configs:             configs,
		variables:           variablesCopy,
		gc:                  gc,
		glassOverrides:      glassOverrides,
		initialDiameters:    initialDiameters,
		maxIter:             maxIter,
		mu:                  mu,
		tol:                 tol,
		epsilon:             epsilon,
		numRays:             numRays,
		baseNumRays:         numRays,
		apertureMargin:      apertureMargin,
		apertureMarginMM:    0.2,
		muConMax:            muConMax,
		workers:             workers,
		logger:              logger,
		centralDiff:         centralDiff,
		bfgs:                bfgs,
		adaptiveDamping:     adaptiveDamping,
		hull:                hull,
		hullMargin:          hullMargin,
		hullWeight:          hullWeight,
		hullPairs:           hullPairs,
		spotDegenerate:      0.1,
		opdDegenerate:       0.01,
		wavefrontDegenerate: 0.001,
	}

	// Build the flattened region-active constraint states (all configs).
	// nil RegionActiveConfig means disabled: all constraints are always active.
	if raCfg != nil {
		opt.buildRegionActiveStates(raCfg)
	}

	return opt
}

func findConfigByID(configs []config, id string) *config {
	for i := range configs {
		if configs[i].id == id {
			return &configs[i]
		}
	}
	return nil
}

// primaryConfig returns the first config, used by the public single-term
// evaluator which has no config context.
func (o *Optimizer) primaryConfig() *config {
	if len(o.configs) == 0 {
		return &config{}
	}
	return &o.configs[0]
}

// buildRegionActiveStates initialises the region-active state from the
// configuration. Called once from newOptimizer when region_active is enabled.
func (o *Optimizer) buildRegionActiveStates(raCfg *types.RegionActiveConfig) {
	o.regionActiveCfg = raCfg
	o.regionActive = make([]*regionActiveState, len(o.configs))
	for ci := range o.configs {
		cfg := &o.configs[ci]
		states := constraint.BuildRegionActiveStates(cfg.constraints)
		o.regionActive[ci] = &regionActiveState{
			configID: cfg.id,
			states:   states,
		}
	}
}

// UpdateRegionActiveSet implements dls.RegionActiveUpdater. It evaluates all
// constraints at the current x and applies the hysteresis-based active-set
// update and Lagrange multiplier update. Called once per DLS iteration at the
// current x, so the active set is frozen for the rest of the iteration.
func (o *Optimizer) UpdateRegionActiveSet(x []float64) {
	if o.regionActiveCfg == nil || len(o.regionActive) == 0 {
		return
	}
	configSurfaces, tempGC := o.applyVariables(x)
	gc := effectiveGC(o.gc, tempGC)
	defaults := constraint.EffectiveDefaults(o.regionActiveCfg)

	for ci := range o.configs {
		cfg := &o.configs[ci]
		surfaces := configSurfaces[cfg.id]
		ras := o.regionActive[ci]
		if ras == nil {
			continue
		}

		// Compute violations for each constraint.
		violations := make([]float64, len(cfg.constraints))
		for i, c := range cfg.constraints {
			if !c.Active {
				violations[i] = 0
				continue
			}
			angle := o.constraintFieldAngle(cfg, c, surfaces, gc)
			value := constraint.Evaluate(c, surfaces, angle, gc, o.numRays, o.apertureMargin, cfg.stopSurface, cfg.pupilZ)
			err := constraint.ComputeError(c.Kind, value, c)
			violations[i] = err // raw violation (before weighting)
		}

		// Update active set with hysteresis and Lagrange multipliers.
		constraint.UpdateActiveSet(ras.states, violations, defaults)
	}
}

// ActiveConstraintIndices implements dls.ActiveConstraintIndices. It returns
// the flattened indices of constraints that are currently in the active set.
func (o *Optimizer) ActiveConstraintIndices() []int {
	if o.regionActiveCfg == nil || len(o.regionActive) == 0 {
		return nil // nil = all constraints active (legacy)
	}
	var indices []int
	offset := 0
	for ci := range o.configs {
		cfg := &o.configs[ci]
		ras := o.regionActive[ci]
		if ras == nil {
			// No region-active state for this config: all constraints active.
			for i := range cfg.constraints {
				if cfg.constraints[i].Active {
					indices = append(indices, offset+i)
				}
			}
			offset += len(cfg.constraints)
			continue
		}
		for i, s := range ras.states {
			if s.Active {
				indices = append(indices, offset+i)
			}
		}
		offset += len(cfg.constraints)
	}
	return indices
}

// ConstraintMultipliers implements dls.ConstraintMultipliers. It returns the
// flattened Lagrange multipliers for all constraints (active and inactive).
func (o *Optimizer) ConstraintMultipliers() []float64 {
	if o.regionActiveCfg == nil || len(o.regionActive) == 0 {
		return nil
	}
	var lambdas []float64
	for ci := range o.configs {
		ras := o.regionActive[ci]
		if ras == nil {
			cfg := &o.configs[ci]
			for range cfg.constraints {
				lambdas = append(lambdas, 0)
			}
			continue
		}
		lambdas = append(lambdas, constraint.LagrangeMultipliers(ras.states)...)
	}
	return lambdas
}

// SetConstraintMultipliers implements dls.ConstraintMultipliers setter. It
// writes back the Lagrange multipliers from the solver into the region-active
// state. Called after every accepted DLS step.
func (o *Optimizer) SetConstraintMultipliers(lambdas []float64) {
	if o.regionActiveCfg == nil || len(o.regionActive) == 0 {
		return
	}
	offset := 0
	for ci := range o.configs {
		ras := o.regionActive[ci]
		if ras == nil {
			cfg := &o.configs[ci]
			offset += len(cfg.constraints)
			continue
		}
		n := len(ras.states)
		if offset+n <= len(lambdas) {
			constraint.SetLambdas(ras.states, lambdas[offset:offset+n])
		}
		offset += n
	}
}

func (o *Optimizer) Variables() []dls.VariableInfo {
	result := make([]dls.VariableInfo, len(o.variables))
	for i, v := range o.variables {
		var param string
		if v.IsShared {
			if len(v.Bindings) > 0 {
				param = v.Bindings[0].Param
			}
		} else {
			param = v.Param
		}
		result[i] = dls.VariableInfo{
			Name:      v.Name,
			SurfaceID: v.SurfaceID,
			GlassName: v.GlassName,
			Param:     param,
			Min:       v.Min,
			Max:       v.Max,
		}
	}
	return result
}

func (o *Optimizer) Options() dls.Options {
	return dls.Options{
		MaxIter:        o.maxIter,
		Mu:             o.mu,
		Tol:            o.tol,
		Epsilon:        o.epsilon,
		NumRays:        o.numRays,
		ApertureMargin: o.apertureMargin,
		MuConMax:       o.muConMax,
		Workers:        o.workers,
		Logger:         o.logger,
		Stop:           o.stop,
		CentralDiff:    o.centralDiff,
		BFGS:           o.bfgs,
		AdaptiveDamping: o.adaptiveDamping,
	}
}

func (o *Optimizer) InitialState() []float64 {
	return o.getInitialState()
}

// effectiveGC returns the glass catalog reflecting any in-flight nd/vd
// overrides (tempGC), falling back to the base catalog.
func effectiveGC(base, temp *glass.Catalog) *glass.Catalog {
	if temp != nil {
		return temp
	}
	return base
}

// applyVariables maps the variable vector x onto the surfaces of every config
// and returns the updated surfaces plus the glass catalog reflecting any
// in-flight nd/vd overrides (nil when no override is active). It is pure: no
// Optimizer state is mutated, so the DLS Jacobian loop can be parallelised.
func (o *Optimizer) applyVariables(x []float64) (map[string][]types.Surface, *glass.Catalog) {
	configSurfaces := make(map[string][]types.Surface, len(o.configs))
	for ci := range o.configs {
		cfg := &o.configs[ci]
		s := make([]types.Surface, len(cfg.surfaces))
		for j, src := range cfg.surfaces {
			cp := src
			if src.Coefficients != nil {
				cp.Coefficients = append([]float64(nil), src.Coefficients...)
			}
			if src.Decenter != nil {
				cp.Decenter = append([]types.DecenterStep(nil), src.Decenter...)
			}
			s[j] = cp
		}
		configSurfaces[cfg.id] = s
	}

	needTempGC := false
	localOverrides := make(map[string]*types.Glass, len(o.glassOverrides))
	for k, g := range o.glassOverrides {
		cp := *g
		localOverrides[k] = &cp
	}
	for vi, v := range o.variables {
		val := x[vi]

		if v.IsShared {
			for _, b := range v.Bindings {
				surfaces, ok := configSurfaces[b.Config]
				if !ok {
					continue
				}
				idx := surfaceIndex(surfaces, b.ID)
				if idx < 0 {
					continue
				}
				scale := b.Scale
				if scale == 0 {
					scale = 1.0
				}
				SetSurfaceParam(&surfaces[idx], b.Param, scale*val+b.Offset)
			}
			continue
		}

		surfaces, ok := configSurfaces[v.Config]
		if !ok {
			continue
		}
		idx := surfaceIndex(surfaces, v.SurfaceID)
		if idx < 0 {
			continue
		}

		if ai, ok := AsphereCoefIndex(v.Param); ok {
			for len(surfaces[idx].Coefficients) <= ai {
				surfaces[idx].Coefficients = append(surfaces[idx].Coefficients, 0)
			}
			surfaces[idx].Coefficients[ai] = val
			continue
		}
		switch v.Param {
		case "curvature":
			surfaces[idx].Curvature = val
		case "conic":
			surfaces[idx].Conic = val
		case "thickness":
			surfaces[idx].Thickness = val
		case "diameter":
			surfaces[idx].Diameter = val
		case "radius":
			if val == 0 {
				surfaces[idx].Curvature = 0
			} else {
				surfaces[idx].Curvature = 1.0 / val
			}
		case "nd", "vd":
			// Inline model glass (nd/vd carried on the surface, no catalogue
			// key): apply the variable directly to the surface material. A
			// keyed material goes through the in-flight catalogue override.
			m := &surfaces[idx].Material
			if m.HasModel() && !m.HasKey() {
				switch v.Param {
				case "nd":
					m.ND = val
				case "vd":
					m.VD = val
				}
				continue
			}
			key := resolveGlassKeyFromSurface(surfaces, v.SurfaceID)
			if g, ok := localOverrides[key]; ok {
				switch v.Param {
				case "nd":
					g.ND = val
				case "vd":
					g.VD = val
				}
				needTempGC = true
			}
		}
	}

	var tempGC *glass.Catalog
	if needTempGC {
		tempGC = glass.NewCatalog()
		if o.gc != nil {
			for _, g := range o.gc.ByName {
				cp := *g
				tempGC.Add(cp)
			}
		}
		for _, ov := range localOverrides {
			cp := *ov
			tempGC.Add(cp)
		}
	}

	// Apply the power-preserving solve after the variables and in-flight glass
	// overrides are in place but before Precompute, so the solved curvature
	// feeds both ParaxialRadius (Precompute) and every downstream consumer.
	// The solve keeps each pinned element's thin-lens power at its initial
	// value, so the pure glass chromatic optimisation cannot drift the layout.
	o.applyPowerSolve(configSurfaces, effectiveGC(o.gc, tempGC))

	for ci := range o.configs {
		cfg := &o.configs[ci]
		surface.Precompute(configSurfaces[cfg.id])
	}

	return configSurfaces, tempGC
}

// applyPowerSolve reconciles every pinned solve surface's curvature so the
// thin-lens power of its element equals the snapshotted target. It is called
// from applyVariables (pure), so the DLS base-point and Jacobian residuals
// share one consistent power-preserving layout. It only runs while the
// power-preserving solve is enabled (powerSolveEnabled), so the escape
// glass phase can enable it selectively.
func (o *Optimizer) applyPowerSolve(configSurfaces map[string][]types.Surface, gc *glass.Catalog) {
	if !o.powerSolveEnabled {
		return
	}
	if len(o.powerSolve) == 0 {
		return
	}
	for cid, entries := range o.powerSolve {
		surfaces, ok := configSurfaces[cid]
		if !ok {
			continue
		}
		for _, e := range entries {
			paraxial.SolveElementPower(surfaces, gc, e.solveID, e.targetPhi)
		}
	}
}

// restoreDiameters resets auto_aperture surfaces to their initial diameters
// so sizeAutoApertures can measure the true geometric extent.
func (o *Optimizer) restoreDiameters(cfg *config, surfaces []types.Surface) {
	for i := range surfaces {
		key := cfgSurfKey(cfg.id, surfaces[i].ID)
		if d, ok := o.initialDiameters[key]; ok {
			surfaces[i].Diameter = d
		}
	}
}

// termFieldAngle returns the field angle for a merit term, converting
// image-height fields via ray tracing (surfaces-dependent).
func (o *Optimizer) termFieldAngle(cfg *config, term *meritTerm, surfaces []types.Surface, gc *glass.Catalog) float64 {
	if !term.useImageHeight {
		return term.fieldAngle
	}
	return o.imageHeightToFieldAngle(cfg, surfaces, term.imageHeight, term.wavelength, gc)
}

// constraintFieldAngle resolves the field angle of a constraint operand.
func (o *Optimizer) constraintFieldAngle(cfg *config, c types.ConstraintOperand, surfaces []types.Surface, gc *glass.Catalog) float64 {
	for _, f := range cfg.fields {
		if f.ID == c.Field {
			if f.AngleDeg != 0 || f.ImageHeight == 0 {
				return f.AngleDeg
			}
			return o.imageHeightToFieldAngle(cfg, surfaces, f.ImageHeight, 0, gc)
		}
	}
	return 0
}

// gridKey identifies a unique pupil-grid trace within a single merit
// evaluation. Within one evaluation the surfaces, frozen pupil, grid geometry
// and per-field angle are constant, so every grid merit term sharing a
// (field angle, wavelength) traces the identical grid.
type gridKey struct {
	configID   string
	fieldAngle float64
	wavelength float64
}

// evalGridCache is a per-evaluation cache of pupil-grid traces. The DLS solver
// calls ComputeResiduals concurrently for one goroutine per Jacobian column,
// so the cache must be created fresh inside each evaluation call (never stored
// on the Optimizer) to stay race-free; the cached traces are pure functions of
// the evaluation's surfaces and frozen pupil.
type evalGridCache struct {
	spots   map[gridKey][]dls.IPoint
	extents map[gridKey]map[int]float64
}

func newEvalGridCache() *evalGridCache {
	return &evalGridCache{
		spots:   make(map[gridKey][]dls.IPoint),
		extents: make(map[gridKey]map[int]float64),
	}
}

// gridForTerm returns the cached (or freshly traced) pupil grid for a grid
// merit term. A nil cache disables caching.
func (o *Optimizer) gridForTerm(cache *evalGridCache, gc *glass.Catalog, surfaces []types.Surface, cfg *config, term *meritTerm) []dls.IPoint {
	angle := o.termFieldAngle(cfg, term, surfaces, gc)
	trace := func() []dls.IPoint {
		points, _ := dls.TraceFieldGrid(gc, surfaces, cfg.stopSurface, cfg.pupilZ, angle, []float64{0, 1}, term.wavelength, o.apertureMargin, o.numRays, o.gridRotation, o.gridWorkers())
		return points
	}
	if cache == nil {
		return trace()
	}
	key := gridKey{configID: cfg.id, fieldAngle: angle, wavelength: term.wavelength}
	if pts, ok := cache.spots[key]; ok {
		return pts
	}
	pts := trace()
	cache.spots[key] = pts
	return pts
}

// traceFieldGrid traces the pupil grid for a merit term and returns the spot
// points.
func (o *Optimizer) traceFieldGrid(gc *glass.Catalog, surfaces []types.Surface, cfg *config, term *meritTerm) []dls.IPoint {
	return o.gridForTerm(nil, gc, surfaces, cfg, term)
}

// precomputeGrids traces all grid merit terms for cfg in parallel, storing the
// results in cache. This avoids redundant traces when multiple terms share the
// same (field, wavelength) key, and the parallel trace overlaps the CPU-bound
// ray-tracing across cores. The caller must have already called
// sizeAutoApertures (which may also populate cache.extents).
func (o *Optimizer) precomputeGrids(cfg *config, surfaces []types.Surface, gc *glass.Catalog, cache *evalGridCache) {
	// Collect unique grid keys from the scheduled terms.
	type traceJob struct {
		key   gridKey
		angle float64
		wl    float64
	}
	seen := make(map[gridKey]bool)
	var jobs []traceJob
	for _, st := range o.scheduledTerms(cfg) {
		if !isGridKind(st.term.kind) {
			continue
		}
		angle := o.termFieldAngle(cfg, st.term, surfaces, gc)
		key := gridKey{configID: cfg.id, fieldAngle: angle, wavelength: st.term.wavelength}
		if seen[key] {
			continue
		}
		seen[key] = true
		jobs = append(jobs, traceJob{key: key, angle: angle, wl: st.term.wavelength})
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
				pupilZ := cfg.pupilZ
				if cfg.pupilZs != nil {
					if z, ok := cfg.pupilZs[job.angle]; ok {
						pupilZ = z
					}
				}
				points, _ := dls.TraceFieldGrid(gc, surfaces, cfg.stopSurface, pupilZ, job.angle, []float64{0, 1}, job.wl, o.apertureMargin, o.numRays, o.gridRotation, o.gridWorkers())
				mu.Lock()
				cache.spots[job.key] = points
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
}

// isGridKind reports whether the merit kind is evaluated from the pupil-grid
// spot statistics (rather than a chief-ray/paraxial/Seidel/wavefront kind).
// The legacy empty kind means spot_rms.
func isGridKind(kind string) bool {
	switch kind {
	case "", dls.MeritSpotRMS, dls.MeritSpotRMST, dls.MeritSpotRMSS,
		dls.MeritSpotRMSWorst, dls.MeritSpotWeighted, dls.MeritSpotEERadius:
		return true
	}
	return false
}

// evaluateGridKind traces the pupil grid for a grid merit term and returns the
// term's value (the metric itself). spot_rms is the legacy metric; the new
// kinds use the flux-weighted spot statistics. The term's target is applied by
// the caller. A grid with no valid rays returns the bounded degenerate penalty
// (o.spotDegenerate) instead of the legacy 1e6 sentinel, so a fully clipped
// off-axis beam pushes the solver without exploding the merit.
func (o *Optimizer) evaluateGridKind(cfg *config, term *meritTerm, surfaces []types.Surface, gc *glass.Catalog, cache *evalGridCache) float64 {
	points := o.gridForTerm(cache, gc, surfaces, cfg, term)
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
	default:
		val = dls.ComputeSpotRMS(points)
	}
	if val >= 1e6 {
		return o.spotDegenerate
	}
	return val
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

// imageHeightToFieldAngle finds the field angle that lands the chief ray at
// the target image height, via bisection on the image-plane intersection.
func (o *Optimizer) imageHeightToFieldAngle(cfg *config, surfaces []types.Surface, targetHeight, wavelength float64, gc *glass.Catalog) float64 {
	path := dls.BuildPath(surfaces)
	engine := ray.NewEngine(gc, nil)

	apertureRadius := dls.ApertureRadiusForGrid(surfaces, cfg.stopSurface, wavelength, gc, o.apertureMargin)
	if apertureRadius <= 0 {
		return 0
	}

	pol := types.NewCircularJones(true)

	lo, hi := 0.0, 45.0
	for iter := 0; iter < 30; iter++ {
		mid := (lo + hi) / 2
		dir := raymath.DirectionFromAngle(mid)

		origin := types.Vec3{X: 0, Y: 0, Z: -100.0}
		r := types.Ray{
			Wavelength:         wavelength,
			Initial:            types.RayState{Origin: origin, Direction: dir},
			Path:               path,
			Jones:              pol,
			SkipGlassPathCheck: mid == 0,
		}
		result := engine.TraceRay(r, surfaces, false)
		if result.Error != "" {
			hi = mid
			continue
		}
		if len(result.Surfaces) == 0 {
			return 0
		}
		last := result.Surfaces[len(result.Surfaces)-1]
		y := last.Position.Y

		if math.Abs(y-targetHeight) < 1e-6 {
			return mid
		}
		if y < targetHeight {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}

// sizeAutoApertures measures the true geometric beam extent of every field
// (ignoring aperture clipping) and sizes every AutoAperture surface so its
// diameter covers the union envelope of all fields' marginal rays. Using all
// fields (rather than only the extreme field) keeps the lens large enough for
// the widest bundle. Callers must restore the initial diameters first. The
// per-(field, wavelength) extent grids are cached in cache when non-nil.
//
// Every config field is measured, not only the fields that happen to carry a
// grid merit term: a merit that drives off-axis fields purely through wavefront
// terms (e.g. wavefront_astigmatism / wavefront_sphere_rms on the corner) would
// otherwise undersize the apertures to the on-axis beam and clip the off-axis
// wavefront grid, collapsing the corner fit.
func (o *Optimizer) sizeAutoApertures(cfg *config, surfaces []types.Surface, gc *glass.Catalog, cache *evalGridCache) {
	extents := make(map[int]float64)

	// The extents are geometric (aperture-clipping skipped), so one
	// representative wavelength per field is enough for sizing.
	wl := effectiveReferenceWavelength(cfg.referenceWavelength)
	if len(cfg.wavelengths) > 0 {
		wl = cfg.wavelengths[0].Value
	}

	// Measure every field's beam. Fall back to the grid merit term angles when
	// the config has no explicit field list.
	angles := make(map[float64]bool)
	for fi := range cfg.fields {
		angles[o.fieldSizingAngle(cfg, &cfg.fields[fi], surfaces, gc, wl)] = true
	}
	if len(angles) == 0 {
		for ti := range cfg.meritTerms {
			term := &cfg.meritTerms[ti]
			if !isGridKind(term.kind) {
				continue
			}
			angles[o.termFieldAngle(cfg, term, surfaces, gc)] = true
		}
	}

	for angle := range angles {
		key := gridKey{configID: cfg.id, fieldAngle: angle, wavelength: wl}
		var perSurf map[int]float64
		if cache != nil {
			if m, ok := cache.extents[key]; ok {
				perSurf = m
			} else {
				perSurf = o.fieldExtents(cfg, surfaces, gc, &meritTerm{wavelength: wl}, angle)
				cache.extents[key] = perSurf
			}
		} else {
			perSurf = o.fieldExtents(cfg, surfaces, gc, &meritTerm{wavelength: wl}, angle)
		}
		for id, e := range perSurf {
			if e > extents[id] {
				extents[id] = e
			}
		}
	}

	for id, e := range extents {
		for i := range surfaces {
			if surfaces[i].ID == id && surfaces[i].AutoAperture {
				surfaces[i].Diameter = 2 * (e + o.apertureMarginMM)
			}
		}
	}
}

// fieldSizingAngle returns the beam angle used by sizeAutoApertures for a field
// item: angle fields directly, image-height fields converted through the current
// surfaces.
func (o *Optimizer) fieldSizingAngle(cfg *config, f *types.FieldItem, surfaces []types.Surface, gc *glass.Catalog, wl float64) float64 {
	if f.AngleDeg != 0 || f.ImageHeight == 0 {
		return f.AngleDeg
	}
	return o.imageHeightToFieldAngle(cfg, surfaces, f.ImageHeight, wl, gc)
}

// fieldExtents traces the per-surface max radial ray extent for one grid merit
// term, centred on the per-field entrance pupil when one is resolved.
func (o *Optimizer) fieldExtents(cfg *config, surfaces []types.Surface, gc *glass.Catalog, term *meritTerm, angle float64) map[int]float64 {
	pupilZ := cfg.pupilZ
	if cfg.pupilZs != nil {
		if z, ok := cfg.pupilZs[angle]; ok {
			pupilZ = z
		}
	}
	return dls.TraceFieldGridExtents(gc, surfaces, cfg.stopSurface, pupilZ, angle, []float64{0, 1}, term.wavelength, o.apertureMargin, o.extentRays(256), o.gridRotation, o.gridWorkers())
}

// extentRays returns the ray count for a beam-extent measurement. The extent
// grid must resolve the beam edge well enough that the sized auto_aperture
// diameters cover the true bundle (a coarse grid under-measures off-axis
// extents and the resulting lens vignettes the beam). floor raises a low
// numRays grid to the requested minimum density.
func (o *Optimizer) extentRays(floor int) int {
	if o.numRays >= floor {
		return o.numRays
	}
	return floor
}

func (o *Optimizer) EvaluateMerit(x []float64) float64 {
	configSurfaces, tempGC := o.applyVariables(x)
	gc := effectiveGC(o.gc, tempGC)

	merit := 0.0
	cache := newEvalGridCache()
	for ci := range o.configs {
		cfg := &o.configs[ci]
		surfaces := configSurfaces[cfg.id]

		o.restoreDiameters(cfg, surfaces)
		o.sizeAutoApertures(cfg, surfaces, gc, cache)
		o.precomputeGrids(cfg, surfaces, gc, cache)

		cfgMerit := 0.0
		for _, st := range o.scheduledTerms(cfg) {
			term := st.term
			if isGridKind(term.kind) {
				val := o.evaluateGridKind(cfg, term, surfaces, gc, cache)
				diff := val - term.target
				cfgMerit += st.scale * term.weight * term.fieldWeight * term.wavWeight * diff * diff
			} else {
				val := o.evaluateKindTerm(cfg, term, surfaces, gc, cache)
				diff := val - term.target
				cfgMerit += st.scale * term.weight * term.fieldWeight * term.wavWeight * diff * diff
			}
		}
		merit += cfg.weight * cfgMerit
	}

	if o.hull != nil {
		for _, pair := range o.hullPairs {
			merit += o.hull.Penalty(x[pair.ndIndex], x[pair.vdIndex], o.hullMargin, o.hullWeight)
		}
	}
	return merit
}

// MeritBreakdown evaluates the merit at x and returns the contribution of
// each merit term (and the objective total), so the value reported by DLS can
// be reconciled against an external evaluation (e.g. `chief` spot RMS).
func (o *Optimizer) MeritBreakdown(x []float64) map[string]float64 {
	configSurfaces, tempGC := o.applyVariables(x)
	gc := effectiveGC(o.gc, tempGC)

	out := make(map[string]float64)
	objTotal := 0.0
	cache := newEvalGridCache()
	for ci := range o.configs {
		cfg := &o.configs[ci]
		surfaces := configSurfaces[cfg.id]

		o.restoreDiameters(cfg, surfaces)
		o.sizeAutoApertures(cfg, surfaces, gc, cache)

		for _, st := range o.scheduledTerms(cfg) {
			term := st.term
			var contrib float64
			if isGridKind(term.kind) {
				val := o.evaluateGridKind(cfg, term, surfaces, gc, cache)
				diff := val - term.target
				contrib = st.scale * term.weight * term.fieldWeight * term.wavWeight * diff * diff
			} else {
				val := o.evaluateKindTerm(cfg, term, surfaces, gc, cache)
				diff := val - term.target
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
	out["objective_total"] = objTotal
	return out
}

func (o *Optimizer) ComputeResiduals(x []float64) []float64 {
	configSurfaces, tempGC := o.applyVariables(x)
	gc := effectiveGC(o.gc, tempGC)

	var allR []float64
	cache := newEvalGridCache()
	for ci := range o.configs {
		cfg := &o.configs[ci]
		surfaces := configSurfaces[cfg.id]

		o.restoreDiameters(cfg, surfaces)
		o.sizeAutoApertures(cfg, surfaces, gc, cache)
		o.precomputeGrids(cfg, surfaces, gc, cache)

		for _, st := range o.scheduledTerms(cfg) {
			term := st.term
			w := math.Sqrt(cfg.weight * st.scale * term.weight * term.fieldWeight * term.wavWeight)
			if isGridKind(term.kind) {
				val := o.evaluateGridKind(cfg, term, surfaces, gc, cache)
				allR = append(allR, w*(val-term.target))
			} else {
				val := o.evaluateKindTerm(cfg, term, surfaces, gc, cache)
				allR = append(allR, w*(val-term.target))
			}
		}
	}

	if o.hull != nil {
		for _, pair := range o.hullPairs {
			allR = append(allR, o.hull.Residual(x[pair.ndIndex], x[pair.vdIndex], o.hullMargin, o.hullWeight))
		}
	}
	return allR
}

func (o *Optimizer) ComputeConstraints(x []float64) []float64 {
	configSurfaces, tempGC := o.applyVariables(x)
	gc := effectiveGC(o.gc, tempGC)

	var allC []float64
	for ci := range o.configs {
		cfg := &o.configs[ci]
		surfaces := configSurfaces[cfg.id]

		o.restoreDiameters(cfg, surfaces)
		o.sizeAutoApertures(cfg, surfaces, gc, nil)

		for _, c := range cfg.constraints {
			if !c.Active {
				allC = append(allC, 0)
				continue
			}
			angle := o.constraintFieldAngle(cfg, c, surfaces, gc)
			value := constraint.Evaluate(c, surfaces, angle, gc, o.numRays, o.apertureMargin, cfg.stopSurface, cfg.pupilZ)
			err := constraint.ComputeError(c.Kind, value, c)
			w := c.Weight
			if w <= 0 {
				w = 1.0
			}
			allC = append(allC, math.Sqrt(w)*err)
		}
	}
	return allC
}

// ConstraintViolation reports an active constraint whose weighted residual
// magnitude exceeds a tolerance at the final state.
type ConstraintViolation struct {
	ID       string
	Config   string
	Kind     string
	Measure  string
	Residual float64
}

// FinalConstraintViolations evaluates the active constraints at x and returns
// those whose weighted residual magnitude exceeds tol.
func (o *Optimizer) FinalConstraintViolations(x []float64, tol float64) []ConstraintViolation {
	configSurfaces, tempGC := o.applyVariables(x)
	gc := effectiveGC(o.gc, tempGC)

	var out []ConstraintViolation
	for ci := range o.configs {
		cfg := &o.configs[ci]
		surfaces := configSurfaces[cfg.id]

		o.restoreDiameters(cfg, surfaces)
		o.sizeAutoApertures(cfg, surfaces, gc, nil)

		for _, c := range cfg.constraints {
			if !c.Active {
				continue
			}
			angle := o.constraintFieldAngle(cfg, c, surfaces, gc)
			value := constraint.Evaluate(c, surfaces, angle, gc, o.numRays, o.apertureMargin, cfg.stopSurface, cfg.pupilZ)
			err := constraint.ComputeError(c.Kind, value, c)
			w := c.Weight
			if w <= 0 {
				w = 1.0
			}
			residual := math.Sqrt(w) * err
			if math.Abs(residual) > tol {
				out = append(out, ConstraintViolation{
					ID:       c.ID,
					Config:   cfg.id,
					Kind:     string(c.Kind),
					Measure:  string(c.Measure),
					Residual: residual,
				})
			}
		}
	}
	return out
}

// FinalConstraintMeasurements evaluates every active constraint at x and
// returns its measured value and weighted residual. Unlike
// FinalConstraintViolations it reports all constraints, not just the violated
// ones, so callers (e.g. the optimize command) can record the actual value the
// constraint reached (for example the final vignetting factor).
func (o *Optimizer) FinalConstraintMeasurements(x []float64) []types.ConstraintMeasurement {
	configSurfaces, tempGC := o.applyVariables(x)
	gc := effectiveGC(o.gc, tempGC)

	var out []types.ConstraintMeasurement
	for ci := range o.configs {
		cfg := &o.configs[ci]
		surfaces := configSurfaces[cfg.id]

		o.restoreDiameters(cfg, surfaces)
		o.sizeAutoApertures(cfg, surfaces, gc, nil)

		for _, c := range cfg.constraints {
			if !c.Active {
				continue
			}
			angle := o.constraintFieldAngle(cfg, c, surfaces, gc)
			value := constraint.Evaluate(c, surfaces, angle, gc, o.numRays, o.apertureMargin, cfg.stopSurface, cfg.pupilZ)
			err := constraint.ComputeError(c.Kind, value, c)
			w := c.Weight
			if w <= 0 {
				w = 1.0
			}
			out = append(out, types.ConstraintMeasurement{
				ID:       c.ID,
				Config:   cfg.id,
				Kind:     string(c.Kind),
				Measure:  string(c.Measure),
				Field:    c.Field,
				Value:    value,
				Residual: math.Sqrt(w) * err,
			})
		}
	}
	return out
}

func (o *Optimizer) Optimize() Result {
	dlsResult := dls.Solve(o)

	x := make([]float64, len(dlsResult.Variables))
	for i, vs := range dlsResult.Variables {
		x[i] = vs.After
	}

	return Result{
		BeforeMerit: dlsResult.BeforeMerit,
		AfterMerit:  dlsResult.AfterMerit,
		Iterations:  dlsResult.Iterations,
		Status:      dlsResult.Status,
		Variables:   o.buildVariableStates(x),
	}
}

// FinalApertures returns the sized auto_aperture diameters of every config
// after applying x.
func (o *Optimizer) FinalApertures(x []float64) map[string]map[int]float64 {
	configSurfaces, tempGC := o.applyVariables(x)
	result := make(map[string]map[int]float64)

	for ci := range o.configs {
		cfg := &o.configs[ci]
		surfaces := configSurfaces[cfg.id]

		o.restoreDiameters(cfg, surfaces)
		o.finalAutoApertures(cfg, surfaces, effectiveGC(o.gc, tempGC))

		cfgResult := make(map[int]float64)
		for i := range surfaces {
			if surfaces[i].AutoAperture {
				cfgResult[surfaces[i].ID] = surfaces[i].Diameter
			}
		}
		result[cfg.id] = cfgResult
	}
	return result
}

// finalAutoApertures sizes the auto_aperture surfaces of cfg from the same
// dynamic-pupil hex grid as chief --clear-aperture, so the output lens covers
// the true beam envelope (fixed apertures included) plus the configured
// clearance. It runs once per config; the cheaper sizeAutoApertures is used
// during merit evaluation where exact extents are not required.
func (o *Optimizer) finalAutoApertures(cfg *config, surfaces []types.Surface, gc *glass.Catalog) {
	if cfg.refSurface <= 0 || len(cfg.fieldDefs) == 0 {
		o.sizeAutoApertures(cfg, surfaces, gc, nil)
		return
	}
	pol := types.NewCircularJones(true)
	results := chief.DetermineChiefRaysGrid(
		types.System{Surfaces: surfaces, StopSurface: cfg.stopSurface},
		cfg.fieldDefs, cfg.refSurface, o.extentRays(512), gc, pol,
		effectiveReferenceWavelength(cfg.referenceWavelength), false, types.GridHex, nil, nil, nil,
	)
	engine := ray.NewEngine(gc, nil)
	surface.Precompute(surfaces)
	path := dls.BuildPath(surfaces)
	env := chief.BeamEnvelope(results, engine, surfaces, path, effectiveReferenceWavelength(cfg.referenceWavelength), pol)
	for i := range surfaces {
		if !surfaces[i].AutoAperture {
			continue
		}
		if e := env[surfaces[i].ID]; e > 0 {
			surfaces[i].Diameter = 2 * (e + o.apertureMarginMM)
		}
	}
}

// FinalConfigs returns the surfaces of every config after applying x, with
// nd/vd-optimised model glasses materialised (surface materials rewritten to
// the optimised inline model glass).
func (o *Optimizer) FinalConfigs(x []float64) (map[string][]types.Surface, []types.Glass) {
	configSurfaces, _ := o.applyVariables(x)
	newGlasses := o.materializeGlasses(configSurfaces, x)
	return configSurfaces, newGlasses
}

// materializeGlasses collects the optimised nd/vd pairs and rewrites the
// affected surface materials (catalogue-keyed ones become inline model glass;
// inline models already carry the values from applyVariables).
func (o *Optimizer) materializeGlasses(configSurfaces map[string][]types.Surface, x []float64) []types.Glass {
	MaterializeGlassEntries(o.variables, x, o.gc,
		func(v Variable) (string, bool) {
			if v.IsShared {
				return "", false
			}
			return v.GlassName, v.GlassName != ""
		},
		func(origKey string, nd, vd float64) {
			for ci := range o.configs {
				cfgID := o.configs[ci].id
				for i := range configSurfaces[cfgID] {
					if configSurfaces[cfgID][i].Material.HasKey() && configSurfaces[cfgID][i].Material.Key == origKey {
						configSurfaces[cfgID][i].Material = types.Material{ND: nd, VD: vd}
					}
				}
			}
		})
	return nil
}

// AsphereCoefIndex maps an asphere coefficient parameter name (a4/a6/a8/a10/
// a12, or the array aliases coefficient_0..coefficient_4) to the index in
// types.Surface.Coefficients, which holds the h^(2i+4) coefficients.
func AsphereCoefIndex(param string) (int, bool) {
	switch param {
	case "a4", "coefficient_0":
		return 0, true
	case "a6", "coefficient_1":
		return 1, true
	case "a8", "coefficient_2":
		return 2, true
	case "a10", "coefficient_3":
		return 3, true
	case "a12", "coefficient_4":
		return 4, true
	}
	return 0, false
}

// SetSurfaceParam sets a single surface parameter by name.
func SetSurfaceParam(s *types.Surface, param string, val float64) {
	if ai, ok := AsphereCoefIndex(param); ok {
		for len(s.Coefficients) <= ai {
			s.Coefficients = append(s.Coefficients, 0)
		}
		s.Coefficients[ai] = val
		return
	}
	switch param {
	case "curvature":
		s.Curvature = val
	case "conic":
		s.Conic = val
	case "thickness":
		s.Thickness = val
	case "diameter":
		s.Diameter = val
	case "radius":
		if val == 0 {
			s.Curvature = 0
		} else {
			s.Curvature = 1.0 / val
		}
	}
}

func getSurfaceParam(s types.Surface, param string) float64 {
	if ai, ok := AsphereCoefIndex(param); ok {
		if ai < len(s.Coefficients) {
			return s.Coefficients[ai]
		}
		return 0
	}
	switch param {
	case "curvature":
		return s.Curvature
	case "conic":
		return s.Conic
	case "thickness":
		return s.Thickness
	case "diameter":
		return s.Diameter
	case "radius":
		return s.Radius()
	}
	return 0
}

func (o *Optimizer) getInitialState() []float64 {
	x := make([]float64, len(o.variables))
	for i, v := range o.variables {
		if v.IsShared {
			if v.Min == 0 && v.Max == 0 {
				v.Min, v.Max = -1, 1
			}
			x[i] = (v.Min + v.Max) / 2
			if len(v.Bindings) > 0 {
				b := v.Bindings[0]
				if cfg := findConfigByID(o.configs, b.Config); cfg != nil {
					if idx := surfaceIndex(cfg.surfaces, b.ID); idx >= 0 {
						if v0 := getSurfaceParam(cfg.surfaces[idx], b.Param); v0 != 0 {
							x[i] = v0
						}
					}
				}
			}
			if x[i] < v.Min {
				x[i] = v.Min
			} else if x[i] > v.Max {
				x[i] = v.Max
			}
			continue
		}

		cfg := findConfigByID(o.configs, v.Config)
		switch v.Param {
		case "curvature", "conic", "thickness", "diameter", "radius",
			"a4", "a6", "a8", "a10", "a12",
			"coefficient_0", "coefficient_1", "coefficient_2", "coefficient_3", "coefficient_4":
			if cfg != nil {
				if idx := surfaceIndex(cfg.surfaces, v.SurfaceID); idx >= 0 {
					x[i] = getSurfaceParam(cfg.surfaces[idx], v.Param)
				} else {
					x[i] = (v.Min + v.Max) / 2
				}
			} else {
				x[i] = (v.Min + v.Max) / 2
			}
		case "nd", "vd":
			if cfg != nil {
				if idx := surfaceIndex(cfg.surfaces, v.SurfaceID); idx >= 0 {
					m := cfg.surfaces[idx].Material
					if m.HasModel() && !m.HasKey() {
						if v.Param == "nd" {
							x[i] = m.ND
						} else {
							x[i] = m.VD
						}
					} else if m.HasKey() && o.gc != nil {
						if g, ok := o.gc.Lookup(m.Key); ok {
							if v.Param == "nd" {
								x[i] = g.ND
							} else {
								x[i] = g.VD
							}
						}
					}
				}
			}
			if x[i] == 0 {
				x[i] = (v.Min + v.Max) / 2
			}
		default:
			x[i] = (v.Min + v.Max) / 2
		}
	}
	return x
}

func (o *Optimizer) buildVariableStates(x []float64) []VariableState {
	states := make([]VariableState, len(o.variables))
	for i, v := range o.variables {
		st := VariableState{
			Name:   v.Name,
			Config: v.Config,
			After:  x[i],
		}

		if v.IsShared {
			if len(v.Bindings) > 0 {
				st.SurfaceID = v.Bindings[0].ID
				st.Param = v.Bindings[0].Param
			}
			for _, cfg := range o.configs {
				for _, b := range v.Bindings {
					if b.Config == cfg.id {
						if idx := surfaceIndex(cfg.surfaces, b.ID); idx >= 0 {
							st.Before = getSurfaceParam(cfg.surfaces[idx], b.Param)
						}
						break
					}
				}
				if st.Before != 0 {
					break
				}
			}
		} else {
			st.SurfaceID = v.SurfaceID
			st.Param = v.Param
			st.GlassName = v.GlassName
			if cfg := findConfigByID(o.configs, v.Config); cfg != nil {
				if idx := surfaceIndex(cfg.surfaces, v.SurfaceID); idx >= 0 {
					if v.Param == "nd" || v.Param == "vd" {
						m := cfg.surfaces[idx].Material
						switch {
						case m.HasModel() && !m.HasKey():
							if v.Param == "nd" {
								st.Before = m.ND
							} else {
								st.Before = m.VD
							}
						case m.HasKey() && o.gc != nil:
							if g, ok := o.gc.Lookup(m.Key); ok {
								if v.Param == "nd" {
									st.Before = g.ND
								} else {
									st.Before = g.VD
								}
							}
						}
					} else {
						st.Before = getSurfaceParam(cfg.surfaces[idx], v.Param)
					}
				}
			}
		}

		states[i] = st
	}
	return states
}

func buildHullPairs(variables []Variable) []glassPair {
	ndMap := make(map[string]int)
	vdMap := make(map[string]int)
	for i, v := range variables {
		if v.IsShared {
			continue
		}
		switch v.Param {
		case "nd":
			ndMap[v.GlassName] = i
		case "vd":
			vdMap[v.GlassName] = i
		}
	}
	var pairs []glassPair
	for name, ndIdx := range ndMap {
		if vdIdx, ok := vdMap[name]; ok {
			pairs = append(pairs, glassPair{ndIndex: ndIdx, vdIndex: vdIdx, name: name})
		}
	}
	return pairs
}

func resolveGlassKeyFromSurface(surfaces []types.Surface, id int) string {
	for _, s := range surfaces {
		if s.ID == id {
			if s.Material.HasKey() {
				return s.Material.Key
			}
			return ""
		}
	}
	return ""
}

func surfaceIndex(surfaces []types.Surface, id int) int {
	for i, s := range surfaces {
		if s.ID == id {
			return i
		}
	}
	return -1
}

func cfgSurfKey(configID string, surfID int) string {
	return configID + ":" + fmt.Sprint(surfID)
}
