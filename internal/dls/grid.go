package dls

import (
	"math"
	"sort"

	"github.com/hiroki/rayweaver/internal/feasibility"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/pupil"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/raymath"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// TraceFieldGrid traces one field's entrance-pupil grid. vig is the field's
// declared vignetting ellipse (nil = no clip), applied on the grid exactly as
// the chief / wavefront / psf commands apply it, so the merit measures the
// delivered vignetted pupil. The glass-path (edge-thickness) check is skipped
// for the on-axis field, which has no off-axis edge to protect.
func TraceFieldGrid(gc *glass.Catalog, surfaces []types.Surface, stopSurface int, pupilZ float64, fieldAngle float64, fieldDir []float64, wavelength float64, apertureMargin float64, numRays int, rotationOffset float64, workers int, epdOverride float64, vig *types.VignettingDef) ([]IPoint, map[int]float64) {
	return TraceFieldGridWithType(gc, surfaces, stopSurface, pupilZ, fieldAngle, fieldDir, wavelength, apertureMargin, numRays, rotationOffset, workers, epdOverride, vig, types.GridPolar)
}

// TraceFieldGridWithType is TraceFieldGrid with an explicit pupil-grid pattern.
// The merit path uses GridPolar; a feasibility gate that must agree with the
// delivered-design check (`chief` / `zoom`, GridHex) uses GridHex.
func TraceFieldGridWithType(gc *glass.Catalog, surfaces []types.Surface, stopSurface int, pupilZ float64, fieldAngle float64, fieldDir []float64, wavelength float64, apertureMargin float64, numRays int, rotationOffset float64, workers int, epdOverride float64, vig *types.VignettingDef, gridType types.GridType) ([]IPoint, map[int]float64) {
	skipGlassPath := fieldAngle == 0
	return traceGridRays(gc, surfaces, stopSurface, pupilZ, fieldAngle, fieldDir, wavelength, apertureMargin, numRays, rotationOffset, false, false, skipGlassPath, false, workers, gridType, epdOverride, vig)
}

// PrescribedSurvival returns the fraction of the field's full prescribed
// entrance-pupil grid that reaches the image plane (IPoint.OK).
//
// It is the one vig number every consumer should agree on: the hex lattice
// chief / zoom report, no declared vignetting ellipse (the ellipse is an input
// mask on the merit grid, not a second definition of vig), and margin 1.0 so
// the denominator is the physical pupil rather than the optimizer's inflated
// launch. The optimizer's vignetting_factor constraint, its ray_validity gate
// and the escape throughput check all use it; a spot or wavefront term keeps
// using TraceFieldGrid with the field's ellipse.
func PrescribedSurvival(gc *glass.Catalog, surfaces []types.Surface, stopSurface int, pupilZ, fieldAngle float64, fieldDir []float64, wavelength float64, numRays int, rotationOffset float64, workers int, epdOverride float64) float64 {
	ratio, _ := PrescribedSurvivalPoints(gc, surfaces, stopSurface, pupilZ, fieldAngle, fieldDir, wavelength, numRays, rotationOffset, workers, epdOverride)
	return ratio
}

// PrescribedSurvivalPoints is PrescribedSurvival with the number of launched
// grid points, so a caller can report the sample size next to the fraction.
func PrescribedSurvivalPoints(gc *glass.Catalog, surfaces []types.Surface, stopSurface int, pupilZ, fieldAngle float64, fieldDir []float64, wavelength float64, numRays int, rotationOffset float64, workers int, epdOverride float64) (float64, int) {
	points, _ := traceGridRays(gc, surfaces, stopSurface, pupilZ, fieldAngle, fieldDir, wavelength, 1.0, numRays, rotationOffset, false, false, false, false, workers, types.GridHex, epdOverride, nil)
	return SurvivingFraction(points), len(points)
}

// SurvivingFraction returns the fraction of a traced grid whose rays reached
// the image plane. An empty grid counts as fully dead (0), so "no grid" and
// "no survivors" are treated alike.
func SurvivingFraction(points []IPoint) float64 {
	if len(points) == 0 {
		return 0
	}
	n := 0
	for _, q := range points {
		if q.OK {
			n++
		}
	}
	return float64(n) / float64(len(points))
}

// TraceFieldGridExtents traces a pupil grid with the auto-aperture check
// disabled (and the fixed apertures, stop and glass path kept closed),
// returning the max radial ray extent on each surface: the beam the delivered
// lens actually passes, measured in the surface-local frame the tracer clips
// on. The grid is a dense hex pattern so off-axis beam edges (which the polar
// rings under-sample) are resolved. A ray that fails later in the path still
// contributes every surface it reached (see surfaceExtents), so an undersized
// aperture downstream does not starve the surfaces before it.
//
// The fixed (auto_aperture: false) apertures stay closed on purpose: they are
// the user's prescription (a stop inside the lens bounds the beam), and a ray
// blocked there is not part of the delivered beam further on. Skipping every
// aperture made this measurement larger than chief.BeamEnvelope and than the
// optimizer's own final sizing, so the three tools settled different
// diameters (measured on the 50 mm f/4 sample: 15.6 mm final vs a 19.8 mm
// "need" on the same surface).
func TraceFieldGridExtents(gc *glass.Catalog, surfaces []types.Surface, stopSurface int, pupilZ float64, fieldAngle float64, fieldDir []float64, wavelength float64, apertureMargin float64, numRays int, rotationOffset float64, workers int, epdOverride float64) map[int]float64 {
	return TraceFieldGridExtentsVignetted(gc, surfaces, stopSurface, pupilZ, fieldAngle, fieldDir, wavelength, apertureMargin, numRays, rotationOffset, workers, epdOverride, nil)
}

// TraceFieldGridExtentsVignetted is TraceFieldGridExtents with the field's
// prescribed (vignetted) pupil: when vig is set the launch is clipped to the
// vignetting ellipse, so the returned extents describe the beam the merit grid
// actually traces. Measuring a deliberately vignetted field against the full
// aperture would report the prescription itself as clipping.
func TraceFieldGridExtentsVignetted(gc *glass.Catalog, surfaces []types.Surface, stopSurface int, pupilZ float64, fieldAngle float64, fieldDir []float64, wavelength float64, apertureMargin float64, numRays int, rotationOffset float64, workers int, epdOverride float64, vig *types.VignettingDef) map[int]float64 {
	return traceFieldGridExtents(gc, surfaces, stopSurface, pupilZ, fieldAngle, fieldDir, wavelength, apertureMargin, numRays, rotationOffset, workers, epdOverride, vig, false)
}

// TraceFieldGridExtentsRelaxed is TraceFieldGridExtentsVignetted with a
// virtual extension past a surface the ray misses: the ray is advanced to that
// surface's vertex tangent plane and the surfaces beyond it are measured too.
// It is a feasibility surrogate used by the beam_clearance merit kind to give
// a slope where the physical trace has no beam at all. Sizing, the clipping
// warning and the beam_clearance constraint keep using the physical envelope:
// a ray that misses a surface entirely is not part of the delivered beam, and
// extending it can only inflate the envelope.
func TraceFieldGridExtentsRelaxed(gc *glass.Catalog, surfaces []types.Surface, stopSurface int, pupilZ float64, fieldAngle float64, fieldDir []float64, wavelength float64, apertureMargin float64, numRays int, rotationOffset float64, workers int, epdOverride float64, vig *types.VignettingDef) map[int]float64 {
	return traceFieldGridExtents(gc, surfaces, stopSurface, pupilZ, fieldAngle, fieldDir, wavelength, apertureMargin, numRays, rotationOffset, workers, epdOverride, vig, true)
}

func traceFieldGridExtents(gc *glass.Catalog, surfaces []types.Surface, stopSurface int, pupilZ float64, fieldAngle float64, fieldDir []float64, wavelength float64, apertureMargin float64, numRays int, rotationOffset float64, workers int, epdOverride float64, vig *types.VignettingDef, virtualMiss bool) map[int]float64 {
	_, perSurfMax := traceGridRays(gc, surfaces, stopSurface, pupilZ, fieldAngle, fieldDir, wavelength, apertureMargin, numRays, rotationOffset, false, true, false, virtualMiss, workers, types.GridHex, epdOverride, vig)
	return perSurfMax
}

// TraceFieldExtents8Rays measures the per-surface max radial ray extent for
// one field using 8 rays from the entrance pupil: 4 cardinal
// (top/bottom/left/right) and 4 diagonal (45°). The entrance pupil position
// (dynamic pupil Z) and diameter (paraxial EPD) fully determine the ray
// origins and directions. Aperture and glass-path checks are disabled so the
// measurement is a conservative, unclipped geometric envelope: the surfaces
// being sized never clip their own measurement, and a fixed aperture inside
// the lens cannot truncate the bundle before the surfaces it is supposed to
// protect. That over-sizes rather than under-sizes, which is what a
// per-evaluation sizing needs (an undersized auto aperture clips the merit
// grid and kills a field). The final, delivered diameters come from the
// fixed-aperture-respecting hex envelope (chief --clear-aperture /
// FinalApertures); a ray that fails later still contributes every surface it
// reached (see surfaceExtents).
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

	perSurfMax := surfaceExtents(samples, surfaces)
	pupil.Release(samples)
	return perSurfMax
}

