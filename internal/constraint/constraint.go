package constraint

import (
	"math"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/raymath"
	"github.com/hiroki/rayweaver/internal/types"
)

func Evaluate(op types.ConstraintOperand, surfaces []types.Surface, fieldAngle float64, gc *glass.Catalog) float64 {
	if !op.Active {
		return 0
	}

	switch op.Measure {
	case types.MeasureImageHeight:
		return evaluateImageHeight(surfaces, fieldAngle, op.Wavelength, gc)
	case types.MeasureIncidentAngle:
		return evaluateIncidentAngle(surfaces, fieldAngle, op.Wavelength, op.Surface, gc)
	case types.MeasureThickness:
		return evaluateThickness(surfaces, op.Surface)
	case types.MeasureEFL:
		return evaluateEFL(surfaces, gc)
	case types.MeasureAbsEFL:
		return math.Abs(evaluateEFL(surfaces, gc))
	case types.MeasureSystemLength:
		return evaluateSystemLength(surfaces)
	case types.MeasureEntrancePupilDiameter:
		return evaluateEntrancePupilDiameter(surfaces, gc)
	case types.MeasureDiameter:
		return evaluateDiameter(surfaces, op.Surface)
	case types.MeasureEdgeThickness:
		backID := int(math.Round(op.Target))
		return evaluateEdgeThickness(surfaces, op.Surface, backID)
	case types.MeasureFNumber:
		return evaluateFNumber(surfaces, gc)
	default:
		return 0
	}
}

func ComputeError(kind types.ConstraintKind, value float64, op types.ConstraintOperand) float64 {
	switch kind {
	case types.ConstraintEquality:
		return value - op.Target

	case types.ConstraintInequalityUpper:
		if value > op.Upper {
			return value - op.Upper
		}
		return 0

	case types.ConstraintInequalityLower:
		if value < op.Lower {
			return op.Lower - value
		}
		return 0

	case types.ConstraintBand:
		if value < op.Lower {
			return op.Lower - value
		}
		if value > op.Upper {
			return value - op.Upper
		}
		return 0

	case types.ConstraintFuzzy:
		d := math.Abs(value - op.Target)
		if d <= op.BandWidth {
			return 0
		}
		return (d - op.BandWidth) / op.Softness

	default:
		return 0
	}
}

func traceChiefRay(surfaces []types.Surface, fieldAngle, wavelength float64, gc *glass.Catalog) types.RayResult {
	engine := ray.NewEngine(gc, nil)

	thetaRad := fieldAngle * math.Pi / 180.0
	sinT := math.Sin(thetaRad)
	cosT := math.Cos(thetaRad)

	dx, dy := 0.0, 1.0
	rayDir := types.Vec3{X: sinT * dx, Y: sinT * dy, Z: cosT}.Normalize()

	zStart := -100.0
	origin := types.Vec3{X: 0, Y: 0, Z: zStart}

	path := buildPath(surfaces)

	r := types.Ray{
		Wavelength: wavelength,
		Initial:    types.RayState{Origin: origin, Direction: rayDir},
		Path:       path,
		Jones:      types.NewCircularJones(true),
	}

	return engine.TraceRay(r, surfaces)
}

func evaluateImageHeight(surfaces []types.Surface, fieldAngle, wavelength float64, gc *glass.Catalog) float64 {
	if wavelength == 0 {
		wavelength = 0.0005876
	}
	result := traceChiefRay(surfaces, fieldAngle, wavelength, gc)
	if result.Error != "" || len(result.Surfaces) == 0 {
		return 0
	}
	last := result.Surfaces[len(result.Surfaces)-1]
	return last.Position.Y
}

