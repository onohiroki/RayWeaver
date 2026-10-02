package optimize

import (
	"math"

	"github.com/hiroki/rayweaver/internal/chief"
	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/psf"
	"github.com/hiroki/rayweaver/internal/pupil"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/raymath"
	"github.com/hiroki/rayweaver/internal/types"
	"github.com/hiroki/rayweaver/internal/wavefront"
)

const (
	MeritSpotRMS           = "spot_rms"
	MeritSpotRMST          = "spot_rms_t"
	MeritSpotRMSS          = "spot_rms_s"
	MeritSpotRMSWorst      = "spot_rms_worst"
	MeritSpotWeightedRMS   = "spot_rms_weighted"
	MeritSpotEERadius      = "spot_ee_radius"
	MeritDistortionPct     = "distortion_pct"
	MeritLateralColor      = "lateral_color"
	MeritLongitudinalColor = "longitudinal_color"
	MeritGlassRole         = "glass_role"
	MeritSeidelSpherical   = "seidel_spherical"
	MeritSeidelComa        = "seidel_coma"
	MeritSeidelAstigmatism = "seidel_astigmatism"
	MeritSeidelDistortion  = "seidel_distortion"
	MeritOPDRMS            = "opd_rms"

	// Wavefront paraboloid fit kinds.
	MeritWavefrontDefocus     = "wavefront_defocus"
	MeritWavefrontAstigmatism = "wavefront_astigmatism"
	MeritWavefrontTilt        = "wavefront_tilt"
	MeritWavefrontRMSResidual = "wavefront_rms_residual"
	MeritWavefrontSphereRMS   = "wavefront_sphere_rms"
	MeritWavefrontSpherePV    = "wavefront_sphere_pv"
	MeritWavefrontX2          = "wavefront_x2"
	MeritWavefrontY2          = "wavefront_y2"
	MeritWavefrontXY          = "wavefront_xy"
	MeritWavefrontX           = "wavefront_x"
	MeritWavefrontY           = "wavefront_y"
	MeritWavefrontConstant    = "wavefront_constant"

	// field_alive penalises a field whose pupil grid has too few valid rays.
	MeritFieldAlive = "field_alive"

	// pupil_fill penalises a field's surviving-pupil fraction without bound:
	// the value (1-ratio)/ratio grows without limit as the fraction of valid
	// grid rays shrinks, so partial clipping cannot be traded against a smaller
	// aberration residual (the bounded field_alive deficit and the
	// zero-rays-only degenerate penalty cannot express that trade).
	MeritPupilFill = "pupil_fill"

	// Virtual entrance pupil merit kinds.
	MeritPupilPosition = "pupil_position"
	MeritPupilDiameter = "pupil_diameter"
	MeritVignetting    = "vignetting"
	MeritClearAperture = "clear_aperture"
	MeritEdgeThickness = "edge_thickness"

	// System-level EFL merit kind (replaces the constraint-based focal_length).
	MeritFocalLength = "focal_length"

	// Geometric MTF merit kinds (Phase 1: direct complex sum).
	// These evaluate the sagittal/tangential MTF at a specified spatial frequency.
	// The residual is hinge-style: max(0, target - MTF), so only under-performance is penalized.
	MeritGeometricMTFSag = "geometric_mtf_sag"
	MeritGeometricMTFTan = "geometric_mtf_tan"

	// Diffraction MTF merit kinds: the gate's own measurement — the vector
	// Huygens integral on the delivered image plane followed by the FFT/MTF
	// the `psf` / `focus mtf` commands report — evaluated inside the merit at
	// the term's `frequency`. Hinge-style like the geometric kinds: only
	// under-performance against `target` is penalised.
	MeritDiffractionMTFSag = "diffraction_mtf_sag"
	MeritDiffractionMTFTan = "diffraction_mtf_tan"
)

// isDiffractionKind reports whether a merit kind is a diffraction-MTF term
// (one psf trace + Huygens integration + FFT per field/wavelength/frequency).
func isDiffractionKind(kind string) bool {
	return kind == MeritDiffractionMTFSag || kind == MeritDiffractionMTFTan
}