// surfaceExtents reduces traced samples to the maximum radial ray height on
// each surface. It reads every per-surface result a ray produced, including the
// prefix of a ray that later failed: the beam at those surfaces is real, and
// discarding it would leave a broken design with no measurement at all on the
// surfaces it already reached. A Missed entry is skipped because the tracer
// records it at the previous surface's position (or with no hit point); the one
// exception is an aperture clip, which is recorded at the real hit point: the
// beam reached that surface, and its height is exactly the overfill the
// aperture has to cover. A Virtual hit is a real point on the extended ray and
// is measured.
//
// The height is measured in the surface-local frame, which is the frame the
// aperture check uses (ray.TraceRay: h = sqrt(x^2 + y^2) against diameter/2).
// A global max(|X|, |Y|) is a bounding-box measure and overstates the margin
// of an off-axis, tilted or decentered bundle.
func surfaceExtents(samples []pupil.Sample, surfaces []types.Surface) map[int]float64 {
	byID := make(map[int]types.Surface, len(surfaces))
	for _, s := range surfaces {
		byID[s.ID] = s
	}
	perSurfMax := make(map[int]float64)
	for i := range samples {
		for _, sr := range samples[i].Surfaces {
			if sr.Interaction == types.Missed && sr.ErrorCode != string(ray.ErrApertureStop) {
				continue
			}
			s, ok := byID[sr.SurfaceID]
			if !ok {
				continue
			}
			local := s.GlobalToLocal.MultiplyPoint(sr.Position)
			h := math.Hypot(local.X, local.Y)
			if h > perSurfMax[sr.SurfaceID] {
				perSurfMax[sr.SurfaceID] = h
			}
		}
	}
	return perSurfMax
}