func evaluateIncidentAngle(surfaces []types.Surface, fieldAngle, wavelength float64, targetSurface int, gc *glass.Catalog) float64 {
	if targetSurface == 0 {
		return 0
	}
	if wavelength == 0 {
		wavelength = 0.0005876
	}
	result := traceChiefRay(surfaces, fieldAngle, wavelength, gc)
	if result.Error != "" || len(result.Surfaces) == 0 {
		return 0
	}

	targetIdx := -1
	prevIdx := -1
	for i, sr := range result.Surfaces {
		if sr.SurfaceID == targetSurface {
			targetIdx = i
			prevIdx = i - 1
			break
		}
	}
	if targetIdx < 0 || prevIdx < 0 {
		return 0
	}

	approachingDir := result.Surfaces[prevIdx].Direction

	surf := findSurfaceByID(surfaces, targetSurface)
	if surf == nil {
		return 0
	}

	hitGlobal := result.Surfaces[targetIdx].Position
	hitLocal := surf.GlobalToLocal.MultiplyPoint(hitGlobal)
	localDir := surf.GlobalToLocal.MultiplyVector(approachingDir).Normalize()

	normal := computeNormal(surf, hitLocal)

	cosTheta := -localDir.Dot(normal)
	if cosTheta < 0 {
		cosTheta = -cosTheta
	}

	return math.Acos(cosTheta) * 180.0 / math.Pi
}

func evaluateThickness(surfaces []types.Surface, targetSurface int) float64 {
	for _, s := range surfaces {
		if s.ID == targetSurface {
			return s.Thickness
		}
	}
	return 0
}

func evaluateEFL(surfaces []types.Surface, gc *glass.Catalog) float64 {
	sys := types.System{Surfaces: surfaces}
	res := paraxial.Compute(sys, 0.0005876, gc, 0, nil)
	return res.FocalLength
}

func evaluateDiameter(surfaces []types.Surface, id int) float64 {
	for _, s := range surfaces {
		if s.ID == id {
			return s.Diameter
		}
	}
	return 0
}

func sagitta(curvature, semiDiam float64) float64 {
	if curvature == 0 || semiDiam <= 0 {
		return 0
	}
	R := 1.0 / curvature
	absR := math.Abs(R)
	h := math.Abs(semiDiam)
	if h >= absR {
		return 0
	}
	return R - math.Copysign(math.Sqrt(absR*absR-h*h), R)
}

func evaluateEdgeThickness(surfaces []types.Surface, frontID, backID int) float64 {
	var front, back *types.Surface
	for i := range surfaces {
		if surfaces[i].ID == frontID {
			front = &surfaces[i]
		}
		if surfaces[i].ID == backID {
			back = &surfaces[i]
		}
	}
	if front == nil || back == nil {
		return 0
	}

	center := front.Thickness
	h := math.Min(front.Diameter, back.Diameter) / 2.0
	return center + sagitta(back.Curvature, h) - sagitta(front.Curvature, h)
}

func evaluateFNumber(surfaces []types.Surface, gc *glass.Catalog) float64 {
	sys := types.System{Surfaces: surfaces}
	res := paraxial.Compute(sys, 0.0005876, gc, 0, nil)
	return res.InfConjImageSpaceFNumber
}

func evaluateEntrancePupilDiameter(surfaces []types.Surface, gc *glass.Catalog) float64 {
	sys := types.System{Surfaces: surfaces}
	res := paraxial.Compute(sys, 0.0005876, gc, 0, nil)
	return res.EntrancePupilDiameter
}

func evaluateSystemLength(surfaces []types.Surface) float64 {
	total := 0.0
	for _, s := range surfaces {
		total += s.Thickness
	}
	return total
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

func findSurfaceByID(surfaces []types.Surface, id int) *types.Surface {
	for i := range surfaces {
		if surfaces[i].ID == id {
			return &surfaces[i]
		}
	}
	return nil
}

func computeNormal(surf *types.Surface, p types.Vec3) types.Vec3 {
	switch surf.Type {
	case types.Sphere:
		if surf.Radius() == 0 {
			return types.Vec3{0, 0, 1}
		}
		return raymath.SphereNormal(p, surf.Radius())
	case types.AspherePolynomial:
		sagFunc := func(h float64) float64 {
			return raymath.PolynomialAsphereSag(h, surf.Radius(), surf.Conic, surf.Coefficients)
		}
		return raymath.AsphereNormal(p, sagFunc)
	case types.AsphereZernike:
		sagFunc := func(h float64) float64 {
			return raymath.ZernikeAsphereSag(h, surf.Radius(), surf.Conic, surf.Coefficients, surf.NormRadius)
		}
		return raymath.AsphereNormal(p, sagFunc)
	default:
		if surf.Radius() == 0 {
			return types.Vec3{0, 0, 1}
		}
		return raymath.SphereNormal(p, surf.Radius())
	}
}
