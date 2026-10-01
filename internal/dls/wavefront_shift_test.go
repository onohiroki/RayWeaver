package dls

import (
	"math"
	"testing"
)

// pupilGridPoints builds a disc-sampled pupil grid in millimetres with the given
// OPL function, so a test can inject an exact wavefront on the pupil. The OPL is
// evaluated analytically at each site; ComputeWavefrontShift then recovers it only
// through the reference-sphere fit and the triangulation, which is what these
// tests pin.
func pupilGridPoints(n int, radius float64, opl func(x, y float64) float64) []IPoint {
	var out []IPoint
	for j := 0; j <= n; j++ {
		for i := 0; i <= n; i++ {
			x := -radius + 2*radius*float64(i)/float64(n)
			y := -radius + 2*radius*float64(j)/float64(n)
			if x*x+y*y > radius*radius {
				continue
			}
			out = append(out, IPoint{
				X: x, Y: y, OK: true,
				PupilX: x, PupilY: y,
				OPL:       opl(x, y),
				Area:      1,
				Intensity: 1,
			})
		}
	}
	return out
}

const (
	testWavelength = 0.0005876
	testPupilR     = 6.25
	testShearScale = 48.6 // R_ent/NA, i.e. the EFL, of the f/3.9 case
)

// A perfect lens focuses a parallel bundle to the image plane, so the OPL of a
// pupil sample at radius r is r²/(2L) with L the pupil-to-image path scale. That
// is a pure radial quadratic — the reference sphere the estimator removes — so a
// perfect lens must score zero.
func idealLens(radius, scale float64) func(x, y float64) float64 {
	return func(x, y float64) float64 { return (x*x + y*y) / (2 * scale) }
}

// TestComputeWavefrontShiftReadsAPerfectLensAsPerfect is the decisive check that
// the estimator scores the wavefront rather than the focus position. The focusing
// of a converging bundle is a quadratic in the pupil, so its sheared difference is
// *linear*; without the reference-sphere fit an ideal lens would score a large
// residual and the solver would walk the image plane instead of fixing anything.
// The residual after the fit is a linear ramp over the disc, which the mean
// subtraction does not remove, so a small nonzero floor here is expected — the
// assertion is that it is orders of magnitude below any real aberration.
func TestComputeWavefrontShiftReadsAPerfectLensAsPerfect(t *testing.T) {
	pts := pupilGridPoints(32, testPupilR, idealLens(testPupilR, testShearScale))
	perfect := ComputeWavefrontShift(pts, 10, testWavelength, testShearScale, "sag")
	// Add a spherical term (a quartic, well above the reference sphere) and check
	// it is picked up by a wide margin.
	aberrated := pupilGridPoints(32, testPupilR, func(x, y float64) float64 {
		r2 := x*x + y*y
		return r2/(2*testShearScale) + 1e-3*r2*r2
	})
	got := ComputeWavefrontShift(aberrated, 10, testWavelength, testShearScale, "sag")
	if got <= 100*perfect {
		t.Errorf("spherical term scored %g against a perfect lens's %g; the reference sphere is not being removed", got, perfect)
	}
	if got <= 0 {
		t.Errorf("spherical term scored %g, want > 0", got)
	}
}

// TestComputeWavefrontShiftIsNotInert is the regression for the kind being
// measured by nothing. The pairing used to be an exact 0.001 mm key match against
// the grid coordinates, which no traced grid satisfies, so every state reported
// 0.000000 — against a live spot_rms of 0.001529 on the same design.
func TestComputeWavefrontShiftIsNotInert(t *testing.T) {
	pts := pupilGridPoints(32, testPupilR, func(x, y float64) float64 {
		r2 := x*x + y*y
		return r2/(2*testShearScale) + 1e-3*(x*x-y*y)
	})
	for _, dir := range []string{"sag", "tan"} {
		if got := ComputeWavefrontShift(pts, 10, testWavelength, testShearScale, dir); got <= 0 {
			t.Errorf("%s: ComputeWavefrontShift = %g, want > 0 (the kind is inert)", dir, got)
		}
	}
}

