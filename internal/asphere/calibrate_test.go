package asphere

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
)

// syntheticMerit returns (base, asphere, deriv) for the exact quadratic merit
// curve M(β) = M0 + c·(β − β0)² evaluated at β = probe, so the calibration
// must recover β0 exactly.
func syntheticMerit(M0, c, beta0, probe float64) (base, asphere float64, deriv []float64) {
	base = M0 + c*beta0*beta0          // M(0)
	asphere = M0 + c*(probe-beta0)*(probe-beta0)
	dM := 2 * c * (probe - beta0)      // dM/dβ at probe
	// Single non-zero coefficient A4 = 1e-4 ⇒ deriv = dM / A4.
	deriv = []float64{dM / 1e-4, 0, 0, 0, 0}
	return base, asphere, deriv
}

func TestCalibrateScaleRecoversQuadraticMinimum(t *testing.T) {
	coeffs := types.AsphereCoeffs{A4: 1e-4}
	base, asphere, deriv := syntheticMerit(0.03, 0.1, 0.25, 0.2)
	beta, ok := CalibrateScale(coeffs, base, asphere, deriv, 0.2)
	if !ok {
		t.Fatal("CalibrateScale returned ok=false")
	}
	if math.Abs(beta-0.25) > 1e-9 {
		t.Fatalf("calibrated scale = %v, want 0.25 (the exact quadratic minimum)", beta)
	}
}

// Real measured data from the α-dependence experiment on asphere-demo-init.yaml
// surface 8 (see REPORT.md): the calibration must land near the known optimum.
func TestCalibrateScaleRealProbeCases(t *testing.T) {
	coeffs := types.AsphereCoeffs{
		A4: -1.143785706688277e-05,
		A6: -2.0584147555186952e-07,
		A8: -2.9603538070677016e-09,
		A10: -4.1859728912992694e-11,
	}

	cases := []struct {
		name    string
		probe   float64
		base    float64
		asphere float64
		deriv   []float64
		wantMin float64
		wantMax float64
	}{
		// probe at the optimum: recovers ~0.19.
		{"probe 0.2 (near optimum)", 0.2, 0.03040, 0.00872,
			[]float64{-622.18, -23396.18, -1.3208e6, 2.0623e8, 4.2223e8},
			0.18, 0.20},
		// probe overshooting: quadratic has no interior min → shrink to α/4.
		{"probe 0.5 (overshoot)", 0.5, 0.03040, 0.05077,
			[]float64{-2332.94, -204218.83, -4.2945e6, 1.9088e8, 3.1118e8},
			0.124, 0.126},
		// probe undershooting: recovers ~0.074, bounded by the 2α clamp.
		{"probe 0.05 (undershoot)", 0.05, 0.03040, 0.02268,
			[]float64{2438.88, 183343.94, 820508.08, 1.8239e8, 4.7910e8},
			0.070, 0.080},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			beta, ok := CalibrateScale(coeffs, tc.base, tc.asphere, tc.deriv, tc.probe)
			if !ok {
				t.Fatal("CalibrateScale returned ok=false")
			}
			if beta < tc.wantMin || beta > tc.wantMax {
				t.Fatalf("calibrated scale = %v, want in [%v, %v]", beta, tc.wantMin, tc.wantMax)
			}
		})
	}
}

func TestCalibrateScaleDegenerate(t *testing.T) {
	coeffs := types.AsphereCoeffs{A4: 1e-4}
	if _, ok := CalibrateScale(coeffs, 0, 1, nil, 0.2); ok {
		t.Fatal("expected ok=false for non-positive base")
	}
	if _, ok := CalibrateScale(coeffs, 1, 1, nil, 0); ok {
		t.Fatal("expected ok=false for non-positive probe")
	}
	if _, ok := CalibrateScale(coeffs, math.NaN(), 1, nil, 0.2); ok {
		t.Fatal("expected ok=false for NaN base")
	}
}