// evaluateKindTerm evaluates a non-spot merit term for the given config,
// returning 0 for unknown kinds. The per-evaluation grid cache is shared with
// the grid merit kinds so opd_rms and the spot kinds reuse one pupil trace per
// (field, wavelength); a nil cache disables caching. p carries the per-call
// virtual-entrance-pupil values (see applyVariables).
func (o *Optimizer) evaluateKindTerm(cfg *config, term *meritTerm, surfaces []types.Surface, gc *glass.Catalog, cache *evalGridCache, p appliedPupil) float64 {
	switch term.kind {
	case MeritOPDRMS:
		points := o.gridForTerm(cache, gc, surfaces, cfg, term, p)
		if len(points) == 0 {
			return o.opdDegenerate
		}
		val := ComputeOPDRMS(points)
		if val >= 1e6 {
			return o.opdDegenerate
		}
		return val
	case MeritFieldAlive:
		return o.evaluateFieldAliveTerm(cfg, term, surfaces, gc, cache, p)
	case MeritPupilFill:
		return o.evaluatePupilFillTerm(cfg, term, surfaces, gc, cache, p)
	case MeritGeometricMTFSag:
		points := o.gridForTerm(cache, gc, surfaces, cfg, term, p)
		if len(points) == 0 {
			return 0
		}
		sag, _ := dls.ComputeGeometricMTF(points, term.frequency)
		// Clamp metric to target so (val-target)^2 = max(0, target-MTF)^2:
		// a one-sided floor that only penalises under-performance.
		if sag >= term.target {
			return term.target
		}
		return sag
	case MeritGeometricMTFTan:
		points := o.gridForTerm(cache, gc, surfaces, cfg, term, p)
		if len(points) == 0 {
			return 0
		}
		_, tan := dls.ComputeGeometricMTF(points, term.frequency)
		if tan >= term.target {
			return term.target
		}
		return tan
	case MeritDiffractionMTFSag, MeritDiffractionMTFTan:
		sag, tan := o.diffractionMTFValues(cfg, term, surfaces, gc, cache, p)
		val := sag
		if term.kind == MeritDiffractionMTFTan {
			val = tan
		}
		// Same hinge as the geometric kinds: clamping at the target makes
		// (val-target)^2 = max(0, target-MTF)^2, so a design already at the
		// gate is never pushed to buy MTF it does not need. An unevaluable
		// term yields 0, i.e. the full target deficit.
		if val >= term.target {
			return term.target
		}
		return val
	case dls.MeritWavefrontShiftSag:
		points := o.gridForTerm(cache, gc, surfaces, cfg, term, p)
		if len(points) == 0 {
			return 0
		}
		scale := o.wavefrontShearScale(cfg, surfaces, gc, term.wavelength)
		return dls.ComputeWavefrontShift(points, term.frequency, term.wavelength, scale, "sag")
	case dls.MeritWavefrontShiftTan:
		points := o.gridForTerm(cache, gc, surfaces, cfg, term, p)
		if len(points) == 0 {
			return 0
		}
		scale := o.wavefrontShearScale(cfg, surfaces, gc, term.wavelength)
		return dls.ComputeWavefrontShift(points, term.frequency, term.wavelength, scale, "tan")
	case dls.MeritWavefrontPairPhase:
		points := o.gridForTerm(cache, gc, surfaces, cfg, term, p)
		if len(points) == 0 {
			return 0
		}
		apR := dls.ApertureRadiusForGrid(surfaces, cfg.stopSurface, term.wavelength, gc, o.apertureMargin, p.dia)
		return dls.ComputeWavefrontPairPhase(points, apR, term.wavelength)
	default:
		if isGridKind(term.kind) {
			return o.evaluateGridKind(cfg, term, surfaces, gc, cache, p)
		}
		if isWavefrontKind(term.kind) {
			return o.evaluateWavefrontTerm(cfg, term, surfaces, gc, cache, p)
		}
		// glass_role reads the per-iteration frozen role targets (computed in
		// UpdatePupils) when available so the DLS base-point and Jacobian
		// residuals share one role assignment; a nil frozen map falls back to
		// a fresh classification of the current surfaces.
		return evaluateKindValue(term.kind, term, surfaces, gc, o.roleTargets[cfg.id], cfg)
	}
}

// isWavefrontKind reports whether the merit kind is evaluated via a wavefront
// fit on the reference surface (paraboloid coefficients, or the reference-sphere
// residual RMS/PV that drives the psf Strehl).
func isWavefrontKind(kind string) bool {
	switch kind {
	case MeritWavefrontDefocus, MeritWavefrontAstigmatism, MeritWavefrontTilt,
		MeritWavefrontRMSResidual, MeritWavefrontSphereRMS, MeritWavefrontSpherePV,
		MeritWavefrontX2, MeritWavefrontY2, MeritWavefrontXY, MeritWavefrontX,
		MeritWavefrontY, MeritWavefrontConstant:
		return true
	}
	return false
}

