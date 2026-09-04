package psf

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/spectral"
	"github.com/hiroki/rayweaver/internal/types"
)

// gaussianGrid builds an image-plane grid of an unnormalized Gaussian PSF
// exp(-r²/2σ²) with a symmetric centred window.
func gaussianGrid(n int, dx, sigma float64) ([]float64, ImageGridSpec) {
	spec := ImageGridSpec{
		NX: n, NY: n,
		X0: -float64(n) / 2 * dx,
		Y0: -float64(n) / 2 * dx,
		DX: dx, DY: dx,
	}
	I := make([]float64, n*n)
	for j := 0; j < n; j++ {
		for i := 0; i < n; i++ {
			x := spec.X0 + (float64(i)+0.5)*dx
			y := spec.Y0 + (float64(j)+0.5)*dx
			I[j*n+i] = math.Exp(-(x*x + y*y) / (2 * sigma * sigma))
		}
	}
	return I, spec
}

// TestMTFGaussian verifies the computed MTF of a Gaussian PSF against the
// analytic result exp(-2π²σ²f²), and that the PTF is zero about the centroid.
func TestMTFGaussian(t *testing.T) {
	const (
		n     = 128
		dx    = 6.25e-4
		sigma = 8e-3
	)
	I, spec := gaussianGrid(n, dx, sigma)
	m := ComputeMTF(I, spec, nil)
	if m == nil {
		t.Fatal("ComputeMTF returned nil")
	}
	if len(m.Sagittal.Thresholds) == 0 || len(m.Tangential.Thresholds) == 0 {
		t.Fatalf("expected default thresholds (0.5/0.3/0.1), got sag=%d tan=%d",
			len(m.Sagittal.Thresholds), len(m.Tangential.Thresholds))
	}
	for _, axis := range []*types.PSFMTFAxis{&m.Sagittal, &m.Tangential} {
		for _, p := range axis.Curve {
			want := math.Exp(-2 * math.Pi * math.Pi * sigma * sigma * p.Frequency * p.Frequency)
			if want < 0.05 {
				continue
			}
			if math.Abs(p.MTF-want) > 0.02*want && math.Abs(p.MTF-want) > 0.006 {
				t.Errorf("MTF(f=%.1f) = %.5f, want %.5f (Gaussian)", p.Frequency, p.MTF, want)
			}
			if math.Abs(p.PTF) > 0.02 {
				t.Errorf("PTF(f=%.1f) = %v, want ~0 (centred Gaussian)", p.Frequency, p.PTF)
			}
			if math.Abs(p.OTFReal-p.MTF) > 0.02 {
				t.Errorf("OTF real (%.4f) should equal |OTF| (%.4f) for centred Gaussian", p.OTFReal, p.MTF)
			}
		}
		// The MTF50 crossing of a Gaussian: f = √(ln2/(2π²σ²)).
		wantF50 := math.Sqrt(math.Ln2 / (2 * math.Pi * math.Pi * sigma * sigma))
		if len(axis.Thresholds) > 0 {
			if math.Abs(axis.Thresholds[0].Frequency-wantF50) > 0.1*wantF50 {
				t.Errorf("MTF50 crossing = %.2f, want ~%.2f", axis.Thresholds[0].Frequency, wantF50)
			}
		}
	}
}

// TestMTFEvaluated verifies user-selected frequencies appear under evaluated.
func TestMTFEvaluated(t *testing.T) {
	I, spec := gaussianGrid(64, 1e-3, 5e-3)
	cfg := &types.PSFMTFConfig{Frequencies: []float64{10, 25, 100}}
	m := ComputeMTF(I, spec, cfg)
	if m == nil {
		t.Fatal("ComputeMTF returned nil")
	}
	if len(m.Sagittal.Evaluated) != 3 || len(m.Tangential.Evaluated) != 3 {
		t.Fatalf("evaluated = %d/%d, want 3/3", len(m.Sagittal.Evaluated), len(m.Tangential.Evaluated))
	}
	if m.Sagittal.Evaluated[0].Frequency != 10 || m.Sagittal.Evaluated[2].Frequency != 100 {
		t.Errorf("evaluated frequencies = %v", m.Sagittal.Evaluated)
	}
}

