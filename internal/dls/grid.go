package dls

import (
	"math"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/raymath"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

func TraceFieldGrid(gc *glass.Catalog, surfaces []types.Surface, stopSurface int, pupilZ float64, fieldAngle float64, fieldDir []float64, wavelength float64, apertureMargin float64, numRays int, rotationOffset float64, workers int) ([]IPoint, map[int]float64) {
	skipGlassPath := fieldAngle == 0
	return traceGridRays(gc, surfaces, stopSurface, pupilZ, fieldAngle, fieldDir, wavelength, apertureMargin, numRays, rotationOffset, false, skipGlassPath, workers)
}

// TraceFieldGridExtents traces a pupil grid with aperture and glass-path
// checks disabled, returning the true geometric max radial ray extent on each
// surface, independent of any surface aperture clipping.
func TraceFieldGridExtents(gc *glass.Catalog, surfaces []types.Surface, stopSurface int, pupilZ float64, fieldAngle float64, fieldDir []float64, wavelength float64, apertureMargin float64, numRays int, rotationOffset float64, workers int) map[int]float64 {
	_, perSurfMax := traceGridRays(gc, surfaces, stopSurface, pupilZ, fieldAngle, fieldDir, wavelength, apertureMargin, numRays, rotationOffset, true, true, workers)
	return perSurfMax
}

func traceGridRays(gc *glass.Catalog, surfaces []types.Surface, stopSurface int, pupilZ float64, fieldAngle float64, fieldDir []float64, wavelength float64, apertureMargin float64, numRays int, rotationOffset float64, skipApertureCheck, skipGlassPathCheck bool, workers int) ([]IPoint, map[int]float64) {
	engine := ray.NewEngine(gc, nil)
	p := BuildPath(surfaces)

	thetaRad := raymath.DegToRad(fieldAngle)
	sinT := math.Sin(thetaRad)
	cosT := math.Cos(thetaRad)

	dx, dy := 0.0, 1.0
	if len(fieldDir) >= 2 {
		norm := math.Hypot(fieldDir[0], fieldDir[1])
		if norm > 0 {
			dx = fieldDir[0] / norm
			dy = fieldDir[1] / norm
		}
	}

	rayDir := types.Vec3{X: sinT * dx, Y: sinT * dy, Z: cosT}.Normalize()

	apertureRadius := ApertureRadiusForGrid(surfaces, stopSurface, wavelength, gc, apertureMargin)
	if apertureRadius <= 0 {
		return nil, nil
	}

	zStart := -100.0
	grid := generatePupilGrid(numRays, apertureRadius, rotationOffset)

	tanComponent := math.Sqrt(rayDir.X*rayDir.X + rayDir.Y*rayDir.Y)
	if rayDir.Z > 1e-12 && tanComponent > 1e-12 {
		tanComponent /= rayDir.Z
		pupilOffsetX := -(pupilZ - zStart) * tanComponent * dx
		pupilOffsetY := -(pupilZ - zStart) * tanComponent * dy
		for i := range grid {
			grid[i].X += pupilOffsetX
			grid[i].Y += pupilOffsetY
		}
	}

	points := make([]IPoint, len(grid))
	perRayMax := make([]map[int]float64, len(grid))

	trace := func(i int) {
		pt := grid[i]
		origin := types.Vec3{X: pt.X, Y: pt.Y, Z: zStart}
		r := types.Ray{
			Wavelength:         wavelength,
			Initial:            types.RayState{Origin: origin, Direction: rayDir},
			Path:               p,
			Jones:              types.NewCircularJones(true),
			SkipGlassPathCheck: skipGlassPathCheck,
			SkipApertureCheck:  skipApertureCheck,
		}

		result := engine.TraceRay(r, surfaces)
		if result.Error != "" {
			points[i] = IPoint{OK: false}
			return
		}

		local := make(map[int]float64, len(result.Surfaces))
		for _, sr := range result.Surfaces {
			ax := math.Abs(sr.Position.X)
			ay := math.Abs(sr.Position.Y)
			e := ax
			if ay > e {
				e = ay
			}
			if e > local[sr.SurfaceID] {
				local[sr.SurfaceID] = e
			}
		}
		perRayMax[i] = local

		if len(result.Surfaces) > 0 {
			last := result.Surfaces[len(result.Surfaces)-1]
			points[i] = IPoint{X: last.Position.X, Y: last.Position.Y, OPL: result.OPLTotal, OK: true}
		} else {
			points[i] = IPoint{OK: false}
		}
	}

	parallelColumns(len(grid), workers, trace)

	perSurfMax := make(map[int]float64)
	for _, local := range perRayMax {
		for id, e := range local {
			if e > perSurfMax[id] {
				perSurfMax[id] = e
			}
		}
	}

	return points, perSurfMax
}

func generatePupilGrid(numRays int, apertureRadius float64, rotationOffset float64) []pupilPoint {
	var pts []pupilPoint
	n := int(math.Sqrt(float64(numRays)))
	if n < 2 {
		n = 2
	}
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			r := (float64(i) + 0.5) / float64(n) * apertureRadius
			theta := 2*math.Pi*(float64(j)+0.5)/float64(n) + rotationOffset
			pts = append(pts, pupilPoint{
				X: r * math.Cos(theta),
				Y: r * math.Sin(theta),
			})
		}
	}
	return pts
}

