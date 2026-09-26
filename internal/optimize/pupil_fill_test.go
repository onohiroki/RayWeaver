package optimize

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// TestPupilFillValue pins the unbounded pupil_fill formula and its cap. The
// value must grow without bound as the surviving fraction shrinks, so partial
// clipping cannot be traded against a smaller (survivor-only) aberration
// residual the way the bounded field_alive deficit allows.
func TestPupilFillValue(t *testing.T) {
	cases := []struct{ ratio, want float64 }{
		{1.0, 0},
		{0.9, 1.0 / 9.0},
		{0.5, 1.0},
		{0.1, 9.0},
		{0.03, (1 - 0.03) / 0.03},
		{1e-3, (1 - 1e-3) / 1e-3},
		{5e-4, pupilFillCap},
		{0, pupilFillCap},
	}
	for _, c := range cases {
		if got := pupilFillValue(c.ratio); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("pupilFillValue(%v) = %v, want %v", c.ratio, got, c.want)
		}
	}
	// Unboundedness: at 3% fill the value (≈32.3, squared ≈1045) dwarfs the
	// bounded field_alive deficit (max 1), so a unit-weight pupil_fill term
	// already dominates a unit-weight field_alive term.
	if math.Pow(pupilFillValue(0.03), 2) < 1000 {
		t.Error("pupil_fill at 3% fill is not dominant enough to prevent clipping")
	}
}

// TestPupilFillFromPoints checks the grid mapping: all-valid → 0, a half-filled
// grid → 1, an empty grid → the cap.
func TestPupilFillFromPoints(t *testing.T) {
	mk := func(ok, notOK int) []dls.IPoint {
		pts := make([]dls.IPoint, 0, ok+notOK)
		for i := 0; i < ok; i++ {
			pts = append(pts, dls.IPoint{OK: true})
		}
		for i := 0; i < notOK; i++ {
			pts = append(pts, dls.IPoint{OK: false})
		}
		return pts
	}
	if got := pupilFillFromPoints(mk(64, 0)); got != 0 {
		t.Errorf("full grid = %v, want 0", got)
	}
	if got := pupilFillFromPoints(mk(32, 32)); math.Abs(got-1) > 1e-9 {
		t.Errorf("half grid = %v, want 1", got)
	}
	if got := pupilFillFromPoints(mk(0, 64)); got != pupilFillCap {
		t.Errorf("dead grid = %v, want %v", got, pupilFillCap)
	}
	if got := pupilFillFromPoints(nil); got != pupilFillCap {
		t.Errorf("empty grid = %v, want %v", got, pupilFillCap)
	}
}

// TestPupilFillRouting pins the kind routing: pupil_fill consumes a pupil-grid
// trace but is not a spot kind (so it is not silently evaluated as spot_rms).
func TestPupilFillRouting(t *testing.T) {
	if isGridKind(MeritPupilFill) {
		t.Error("isGridKind(pupil_fill) = true; it has no hinge target and must not route to evaluateGridKind")
	}
	if !isGridTraceKind(MeritPupilFill) {
		t.Error("isGridTraceKind(pupil_fill) = false; it needs a pupil-grid trace")
	}
}

// TestPupilFillMerit drives the kind through evaluateKindTerm so it is wired
// into the term dispatch, and checks the returned value matches the grid's
// fill mapping.
func TestPupilFillMerit(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.02, Thickness: 5.0, Material: types.Material{Key: "N-BK7"}, Diameter: 50.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.02, Thickness: 40.0, Material: types.Material{}, Diameter: 50.0},
	}
	surface.Precompute(surfaces)
	cfg := Config{
		Surfaces:     surfaces,
		Variables:    []Variable{},
		MeritTerms:   []MeritTerm{{Kind: MeritPupilFill, FieldAngle: 0, FieldIndex: 0, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1000}},
		GlassCatalog: gc,
		NumRays:      64,
	}
	opt := NewOptimizer(cfg)
	ccfg := opt.primaryConfig()
	term := &ccfg.meritTerms[0]

	want := pupilFillFromPoints(opt.gridForTerm(nil, gc, ccfg.surfaces, ccfg, term, appliedPupil{}))
	got := opt.evaluateKindTerm(ccfg, term, ccfg.surfaces, gc, nil, appliedPupil{})
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("evaluateKindTerm(pupil_fill) = %v, want the grid fill value %v", got, want)
	}
	if got < 0 || got > pupilFillCap {
		t.Fatalf("pupil_fill = %v, want within [0, %v]", got, pupilFillCap)
	}
}
