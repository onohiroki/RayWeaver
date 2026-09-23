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

func TraceFieldGrid(gc *glass.Catalog, surfaces []types.Surface, stopSurface int, pupilZ float64, fieldAngle float64, fieldDir []float64, wavelength float64, apertureMargin float64, numRays int, rotationOffset float64, workers int, epdOverride float64) ([]IPoint, map[int]float64) {
	skipGlassPath := fieldAngle == 0
	return traceGridRays(gc, surfaces, stopSurface, pupilZ, fieldAngle, fieldDir, wavelength, apertureMargin, numRays, rotationOffset, false, skipGlassPath, workers, types.GridPolar, epdOverride)
}

// TraceFieldGridExtents traces a pupil grid with aperture and glass-path
// checks disabled, returning the true geometric max radial ray extent on each
// surface, independent of any surface aperture clipping. The grid is a dense
// hex pattern so off-axis beam edges (which the polar rings under-sample) are
// resolved; the returned extents therefore match the chief --clear-aperture
// beam envelope.
func TraceFieldGridExtents(gc *glass.Catalog, surfaces []types.Surface, stopSurface int, pupilZ float64, fieldAngle float64, fieldDir []float64, wavelength float64, apertureMargin float64, numRays int, rotationOffset float64, workers int, epdOverride float64) map[int]float64 {
	_, perSurfMax := traceGridRays(gc, surfaces, stopSurface, pupilZ, fieldAngle, fieldDir, wavelength, apertureMargin, numRays, rotationOffset, true, true, workers, types.GridHex, epdOverride)
	return perSurfMax
}

// TraceFieldExtents8Rays measures the per-surface max radial ray extent for
// one field using 8 rays from the entrance pupil: 4 cardinal
// (top/bottom/left/right) and 4 diagonal (45°). The entrance pupil position
// (dynamic pupil Z) and diameter (paraxial EPD) fully determine the ray
// origins and directions. Aperture and glass-path checks are disabled so the
// true geometric beam envelope is measured independent of surface clipping.
func TraceFieldExtents8Rays(gc *glass.Catalog, surfaces []types.Surface, stopSurface int, pupilZ float64, fieldAngle float64, fieldDir []float64, wavelength float64, apertureMargin float64, workers int, epdOverride float64) map[int]float64 {
	engine := ray.NewEngine(gc, nil)
	p := BuildPath(surfaces)

	rayDir := raymath.DirectionFromField(fieldAngle, fieldDir)

	apertureRadius := ApertureRadiusForGrid(surfaces, stopSurface, wavelength, gc, apertureMargin, epdOverride)
	if apertureRadius <= 0 {
		return nil
	}

	zStart := -100.0
	cx, cy := pupil.GridCentre(rayDir, pupilZ, zStart)
	wavefrontC := types.Vec3{X: cx, Y: cy, Z: zStart}

	r := apertureRadius
	d := r * math.Sqrt(2) / 2
	type card struct{ px, py float64 }
	cards := [8]card{
		{0, +r},
		{0, -r},
		{+r, 0},
		{-r, 0},
		{+d, +d},
		{-d, +d},
		{+d, -d},
		{-d, -d},
	}

	samples := make([]pupil.Sample, 8)
	for i, c := range cards {
		origin := types.Vec3{X: cx + c.px, Y: cy + c.py, Z: zStart}
		samples[i] = pupil.Sample{
			PupilX:             c.px,
			PupilY:             c.py,
			Area:               r,
			Origin:             origin,
			Dir:                rayDir,
			OPLDelta:           wavefrontC.Subtract(origin).Dot(rayDir),
			SkipApertureCheck:  true,
			SkipGlassPathCheck: true,
		}
	}

	pupil.Trace(engine, p, surfaces, samples, wavelength, types.NewCircularJones(true), workers)

	perSurfMax := make(map[int]float64)
	for _, s := range samples {
		if !s.OK {
			continue
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
	return perSurfMax
}

func traceGridRays(gc *glass.Catalog, surfaces []types.Surface, stopSurface int, pupilZ float64, fieldAngle float64, fieldDir []float64, wavelength float64, apertureMargin float64, numRays int, rotationOffset float64, skipApertureCheck, skipGlassPathCheck bool, workers int, gridType types.GridType, epdOverride float64) ([]IPoint, map[int]float64) {
	engine := ray.NewEngine(gc, nil)
	p := BuildPath(surfaces)

	rayDir := raymath.DirectionFromField(fieldAngle, fieldDir)

	apertureRadius := ApertureRadiusForGrid(surfaces, stopSurface, wavelength, gc, apertureMargin, epdOverride)
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
			PupilX:    s.PupilX,
			PupilY:    s.PupilY,
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

func estimateEntrancePupilRadiusFromFirstSurface(surfaces []types.Surface) float64 {
	return paraxial.EstimateEntrancePupilRadiusFromFirstSurface(surfaces)
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
func ApertureRadiusForGrid(surfaces []types.Surface, stopSurface int, wavelength float64, gc *glass.Catalog, margin float64, epdOverride float64) float64 {
	if epdOverride > 0 {
		return (epdOverride / 2) * margin
	}
	rPar := paraxial.EntrancePupilRadius(surfaces, stopSurface, wavelength, gc) * margin
	if stopSurface > 0 && rPar > 0 {
		return rPar
	}
	if rPar > 0 {
		return rPar
	}
	rFixed := paraxial.EntrancePupilRadiusStopFree(surfaces, wavelength, gc)
	if rFixed > 0 {
		return rFixed
	}
	rEst := estimateEntrancePupilRadiusFromFirstSurface(surfaces)
	if rEst > 0 {
		r := rEst * paraxial.EstimatedEPDMargin
		maxR := rEst * paraxial.MaxEstimatedEPDMultiplier
		if r > maxR {
			r = maxR
		}
		return r
	}
	return surface.MinApertureRadius(surfaces)
}
