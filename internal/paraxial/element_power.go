package paraxial

import (
	"math"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/types"
)

// DLine is the d-line wavelength in mm, used as the default wavelength for
// element-power features.
const DLine = 0.000587562

// isAirMaterial reports whether a surface material is the implicit air/object
// medium (empty, "1", or "air").
func isAirMaterial(m types.Material) bool {
	return m.IsAir() || m.Key == "1"
}

// elementGroups returns the surface-index groups of every lens element in
// system order, sharing the grouping of ElementPowers: a refractive element is
// two consecutive non-air surfaces (a cemented doublet counts as a single
// element), a mirror is a single surface. A trailing lone glass surface (no
// exit surface) is dropped.
func elementGroups(surfaces []types.Surface) [][]int {
	var groups [][]int
	for i := 0; i < len(surfaces); {
		if surfaces[i].Reflects() {
			groups = append(groups, []int{i})
			i++
			continue
		}
		if isAirMaterial(surfaces[i].Material) {
			i++
			continue
		}
		r2 := i + 1
		if r2 >= len(surfaces) {
			break
		}
		groups = append(groups, []int{i, r2})
		i = r2
	}
	return groups
}

// ElementPowers returns the thin-lens power of every lens element in system
// order: the sum of the surface powers of the surfaces bounding each element
// (for a refractive element in air this reduces to (n-1)(c1-c2); a mirror is a
// single-surface element with power -2n/R). Air gaps are skipped. The result
// is empty when the system has no elements.
func ElementPowers(surfaces []types.Surface, wavelength float64, gc *glass.Catalog) []float64 {
	nIndex := resolveIndices(surfaces, wavelength, gc)

	var powers []float64
	for _, g := range elementGroups(surfaces) {
		powers = append(powers, elementPowerAt(surfaces, nIndex, g))
	}
	return powers
}

// elementPowerAt sums the paraxial surface powers of the surfaces in the
// element, using the same nBefore/nAfter convention as the forward trace.
func elementPowerAt(surfaces []types.Surface, nIndex []float64, idx []int) float64 {
	phi := 0.0
	for _, j := range idx {
		nBefore := 1.0
		if j > 0 {
			nBefore = nIndex[j-1]
		}
		nAfter := nIndex[j]
		if surfaces[j].Reflects() {
			nAfter = -nBefore
		}
		R := surfaces[j].ParaxialRadius
		if R == 0 {
			continue
		}
		phi += surfacePower(surfaces[j], nBefore, nAfter, R)
	}
	return phi
}

// ElementPowerForSurface returns the thin-lens power of the element whose
// bounding surfaces include surfaceID, using the same grouping as
// ElementPowers. It returns 0 when the surface is not part of a lens element
// (air gap, object, image plane) or is not found.
func ElementPowerForSurface(surfaces []types.Surface, wavelength float64, gc *glass.Catalog, surfaceID int) float64 {
	nIndex := resolveIndices(surfaces, wavelength, gc)
	for _, g := range elementGroups(surfaces) {
		for _, idx := range g {
			if surfaces[idx].ID == surfaceID {
				return elementPowerAt(surfaces, nIndex, g)
			}
		}
	}
	return 0
}

// ElementPowerCurvature returns the thin-lens power of the element containing
// surfaceID computed directly from the surface curvatures, with no dependence
// on the Precompute-derived ParaxialRadius. It matches ElementPowerForSurface
// once Precompute has refreshed ParaxialRadius, and matches the power the
// power-preserving solve (SolveElementPower) preserves. Use it when the
// surfaces have not been through Precompute (e.g. a target snapshot taken at
// Optimizer construction).
func ElementPowerCurvature(surfaces []types.Surface, gc *glass.Catalog, surfaceID int) float64 {
	nIndex := resolveIndices(surfaces, DLine, gc)
	for _, g := range elementGroups(surfaces) {
		found := false
		for _, j := range g {
			if surfaces[j].ID == surfaceID {
				found = true
				break
			}
		}
		if !found {
			continue
		}
		phi := 0.0
		for _, j := range g {
			nBefore := 1.0
			if j > 0 {
				nBefore = nIndex[j-1]
			}
			nAfter := nIndex[j]
			if surfaces[j].Reflects() {
				nAfter = -nBefore
			}
			phi += (nAfter - nBefore) * surfaces[j].Curvature
		}
		return phi
	}
	return 0
}

// SolveElementPower adjusts the curvature of the surface whose ID is
// solveSurfaceID so that the thin-lens power of the element containing it
// equals targetPhi. The solve is exact for the paraxial model: the element
// power is the sum of the surface powers, and only the solve surface's
// curvature is free, so
//
//	targetPhi = K + (nAfter - nBefore) * c_solve
//
// where K is the sum over the element's other surfaces. The solve surface's
// Curvature is written in place; the caller must run surface.Precompute to
// refresh ParaxialRadius from the new curvature. It returns false when the
// surface is not part of a refractive lens element, or the solve coefficient
// (index contrast on the solve surface) is zero (e.g. a mirror). Mirrors are
// skipped: their power is not driven by a dispersion.
func SolveElementPower(surfaces []types.Surface, gc *glass.Catalog, solveSurfaceID int, targetPhi float64) bool {
	nIndex := resolveIndices(surfaces, DLine, gc)
	for _, g := range elementGroups(surfaces) {
		solveIdx := -1
		for _, j := range g {
			if surfaces[j].ID == solveSurfaceID {
				solveIdx = j
				break
			}
		}
		if solveIdx < 0 {
			continue
		}
		if surfaces[solveIdx].Reflects() {
			return false
		}
		// Sum the power of the element's other surfaces, using the current
		// curvature directly (ParaxialRadius may be stale when a curvature
		// variable was just applied).
		K := 0.0
		for _, j := range g {
			if j == solveIdx {
				continue
			}
			nBefore := 1.0
			if j > 0 {
				nBefore = nIndex[j-1]
			}
			nAfter := nIndex[j]
			if surfaces[j].Reflects() {
				nAfter = -nBefore
			}
			K += (nAfter - nBefore) * surfaces[j].Curvature
		}
		nBefore := 1.0
		if solveIdx > 0 {
			nBefore = nIndex[solveIdx-1]
		}
		nAfter := nIndex[solveIdx]
		coeff := nAfter - nBefore
		if math.Abs(coeff) < 1e-12 {
			return false
		}
		surfaces[solveIdx].Curvature = (targetPhi - K) / coeff
		return true
	}
	return false
}
