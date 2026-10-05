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

const spotFlatWL = 0.00058756

// spotFlatField is the off-axis test field. Kept modest (10 degrees): the
// conversion from fitted slope to plane shift is the paraxial one (B = |EFL|),
// whose per-field error grows with the tangential/sagittal focus separation of
// an uncorrected lens, and this fixture is a bare singlet.
const spotFlatField = 10.0

// spotFlatLens is a slow (~F/3) biconvex singlet with an explicit image plane
// as its last surface: backGap (surface 2's thickness) positions the delivered
// image plane, so a test can shift the plane by a known amount and check what
// the spot-slope fit reports. Surface 1 doubles as the stop (fixed 20 mm
// aperture, Config.StopSurface 1) so the grid radius is the paraxial
// entrance-pupil radius instead of a stop-free estimate; the image plane is
// auto_aperture and gets resized to the measured beam during evaluation.
// Slow enough that spherical aberration does not dominate the slope fit —
// the conversion dz = slope * |EFL| is the paraxial one (exact for an
// aberration-free bundle), so a fast fixture would measure the marginal-ray
// SA bias instead of the plane shift.
func spotFlatLens(backGap float64) []types.Surface {
	return []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.02, Thickness: 8.0,
			Material: types.Material{Key: "N-BK7"}, Diameter: 20.0, AutoAperture: false},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: backGap,
			Material: types.Material{}, Diameter: 30.0, AutoAperture: false},
		{ID: 3, Type: types.Sphere, Curvature: 0.0, Thickness: 10.0,
			Material: types.Material{}, Diameter: 80.0, AutoAperture: true},
	}
}

func spotFlatGlass() *glass.Catalog {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	return gc
}

// spotFlatBFL is the paraxial back focal length of spotFlatLens: the backGap
// that puts the image plane on the paraxial focus. The image-plane surface is
// left out of the measurement — paraxial reports the second principal focus
// from the system's last surface, which for the full fixture would be the
// image plane itself.
func spotFlatBFL(t *testing.T) float64 {
	t.Helper()
	surfaces := spotFlatLens(60)
	surface.Precompute(surfaces)
	pr := paraxial.Compute(types.System{Surfaces: surfaces[:2], StopSurface: 1}, spotFlatWL, spotFlatGlass(), 0, nil)
	if pr.SecondPrincipalFocus <= 0 || pr.FocalLength <= 0 {
		t.Fatalf("paraxial BFL = %v (EFL %v), want a positive lens", pr.SecondPrincipalFocus, pr.FocalLength)
	}
	return pr.SecondPrincipalFocus
}

// spotFlatEFL is the paraxial effective focal length of spotFlatLens, the
// focus scale B the merit layer applies to the fitted slope.
func spotFlatEFL(t *testing.T) float64 {
	t.Helper()
	surfaces := spotFlatLens(60)
	surface.Precompute(surfaces)
	return paraxial.Compute(types.System{Surfaces: surfaces[:2], StopSurface: 1},
		spotFlatWL, spotFlatGlass(), 0, nil).FocalLength
}

// spotFlatConfig is the two-field (0 / spotFlatField degrees) spot-slope
// merit: both new kinds declared for both fields.
func spotFlatConfig(surfaces []types.Surface) Config {
	return Config{
		Surfaces:    surfaces,
		StopSurface: 1,
		Fields: []types.FieldItem{
			{ID: 0, AngleDeg: 0, Weight: 1},
			{ID: 1, AngleDeg: spotFlatField, Weight: 2},
		},
		MeritTerms: []MeritTerm{
			{Kind: dls.MeritSpotFlatness, FieldAngle: 0, FieldIndex: 0, FieldWeight: 1,
				Wavelength: spotFlatWL, WavWeight: 1, Weight: 1},
			{Kind: dls.MeritSpotFlatness, FieldAngle: spotFlatField, FieldIndex: 1, FieldWeight: 2,
				Wavelength: spotFlatWL, WavWeight: 1, Weight: 1},
			{Kind: dls.MeritSpotAstigmatism, FieldAngle: 0, FieldIndex: 0, FieldWeight: 1,
				Wavelength: spotFlatWL, WavWeight: 1, Weight: 1},
			{Kind: dls.MeritSpotAstigmatism, FieldAngle: spotFlatField, FieldIndex: 1, FieldWeight: 2,
				Wavelength: spotFlatWL, WavWeight: 1, Weight: 1},
		},
		GlassCatalog: spotFlatGlass(),
		NumRays:      64,
	}
}

func newSpotFlatOptimizer(t *testing.T, backGap float64) *Optimizer {
	t.Helper()
	surfaces := spotFlatLens(backGap)
	surface.Precompute(surfaces)
	return NewOptimizer(spotFlatConfig(surfaces))
}