// evaluateWavefrontTerm fits the wavefront on the reference surface for the
// term's (field, wavelength) and returns the requested quantity: one paraboloid
// coefficient (wavefront_defocus/astigmatism/tilt/rms_residual/x2/y2/xy/x/y/
// constant), or the reference-sphere residual RMS/PV
// (wavefront_sphere_rms/pv — piston+tilt+defocus removed, astigmatism
// retained, the exact quantity psf reports as rms_opd and the direct Strehl
// determinant). The entrance-pupil grid is centred on the per-call virtual
// pupil position when active (it follows the axial_position variable exactly,
// so the derivative is consistent) or the config's frozen per-iteration pupil
// (dls.pupilZ) so the DLS base point and its Jacobian perturbations share one
// pupil otherwise. A degenerate fit (no grid, too few valid rays) returns the
// bounded degenerate penalty so the solver is pushed away rather than misled.
func (o *Optimizer) evaluateWavefrontTerm(cfg *config, term *meritTerm, surfaces []types.Surface, gc *glass.Catalog, cache *evalGridCache, p appliedPupil) float64 {
	refSurface := wavefrontRefSurface(cfg, surfaces)
	angle := o.termFieldAngle(cfg, term, surfaces, gc)
	fd := meritFieldDef(cfg, term, angle)
	sys := types.System{Surfaces: surfaces, StopSurface: cfg.stopSurface}

	// Pupil Z for the term's own field: the per-call virtual-pupil position
	// when active, else the frozen per-field entrance pupil. Stop-free
	// (dynamic-pupil) systems keep a PER-FIELD entrance pupil, so the
	// config-level pupilZ (field 0's aperture) is wrong for off-axis terms: a
	// grid frozen at field 0's pupil plane misses the term field's beam and
	// the sphere fit inflates the OPD with mis-centred crescent sampling
	// (observed 40x on a 23deg corner). Mirror evaluateGridKind's per-field
	// lookup, falling back to the config-level pupilZ when the field has no
	// entry (static stop path) or the angle is not an exact map key.
	frozenZ := o.gridCentring(cfg, p, angle)

	// lookup returns the cached wavefront entry for the given pupil mode,
	// computing and caching it on miss. The frozen→dynamic fallback is
	// handled by the caller: a frozen failure is cached so the same frozen
	// key is not retried within the same eval.
	lookup := func(frozen bool, frozenPupilZ *float64) wavefront.Entry {
		opts := o.wavefrontOptions(cfg, p)
		if cache == nil {
			// No cache: compute directly.
			entry, _ := wavefront.AnalyzeField(sys, gc, fd, refSurface, o.numRays, term.wavelength, o.apertureMargin, frozenPupilZ, cfg.pupilModel, cfg.rayDefinition, opts)
			return entry
		}
		fz := 0.0
		if frozenPupilZ != nil {
			fz = *frozenPupilZ
		}
		key := wfKey{
			configID:   cfg.id,
			angle:      angle,
			wavelength: term.wavelength,
			refSurface: refSurface,
			frozen:     frozen,
			frozenZ:    fz,
		}
		if e, ok := cache.wavefront[key]; ok {
			return *e
		}
		entry, _ := wavefront.AnalyzeField(sys, gc, fd, refSurface, o.numRays, term.wavelength, o.apertureMargin, frozenPupilZ, cfg.pupilModel, cfg.rayDefinition, opts)
		cache.wavefront[key] = &entry
		return entry
	}

	// Try frozen pupil first, fall back to dynamic pupil on failure.
	entry := lookup(true, &frozenZ)
	if entry.Failed {
		entry = lookup(false, nil)
		if entry.Failed {
			// A wavefront fit that fails even with the dynamic pupil (e.g. a
			// strongly off-axis field whose beam is fully clipped) returns the
			// bounded degenerate penalty instead of the legacy 1e6 sentinel,
			// so the DLS line search is not stalled by a weight·1e12 merit
			// contribution.
			return o.wavefrontDegenerate
		}
	}

	// Derive the term value from the cached entry.
	switch term.kind {
	case MeritWavefrontSphereRMS:
		return entry.Statistics.RMS
	case MeritWavefrontSpherePV:
		return entry.Statistics.PV
	case MeritWavefrontDefocus:
		// With the image plane as the OPD reference the defocus coefficient is
		// the field's image-plane focus error (field curvature plus the system
		// defocus); report it as the equivalent longitudinal focus shift in mm
		// so it is weightable like the chromatic focus terms. With the default
		// best-focus reference the coefficient is the zonal focus spread inside
		// the field, in mm/mm².
		if entry.DefocusMM != 0 {
			return entry.DefocusMM
		}
		return entry.Paraboloid.Defocus
	default:
		return wavefrontCoeff(term.kind, entry.Paraboloid)
	}
}

// defaultDiffractionPolarizations is the diffraction-MTF default polarization:
// RCP+LCP, the same incoherent average `focus mtf --polarization RCP+LCP`
// reports. On an all-refractive system the two circular states give identical
// intensity maps, so optimization.diffraction_mtf.polarizations: [RCP] halves
// the cost without changing the value — but only after that equivalence has
// been checked for the system at hand.
var defaultDiffractionPolarizations = []string{string(types.PolRCPLCP)}

// diffractionSettings is the resolved sampling of the diffraction MTF merit
// kinds (optimization.diffraction_mtf; zero values mean built-in defaults).
type diffractionSettings struct {
	numRays       int
	gridSize      int
	maxGrid       int
	polarizations []string
}

// diffractionSampling resolves the configured sampling for the current run.
//
// num_rays is the target number of EFFECTIVE (surviving) wavefront samples, not
// the nominal grid size: computeDiffractionMTF compensates for the field's
// vignetting ellipse so every field is sampled alike (see
// vignettedNumRays). 400 rather than the gate's 1600: the merit needs the same
// kind of pupil sampling, not the same photon budget. Measured on a
// diffraction-limited triplet at 10 c/mm (gate = 1600 rays), the sagittal /
// tangential read was
//
//	64 → 0.693/0.685    200 → 0.772/0.776    400 → 0.902/0.904
//	128 → 0.703/0.703   256 → 0.838/0.840   1600 → 0.907/0.907
//
// so 128 rays - the previous default - reads ~0.20 BELOW the gate near the
// hinge: the hinge at target 0.50 would have been satisfied at a true MTF of
// ~0.30. 400 is within 0.005-0.02 of the 1600-ray value, which is the accuracy
// the gate tolerance needs; 300 still reads 0.04-0.06 low.
func (o *Optimizer) diffractionSampling() diffractionSettings {
	out := diffractionSettings{
		numRays:       400,
		gridSize:      64, // psf's own default
		maxGrid:       psf.DefaultDiffractionMaxGrid,
		polarizations: defaultDiffractionPolarizations,
	}
	s := o.diffractionCfg
	if s == nil {
		return out
	}
	if s.NumRays > 0 {
		out.numRays = s.NumRays
	}
	if s.GridSize > 0 {
		out.gridSize = s.GridSize
	}
	if s.MaxGrid != 0 {
		out.maxGrid = s.MaxGrid
	}
	if len(s.Polarizations) > 0 {
		out.polarizations = s.Polarizations
	}
	return out
}

