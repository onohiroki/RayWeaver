package paraxial

import (
	"math"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

type rayState struct {
	Z float64
	Y float64
	U float64
}

func surfacePower(s types.Surface, nBefore, nAfter, R float64) float64 {
	if s.Reflects() {
		return -2.0 * nBefore / R
	}
	return (nAfter - nBefore) / R
}

// traceForward walks the paraxial marginal ray in the unfolded local frame.
// Surfaces are separated by their local path length (thickness), which is
// positive in the fold model; reflections flip the index sign and the power
// formula uses the local (beam-frame) radius. Physical Z is only used for the
// reported vertex positions.
func traceForward(surfaces []types.Surface, nIndex []float64, y0, u0 float64) ([]rayState, rayState) {
	physZ := surface.PhysicalZ(surfaces)
	vertices := make([]rayState, 0, len(surfaces))
	y, u := y0, u0
	for i := 0; i < len(surfaces); i++ {
		nBefore := 1.0
		if i > 0 {
			nBefore = nIndex[i-1]
		}
		nAfter := nIndex[i]
		if surfaces[i].Reflects() {
			nAfter = -nBefore
		}
		R := surfaces[i].ParaxialRadius

		if R != 0 {
			phi := surfacePower(surfaces[i], nBefore, nAfter, R)
			u = (nBefore*u - y*phi) / nAfter
		} else if !surfaces[i].Reflects() {
			u = nBefore * u / nAfter
		}

		vertices = append(vertices, rayState{Z: physZ[i], Y: y, U: u})

		y += surfaces[i].Thickness * u
	}
	final := rayState{Z: surfaces[len(surfaces)-1].PhysicalZ, Y: y, U: u}
	return vertices, final
}

func traceReversed(surfaces []types.Surface, nIndex []float64, y0, u0 float64) ([]rayState, rayState) {
	physZ := surface.PhysicalZ(surfaces)
	vertices := make([]rayState, 0, len(surfaces))
	z := physZ[len(physZ)-1]
	y, u := y0, u0
	for i := len(surfaces) - 1; i >= 0; i-- {
		t := -surfaces[i].Thickness
		y += t * u
		z += t

		nBefore := nIndex[i]
		nAfter := 1.0
		if i > 0 {
			nAfter = nIndex[i-1]
		}
		if surfaces[i].Reflects() {
			nAfter = -nBefore
		}

		R := surfaces[i].ParaxialRadius
		if R != 0 {
			phi := surfacePower(surfaces[i], nBefore, nAfter, R)
			u = (nBefore*u - y*phi) / nAfter
		} else if !surfaces[i].Reflects() {
			u = nBefore * u / nAfter
		}

		vertices = append(vertices, rayState{Z: z, Y: y, U: u})
	}
	final := rayState{Z: z, Y: y, U: u}
	return vertices, final
}

func tracePupilForward(surfaces []types.Surface, nIndex []float64, startIdx int, y0, u0 float64) rayState {
	y, u := y0, u0
	for i := startIdx; i < len(surfaces); i++ {
		nBefore := 1.0
		if i > 0 {
			nBefore = nIndex[i-1]
		}
		nAfter := nIndex[i]
		if surfaces[i].Reflects() {
			nAfter = -nBefore
		}
		R := surfaces[i].ParaxialRadius

		if R != 0 {
			phi := surfacePower(surfaces[i], nBefore, nAfter, R)
			u = (nBefore*u - y*phi) / nAfter
		} else if !surfaces[i].Reflects() {
			u = nBefore * u / nAfter
		}

		y += surfaces[i].Thickness * u
	}
	return rayState{Y: y, U: u}
}

func tracePupilBackward(surfaces []types.Surface, nIndex []float64, startIdx int, y0, u0 float64) rayState {
	// startIdx is the index of the stop surface.
	// We start AT the stop vertex and go backward through surfaces[startIdx-1]..[0].
	y, u := y0, u0
	for i := startIdx - 1; i >= 0; i-- {
		// Undo the transfer from surface[i] to surface[i+1] (which brought us to the stop)
		y = y - surfaces[i].Thickness*u

		// Now undo the refraction at surface[i]
		// The material BEFORE surface[i] (in forward) is nIndex[i-1] or 1.0
		// The material AFTER surface[i] (in forward) is nIndex[i]
		nBefore := nIndex[i] // forward's n_after
		nAfter := 1.0
		if i > 0 {
			nAfter = nIndex[i-1] // forward's n_before
		}
		if surfaces[i].Reflects() {
			nAfter = -nBefore
		}

		R := surfaces[i].ParaxialRadius
		if R != 0 {
			phi := surfacePower(surfaces[i], nBefore, nAfter, R)
			u = (nBefore*u - y*phi) / nAfter
		} else if !surfaces[i].Reflects() {
			u = nBefore * u / nAfter
		}
	}
	return rayState{Y: y, U: u}
}

func stopSurfaceID(surfaces []types.Surface, explicitID int) int {
	if explicitID > 0 {
		for _, s := range surfaces {
			if s.ID == explicitID {
				return explicitID
			}
		}
	}
	return 0
}

func stopSurfaceIndex(surfaces []types.Surface, explicitID int) int {
	stopID := stopSurfaceID(surfaces, explicitID)
	for i, s := range surfaces {
		if s.ID == stopID {
			return i
		}
	}
	return -1
}

func computeStopZ(surfaces []types.Surface, stopID int) float64 {
	if stopID <= 0 {
		stopID = stopSurfaceID(surfaces, 0)
	}
	if stopID == 0 {
		return 0
	}
	for _, s := range surfaces {
		if s.ID == stopID {
			return s.PhysicalZ
		}
	}
	return 0
}

// totalTrack is the physical distance from the first surface vertex to the
// image plane. The image lies at the last surface vertex advanced along its
// local Z by the last thickness; after an odd number of reflections the local
// Z points toward global -Z.
func totalTrack(surfaces []types.Surface) float64 {
	z := surface.PhysicalZ(surfaces)
	if len(z) == 0 {
		return 0
	}
	sign := 1.0
	reflectCount := 0
	for _, s := range surfaces {
		if s.Reflects() {
			reflectCount++
		}
	}
	if reflectCount%2 == 1 {
		sign = -1.0
	}
	imgZ := z[len(z)-1] + surfaces[len(surfaces)-1].Thickness*sign
	return imgZ - z[0]
}

// MarginalRayHeights returns the paraxial marginal-ray height at each
// surface's vertex for a unit-height, infinite-conjugate input ray. The trace
// runs in the unfolded local frame (reflections flip the index sign, not the
// direction of travel), so each value is the beam-radius scale factor of that
// surface relative to the unit-height input.
func MarginalRayHeights(surfaces []types.Surface, wavelength float64, gc *glass.Catalog) []float64 {
	if len(surfaces) == 0 {
		return nil
	}
	nIndex := resolveIndices(surfaces, wavelength, gc)
	vertices, _ := traceForward(surfaces, nIndex, 1.0, 0.0)
	y := make([]float64, len(vertices))
	for i, v := range vertices {
		y[i] = v.Y
	}
	return y
}

func resolveIndices(surfaces []types.Surface, wavelength float64, gc *glass.Catalog) []float64 {
	n := make([]float64, len(surfaces))
	for i, s := range surfaces {
		v, err := gc.RefractiveIndex(s.Material, wavelength)
		if err != nil || v == 0 {
			v = 1.0
		}
		n[i] = v
	}
	for i := range surfaces {
		if !surfaces[i].Reflects() {
			continue
		}
		j := i - 1
		for j >= 0 && surfaces[j].Reflects() {
			j--
		}
		if j < 0 {
			n[i] = 1.0
		} else {
			n[i] = n[j]
		}
	}
	return n
}

func Compute(
	system types.System,
	wavelength float64,
	gc *glass.Catalog,
	objectHeight float64,
	chiefRays []types.ChiefRayResult,
) types.ParaxialResult {
	surfaces := system.Surfaces
	nIndex := resolveIndices(surfaces, wavelength, gc)

	nObj := 1.0
	nImg := 1.0
	if len(surfaces) > 0 {
		nImg = nIndex[len(surfaces)-1]
	}

	tt := totalTrack(surfaces)
	stopIdx := stopSurfaceIndex(surfaces, system.StopSurface)

	var r types.ParaxialResult

	r.TotalTrack = tt
	r.ObjectSpaceIndex = nObj
	r.ImageSpaceIndex = nImg

	// Glass-role classification of every lens element (paraxial.GlassRoles).
	// Computed here, before the early returns of the marginal-ray trace, so the
	// element_roles output is always populated.
	for _, er := range GlassRoles(surfaces, gc) {
		r.ElementRoles = append(r.ElementRoles, types.ElementRole{
			SurfaceIDs: er.SurfaceIDs,
			Phi:        er.Phi,
			Y:          er.Y,
			W:          er.W,
			Role:       er.Role,
			VTarget:    er.VTarget,
			NDTarget:   er.NDTarget,
		})
	}

	// --- Forward marginal ray trace (infinite conjugate) ---
	fwdVerts, _ := traceForward(surfaces, nIndex, 1.0, 0.0)
	if len(fwdVerts) == 0 {
		return r
	}

	// EFL uses the last vertex's angle (image space)
	lastVert := fwdVerts[len(fwdVerts)-1]
	uLast := lastVert.U

	if math.Abs(uLast) > 1e-15 {
		efl := -1.0 / (nImg * uLast)
		r.FocalLength = efl
		r.SecondFocalLength = efl

		// BFL from last LENS surface vertex (skip image plane at end)
		bflIdx := len(fwdVerts) - 1
		for bflIdx > 0 && surfaces[bflIdx].Radius() == 0 && surfaces[bflIdx].Thickness == 0 {
			bflIdx--
		}
		bflVert := fwdVerts[bflIdx]
		bfl := -bflVert.Y / bflVert.U
		r.SecondPrincipalFocus = bfl

		r.SecondPrincipalPoint = bfl - efl
		r.SecondNodalPoint = r.SecondPrincipalPoint
	} else {
		return r
	}

	// --- Reverse marginal ray trace (for front cardinal points) ---
	revVerts, _ := traceReversed(surfaces, nIndex, 1.0, 0.0)
	if len(revVerts) > 0 {
		// Front side: use the first real surface (from the front).
		// revVerts[N-1] corresponds to surfaces[0] (first surface).
		frontIdx := len(revVerts) - 1
		frontVert := revVerts[frontIdx]
		yR, uR := frontVert.Y, frontVert.U

		if math.Abs(uR) > 1e-15 {
			ffl := -1.0 / (nObj * uR)
			fflMag := math.Abs(ffl)
			r.FirstFocalLength = fflMag

			frontFocus := -yR / uR
			r.FirstPrincipalFocus = frontFocus

			r.FirstPrincipalPoint = frontFocus + fflMag
			r.FirstNodalPoint = r.FirstPrincipalPoint
		}
	}

	// --- Entrance pupil ---
	// With an explicit stop, the diameter comes from a paraxial trace through
	// the stop aperture. Without a stop (dynamic-pupil systems), prefer the
	// chief-ray entrance pupil when available; otherwise use the beam-aware
	// fixed-aperture projection to estimate the entrance-pupil diameter
	// internally.
	var chiefPupil *types.Pupil
	for _, cr := range chiefRays {
		if cr.EntrancePupil != nil && cr.EntrancePupil.Radius > 0 {
			r.EntrancePupilLocation = cr.EntrancePupil.Center.Z
			chiefPupil = cr.EntrancePupil
			break
		}
	}

	if stopIdx >= 0 {
		stopR := surfaces[stopIdx].Diameter / 2.0
		if stopR > 0 {
			pupilRay := tracePupilBackward(surfaces, nIndex, stopIdx, 0, 1.0)
			if math.Abs(pupilRay.U) > 1e-15 {
				if chiefPupil == nil {
					r.EntrancePupilLocation = -pupilRay.Y / pupilRay.U
				}
				eRay := tracePupilBackward(surfaces, nIndex, stopIdx, stopR, 0)
				epRad := math.Abs(eRay.Y + eRay.U*(r.EntrancePupilLocation))
				r.EntrancePupilDiameter = 2 * epRad
			}
		}
	} else if chiefPupil != nil {
		r.EntrancePupilDiameter = 2 * chiefPupil.Radius
	} else {
		// Stop-free, no chief pupil: infer from the beam-aware fixed-aperture
		// projection or first-glass/mirror surface estimate.
		if epRad := EntrancePupilRadiusStopFree(surfaces, wavelength, gc); epRad > 0 {
			r.EntrancePupilDiameter = 2 * epRad
			// Use the Z of the tightest fixed aperture as the entrance-pupil location.
			if z := surface.FixedMinApertureRadiusZ(surfaces); z != 0 {
				r.EntrancePupilLocation = z
			}
		}
	}

	if r.EntrancePupilDiameter > 0 && math.Abs(r.FocalLength) > 1e-15 {
		r.InfConjImageSpaceFNumber = r.FocalLength / r.EntrancePupilDiameter
		if r.InfConjImageSpaceFNumber > 0 {
			r.InfConjImageSpaceNA = 1.0 / (2.0 * r.InfConjImageSpaceFNumber)
		}
		r.ImageSpaceFNumber = r.InfConjImageSpaceFNumber
		r.ImageSpaceNA = r.InfConjImageSpaceNA
	}

	// --- Exit pupil ---
	if stopIdx >= 0 {
		stopR := surfaces[stopIdx].Diameter / 2.0
		if stopR > 0 {
			pRay := tracePupilForward(surfaces, nIndex, stopIdx, 0, 1.0)
			if math.Abs(pRay.U) > 1e-15 {
				epLoc := -pRay.Y / pRay.U
				r.ExitPupilLocation = epLoc

				eRay := tracePupilForward(surfaces, nIndex, stopIdx, stopR, 0)
				epRad := math.Abs(eRay.Y + eRay.U*epLoc)
				r.ExitPupilDiameter = 2 * epRad
			}
		}
	}

	// --- Half-angle of view ---
	maxAngle := 0.0
	for _, cr := range chiefRays {
		if cr.FieldAngle > maxAngle {
			maxAngle = cr.FieldAngle
		}
	}
	if maxAngle > 0 {
		r.HalfAngleOfView = maxAngle
	}

	// --- Magnification (finite conjugate) ---
	if objectHeight > 0 {
		magVerts, _ := traceForward(surfaces, nIndex, objectHeight, 0.0)
		if len(magVerts) > 0 {
			yImg := magVerts[len(magVerts)-1].Y
			r.Magnification = yImg / objectHeight
			if math.Abs(r.Magnification) > 1e-15 {
				r.Minification = 1.0 / math.Abs(r.Magnification)
			}
		}
	}

	return r
}

// --- Stop-free entrance-pupil estimation ---

const (
	// EstimatedEPDMargin is the safety margin applied to the first-surface
	// EPD estimate when no stop or fixed aperture is present.
	EstimatedEPDMargin = 0.85
	// MaxEstimatedEPDMultiplier caps the first-surface estimate at this
	// multiple to prevent a wildly wrong aperture.
	MaxEstimatedEPDMultiplier = 3.0
)

// EntrancePupilRadiusStopFree returns the beam-aware entrance-pupil radius for
// a system without an explicit stop surface. It replicates the logic used by
// the chief ray and dls grid traces: (1) project every fixed-surface aperture
// back to the reference marginal-ray height (the tightest fixed surface) and
// take the minimum, or (2) fall back to an estimate from the first
// glass/mirror surface radius. The returned radius is at margin 1.0 (no extra
// safety margin), suitable for both EPD reporting and grid sizing.
func EntrancePupilRadiusStopFree(surfaces []types.Surface, wavelength float64, gc *glass.Catalog) float64 {
	rFixed := fixedApertureAtPupil(surfaces, wavelength, gc)
	if rFixed > 0 {
		return rFixed
	}
	rEst := EstimateEntrancePupilRadiusFromFirstSurface(surfaces)
	if rEst > 0 {
		r := rEst * EstimatedEPDMargin
		maxR := rEst * MaxEstimatedEPDMultiplier
		if r > maxR {
			r = maxR
		}
		return r
	}
	return 0
}

// fixedApertureAtPupil returns the grid-radius cap imposed by the fixed
// (auto_aperture: false) surfaces, with each surface's aperture projected back
// to the aperture position along the paraxial marginal ray. The reference
// marginal height is the tightest fixed surface's (the dynamic-pupil aperture
// position); a surface at marginal height y_s caps the grid at r_s * yRef / y_s.
func fixedApertureAtPupil(surfaces []types.Surface, wavelength float64, gc *glass.Catalog) float64 {
	ys := MarginalRayHeights(surfaces, wavelength, gc)
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

// EstimateEntrancePupilDiameterFromFirstSurface returns an estimated
// entrance-pupil diameter as 2*|R| where R is the radius of the first
// surface that carries glass (material != AIR) or is a mirror
// (reflect:true). Plane surfaces (curvature==0) are skipped. When no such
// surface exists the estimate is 0.
func EstimateEntrancePupilDiameterFromFirstSurface(surfaces []types.Surface) float64 {
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

// EstimateEntrancePupilRadiusFromFirstSurface returns the radius form of the
// first-surface EPD estimate.
func EstimateEntrancePupilRadiusFromFirstSurface(surfaces []types.Surface) float64 {
	d := EstimateEntrancePupilDiameterFromFirstSurface(surfaces)
	if d <= 0 {
		return 0
	}
	return d / 2
}