// TestSpotSlopeKindsRouteThroughTheGrid pins the routing of the two new kinds:
// they must be grid kinds (evaluated from the pupil-grid spot statistics and
// precomputed with them), grid *trace* kinds (the precompute predicate) and
// never wavefront kinds. A kind missing from isGridKind would fall through to
// evaluateKindTerm, which returns 0 for unknown kinds and silently drops the
// term — pinned end to end by TestSpotFlatnessMatchesMeritValue, where a zero
// value cannot match the reported flatness.
func TestSpotSlopeKindsRouteThroughTheGrid(t *testing.T) {
	for _, k := range []string{dls.MeritSpotFlatness, dls.MeritSpotAstigmatism} {
		if !isGridKind(k) {
			t.Errorf("isGridKind(%q) = false; the term would evaluate to 0", k)
		}
		if !isGridTraceKind(k) || !isTraceKind(k) {
			t.Errorf("isGridTraceKind/isTraceKind(%q) = false; the grid would not be precomputed", k)
		}
		if isWavefrontKind(k) {
			t.Errorf("isWavefrontKind(%q) = true; the kind has no wavefront evaluator", k)
		}
	}
}

// TestSpotFlatnessEvaluatesNonZero guards the end of that routing: the
// diagnostics must return one fitted row per (field, wavelength), every ray
// grid surviving and every reported defocus finite (a fit that silently
// degenerates would report the bounded penalty or NaN instead).
func TestSpotFlatnessEvaluatesNonZero(t *testing.T) {
	opt := newSpotFlatOptimizer(t, spotFlatBFL(t))
	x := opt.getInitialState()
	rows := opt.SpotFlatnessDiagnostics(x)
	if len(rows) == 0 {
		t.Fatalf("SpotFlatnessDiagnostics returned no rows")
	}
	for _, r := range rows {
		if !r.OK {
			t.Errorf("field %d wavelength %v: fit not OK (rays=%d)", r.Field, r.Wavelength, r.Rays)
		}
		if r.Rays == 0 {
			t.Errorf("field %d: 0 rays survived the trace", r.Field)
		}
		if math.IsNaN(r.DefocusMM) || math.IsInf(r.DefocusMM, 0) {
			t.Errorf("field %d: defocus = %v", r.Field, r.DefocusMM)
		}
	}
	if len(rows) != 2 {
		t.Errorf("rows = %d, want one row per (field, wavelength) = 2", len(rows))
	}
}

// TestSpotFlatnessMatchesMeritValue ties the reported diagnostics to what the
// merit actually uses: with a single flatness term of weight 1 the merit is
// the square of the term's value, which must equal the row's flatness_mm.
func TestSpotFlatnessMatchesMeritValue(t *testing.T) {
	surfaces := spotFlatLens(spotFlatBFL(t))
	surface.Precompute(surfaces)
	cfg := spotFlatConfig(surfaces)
	cfg.MeritTerms = []MeritTerm{cfg.MeritTerms[0]} // only field 0's flatness
	opt := NewOptimizer(cfg)
	x := opt.getInitialState()

	merit := opt.EvaluateMerit(x)
	rows := opt.SpotFlatnessDiagnostics(x)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	got := math.Sqrt(merit)
	want := math.Abs(rows[0].FlatnessMM)
	if math.Abs(got-want) > 1e-9*math.Max(1, want) {
		t.Errorf("sqrt(merit) = %v, diagnostics flatness_mm = %v (field 0)", got, want)
	}
	// The mean is over both config fields, so field 0 must not be its own
	// reference (that would make flatness 0 by construction).
	if want < 1e-6 {
		t.Errorf("flatness_mm = %v, want a nonzero field-curvature spread", rows[0].FlatnessMM)
	}
}

