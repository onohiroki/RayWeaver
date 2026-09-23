package optimize

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// TestSpotNormalizationUsesTermWavelength guards the P8 fix: the Airy radius
// used to normalize spot terms was computed once at the default wavelength and
// reused for every term, biasing the 486/656 nm terms by ~11%. Each term must
// now normalize by the Airy radius at its own wavelength (proportional to it).
func TestSpotNormalizationUsesTermWavelength(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	surfaces := singletSurfaces()
	surface.Precompute(surfaces)
	if airyRadiusMM(surfaces, 0, nil, gc, types.DefaultWavelength) <= 0 {
		t.Skip("no valid Airy radius for the singlet; the test cannot discriminate")
	}

	cfg := Config{
		Surfaces:  surfaces,
		Variables: []Variable{},
		MeritTerms: []MeritTerm{
			{Kind: dls.MeritSpotRMS, FieldAngle: 0, FieldWeight: 1, Wavelength: 0.0004861, WavWeight: 1, Weight: 1},
			{Kind: dls.MeritSpotRMS, FieldAngle: 0, FieldWeight: 1, Wavelength: 0.0006563, WavWeight: 1, Weight: 1},
		},
		GlassCatalog:  gc,
		NumRays:       16,
		Normalization: &types.MeritNormalizationConfig{Enabled: true},
	}
	opt := NewOptimizer(cfg)
	ts := opt.primaryConfig().meritTerms
	if len(ts) != 2 {
		t.Fatalf("got %d merit terms, want 2", len(ts))
	}
	if ts[0].normScale <= 0 || ts[1].normScale <= 0 {
		t.Fatalf("normScale not set: %v %v", ts[0].normScale, ts[1].normScale)
	}
	want0 := airyRadiusMM(surfaces, 0, nil, gc, 0.0004861)
	want1 := airyRadiusMM(surfaces, 0, nil, gc, 0.0006563)
	if want0 == want1 {
		t.Fatalf("per-wavelength Airy radii coincide (%v); the test cannot discriminate", want0)
	}
	if math.Abs(ts[0].normScale-want0) > 1e-12 || math.Abs(ts[1].normScale-want1) > 1e-12 {
		t.Errorf("normScale = (%v, %v), want the per-wavelength Airy radii (%v, %v)",
			ts[0].normScale, ts[1].normScale, want0, want1)
	}
}
