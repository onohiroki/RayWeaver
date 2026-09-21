package optimize

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// TestNormalizationScalesMerit verifies the normalization is actually wired
// through NewOptimizer into the merit: enabling it divides the spot residual by
// the wavelength, so the merit grows by 1/lambda^2.
func TestNormalizationScalesMerit(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 10.0, Material: types.Material{Key: "N-BK7"}, Diameter: 200.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 100.0, Material: types.Material{}, Diameter: 200.0},
	}
	surface.Precompute(surfaces)

	wl := 0.00058756
	terms := []MeritTerm{{Kind: MeritSpotRMS, FieldAngle: 16.0, FieldWeight: 1.0, Wavelength: wl, WavWeight: 1.0, Weight: 1.0}}
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
	want := off / (wl * wl)
	if math.Abs(on-want) > 1e-3*want {
		t.Errorf("normalized merit = %v, want %v (= off/lambda^2)", on, want)
	}
}

// TestNormalizationScale verifies the automatic per-kind normalization
// denominator: wave-scaled aberration kinds use the wavelength, dimensionless
// kinds stay at 1, overrides win, and a zero wavelength falls back to 1.
func TestNormalizationScale(t *testing.T) {
	wl := 0.0005876
	cases := []struct {
		kind string
		want float64
	}{
		{"spot_rms", wl},
		{"", wl}, // legacy empty kind == spot_rms
		{"spot_rms_t", wl},
		{"spot_rms_worst", wl},
		{"spot_rms_weighted", wl},
		{"spot_ee_radius", wl},
		{"wavefront_rms_residual", wl},
		{"wavefront_sphere_rms", wl},
		{"wavefront_sphere_pv", wl},
		{"opd_rms", wl},
		{"lateral_color", wl},
		{"longitudinal_color", wl},
		{"geometric_mtf_sag", 1.0},
		{"focal_length", 1.0},
		{"field_alive", 1.0},
		{"distortion_pct", 1.0},
		{"glass_role", 1.0},
	}
	for _, c := range cases {
		if got := normalizationScale(c.kind, wl, nil); math.Abs(got-c.want) > 1e-15 {
			t.Errorf("normalizationScale(%q) = %v, want %v", c.kind, got, c.want)
		}
	}

	// A per-kind override wins over the automatic scale.
	scales := map[string]float64{"spot_rms": 0.003}
	if got := normalizationScale("spot_rms", wl, scales); got != 0.003 {
		t.Errorf("override = %v, want 0.003", got)
	}

	// A non-positive wavelength cannot be normalized -> 1.
	if got := normalizationScale("spot_rms", 0, nil); got != 1.0 {
		t.Errorf("zero wavelength = %v, want 1", got)
	}
}

// TestNormScaleFor verifies the resolution precedence: explicit term scale,
// then the automatic scale when enabled, else 1.
func TestNormScaleFor(t *testing.T) {
	wl := 0.0005876

	if got := normScaleFor("spot_rms", 0, wl, nil); got != 1.0 {
		t.Errorf("nil config = %v, want 1", got)
	}
	if got := normScaleFor("spot_rms", 0, wl, &types.MeritNormalizationConfig{Enabled: false}); got != 1.0 {
		t.Errorf("disabled = %v, want 1", got)
	}
	enabled := &types.MeritNormalizationConfig{Enabled: true}
	if got := normScaleFor("spot_rms", 0, wl, enabled); got != wl {
		t.Errorf("enabled spot = %v, want %v", got, wl)
	}
	// An explicit term scale always wins, even when normalization is off.
	if got := normScaleFor("spot_rms", 0.01, wl, enabled); got != 0.01 {
		t.Errorf("explicit scale = %v, want 0.01", got)
	}
	if got := normScaleFor("spot_rms", 0.01, wl, nil); got != 0.01 {
		t.Errorf("explicit scale (disabled) = %v, want 0.01", got)
	}
	// A non-positive explicit scale is ignored.
	if got := normScaleFor("spot_rms", -1, wl, enabled); got != wl {
		t.Errorf("negative explicit scale = %v, want %v", got, wl)
	}
	// A zero term wavelength falls back to the config reference wavelength.
	if got := normScaleFor("spot_rms", 0, 0, enabled); got != types.DefaultWavelength {
		t.Errorf("zero wavelength = %v, want %v", got, types.DefaultWavelength)
	}
}

// TestResolveNormScale verifies the multi-config (ConfigInput) wrapper uses the
// reference wavelength when the term omits one.
func TestResolveNormScale(t *testing.T) {
	wl := 0.0005876
	ci := ConfigInput{
		ReferenceWavelength: wl,
		Normalization:       &types.MeritNormalizationConfig{Enabled: true},
	}
	if got := resolveNormScale(types.MeritTerm{Kind: "spot_rms"}, ci); math.Abs(got-wl) > 1e-15 {
		t.Errorf("term wavelength unset = %v, want the reference %v", got, wl)
	}
	if got := resolveNormScale(types.MeritTerm{Kind: "spot_rms", Wavelength: 0.0004861}, ci); got != 0.0004861 {
		t.Errorf("explicit term wavelength = %v, want 0.0004861", got)
	}
	if got := resolveNormScale(types.MeritTerm{Kind: "spot_rms", Scale: 0.002}, ci); got != 0.002 {
		t.Errorf("explicit term scale = %v, want 0.002", got)
	}
}
