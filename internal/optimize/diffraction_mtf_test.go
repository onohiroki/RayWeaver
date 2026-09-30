package optimize

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// diffractionFixture is a one-term optimizer on the singlet with the cheapest
// affordable diffraction sampling, so the test exercises the real trace +
// Huygens + FFT path rather than a stub.
type diffractionFixture struct {
	opt   *Optimizer
	term  *meritTerm
	gc    *glass.Catalog
	cache *evalGridCache
}

// newDiffractionFixture builds the fixture with the given hinge target.
func newDiffractionFixture(target float64) *diffractionFixture {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	surfaces := singletSurfaces()
	surface.Precompute(surfaces)

	const wl = 0.00058756
	cfg := Config{
		Surfaces:  surfaces,
		Variables: []Variable{},
		MeritTerms: []MeritTerm{{
			Kind: MeritDiffractionMTFSag, FieldAngle: 0, FieldDir: []float64{0, 1},
			FieldWeight: 1, Wavelength: wl, WavWeight: 1, Weight: 1, Target: target, Frequency: 10,
		}},
		GlassCatalog: gc,
		NumRays:      32,
	}
	opt := NewOptimizer(cfg)
	opt.SetDiffractionMTF(&types.DiffractionMTFConfig{NumRays: 32, GridSize: 32, MaxGrid: 64})

	return &diffractionFixture{
		opt:   opt,
		gc:    gc,
		cache: newEvalGridCache(),
		term: &meritTerm{
			kind: MeritDiffractionMTFSag, fieldAngle: 0, fieldIndex: -1,
			fieldDirX: 0, fieldDirY: 1, wavelength: wl, frequency: 10, target: target, weight: 1,
		},
	}
}

// eval dispatches one term through evaluateKindTerm with the fixture's cache
// (shared, so a sag+tan pair shares one evaluation).
func (f *diffractionFixture) eval(term *meritTerm) float64 {
	return f.opt.evaluateKindTerm(f.opt.primaryConfig(), term, f.opt.primaryConfig().surfaces, f.gc, f.cache, appliedPupil{})
}

// TestVignettedNumRaysTargetsSurvivingSamples pins the vignetting compensation.
// Both pupil-grid builders clip the full nominal grid against the prescribed
// ellipse rather than re-laying the grid into it, so a nominal ray count is not
// a sample count: a field whose ellipse keeps 50% of the nominal pupil gets half
// the effective samples, and the MTF error from an under-sampled pupil is ~0.20
// near the hinge (see diffractionSampling). The target is therefore the
// surviving count, and the compensation saturates so a near-dead ellipse cannot
// ask for a million launch rays.
func TestVignettedNumRaysTargetsSurvivingSamples(t *testing.T) {
	const target = 400
	cases := []struct {
		name string
		vig  *types.VignettingDef
		want int
	}{
		{"no vignetting", nil, target},
		{"zero def is no vignetting", &types.VignettingDef{}, target},
		{"unclipped", &types.VignettingDef{DecenterX: 0.2, CompressionX: 0, CompressionY: 0}, target},
		{"mild clip", &types.VignettingDef{CompressionX: 0.02, CompressionY: 0.02}, 417},
		{"our 23 deg ellipse", &types.VignettingDef{CompressionX: 0, CompressionY: 0.5}, 800},
		{"11.5 deg ellipse", &types.VignettingDef{CompressionX: 0, CompressionY: 0.25}, 534},
		{"half in both axes", &types.VignettingDef{CompressionX: 0.5, CompressionY: 0.5}, 1600},
	}
	for _, c := range cases {
		if got := vignettedNumRays(target, c.vig); got != c.want {
			t.Errorf("%s: vignettedNumRays(%d, %+v) = %d, want %d", c.name, target, c.vig, got, c.want)
		}
	}
	// Saturation: a 0.1%-area pupil would ask for 400 000 rays without the cap.
	dead := &types.VignettingDef{CompressionX: 0.99, CompressionY: 0.99}
	if got, want := vignettedNumRays(target, dead), target*maxVignettedNumRaysFactor; got != want {
		t.Errorf("near-dead ellipse: got %d, want the saturated %d", got, want)
	}
	// A degenerate ellipse keeps nothing; there is nothing to scale up for.
	if got := vignettedNumRays(target, &types.VignettingDef{CompressionY: 1}); got != target {
		t.Errorf("degenerate ellipse: got %d, want %d (unchanged)", got, target)
	}
	if got := vignettedNumRays(0, nil); got != 0 {
		t.Errorf("vignettedNumRays(0, nil) = %d, want 0", got)
	}
}