func TestCalibrateScaleClampsToSafeRange(t *testing.T) {
	// An extreme quadratic with a minimum far beyond the clamp must be pulled
	// back to 2·probe.
	base, asphere, deriv := syntheticMerit(0.03, 0.01, 3.0, 0.2)
	beta, ok := CalibrateScale(types.AsphereCoeffs{A4: 1e-4}, base, asphere, deriv, 0.2)
	if !ok {
		t.Fatal("CalibrateScale returned ok=false")
	}
	if math.Abs(beta-0.4) > 1e-12 {
		t.Fatalf("calibrated scale = %v, want clamped to 2·probe = 0.4", beta)
	}
}

// TestRunTripletCalibratesScale checks the full Run path fills the calibrated
// fields and that disabling calibration leaves them empty.
func TestRunTripletCalibratesScale(t *testing.T) {
	surfaces, gc := tripletSystem()
	fields := []Field{
		{ID: 1, Angle: 0, Weight: 1, Direction: []float64{0, 1}},
		{ID: 2, Angle: 16, Weight: 1, Direction: []float64{0, 1}},
		{ID: 3, Angle: 24, Weight: 1, Direction: []float64{0, 1}},
	}

	cfg := DefaultConfig()
	cfg.TopK = 2
	cfg.SensitivitySamples = 7
	res := Run(surfaces, fields, nil, cfg, gc, 0, 8, nil)

	calibrated := 0
	for _, r := range res.Rankings {
		s := r.Sensitivity
		if s == nil {
			continue
		}
		if s.CalibratedScale <= 0 {
			t.Fatalf("surface %d: calibration produced no scale (%v)", r.SurfaceID, s.CalibratedScale)
		}
		if s.CalibratedImprovement < 0 {
			t.Fatalf("surface %d: negative calibrated improvement %v", r.SurfaceID, s.CalibratedImprovement)
		}
		calibrated++
	}
	if calibrated == 0 {
		t.Fatal("no sensitivity matrix was calibrated")
	}

	// Top-K entries carry the calibrated embedded coefficients.
	for _, r := range res.Rankings {
		if r.CalibratedCoefficients == (types.AsphereCoeffs{}) {
			continue
		}
		if r.Sensitivity == nil || r.Sensitivity.CalibratedScale <= 0 {
			t.Fatalf("surface %d: calibrated coefficients without a calibrated scale", r.SurfaceID)
		}
	}

	// Disabling calibration leaves the calibrated fields empty.
	cfgOff := DefaultConfig()
	cfgOff.TopK = 2
	cfgOff.SensitivitySamples = 7
	cfgOff.CalibrateScale = false
	resOff := Run(surfaces, fields, nil, cfgOff, gc, 0, 8, nil)
	for _, r := range resOff.Rankings {
		if s := r.Sensitivity; s != nil && s.CalibratedScale != 0 {
			t.Fatalf("surface %d: calibration not disabled (%v)", r.SurfaceID, s.CalibratedScale)
		}
		if r.CalibratedCoefficients != (types.AsphereCoeffs{}) {
			t.Fatalf("surface %d: calibrated coefficients not disabled", r.SurfaceID)
		}
	}
}

// TestScoreSurfaceClampsNegativeMeasuredH guards the backstop that prevents an
// overshooting probe from feeding a negative sensitivity term into the score.
func TestScoreSurfaceClampsNegativeMeasuredH(t *testing.T) {
	cells := []types.AsphereCellStat{
		{SurfaceID: 1, MeanR: 1, CommonOPD: 0.01, Weight: 1, OccupiedFields: []int{1, 2}},
	}
	surf := types.Surface{Curvature: 0.02, Diameter: 6}
	weights := DefaultConfig().ScoreWeights
	opts := ScoreOptions{MeasuredH: -0.5, HasMeasuredH: true, MaxEvenOrder: 10}
	score := ScoreSurface(cells, surf, 1.0, 1.5, weights, opts)
	if score.SensitivityPenalty != 0 {
		t.Fatalf("SensitivityPenalty = %v, want 0 (negative measured H clamped)", score.SensitivityPenalty)
	}
}
