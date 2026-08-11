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
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 10.0, Material: types.Material{Key: "N-BK7"}, Diameter: diameter},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 100.0, Material: types.Material{}, Diameter: diameter},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: diameter},
	}
	surface.Precompute(surfaces)
	return types.System{Surfaces: surfaces}, gc
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
