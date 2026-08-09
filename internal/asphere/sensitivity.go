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