// meritFieldDef builds the FieldDef a wavefront / diffraction trace uses for a
// merit term: the term's resolved angle, the field's declared vignetting and
// the field's image-plane direction. Without the vignetting a heavily
// vignetted off-axis corner samples the full pupil and the analysis collapses;
// a term without a field index (or an index outside the field list) keeps the
// legacy full-pupil, +Y behaviour.
func meritFieldDef(cfg *config, term *meritTerm, angle float64) types.FieldDef {
	fd := types.FieldDef{Angle: angle, Direction: []float64{0, 1}}
	if term.fieldIndex >= 0 && cfg != nil && term.fieldIndex < len(cfg.fields) {
		f := &cfg.fields[term.fieldIndex]
		fd.Vignetting = f.Vignetting
		if dx, dy, ok := fieldDir(f.Direction); ok {
			fd.Direction = []float64{dx, dy}
		}
	}
	return fd
}

// wavefrontRefSurface resolves the surface the wavefront / diffraction traces
// sample: the config's reference surface when it lies before the image plane,
// else the last optical surface (a chief reference surface set to the
// conventional image plane is not a valid sampling surface) — the same rule
// the standalone `wavefront` and `psf` commands apply.
func wavefrontRefSurface(cfg *config, surfaces []types.Surface) int {
	refSurface := 0
	if cfg != nil {
		refSurface = cfg.refSurface
	}
	if refSurface <= 0 || refSurface >= surfaces[len(surfaces)-1].ID {
		return psf.DefaultReferenceSurface(surfaces)
	}
	return refSurface
}

// diffractionMTFValues returns the sagittal/tangential diffraction MTF of the
// term's (field, wavelength) at the term's frequency. Both axes come from one
// evaluation, so a sag+tan term pair on the same field shares one pupil trace
// and one Huygens integration through the per-evaluation cache.
func (o *Optimizer) diffractionMTFValues(cfg *config, term *meritTerm, surfaces []types.Surface, gc *glass.Catalog, cache *evalGridCache, p appliedPupil) (float64, float64) {
	if cache == nil {
		return o.computeDiffractionMTF(cfg, term, surfaces, gc, p)
	}
	key := diffractionMTFKey{
		gridKey: gridKey{
			configID:   cfg.id,
			fieldIndex: term.fieldIndex,
			fieldAngle: o.termFieldAngle(cfg, term, surfaces, gc),
			wavelength: term.wavelength,
		},
		frequency: term.frequency,
	}
	if v, ok := cache.diffraction[key]; ok {
		return v.sag, v.tan
	}
	sag, tan := o.computeDiffractionMTF(cfg, term, surfaces, gc, p)
	cache.diffraction[key] = diffractionMTFValue{sag: sag, tan: tan}
	return sag, tan
}

// maxVignettedNumRaysFactor bounds the vignetting compensation of
// vignettedNumRays. A near-dead ellipse (area 1e-3) would otherwise ask for a
// million launch rays to reach the target sample count; past a few times the
// target the extra rays buy nothing anyway (the pupil_fill term is what drives
// a clipped field back to health), so the compensation saturates here.
const maxVignettedNumRaysFactor = 4

// vignettedNumRays returns the nominal entrance-pupil ray count to launch for a
// field whose vignetting ellipse keeps the fraction area of the nominal pupil,
// so that roughly `target` samples survive the clip.
//
// Both pupil-grid builders (psf.FrozenPupilGrid and psf.ComputeFieldGrid) clip
// the full nominal grid against the ellipse rather than re-laying the grid into
// it, so without this a vignetted field is sampled in proportion to its area —
// measured on a 6-element at num_rays 400: 400 valid at the full field against
// 360 at 80% area. The MTF error from an under-sampled pupil is large near the
// hinge (see diffractionSampling), so the sample count - not the grid size - is
// what the prescribed vignetting must not silently eat.
func vignettedNumRays(target int, vig *types.VignettingDef) int {
	if target <= 0 || vig == nil || vig.IsZero() {
		return target
	}
	area := (1 - vig.CompressionX) * (1 - vig.CompressionY)
	if area <= 0 {
		return target
	}
	if area >= 1 {
		return target
	}
	n := int(math.Ceil(float64(target) / area))
	if max := target * maxVignettedNumRaysFactor; n > max {
		return max
	}
	return n
}

// computeDiffractionMTF traces and evaluates one term's field. The frozen
// pupil (per-iteration, so the DLS base point and its Jacobian perturbations
// share one entrance pupil, exactly like the wavefront terms) is tried first,
// the dynamic pupil second. A failure returns 0 for both axes: under the
// hinge that is the full target deficit, so the solver is pushed toward a
// state where a PSF can be formed at all instead of being fed a fabricated
// number.
func (o *Optimizer) computeDiffractionMTF(cfg *config, term *meritTerm, surfaces []types.Surface, gc *glass.Catalog, p appliedPupil) (float64, float64) {
	sampling := o.diffractionSampling()
	angle := o.termFieldAngle(cfg, term, surfaces, gc)
	fd := meritFieldDef(cfg, term, angle)
	sys := types.System{Surfaces: surfaces, StopSurface: cfg.stopSurface}
	frozenZ := o.gridCentring(cfg, p, angle)
	opts := psf.DiffractionMTFOptions{
		Wavelength:       term.wavelength,
		Frequency:        term.frequency,
		ReferenceSurface: wavefrontRefSurface(cfg, surfaces),
		NumRays:          vignettedNumRays(sampling.numRays, fd.Vignetting),
		GridSize:         sampling.gridSize,
		MaxGrid:          sampling.maxGrid,
		Polarizations:    sampling.polarizations,
		ApertureMargin:   o.apertureMargin,
		EPDOverride:      p.dia,
		PupilModel:       cfg.pupilModel,
		RayDefinition:    cfg.rayDefinition,
		Workers:          o.gridWorkers(),
	}
	res, err := psf.ComputeDiffractionMTF(sys, gc, fd, &frozenZ, opts)
	if err != nil {
		res, err = psf.ComputeDiffractionMTF(sys, gc, fd, nil, opts)
	}
	if err != nil {
		return 0, 0
	}
	return res.Sagittal, res.Tangential
}

