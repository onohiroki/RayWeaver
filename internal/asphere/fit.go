package asphere

import (
	"fmt"
	"math"

	"github.com/hiroki/rayweaver/internal/types"
)

// FitAsphereCoeffs estimates the Stage-1 asphere coefficients for a candidate
// surface from its cell OPD statistics, using the common OPD approach
// (fitRadial) instead of the per-cell OPD approach (targetSag).
// The target sag is fitted to the basis [r², r⁴, r⁶, ...] in the radius
// normalised by the footprint max radius so the fit is well conditioned, and
// regularised (ridge) so high-order terms cannot overfit cell noise into
// oscillatory shapes. With preserve_vertex_curvature the r² term is reported as
// a removed-defocus warning, otherwise as a suggested vertex-curvature change.
// The conic is estimated on the polynomial residual via a pure-conic fit.
// Constraints are applied to ensure the fit is physically sensible and does not
// overfit: A4 coefficient bounded by surface radius, conic coefficient bounded
// by surface radius.
func FitAsphereCoeffs(cells []types.AsphereCellStat, surf types.Surface, n1, n2 float64, cfg Config) (types.AsphereCoeffs, []string) {
	// Fit the common OPD to an even-order polynomial in the radius normalised
	// by the footprint max radius, ridge-regularised. fitRadial returns the
	// full polynomial coefficients [r², r⁴, ...] in OPD units (mm); the fast
	// OPD→sag approximation dz ≈ -O/(n2-n1) converts them to surface sag.
	coef, _ := fitRadial(cells, cfg.MaxEvenOrder)
	if coef == nil {
		return types.AsphereCoeffs{}, []string{"insufficient cells for sag fit"}
	}

	// Footprint max radius (physical units).
	rMax := 0.0
	for _, c := range cells {
		if c.MeanR > rMax {
			rMax = c.MeanR
		}
	}
	if rMax <= 0 {
		return types.AsphereCoeffs{}, []string{"degenerate footprint radius"}
	}

	// OPD → sag conversion factor (normal-incidence approximation).
	dn := n2 - n1
	if math.Abs(dn) < 1e-12 {
		return types.AsphereCoeffs{}, []string{"degenerate refractive-index difference"}
	}

	nTerms := maxOrder(cfg.MaxEvenOrder)

	// coef[0] is the r² (defocus) term in OPD units; the sag vertex-curvature
	// change it implies is 2·a2 = -2·coef[0]/(dn·rMax²).
	a2 := -coef[0] / (dn * rMax * rMax)
	var warnings []string
	if cfg.PreserveVertexCurvature {
		warnings = append(warnings, fmt.Sprintf("removed defocus r² term (2·a2=%.6g); not part of the asphere coefficients", 2*a2))
	} else {
		warnings = append(warnings, fmt.Sprintf("vertex curvature change %.6g (r² term; apply via curvature)", 2*a2))
	}

	// Convert normalised-radius OPD coefficients to physical sag coefficients:
	// a_phys · r^k = -coef_j/(dn) · (r/rMax)^k  →  a_phys = -coef_j/(dn·rMax^k).
	out := types.AsphereCoeffs{}
	for p := 0; p < nTerms; p++ {
		v := -coef[p+1] / (dn * math.Pow(rMax, float64(4+2*p)))
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
		// Fit the conic to the defocus-removed sag target (the aspheric
		// deformation). A conic is an alternative low-order parametrisation of
		// the same shape as the A4+ polynomial terms, so the two overlap; the
		// pure-conic least squares only reports a conic when it genuinely
		// improves over the plain sphere (guarded in fitConic).
		resid := make([]float64, len(cells))
		xs := make([]float64, len(cells))
		ws := make([]float64, len(cells))
		for i := range cells {
			rho := cells[i].MeanR / rMax
			xs[i] = cells[i].MeanR
			resid[i] = -(cells[i].CommonOPD - coef[0]*rho*rho) / dn
			ws[i] = cells[i].Weight
		}
		out.Conic = fitConic(xs, resid, ws, surf)
	}

	// Apply coefficient constraints to ensure the fit is physically sensible
	// and does not overfit: A4 coefficient bounded by surface radius, conic
	// coefficient bounded by surface radius. The bounds use the magnitude of
	// the (beam-frame) radius so both convex and concave surfaces are guarded.
	rInv := 0.0
	if r := surf.Radius(); r != 0 {
		rInv = 1.0 / math.Abs(r)
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

// fitRadial fits the shared-cell common OPD to an even-order polynomial
// [r², r⁴, ..., r^(4+2(nTerms-1))] in the cell mean radius (normalised by the
// footprint max radius, ridge-regularised) and returns the full polynomial
// coefficients (including the r² defocus term) in OPD units, together with the
// R² fit quality measured on the r²-removed residual (how well a
// rotationally-symmetric asphere, excluding a removable defocus, represents
// the shared OPD). The caller applies the OPD→sag conversion dz ≈ -O/(n2-n1).
func fitRadial(cells []types.AsphereCellStat, nTerms int) ([]float64, float64) {
	n := len(cells)
	xs := make([]float64, n)
	ys := make([]float64, n)
	ws := make([]float64, n)
	for i, c := range cells {
		xs[i] = c.MeanR
		ys[i] = c.CommonOPD
		ws[i] = c.Weight
	}
	rMax := 0.0
	for _, x := range xs {
		if x > rMax {
			rMax = x
		}
	}
	if rMax <= 0 {
		return nil, 0
	}

	ncols := nTerms + 1
	rows := make([][]float64, n)
	for i := range xs {
		rho := xs[i] / rMax
		row := make([]float64, ncols)
		row[0] = rho * rho
		for p := 0; p < nTerms; p++ {
			row[p+1] = math.Pow(rho, float64(4+2*p))
		}
		rows[i] = row
	}

	wts := make([]float64, n)
	copy(wts, ws)
	orderPenalty := make([]float64, ncols)
	for j := 0; j < ncols; j++ {
		orderPenalty[j] = 1 + float64(j)*0.5
	}
	coef, ok := solveRidge(rows, ys, wts, 0.05, orderPenalty)
	if !ok {
		return nil, 0
	}

	// Residual after removing the r² (defocus) component.
	resid := make([]float64, n)
	for i := range ys {
		resid[i] = ys[i] - coef[0]*xs[i]*xs[i]/rMax/rMax
	}

	// R² of the r⁴+ fit on the residual.
	var mean, wSum float64
	for i := range resid {
		mean += ws[i] * resid[i]
		wSum += ws[i]
	}
	if wSum <= 0 {
		return coef, 0
	}
	mean /= wSum

	var ssTot, ssRes float64
	for i := range resid {
		pred := 0.0
		for j := 0; j < nTerms; j++ {
			pred += coef[j+1] * rows[i][j+1]
		}
		ssTot += ws[i] * (resid[i] - mean) * (resid[i] - mean)
		ssRes += ws[i] * (resid[i] - pred) * (resid[i] - pred)
	}
	if ssTot <= 1e-30 {
		return coef, 0
	}
	return coef, 1 - ssRes/ssTot
}
