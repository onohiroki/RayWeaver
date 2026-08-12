package psf

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// singletSystem builds a simple biconvex singlet (R=100 BK7 10mm, R=-100) with
// a flat image plane, all in air. The stop aperture is the fixed surface
// diameter (dynamic pupil).
func singletSystem(diameter float64) (types.System, *glass.Catalog) {
	return singletSystemBFL(diameter, 100)
}

// singletSystemBFL builds the same singlet with the image plane placed bfl mm
// behind the rear vertex (surface 2). Paraxial back focal length ≈ 95.1 mm,
// so bfl > 95.1 puts the image plane behind focus (defocus-dominated).
func singletSystemBFL(diameter, bfl float64) (types.System, *glass.Catalog) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 10.0, Material: types.Material{Key: "N-BK7"}, Diameter: diameter},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: bfl, Material: types.Material{}, Diameter: diameter},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: diameter},
	}
	surface.Precompute(surfaces)
	return types.System{Surfaces: surfaces}, gc
}

// TestPSFBestFocusDefocusDominance verifies that a low on-axis fixed-plane
// Strehl is caused by image-plane defocus (field curvature), not by the
// Huygens model: the same on-axis field evaluated at its best-focus image
// plane (the geometric spot-RMS minimum, as in the wavefront command) must
// recover a near-diffraction-limited Strehl, and the recovered shift must
// match the deliberate back-focal displacement. This is the property that
// makes psf --best-focus and the wavefront best-focus metrics agree.
func TestPSFBestFocusDefocusDominance(t *testing.T) {
	// Image plane 3 mm behind the ~95.1 mm paraxial focus: defocused on-axis.
	sys, gc := singletSystemBFL(8, 98.1)
	fields := []types.FieldDef{{Angle: 0, Direction: []float64{0, 1}}}
	wl := []float64{testWavelength}

	fixed, err := Compute(sys, gc, fields, wl, Options{NumRays: 200})
	if err != nil {
		t.Fatal(err)
	}
	bf, err := Compute(sys, gc, fields, wl, Options{NumRays: 200, BestFocus: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(fixed) == 0 || len(bf) == 0 {
		t.Fatal("no result")
	}
	rf, rb := fixed[0], bf[0]
	if rf.Strehl >= rb.Strehl {
		t.Errorf("fixed-plane Strehl %v must be below best-focus Strehl %v", rf.Strehl, rb.Strehl)
	}
	if rb.Strehl <= 0.5 {
		t.Errorf("best-focus on-axis Strehl %v should be near diffraction limited", rb.Strehl)
	}
	if math.Abs(rb.BestFocusShift) < 2.0 {
		t.Errorf("best-focus shift %v mm should recover the ~3 mm deliberate defocus", rb.BestFocusShift)
	}
	if rb.SpotRMS >= rf.SpotRMS {
		t.Errorf("best-focus spot RMS %v must be below fixed-plane %v", rb.SpotRMS, rf.SpotRMS)
	}
}

// TestPSFRealSinglet runs the full pipeline (polarized ray tracing → wavefront
// → Huygens) on a real singlet and checks the on-axis PSF is finite and
// diffraction-scale: Strehl in (0, 1], positive peak/FWHM/Airy, and a FWHM
// comparable to the Airy core.
func TestPSFRealSinglet(t *testing.T) {
	fields := []types.FieldDef{{Angle: 0, Direction: []float64{0, 1}}}
	wl := []float64{testWavelength}

	sys, gc := singletSystem(8)
	res, err := Compute(sys, gc, fields, wl, Options{NumRays: 300})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) == 0 {
		t.Fatal("no result")
	}
	r := res[0]
	if r.Strehl <= 0 || r.Strehl > 1.0 {
		t.Errorf("on-axis singlet Strehl = %v, want in (0, 1]", r.Strehl)
	}
	if r.PeakValue <= 0 {
		t.Errorf("peak value = %v, want > 0", r.PeakValue)
	}
	if r.AiryRadius <= 0 {
		t.Errorf("Airy radius = %v, want > 0", r.AiryRadius)
	}
	if r.FWHMX <= 0 || r.FWHMY <= 0 {
		t.Errorf("FWHM = (%v, %v), want > 0", r.FWHMX, r.FWHMY)
	}
	// Airy FWHM ≈ 1.03·0.61λ/NA for a circular aperture; allow aberration
	// margin (FWHM should not vastly exceed the Airy core).
	if r.FWHMX > 2.0*r.AiryRadius {
		t.Errorf("FWHM_x = %v exceeds 2× Airy radius %v (PSF too wide?)", r.FWHMX, r.AiryRadius)
	}
}

// TestPSFApertureScaling verifies the diffraction core shrinks as the
// aperture grows: the Airy radius at a small aperture must be larger than at a
// big one, and the FWHM must be a finite positive length.
func TestPSFApertureScaling(t *testing.T) {
	fields := []types.FieldDef{{Angle: 0, Direction: []float64{0, 1}}}
	wl := []float64{testWavelength}

	sysBig, gcBig := singletSystem(16)
	sysSmall, gcSmall := singletSystem(8)
	resBig, err := Compute(sysBig, gcBig, fields, wl, Options{NumRays: 200})
	if err != nil || len(resBig) == 0 {
		t.Fatalf("big aperture compute failed: %v", err)
	}
	resSmall, err := Compute(sysSmall, gcSmall, fields, wl, Options{NumRays: 200})
	if err != nil || len(resSmall) == 0 {
		t.Fatalf("small aperture compute failed: %v", err)
	}
	if !(resSmall[0].AiryRadius > resBig[0].AiryRadius) {
		t.Errorf("smaller aperture should have larger Airy radius: %v vs %v",
			resSmall[0].AiryRadius, resBig[0].AiryRadius)
	}
	if math.Abs(resBig[0].FWHMX) < 1e-6 || math.IsNaN(resBig[0].FWHMX) {
		t.Errorf("big aperture FWHM = %v", resBig[0].FWHMX)
	}
}

// TestPSFWhite runs the polychromatic (white) pipeline on the singlet: the
// result must carry a spectral curve, per-wavelength contributions and an MTF,
// and the Strehl must stay physical.
func TestPSFWhite(t *testing.T) {
	fields := []types.FieldDef{{Angle: 0, Direction: []float64{0, 1}}}
	wls := []float64{0.00045, 0.00055, 0.00065}

	sys, gc := singletSystem(8)
	res, err := Compute(sys, gc, fields, wls, Options{NumRays: 120, SpectralCurve: "FLAT"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) == 0 {
		t.Fatal("no white result")
	}
	r := res[0]
	if r.SpectralCurve != "FLAT" {
		t.Errorf("SpectralCurve = %q, want FLAT", r.SpectralCurve)
	}
	if len(r.Contributions) != 3 {
		t.Errorf("contributions = %d, want 3 (one per wavelength)", len(r.Contributions))
	}
	for _, c := range r.Contributions {
		if c.SpectralWeight != 1 {
			t.Errorf("FLAT weight at %v = %v, want 1", c.Wavelength, c.SpectralWeight)
		}
		if c.MTF == nil {
			t.Errorf("contribution %v has no MTF", c.Wavelength)
		}
	}
	if r.MTF == nil {
		t.Fatal("white result has no MTF")
	}
	if len(r.MTF.Sagittal.Thresholds) != 3 || len(r.MTF.Tangential.Thresholds) != 3 {
		t.Errorf("default MTF thresholds = %d/%d, want 3/3",
			len(r.MTF.Sagittal.Thresholds), len(r.MTF.Tangential.Thresholds))
	}
	if r.Strehl <= 0 || r.Strehl > 1.0 {
		t.Errorf("white Strehl = %v, want in (0, 1]", r.Strehl)
	}
	if r.Transmittance <= 0 || r.Transmittance > 1 {
		t.Errorf("white transmittance = %v, want in (0, 1]", r.Transmittance)
	}
}
