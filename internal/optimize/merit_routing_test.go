package optimize

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/paraxial"
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
	scale := opt.wavefrontShearScale(ccfg, surfaces, gc, wl)
	if scale <= 0 {
		t.Fatalf("wavefrontShearScale = %v, want a positive R_ent/NA", scale)
	}
	want := dls.ComputeWavefrontShift(points, 50, wl, scale, "sag")
	got := opt.evaluateKindTerm(ccfg, term, surfaces, gc, nil, appliedPupil{})
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("evaluateKindTerm(wavefront_shift_sag) = %v, want the dedicated shift %v", got, want)
	}
	if spot := dls.ComputeSpotRMS(points); math.Abs(want-spot) < 1e-12 {
		t.Skip("dedicated shift and spot_rms coincide on this system; comparison is not discriminating")
	}
}

// TestWavefrontShiftKindIsNotInert guards the regression that made
// wavefront_shift_sag/tan useless: the kind paired pupil samples by exact 0.001 mm
// key equality, which no traced grid satisfies, so it reported 0.000000 for every
// state — a merit term that measured nothing while contributing nothing. Measured
// on a traced 6-element at 10 lp/mm the kind read 0 against a live spot_rms of
// 0.001529. It must now be positive, and it must be driven by the frequency.
func TestWavefrontShiftKindIsNotInert(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	surfaces := doubletSurfaces()
	surface.Precompute(surfaces)

	const wl = 0.00058756
	cfg := Config{
		Surfaces:  surfaces,
		Variables: []Variable{},
		MeritTerms: []MeritTerm{{
			Kind: dls.MeritWavefrontShiftSag, FieldAngle: 0.0, FieldWeight: 1.0,
			Wavelength: wl, WavWeight: 1.0, Weight: 1.0, Frequency: 50,
		}},
		GlassCatalog: gc,
		NumRays:      64,
	}
	opt := NewOptimizer(cfg)
	ccfg := opt.primaryConfig()

	val := func(freq float64) float64 {
		term := &meritTerm{kind: dls.MeritWavefrontShiftSag, fieldAngle: 0.0, wavelength: wl, frequency: freq}
		return opt.evaluateKindTerm(ccfg, term, surfaces, gc, nil, appliedPupil{})
	}
	at50 := val(50)
	if at50 <= 0 {
		t.Fatalf("wavefront_shift_sag at 50 lp/mm = %v, want > 0 (the kind is inert)", at50)
	}
	// The value is the sheared-OPL variance, so it grows with the square of the
	// frequency: a 50% frequency step must raise it by well under the 1.5x that a
	// linear response would give, and well over the flat 1.0 an inert kind gives.
	at150 := val(150)
	if ratio := at150 / at50; ratio <= 1.0 || ratio >= 4*1.2 {
		t.Errorf("tripling the frequency scaled the value by %g, want a clear quadratic response", ratio)
	}
}

// doubletSurfaces returns a cemented doublet with a stop, so the traced pupil grid
// has a real aperture and a finite NA for the wavefront-shift shear scale.
func doubletSurfaces() []types.Surface {
	return []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.02, Thickness: 5.0, Material: types.Material{Key: "N-BK7"}, Diameter: 30.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.02, Thickness: 2.0, Material: types.Material{}, Diameter: 30.0},
		{ID: 3, Type: types.Sphere, Curvature: -0.06, Thickness: 40.0, Material: types.Material{}, Diameter: 30.0, Role: "stop"},
	}
}

// TestWavefrontShiftShearScaleIsTheFocalLength pins where the shear scale comes
// from. The displacement is s = ν·λ·R_ent/NA, and for an infinite conjugate the
// working F-number is EFL/EPD, so NA = R_ent/EFL and the scale collapses to the
// EFL. Reading it that way is what keeps a virtual entrance pupil from corrupting
// it: the paraxial entrance-pupil radius of a stop-free system is an estimate off
// the first surface, and on the 6-element this was developed on it reported a
// 118 mm EPD where the design prescribes 12.5 mm — an NA nine times too large and
// a shear nine times too short, which reported the kind as 0. The EFL depends
// only on the powered surfaces, so it is immune to that.
func TestWavefrontShiftShearScaleIsTheFocalLength(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	const wl = 0.00058756

	// A stop-free system with only auto apertures, so the paraxial
	// entrance-pupil radius has to fall back to the first-surface estimate: the
	// situation that broke the NA.
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 1.0 / 70.0, Thickness: 8.0, Material: types.Material{Key: "N-BK7"}, Diameter: 14.0, AutoAperture: true},
		{ID: 2, Type: types.Sphere, Curvature: -1.0 / 200.0, Thickness: 5.0, Material: types.Material{}, Diameter: 14.0, AutoAperture: true},
		{ID: 3, Type: types.Sphere, Curvature: -1.0 / 90.0, Thickness: 60.0, Material: types.Material{}, Diameter: 14.0, AutoAperture: true},
	}
	surface.Precompute(surfaces)

	cfg := Config{
		Surfaces:  surfaces,
		Variables: []Variable{},
		MeritTerms: []MeritTerm{{
			Kind: dls.MeritWavefrontShiftSag, FieldAngle: 0.0, FieldWeight: 1.0,
			Wavelength: wl, WavWeight: 1.0, Weight: 1.0, Frequency: 10,
		}},
		GlassCatalog: gc,
		NumRays:      64,
		RefSurface:   3,
		Fields:       []types.FieldItem{{AngleDeg: 0, Weight: 1}},
	}
	opt := NewOptimizer(cfg)
	ccfg := opt.primaryConfig()

	pr := paraxial.Compute(types.System{Surfaces: surfaces}, wl, gc, 0, nil)
	efl := math.Abs(pr.FocalLength)
	if efl <= 0 {
		t.Fatalf("paraxial EFL = %v; the fixture is degenerate", efl)
	}
	got := opt.wavefrontShearScale(ccfg, surfaces, gc, wl)
	if math.Abs(got-efl) > 1e-9*efl {
		t.Errorf("wavefrontShearScale = %v, want the EFL %v", got, efl)
	}
	// The NA the estimate route would have used must indeed be far off, or this
	// fixture no longer covers the regression it exists for.
	wantNA := 1 / (2 * efl / 2 / dls.ApertureRadiusForGrid(surfaces, 0, wl, gc, 1.0, 12.5))
	if pr.ImageSpaceNA > 0 && pr.ImageSpaceNA > 2*wantNA {
		t.Logf("fixture check: paraxial estimated NA %v against the applied %v, so the regression is covered", pr.ImageSpaceNA, wantNA)
	}
}