// UnreachedClearance is the clearance a caller should use for a surface no
// traced ray reached. It is negative, so an inequality-lower bound treats the
// absent measurement as a violation instead of reading it as the safest
// surface; a caller that needs the distinction can use BeamClearance directly.
const UnreachedClearance = -1.0 // mm

// BeamClearance returns surfaceRadius - radial beam extent for one surface.
// reached is false when the traced samples produced no hit on that surface (a
// missed surface, total internal reflection before it, or a grid that could not
// be launched), so the caller can apply the "absence is a violation" policy.
func BeamClearance(extents map[int]float64, surfaces []types.Surface, surfaceID int) (clearance float64, reached bool) {
	for _, s := range surfaces {
		if s.ID != surfaceID {
			continue
		}
		e, ok := extents[surfaceID]
		if !ok {
			return 0, false
		}
		return s.Diameter/2 - e, true
	}
	return 0, false
}

// maxNodeSolves bounds the per-call node solves in the relaxed extents trace:
// the eight outermost missed rays dominate the envelope, and solving more
// would multiply the cost of every merit evaluation (and every finite-
// difference Jacobian column) without moving the measured envelope.
const maxNodeSolves = 8

// solveMissedNodes replaces the virtual (vertex-plane) hits of the outermost
// missed rays with a node-solved physical path when one exists. The node solve
// is the feasibility Layer S fail-over: it turns "this ray has no beam past the
// surface" into an actual path, or leaves the virtual fallback when the least-
// squares path does not converge. It is bounded to maxNodeSolves per call.
func solveMissedNodes(samples []pupil.Sample, surfaces []types.Surface, wavelength float64, gc *glass.Catalog) {
	type candidate struct {
		index  int
		radius float64
	}
	var candidates []candidate
	for i := range samples {
		if hasVirtualHit(samples[i].Surfaces) {
			candidates = append(candidates, candidate{i, math.Hypot(samples[i].PupilX, samples[i].PupilY)})
		}
	}
	if len(candidates) == 0 {
		return
	}
	sort.Slice(candidates, func(a, b int) bool { return candidates[a].radius > candidates[b].radius })
	if len(candidates) > maxNodeSolves {
		candidates = candidates[:maxNodeSolves]
	}
	path := BuildPath(surfaces)
	for _, c := range candidates {
		s := &samples[c.index]
		np := feasibility.SolveNodes(surfaces, path,
			types.RayState{Origin: s.Origin, Direction: s.Dir},
			wavelength, gc,
			feasibility.SolveOptions{MaxIter: 20, Tolerance: 1e-7, Initial: initialPath(*s, path)},
		)
		if !np.Converged {
			continue
		}
		byID := make(map[int]types.Vec3, len(np.Surfaces))
		for k, id := range np.Surfaces {
			byID[id] = np.Points[k]
		}
		for k := range s.Surfaces {
			sr := &s.Surfaces[k]
			p, ok := byID[sr.SurfaceID]
			if !ok {
				continue
			}
			sr.Position = p
			sr.Interaction = types.Transmit
			if idx := SurfaceIndex(surfaces, sr.SurfaceID); idx >= 0 && surfaces[idx].Reflects() {
				sr.Interaction = types.Reflect
			}
			sr.ErrorCode = ""
		}
	}
}