// ApertureRadiusForGrid returns the entrance-pupil radius used for grid
// traces. With an explicit stop the radius is the paraxial entrance-pupil
// radius (the stop's image), so the F-number is preserved and image-side fixed
// surfaces that comfortably exceed the local beam do not shrink the pupil.
// Without a stop (dynamic pupil) the radius is the beam-aware cap of the
// auto_aperture:false surfaces: each aperture projected back to the aperture
// position along the paraxial marginal ray, so a surface only caps when its
// clear aperture is smaller than the beam at that surface.
func ApertureRadiusForGrid(surfaces []types.Surface, stopSurface int, wavelength float64, gc *glass.Catalog, margin float64) float64 {
	rPar := paraxialEntranceRadius(surfaces, stopSurface, wavelength, gc, margin)
	if stopSurface > 0 && rPar > 0 {
		return rPar
	}
	rFixed := fixedApertureAtPupil(surfaces, wavelength, gc)
	if rFixed > 0 {
		return rFixed
	}
	if rPar > 0 {
		return rPar
	}
	return surface.MinApertureRadius(surfaces)
}

// fixedApertureAtPupil returns the grid-radius cap imposed by the fixed
// (auto_aperture: false) surfaces for a dynamic (stop-free) pupil, with each
// surface's aperture projected back to the aperture position along the
// paraxial marginal ray. The reference marginal height is the tightest fixed
// surface's (the dynamic-pupil aperture position); a surface at marginal
// height y_s caps the grid at r_s * yRef / y_s. Image-side surfaces whose
// clear diameter exceeds the local beam (e.g. a field flattener near the
// image, where the marginal has converged) therefore do not wrongly shrink
// the entrance pupil.
func fixedApertureAtPupil(surfaces []types.Surface, wavelength float64, gc *glass.Catalog) float64 {
	ys := paraxial.MarginalRayHeights(surfaces, wavelength, gc)
	if len(ys) != len(surfaces) {
		return surface.FixedMinApertureRadius(surfaces)
	}

	refIdx := -1
	refR := math.MaxFloat64
	for i, s := range surfaces {
		if s.AutoAperture || s.Diameter <= 0 {
			continue
		}
		if s.Diameter/2 < refR {
			refR = s.Diameter / 2
			refIdx = i
		}
	}
	if refIdx < 0 || math.Abs(ys[refIdx]) < 1e-12 {
		return surface.FixedMinApertureRadius(surfaces)
	}
	yRef := math.Abs(ys[refIdx])

	best := math.MaxFloat64
	for i, s := range surfaces {
		if s.AutoAperture || s.Diameter <= 0 {
			continue
		}
		if i >= len(ys) || math.Abs(ys[i]) < 1e-12 {
			continue
		}
		cap := (s.Diameter / 2) * yRef / math.Abs(ys[i])
		if cap < best {
			best = cap
		}
	}
	if best == math.MaxFloat64 {
		return surface.FixedMinApertureRadius(surfaces)
	}
	return best
}

func paraxialEntranceRadius(surfaces []types.Surface, stopSurface int, wavelength float64, gc *glass.Catalog, margin float64) float64 {
	sys := types.System{Surfaces: surfaces, StopSurface: stopSurface}
	res := paraxial.Compute(sys, wavelength, gc, 0, nil)
	if res.EntrancePupilDiameter > 0 {
		return (res.EntrancePupilDiameter / 2) * margin
	}
	return 0
}