// TestSpotFlatnessPlaneShiftInvariance pins the semantics that make
// spot_flatness safe next to back_focus_solve. Moving the delivered image
// plane by dz shifts every field's equivalent defocus by about -dz (the mm
// scale is the physical plane shift), while the mean-subtracted flatness — the
// quantity the term penalises — and the astigmatic deviation (a traceless
// part, hence plane-invariant) must not move with it: a common defocus is
// back_focus_solve's business, not the lens shape's.
//
// The defocus tolerance is looser than the invariance one on purpose. The
// slope-to-mm conversion is paraxial (B = |EFL|, exact for an aberration-free
// bundle); an off-axis field whose tangential and sagittal bundles converge at
// different rates — this bare singlet carries ~1 mm of astigmatism at 10
// degrees — responds to a plane shift by B/B_eff ~ 1 + astig/B. A corrected
// system stays well under a percent; 10% still catches the failures that
// matter here (a routing regression gives no response at all, a unit slip an
// order of magnitude).
func TestSpotFlatnessPlaneShiftInvariance(t *testing.T) {
	const shift = 1.0
	const shiftTol = 0.10 * shift // paraxial-conversion error, see above
	const flatTol = 0.05 * shift  // mean subtraction must cancel to well under
	bfl := spotFlatBFL(t)
	base := newSpotFlatOptimizer(t, bfl)
	moved := newSpotFlatOptimizer(t, bfl+shift)

	rowsBase := base.SpotFlatnessDiagnostics(base.getInitialState())
	rowsMoved := moved.SpotFlatnessDiagnostics(moved.getInitialState())
	if len(rowsBase) != len(rowsMoved) || len(rowsBase) == 0 {
		t.Fatalf("row count changed: %d vs %d", len(rowsBase), len(rowsMoved))
	}
	for i := range rowsBase {
		b, m := rowsBase[i], rowsMoved[i]
		if !b.OK || !m.OK {
			t.Fatalf("field %d: fit not OK (base %v, moved %v)", b.Field, b.OK, m.OK)
		}
		t.Logf("field %d: defocus %.4f -> %.4f, flatness %.4f -> %.4f, astig %.4f -> %.4f, residual %.4f/%.4f",
			b.Field, b.DefocusMM, m.DefocusMM, b.FlatnessMM, m.FlatnessMM,
			b.AstigMM, m.AstigMM, b.ResidualMM, m.ResidualMM)
		// Each field's own defocus moves with the plane, in mm, in the
		// direction that shortens the gap when the plane moves away.
		if d := (b.DefocusMM - m.DefocusMM); math.Abs(d-shift) > shiftTol {
			t.Errorf("field %d: defocus moved by %.6f mm for a %.3f mm plane shift, want ~-%.3f",
				b.Field, d, shift, shift)
		}
		// The mean-subtracted flatness must be plane-invariant.
		if d := math.Abs(b.FlatnessMM - m.FlatnessMM); d > flatTol {
			t.Errorf("field %d: flatness moved by %.6f mm under a %.3f mm plane shift, want ~0",
				b.Field, d, shift)
		}
		// The traceless astigmatic deviation is plane-invariant as well.
		if d := math.Abs(b.AstigMM - m.AstigMM); d > flatTol {
			t.Errorf("field %d: astigmatism moved by %.6f mm under a %.3f mm plane shift, want ~0",
				b.Field, d, shift)
		}
	}

	// Field curvature is what the term is for: the off-axis field must sit
	// measurably nearer the lens than the on-axis one (positive singlet).
	if spread := rowsBase[0].DefocusMM - rowsBase[1].DefocusMM; spread < 0.02 {
		t.Errorf("on-axis minus off-axis defocus = %.6f mm, want a positive field-curvature spread", spread)
	}
}

// TestSpotFlatnessZeroAtParaxialFocus anchors the absolute scale: with the
// image plane on the paraxial back focus the on-axis field must read within a
// couple of millimetres of zero — the residue is this singlet's real-ray
// spherical aberration (its marginal focus sits ~1.2 mm inside the paraxial
// one), while a scale error would show as tens of millimetres — and the
// off-axis field must read clearly more negative (its focus lies in front of
// the plane: positive lens, inward field curvature).
func TestSpotFlatnessZeroAtParaxialFocus(t *testing.T) {
	opt := newSpotFlatOptimizer(t, spotFlatBFL(t))
	rows := opt.SpotFlatnessDiagnostics(opt.getInitialState())
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	t.Logf("on-axis defocus %.4f mm, off-axis defocus %.4f mm (plane at the paraxial BFL)",
		rows[0].DefocusMM, rows[1].DefocusMM)
	if math.Abs(rows[0].DefocusMM) > 2 {
		t.Errorf("on-axis defocus = %.4f mm with the plane at the paraxial focus, want ~0", rows[0].DefocusMM)
	}
	if spread := rows[0].DefocusMM - rows[1].DefocusMM; spread < 0.5 {
		t.Errorf("on-axis minus off-axis defocus = %.4f mm, want an inward field curvature of ~mm", spread)
	}
}

// TestSpotFocusScaleIsEFL pins the slope-to-plane-shift conversion: B = |EFL|
// (equivalently R_marginal/NA' with paraxial's NA = EPD/(2*EFL)), the value
// spotFitFromPoints multiplies the fitted slope by. Everything the diagnostics
// report in millimetres depends on it.
func TestSpotFocusScaleIsEFL(t *testing.T) {
	surfaces := spotFlatLens(60)
	surface.Precompute(surfaces)
	opt := NewOptimizer(spotFlatConfig(surfaces))
	efl := spotFlatEFL(t)
	if efl <= 0 {
		t.Fatalf("fixture EFL = %v", efl)
	}
	got := opt.spotFocusScale(&opt.configs[0], surfaces, spotFlatGlass(), spotFlatWL, nil)
	if math.Abs(got-efl) > 1e-9*efl {
		t.Errorf("spotFocusScale = %v, want |EFL| = %v", got, efl)
	}
}

