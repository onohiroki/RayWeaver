package dls

import (
	"math"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/pupil"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/raymath"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

func TraceFieldGrid(gc *glass.Catalog, surfaces []types.Surface, stopSurface int, pupilZ float64, fieldAngle float64, fieldDir []float64, wavelength float64, apertureMargin float64, numRays int, rotationOffset float64, workers int) ([]IPoint, map[int]float64) {
	skipGlassPath := fieldAngle == 0
	return traceGridRays(gc, surfaces, stopSurface, pupilZ, fieldAngle, fieldDir, wavelength, apertureMargin, numRays, rotationOffset, false, skipGlassPath, workers, types.GridPolar)
}

// TraceFieldGridExtents traces a pupil grid with aperture and glass-path
// checks disabled, returning the true geometric max radial ray extent on each
// surface, independent of any surface aperture clipping. The grid is a dense
// hex pattern so off-axis beam edges (which the polar rings under-sample) are
// resolved; the returned extents therefore match the chief --clear-aperture
// beam envelope.
func TraceFieldGridExtents(gc *glass.Catalog, surfaces []types.Surface, stopSurface int, pupilZ float64, fieldAngle float64, fieldDir []float64, wavelength float64, apertureMargin float64, numRays int, rotationOffset float64, workers int) map[int]float64 {
	_, perSurfMax := traceGridRays(gc, surfaces, stopSurface, pupilZ, fieldAngle, fieldDir, wavelength, apertureMargin, numRays, rotationOffset, true, true, workers, types.GridHex)
	return perSurfMax
}

func traceGridRays(gc *glass.Catalog, surfaces []types.Surface, stopSurface int, pupilZ float64, fieldAngle float64, fieldDir []float64, wavelength float64, apertureMargin float64, numRays int, rotationOffset float64, skipApertureCheck, skipGlassPathCheck bool, workers int, gridType types.GridType) ([]IPoint, map[int]float64) {
	engine := ray.NewEngine(gc, nil)
	p := BuildPath(surfaces)

	rayDir := raymath.DirectionFromField(fieldAngle, fieldDir)

	apertureRadius := ApertureRadiusForGrid(surfaces, stopSurface, wavelength, gc, apertureMargin)
	if apertureRadius <= 0 {
		return nil, nil
	}

	zStart := -100.0
	cx, cy := pupil.GridCentre(rayDir, pupilZ, zStart)

	// Parallel angle-field bundle: the OPL must carry no launch-geometry tilt.
	// pupil.OPLScalar keeps the origins on the zStart plane and subtracts the
	// tilt from the recorded OPL, so the ray positions (and therefore
	// spot_rms / aperture extents) are bit-identical to the unprojected trace
	// while opd_rms still gets the corrected OPL.
	samples := pupil.Launch(pupil.LaunchSpec{
		NumRays:           numRays,
		GridType:          gridType,
		RotationOffset:    rotationOffset,
		ApertureRadius:    apertureRadius,
		RayDir:            rayDir,
		CentreX:           cx,
		CentreY:           cy,
		ZStart:            zStart,
		OPLMode:           pupil.OPLScalar,
		SkipApertureCheck: skipApertureCheck,
		SkipGlassPath:     skipGlassPathCheck,
	})
	pupil.Trace(engine, p, surfaces, samples, wavelength, types.NewCircularJones(true), workers)

	points := make([]IPoint, len(samples))
	perSurfMax := make(map[int]float64)
	for i, s := range samples {
		if !s.OK || len(s.Surfaces) == 0 {
			points[i] = IPoint{OK: false}
			continue
		}
		last := s.Surfaces[len(s.Surfaces)-1]
		points[i] = IPoint{
			X:         last.Position.X,
			Y:         last.Position.Y,
			OPL:       s.OPL,
			OK:        true,
			Area:      s.Area,
			Intensity: s.Intensity,
		}
		for _, sr := range s.Surfaces {
			ax := math.Abs(sr.Position.X)
			ay := math.Abs(sr.Position.Y)
			e := ax
			if ay > e {
				e = ay
			}
			if e > perSurfMax[sr.SurfaceID] {
				perSurfMax[sr.SurfaceID] = e
			}
		}
	}

	return points, perSurfMax
}

const (
	estimatedEPDMargin          = 0.85
	maxEstimatedEPDMultiplier   = 3.0
)

// estimateEntrancePupilDiameterFromFirstSurface returns an estimated
// entrance-pupil diameter as 2*|R| where R is the radius of the first
// surface that carries glass (material != AIR) or is a mirror
// (reflect:true). Plane surfaces (curvature==0) are skipped. When no such
// surface exists the estimate is 0.
func estimateEntrancePupilDiameterFromFirstSurface(surfaces []types.Surface) float64 {
	for _, s := range surfaces {
		if s.Curvature == 0 {
			continue
		}
		isGlass := !s.Material.IsAir()
		if !isGlass && !s.Reflects() {
			continue
		}
		r := s.Radius()
		if r == 0 {
			continue
		}
		return 2 * math.Abs(r)
	}
	return 0
}

func estimateEntrancePupilRadiusFromFirstSurface(surfaces []types.Surface) float64 {
	d := estimateEntrancePupilDiameterFromFirstSurface(surfaces)
	if d <= 0 {
		return 0
	}
	return d / 2
}

// ApertureRadiusForGrid returns the entrance-pupil radius used for grid
// traces. With an explicit stop the radius is the paraxial entrance-pupil
// radius (the stop's image), so the F-number is preserved and image-side fixed
// surfaces that comfortably exceed the local beam do not shrink the pupil.
// Without a stop (dynamic pupil) the radius is the beam-aware cap of the
// auto_aperture:false surfaces: each aperture projected back to the aperture
// position along the paraxial marginal ray, so a surface only caps when its
// clear aperture is smaller than the beam at that surface.
// When EPD is undetermined (no stop, no fixed cap) the fallback is an
// estimate from the first glass/mirror surface radius: 2*|R| with a 0.85
// safety margin, capped at 3x the estimate.
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
	if rEst := estimateEntrancePupilRadiusFromFirstSurface(surfaces); rEst > 0 {
		r := rEst * estimatedEPDMargin
		maxR := rEst * maxEstimatedEPDMultiplier
		if r > maxR {
			r = maxR
		}
		return r
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

// RecommendedThresholds returns the adaptive re-estimation thresholds for a
// given full field of view (degrees, 2*max|angle|). Wide FOV needs fewer rays
// and tighter change detection; narrow FOV needs more rays and looser tolerance.
// The thresholds are variable as requested and mirror the design doc table.
func RecommendedThresholds(fullFOVDeg float64) (minSurviving int, changeRate float64) {
	switch {
	case fullFOVDeg > 60:
		return 80, 0.15 // wide: 50-100 rays, 10-20% change
	case fullFOVDeg >= 30:
		return 180, 0.22 // medium: 128-256 rays, 15-30% change
	default:
		return 320, 0.30 // narrow: 256-512 rays, 20-40% change
	}
}

// MaxFieldOfView returns the full FOV (2*maxAngle) in degrees covering both
// infinite-conjugate (angle) and finite-conjugate (height+object_z) fields.
// For finite conjugate the equivalent angle is atan(|height/objectZ|).
func MaxFieldOfView(fields []types.FieldDef) float64 {
	maxA := 0.0
	for _, f := range fields {
		var a float64
		if math.Abs(f.Height) > 1e-12 && math.Abs(f.ObjectZ) > 1e-12 {
			a = math.Abs(math.Atan2(math.Abs(f.Height), math.Abs(f.ObjectZ))) * 180.0 / math.Pi
		} else {
			a = math.Abs(f.Angle)
		}
		if a > maxA {
			maxA = a
		}
	}
	return 2 * maxA
}

// ReestimateApertureRadiusFromSamples estimates an effective pupil radius from
// surviving samples that reached the image plane. It is the intended
// evaluation-plane re-estimation: trace a fan/grid of diameter 2*|R| (with the
// 0.85 margin), exclude rays that failed with missed_surface / numerical
// instability / glass_path violations (Sample.OK==false), and derive the
// effective clear radius as the maximum pupil radius among survivors.
// When no survivors exist it returns 0. The candidate is clamped to
// 3× the initial geometric estimate to satisfy the "max 3x" requirement.
func ReestimateApertureRadiusFromSamples(samples []pupil.Sample, initialEstimateRadius float64) float64 {
	if initialEstimateRadius <= 0 || len(samples) == 0 {
		return 0
	}
	maxR := 0.0
	has := false
	for _, s := range samples {
		if !s.OK {
			continue
		}
		// Error categories excluded: missed_surface, numerical instability
		// (no intersection / den ~0), glass_path violations — all map to !OK.
		// aperture_stop is also !OK but would have been excluded only if
		// SkipApertureCheck==false; callers tracing for EPD estimation use
		// SkipApertureCheck==false so vignetted rays are excluded as intended.
		r := math.Hypot(s.PupilX, s.PupilY)
		if r > maxR {
			maxR = r
			has = true
		}
	}
	if !has {
		return 0
	}
	maxAllowed := initialEstimateRadius * maxEstimatedEPDMultiplier
	if maxR > maxAllowed {
		maxR = maxAllowed
	}
	return maxR
}

// ClampToMaxEstimate clamps a re-estimated radius to 3× the initial
// geometric estimate (2*|R|/2).
func ClampToMaxEstimate(reestimated, initialEstimateRadius float64) float64 {
	if initialEstimateRadius <= 0 {
		return reestimated
	}
	m := initialEstimateRadius * maxEstimatedEPDMultiplier
	if reestimated > m {
		return m
	}
	return reestimated
}
