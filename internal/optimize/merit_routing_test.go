package optimize

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// TestWavefrontShiftKindsRouteToDedicatedEvaluator guards the P4 fix: the
// wavefront-shift / pair-phase merit kinds consume a pupil-grid trace but have
// their own evaluators. Listing them in isGridKind routed them to
// evaluateGridKind, whose switch has no case for them, so they were silently
// evaluated as spot_rms and their dedicated implementations were dead.
func TestWavefrontShiftKindsRouteToDedicatedEvaluator(t *testing.T) {
	kinds := []string{dls.MeritWavefrontShiftSag, dls.MeritWavefrontShiftTan, dls.MeritWavefrontPairPhase}
	for _, k := range kinds {
		if isGridKind(k) {
			t.Errorf("isGridKind(%q) = true; the kind would be evaluated as spot_rms", k)
		}
		if !isGridTraceKind(k) {
			t.Errorf("isGridTraceKind(%q) = false; the kind still needs a grid trace", k)
		}
	}
	// Spot kinds stay grid kinds (and trace kinds).
	for _, k := range []string{"", dls.MeritSpotRMS, dls.MeritSpotRMST} {
		if !isGridKind(k) || !isGridTraceKind(k) {
			t.Errorf("spot kind %q must be both a grid kind and a trace kind", k)
		}
	}

	// Functional check: evaluateKindTerm must return the dedicated shift value,
	// not the spot RMS.
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	surfaces := singletSurfaces()
	surface.Precompute(surfaces)

	const wl = 0.00058756
	cfg := Config{
		Surfaces:  surfaces,
		Variables: []Variable{},
		MeritTerms: []MeritTerm{{
			Kind: dls.MeritWavefrontShiftSag, FieldAngle: 10.0, FieldWeight: 1.0,
			Wavelength: wl, WavWeight: 1.0, Weight: 1.0, Frequency: 50,
		}},
		GlassCatalog: gc,
		NumRays:      32,
	}
	opt := NewOptimizer(cfg)
	ccfg := opt.primaryConfig()
	term := &meritTerm{kind: dls.MeritWavefrontShiftSag, fieldAngle: 10.0, wavelength: wl, frequency: 50}

	points := opt.gridForTerm(nil, gc, surfaces, ccfg, term, appliedPupil{})
	if len(points) == 0 {
		t.Skip("no grid points for the singlet; cannot compare")
	}
	apR := dls.ApertureRadiusForGrid(surfaces, ccfg.stopSurface, wl, gc, opt.apertureMargin, 0)
	want := dls.ComputeWavefrontShift(points, 50, wl, apR, "sag")
	got := opt.evaluateKindTerm(ccfg, term, surfaces, gc, nil, appliedPupil{})
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("evaluateKindTerm(wavefront_shift_sag) = %v, want the dedicated shift %v", got, want)
	}
	if spot := dls.ComputeSpotRMS(points); math.Abs(want-spot) < 1e-12 {
		t.Skip("dedicated shift and spot_rms coincide on this system; comparison is not discriminating")
	}
}