// wavefrontOptions builds the per-analysis wavefront options: the applied
// virtual-entrance-pupil diameter (so the frozen grid samples the prescribed
// pupil instead of the paraxial/fixed-aperture radius) and the configured OPD
// reference (the per-field best focus, or the delivered image plane when
// optimization.wavefront_reference selects it).
func (o *Optimizer) wavefrontOptions(cfg *config, p appliedPupil) wavefront.FieldOptions {
	return wavefront.FieldOptions{
		EPDOverride:    p.dia,
		PlaneReference: cfg != nil && cfg.wavefrontPlaneRef,
	}
}

// wavefrontCoeff returns the paraboloid coefficient a wavefront merit kind
// reads from a fitted paraboloid.
func wavefrontCoeff(kind string, pab wavefront.Paraboloid) float64 {
	switch kind {
	case MeritWavefrontDefocus:
		return pab.Defocus
	case MeritWavefrontAstigmatism:
		return pab.Astigmatism
	case MeritWavefrontTilt:
		return pab.Tilt
	case MeritWavefrontRMSResidual:
		return pab.RMSResidual
	case MeritWavefrontX2:
		return pab.X2
	case MeritWavefrontY2:
		return pab.Y2
	case MeritWavefrontXY:
		return pab.XY
	case MeritWavefrontX:
		return pab.X
	case MeritWavefrontY:
		return pab.Y
	case MeritWavefrontConstant:
		return pab.Constant
	}
	return 0
}

func evaluateKindValue(kind string, term *meritTerm, surfaces []types.Surface, gc *glass.Catalog, frozen map[int]paraxial.ElementRole, cfg *config) float64 {
	if gc == nil {
		gc = glass.NewCatalog()
	}
	switch kind {
	case MeritDistortionPct:
		return evaluateDistortionPct(term.fieldAngle, term.wavelength, surfaces, gc, resolvedPupilZ(cfg))
	case MeritLateralColor:
		return evaluateLateralColor(term.fieldAngle, term.wavelength, term.comparisonWavelength, surfaces, gc, resolvedPupilZ(cfg))
	case MeritLongitudinalColor:
		return evaluateLongitudinalColor(term.wavelength, term.comparisonWavelength, surfaces, gc)
	case MeritGlassRole:
		if len(term.surfaceSet) == 0 {
			return 0
		}
		return glassRoleForSurface(surfaces, gc, term.surfaceSet[0], frozen)
	case MeritSeidelSpherical:
		return evaluateSeidel(term.fieldAngle, term.wavelength, surfaces, gc).Spherical
	case MeritSeidelComa:
		return evaluateSeidel(term.fieldAngle, term.wavelength, surfaces, gc).Coma
	case MeritSeidelAstigmatism:
		return evaluateSeidel(term.fieldAngle, term.wavelength, surfaces, gc).Astigmatism
	case MeritSeidelDistortion:
		return evaluateSeidel(term.fieldAngle, term.wavelength, surfaces, gc).Distortion
	case MeritPupilPosition:
		return evaluatePupilPosition(term.fieldAngle, term.wavelength, surfaces, gc, term.surfaceSet, cfg.stopSurface, cfg.rayDefinition)
	case MeritPupilDiameter:
		return evaluatePupilDiameter(term.fieldAngle, term.wavelength, surfaces, gc, cfg.stopSurface, cfg.rayDefinition)
	case MeritVignetting:
		return evaluateVignetting(term.fieldAngle, term.wavelength, surfaces, gc, term.surfaceSet, cfg.stopSurface, cfg.rayDefinition)
	case MeritClearAperture:
		return evaluateClearAperture(term.fieldAngle, term.wavelength, surfaces, gc, term.surfaceSet, cfg.stopSurface, cfg.rayDefinition)
	case MeritEdgeThickness:
		return evaluateEdgeThickness(term.fieldAngle, term.wavelength, surfaces, gc, term.surfaceSet)
	case MeritFocalLength:
		return evaluateFocalLength(surfaces, gc)
	default:
		return 0
	}
}

// resolvedPupilZ exposes the config's entrance-pupil Z to callers that must
// launch a ray through the pupil centre (distortion, lateral_color). It
// returns nil when no pupil has been resolved — no chief section / reference
// surface, or the chief pass did not yield one — so the caller keeps the
// historical axis launch instead of inventing a pupil. Presence is carried by
// the pointer, never by the value: Z=0 mm is a valid virtual entrance pupil
// and must be honoured.
func resolvedPupilZ(cfg *config) *float64 {
	if cfg == nil || !cfg.pupilResolved {
		return nil
	}
	z := cfg.pupilZ
	return &z
}

// fieldAliveThreshold resolves the field_alive aliveness threshold: the term's
// target when set, else the default 0.3 (30% of the grid must survive). Both
// the traced and the untraceable-grid paths use it so a field whose grid
// cannot be traced is penalised exactly like a fully dead traced field (it
// previously used a laxer 0.1 default and was rewarded for failing).
func fieldAliveThreshold(target float64) float64 {
	if target > 0 {
		return target
	}
	return 0.3
}

