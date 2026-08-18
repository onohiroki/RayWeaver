package paraxial

import (
	"math"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/types"
)

// Glass-role tuning constants for the role-based glass_role merit kind. The
// role of a lens element is judged by its chromatic weight w = phi·y² (the
// thin-lens power times the squared paraxial marginal-ray height — the Seidel
// axial-colour weight), compared against the opposite-chromatic-sign
// neighbours in element order: the element with the larger |w| is the couple's
// crown (high vd, bears the net power) and the smaller |w| the flint (low vd,
// corrects the colour), regardless of the sign of the power. A positive-power
// element can therefore be a flint (the double-Gauss "positive flint") and a
// negative-power element a crown (a negative relay/Barlow group) — cases the
// fixed sign-based rule vd = 45 + 16·tanh(phi) cannot express.
const (
	// glassRoleCrownRefV is the target Abbe number of the power-bearing member
	// of an achromatic couple (N-BK7/SK-like). The compensating member's target
	// follows from the couple achromatism w_c/V_c + w_f/V_f = 0, i.e.
	// V_f = V_c·|w_f|/|w_c|, clamped below.
	glassRoleCrownRefV = 60.0
	// glassRoleVDNeutral is the target for an element with no opposite-sign
	// neighbour or a negligible chromatic weight (near the stop / image plane):
	// it is never forced to an extreme glass.
	glassRoleVDNeutral = 45.0
	// glassRoleVDMin clamps the flint-side target so the derived Abbe number
	// stays inside the physical glass map.
	glassRoleVDMin = 20.0
	// glassRoleVDMax bounds the neutral/dominant targets.
	glassRoleVDMax = 90.0
	// glassRoleNeutralFraction is the fraction of the couple's total |w| below
	// which an element is treated as chromatically neutral.
	glassRoleNeutralFraction = 0.05
	// glassRoleNDLineA/glassRoleNDLineB define the "normal glass line"
	// nd ≈ 1.635 − 0.0025·(vd − 50) used to derive a role-consistent nd target
	// from the vd target.
	glassRoleNDLineA = 1.635
	glassRoleNDLineB = -0.0025
	// glassRoleNDBoostPos lifts the nd target of positive-chromatic-weight
	// elements onto the lanthanum-crown side of the line: a high-nd crown
	// flattens the Petzval sum and reduces the surface bending of a positive
	// power-bearer.
	glassRoleNDBoostPos = 0.04
	glassRoleNDMin      = 1.4
	glassRoleNDMax      = 2.0
)

// ElementRole classifies one lens element's glass role: the derived vd/nd
// targets of the achromatic couple it belongs to.
type ElementRole struct {
	SurfaceIDs []int   `yaml:"surface_ids,omitempty"`
	Phi        float64 `yaml:"phi"`
	Y          float64 `yaml:"y"`
	W          float64 `yaml:"w"`
	Role       string  `yaml:"role"` // dominant | compensating | neutral
	VTarget    float64 `yaml:"vd_target"`
	NDTarget   float64 `yaml:"nd_target"`
}

// GlassRoles returns the glass-role classification of every lens element in
// system order (same grouping as ElementPowers): the chromatic weight
// w = phi·y² of each element, its partner's combined weight W_opp over the
// adjacent opposite-chromatic-sign elements, and the vd/nd targets derived
// from the couple achromatism w_c/V_c + w_f/V_f = 0. The marginal-ray height
// y is the unit-height infinite-conjugate value (MarginalRayHeights), so a
// finite-conjugate system gets an approximate — but consistent — weighting.
func GlassRoles(surfaces []types.Surface, gc *glass.Catalog) []ElementRole {
	groups := elementGroups(surfaces)
	if len(groups) == 0 {
		return nil
	}
	nIndex := resolveIndices(surfaces, DLine, gc)
	ys := MarginalRayHeights(surfaces, DLine, gc)
	roles := make([]ElementRole, len(groups))
	w := make([]float64, len(groups))
	for gi, g := range groups {
		phi := elementPowerAt(surfaces, nIndex, g)
		y := 0.0
		if g[0] < len(ys) {
			y = ys[g[0]]
		}
		roles[gi] = ElementRole{
			SurfaceIDs: surfaceIDsOfGroup(surfaces, g),
			Phi:        phi,
			Y:          y,
			W:          phi * y * y,
		}
		w[gi] = roles[gi].W
	}

	// W_opp per element: the combined |w| of the adjacent elements with the
	// opposite chromatic sign (a Cooke-triplet middle element pairs with both
	// outer elements).
	wopp := make([]float64, len(groups))
	for i := range groups {
		for _, j := range neighborIndices(i, len(groups)) {
			if w[i]*w[j] < 0 {
				wopp[i] += math.Abs(w[j])
			}
		}
	}

	for i := range roles {
		role := &roles[i]
		wi := math.Abs(w[i])
		switch {
		case wopp[i] == 0 || wi < glassRoleNeutralFraction*(wi+wopp[i]):
			// No chromatic partner, or a negligible weight: the element's glass
			// has no paired colour role — keep it near the neutral centre.
			role.Role = "neutral"
			role.VTarget = glassRoleVDNeutral
		case wi > wopp[i]:
			// The couple's power-bearer: the crown of the pair, whatever the
			// sign of its power.
			role.Role = "dominant"
			role.VTarget = glassRoleCrownRefV
		default:
			// The couple's chromatic corrector: the flint, its vd derived from
			// the achromatism ratio V_f = V_c·|w_f|/|w_c|.
			role.Role = "compensating"
			role.VTarget = clampF(glassRoleCrownRefV*wi/wopp[i], glassRoleVDMin, glassRoleCrownRefV)
		}
		boost := 0.0
		if w[i] > 0 {
			boost = glassRoleNDBoostPos
		}
		role.NDTarget = clampF(ndLine(role.VTarget)+boost, glassRoleNDMin, glassRoleNDMax)
	}
	return roles
}

// ElementRoleForSurface returns the glass-role classification of the element
// whose bounding surfaces include the given surface ID.
func ElementRoleForSurface(surfaces []types.Surface, gc *glass.Catalog, surfaceID int) (ElementRole, bool) {
	for _, r := range GlassRoles(surfaces, gc) {
		for _, id := range r.SurfaceIDs {
			if id == surfaceID {
				return r, true
			}
		}
	}
	return ElementRole{}, false
}

// ndLine returns the "normal glass line" nd at the given Abbe number.
func ndLine(vd float64) float64 {
	return glassRoleNDLineA + glassRoleNDLineB*(vd-50.0)
}

// neighborIndices returns the in-range adjacent element indices of i.
func neighborIndices(i, n int) []int {
	var out []int
	if i-1 >= 0 {
		out = append(out, i-1)
	}
	if i+1 < n {
		out = append(out, i+1)
	}
	return out
}

// surfaceIDsOfGroup returns the surface IDs of the surfaces in the group.
func surfaceIDsOfGroup(surfaces []types.Surface, g []int) []int {
	ids := make([]int, len(g))
	for i, idx := range g {
		ids[i] = surfaces[idx].ID
	}
	return ids
}

func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