// TestDiffractionMTFKindsRouteToDiffractionEvaluator guards the routing: the
// diffraction kinds consume a pupil trace but have their own evaluator. Listing
// them in isGridKind would silently evaluate them as spot_rms (whose switch has
// no case for them) and isGridTraceKind would precompute them into the spot
// cache — the two angle-fallback loops do need to see them, which is what
// isTraceKind reports.
func TestDiffractionMTFKindsRouteToDiffractionEvaluator(t *testing.T) {
	for _, k := range []string{MeritDiffractionMTFSag, MeritDiffractionMTFTan} {
		if isGridKind(k) {
			t.Errorf("isGridKind(%q) = true; the kind would be evaluated as spot_rms", k)
		}
		if isGridTraceKind(k) {
			t.Errorf("isGridTraceKind(%q) = true; the kind would be precomputed into the spot cache", k)
		}
		if !isTraceKind(k) {
			t.Errorf("isTraceKind(%q) = false; the angle-fallback loops would skip the kind", k)
		}
		if !isDiffractionKind(k) {
			t.Errorf("isDiffractionKind(%q) = false", k)
		}
	}
	// The spot kinds must stay grid (and trace) kinds.
	if !isGridKind(MeritSpotRMST) || !isGridTraceKind(MeritSpotRMST) {
		t.Error("spot_rms_t must stay a grid trace kind")
	}
}

// TestDiffractionMTFTermMatchesItsEvaluatorAndHinge checks the value path: the
// dispatched term equals the shared evaluator's raw MTF (so the sagittal and
// tangential kinds read the same one evaluation), the hinge clamps at the
// target — (value−target)² = max(0, target−MTF)², a one-sided floor — and an
// unattainable target is never overshot. The measurement itself is the
// standalone psf one, so the raw value must be a physical MTF in [0,1].
func TestDiffractionMTFTermMatchesItsEvaluatorAndHinge(t *testing.T) {
	// target 1: no clamping, the dispatched value is the raw MTF.
	f := newDiffractionFixture(1.0)
	rawSag := f.eval(f.term)
	if math.IsNaN(rawSag) || math.IsInf(rawSag, 0) || rawSag < 0 || rawSag > 1 {
		t.Fatalf("raw diffraction_mtf_sag = %v, want a finite MTF in [0,1]", rawSag)
	}
	if len(f.cache.diffraction) != 1 {
		t.Fatalf("diffraction cache holds %d entries after one field, want 1", len(f.cache.diffraction))
	}

	// The tangential kind must share that single evaluation.
	tanTerm := *f.term
	tanTerm.kind = MeritDiffractionMTFTan
	rawTan := f.eval(&tanTerm)
	if len(f.cache.diffraction) != 1 {
		t.Errorf("a sag+tan pair on one field used %d evaluations, want 1", len(f.cache.diffraction))
	}
	if math.IsNaN(rawTan) || rawTan < 0 || rawTan > 1 {
		t.Errorf("raw diffraction_mtf_tan = %v, want a finite MTF in [0,1]", rawTan)
	}

	// A fresh cache must reproduce the same value (the trace is deterministic).
	fresh := newDiffractionFixture(1.0)
	again := fresh.eval(fresh.term)
	if math.Abs(again-rawSag) > 1e-12 {
		t.Errorf("re-evaluation gave %v, want %v (nondeterministic trace)", again, rawSag)
	}

	// Hinge: with a target below the value the term must return exactly the
	// target, so a design already at the gate is not pushed to buy MTF.
	if rawSag > 1e-6 {
		mid := rawSag / 2
		h := newDiffractionFixture(mid)
		got := h.eval(h.term)
		if math.Abs(got-mid) > 1e-12 {
			t.Errorf("target %.6f < value %.6f returned %.6f, want exactly the target", mid, rawSag, got)
		}
	}
}