// residualTarget returns the target the merit residual (value−target)² uses.
// For field_alive the term's `target` is the aliveness *threshold* (documented
// in AGENTS.md: "default threshold is 0.3; override with target on the merit
// term"), not a value to drive toward: the residual must be the plain deficit,
// so a nonzero target must not be subtracted or the term would reward a field
// that is closer to dead (a target of 0.9 would minimise at ratio 0). All other
// kinds use their target directly.
func (t *meritTerm) residualTarget() float64 {
	if t.kind == MeritFieldAlive {
		return 0
	}
	return t.target
}

// evaluateFieldAliveTerm traces the pupil grid for the term's field and returns
// the "aliveness deficit": max(0, threshold − nValid/totalRays). A fully dead
// field (nValid=0) returns threshold; a fully alive field returns 0. The
// threshold defaults to 0.3 (30% of the grid must survive) when target is 0.
func (o *Optimizer) evaluateFieldAliveTerm(cfg *config, term *meritTerm, surfaces []types.Surface, gc *glass.Catalog, cache *evalGridCache, p appliedPupil) float64 {
	points := o.gridForTerm(cache, gc, surfaces, cfg, term, p)
	threshold := fieldAliveThreshold(term.target)
	totalRays := len(points)
	if totalRays == 0 {
		// Grid could not be traced at all — treat as fully dead.
		return threshold
	}
	nValid := 0
	for _, p := range points {
		if p.OK {
			nValid++
		}
	}
	ratio := float64(nValid) / float64(totalRays)
	deficit := threshold - ratio
	if deficit <= 0 {
		return 0
	}
	return deficit
}

// pupilFillCap bounds the pupil_fill merit value for a grid that is empty or
// effectively dead (surviving fraction below 1e-3), so the squared residual
// stays finite for the DLS line search while still dwarfing any live-field
// aberration term.
const pupilFillCap = 999.0

// evaluatePupilFillTerm returns the unbounded pupil-fill deficit
// (1−ratio)/ratio for the term's field, where ratio is the fraction of the
// pupil-grid rays that survived (OK). It is 0 when every ray survives and grows
// without bound as the surviving fraction shrinks: 0.5 → 1, 0.1 → 9, 0.03 →
// ~32.3. Unlike field_alive — whose max(0, threshold−ratio) deficit is bounded
// by `threshold` — this cannot be traded against the (survivor-only) spot/OPD
// residuals, which shrink as the pupil is clipped. It reuses the same cached
// grid trace as the other grid kinds, so it costs no extra tracing.
func (o *Optimizer) evaluatePupilFillTerm(cfg *config, term *meritTerm, surfaces []types.Surface, gc *glass.Catalog, cache *evalGridCache, p appliedPupil) float64 {
	return pupilFillFromPoints(o.gridForTerm(cache, gc, surfaces, cfg, term, p))
}

// pupilFillFromPoints returns the pupil_fill value of a traced pupil grid: the
// fraction of OK points mapped through pupilFillValue. An empty grid (the trace
// could not be built at all) is treated as fully dead.
func pupilFillFromPoints(points []dls.IPoint) float64 {
	total := len(points)
	if total == 0 {
		return pupilFillCap
	}
	nValid := 0
	for _, q := range points {
		if q.OK {
			nValid++
		}
	}
	return pupilFillValue(float64(nValid) / float64(total))
}

// pupilFillValue maps a surviving-ray fraction to the unbounded pupil_fill
// merit value: 0 when every ray survives, (1−ratio)/ratio otherwise, capped at
// pupilFillCap for an effectively dead pupil (ratio < 1e-3).
func pupilFillValue(ratio float64) float64 {
	if ratio >= 1 {
		return 0
	}
	if ratio < 1e-3 {
		return pupilFillCap
	}
	return (1 - ratio) / ratio
}

// evaluateDistortionPct returns the distortion percentage for the term's field.
func evaluateDistortionPct(fieldAngle, wavelength float64, surfaces []types.Surface, gc *glass.Catalog, pupilZ *float64) float64 {
	if wavelength == 0 {
		wavelength = types.DefaultWavelength
	}

	yChief := traceChiefImageHeight(surfaces, fieldAngle, wavelength, gc, pupilZ)
	if yChief == 0 {
		return 0
	}

	sys := types.System{Surfaces: surfaces}
	pr := paraxial.Compute(sys, wavelength, gc, 0, nil)
	yParax := pr.FocalLength * math.Tan(raymath.DegToRad(fieldAngle))

	if math.Abs(yParax) < 1e-15 {
		return 0
	}
	return 100.0 * (yChief - yParax) / yParax
}

func evaluateLateralColor(fieldAngle, wl1, wl2 float64, surfaces []types.Surface, gc *glass.Catalog, pupilZ *float64) float64 {
	if wl1 == 0 {
		wl1 = types.DefaultWavelength
	}
	if wl2 == 0 {
		return 0
	}

	y1 := traceChiefImageHeight(surfaces, fieldAngle, wl1, gc, pupilZ)
	y2 := traceChiefImageHeight(surfaces, fieldAngle, wl2, gc, pupilZ)
	return y2 - y1
}

func evaluateLongitudinalColor(wl1, wl2 float64, surfaces []types.Surface, gc *glass.Catalog) float64 {
	if wl1 == 0 {
		wl1 = types.DefaultWavelength
	}
	if wl2 == 0 {
		return 0
	}

	sys := types.System{Surfaces: surfaces}
	pr1 := paraxial.Compute(sys, wl1, gc, 0, nil)
	pr2 := paraxial.Compute(sys, wl2, gc, 0, nil)
	return pr2.FocalLength - pr1.FocalLength
}