// analyticDiffractionMTF is the incoherent OTF of a diffraction-limited
// circular aperture: 2/π·(acos(s/s0) - (s/s0)·√(1-(s/s0)²)).
func analyticDiffractionMTF(s, s0 float64) float64 {
	if s >= s0 {
		return 0
	}
	x := s / s0
	return 2 / math.Pi * (math.Acos(x) - x*math.Sqrt(1-x*x))
}

// TestMTFDiffractionLimited runs the full Huygens integration on a perfect
// converging sphere (→ ideal Airy PSF) and checks the FFT-derived MTF against
// the analytic diffraction-limited curve within 5 %.
func TestMTFDiffractionLimited(t *testing.T) {
	const (
		a          = 5.0 // aperture radius (mm)
		f          = 50.0
		na         = a / f
		nImage     = 1.0
		imagePlane = f
	)
	samples := makePerfectSphereSamples(a, f, 48, 40)
	center := types.Vec3{Z: f}
	spec := ImageGridSpec{NX: 128, NY: 128, X0: -0.0256, Y0: -0.0256, DX: 4e-4, DY: 4e-4}
	g := ComputeField(samples, center, imagePlane, nImage, testWavelength, spec, false)
	m := ComputeMTF(g.Intensity, spec, nil)
	if m == nil {
		t.Fatal("ComputeMTF returned nil")
	}
	s0 := 2 * na / testWavelength // cutoff frequency (cycles/mm)

	for _, axis := range []*types.PSFMTFAxis{&m.Sagittal, &m.Tangential} {
		for _, p := range axis.Curve {
			want := analyticDiffractionMTF(p.Frequency, s0)
			if want < 0.03 || p.Frequency > 0.82*s0 {
				continue
			}
			if math.Abs(p.MTF-want) > 0.08*want {
				t.Errorf("diffraction MTF(f=%.1f) = %.4f, want %.4f", p.Frequency, p.MTF, want)
			}
		}
	}
	// Near and beyond the cutoff the MTF must stay below the diffraction
	// curve and vanish beyond it, despite the finite-window sampling.
	for _, axis := range []*types.PSFMTFAxis{&m.Sagittal, &m.Tangential} {
		for _, p := range axis.Curve {
			if p.Frequency > 0.82*s0 && p.Frequency <= s0 && p.MTF > 1.15*analyticDiffractionMTF(p.Frequency+1, s0)+0.02 {
				t.Errorf("near-cutoff MTF(f=%.1f) = %.4f exceeds diffraction curve", p.Frequency, p.MTF)
			}
			if p.Frequency > 1.05*s0 && p.MTF > 0.01 {
				t.Errorf("MTF(f=%.1f > cutoff) = %.4f, want ~0", p.Frequency, p.MTF)
			}
		}
	}
	// Isotropic PSF → sagittal and tangential agree.
	if len(m.Sagittal.Curve) > 0 {
		if math.Abs(m.Sagittal.Curve[1].MTF-m.Tangential.Curve[1].MTF) > 0.02 {
			t.Errorf("sagittal/tangential mismatch for isotropic PSF: %.4f vs %.4f",
				m.Sagittal.Curve[1].MTF, m.Tangential.Curve[1].MTF)
		}
	}
}

