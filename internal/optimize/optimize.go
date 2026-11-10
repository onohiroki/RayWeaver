package optimize

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"strings"
	"sync/atomic"

	"github.com/hiroki/rayweaver/internal/cam"
	"github.com/hiroki/rayweaver/internal/constraint"
	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/spectral"
	"github.com/hiroki/rayweaver/internal/types"
)

// Warnf reports a non-fatal optimisation warning to stderr. The cmd/rayweave
// binary wires it to its own stderr writer (main.go, like glass.Warnf); the
// default writes to os.Stderr so a library caller still sees the problem.
// Failures that used to be silent (a back-focus solve that cannot evaluate
// the wavefront) must stay visible: silently leaving a stale image plane is
// how an extracted minimum ships a lens that is tens of mm out of focus.
var Warnf = func(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

// Config is the single-configuration optimisation input. It is an adapter
// over the unified Optimizer: the configuration becomes config "config1" and
// the variables become local variables of that config.
type Config struct {
	Surfaces            []types.Surface
	Variables           []Variable
	MeritTerms          []MeritTerm
	Fields              []types.FieldItem
	Wavelengths         []types.WavelengthItem
	Spectrum            *types.SpectralConfig
	ReferenceWavelength float64
	// WavelengthNormalization is "sum" (default) or "none"; it applies to
	// wavelength_set expansion in MeritModes.
	WavelengthNormalization string
	Constraints             []types.ConstraintOperand
	GlassCatalog            *glass.Catalog
	CoatingCatalog          interface{}
	StopSurface             int
	RefSurface              int
	PupilZ                  float64
	MaxIter                 int
	Mu                      float64
	Tol                     float64
	Epsilon                 float64
	NumRays                 int
	ApertureMargin          float64
	ApertureMarginMM        float64
	MuConMax                float64
	Workers                 int
	Logger                  dls.Logger
	CentralDiff             bool
	BFGS                    bool
	Hull                    *glass.ConvexHull
	HullMargin              float64
	HullWeight              float64
	// Normalization enables automatic per-term scale normalization (nil =
	// disabled; residuals keep their raw units).
	Normalization *types.MeritNormalizationConfig
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
	// AdaptiveDamping configures per-variable adaptive damping (nil = built-in defaults).
	AdaptiveDamping *types.AdaptiveDampingConfig
	// PupilModel is the virtual entrance pupil configuration (nil when not in use).
	PupilModel *types.PupilModelConfig
	// RayDefinition is the document's chief-ray definition
	// (chief.chief_ray_definition; "" = the per-system default).
	RayDefinition string
	// WavefrontReference selects the OPD reference of the wavefront merit terms:
	// "" / "best_focus" (default) or "image_plane".
	WavefrontReference string
	// GlassAttraction configures the soft-min potential pulling nd/vd toward
	// real catalog glasses (nil = disabled).
	GlassAttraction *types.GlassAttractionConfig
}

// ConfigInput describes one configuration (zoom position) of a
// multi-configuration optimisation.
type ConfigInput struct {
	ID                      string
	Weight                  float64
	StopSurface             int
	RefSurface              int
	PupilZ                  float64
	ReferenceWavelength     float64
	Surfaces                []types.Surface
	Fields                  []types.FieldItem
	Wavelengths             []types.WavelengthItem
	Spectrum                *types.SpectralConfig
	WavelengthNormalization string
	MeritTerms              []types.MeritTerm
	MeritModes              []types.MeritMode
	Constraints             []types.ConstraintOperand
	RegionActive            *types.RegionActiveConfig
	PupilModel              *types.PupilModelConfig
	Normalization           *types.MeritNormalizationConfig
	// RayDefinition is the document's chief-ray definition
	// (chief.chief_ray_definition; "" = the per-system default).
	RayDefinition string
	// WavefrontReference selects the OPD reference of the wavefront merit
	// terms: "" / "best_focus" (default) or "image_plane" (see
	// types.OptimizationConfig.WavefrontReference).
	WavefrontReference string
}

// planeReference reports whether the wavefront merit terms must reference the
// delivered image plane instead of the per-field best focus.
func planeReference(ref string) bool {
	return strings.EqualFold(strings.TrimSpace(ref), "image_plane")
}

func effectiveReferenceWavelength(wavelength float64) float64 {
	if wavelength > 0 {
		return wavelength
	}
	return types.DefaultWavelength
}

type Result struct {
	BeforeMerit float64
	AfterMerit  float64
	Iterations  int
	Status      string
	Variables   []VariableState
	// AcceptedSteps counts the DLS iterations that reduced the merit; 0 after a
	// full run means the solver never moved.
	AcceptedSteps int
	// Diagnostic explains a stalled solve ("" when it made progress or
	// converged) — flat merit plus the worst Jacobian conditioning seen.
	Diagnostic string
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
	pupilZs map[float64]float64
	// pupilResolved reports whether pupilZ (and pupilZs) hold a pupil actually
	// resolved for this config rather than the struct's zero default. 0 mm is
	// a legal entrance-pupil Z (the virtual pupil range crosses it), so
	// consumers test this flag — via resolvedPupilZ — and never the value.
	pupilResolved bool
	fieldDefs     []types.FieldDef
	surfaces      []types.Surface
	fields        []types.FieldItem
	wavelengths   []types.WavelengthItem
	spectrum      *types.SpectralConfig
	meritTerms    []meritTerm
	// fieldResolutions holds one entry per position in fields — the angle the
	// run froze for it at the start geometry — and is produced by
	// freezeFieldAngles. Nil until the freeze has run; consumers then resolve
	// an image-height field on the fly (see frozenFieldAngle).
	fieldResolutions []fieldResolution
	// meritModes holds the config's named merit-mode term lists (from
	// configs[].merit_modes). Nil when the config uses its fixed merit.
	meritModes map[string][]meritTerm
	// meritModeNumRays holds the per-mode num_rays override (from
	// configs[].merit_modes[].num_rays). Nil when no mode declares num_rays.
	meritModeNumRays map[string]int
	constraints      []types.ConstraintOperand
	// pupilModel is the virtual entrance pupil config for this config (nil
	// when not in use). When set, UpdatePupils uses its axial_position/
	// diameter instead of running chief, and applyVariables handles
	// pupil_model type variables targeting it.
	pupilModel *types.PupilModelConfig
	// rayDefinition is the document's chief-ray definition
	// (chief.chief_ray_definition; "" = the per-system default) passed to
	// every chief pass this optimisation makes, so the pupil/chief ray the
	// solver sees matches what `chief` reports for the same document.
	rayDefinition string
	// wavefrontPlaneRef reports optimization.wavefront_reference: image_plane.
	// The wavefront merit terms then fit their paraboloid against the delivered
	// image plane instead of the per-field best focus, so the defocus term
	// carries the image-plane focus error (field curvature + system defocus).
	wavefrontPlaneRef bool
	// backFocusSolve is the per-config back-focus solve configuration (nil
	// when not in use). It is set by SetBackFocusSolve and used by
	// resolveBackFocusTarget.
	backFocusSolve *types.BackFocusSolveConfig
}

// meritTerm is the unified merit term: weights and (for angle fields) the
// field angle are resolved at construction; image-height fields are converted
// to an angle per evaluation because they depend on the current surfaces.
type meritTerm struct {
	kind                 string
	fieldAngle           float64
	useImageHeight       bool
	imageHeight          float64
	fieldWeight          float64
	wavelength           float64
	comparisonWavelength float64
	wavWeight            float64
	// wavelengths is the resolved set for the aggregate polychromatic kinds
	// and secondary_spectrum; nil for ordinary per-wavelength terms.
	wavelengths []types.WavelengthSample
	weight      float64
	target      float64
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
	// frequency is the spatial frequency in lp/mm for geometric MTF kinds.
	frequency float64
	// normScale is the normalization denominator applied to the residual
	// (value - target) / normScale before weighting. 1 = no normalization.
	normScale float64
}

// normDivisor returns the normalization denominator, treating a zero value as
// 1 so a meritTerm built without an explicit normalization (e.g. in tests or by
// a caller that bypasses buildMeritTermFromTypes) is left unnormalized.
func (t meritTerm) normDivisor() float64 {
	if t.normScale > 0 {
		return t.normScale
	}
	return 1.0
}

// SetEscapePhase sets the numeric phase metric used by the "phase" schedule
// metric.  The escape.Wrapper forwards the current escape-cycle phase
// (PhaseEscape=0, PhaseGlassSolve=0.5, PhaseClean=1) through this setter so
// the merit schedule can switch mode weights at phase boundaries. It also
// activates phase-dependent hull weighting (hullPhaseFactor): a plain
// optimize run never calls this, so its hull penalty always runs at full
// strength.
func (o *Optimizer) SetEscapePhase(p float64) {
	o.phase = p
	o.phaseActive = true
}

// wavefrontShearScale returns the length, in mm, that converts a spatial frequency
// in lp/mm into the entrance-pupil displacement at which the pupil
// autocorrelation — and therefore the OTF — is read (dls.ComputeWavefrontShift).
//
// The displacement is s = ν·λ·R_ent/NA, and for an infinite conjugate the working
// F-number is F# = EFL/EPD, so NA = R_ent/EFL and the scale collapses to the EFL.
// It is therefore the EFL that is read, not a ratio of two pupil quantities: the
// paraxial entrance-pupil radius is an estimate that ignores a virtual entrance
// pupil (a stop-free system reported an 118 mm EPD where the design prescribes
// 12.5 mm, i.e. an NA nine times too large and a shear nine times too short), while
// the EFL depends only on the powered surfaces. A finite conjugate picks up the
// magnification, since its working F-number is EFL/(EPD·M).
//
// Zero when the focal length is unavailable, which makes the wavefront-shift kinds
// report no value rather than a wrong one.
func (o *Optimizer) wavefrontShearScale(cfg *config, surfaces []types.Surface, gc *glass.Catalog, wavelength float64) float64 {
	wl := wavelength
	if wl <= 0 {
		wl = effectiveReferenceWavelength(cfg.referenceWavelength)
	}
	if wl <= 0 {
		return 0
	}
	pr := paraxial.Compute(types.System{Surfaces: surfaces, StopSurface: cfg.stopSurface}, wl, gc, 0, nil)
	efl := math.Abs(pr.FocalLength)
	if efl <= 0 {
		return 0
	}
	if m := math.Abs(pr.Magnification); m > 1e-9 {
		return 2 * efl / m
	}
	return efl
}

type Optimizer struct {
	configs          []config
	variables        []Variable
	gc               *glass.Catalog
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
	// camLink, when set, re-solves the zoom cam airspaces on every evaluation
	// (see SetCamCoupling): the linkage is geometry, not a variable.
	camLink *camCoupling
	// Glass attraction: soft-min potential pulling nd/vd toward catalog glasses.
	catalogField         *glass.CatalogField
	attractionWeight     float64 // current run-level weight w(t)
	attractionWeightFrom float64 // weight at t=0
	attractionWeightTo   float64 // weight at t=1
	attractionSigmaND    float64 // gaussian kernel width (normalised units)
	attractionSigmaVD    float64
	attractionKernel     string // "distance" | "gaussian"
	attractionMetric     string // "iteration" | "run_iteration" | "merit_ratio"
	attractionCurve      string // "linear" | "sigmoid" | "step"
	attractionAnchorFrom float64
	attractionAnchorTo   float64
	attractionRunIter    int       // accumulating counter for run_iteration
	attractionScales     []float64 // per-pair weight scales (sensitivity)
	// Sensitivity EMA state (per glass pair, index matches hullPairs).
	sensitivityEMA      []float64
	sensitivityWeighted bool
	sensitivityPower    float64
	sensitivityScaleMin float64
	sensitivityScaleMax float64
	sensitivityRef      string // "mean" | "max"
	initialOpticalMerit float64
	// skipAttraction/hull are temporary flags for sensitivity computation.
	skipAttraction bool
	skipHull       bool
	// lightApertureSizing reduces the hex-grid ray count for auto-aperture
	// beam-extent measurement from max(numRays, 256) to numRays. Used by the
	// PSO explorer where speed matters more than aperture precision.
	lightApertureSizing bool
	// jacobianActive signals that sizeAutoApertures should reuse the
	// diameters cached by the most recent BeginJacobian call instead of
	// re-tracing beam extents. The Jacobian sweep bracket (BeginJacobian /
	// EndJacobian) sets this flag; concurrent column goroutines read it
	// without writing.
	jacobianActive bool
	// reuseDiameters holds the per-config, per-surface auto-aperture
	// diameters computed at the Jacobian base point. Keyed by config ID
	// and surface ID.
	reuseDiameters map[string]map[int]float64
	// roleTargets holds the per-config, per-surface glass-role classification
	// (paraxial.ElementRole) frozen at the top of each DLS iteration by
	// updateGlassRoles, so the base-point and Jacobian residuals share one role
	// assignment — the same frozen-per-iteration convention as the pupil.
	roleTargets map[string]map[int]paraxial.ElementRole
	// backFocusStats aggregates what the in-run back-focus hard solve did for
	// opt_results.back_focus: the last method and shift, the fields a partial
	// wavefront solve had to drop, and the total failure count (every one of
	// which fell back to the paraxial solve). applyBackFocusSolve runs from
	// applyVariables, which the DLS Jacobian sweep calls concurrently for
	// every column, so all access takes the mutex.
	backFocusStats backFocusDiagnostics
	// backFocusWarnings throttles the back-focus warnings: the solve runs for
	// every Jacobian column, so an unguarded warning would flood stderr. The
	// first three events are always reported, then one in 50.
	backFocusWarnings atomic.Int64
	// diffractionCfg is optimization.diffraction_mtf, the sampling of the
	// diffraction_mtf_sag/tan merit kinds (nil = built-in defaults). Set by
	// SetDiffractionMTF before the run, read-only afterwards.
	diffractionCfg *types.DiffractionMTFConfig
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
	// phase holds the current escape-cycle phase metric (0=escape, 0.5=glass,
	// 1=clean), forwarded by the escape.Wrapper for the "phase" schedule metric.
	// phaseActive marks that SetEscapePhase has been called (i.e. an escape
	// cycle drives this optimizer); a plain optimize run never sets it and
	// therefore keeps full-strength hull penalty in every evaluation.
	phase            float64
	phaseActive      bool
	initialMerit     float64
	initialSpotRatio float64 // initial spot_diffraction ratio for normalisation
	modeChanges      int
	lastMetric       float64
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
	// backFocusSolve holds the back-focus solve configuration. When set,
	// applyBackFocusSolve adjusts the target surface thickness after every
	// variable application so the image plane stays at the desired focus.
	backFocusSolve *types.BackFocusSolveConfig
	// backFocusTargets maps config ID to the per-config solve entries.
	backFocusTargets map[string][]backFocusSolveEntry
	// backFocusSchedule holds the dynamic switching configuration (nil when
	// not in use). When set, updateBackFocusType reads the merit schedule's
	// mode weights and switches currentBackFocusType accordingly.
	backFocusSchedule *types.BackFocusScheduleConfig
	// currentBackFocusType is the active back-focus solve type ("paraxial"
	// or "wavefront"), updated by updateBackFocusType at the top of each
	// DLS iteration. Defaults to backFocusSolve.Type.
	currentBackFocusType string
	// linkedVariables holds dependent variables whose values are computed
	// from other variables during applyVariables. They do not occupy the
	// optimisation vector.
	linkedVariables []LinkedVariable
	// varNameIndex maps variable names (shared, local, and linked) to
	// their position in the combined lookup: independent variables use
	// their index in the optimisation vector; linked variables use
	// len(variables) + their index in linkedVariables.
	varNameIndex map[string]int
}

// regionActiveState is the per-config mutable state for the Region Active
// Method. It holds the flattened list of constraint states across all configs.
type regionActiveState struct {
	configID string
	states   []constraint.RegionActiveState
}

// SetStop wires the mid-solve stop channel into the DLS options returned by
// Options. Closing it asks the running solve to abort at the next checkpoint
// (top of an iteration, after the pupil update / Jacobian, inside the line
// search) and return its best-so-far state instead of a converged result.
func (o *Optimizer) SetStop(stop <-chan struct{}) {
	o.stop = stop
}

// SetDegenerate overrides the bounded penalties for merit terms that cannot
// be evaluated (pupil grid with no valid rays, or a failed wavefront fit).
// Non-positive values keep the built-in defaults (spot 0.1, opd 0.01,
// wavefront 0.01 mm). The fallbacks must exceed a realistic residual so a
// failed evaluation is never cheaper than a real measurement, while staying
// bounded so the DLS line search is not stalled. See the DegenerateConfig
// YAML section.
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

// SetDiffractionMTF configures the sampling of the diffraction_mtf_sag /
// diffraction_mtf_tan merit kinds (optimization.diffraction_mtf). A nil
// config keeps the built-in defaults: 128 entrance-pupil rays, the psf image
// grid rules capped at psf.DefaultDiffractionMaxGrid, RCP+LCP polarization.
func (o *Optimizer) SetDiffractionMTF(cfg *types.DiffractionMTFConfig) {
	o.diffractionCfg = cfg
}

// NewOptimizer builds a single-configuration Optimizer (backward-compatible
// entry point).
func NewOptimizer(cfg Config) *Optimizer {
	c := config{
		id:                  "config1",
		weight:              1.0,
		stopSurface:         cfg.StopSurface,
		refSurface:          cfg.RefSurface,
		pupilZ:              resolvePupilZ(cfg.Surfaces, cfg.StopSurface, cfg.PupilZ),
		pupilResolved:       seedPupilResolved(cfg.StopSurface, cfg.PupilZ, cfg.PupilModel),
		fieldDefs:           fieldDefsFromItems(cfg.Fields),
		surfaces:            cfg.Surfaces,
		fields:              cfg.Fields,
		wavelengths:         cfg.Wavelengths,
		spectrum:            cfg.Spectrum,
		referenceWavelength: cfg.ReferenceWavelength,
		constraints:         cfg.Constraints,
		pupilModel:          cfg.PupilModel,
		rayDefinition:       cfg.RayDefinition,

		wavefrontPlaneRef: planeReference(cfg.WavefrontReference),
	}
	// Airy radius for spot-term normalization, evaluated at each term's own
	// wavelength (0 = fall back to lambda). A single default-wavelength value
	// biased the 486/656 nm spot terms by ~11%.
	airyFor := func(wavelength float64) float64 {
		return airyRadiusMM(cfg.Surfaces, cfg.StopSurface, cfg.PupilModel, cfg.GlassCatalog, wavelength)
	}
	for _, t := range cfg.MeritTerms {
		dx, dy := 0.0, 1.0
		if len(t.FieldDir) >= 2 {
			dx, dy = normalizeDir(t.FieldDir)
		}
		mt := meritTerm{
			kind:                 t.Kind,
			fieldAngle:           t.FieldAngle,
			fieldDirX:            dx,
			fieldDirY:            dy,
			fieldIndex:           t.FieldIndex,
			fieldWeight:          t.FieldWeight,
			wavelength:           t.Wavelength,
			comparisonWavelength: t.ComparisonWavelength,
			wavWeight:            t.WavWeight,
			wavelengths:          t.Wavelengths,
			weight:               t.Weight,
			target:               t.Target,
			fraction:             t.Fraction,
			frequency:            t.Frequency,
			surfaceSet:           append([]int(nil), t.SurfaceSet...),
			normScale:            normScaleFor(t.Kind, t.Scale, t.Wavelength, airyFor(t.Wavelength), cfg.Normalization),
		}
		// A field declared by image_height is resolved to its angle at the start
		// geometry (see freezeFieldAngles), never read as the declared angle —
		// which is 0 for such a field, i.e. the on-axis field the document did
		// not ask for.
		if i := t.FieldIndex; i >= 0 && i < len(c.fields) && c.fields[i].ImageHeight != 0 {
			mt.useImageHeight = true
			mt.imageHeight = c.fields[i].ImageHeight
			mt.fieldAngle = 0
		}
		c.meritTerms = append(c.meritTerms, mt)
	}
	if len(cfg.MeritModes) > 0 {
		resolver := spectral.NewResolver(cfg.Wavelengths, cfg.Spectrum, cfg.ReferenceWavelength)
		c.meritModes = make(map[string][]meritTerm, len(cfg.MeritModes))
		for _, m := range cfg.MeritModes {
			modeTerms := m.Terms
			if expanded, err := spectral.ExpandTerms(modeTerms, resolver, cfg.WavelengthNormalization); err == nil {
				if len(expanded) > 20 && spectral.HasSpectralSet(modeTerms) {
					Warnf("warning: merit mode %q: wavelength_set: spectral expanded %d merit terms; prefer an aggregate polychromatic kind or a coarser spectrum.optimize_step", m.Name, len(expanded))
				}
				modeTerms = expanded
			} else {
				Warnf("warning: merit mode %q: %v; the unexpanded terms are used", m.Name, err)
			}
			var terms []meritTerm
			for _, t := range modeTerms {
				// `field` is the field index (documented in
				// merit-and-constraints.md), not the field's id. The imported
				// documents used to give every field id 0, which made every
				// mode term with field > 0 fall back to field 0; keep an id
				// lookup as the fallback for a non-sequential id set.
				fieldIndex := -1
				if t.Field >= 0 && t.Field < len(c.fields) {
					fieldIndex = t.Field
				} else {
					for i := range c.fields {
						if c.fields[i].ID == t.Field {
							fieldIndex = i
							break
						}
					}
				}
				var fieldAngle, fieldWeight float64
				var fieldDirX, fieldDirY float64 = 0.0, 1.0
				if fieldIndex >= 0 {
					f := &c.fields[fieldIndex]
					fieldAngle = f.AngleDeg
					fieldWeight = f.Weight
					if len(f.Direction) >= 2 {
						fieldDirX, fieldDirY = normalizeDir(f.Direction)
					}
				}
				if fieldWeight == 0 {
					fieldWeight = 1.0
				}
				wl := t.Wavelength
				if wl <= 0 {
					wl = effectiveReferenceWavelength(cfg.ReferenceWavelength)
				}
				wavWeight := t.WavelengthWeight
				if wavWeight <= 0 {
					wavWeight = 1
				}
				mt := meritTerm{
					kind:                 t.Kind,
					fieldAngle:           fieldAngle,
					fieldDirX:            fieldDirX,
					fieldDirY:            fieldDirY,
					fieldIndex:           fieldIndex,
					fieldWeight:          fieldWeight,
					wavelength:           wl,
					comparisonWavelength: t.ComparisonWavelength,
					wavWeight:            wavWeight,
					wavelengths:          append([]types.WavelengthSample(nil), t.ResolvedWavelengths...),
					weight:               t.Weight,
					target:               t.Target,
					fraction:             t.Fraction,
					frequency:            t.Frequency,
					surfaceSet:           append([]int(nil), t.SurfaceSet...),
					normScale:            normScaleFor(t.Kind, t.Scale, t.Wavelength, airyFor(t.Wavelength), cfg.Normalization),
				}
				if fieldIndex >= 0 && c.fields[fieldIndex].ImageHeight != 0 {
					mt.useImageHeight = true
					mt.imageHeight = c.fields[fieldIndex].ImageHeight
					mt.fieldAngle = 0
				}
				terms = append(terms, mt)
			}
			c.meritModes[m.Name] = terms
		}
	}
	variables := make([]Variable, len(cfg.Variables))
	for i, v := range cfg.Variables {
		vv := Variable{
			Name:      v.Name,
			SurfaceID: v.SurfaceID,
			GlassName: v.GlassName,
			Param:     v.Param,
			Min:       v.Min,
			Max:       v.Max,
			Config:    "config1",
		}
		// Compute SurfaceSet for nd/vd variables: all glass surfaces in the
		// same lens element, so hull/attraction treats the element as a unit.
		if v.Param == "nd" || v.Param == "vd" {
			vv.SurfaceSet = elementGlassSurfaces(cfg.Surfaces, v.SurfaceID)
		}
		variables[i] = vv
	}
	opt := newOptimizer(
		[]config{c}, variables, nil, nil, cfg.GlassCatalog,
		cfg.MaxIter, cfg.Mu, cfg.Tol, cfg.Epsilon, cfg.ApertureMargin, cfg.NumRays,
		cfg.MuConMax, cfg.Workers, cfg.Logger, cfg.Hull, cfg.HullMargin, cfg.HullWeight,
		cfg.CentralDiff, cfg.BFGS, cfg.AdaptiveDamping, cfg.RegionActive,
	)
	opt.SetApertureMarginMM(cfg.ApertureMarginMM)
	opt.SetDegenerate(cfg.SpotDegenerate, cfg.OPDDegenerate, cfg.WavefrontDegenerate)
	opt.SetPowerSolve(cfg.PowerSolveSurfaces)
	if cfg.GlassAttraction != nil {
		opt.SetGlassAttraction(cfg.GlassAttraction, cfg.GlassCatalog)
	}
	return opt
}

// NewMultiOptimizer builds a unified Optimizer over one or more configs,
// with shared variables (one x driving many bindings), local variables
// (one x driving a single surface of one config), and variable links
// (dependent variables computed from other variables).
func NewMultiOptimizer(configs []ConfigInput, sharedVars []types.SharedVariable, localVars []types.LocalVariableDef, variableLinks []types.VariableLink, gc *glass.Catalog, maxIter int, mu, tol, epsilon, apertureMargin float64, numRays int, muConMax float64, workers int, logger dls.Logger, hull *glass.ConvexHull, hullMargin, hullWeight float64, centralDiff, bfgs bool, adaptiveDamping *types.AdaptiveDampingConfig, raCfg *types.RegionActiveConfig) *Optimizer {
	internal := make([]config, len(configs))
	for i, ci := range configs {
		resolver := spectral.NewResolver(ci.Wavelengths, ci.Spectrum, ci.ReferenceWavelength)
		if expanded, err := spectral.ExpandTerms(ci.MeritTerms, resolver, ci.WavelengthNormalization); err == nil {
			if len(expanded) > 20 && spectral.HasSpectralSet(ci.MeritTerms) {
				Warnf("warning: config %q: wavelength_set: spectral expanded %d merit terms; prefer an aggregate polychromatic kind or a coarser spectrum.optimize_step", ci.ID, len(expanded))
			}
			ci.MeritTerms = expanded
		} else {
			Warnf("warning: config %q merit: %v; the unexpanded terms are used", ci.ID, err)
		}
		if len(ci.MeritModes) > 0 {
			modes := make([]types.MeritMode, len(ci.MeritModes))
			for mi, m := range ci.MeritModes {
				modes[mi] = m
				if expanded, err := spectral.ExpandTerms(m.Terms, resolver, ci.WavelengthNormalization); err == nil {
					if len(expanded) > 20 && spectral.HasSpectralSet(m.Terms) {
						Warnf("warning: config %q mode %q: wavelength_set: spectral expanded %d merit terms; prefer an aggregate polychromatic kind or a coarser spectrum.optimize_step", ci.ID, m.Name, len(expanded))
					}
					modes[mi].Terms = expanded
				} else {
					Warnf("warning: config %q mode %q: %v; the unexpanded terms are used", ci.ID, m.Name, err)
				}
			}
			ci.MeritModes = modes
		}
		c := config{
			id:            ci.ID,
			weight:        ci.Weight,
			stopSurface:   ci.StopSurface,
			refSurface:    ci.RefSurface,
			pupilZ:        resolvePupilZ(ci.Surfaces, ci.StopSurface, ci.PupilZ),
			pupilResolved: seedPupilResolved(ci.StopSurface, ci.PupilZ, ci.PupilModel),
			fieldDefs:     fieldDefsFromItems(ci.Fields),
			surfaces:      ci.Surfaces,
			fields:        ci.Fields,
			wavelengths:   ci.Wavelengths,
			spectrum:      ci.Spectrum,
			constraints:   ci.Constraints,
			pupilModel:    ci.PupilModel,
			rayDefinition: ci.RayDefinition,

			wavefrontPlaneRef: planeReference(ci.WavefrontReference),
		}
		// The Airy radius for spot-term normalization is resolved per term at
		// the term's own wavelength inside buildMeritTermFromTypes.
		if len(ci.MeritModes) > 0 {
			c.meritModes = make(map[string][]meritTerm, len(ci.MeritModes))
			c.meritModeNumRays = make(map[string]int, len(ci.MeritModes))
			for _, m := range ci.MeritModes {
				var terms []meritTerm
				for _, t := range m.Terms {
					terms = append(terms, buildMeritTermFromTypes(t, ci, gc))
				}
				c.meritModes[m.Name] = terms
				if m.NumRays > 0 {
					c.meritModeNumRays[m.Name] = m.NumRays
				}
			}
		}
		for _, t := range ci.MeritTerms {
			c.meritTerms = append(c.meritTerms, buildMeritTermFromTypes(t, ci, gc))
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
		// A shared variable that binds a glass parameter (nd/vd) carries that
		// param so the glass paths (startup conversion, apply, materialize)
		// recognise it. All bindings must use the same param and the same
		// initial glass; the command layer validates both before building.
		sharedParam := ""
		if len(sv.Bindings) > 0 {
			sharedParam = sv.Bindings[0].Param
		}
		variables = append(variables, Variable{
			Name:     name,
			IsShared: true,
			Bindings: sv.Bindings,
			Param:    sharedParam,
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
		// Pupil-model variables target the config's virtual entrance pupil
		// (applyVariables keys on the prefixed param name), not a surface.
		param := lv.Target.Param
		if lv.Target.Type == "pupil_model" {
			param = "pupil_model_" + param
		}
		vv := Variable{
			Name:      name,
			SurfaceID: lv.Target.ID,
			Param:     param,
			Min:       lv.Min,
			Max:       lv.Max,
			Config:    lv.Config,
		}
		// Compute SurfaceSet for nd/vd variables: all glass surfaces in the
		// same lens element.
		if lv.Target.Param == "nd" || lv.Target.Param == "vd" {
			cfgSurfaces := findConfigByID(internal, lv.Config)
			if cfgSurfaces != nil {
				vv.SurfaceSet = elementGlassSurfaces(cfgSurfaces.surfaces, lv.Target.ID)
			}
		}
		variables = append(variables, vv)
	}

	// Build name→index lookup for independent variables.
	varNameIndex := make(map[string]int, len(variables))
	for i, v := range variables {
		if v.Name != "" {
			varNameIndex[v.Name] = i
		}
	}

	// Process variable links: resolve source references and build
	// linkedVariables. Links are processed in order so that a link can
	// reference another link that appears earlier in the list.
	var linkedVars []LinkedVariable
	for li, lk := range variableLinks {
		if !lk.Active {
			continue
		}
		sourceIdx, ok := varNameIndex[lk.Source]
		if !ok {
			// Source not found — skip this link (will be caught at
			// applyVariables time with a zero value).
			sourceIdx = -1
		}
		param := lk.Target.Param
		if lk.Target.Type == "pupil_model" {
			param = "pupil_model_" + param
		}
		scale := lk.Relation.Scale
		offset := lk.Relation.Offset
		linkedVars = append(linkedVars, LinkedVariable{
			Name:        lk.Name,
			SurfaceID:   lk.Target.ID,
			Param:       param,
			Config:      lk.Target.Config,
			SourceIndex: sourceIdx,
			Scale:       scale,
			Offset:      offset,
		})
		// Register in varNameIndex so subsequent links can reference this one.
		if lk.Name != "" {
			varNameIndex[lk.Name] = len(variables) + li
		}
	}

	return newOptimizer(
		internal, variables, linkedVars, varNameIndex, gc,
		maxIter, mu, tol, epsilon, apertureMargin, numRays, muConMax,
		workers, logger, hull, hullMargin, hullWeight,
		centralDiff, bfgs, adaptiveDamping, raCfg,
	)
}

// resolveNormScale resolves a merit term's normalization denominator from the
// multi-config input (see normScaleFor). The Airy radius is evaluated at the
// term's own wavelength so the 486/656 nm spot terms normalize correctly.
func resolveNormScale(t types.MeritTerm, ci ConfigInput, gc *glass.Catalog) float64 {
	wl := t.Wavelength
	if wl <= 0 {
		wl = ci.ReferenceWavelength
	}
	airy := airyRadiusMM(ci.Surfaces, ci.StopSurface, ci.PupilModel, gc, wl)
	return normScaleFor(t.Kind, t.Scale, wl, airy, ci.Normalization)
}

// normScaleFor resolves a merit term's normalization denominator:
//  1. an explicit term scale (Scale field), else
//  2. the per-kind override / automatic scale when normalization is enabled,
//     else
//  3. 1 (no normalization).
func normScaleFor(kind string, explicitScale, wavelength, airyMM float64, norm *types.MeritNormalizationConfig) float64 {
	if explicitScale > 0 {
		return explicitScale
	}
	if norm == nil || !norm.Enabled {
		return 1.0
	}
	if wavelength <= 0 {
		wavelength = types.DefaultWavelength
	}
	return normalizationScale(kind, wavelength, airyMM, norm.Scales)
}

// normalizationScale returns the automatic normalization denominator for a
// merit kind:
//   - spot-size kinds use the diffraction-limited Airy radius 0.61·λ/NA (so a
//     diffraction-limited spot normalizes to 1), falling back to the wavelength
//     when the NA is unavailable;
//   - equivalent-focus-shift kinds (spot_flatness / spot_astigmatism, values in
//     mm of image-plane shift) use the shift that puts one wave of defocus at
//     the pupil edge, 2λ/NA²;
//   - other length-valued aberration kinds (wavefront RMS/PV, OPD RMS,
//     chromatic focal/image shift) use the term's wavelength in mm, so the
//     value is measured in waves;
//   - everything else keeps scale 1 (already dimensionless, a length without a
//     natural scale such as focal_length in mm, or a fraction).
//
// A per-kind override in scales takes precedence. The legacy empty kind means
// spot_rms.
func normalizationScale(kind string, wavelengthMM, airyMM float64, scales map[string]float64) float64 {
	if s, ok := scales[kind]; ok && s > 0 {
		return s
	}
	if kind == "" {
		kind = dls.MeritSpotRMS
	}
	if s, ok := scales[kind]; ok && s > 0 {
		return s
	}
	if isSpotScaledKind(kind) {
		if airyMM > 0 {
			return airyMM
		}
		if wavelengthMM > 0 {
			return wavelengthMM
		}
		return 1.0
	}
	if isFocusShiftScaledKind(kind) {
		// The value is an equivalent longitudinal image-plane shift, so its
		// natural scale is the shift that puts one wave of defocus at the pupil
		// edge: edge OPD = dz*NA^2/2 (waves) = dz*NA^2/(2*lambda) = 1 gives
		// dz = 2*lambda/NA^2, with NA recovered from the Airy radius
		// (NA = 0.61*lambda/airy). Falls back to the wavelength like the other
		// length-valued kinds when the NA is unavailable.
		if airyMM > 0 && wavelengthMM > 0 {
			na := 0.61 * wavelengthMM / airyMM
			if na > 0 {
				return 2 * wavelengthMM / (na * na)
			}
		}
		if wavelengthMM > 0 {
			return wavelengthMM
		}
		return 1.0
	}
	if isWaveScaledKind(kind) && wavelengthMM > 0 {
		return wavelengthMM
	}
	return 1.0
}

// isSpotScaledKind reports whether a merit kind's value is a spot radius in mm
// whose natural normalization is the diffraction-limited Airy radius.
func isSpotScaledKind(kind string) bool {
	switch kind {
	case dls.MeritSpotRMS, dls.MeritSpotRMST, dls.MeritSpotRMSS,
		dls.MeritSpotRMSWorst, dls.MeritSpotWeighted, dls.MeritSpotEERadius,
		MeritPolychromaticSpot, MeritPolychromaticEE:
		return true
	}
	return false
}

// isFocusShiftScaledKind reports whether a merit kind's value is an equivalent
// longitudinal image-plane shift in mm whose natural normalization is the
// plane shift that puts one wave of defocus at the pupil edge (2λ/NA²).
func isFocusShiftScaledKind(kind string) bool {
	switch kind {
	case dls.MeritSpotFlatness, dls.MeritSpotAstigmatism:
		return true
	}
	return false
}

// isWaveScaledKind reports whether a merit kind's value is an OPD/length in mm
// whose natural normalization is the wavelength (so value/scale is in waves).
func isWaveScaledKind(kind string) bool {
	switch kind {
	case MeritOPDRMS, MeritWavefrontRMSResidual, MeritWavefrontSphereRMS,
		MeritWavefrontSpherePV, MeritLongitudinalColor, MeritLateralColor,
		MeritAxialColor, MeritSecondarySpectrum:
		return true
	}
	return false
}

// airyRadiusMM returns the diffraction-limited Airy disk radius 0.61·λ/NA for a
// config, used to normalize spot-size merit terms. The paraxial NA is taken
// from paraxial.Compute; when a virtual entrance pupil fixes the EPD
// independently of the stop/beam, the NA is derived from that diameter
// (EPD/(2·|EFL|)) so a stop-free virtual-pupil system is not mis-normalized by
// the beam-aware fallback pupil. Returns 0 when the NA cannot be computed, so
// the caller falls back to the wavelength.
func airyRadiusMM(surfaces []types.Surface, stopSurface int, pupilModel *types.PupilModelConfig, gc *glass.Catalog, wavelength float64) float64 {
	if wavelength <= 0 {
		wavelength = types.DefaultWavelength
	}
	if len(surfaces) == 0 {
		return 0
	}
	if gc == nil {
		gc = glass.NewCatalog()
	}
	sys := types.System{Surfaces: surfaces, StopSurface: stopSurface}
	pr := paraxial.Compute(sys, wavelength, gc, 0, nil)
	na := pr.ImageSpaceNA
	if na <= 0 {
		na = pr.InfConjImageSpaceNA
	}
	if pupilModel != nil && pupilModel.Mode == "virtual_entrance_pupil" && pupilModel.Diameter > 0 {
		if efl := math.Abs(pr.FocalLength); efl > 1e-9 {
			na = pupilModel.Diameter / (2 * efl)
		}
	}
	if na <= 0 {
		return 0
	}
	return 0.61 * wavelength / na
}

func newOptimizer(configs []config, variables []Variable, linkedVars []LinkedVariable, varNameIndex map[string]int, gc *glass.Catalog, maxIter int, mu, tol, epsilon, apertureMargin float64, numRays int, muConMax float64, workers int, logger dls.Logger, hull *glass.ConvexHull, hullMargin, hullWeight float64, centralDiff, bfgs bool, adaptiveDamping *types.AdaptiveDampingConfig, raCfg *types.RegionActiveConfig) *Optimizer {
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

	// Every nd/vd variable — shared or local — targets only the surfaces it
	// actually binds (bindings only): those keyed target surfaces are converted
	// to a self-contained inline model glass seeded from their catalogue entry,
	// while other surfaces sharing the same catalogue key keep it. No catalogue
	// override is registered, so a sibling surface is never silently rewritten.
	//
	// First pass: resolve the initial catalogue key of every target before any
	// surface is converted, so the nd variable's conversion does not hide the key
	// from the vd variable on the same surface.
	type glassTarget struct {
		cfg string
		id  int
		key string
	}
	targetsByVar := make([][]glassTarget, len(variablesCopy))
	for i := range variablesCopy {
		v := &variablesCopy[i]
		if v.Param != "nd" && v.Param != "vd" {
			continue
		}
		var ts []glassTarget
		if v.IsShared {
			for _, b := range v.Bindings {
				if b.Param == "nd" || b.Param == "vd" {
					ts = append(ts, glassTarget{cfg: b.Config, id: b.ID})
				}
			}
		} else {
			ts = append(ts, glassTarget{cfg: v.Config, id: v.SurfaceID})
		}
		for k := range ts {
			cfg := findConfigByID(configs, ts[k].cfg)
			if cfg == nil {
				continue
			}
			si := surfaceIndex(cfg.surfaces, ts[k].id)
			if si < 0 {
				continue
			}
			if m := cfg.surfaces[si].Material; m.HasKey() {
				ts[k].key = m.Key
			}
		}
		targetsByVar[i] = ts
		if v.GlassName == "" {
			for _, t := range ts {
				if t.key != "" {
					v.GlassName = t.key
					break
				}
			}
		}
	}
	// Second pass: convert the keyed target surfaces to inline model glasses.
	for i := range variablesCopy {
		for _, t := range targetsByVar[i] {
			if t.key == "" {
				continue
			}
			cfg := findConfigByID(configs, t.cfg)
			if cfg == nil {
				continue
			}
			si := surfaceIndex(cfg.surfaces, t.id)
			if si < 0 {
				continue
			}
			if cfg.surfaces[si].Material.HasKey() {
				if g, ok := gc.Lookup(t.key); ok {
					cfg.surfaces[si].Material = types.Material{ND: g.ND, VD: g.VD}
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
		wavefrontDegenerate: 0.01,
		linkedVariables:     linkedVars,
		varNameIndex:        varNameIndex,
	}

	// Build the flattened region-active constraint states (all configs).
	// nil RegionActiveConfig means disabled: all constraints are always active.
	if raCfg != nil {
		opt.buildRegionActiveStates(raCfg)
	}

	// Freeze the image-height field angles at the start geometry before any
	// evaluation runs, so every consumer — merit, constraints, aperture sizing,
	// warnings — measures one field per declared field for the whole run.
	opt.freezeFieldAngles()

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

// findConfig returns the config with the given ID, or nil if not found.
func (o *Optimizer) findConfig(id string) *config {
	return findConfigByID(o.configs, id)
}

// primaryConfig returns the first config, used by the public single-term
// evaluator which has no config context.
func (o *Optimizer) primaryConfig() *config {
	if len(o.configs) == 0 {
		return &config{}
	}
	return &o.configs[0]
}

func (o *Optimizer) Options() dls.Options {
	return dls.Options{
		MaxIter:         o.maxIter,
		Mu:              o.mu,
		Tol:             o.tol,
		Epsilon:         o.epsilon,
		NumRays:         o.numRays,
		ApertureMargin:  o.apertureMargin,
		MuConMax:        o.muConMax,
		Workers:         o.workers,
		Logger:          o.logger,
		Stop:            o.stop,
		CentralDiff:     o.centralDiff,
		BFGS:            o.bfgs,
		AdaptiveDamping: o.adaptiveDamping,
	}
}

func (o *Optimizer) InitialState() []float64 {
	return o.getInitialState()
}

// camCoupling is the zoom linkage the optimizer applies to every evaluation:
// the cam airspace set, the solver options, and the EFL target of each config.
type camCoupling struct {
	opts    cam.Options
	targets map[string]float64
}

// SetCamCoupling makes every merit evaluation re-solve the cam airspaces of the
// coupled configs, so a DLS step cannot trade an airspace against the zoom
// linkage (one detector, one EFL per position). Call it with the document's
// `cam:` section resolved; the cam surfaces must not also be variables, or the
// solve overrides them. A config whose solve fails keeps the layout the
// variables produced, so the merit still reports the resulting EFL error
// instead of hiding it.
func (o *Optimizer) SetCamCoupling(opts cam.Options, targets map[string]float64) {
	if len(opts.Surfaces) == 0 || len(targets) == 0 {
		return
	}
	o.camLink = &camCoupling{opts: opts, targets: targets}
}

// applyCamLink solves the cam airspaces of every coupled config in place. A
// single-config run whose config carries no id takes the sole target, so a
// document without config ids still couples.
func (o *Optimizer) applyCamLink(configSurfaces map[string][]types.Surface) {
	if o.camLink == nil {
		return
	}
	sole := 0.0
	if len(o.configs) == 1 && len(o.camLink.targets) == 1 {
		for _, v := range o.camLink.targets {
			sole = v
		}
	}
	for ci := range o.configs {
		cfg := &o.configs[ci]
		target, ok := o.camLink.targets[cfg.id]
		if !ok && sole > 0 {
			target, ok = sole, true
		}
		if !ok || target <= 0 {
			continue
		}
		cur := configSurfaces[cfg.id]
		if len(cur) == 0 {
			continue
		}
		res, err := cam.Solve([]cam.Config{{ID: cfg.id, Surfaces: cur, TargetEFL: target}}, o.gc, o.camLink.opts)
		if err != nil || len(res.Configs) == 0 || len(res.Configs[0].Surfaces) != len(cur) {
			continue
		}
		configSurfaces[cfg.id] = res.Configs[0].Surfaces
	}
}

// effectiveGC returns the glass catalog reflecting any in-flight nd/vd
// overrides (tempGC), falling back to the base catalog.
func effectiveGC(base, temp *glass.Catalog) *glass.Catalog {
	if temp != nil {
		return temp
	}
	return base
}

// record stores one solve's outcome. failed marks a wavefront solve that broke
// down entirely and fell back to the paraxial focus.
func (d *backFocusDiagnostics) record(method string, shift float64, used int, dropped []string, failed bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.v.Method = method
	d.v.ShiftMM = shift
	d.v.FieldsUsed = used
	d.v.FieldsDropped = append(d.v.FieldsDropped[:0], dropped...)
	if failed {
		d.v.Failures++
	}
}

// snapshot returns a copy safe to hand to the result document.
func (d *backFocusDiagnostics) snapshot() types.BackFocusDiagnostics {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := d.v
	out.FieldsDropped = append([]string(nil), d.v.FieldsDropped...)
	return out
}

// restore replaces the recorded values with a previous snapshot. Used to undo
// the bookkeeping of a speculative materialisation (one that runs
// applyVariables purely to read a lens — see reanchorPowerSolve and
// SettlePowerSolve — rather than to report a solve).
func (d *backFocusDiagnostics) restore(v types.BackFocusDiagnostics) {
	d.mu.Lock()
	defer d.mu.Unlock()
	v.FieldsDropped = append([]string(nil), v.FieldsDropped...)
	d.v = v
}

// configByID returns the parsed config with the given id.
func (o *Optimizer) configByID(id string) (*config, bool) {
	for ci := range o.configs {
		if o.configs[ci].id == id {
			return &o.configs[ci], true
		}
	}
	return nil, false
}

// beamClipTolerance is the clear-aperture deficit (mm) at which
// WarnBeamClipping reports a surface. The envelope is a sampled measurement —
// the beam is traced at discrete azimuths and no interpolation is done — so a
// tolerance keeps a ray that falls between two samples from being reported as
// clipping.
const beamClipTolerance = 0.01

// beamClipWarnMax caps the number of surfaces WarnBeamClipping prints; a badly
// undersized lens can clip at every surface and the first (worst) few are
// enough to act on.
const beamClipWarnMax = 8

func (o *Optimizer) Optimize() Result {
	dlsResult := dls.Solve(o)

	x := make([]float64, len(dlsResult.Variables))
	for i, vs := range dlsResult.Variables {
		x[i] = vs.After
	}

	return Result{
		BeforeMerit:   dlsResult.BeforeMerit,
		AfterMerit:    dlsResult.AfterMerit,
		Iterations:    dlsResult.Iterations,
		Status:        dlsResult.Status,
		Variables:     o.buildVariableStates(x),
		AcceptedSteps: dlsResult.AcceptedSteps,
		Diagnostic:    dlsResult.Diagnostic,
	}
}

// FinalConfigs returns the surfaces of every config after applying x, with
// nd/vd-optimised model glasses materialised (surface materials rewritten to
// the optimised inline model glass).
func (o *Optimizer) FinalConfigs(x []float64) (map[string][]types.Surface, []types.Glass) {
	configSurfaces, _, _ := o.applyVariables(x)
	newGlasses := o.materializeGlasses(configSurfaces, x)
	return configSurfaces, newGlasses
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
						switch b.Param {
						case "nd", "vd":
							if nd, vd, ok := glassNDVD(cfg.surfaces[idx].Material, o.gc); ok {
								if b.Param == "nd" {
									x[i] = nd
								} else {
									x[i] = vd
								}
							}
						default:
							if v0 := getSurfaceParam(cfg.surfaces[idx], b.Param); v0 != 0 {
								x[i] = v0
							}
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
					if nd, vd, ok := glassNDVD(cfg.surfaces[idx].Material, o.gc); ok {
						if v.Param == "nd" {
							x[i] = nd
						} else {
							x[i] = vd
						}
					}
				}
			}
			if x[i] == 0 {
				x[i] = (v.Min + v.Max) / 2
			}
		case "power":
			if cfg != nil {
				x[i] = paraxial.ElementPowerCurvature(cfg.surfaces, o.gc, v.SurfaceID)
			} else {
				x[i] = (v.Min + v.Max) / 2
			}
			if x[i] < v.Min {
				x[i] = v.Min
			} else if x[i] > v.Max {
				x[i] = v.Max
			}
		case "pupil_model_axial_position", "pupil_model_diameter":
			if cfg != nil && cfg.pupilModel != nil {
				if v.Param == "pupil_model_diameter" {
					x[i] = cfg.pupilModel.Diameter
				} else {
					x[i] = cfg.pupilModel.AxialPosition
				}
			} else {
				x[i] = (v.Min + v.Max) / 2
			}
			if x[i] < v.Min {
				x[i] = v.Min
			} else if x[i] > v.Max {
				x[i] = v.Max
			}
		default:
			x[i] = (v.Min + v.Max) / 2
		}
	}
	return x
}
