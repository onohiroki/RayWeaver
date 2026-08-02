package optimize

import (
	"math"

	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/raymath"
	"github.com/hiroki/rayweaver/internal/types"
)

const (
	MeritSpotRMS           = "spot_rms"
	MeritDistortionPct     = "distortion_pct"
	MeritLateralColor      = "lateral_color"
	MeritLongitudinalColor = "longitudinal_color"
	MeritSeidelSpherical   = "seidel_spherical"
	MeritSeidelComa        = "seidel_coma"
	MeritSeidelAstigmatism = "seidel_astigmatism"
	MeritSeidelDistortion  = "seidel_distortion"
	MeritOPDRMS            = "opd_rms"
)

// evaluateKindTerm evaluates a non-spot merit term for the given config,
// returning 0 for unknown kinds.
func (o *Optimizer) evaluateKindTerm(cfg *config, term *meritTerm, surfaces []types.Surface, gc *glass.Catalog) float64 {
	switch term.kind {
	case MeritOPDRMS:
		points := o.traceFieldGrid(gc, surfaces, cfg, term)
		if len(points) == 0 {
			return 1e6
		}
		return ComputeOPDRMS(points)
	default:
		return evaluateKindValue(term.kind, term, surfaces, gc)
	}
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
// loop (e.g. external tools evaluating a single term). OPD_RMS requires an
// Optimizer to trace the grid and returns 0 when o is nil.
func EvaluateMeritKind(kind string, term MeritTerm, surfaces []types.Surface, gc *glass.Catalog, o *Optimizer) float64 {
	if kind == MeritOPDRMS {
		if o == nil {
			return 0
		}
		cfg := o.primaryConfig()
		mt := meritTerm{
			kind:       MeritOPDRMS,
			fieldAngle: term.FieldAngle,
			wavelength: term.Wavelength,
		}
		return o.evaluateKindTerm(cfg, &mt, surfaces, gc)
	}
	mt := meritTerm{
		kind:        kind,
		fieldAngle:  term.FieldAngle,
		wavelength:  term.Wavelength,
		wavelength2: term.Wavelength2,
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