// TestComputeWavefrontShiftIgnoresPistonAndTilt pins the reason the reference
// sphere is fitted rather than the path differences simply summed. Piston and
// tilt add the same phase to every term, so they must not be charged — a shifted
// PSF has the same MTF magnitude. The invariance is near-exact (a linear function
// is reproduced exactly by the fit and by barycentric interpolation), so the
// tolerance is float noise.
func TestComputeWavefrontShiftIgnoresPistonAndTilt(t *testing.T) {
	base := func(x, y float64) float64 { return 1e-3 * (x*x - y*y) }
	pts := pupilGridPoints(32, testPupilR, base)
	shifted := pupilGridPoints(32, testPupilR, func(x, y float64) float64 {
		return base(x, y) + 0.01*x + 0.004
	})
	for _, dir := range []string{"sag", "tan"} {
		a := ComputeWavefrontShift(pts, 10, testWavelength, testShearScale, dir)
		b := ComputeWavefrontShift(shifted, 10, testWavelength, testShearScale, dir)
		if a <= 0 {
			t.Fatalf("%s: base value is 0; the check would be vacuous", dir)
		}
		if math.Abs(b-a) > 1e-12*math.Max(1, a) {
			t.Errorf("%s: piston+tilt changed the value: %.12g -> %.12g (want invariant)", dir, a, b)
		}
	}
}

// TestComputeWavefrontShiftSeesAstigmatism pins that a non-axisymmetric aberration
// survives the reference sphere and is caught, quadratically in its coefficient —
// the sheared difference of a quadratic is linear in the pupil, so the variance
// goes as the square.
func TestComputeWavefrontShiftSeesAstigmatism(t *testing.T) {
	val := func(a float64, dir string) float64 {
		pts := pupilGridPoints(32, testPupilR, func(x, y float64) float64 {
			return a * (x*x - y*y)
		})
		return ComputeWavefrontShift(pts, 10, testWavelength, testShearScale, dir)
	}
	for _, dir := range []string{"sag", "tan"} {
		one := val(1e-4, dir)
		two := val(2e-4, dir)
		if one <= 0 {
			t.Fatalf("%s: astigmatism scored %g, want > 0 (it is being removed by the reference sphere)", dir, one)
		}
		if ratio := two / one; math.Abs(ratio-4) > 0.25 {
			t.Errorf("%s: doubling the astigmatism scaled the value by %g, want ~4 (quadratic)", dir, ratio)
		}
	}
}

// TestComputeWavefrontShiftSeesSpherical pins the same for a rotationally
// symmetric term above the reference sphere.
func TestComputeWavefrontShiftSeesSpherical(t *testing.T) {
	val := func(a float64) float64 {
		pts := pupilGridPoints(32, testPupilR, func(x, y float64) float64 {
			r2 := x*x + y*y
			return a * r2 * r2
		})
		return ComputeWavefrontShift(pts, 10, testWavelength, testShearScale, "sag")
	}
	one, two := val(1e-3), val(2e-3)
	if one <= 0 {
		t.Fatalf("spherical scored %g, want > 0", one)
	}
	if ratio := two / one; math.Abs(ratio-4) > 0.25 {
		t.Errorf("doubling the spherical term scaled the value by %g, want ~4 (quadratic)", ratio)
	}
}

// TestComputeWavefrontShiftIsFrequencyWeighted pins that the reading is not a
// single fixed blur number but carries the frequency: the sheared difference of a
// smooth wavefront is proportional to s, and the statistic is a variance of it, so
// the value goes as s². That weighting is what makes the term a
// frequency-specific proxy rather than another spot size, and it only holds
// because the shear really is ν·λ·shearScale. The exponent is only approximate:
// the reference-sphere fit absorbs part of the quartic into its quadratic, and
// barycentric interpolation of a quartic is not exact, so the measured ratio is
// 3.7 rather than 4. The tolerance pins the exponent, not the constant.
func TestComputeWavefrontShiftIsFrequencyWeighted(t *testing.T) {
	val := func(freq float64) float64 {
		pts := pupilGridPoints(32, testPupilR, func(x, y float64) float64 {
			r2 := x*x + y*y
			return r2 * r2
		})
		return ComputeWavefrontShift(pts, freq, testWavelength, testShearScale, "sag")
	}
	one, two := val(5), val(10)
	if one <= 0 {
		t.Fatalf("value at 5 lp/mm is 0")
	}
	if ratio := two / one; math.Abs(ratio-4) > 0.4 {
		t.Errorf("doubling the frequency scaled the value by %g, want ~4 (s²)", ratio)
	}
}

