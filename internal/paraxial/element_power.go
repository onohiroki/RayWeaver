package paraxial

import (
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

// ElementPowers returns the thin-lens power of every lens element in system
// order: the sum of the surface powers of the surfaces bounding each element
// (for a refractive element in air this reduces to (n-1)(c1-c2); a mirror is a
// single-surface element with power -2n/R). Air gaps are skipped. The result
// is empty when the system has no elements.
func ElementPowers(surfaces []types.Surface, wavelength float64, gc *glass.Catalog) []float64 {
	nIndex := resolveIndices(surfaces, wavelength, gc)

	var powers []float64
	for i := 0; i < len(surfaces); {
		if surfaces[i].Reflects() {
			powers = append(powers, elementPowerAt(surfaces, nIndex, []int{i}))
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
		powers = append(powers, elementPowerAt(surfaces, nIndex, []int{i, r2}))
		i = r2
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
	for i := 0; i < len(surfaces); {
		if surfaces[i].Reflects() {
			if surfaces[i].ID == surfaceID {
				return elementPowerAt(surfaces, nIndex, []int{i})
			}
			i++
			continue
		}
		if isAirMaterial(surfaces[i].Material) {
			i++
			continue
		}
		r2 := i + 1
		if r2 >= len(surfaces) {
			if surfaces[i].ID == surfaceID {
				return elementPowerAt(surfaces, nIndex, []int{i})
			}
			break
		}
		if surfaces[i].ID == surfaceID || surfaces[r2].ID == surfaceID {
			return elementPowerAt(surfaces, nIndex, []int{i, r2})
		}
		i = r2
	}
	return 0
}
