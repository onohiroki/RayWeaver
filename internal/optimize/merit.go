package optimize

import (
	"math"

	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/psf"
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

	// Wavefront paraboloid fit kinds. Each evaluates the least-squares
	// quadratic fit P(x,y) = a·x² + b·y² + c·xy + d·x + e·y + f of the OPD on
	// the reference surface (referenced to the best-focus point, exactly like
	// the `wavefront` command) and targets one coefficient — either the raw
	// fit coefficients (x2/y2/xy/x/y/constant) or the derived low-order
	// magnitudes (defocus/astigmatism/tilt/rms_residual). The reference
	// surface defaults to the last optical surface; the grid follows
	// optimization.num_rays and optimization.aperture_margin.
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
)

// evaluateKindTerm evaluates a non-spot merit term for the given config,
// returning 0 for unknown kinds. The per-evaluation grid cache is shared with
// the grid merit kinds so opd_rms and the spot kinds reuse one pupil trace per
// (field, wavelength); a nil cache disables caching.
func (o *Optimizer) evaluateKindTerm(cfg *config, term *meritTerm, surfaces []types.Surface, gc *glass.Catalog, cache *evalGridCache) float64 {
	switch term.kind {
	case MeritOPDRMS:
		points := o.gridForTerm(cache, gc, surfaces, cfg, term)
		if len(points) == 0 {
			return o.opdDegenerate
		}
		val := ComputeOPDRMS(points)
		if val >= 1e6 {
			return o.opdDegenerate
		}
		return val
	default:
		if isGridKind(term.kind) {
			return o.evaluateGridKind(cfg, term, surfaces, gc, cache)
		}
		if isWavefrontKind(term.kind) {
			return o.evaluateWavefrontTerm(cfg, term, surfaces, gc)
		}
		return evaluateKindValue(term.kind, term, surfaces, gc)
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
// determinant). The entrance-pupil grid is centred on the config's frozen
// per-iteration pupil (dls.pupilZ) so the DLS base point and its Jacobian
// perturbations share one pupil, keeping the derivative consistent. A
// degenerate fit (no grid, too few valid rays) returns the bounded degenerate
// penalty so the solver is pushed away rather than misled.
func (o *Optimizer) evaluateWavefrontTerm(cfg *config, term *meritTerm, surfaces []types.Surface, gc *glass.Catalog) float64 {
	refSurface := cfg.refSurface
	if refSurface <= 0 || refSurface >= surfaces[len(surfaces)-1].ID {
		// The wavefront reference surface must lie before the image plane: a
		// chief reference surface set to the image plane (the conventional
		// last surface) is not a valid wavefront sampling surface. Fall back
		// to the last optical surface, exactly like the standalone `wavefront`
		// command's default.
		refSurface = psf.DefaultReferenceSurface(surfaces)
	}
	angle := o.termFieldAngle(cfg, term, surfaces, gc)
	fd := types.FieldDef{Angle: angle, Direction: []float64{0, 1}}
	// Carry the term's field declared vignetting (and direction) into the
	// pupil-grid clip, matching the standalone `wavefront` command. Without it
	// a heavily vignetted off-axis corner samples the full pupil and the fit
	// collapses (< 6 valid rays). A negative fieldIndex (unset) keeps the
	// legacy no-clip behaviour.
	if term.fieldIndex >= 0 && cfg != nil && term.fieldIndex < len(cfg.fields) {
		f := &cfg.fields[term.fieldIndex]
		fd.Vignetting = f.Vignetting
		if dx, dy, ok := fieldDir(f.Direction); ok {
			fd.Direction = []float64{dx, dy}
		}
	}
	sys := types.System{Surfaces: surfaces, StopSurface: cfg.stopSurface}

	// fit evaluates the term's quantity on the given (frozen or dynamic)
	// pupil. The closure keeps the frozen→dynamic fallback and the bounded
	// degenerate penalty shared by both the paraboloid and sphere kinds.
	fit := func(frozenPupilZ *float64) (float64, error) {
		switch term.kind {
		case MeritWavefrontSphereRMS, MeritWavefrontSpherePV:
			rms, pv, err := wavefront.FitFieldSphereRMS(sys, gc, fd, refSurface, o.numRays, term.wavelength, o.apertureMargin, frozenPupilZ)
			if err != nil {
				return 0, err
			}
			if term.kind == MeritWavefrontSpherePV {
				return pv, nil
			}
			return rms, nil
		default:
			pab, err := wavefront.FitFieldParaboloid(sys, gc, fd, refSurface, o.numRays, term.wavelength, o.apertureMargin, frozenPupilZ)
			if err != nil {
				return 0, err
			}
			return wavefrontCoeff(term.kind, pab), nil
		}
	}

	val, err := fit(&cfg.pupilZ)
	if err != nil {
		// Fall back to the dynamic pupil (chief resolves the entrance pupil
		// itself). The frozen grid does not apply the fixed-surface vignetting
		// cut, so a strongly off-axis field whose beam clips a fixed aperture
		// ends up with too few valid rays; the chief-derived grid clips
		// correctly and matches the standalone `wavefront` command exactly.
		val, err = fit(nil)
		if err != nil {
			// A wavefront fit that fails even with the dynamic pupil (e.g. a
			// strongly off-axis field whose beam is fully clipped) returns the
			// bounded degenerate penalty instead of the legacy 1e6 sentinel,
			// so the DLS line search is not stalled by a weight·1e12 merit
			// contribution.
			return o.wavefrontDegenerate
		}
	}
	return val
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

func evaluateKindValue(kind string, term *meritTerm, surfaces []types.Surface, gc *glass.Catalog) float64 {
	if gc == nil {
		gc = glass.NewCatalog()
	}
	switch kind {
	case MeritDistortionPct:
		return evaluateDistortionPct(term.fieldAngle, term.wavelength, surfaces, gc)
	case MeritLateralColor:
		return evaluateLateralColor(term.fieldAngle, term.wavelength, term.wavelength2, surfaces, gc)
	case MeritLongitudinalColor:
		return evaluateLongitudinalColor(term.wavelength, term.wavelength2, surfaces, gc)
	case MeritGlassRole:
		if len(term.surfaceSet) == 0 {
			return 0
		}
		return glassRoleForSurface(surfaces, gc, term.surfaceSet[0])
	case MeritSeidelSpherical:
		return evaluateSeidel(term.fieldAngle, term.wavelength, surfaces, gc).Spherical
	case MeritSeidelComa:
		return evaluateSeidel(term.fieldAngle, term.wavelength, surfaces, gc).Coma
	case MeritSeidelAstigmatism:
		return evaluateSeidel(term.fieldAngle, term.wavelength, surfaces, gc).Astigmatism
	case MeritSeidelDistortion:
		return evaluateSeidel(term.fieldAngle, term.wavelength, surfaces, gc).Distortion
	default:
		return 0
	}
}

// EvaluateMeritKind is the public wrapper over the merit-kind evaluator,
// kept for callers that do not go through the unified Optimizer evaluation
// loop (e.g. external tools evaluating a single term). OPD_RMS and the
// wavefront paraboloid kinds require an Optimizer to trace the grid and return
// 0 when o is nil.
func EvaluateMeritKind(kind string, term MeritTerm, surfaces []types.Surface, gc *glass.Catalog, o *Optimizer) float64 {
	if kind == MeritOPDRMS || isWavefrontKind(kind) || isGridKind(kind) {
		if o == nil {
			return 0
		}
		cfg := o.primaryConfig()
		dx, dy := 0.0, 1.0
		if len(term.FieldDir) >= 2 {
			dx, dy = normalizeDir(term.FieldDir)
		}
		mt := meritTerm{
			kind:       kind,
			fieldAngle: term.FieldAngle,
			fieldDirX:  dx,
			fieldDirY:  dy,
			wavelength: term.Wavelength,
			fraction:   term.Fraction,
			surfaceSet: append([]int(nil), term.SurfaceSet...),
		}
		return o.evaluateKindTerm(cfg, &mt, surfaces, gc, nil)
	}
	mt := meritTerm{
		kind:        kind,
		fieldAngle:  term.FieldAngle,
		wavelength:  term.Wavelength,
		wavelength2: term.Wavelength2,
		surfaceSet:  append([]int(nil), term.SurfaceSet...),
	}
	return evaluateKindValue(kind, &mt, surfaces, gc)
}

func evaluateDistortionPct(fieldAngle, wavelength float64, surfaces []types.Surface, gc *glass.Catalog) float64 {
	if wavelength == 0 {
		wavelength = types.DefaultWavelength
	}

	yChief := traceChiefImageHeight(surfaces, fieldAngle, wavelength, gc)
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

func evaluateLateralColor(fieldAngle, wl1, wl2 float64, surfaces []types.Surface, gc *glass.Catalog) float64 {
	if wl1 == 0 {
		wl1 = types.DefaultWavelength
	}
	if wl2 == 0 {
		return 0
	}

	y1 := traceChiefImageHeight(surfaces, fieldAngle, wl1, gc)
	y2 := traceChiefImageHeight(surfaces, fieldAngle, wl2, gc)
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

// glass_role tuning constants: the target Abbe number of an element of
// thin-lens power phi at the d-line is vd_center + delta·tanh(gamma·phi), so a
// positive-power element is steered to the crown (high-vd) end and a
// negative-power element to the flint (low-vd) end.
const (
	meritGlassRoleCenter = 45.0
	meritGlassRoleDelta  = 16.0
	meritGlassRoleGamma  = 1.0
)

func glassRoleTarget(phi float64) float64 {
	return meritGlassRoleCenter + meritGlassRoleDelta*math.Tanh(meritGlassRoleGamma*phi)
}

// glassRoleForSurface returns the glass-role residual
// vd_actual − vd_target for the element containing the surface with the given
// ID: 0 when the surface is not part of a lens element (air gap, object,
// image plane).
func glassRoleForSurface(surfaces []types.Surface, gc *glass.Catalog, id int) float64 {
	phi := paraxial.ElementPowerForSurface(surfaces, paraxial.DLine, gc, id)
	vdActual := glassVDForSurface(surfaces, gc, id)
	return vdActual - glassRoleTarget(phi)
}

func evaluateSeidel(fieldAngle, wavelength float64, surfaces []types.Surface, gc *glass.Catalog) paraxial.SeidelCoefficients {
	return paraxial.ComputeSeidel(surfaces, fieldAngle, wavelength, gc)
}

func traceChiefImageHeight(surfaces []types.Surface, fieldAngleDeg float64, wavelength float64, gc *glass.Catalog) float64 {
	engine := ray.NewEngine(gc, nil)
	path := dls.BuildPath(surfaces)

	dir := raymath.DirectionFromAngle(fieldAngleDeg)

	zStart := -100.0
	origin := types.Vec3{X: 0, Y: 0, Z: zStart}

	r := types.Ray{
		Wavelength: wavelength,
		Initial:    types.RayState{Origin: origin, Direction: dir},
		Path:       path,
		Jones:      types.NewCircularJones(true),
	}

	result := engine.TraceRay(r, surfaces)
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