// TestComputeWavefrontShiftFrequencyIsLpmm pins the frequency calibration through
// the cutoff: past the displacement at which the pupil no longer overlaps itself
// no pair survives and the value falls to zero, and that displacement must be the
// pupil diameter — which only happens if the shear really is ν·λ·shearScale.
func TestComputeWavefrontShiftFrequencyIsLpmm(t *testing.T) {
	pts := pupilGridPoints(32, testPupilR, func(x, y float64) float64 {
		r2 := x*x + y*y
		return 1e-3 * r2 * r2
	})
	cutoff := 2 * testPupilR / (testWavelength * testShearScale)
	if got := ComputeWavefrontShift(pts, 1.5*cutoff, testWavelength, testShearScale, "sag"); got != 0 {
		t.Errorf("past the cutoff: got %g, want 0 (no pair has pupil overlap)", got)
	}
	if got := ComputeWavefrontShift(pts, cutoff/4, testWavelength, testShearScale, "sag"); got <= 0 {
		t.Errorf("inside the cutoff: got %g, want > 0", got)
	}
}

// TestComputeWavefrontShiftDropsPairsWithoutPupilOverlap pins that a pair whose
// displaced point has left the sampled pupil is dropped rather than matched to a
// far triangle. The probe is at 1.5x the cutoff rather than exactly at it: the
// sampled disc's lattice boundary leaves a few points within a cell of the rim, so
// the exact cutoff is a sampling artefact rather than a statement about the
// estimator.
func TestComputeWavefrontShiftDropsPairsWithoutPupilOverlap(t *testing.T) {
	pts := pupilGridPoints(32, testPupilR, func(x, y float64) float64 {
		r2 := x*x + y*y
		return 1e-3 * r2 * r2
	})
	cutoff := 2 * testPupilR / (testWavelength * testShearScale)
	if got := ComputeWavefrontShift(pts, 1.5*cutoff, testWavelength, testShearScale, "sag"); got != 0 {
		t.Errorf("past the cutoff: got %g, want 0 (no pair has pupil overlap)", got)
	}
}

// TestComputeWavefrontShiftDegenerateInputs covers the no-usable-data paths.
func TestComputeWavefrontShiftDegenerateInputs(t *testing.T) {
	pts := pupilGridPoints(32, testPupilR, func(x, y float64) float64 { return x * x * x })
	zeroWeight := pupilGridPoints(32, testPupilR, func(x, y float64) float64 { return x * x * x })
	for i := range zeroWeight {
		zeroWeight[i].Area = 0
	}
	cases := []struct {
		name            string
		pts             []IPoint
		freq, wl, scale float64
		dir             string
	}{
		{"no points", nil, 10, testWavelength, testShearScale, "sag"},
		{"zero frequency", pts, 0, testWavelength, testShearScale, "sag"},
		{"negative frequency", pts, -10, testWavelength, testShearScale, "sag"},
		{"zero wavelength", pts, 10, 0, testShearScale, "sag"},
		{"zero shear scale", pts, 10, testWavelength, 0, "sag"},
		{"too few samples", pts[:3], 10, testWavelength, testShearScale, "sag"},
		{"no valid samples", []IPoint{{PupilX: 1, PupilY: 1, Area: 1, Intensity: 1}}, 10, testWavelength, testShearScale, "sag"},
		{"zero weight", zeroWeight, 10, testWavelength, testShearScale, "sag"},
	}
	for _, c := range cases {
		if got := ComputeWavefrontShift(c.pts, c.freq, c.wl, c.scale, c.dir); got != 0 {
			t.Errorf("%s: got %g, want 0", c.name, got)
		}
	}
}
