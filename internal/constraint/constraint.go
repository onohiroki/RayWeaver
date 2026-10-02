package constraint

import (
	"math"

	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/raymath"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// Evaluate evaluates one constraint operand against the current system.
// epd is the prescribed entrance-pupil diameter the caller already resolved
// (the virtual entrance pupil's diameter, or 0 to fall back to the paraxial
// entrance pupil). It only feeds the edge_thickness measure, which needs to
// know where the beam actually reaches the element.
func Evaluate(op types.ConstraintOperand, surfaces []types.Surface, fieldAngle float64, gc *glass.Catalog, numRays int, apertureMargin float64, stopSurface int, pupilZ float64, epd float64) float64 {
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
	case types.MeasureFocalLength:
		return math.Abs(evaluateEFL(surfaces, gc))
	case types.MeasureSystemLength:
		return evaluateSystemLength(surfaces)
	case types.MeasureEntrancePupilDiameter:
		return evaluateEntrancePupilDiameter(surfaces, gc, stopSurface)
	case types.MeasureDiameter:
		return evaluateDiameter(surfaces, op.Surface)
	case types.MeasureEdgeThickness:
		backID := op.BackSurface
		if backID == 0 {
			// Auto-derive the back surface: the next surface in system
			// (slice) order, which is what front.Thickness is measured to.
			backID = nextSurfaceID(surfaces, op.Surface)
		}
		return evaluateEdgeThickness(surfaces, op.Surface, backID, gc, stopSurface, epd)
	case types.MeasureFNumber:
		return evaluateFNumber(surfaces, gc, stopSurface)
	case types.MeasureBeamClearance:
		return evaluateBeamClearance(surfaces, pupilZ, fieldAngle, op.Wavelength, gc, op.Surface, numRays, apertureMargin)
	case types.MeasureVignettingFactor:
		return evaluateVignettingFactor(surfaces, pupilZ, fieldAngle, op.Wavelength, gc, numRays, apertureMargin)
	case types.MeasureBeamDiameter:
		return evaluateBeamDiameter(surfaces, pupilZ, fieldAngle, op.Wavelength, gc, op.Surface, numRays, apertureMargin)
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

	thetaRad := raymath.DegToRad(fieldAngle)
	sinT := math.Sin(thetaRad)
	cosT := math.Cos(thetaRad)

	dx, dy := 0.0, 1.0
	rayDir := types.Vec3{X: sinT * dx, Y: sinT * dy, Z: cosT}.Normalize()

	zStart := -100.0
	origin := types.Vec3{X: 0, Y: 0, Z: zStart}

	path := dls.BuildPath(surfaces)

	r := types.Ray{
		Wavelength: wavelength,
		Initial:    types.RayState{Origin: origin, Direction: rayDir},
		Path:       path,
		Jones:      types.NewCircularJones(true),
	}

	return engine.TraceRay(r, surfaces, false)
}

func evaluateImageHeight(surfaces []types.Surface, fieldAngle, wavelength float64, gc *glass.Catalog) float64 {
	if wavelength == 0 {
		wavelength = types.DefaultWavelength
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
		wavelength = types.DefaultWavelength
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
	res := paraxial.Compute(sys, types.DefaultWavelength, gc, 0, nil)
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

func evaluateBeamClearance(surfaces []types.Surface, pupilZ float64, fieldAngle, wavelength float64, gc *glass.Catalog, surfaceID int, numRays int, apertureMargin float64) float64 {
	if wavelength == 0 {
		wavelength = types.DefaultWavelength
	}
	perSurfMax := dls.TraceFieldGridExtents(gc, surfaces, 0, pupilZ, fieldAngle, []float64{0, 1}, wavelength, apertureMargin, numRays, 0, 1, 0)
	if perSurfMax == nil {
		return 0
	}
	for _, s := range surfaces {
		if s.ID == surfaceID {
			return s.Diameter/2 - perSurfMax[surfaceID]
		}
	}
	return 0
}

func evaluateBeamDiameter(surfaces []types.Surface, pupilZ float64, fieldAngle, wavelength float64, gc *glass.Catalog, surfaceID int, numRays int, apertureMargin float64) float64 {
	if wavelength == 0 {
		wavelength = types.DefaultWavelength
	}
	perSurfMax := dls.TraceFieldGridExtents(gc, surfaces, 0, pupilZ, fieldAngle, []float64{0, 1}, wavelength, apertureMargin, numRays, 0, 1, 0)
	if perSurfMax == nil {
		return 0
	}
	for _, s := range surfaces {
		if s.ID == surfaceID {
			return 2 * perSurfMax[surfaceID]
		}
	}
	return 0
}

func evaluateVignettingFactor(surfaces []types.Surface, pupilZ float64, fieldAngle, wavelength float64, gc *glass.Catalog, numRays int, apertureMargin float64) float64 {
	if wavelength == 0 {
		wavelength = types.DefaultWavelength
	}
	// No vignetting ellipse: a constraint operand has no access to the field's
	// declared fields[].vignetting, so this measures the physical vignetting
	// (aperture / edge-thickness clipping) of the full prescribed pupil.
	points, _ := dls.TraceFieldGrid(gc, surfaces, 0, pupilZ, fieldAngle, []float64{0, 1}, wavelength, apertureMargin, numRays, 0, 1, 0, nil)
	if len(points) == 0 {
		return 0
	}
	passCount := 0
	for _, p := range points {
		if p.OK {
			passCount++
		}
	}
	return float64(passCount) / float64(len(points))
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

// nextSurfaceID returns the ID of the surface immediately after frontID in the
// system (slice) order, which is the surface front.Thickness is measured to. It
// returns 0 when frontID is unknown or is the last surface.
func nextSurfaceID(surfaces []types.Surface, frontID int) int {
	idx := dls.SurfaceIndex(surfaces, frontID)
	if idx < 0 || idx+1 >= len(surfaces) {
		return 0
	}
	return surfaces[idx+1].ID
}

// evaluateEdgeThickness returns the edge thickness of the element between
// frontID and backID, measured at the larger of the element's physical rim
// min(front, back diameter)/2 and the prescribed entrance-pupil radius.
//
// The rim alone was not enough: every auto_aperture diameter is an optimisation
// variable, so the solver could shrink it and make the sagitta height - and with
// it the required thickness - disappear, while the beam still lands further out
// than the rim the band was being measured at. Flooring the height by the pupil
// keeps the constraint measuring the edge the light actually crosses. A design
// whose rim already reaches beyond the pupil is unaffected, so the extra floor
// only ever fires when an aperture has been drawn in below the beam.
// Returns 0 when either surface is unknown.
func evaluateEdgeThickness(surfaces []types.Surface, frontID, backID int, gc *glass.Catalog, stopSurface int, epd float64) float64 {
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
	if r := prescribedPupilRadius(surfaces, gc, stopSurface, epd); r > h {
		h = r
	}
	return center + sagitta(back.Curvature, h) - sagitta(front.Curvature, h)
}

// prescribedPupilRadius is the radius at which the prescribed beam reaches the
// element. The caller's explicit entrance-pupil diameter wins (the virtual
// entrance pupil resolves it once per evaluation and needs no catalogue), else
// the paraxial entrance pupil; 0 when neither is available, which leaves the
// physical rim as the measurement height.
func prescribedPupilRadius(surfaces []types.Surface, gc *glass.Catalog, stopSurface int, epd float64) float64 {
	if epd > 0 {
		return epd / 2
	}
	if gc == nil {
		return 0
	}
	if r := paraxial.EntrancePupilRadius(surfaces, stopSurface, types.DefaultWavelength, gc); r > 0 {
		return r
	}
	return 0
}

func evaluateFNumber(surfaces []types.Surface, gc *glass.Catalog, stopSurface int) float64 {
	sys := types.System{Surfaces: surfaces, StopSurface: stopSurface}
	res := paraxial.Compute(sys, types.DefaultWavelength, gc, 0, nil)
	if res.InfConjImageSpaceFNumber > 0 {
		return res.InfConjImageSpaceFNumber
	}
	// No explicit stop: the beam is limited by the smallest fixed surface.
	epd := evaluateEntrancePupilDiameter(surfaces, gc, stopSurface)
	if epd > 0 && math.Abs(res.FocalLength) > 1e-15 {
		return res.FocalLength / epd
	}
	return 0
}

func evaluateEntrancePupilDiameter(surfaces []types.Surface, gc *glass.Catalog, stopSurface int) float64 {
	sys := types.System{Surfaces: surfaces, StopSurface: stopSurface}
	res := paraxial.Compute(sys, types.DefaultWavelength, gc, 0, nil)
	if res.EntrancePupilDiameter > 0 {
		return res.EntrancePupilDiameter
	}
	// No explicit stop: the aperture is the smallest fixed surface.
	r := surface.FixedMinApertureRadius(surfaces)
	if r <= 0 {
		r = surface.MinApertureRadius(surfaces)
	}
	return 2 * r
}

func evaluateSystemLength(surfaces []types.Surface) float64 {
	total := 0.0
	for _, s := range surfaces {
		total += s.Thickness
	}
	return total
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
	return surface.Normal(*surf, p)
}
