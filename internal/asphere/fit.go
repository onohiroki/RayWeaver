package asphere

import (
	"fmt"
	"math"

	"github.com/hiroki/rayweaver/internal/types"
)

// FitAsphereCoeffsJoint converts the exact-ray joint fit into physical asphere
// coefficients. Unlike the legacy cell fit, every traced ray contributes to
// the coefficient estimate and the conic fit.
func FitAsphereCoeffsJoint(jf *JointFit, surf types.Surface, n1, n2 float64, cfg Config) (types.AsphereCoeffs, []string) {
	if jf == nil || len(jf.Coef) < 2 || jf.RMax <= 0 {
		return types.AsphereCoeffs{}, []string{"insufficient rays for sag fit"}
	}
	dn := n2 - n1
	if math.Abs(dn) < 1e-12 {
		return types.AsphereCoeffs{}, []string{"degenerate refractive-index difference"}
	}
	// The joint fit stores the normalised polynomial. Its first term is the
	// removable vertex-curvature term; all following terms become A4..A12.
	nTerms := maxOrder(cfg.MaxEvenOrder)
	out := types.AsphereCoeffs{}
	warnings := []string{}
	a2 := -jf.Coef[0] / (dn * jf.RMax * jf.RMax)
	if cfg.PreserveVertexCurvature {
		warnings = append(warnings, fmt.Sprintf("removed defocus r² term (2·a2=%.6g); not part of the asphere coefficients", 2*a2))
	} else {
		warnings = append(warnings, fmt.Sprintf("vertex curvature change %.6g (r² term; apply via curvature)", 2*a2))
	}
	for p := 0; p < nTerms && p+1 < len(jf.Coef); p++ {
		v := -jf.Coef[p+1] / (dn * math.Pow(jf.RMax, float64(4+2*p)))
		switch p {
		case 0:
			out.A4 = v
		case 1:
			out.A6 = v
		case 2:
			out.A8 = v
		case 3:
			out.A10 = v
		case 4:
			out.A12 = v
		}
	}
	if cfg.IncludeConic {
		// Use a compact synthetic radial sample for the conic projection. The
		// polynomial itself is the exact-ray fit; this only estimates an optional
		// alternate conic representation of its defocus-removed sag.
		const samples = 64
		xs, ys, ws := make([]float64, samples), make([]float64, samples), make([]float64, samples)
		for i := 0; i < samples; i++ {
			r := jf.RMax * float64(i+1) / samples
			rho := r / jf.RMax
			opd := 0.0
			for p := 1; p < len(jf.Coef); p++ {
				opd += jf.Coef[p] * math.Pow(rho, float64(4+2*(p-1)))
			}
			xs[i], ys[i], ws[i] = r, -opd/dn, 1
		}
		out.Conic = fitConic(xs, ys, ws, surf)
	}
	rInv := 0.0
	if r := surf.Radius(); r != 0 {
		rInv = 1 / math.Abs(r)
	}
	if rInv != 0 && math.Abs(out.A4) > rInv {
		out.A4 = math.Copysign(rInv, out.A4)
		warnings = append(warnings, "A4 coefficient bounded by surface radius")
	}
	if rInv != 0 && math.Abs(out.Conic) > rInv {
		out.Conic = math.Copysign(rInv, out.Conic)
		warnings = append(warnings, "conic coefficient bounded by surface radius")
	}
	return out, warnings
}

// fitConic estimates the conic constant by fitting the target sag to the
// pure-conic deformation z_k(r)-z_0(r) via a weighted 1-D grid search.
func fitConic(xs, ys, ws []float64, surf types.Surface) float64 {
	R := surf.Radius()
	if R == 0 {
		return 0
	}
	c := 1.0 / R
	zBase := func(r float64) float64 {
		disc := 1 - c*c*r*r
		if disc < 0 {
			return math.NaN()
		}
		return c * r * r / (1 + math.Sqrt(disc))
	}
	zConic := func(r, k float64) float64 {
		disc := 1 - (1+k)*c*c*r*r
		if disc < 0 {
			return math.NaN()
		}
		return c * r * r / (1 + math.Sqrt(disc))
	}

	// Baseline error at k = 0 (pure sphere): the conic adds zConic−zBase over
	// the base sphere, so the k=0 residual is just −ys.
	errAtZero := 0.0
	for j := range xs {
		d := -ys[j]
		errAtZero += ws[j] * d * d
	}

	bestK := 0.0
	bestErr := math.Inf(1)
	const kLo, kHi = -20.0, 20.0
	const steps = 800
	for i := 0; i <= steps; i++ {
		k := kLo + (kHi-kLo)*float64(i)/steps
		err := 0.0
		for j := range xs {
			d := (zConic(xs[j], k) - zBase(xs[j])) - ys[j]
			if math.IsNaN(d) {
				err = math.Inf(1)
				break
			}
			err += ws[j] * d * d
		}
		if err < bestErr {
			bestErr = err
			bestK = k
		}
	}
	// A conic is only meaningful when it actually reduces the residual by a
	// meaningful margin AND the optimum is interior to the search range. For a
	// weak base curvature the conic deformation is negligible (any k fits
	// equally well → edge), and an edge-saturating optimum means the conic
	// alone cannot represent the residual (the honest answer is to leave the
	// conic to a later phase).
	if errAtZero > 0 && bestErr > 0.98*errAtZero {
		return 0
	}
	if bestK <= kLo*(1+0.05) || bestK >= kHi*(1-0.05) {
		return 0
	}
	return bestK
}

// ScaleCoefficients returns the coefficients scaled by α for safe insertion.
func ScaleCoefficients(coeffs types.AsphereCoeffs, scale float64) types.AsphereCoeffs {
	return types.AsphereCoeffs{
		Conic: coeffs.Conic * scale,
		A4:    coeffs.A4 * scale,
		A6:    coeffs.A6 * scale,
		A8:    coeffs.A8 * scale,
		A10:   coeffs.A10 * scale,
		A12:   coeffs.A12 * scale,
	}
}
