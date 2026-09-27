package optimize

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// apertureSizingSurfaces is a singlet with an image plane (surface 3), the last
// surface being the one whose footprint is the most field-dependent and so the
// first to be clipped by a short diameter.
func apertureSizingSurfaces() []types.Surface {
	surfs := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.02, Thickness: 5.0, Material: types.Material{Key: "N-BK7"}, Diameter: 30.0, AutoAperture: true},
		{ID: 2, Type: types.Sphere, Curvature: -0.02, Thickness: 40.0, Material: types.Material{}, Diameter: 30.0, AutoAperture: true},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 30.0, AutoAperture: true},
	}
	surface.Precompute(surfs)
	return surfs
}

func apertureSizingGC() *glass.Catalog {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	return gc
}

// TestApertureSizingWavelengths verifies that the auto-aperture sizing covers
// every config wavelength (plus the reference when it is not declared), so the
// sized diameters envelope the widest footprint rather than a single
// representative one. A single-wavelength sizing can return a diameter below
// the same surface's footprint at another wavelength and clip the field.
func TestApertureSizingWavelengths(t *testing.T) {
	cfg := &config{
		referenceWavelength: 0.00058756,
		wavelengths: []types.WavelengthItem{
			{ID: 0, Value: 0.0004861},
			{ID: 1, Value: 0.0005876},
			{ID: 2, Value: 0.0006563},
		},
	}
	got := apertureSizingWavelengths(cfg)
	if len(got) != 3 {
		t.Fatalf("apertureSizingWavelengths = %v, want all 3 config wavelengths", got)
	}
	for i, want := range []float64{0.0004861, 0.0005876, 0.0006563} {
		if math.Abs(got[i]-want) > 1e-12 {
			t.Errorf("wavelength[%d] = %v, want %v", i, got[i], want)
		}
	}

	// A reference wavelength outside the declared list is appended, not dropped:
	// the back-focus / chief passes evaluate at it.
	extra := &config{
		referenceWavelength: 0.00055,
		wavelengths:         []types.WavelengthItem{{ID: 0, Value: 0.0004861}},
	}
	got = apertureSizingWavelengths(extra)
	if len(got) != 2 || math.Abs(got[1]-0.00055) > 1e-12 {
		t.Errorf("apertureSizingWavelengths with an undeclared reference = %v, want the config list plus 0.00055", got)
	}

	// A duplicate is folded, and a config without wavelengths still yields the
	// reference.
	only := &config{referenceWavelength: 0.00058756, wavelengths: []types.WavelengthItem{{ID: 0, Value: 0.00058756}}}
	if got = apertureSizingWavelengths(only); len(got) != 1 {
		t.Errorf("duplicate reference folded wrong: %v", got)
	}
	if got = apertureSizingWavelengths(&config{}); len(got) != 1 || math.Abs(got[0]-types.DefaultWavelength) > 1e-12 {
		t.Errorf("apertureSizingWavelengths with no config = %v, want the default wavelength", got)
	}
}

// TestFinalAutoAperturesCoversAllWavelengths verifies that the output sizing
// envelopes every config wavelength: the image-plane diameter it produces must
// be at least the widest per-wavelength footprint plus the clearance, so
// re-tracing the sized lens at any config wavelength does not clip the field.
func TestFinalAutoAperturesCoversAllWavelengths(t *testing.T) {
	gc := apertureSizingGC()
	const fieldAngle = 12.0
	const refSurface = 3
	fields := []types.FieldItem{{ID: 0, AngleDeg: fieldAngle, Weight: 1}}
	wls := []float64{0.0004861, 0.0005876, 0.0006563}

	// Widest footprint per wavelength, measured with no aperture limit.
	surfs := apertureSizingSurfaces()
	for i := range surfs {
		surfs[i].Diameter = 200.0
	}
	surface.Precompute(surfs)
	opt := NewOptimizer(Config{Surfaces: surfs, GlassCatalog: gc, NumRays: 256})
	cfg := opt.primaryConfig()
	cfg.refSurface = refSurface
	cfg.fieldDefs = fieldDefsFromItems(fields)
	cfg.wavelengths = []types.WavelengthItem{
		{ID: 0, Value: wls[0]}, {ID: 1, Value: wls[1]}, {ID: 2, Value: wls[2]},
	}
	cfg.referenceWavelength = wls[1]
	p := appliedPupil{}

	// Per-wavelength image-plane extent via the same helper the sizing uses.
	perWL := make([]float64, len(wls))
	envCfg := *cfg
	for i, wl := range wls {
		envCfg.wavelengths = []types.WavelengthItem{{ID: 0, Value: wl}}
		probe := append([]types.Surface(nil), surfs...)
		surface.Precompute(probe)
		opt.finalAutoApertures(&envCfg, probe, gc, p)
		perWL[i] = probe[2].Diameter
		if perWL[i] <= 0 {
			t.Fatalf("wavelength %v: image plane not sized (diameter %v)", wl, probe[2].Diameter)
		}
	}
	wantMax := 0.0
	for _, d := range perWL {
		if d > wantMax {
			wantMax = d
		}
	}

	// One sizing pass over all wavelengths must return the widest of them.
	all := append([]types.Surface(nil), surfs...)
	surface.Precompute(all)
	opt.finalAutoApertures(cfg, all, gc, p)
	got := all[2].Diameter
	if got < wantMax-1e-9 {
		t.Errorf("image-plane diameter = %v, want >= the widest single-wavelength sizing %v (per wavelength %v)",
			got, wantMax, perWL)
	}
}

// TestFieldDefsCarryVignetting verifies the aperture-sizing / back-focus field
// definitions keep each field's declared vignetting ellipse, so those passes
// trace the same (vignetted) pupil the merit grid does. Dropping it made the
// footprint measurement envelope the full pupil while the merit evaluated the
// vignetted one.
func TestFieldDefsCarryVignetting(t *testing.T) {
	clip := types.VignettingDef{CompressionX: 0.1056, CompressionY: 0.1056}
	items := []types.FieldItem{
		{ID: 0, AngleDeg: 0, Weight: 1},
		{ID: 1, AngleDeg: 20, Weight: 1, Vignetting: &clip},
	}
	fds := fieldDefsFromItems(items)
	if len(fds) != 2 {
		t.Fatalf("fieldDefsFromItems returned %d defs, want 2", len(fds))
	}
	if fds[0].Vignetting != nil {
		t.Errorf("field 0 vignetting = %+v, want nil", fds[0].Vignetting)
	}
	if fds[1].Vignetting == nil {
		t.Fatal("field 1 vignetting dropped: the sizing/back-focus passes would trace the full pupil")
	}
	if fds[1].Vignetting.CompressionX != clip.CompressionX || fds[1].Vignetting.CompressionY != clip.CompressionY {
		t.Errorf("field 1 vignetting = %+v, want %+v", *fds[1].Vignetting, clip)
	}
}