// TestSpotFlatnessSingleFieldConfigIsNull pins the documented fallback: a
// config that declares no fields has no curvature to measure, so the
// mean-subtracted value collapses to 0 instead of penalising the common
// defocus back_focus_solve already owns.
func TestSpotFlatnessSingleFieldConfigIsNull(t *testing.T) {
	surfaces := spotFlatLens(spotFlatBFL(t))
	surface.Precompute(surfaces)
	cfg := spotFlatConfig(surfaces)
	cfg.Fields = nil
	cfg.MeritTerms = []MeritTerm{cfg.MeritTerms[0]} // one flatness term, no fields
	opt := NewOptimizer(cfg)
	x := opt.getInitialState()
	if merit := opt.EvaluateMerit(x); merit != 0 {
		t.Errorf("merit on a field-less config = %v, want 0 (no curvature without fields)", merit)
	}
	if rows := opt.SpotFlatnessDiagnostics(x); rows != nil {
		t.Errorf("SpotFlatnessDiagnostics = %d rows, want nil on a field-less config", len(rows))
	}
}

// TestSpotSlopeKindDegenerateGrid pins the failure path: when the pupil grid
// dies entirely (here a rear surface aperture so small that every ray is
// vignetted) neither kind may return a silent 0 — both report the bounded
// degenerate penalty, and the diagnostics keep the row but mark it !ok with
// zeroed values so the loss of information stays visible.
func TestSpotSlopeKindDegenerateGrid(t *testing.T) {
	surfaces := spotFlatLens(spotFlatBFL(t))
	surfaces[1].Diameter = 0.01 // rear lens surface: nothing gets through
	surface.Precompute(surfaces)
	cfg := spotFlatConfig(surfaces)
	cfg.MeritTerms = []MeritTerm{cfg.MeritTerms[0], cfg.MeritTerms[2]} // field 0: flatness + astig
	opt := NewOptimizer(cfg)
	if opt.spotDegenerate <= 0 {
		t.Fatalf("spotDegenerate penalty = %v, want the bounded default", opt.spotDegenerate)
	}

	x := opt.getInitialState()
	merit := opt.EvaluateMerit(x)
	// Two terms, each weight 1, target 0, no normalization: merit == 2·penalty².
	want := 2 * opt.spotDegenerate * opt.spotDegenerate
	if math.Abs(merit-want) > 1e-9*want {
		t.Errorf("merit on a dead grid = %v, want the degenerate penalty %v", merit, want)
	}

	rows := opt.SpotFlatnessDiagnostics(x)
	if len(rows) == 0 {
		t.Fatalf("diagnostics returned no rows on a dead grid")
	}
	for _, r := range rows {
		if r.OK {
			t.Errorf("field %d: OK on a grid where every ray died", r.Field)
		}
		if r.DefocusMM != 0 || r.FlatnessMM != 0 || r.AstigMM != 0 {
			t.Errorf("field %d: !ok row carries values (defocus %v, flatness %v, astig %v), want zeros",
				r.Field, r.DefocusMM, r.FlatnessMM, r.AstigMM)
		}
	}
}

// TestFocusShiftNormalizationScale pins the merit_normalization denominator of
// the new kinds: the plane shift that puts one wave of defocus at the pupil
// edge, 2λ/NA², with NA recovered from the Airy radius — and the wavelength
// fallback when no Airy radius is available.
func TestFocusShiftNormalizationScale(t *testing.T) {
	const wl = 0.00058756
	const airy = 0.0024
	na := 0.61 * wl / airy
	want := 2 * wl / (na * na)
	for _, kind := range []string{dls.MeritSpotFlatness, dls.MeritSpotAstigmatism} {
		if got := normalizationScale(kind, wl, airy, nil); math.Abs(got-want) > 1e-15 {
			t.Errorf("normalizationScale(%q) = %v, want %v (2*wl/NA^2)", kind, got, want)
		}
		if got := normalizationScale(kind, wl, 0, nil); got != wl {
			t.Errorf("normalizationScale(%q, no Airy) = %v, want the wavelength %v", kind, got, wl)
		}
		if !isFocusShiftScaledKind(kind) {
			t.Errorf("isFocusShiftScaledKind(%q) = false", kind)
		}
	}
	// The spot-size kinds keep their Airy normalization.
	if got := normalizationScale(dls.MeritSpotRMS, wl, airy, nil); got != airy {
		t.Errorf("normalizationScale(spot_rms) = %v, want the Airy radius %v", got, airy)
	}
}
