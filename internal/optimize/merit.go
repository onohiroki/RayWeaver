package optimize

import (
	"math"

	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/types"
)

const (
	MeritSpotRMS          = "spot_rms"
	MeritDistortionPct    = "distortion_pct"
	MeritLateralColor     = "lateral_color"
	MeritLongitudinalColor = "longitudinal_color"
	MeritSeidelSpherical  = "seidel_spherical"
	MeritSeidelComa       = "seidel_coma"
	MeritSeidelAstigmatism = "seidel_astigmatism"
	MeritSeidelDistortion = "seidel_distortion"
	MeritOPDRMS           = "opd_rms"
)

func EvaluateMeritKind(kind string, term MeritTerm, surfaces []types.Surface, gc *glass.Catalog, o *Optimizer) float64 {
	if gc == nil {
		gc = glass.NewCatalog()
	}

	switch kind {
	case MeritDistortionPct:
		return evaluateDistortionPct(term, surfaces, gc)
	case MeritLateralColor:
		return evaluateLateralColor(term, surfaces, gc)
	case MeritLongitudinalColor:
		return evaluateLongitudinalColor(term, surfaces, gc)
	case MeritSeidelSpherical:
		return evaluateSeidel(term, surfaces, gc).Spherical
	case MeritSeidelComa:
		return evaluateSeidel(term, surfaces, gc).Coma
	case MeritSeidelAstigmatism:
		return evaluateSeidel(term, surfaces, gc).Astigmatism
	case MeritSeidelDistortion:
		return evaluateSeidel(term, surfaces, gc).Distortion
	case MeritOPDRMS:
		return evaluateOPDRMS(term, surfaces, gc, o)
	default:
		return 0
	}
}

func evaluateDistortionPct(term MeritTerm, surfaces []types.Surface, gc *glass.Catalog) float64 {
	wl := term.Wavelength
	if wl == 0 {
		wl = 0.0005876
	}

	yChief := traceChiefImageHeight(surfaces, term.FieldAngle, wl, gc)
	if yChief == 0 {
		return 0
	}

	sys := types.System{Surfaces: surfaces}
	pr := paraxial.Compute(sys, wl, gc, 0, nil)
	yParax := pr.FocalLength * math.Tan(term.FieldAngle*math.Pi/180.0)

	if math.Abs(yParax) < 1e-15 {
		return 0
	}
	return 100.0 * (yChief - yParax) / yParax
}

func evaluateLateralColor(term MeritTerm, surfaces []types.Surface, gc *glass.Catalog) float64 {
	wl1 := term.Wavelength
	wl2 := term.Wavelength2
	if wl1 == 0 {
		wl1 = 0.0005876
	}
	if wl2 == 0 {
		return 0
	}

	y1 := traceChiefImageHeight(surfaces, term.FieldAngle, wl1, gc)
	y2 := traceChiefImageHeight(surfaces, term.FieldAngle, wl2, gc)
	return y2 - y1
}

func evaluateLongitudinalColor(term MeritTerm, surfaces []types.Surface, gc *glass.Catalog) float64 {
	wl1 := term.Wavelength
	wl2 := term.Wavelength2
	if wl1 == 0 {
		wl1 = 0.0005876
	}
	if wl2 == 0 {
		return 0
	}

	sys := types.System{Surfaces: surfaces}
	pr1 := paraxial.Compute(sys, wl1, gc, 0, nil)
	pr2 := paraxial.Compute(sys, wl2, gc, 0, nil)
	return pr2.FocalLength - pr1.FocalLength
}

func evaluateSeidel(term MeritTerm, surfaces []types.Surface, gc *glass.Catalog) paraxial.SeidelCoefficients {
	return paraxial.ComputeSeidel(surfaces, term.FieldAngle, term.Wavelength, gc)
}

func traceChiefImageHeight(surfaces []types.Surface, fieldAngleDeg float64, wavelength float64, gc *glass.Catalog) float64 {
	engine := ray.NewEngine(gc, nil)
	path := buildPath(surfaces)

	thetaRad := fieldAngleDeg * math.Pi / 180.0
	sinT := math.Sin(thetaRad)
	cosT := math.Cos(thetaRad)
	dir := types.Vec3{X: 0, Y: sinT, Z: cosT}.Normalize()

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

func evaluateOPDRMS(term MeritTerm, surfaces []types.Surface, gc *glass.Catalog, o *Optimizer) float64 {
	if o == nil {
		return 0
	}
	points, _ := o.traceFieldGrid(surfaces, term.FieldAngle, term.FieldDir, term.Wavelength)
	if len(points) == 0 {
		return 1e6
	}
	return ComputeOPDRMS(points)
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

func buildPath(surfaces []types.Surface) []int {
	path := []int{0}
	for _, s := range surfaces {
		if s.ID > 0 {
			path = append(path, s.ID)
		}
	}
	return path
}


