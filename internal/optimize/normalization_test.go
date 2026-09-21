package optimize

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// TestNormalizationScale verifies the automatic per-kind normalization
// denominator: spot kinds use the Airy radius, other aberration kinds the
// wavelength, dimensionless kinds stay at 1, overrides win, and a missing Airy
// radius falls back to the wavelength.
func TestNormalizationScale(t *testing.T) {
	wl := 0.0005876
	airy := 0.0036

	for _, k := range []string{"spot_rms", "", "spot_rms_t", "spot_rms_worst", "spot_rms_weighted", "spot_ee_radius"} {
		if got := normalizationScale(k, wl, airy, nil); got != airy {
			t.Errorf("normalizationScale(%q) = %v, want Airy %v", k, got, airy)
		}
	}
	for _, k := range []string{"wavefront_rms_residual", "wavefront_sphere_rms", "wavefront_sphere_pv", "opd_rms", "lateral_color", "longitudinal_color"} {
		if got := normalizationScale(k, wl, airy, nil); got != wl {
			t.Errorf("normalizationScale(%q) = %v, want wavelength %v", k, got, wl)
		}
	}
	for _, k := range []string{"geometric_mtf_sag", "focal_length", "field_alive", "distortion_pct", "glass_role"} {
		if got := normalizationScale(k, wl, airy, nil); got != 1.0 {
			t.Errorf("normalizationScale(%q) = %v, want 1", k, got)
		}
	}

	// No Airy radius (no NA) -> spot kinds fall back to the wavelength.
	if got := normalizationScale("spot_rms", wl, 0, nil); got != wl {
		t.Errorf("spot without Airy = %v, want wavelength %v", got, wl)
	}

	// A per-kind override wins over the automatic scale.
	scales := map[string]float64{"spot_rms": 0.003}
	if got := normalizationScale("spot_rms", wl, airy, scales); got != 0.003 {
		t.Errorf("override = %v, want 0.003", got)
	}
}

// TestNormScaleFor verifies the resolution precedence: explicit term scale,
// then the automatic scale when enabled, else 1.
func TestNormScaleFor(t *testing.T) {
	wl := 0.0005876
	airy := 0.0036

	if got := normScaleFor("spot_rms", 0, wl, airy, nil); got != 1.0 {
		t.Errorf("nil config = %v, want 1", got)
	}
	if got := normScaleFor("spot_rms", 0, wl, airy, &types.MeritNormalizationConfig{Enabled: false}); got != 1.0 {
		t.Errorf("disabled = %v, want 1", got)
	}
	enabled := &types.MeritNormalizationConfig{Enabled: true}
	if got := normScaleFor("spot_rms", 0, wl, airy, enabled); got != airy {
		t.Errorf("enabled spot = %v, want Airy %v", got, airy)
	}
	if got := normScaleFor("wavefront_rms_residual", 0, wl, airy, enabled); got != wl {
		t.Errorf("enabled wavefront = %v, want wavelength %v", got, wl)
	}
	// An explicit term scale always wins, even when normalization is off.
	if got := normScaleFor("spot_rms", 0.01, wl, airy, enabled); got != 0.01 {
		t.Errorf("explicit scale = %v, want 0.01", got)
	}
	if got := normScaleFor("spot_rms", 0.01, wl, airy, nil); got != 0.01 {
		t.Errorf("explicit scale (disabled) = %v, want 0.01", got)
	}
	// A non-positive explicit scale is ignored.
	if got := normScaleFor("spot_rms", -1, wl, airy, enabled); got != airy {
		t.Errorf("negative explicit scale = %v, want Airy %v", got, airy)
	}
	// A zero term wavelength for a wave-scaled kind falls back to the default.
	if got := normScaleFor("wavefront_rms_residual", 0, 0, 0, enabled); got != types.DefaultWavelength {
		t.Errorf("zero wavelength = %v, want %v", got, types.DefaultWavelength)
	}
}

// TestResolveNormScale verifies the multi-config (ConfigInput) wrapper uses the
// reference wavelength when the term omits one.
func TestResolveNormScale(t *testing.T) {
	wl := 0.0005876
	airy := 0.0036
	ci := ConfigInput{
		ReferenceWavelength: wl,
		Normalization:       &types.MeritNormalizationConfig{Enabled: true},
	}
	if got := resolveNormScale(types.MeritTerm{Kind: "spot_rms"}, ci, airy); got != airy {
		t.Errorf("spot term = %v, want Airy %v", got, airy)
	}
	if got := resolveNormScale(types.MeritTerm{Kind: "wavefront_rms_residual"}, ci, airy); math.Abs(got-wl) > 1e-15 {
		t.Errorf("wavefront term = %v, want the reference %v", got, wl)
	}
	if got := resolveNormScale(types.MeritTerm{Kind: "wavefront_rms_residual", Wavelength: 0.0004861}, ci, airy); got != 0.0004861 {
		t.Errorf("explicit term wavelength = %v, want 0.0004861", got)
	}
	if got := resolveNormScale(types.MeritTerm{Kind: "spot_rms", Scale: 0.002}, ci, airy); got != 0.002 {
		t.Errorf("explicit term scale = %v, want 0.002", got)
	}
}

// TestNormalizationScalesMerit verifies the normalization is actually wired
// through NewOptimizer into the merit: enabling it divides the spot residual by
// the Airy radius, so the merit grows by 1/airy^2.
func TestNormalizationScalesMerit(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 10.0, Material: types.Material{Key: "N-BK7"}, Diameter: 200.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 100.0, Material: types.Material{}, Diameter: 200.0},
	}
	surface.Precompute(surfaces)

	airy := airyRadiusMM(surfaces, 0, nil, gc, types.DefaultWavelength)
	if airy <= 0 {
		t.Skip("no image-space NA for the test system")
	}

	terms := []MeritTerm{{Kind: MeritSpotRMS, FieldAngle: 16.0, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0}}
	run := func(norm *types.MeritNormalizationConfig) float64 {
		cfg := Config{Surfaces: surfaces, MeritTerms: terms, GlassCatalog: gc, NumRays: 16, Normalization: norm}
		opt := NewOptimizer(cfg)
		return opt.EvaluateMerit(opt.getInitialState())
	}

	off := run(nil)
	if !(off > 0) {
		t.Fatalf("unnormalized merit = %v, want > 0", off)
	}
	on := run(&types.MeritNormalizationConfig{Enabled: true})
	want := off / (airy * airy)
	if math.Abs(on-want) > 1e-3*want {
		t.Errorf("normalized merit = %v, want %v (= off/airy^2)", on, want)
	}
}