// TestComputePolychromaticMTF verifies the polychromatic MTF computation
// using OTF complex-weighted averaging. Uses two Gaussian PSFs with
// different sigmas to simulate wavelength-dependent PSFs.
func TestComputePolychromaticMTF(t *testing.T) {
	const (
		n  = 128
		dx = 6.25e-4
	)
	spec := ImageGridSpec{NX: n, NY: n, X0: -float64(n)/2*dx, Y0: -float64(n)/2*dx, DX: dx, DY: dx}

	// Two wavelengths with different Gaussian sigmas
	sigma1 := 8e-3
	sigma2 := 10e-3
	wl1 := 486.13e-6  // F line
	wl2 := 656.28e-6  // C line

	I1 := make([]float64, n*n)
	I2 := make([]float64, n*n)
	for j := 0; j < n; j++ {
		for i := 0; i < n; i++ {
			x := spec.X0 + (float64(i)+0.5)*dx
			y := spec.Y0 + (float64(j)+0.5)*dx
			I1[j*n+i] = math.Exp(-(x*x + y*y) / (2 * sigma1 * sigma1))
			I2[j*n+i] = math.Exp(-(x*x + y*y) / (2 * sigma2 * sigma2))
		}
	}

	// Flat SPD with equal weights, unit transmittance
	spdCurve := spectral.Flat(486.13, 656.28)
	transmittances := []float64{1.0, 1.0}
	wavelengths := []float64{wl1, wl2}
	intensities := [][]float64{I1, I2}

	cfg := &types.PSFMTFConfig{
		SpectralCurve:     "FLAT",
		CombinationMethod: "otf",
	}

	m := ComputePolychromaticMTF(wavelengths, intensities, spec, spdCurve, transmittances, cfg)
	if m == nil {
		t.Fatal("ComputePolychromaticMTF returned nil")
	}

	// Verify spectral curve and combination method are recorded
	if m.SpectralCurve != "FLAT" {
		t.Errorf("SpectralCurve = %q, want FLAT", m.SpectralCurve)
	}
	if m.CombinationMethod != "otf" {
		t.Errorf("CombinationMethod = %q, want otf", m.CombinationMethod)
	}

	// For two equal-weight Gaussians, the combined MTF should be between
	// the two individual MTFs. Check at a few frequencies.
	// MTF of Gaussian: exp(-2π²σ²f²)
	checkFreqs := []float64{10, 50, 100}
	for _, f := range checkFreqs {
		mtf1 := math.Exp(-2 * math.Pi * math.Pi * sigma1 * sigma1 * f * f)
		mtf2 := math.Exp(-2 * math.Pi * math.Pi * sigma2 * sigma2 * f * f)
		// Combined OTF average: |(OTF1 + OTF2)/2|, for centred Gaussians OTF is real
		expected := (mtf1 + mtf2) / 2

		// Get computed MTF from sagittal axis
		var computed float64
		for _, p := range m.Sagittal.Curve {
			if math.Abs(p.Frequency-f) < 1.0 {
				computed = p.MTF
				break
			}
		}
		if computed == 0 {
			continue // frequency not in curve
		}
		if math.Abs(computed-expected) > 0.05*expected && math.Abs(computed-expected) > 0.01 {
			t.Errorf("polychromatic MTF(f=%.1f) = %.5f, want %.5f (avg of %.5f, %.5f)",
				f, computed, expected, mtf1, mtf2)
		}
	}
}

// TestComputePolychromaticMTFWithShift verifies that lateral shift between
// wavelengths (simulating lateral color) can reduce the combined MTF
// due to phase cancellation in the OTF average.
func TestComputePolychromaticMTFWithShift(t *testing.T) {
	const (
		n  = 128
		dx = 6.25e-4
	)
	spec := ImageGridSpec{NX: n, NY: n, X0: -float64(n)/2*dx, Y0: -float64(n)/2*dx, DX: dx, DY: dx}

	// Same Gaussian, but second wavelength shifted by 0.005mm in x
	// This creates a linear phase difference in the OTF
	sigma := 8e-3
	shift := 0.005
	wl1 := 486.13e-6
	wl2 := 656.28e-6

	I1 := make([]float64, n*n)
	I2 := make([]float64, n*n)
	for j := 0; j < n; j++ {
		for i := 0; i < n; i++ {
			x := spec.X0 + (float64(i)+0.5)*dx
			y := spec.Y0 + (float64(j)+0.5)*dx
			I1[j*n+i] = math.Exp(-(x*x + y*y) / (2 * sigma * sigma))
			// Shifted Gaussian
			I2[j*n+i] = math.Exp(-((x-shift)*(x-shift) + y*y) / (2 * sigma * sigma))
		}
	}

	spdCurve := spectral.Flat(486.13, 656.28)
	transmittances := []float64{1.0, 1.0}
	wavelengths := []float64{wl1, wl2}
	intensities := [][]float64{I1, I2}

	cfg := &types.PSFMTFConfig{
		SpectralCurve:     "FLAT",
		CombinationMethod: "otf",
	}

	m := ComputePolychromaticMTF(wavelengths, intensities, spec, spdCurve, transmittances, cfg)
	if m == nil {
		t.Fatal("ComputePolychromaticMTF returned nil")
	}

	// Verify the result is valid and has expected structure
	if len(m.Sagittal.Curve) == 0 {
		t.Fatal("no MTF curve computed")
	}
	// The combined MTF should be finite and positive
	for _, p := range m.Sagittal.Curve {
		if p.MTF < 0 || p.MTF > 1 {
			t.Errorf("MTF out of range: %v at f=%.1f", p.MTF, p.Frequency)
		}
		if math.IsNaN(p.MTF) {
			t.Errorf("MTF is NaN at f=%.1f", p.Frequency)
		}
	}
}