func hasVirtualHit(results []types.SurfaceResult) bool {
	for _, sr := range results {
		if sr.Interaction == types.Virtual {
			return true
		}
	}
	return false
}

// initialPath builds the node solver's warm start from the trace's per-surface
// positions (a virtual hit is still a point on the extended ray). It returns
// nil when a path surface has no result, which makes SolveNodes fall back to
// its launch-line initial guess.
func initialPath(s pupil.Sample, path []int) []types.Vec3 {
	byID := make(map[int]types.Vec3, len(s.Surfaces))
	for _, sr := range s.Surfaces {
		if sr.Interaction == types.Missed {
			continue
		}
		byID[sr.SurfaceID] = sr.Position
	}
	out := make([]types.Vec3, 0, len(path)-1)
	for _, id := range path {
		if id == 0 {
			continue
		}
		p, ok := byID[id]
		if !ok {
			return nil
		}
		out = append(out, p)
	}
	return out
}

func traceGridRays(gc *glass.Catalog, surfaces []types.Surface, stopSurface int, pupilZ float64, fieldAngle float64, fieldDir []float64, wavelength float64, apertureMargin float64, numRays int, rotationOffset float64, skipApertureCheck, skipAutoApertureCheck, skipGlassPathCheck, virtualMiss bool, workers int, gridType types.GridType, epdOverride float64, vig *types.VignettingDef) ([]IPoint, map[int]float64) {
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
		NumRays:               numRays,
		GridType:              gridType,
		RotationOffset:        rotationOffset,
		ApertureRadius:        apertureRadius,
		RayDir:                rayDir,
		CentreX:               cx,
		CentreY:               cy,
		ZStart:                zStart,
		OPLMode:               pupil.OPLScalar,
		SkipApertureCheck:     skipApertureCheck,
		SkipGlassPath:         skipGlassPathCheck,
		SkipAutoApertureCheck: skipAutoApertureCheck,
		VirtualMiss:           virtualMiss,
		Vig:                   vig,
	})
	pupil.Trace(engine, p, surfaces, samples, wavelength, types.NewCircularJones(true), workers)
	if virtualMiss {
		solveMissedNodes(samples, surfaces, wavelength, gc)
	}

	points := make([]IPoint, len(samples))
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
	}
	perSurfMax := surfaceExtents(samples, surfaces)
	pupil.Release(samples)

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