// evaluateFocalLength returns the effective focal length of the system.
// Used as a merit term with a target value (e.g. 50.0) so the optimizer
// penalises (EFL − target)².  The sign is preserved so that a system with
// flipped EFL (negative focal length) receives a large penalty.
func evaluateFocalLength(surfaces []types.Surface, gc *glass.Catalog) float64 {
	sys := types.System{Surfaces: surfaces}
	pr := paraxial.Compute(sys, types.DefaultWavelength, gc, 0, nil)
	return pr.FocalLength
}

// glass_role tuning constants: the combined vd/nd residual maps an Abbe-number
// error and a refractive-index error into one value. glassRoleNDResidualScale
// converts an nd error (span ~0.6) into Abbe-number-like units (span ~60).
const glassRoleNDResidualScale = 60.0

// glassRoleForSurface returns the glass-role residual for the element
// containing the surface with the given ID: 0 when the surface is not part of
// a lens element (air gap, object, image plane). The residual is the combined
// signed magnitude of the vd and nd deviations from the role-derived targets,
// sign = sign(vd_actual − vd_target + K·(nd_actual − nd_target)), so
// residual² = (vd_actual − vd_target)² + K²·(nd_actual − nd_target)² — a valid
// least-squares term that keeps the Σresidual² == merit identity. The target
// comes from the frozen per-iteration role classification when provided,
// else from a fresh classification of the current surfaces.
func glassRoleForSurface(surfaces []types.Surface, gc *glass.Catalog, id int, frozen map[int]paraxial.ElementRole) float64 {
	var role paraxial.ElementRole
	ok := false
	if frozen != nil {
		role, ok = frozen[id]
	}
	if !ok {
		role, ok = paraxial.ElementRoleForSurface(surfaces, gc, id)
	}
	if !ok {
		return 0
	}
	vdActual := glassVDForSurface(surfaces, gc, id)
	ndActual := glassNDForSurface(surfaces, gc, id)
	dv := vdActual - role.VTarget
	dn := (ndActual - role.NDTarget) * glassRoleNDResidualScale
	mag := math.Sqrt(dv*dv + dn*dn)
	if mag == 0 || dv+dn >= 0 {
		return mag
	}
	return -mag
}

func evaluateSeidel(fieldAngle, wavelength float64, surfaces []types.Surface, gc *glass.Catalog) paraxial.SeidelCoefficients {
	return paraxial.ComputeSeidel(surfaces, fieldAngle, wavelength, gc)
}

// traceChiefImageHeight returns the image height of the field's chief ray at
// wavelength.
//
// pupilZ, when non-nil, is the config's entrance-pupil Z and the ray is
// launched through the pupil centre (the wavefront-plane launch the merit grid
// uses). It is a pointer, not a float, because 0 mm is a legitimate pupil
// position: a virtual entrance pupil at Z=0 must still aim the chief ray
// through the pupil centre, and only the nil case (no pupil resolved) may fall
// back to the historical axis launch. Without the centre launch the ray starts
// on the optical axis at zStart, which for any non-zero field angle arrives at
// the lens at 100*tan(theta) — far outside every aperture — so the trace failed
// and the caller got 0. That made lateral_color silently evaluate to exactly 0
// for every angle field.
func traceChiefImageHeight(surfaces []types.Surface, fieldAngleDeg float64, wavelength float64, gc *glass.Catalog, pupilZ *float64) float64 {
	engine := ray.NewEngine(gc, nil)
	path := dls.BuildPath(surfaces)

	dir := raymath.DirectionFromAngle(fieldAngleDeg)

	zStart := -100.0
	origin := types.Vec3{X: 0, Y: 0, Z: zStart}
	if pupilZ != nil {
		origin.X, origin.Y = pupil.GridCentre(dir, *pupilZ, zStart)
	}

	r := types.Ray{
		Wavelength: wavelength,
		Initial:    types.RayState{Origin: origin, Direction: dir},
		Path:       path,
		Jones:      types.NewCircularJones(true),
	}

	result := engine.TraceRay(r, surfaces, false)
	if result.Error != "" || len(result.Surfaces) == 0 {
		return 0
	}
	last := result.Surfaces[len(result.Surfaces)-1]
	return last.Position.Y
}

// ComputeOPDRMS returns the RMS of the optical path difference across a pupil
// grid, referenced to the mean OPL of the accepted rays.
func ComputeOPDRMS(points []dls.IPoint) float64 {
	var chiefOPL float64
	var chiefCount int
	for _, p := range points {
		if p.OK {
			chiefOPL += p.OPL
			chiefCount++
		}
	}
	if chiefCount == 0 {
		return 1e6
	}
	refOPL := chiefOPL / float64(chiefCount)

	var sumSq float64
	var count int
	for _, p := range points {
		if !p.OK {
			continue
		}
		opd := p.OPL - refOPL
		sumSq += opd * opd
		count++
	}
	if count == 0 {
		return 1e6
	}
	mean := sumSq / float64(count)
	return math.Sqrt(mean)
}

// evaluatePupilPosition returns the Z coordinate of the entrance pupil center
// for the given field. Returns 0 when the chief ray cannot be traced. The chief
// pass mirrors the document's own pupil definition (stop surface and chief-ray
// definition) so the term measures what `chief` reports for the same system.
func evaluatePupilPosition(fieldAngle, wavelength float64, surfaces []types.Surface, gc *glass.Catalog, surfaceSet []int, stopSurface int, rayDefinition string) float64 {
	if wavelength == 0 {
		wavelength = types.DefaultWavelength
	}
	fd := types.FieldDef{Angle: fieldAngle, Direction: []float64{0, 1}}
	results := chief.DetermineChiefRaysGridMode(
		types.System{Surfaces: surfaces, StopSurface: stopSurface},
		[]types.FieldDef{fd}, lastSurfaceID(surfaces), 16, gc,
		types.NewCircularJones(true), wavelength, false, types.GridPolar,
		nil, nil, nil, nil, 0, 0, rayDefinition,
	)
	if len(results) == 0 || results[0].EntrancePupil == nil {
		return 0
	}
	return results[0].EntrancePupil.Center.Z
}

