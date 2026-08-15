package asphere

import (
	"math"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// CoefficientSet returns the even-order asphere coefficients as a fixed-length
// slice ordered [A4, A6, A8, A10, A12], the convention used by
// raymath.PolynomialAsphereSag.
func CoefficientSet(c types.AsphereCoeffs) []float64 {
	return []float64{c.A4, c.A6, c.A8, c.A10, c.A12}
}

// withAsphere returns a copy of the surfaces where surfaceID carries the given
// even-order asphere coefficients (conic + A4..A12), so the perturbed system
// can be re-traced. The copy is precomputed in place.
func withAsphere(surfaces []types.Surface, surfaceID int, coeffs types.AsphereCoeffs) []types.Surface {
	out := make([]types.Surface, len(surfaces))
	copy(out, surfaces)
	for i := range out {
		if out[i].ID == surfaceID {
			out[i].Type = types.AspherePolynomial
			out[i].Conic = coeffs.Conic
			out[i].Coefficients = CoefficientSet(coeffs)
		}
	}
	surface.Precompute(out)
	return out
}

// traceMerit traces the pupil grid for every (field, wavelength) against the
// given surfaces and returns the weighted RMS OPD over all rays. The OPD is
// referenced per field (piston removed); tilt/defocus removal follow the
// analysis configuration so the merit is comparable to the cell fit.
func traceMerit(surfaces []types.Surface, fields []Field, wavelengths []float64, samples int, gc *glass.Catalog, pupilZs []float64, cfg Config) float64 {
	fps := GenerateFootprints(surfaces, fields, wavelengths, samples, gc, pupilZs)
	PreprocessOPD(fps, cfg.RemoveTilt, cfg.RemoveDefocus)
	var sse, wsum float64
	for _, fd := range fps {
		w := fd.Weight
		if w <= 0 {
			w = 1
		}
		for _, h := range fd.RayHits {
			if !h.OK {
				continue
			}
			sse += w * h.OPD * h.OPD
			wsum += w
		}
	}
	if wsum <= 0 {
		return math.Inf(1)
	}
	return math.Sqrt(sse / wsum)
}

// traceAsphereMerit is traceMerit on the surface carrying the given asphere.
func traceAsphereMerit(surfaces []types.Surface, fields []Field, wavelengths []float64, samples int, gc *glass.Catalog, pupilZs []float64, surfaceID int, coeffs types.AsphereCoeffs, cfg Config) float64 {
	return traceMerit(withAsphere(surfaces, surfaceID, coeffs), fields, wavelengths, samples, gc, pupilZs, cfg)
}

// Sensitivity computes the finite-difference sensitivity of the traced merit
// to each even-order coefficient on the candidate surface (all others frozen)
// and returns the per-coefficient ∂Merit/∂c_j plus the base and asphere merits.
// base is the (already-traced) merit of the un-asphered system shared by every
// candidate; passing it avoids re-tracing the base system per surface. The
// pupil Zs are frozen (shared between base point and perturbations) so the
// derivatives are consistent, mirroring the DLS Jacobian convention.
func Sensitivity(surfaces []types.Surface, fields []Field, wavelengths []float64, samples int, gc *glass.Catalog, pupilZs []float64, surfaceID int, coeffs types.AsphereCoeffs, cfg Config, base float64) types.AsphereSensitivityMatrix {
	asp := traceAsphereMerit(surfaces, fields, wavelengths, samples, gc, pupilZs, surfaceID, coeffs, cfg)

	terms := CoefficientSet(coeffs)
	deriv := make([]float64, len(terms))
	for j := range terms {
		// Step relative to the coefficient, floored to keep it numerically
		// significant for near-zero terms.
		step := math.Max(math.Abs(terms[j])*1e-3, 1e-6*math.Pow(0.1, float64(j)))
		up := coeffs
		lo := coeffs
		applyCoeffStep(&up, j, +step)
		applyCoeffStep(&lo, j, -step)
		mUp := traceAsphereMerit(surfaces, fields, wavelengths, samples, gc, pupilZs, surfaceID, up, cfg)
		mLo := traceAsphereMerit(surfaces, fields, wavelengths, samples, gc, pupilZs, surfaceID, lo, cfg)
		deriv[j] = (mUp - mLo) / (2 * step)
	}

	improvement := 0.0
	if base > 0 {
		improvement = 1 - asp/base
	}
	return types.AsphereSensitivityMatrix{
		BaseMerit:    base,
		AsphereMerit: asp,
		Improvement:  improvement,
		DMeritDCoef:  deriv,
	}
}

// applyCoeffStep perturbs one coefficient of the given set by step.
func applyCoeffStep(c *types.AsphereCoeffs, j int, step float64) {
	switch j {
	case 0:
		c.A4 += step
	case 1:
		c.A6 += step
	case 2:
		c.A8 += step
	case 3:
		c.A10 += step
	case 4:
		c.A12 += step
	}
}

// CalibrateScale estimates the embedded scale for the fitted coefficients from
// the measured sensitivity. The traced OPD merit M(β) with the coefficients
// scaled by β is modelled by a quadratic through the two measured points
// M(0)=base and M(probe)=asphere plus the directional derivative
// D = Σ_j ∂M/∂c_j·c_j along the fitted-coefficient direction (from
// d_merit_d_coef):
//
//	b = (D·probe − Δ)/probe²,  a = D − 2·b·probe,  β* = −a/(2·b)   (Δ = asphere − base)
//
// The proposal β* is clamped to [probe/4, 2·probe] ∩ [0.05, 1.0]. When no
// interior minimum exists (b ≤ 0 or β* ≤ 0) it proposes the shrink probe/4.
// The caller must verify the proposal by re-tracing M(β*) and fall back to the
// probe when it is not an improvement (the pick-min property guarantees
// calibration never does worse than the current behaviour). Returns ok=false
// when calibration is impossible (non-positive or non-finite base/probe).
func CalibrateScale(coeffs types.AsphereCoeffs, base, asphere float64, deriv []float64, probe float64) (beta float64, ok bool) {
	if probe <= 0 || base <= 0 || !isFinite(base) || !isFinite(asphere) {
		return 0, false
	}
	terms := CoefficientSet(coeffs)
	var d float64
	for j := range terms {
		if j < len(deriv) {
			d += deriv[j] * terms[j]
		}
	}
	delta := asphere - base
	b := (d*probe - delta) / (probe * probe)
	if b > 1e-15 {
		a := d - 2*b*probe
		beta := -a / (2 * b)
		if beta > 0 {
			return clampCalibratedScale(beta, probe), true
		}
	}
	// No interior minimum: the local model cannot locate a beneficial scale,
	// so propose a shrink and let the verify trace decide.
	return clampCalibratedScale(probe/4, probe), true
}

func clampCalibratedScale(beta, probe float64) float64 {
	lo := math.Max(probe/4, 0.05)
	hi := math.Min(2*probe, 1.0)
	return math.Max(lo, math.Min(hi, beta))
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

// calibrateSensitivity fills a sensitivity matrix's calibrated fields: it
// proposes one or more embedded scales (the quadratic CalibrateScale estimate,
// or the explicit scale_probes list), verifies each with a re-trace, and keeps
// the scale with the lowest finite merit — the probe scale stays the fallback
// when nothing verifies better (pick-min property). The calibrated improvement
// is the verified relative merit reduction, floored at 0 so an overshooting
// probe can never feed a negative sensitivity term into the ranking.
func calibrateSensitivity(surfaces []types.Surface, fields []Field, wavelengths []float64, cfg Config, gc *glass.Catalog, pupilZs []float64, surfaceID int, coeffs types.AsphereCoeffs, base float64, sens types.AsphereSensitivityMatrix) types.AsphereSensitivityMatrix {
	var cands []float64
	if len(cfg.ScaleProbes) > 0 {
		cands = append(cands, cfg.ScaleProbes...)
	} else if beta, ok := CalibrateScale(coeffs, base, sens.AsphereMerit, sens.DMeritDCoef, cfg.SagScale); ok {
		cands = append(cands, beta)
	} else {
		return sens
	}

	bestScale := cfg.SagScale
	bestMerit := sens.AsphereMerit
	for _, beta := range cands {
		if beta <= 0 || beta > 1 {
			continue
		}
		m := traceAsphereMerit(surfaces, fields, wavelengths, cfg.SensitivitySamples, gc, pupilZs, surfaceID, ScaleCoefficients(coeffs, beta), cfg)
		if !isFinite(m) {
			continue
		}
		if m < bestMerit {
			bestScale, bestMerit = beta, m
		}
	}

	sens.CalibratedScale = bestScale
	sens.CalibratedMerit = bestMerit
	sens.CalibratedImprovement = 0
	if base > 0 {
		if imp := 1 - bestMerit/base; imp > 0 {
			sens.CalibratedImprovement = imp
		}
	}
	return sens
}
