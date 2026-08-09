package main

import (
	"fmt"
	"math"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/optimize"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// validateAspheres runs a short DLS solve for each fitted top-K asphere in
// isolation: the scaled coefficients are inserted onto the candidate surface
// and the asphere coefficients (a4..a12) become the only variables, so the
// merit improvement is a faithful, cheap check that the suggested asphere is a
// real correction rather than an artefact of the OPD-to-sag approximation.
// It returns a map from surface ID to the validation result.
func validateAspheres(surfaces []types.Surface, rankings []types.AsphereSurfaceScore, gc *glass.Catalog, topK, dlsIter, numRays int, stopSurface, refSurface int, pupilZ float64, fields []types.FieldItem, wavelengths []float64, pol types.JonesVector, gridType types.GridType, passThrough *types.PassThroughTarget) map[int]*types.AsphereValidation {
	if dlsIter <= 0 {
		return nil
	}
	out := make(map[int]*types.AsphereValidation)
	for i, rs := range rankings {
		if i >= topK {
			break
		}
		if rs.ScaledCoefficients == (types.AsphereCoeffs{}) {
			continue
		}
		v := validateOneAsphere(surfaces, rs, gc, dlsIter, numRays, stopSurface, refSurface, pupilZ, fields, wavelengths, pol, gridType, passThrough)
		if v != nil {
			out[rs.SurfaceID] = v
		}
	}
	return out
}

// validateOneAsphere inserts one surface's scaled asphere and runs a short
// DLS over only its asphere coefficients.
func validateOneAsphere(surfaces []types.Surface, rs types.AsphereSurfaceScore, gc *glass.Catalog, dlsIter, numRays int, stopSurface, refSurface int, pupilZ float64, fields []types.FieldItem, wavelengths []float64, pol types.JonesVector, gridType types.GridType, passThrough *types.PassThroughTarget) *types.AsphereValidation {
	withAsphere := insertAsphere(surfaces, rs.SurfaceID, rs.ScaledCoefficients)

	// Recompute the dynamic pupil against the asphered system so the initial
	// grid centring hits the new surface (the Optimizer's UpdatePupils only
	// runs after the first merit evaluation).
	if refSurface > 0 && len(fields) > 0 {
		if p := dynamicEntrancePupil(withAsphere, chiefFieldDefsFromItems(fields), refSurface, numRays, gc, pol, gridType, passThrough); p != nil {
			pupilZ = p.Center.Z
		}
	}

	// Merit terms: one OPD-RMS term per (field, wavelength).
	var meritTerms []optimize.MeritTerm
	for _, f := range fields {
		for _, wl := range wavelengths {
			meritTerms = append(meritTerms, optimize.MeritTerm{
				Kind:        optimize.MeritOPDRMS,
				FieldAngle:  f.AngleDeg,
				FieldWeight: f.Weight,
				Wavelength:  wl,
				WavWeight:   1.0,
				Weight:      1.0,
			})
		}
	}
	if len(meritTerms) == 0 {
		return nil
	}

	cfg := optimize.Config{
		Surfaces:       withAsphere,
		Variables:      buildAsphereValidateVariables(rs.SurfaceID, rs.ScaledCoefficients),
		MeritTerms:     meritTerms,
		Fields:         fields,
		GlassCatalog:   gc,
		StopSurface:    stopSurface,
		RefSurface:     refSurface,
		PupilZ:         pupilZ,
		MaxIter:        dlsIter,
		NumRays:        numRays,
		Mu:             1.0,
		Tol:            1e-6,
		Epsilon:        1e-6,
		ApertureMargin: 1.0,
		MuConMax:       1e-4,
		Workers:        2,
	}
	opt := optimize.NewOptimizer(cfg)
	res := opt.Optimize()

	return &types.AsphereValidation{
		SurfaceID:   rs.SurfaceID,
		BeforeMerit: res.BeforeMerit,
		AfterMerit:  res.AfterMerit,
		Improvement: improvement(res.BeforeMerit, res.AfterMerit),
		Iterations:  res.Iterations,
		Status:      res.Status,
	}
}

// chiefFieldDefsFromItems converts FieldItems to chief FieldDefs for the
// dynamic-pupil calculation.
func chiefFieldDefsFromItems(fields []types.FieldItem) []types.FieldDef {
	var out []types.FieldDef
	for _, f := range fields {
		out = append(out, types.FieldDef{Angle: f.AngleDeg})
	}
	return out
}

func improvement(before, after float64) float64 {
	if before <= 0 || math.IsInf(before, 0) {
		return 0
	}
	return 1 - after/before
}

// insertAsphere returns a copy of surfaces with the given surface turned into
// an even-order polynomial asphere carrying the scaled coefficients. The conic
// is deliberately left at its original value (0 for a spherical candidate): a
// fitted conic on a weakly-curved surface can drive the conic sag's
// discriminant negative and break the trace, and the validation's purpose is to
// test the even-order correction in isolation.
func insertAsphere(surfaces []types.Surface, surfaceID int, coeffs types.AsphereCoeffs) []types.Surface {
	out := make([]types.Surface, len(surfaces))
	copy(out, surfaces)
	for i := range out {
		if out[i].ID == surfaceID {
			out[i].Type = types.AspherePolynomial
			out[i].Conic = 0
			out[i].Coefficients = asphereCoefficientSlice(coeffs)
		}
	}
	surface.Precompute(out)
	return out
}

// asphereCoefficientSlice returns the even-order asphere coefficients as a
// fixed-length slice ordered [A4, A6, A8, A10, A12].
func asphereCoefficientSlice(c types.AsphereCoeffs) []float64 {
	return []float64{c.A4, c.A6, c.A8, c.A10, c.A12}
}

// buildAsphereValidateVariables returns the asphere-coefficient optimisation
// variables for one inserted surface: a4..a12 (skipping zero terms).
func buildAsphereValidateVariables(surfaceID int, coeffs types.AsphereCoeffs) []optimize.Variable {
	terms := asphereCoefficientSlice(coeffs)
	params := []string{"a4", "a6", "a8", "a10", "a12"}
	var vars []optimize.Variable
	for i, v := range terms {
		if v == 0 {
			continue
		}
		// Bounds span a generous range around the scaled value so the short
		// DLS can explore while staying stable.
		lo := v - 10*math.Abs(v) - 1e-9
		hi := v + 10*math.Abs(v) + 1e-9
		vars = append(vars, optimize.Variable{
			Name:      fmt.Sprintf("s%d_%s", surfaceID, params[i]),
			SurfaceID: surfaceID,
			Param:     params[i],
			Min:       lo,
			Max:       hi,
		})
	}
	return vars
}