// evaluatePupilDiameter returns the entrance pupil diameter for the given field.
func evaluatePupilDiameter(fieldAngle, wavelength float64, surfaces []types.Surface, gc *glass.Catalog, stopSurface int, rayDefinition string) float64 {
	if wavelength == 0 {
		wavelength = types.DefaultWavelength
	}
	fd := types.FieldDef{Angle: fieldAngle, Direction: []float64{0, 1}}
	results := chief.DetermineChiefRaysGridMode(
		types.System{Surfaces: surfaces, StopSurface: stopSurface},
		[]types.FieldDef{fd}, lastSurfaceID(surfaces), 16, gc,
		types.NewCircularJones(true), wavelength, false, types.GridPolar,
		nil, nil, nil, nil, 0, 0, rayDefinition,
	)
	if len(results) == 0 || results[0].EntrancePupil == nil {
		return 0
	}
	return results[0].EntrancePupil.Radius * 2.0
}

// evaluateVignetting returns the vignetting ratio for the given field and
// surface. surfaceSet[0] is the surface ID. Returns 0 when not computable.
func evaluateVignetting(fieldAngle, wavelength float64, surfaces []types.Surface, gc *glass.Catalog, surfaceSet []int, stopSurface int, rayDefinition string) float64 {
	if len(surfaceSet) == 0 {
		return 0
	}
	sid := surfaceSet[0]
	// Find the surface index and its diameter.
	var surfIdx = -1
	var surfDiam float64
	for i, s := range surfaces {
		if s.ID == sid {
			surfIdx = i
			surfDiam = s.Diameter
			break
		}
	}
	if surfIdx < 0 || surfDiam <= 0 {
		return 0
	}
	if wavelength == 0 {
		wavelength = types.DefaultWavelength
	}
	fd := types.FieldDef{Angle: fieldAngle, Direction: []float64{0, 1}}
	results := chief.DetermineChiefRaysGridMode(
		types.System{Surfaces: surfaces, StopSurface: stopSurface},
		[]types.FieldDef{fd}, lastSurfaceID(surfaces), 64, gc,
		types.NewCircularJones(true), wavelength, false, types.GridPolar,
		nil, nil, nil, nil, 0, 0, rayDefinition,
	)
	if len(results) == 0 {
		return 0
	}
	r := results[0]
	// Count rays that were vignetted (ImageX/ImageY nil means the ray missed
	// this surface or was clipped by an aperture).
	nTotal := 0
	nVignetted := 0
	for _, gp := range r.GridPoints {
		if gp.ImageX == nil {
			nTotal++
			nVignetted++
			continue
		}
		nTotal++
	}
	if nTotal == 0 {
		return 0
	}
	return float64(nVignetted) / float64(nTotal)
}

// evaluateClearAperture returns the clear aperture diameter at the given surface
// for the field's beam envelope. Returns 0 when not computable.
func evaluateClearAperture(fieldAngle, wavelength float64, surfaces []types.Surface, gc *glass.Catalog, surfaceSet []int, stopSurface int, rayDefinition string) float64 {
	if len(surfaceSet) == 0 {
		return 0
	}
	sid := surfaceSet[0]
	if wavelength == 0 {
		wavelength = types.DefaultWavelength
	}
	fd := types.FieldDef{Angle: fieldAngle, Direction: []float64{0, 1}}
	results := chief.DetermineChiefRaysGridMode(
		types.System{Surfaces: surfaces, StopSurface: stopSurface},
		[]types.FieldDef{fd}, lastSurfaceID(surfaces), 64, gc,
		types.NewCircularJones(true), wavelength, false, types.GridPolar,
		nil, nil, nil, nil, 0, 0, rayDefinition,
	)
	if len(results) == 0 {
		return 0
	}
	engine := ray.NewEngine(gc, nil)
	path := dls.BuildPath(surfaces)
	envelope := chief.BeamEnvelope(results, engine, surfaces, path, wavelength, types.NewCircularJones(true))
	return envelope[sid]
}

// evaluateEdgeThickness returns the edge thickness for the element containing
// the given surface. surfaceSet[0] is the surface ID. Returns 0 when not
// computable.
func evaluateEdgeThickness(fieldAngle, wavelength float64, surfaces []types.Surface, gc *glass.Catalog, surfaceSet []int) float64 {
	if len(surfaceSet) == 0 {
		return 0
	}
	sid := surfaceSet[0]
	// Find the element containing this surface and compute edge thickness.
	for i := range surfaces {
		if surfaces[i].ID != sid {
			continue
		}
		// For a simple approximation, use the min_glass_path/max_glass_path
		// fields if available.
		if surfaces[i].MinGlassPath > 0 && surfaces[i].MaxGlassPath > 0 {
			return surfaces[i].MaxGlassPath - surfaces[i].MinGlassPath
		}
		break
	}
	return 0
}

// lastSurfaceID returns the ID of the last surface in the system.
func lastSurfaceID(surfaces []types.Surface) int {
	if len(surfaces) == 0 {
		return 0
	}
	return surfaces[len(surfaces)-1].ID
}
