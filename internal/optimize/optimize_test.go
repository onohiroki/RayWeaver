package optimize

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/chief"
	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
	"github.com/hiroki/rayweaver/internal/wavefront"
)

func singletSurfaces() []types.Surface {
	return []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 10.0, Material: types.Material{Key: "N-BK7"}, Diameter: 50.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 100.0, Material: types.Material{}, Diameter: 50.0},
	}
}

// TestOptimizerSeidelDistortionKind verifies the seidel_distortion merit kind
// is evaluated via the distortion coefficient: with target = current S5 the
// merit must be ~0 (a spot_rms evaluation of the singlet would be far from 0).
func TestOptimizerSeidelDistortionKind(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	surfaces := singletSurfaces()
	surface.Precompute(surfaces)

	s5 := paraxial.ComputeSeidel(surfaces, 10, 0.00058756, gc).Distortion

	cfg := Config{
		Surfaces:  surfaces,
		Variables: []Variable{},
		MeritTerms: []MeritTerm{{
			Kind:        MeritSeidelDistortion,
			FieldAngle:  10.0,
			FieldWeight: 1.0,
			Wavelength:  0.00058756,
			WavWeight:   1.0,
			Weight:      1.0,
			Target:      s5,
		}},
		GlassCatalog: gc,
		NumRays:      16,
	}

	opt := NewOptimizer(cfg)
	x := opt.getInitialState()
	merit := opt.EvaluateMerit(x)

	if merit > 1e-12 {
		t.Errorf("merit with target = current S5 = %v, want ~0", merit)
	}
}

func TestOptimizerEvaluateMerit(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	cfg := Config{
		Surfaces:     singletSurfaces(),
		Variables:    []Variable{},
		MeritTerms:   []MeritTerm{{FieldAngle: 0.0, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0}},
		GlassCatalog: gc,
		NumRays:      16,
	}

	opt := NewOptimizer(cfg)
	x := opt.getInitialState()
	merit := opt.EvaluateMerit(x)

	if math.IsInf(merit, 0) || math.IsNaN(merit) {
		t.Fatalf("evaluateMerit returned non-finite value: %v", merit)
	}
	if merit < 0 {
		t.Fatalf("evaluateMerit returned negative value: %v", merit)
	}
}

// TestOptimizerOffAxisGridKinds verifies the new off-axis spot grid kinds
// evaluate to finite, positive merit and that with target = current value the
// contribution vanishes (so the target semantics work end to end).
func TestOptimizerOffAxisGridKinds(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	// A fast singlet (f=100) clips the 16° chief ray at its 50mm aperture
	// (tan16°·100 ≈ 28.7 > 25), so use a wide aperture for this test.
	wide := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 10.0, Material: types.Material{Key: "N-BK7"}, Diameter: 200.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 100.0, Material: types.Material{}, Diameter: 200.0},
	}
	surface.Precompute(wide)

	terms := []MeritTerm{
		{Kind: MeritSpotRMST, FieldAngle: 16.0, FieldDir: []float64{0, 1}, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0},
		{Kind: MeritSpotRMSS, FieldAngle: 16.0, FieldDir: []float64{0, 1}, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0},
		{Kind: MeritSpotRMSWorst, FieldAngle: 16.0, FieldDir: []float64{0, 1}, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0},
		{Kind: MeritSpotWeightedRMS, FieldAngle: 16.0, FieldDir: []float64{0, 1}, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0},
		{Kind: MeritSpotEERadius, FieldAngle: 16.0, FieldDir: []float64{0, 1}, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0, Fraction: 0.5},
	}

	cfg := Config{
		Surfaces:     wide,
		Variables:    []Variable{},
		MeritTerms:   terms,
		GlassCatalog: gc,
		NumRays:      16,
	}

	opt := NewOptimizer(cfg)
	x := opt.getInitialState()

	bd := opt.MeritBreakdown(x)
	if len(bd)-1 != len(terms) {
		t.Fatalf("MeritBreakdown returned %d terms, want %d", len(bd)-1, len(terms))
	}
	for k, v := range bd {
		if k == "objective_total" {
			continue
		}
		if math.IsInf(v, 0) || math.IsNaN(v) || v < 0 {
			t.Errorf("breakdown %q = %v, want finite positive", k, v)
		}
	}

	// Residuals must be finite and equal to the metric value (target 0).
	residuals := opt.ComputeResiduals(x)
	if len(residuals) != len(terms) {
		t.Fatalf("ComputeResiduals returned %d, want %d", len(residuals), len(terms))
	}
	for i, r := range residuals {
		if math.IsInf(r, 0) || math.IsNaN(r) {
			t.Errorf("residual[%d] = %v, want finite", i, r)
		}
	}

	// With target = the metric's own value the (value-target)^2 contribution
	// must be ~0 for the targeted kinds.
	for ti, term := range terms {
		val := opt.evaluateGridKind(opt.primaryConfig(), &meritTerm{
			kind:       term.Kind,
			fieldAngle: term.FieldAngle,
			fieldDirX:  0,
			fieldDirY:  1,
			wavelength: term.Wavelength,
			fraction:   term.Fraction,
		}, opt.primaryConfig().surfaces, gc, nil)
		if term.Kind == MeritSpotEERadius && val >= 1e6 {
			t.Errorf("%s evaluated to the 1e6 degenerate penalty: %v", term.Kind, val)
		}
		targeted := cfg
		targeted.MeritTerms = []MeritTerm{term}
		targeted.MeritTerms[0].Target = val
		opt2 := NewOptimizer(targeted)
		m := opt2.EvaluateMerit(x)
		if m > 1e-8 {
			t.Errorf("%s with target = value: merit = %v, want ~0 (ti=%d)", term.Kind, m, ti)
		}
	}
}

// TestOptimizerGridCache verifies that grid merit terms sharing a
// (field angle, wavelength) reuse one pupil-grid trace per evaluation: after
// tracing two grid terms of the same field/wavelength through a shared cache,
// the cache holds a single entry and both terms observe the identical trace;
// a different wavelength yields a distinct entry.
func TestOptimizerGridCache(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	surfaces := singletSurfaces()
	surface.Precompute(surfaces)

	cfg := Config{
		Surfaces:  surfaces,
		Variables: []Variable{},
		MeritTerms: []MeritTerm{
			{Kind: MeritSpotRMST, FieldAngle: 16.0, FieldDir: []float64{0, 1}, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0},
			{Kind: MeritSpotRMSS, FieldAngle: 16.0, FieldDir: []float64{0, 1}, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0},
			{Kind: MeritSpotRMSWorst, FieldAngle: 16.0, FieldDir: []float64{0, 1}, FieldWeight: 1.0, Wavelength: 0.0006563, WavWeight: 1.0, Weight: 1.0},
		},
		GlassCatalog: gc,
		NumRays:      32,
	}

	opt := NewOptimizer(cfg)
	ccfg := opt.primaryConfig()
	cache := newEvalGridCache()

	mk := func(kind string, wl float64) *meritTerm {
		return &meritTerm{kind: kind, fieldAngle: 16.0, fieldDirX: 0, fieldDirY: 1, wavelength: wl, fraction: 0.8}
	}

	// Two terms of the same (field, wavelength) must share one cached trace.
	p1 := opt.gridForTerm(cache, gc, ccfg.surfaces, ccfg, mk(MeritSpotRMST, 0.00058756))
	p2 := opt.gridForTerm(cache, gc, ccfg.surfaces, ccfg, mk(MeritSpotRMSS, 0.00058756))
	if len(cache.spots) != 1 {
		t.Fatalf("grid cache holds %d entries after tracing one (field,wl) twice, want 1", len(cache.spots))
	}
	if len(p1) == 0 {
		t.Fatalf("grid trace returned no points")
	}
	if &p1[0] != &p2[0] {
		t.Errorf("two grid terms of the same (field,wl) returned distinct traces (cache miss)")
	}

	// A different wavelength must produce a distinct cache entry.
	p3 := opt.gridForTerm(cache, gc, ccfg.surfaces, ccfg, mk(MeritSpotRMSWorst, 0.0006563))
	if len(cache.spots) != 2 {
		t.Fatalf("grid cache holds %d entries after adding a second wavelength, want 2", len(cache.spots))
	}
	if &p1[0] == &p3[0] {
		t.Errorf("different wavelengths returned the same trace (cache key collision)")
	}

	// A nil cache must not panic and must return fresh traces.
	p4 := opt.gridForTerm(nil, gc, ccfg.surfaces, ccfg, mk(MeritSpotRMST, 0.00058756))
	if len(p4) == 0 {
		t.Fatalf("nil-cache grid trace returned no points")
	}

	// EvaluateMerit / ComputeResiduals results are unaffected by the cache
	// (determinism): two evaluations must agree exactly.
	x := opt.getInitialState()
	m1 := opt.EvaluateMerit(x)
	m2 := opt.EvaluateMerit(x)
	if m1 != m2 {
		t.Errorf("EvaluateMerit not deterministic across cached evaluations: %v vs %v", m1, m2)
	}
	r1 := opt.ComputeResiduals(x)
	r2 := opt.ComputeResiduals(x)
	if len(r1) != len(r2) {
		t.Fatalf("ComputeResiduals lengths differ: %d vs %d", len(r1), len(r2))
	}
	for i := range r1 {
		if r1[i] != r2[i] {
			t.Errorf("ComputeResiduals[%d] not deterministic: %v vs %v", i, r1[i], r2[i])
		}
	}
}

// TestOptimizerDegeneratePenalty verifies that merit terms which cannot be
// evaluated (a pupil grid with no valid rays, or a failed wavefront fit)
// return the bounded degenerate penalty instead of the legacy 1e6 sentinel,
// so the DLS line search is not stalled by a weight·1e12 contribution. It also
// checks the Config override wiring.
func TestOptimizerDegeneratePenalty(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	// An extreme field angle (89°) against a plane makes every grid ray miss
	// the system, so the spot kinds hit the degenerate (no valid rays) path.
	wide := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 5.0, Material: types.Material{Key: "N-BK7"}, Diameter: 50.0},
		{ID: 2, Type: types.Sphere, Curvature: 0.0, Thickness: 50.0, Material: types.Material{}, Diameter: 50.0},
	}
	surface.Precompute(wide)

	cfg := Config{
		Surfaces:     wide,
		Variables:    []Variable{},
		MeritTerms:   []MeritTerm{{FieldAngle: 89.0, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0}},
		GlassCatalog: gc,
		NumRays:      32,
	}
	opt := NewOptimizer(cfg)
	ccfg := opt.primaryConfig()

	// Default penalty values.
	if opt.spotDegenerate != 0.1 || opt.opdDegenerate != 0.01 || opt.wavefrontDegenerate != 0.001 {
		t.Fatalf("default degenerate penalties = %v/%v/%v, want 0.1/0.01/0.001",
			opt.spotDegenerate, opt.opdDegenerate, opt.wavefrontDegenerate)
	}

	// A grid kind on a fully clipped pupil must return the bounded spot
	// penalty, never the 1e6 sentinel.
	term := &meritTerm{kind: "", fieldAngle: 89.0, fieldDirX: 0, fieldDirY: 1, wavelength: 0.00058756}
	val := opt.evaluateGridKind(ccfg, term, ccfg.surfaces, gc, nil)
	if val != 0.1 {
		t.Fatalf("degenerate spot kind = %v, want bounded penalty 0.1", val)
	}
	if val >= 1e6 {
		t.Fatalf("degenerate spot kind leaked the 1e6 sentinel: %v", val)
	}

	// opd_rms on the same clipped pupil must return the bounded opd penalty.
	opd := opt.evaluateKindTerm(ccfg, &meritTerm{kind: MeritOPDRMS, fieldAngle: 89.0, wavelength: 0.00058756}, ccfg.surfaces, gc, nil)
	if opd != 0.01 {
		t.Fatalf("degenerate opd_rms = %v, want bounded penalty 0.01", opd)
	}

	// A config-level override must win over the default.
	cfg2 := cfg
	cfg2.SpotDegenerate = 0.5
	cfg2.OPDDegenerate = 0.05
	cfg2.WavefrontDegenerate = 0.005
	opt2 := NewOptimizer(cfg2)
	if opt2.spotDegenerate != 0.5 || opt2.opdDegenerate != 0.05 || opt2.wavefrontDegenerate != 0.005 {
		t.Fatalf("degenerate overrides not applied: %v/%v/%v",
			opt2.spotDegenerate, opt2.opdDegenerate, opt2.wavefrontDegenerate)
	}
	val2 := opt2.evaluateGridKind(opt2.primaryConfig(), term, opt2.primaryConfig().surfaces, gc, nil)
	if val2 != 0.5 {
		t.Fatalf("degenerate spot kind with override = %v, want 0.5", val2)
	}

	// The bounded penalty must keep the total merit finite and small (the
	// legacy sentinel would make it ~1e12).
	merit := opt.EvaluateMerit([]float64{})
	if merit >= 1.0 {
		t.Fatalf("merit with a degenerate term = %v, want bounded (< 1)", merit)
	}

	// A wavefront fit that cannot run (89° field: too few valid rays even
	// with the dynamic-pupil fallback) must return the bounded wavefront
	// penalty, never the 1e6 sentinel.
	wf := opt.evaluateWavefrontTerm(ccfg, &meritTerm{kind: MeritWavefrontAstigmatism, fieldAngle: 89.0, wavelength: 0.00058756}, ccfg.surfaces, gc)
	if wf != 0.001 {
		t.Fatalf("degenerate wavefront = %v, want bounded penalty 0.001", wf)
	}
	if wf >= 1e6 {
		t.Fatalf("degenerate wavefront leaked the 1e6 sentinel: %v", wf)
	}
	cfgW := cfg
	cfgW.MeritTerms = []MeritTerm{{Kind: MeritWavefrontAstigmatism, FieldAngle: 89.0, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 14000.0}}
	optW := NewOptimizer(cfgW)
	if m := optW.EvaluateMerit([]float64{}); m >= 1.0 {
		t.Fatalf("merit with a degenerate wavefront term = %v, want bounded (< 1)", m)
	}
}

func TestOptimizerApplyVariablesCurvature(t *testing.T) {
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 10.0, Material: types.Material{Key: "N-BK7"}, Diameter: 50.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 100.0, Material: types.Material{}, Diameter: 50.0},
	}

	cfg := Config{
		Surfaces: surfaces,
		Variables: []Variable{
			{SurfaceID: 1, Param: "curvature", Min: -1.0, Max: 1.0},
		},
		MeritTerms:   []MeritTerm{{FieldAngle: 0.0, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0}},
		GlassCatalog: glass.NewCatalog(),
		NumRays:      4,
	}

	opt := NewOptimizer(cfg)
	surfacesMap, _ := opt.applyVariables([]float64{0.02})
	surfacesAfter := surfacesMap["config1"]

	found := false
	for _, s := range surfacesAfter {
		if s.ID == 1 {
			found = true
			if math.Abs(s.Curvature-0.02) > 1e-10 {
				t.Errorf("Curvature = %v, want 0.02", s.Curvature)
			}
		}
	}
	if !found {
		t.Error("Surface 1 not found after applyVariables")
	}
}

func TestOptimizerApplyVariablesThickness(t *testing.T) {
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 10.0, Material: types.Material{Key: "N-BK7"}, Diameter: 50.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 100.0, Material: types.Material{}, Diameter: 50.0},
	}

	cfg := Config{
		Surfaces: surfaces,
		Variables: []Variable{
			{SurfaceID: 2, Param: "thickness", Min: 0.1, Max: 200.0},
		},
		MeritTerms:   []MeritTerm{{FieldAngle: 0.0, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0}},
		GlassCatalog: glass.NewCatalog(),
		NumRays:      4,
	}

	opt := NewOptimizer(cfg)
	surfacesMap, _ := opt.applyVariables([]float64{50.0})
	surfacesAfter := surfacesMap["config1"]

	found := false
	for _, s := range surfacesAfter {
		if s.ID == 2 {
			found = true
			if math.Abs(s.Thickness-50.0) > 1e-10 {
				t.Errorf("Thickness = %v, want 50.0", s.Thickness)
			}
		}
	}
	if !found {
		t.Error("Surface 2 not found after applyVariables")
	}
}

func TestOptimizerResultHasExpectedFields(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 10.0, Material: types.Material{Key: "N-BK7"}, Diameter: 50.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 100.0, Material: types.Material{}, Diameter: 50.0},
	}

	cfg := Config{
		Surfaces: surfaces,
		Variables: []Variable{
			{SurfaceID: 1, Param: "curvature", Min: -1.0, Max: 1.0},
		},
		MeritTerms:   []MeritTerm{{FieldAngle: 0.0, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0}},
		GlassCatalog: gc,
		MaxIter:      1,
		NumRays:      4,
	}

	opt := NewOptimizer(cfg)
	result := opt.Optimize()

	if result.Status != "max_iterations" {
		t.Errorf("Status = %q, want %q (max_iterations for 1 iteration)", result.Status, "max_iterations")
	}
	if result.Iterations != 1 {
		t.Errorf("Iterations = %d, want 1", result.Iterations)
	}
	if result.BeforeMerit <= 0 {
		t.Errorf("BeforeMerit = %v, want positive value", result.BeforeMerit)
	}
	if result.AfterMerit <= 0 {
		t.Errorf("AfterMerit = %v, want positive value", result.AfterMerit)
	}
}

func TestOptimizerStopReturnsInterrupted(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 10.0, Material: types.Material{Key: "N-BK7"}, Diameter: 50.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 100.0, Material: types.Material{}, Diameter: 50.0},
	}

	cfg := Config{
		Surfaces: surfaces,
		Variables: []Variable{
			{SurfaceID: 1, Param: "curvature", Min: -1.0, Max: 1.0},
		},
		MeritTerms:   []MeritTerm{{FieldAngle: 0.0, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0}},
		GlassCatalog: gc,
		MaxIter:      50,
		NumRays:      4,
	}

	opt := NewOptimizer(cfg)
	stop := make(chan struct{})
	close(stop)
	opt.SetStop(stop)

	result := opt.Optimize()

	if result.Status != dls.StatusInterrupted {
		t.Errorf("Status = %q, want %q", result.Status, dls.StatusInterrupted)
	}
	if len(result.Variables) != 1 {
		t.Errorf("Variables = %d entries, want 1 (best-so-far preserved)", len(result.Variables))
	}
}

func TestOptimizerCanImproveDegradedSystem(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "SK18", ND: 1.63854, VD: 55.42})
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "SF12", ND: 1.64831, VD: 33.84})

	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.12, Thickness: 1.524, Material: types.Material{Key: "SK18"}, Diameter: 10.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.004177, Thickness: 2.3368, Material: types.Material{}, Diameter: 10.0},
		{ID: 3, Type: types.Sphere, Curvature: -0.05, Thickness: 0.508, Material: types.Material{Key: "SF12"}, Diameter: 6.0},
		{ID: 4, Type: types.Sphere, Curvature: 0.094413, Thickness: 1.4986, Material: types.Material{}, Diameter: 6.0},
		{ID: 5, Type: types.Sphere, Curvature: 0, Thickness: 1.016, Material: types.Material{}, Diameter: 3.7825297358},
		{ID: 6, Type: types.Sphere, Curvature: 0.025, Thickness: 1.524, Material: types.Material{Key: "SK18"}, Diameter: 6.0},
		{ID: 7, Type: types.Sphere, Curvature: -0.15, Thickness: 21.36695183553, Material: types.Material{}, Diameter: 6.0},
		{ID: 8, Type: types.Sphere, Curvature: 0, Thickness: 0.0, Material: types.Material{}},
	}

	terms := []MeritTerm{
		{FieldAngle: 0.0, FieldDir: []float64{0, 1}, FieldWeight: 1.0, Wavelength: 0.0004861, WavWeight: 1.0, Weight: 1.0},
		{FieldAngle: 0.0, FieldDir: []float64{0, 1}, FieldWeight: 1.0, Wavelength: types.DefaultWavelength, WavWeight: 1.0, Weight: 1.0},
		{FieldAngle: 0.0, FieldDir: []float64{0, 1}, FieldWeight: 1.0, Wavelength: 0.0006563, WavWeight: 1.0, Weight: 1.0},
		{FieldAngle: 16.0, FieldDir: []float64{0, 1}, FieldWeight: 1.0, Wavelength: 0.0004861, WavWeight: 1.0, Weight: 1.0},
		{FieldAngle: 16.0, FieldDir: []float64{0, 1}, FieldWeight: 1.0, Wavelength: types.DefaultWavelength, WavWeight: 1.0, Weight: 1.0},
		{FieldAngle: 16.0, FieldDir: []float64{0, 1}, FieldWeight: 1.0, Wavelength: 0.0006563, WavWeight: 1.0, Weight: 1.0},
		{FieldAngle: 24.0, FieldDir: []float64{0, 1}, FieldWeight: 0.5, Wavelength: 0.0004861, WavWeight: 0.5, Weight: 0.5},
		{FieldAngle: 24.0, FieldDir: []float64{0, 1}, FieldWeight: 0.5, Wavelength: types.DefaultWavelength, WavWeight: 0.5, Weight: 0.5},
		{FieldAngle: 24.0, FieldDir: []float64{0, 1}, FieldWeight: 0.5, Wavelength: 0.0006563, WavWeight: 0.5, Weight: 0.5},
	}

	variables := []Variable{
		{Name: "s1_curvature", SurfaceID: 1, Param: "curvature", Min: 0.02, Max: 0.3},
		{Name: "s3_curvature", SurfaceID: 3, Param: "curvature", Min: -0.3, Max: -0.01},
		{Name: "s6_curvature", SurfaceID: 6, Param: "curvature", Min: 0.005, Max: 0.5},
		{Name: "s7_curvature", SurfaceID: 7, Param: "curvature", Min: -0.3, Max: -0.01},
	}

	cfg := Config{
		Surfaces:     surfaces,
		Variables:    variables,
		MeritTerms:   terms,
		GlassCatalog: gc,
		NumRays:      512,
	}

	opt := NewOptimizer(cfg)
	result := opt.Optimize()

	t.Logf("Status: %s, Iterations: %d", result.Status, result.Iterations)
	t.Logf("Before merit: %.6e", result.BeforeMerit)
	t.Logf("After merit: %.6e", result.AfterMerit)

	if result.BeforeMerit <= 0 {
		t.Errorf("BeforeMerit = %v, want positive", result.BeforeMerit)
	}
	if result.AfterMerit <= 0 {
		t.Errorf("AfterMerit = %v, want positive", result.AfterMerit)
	}
	if result.AfterMerit >= result.BeforeMerit {
		t.Errorf("Optimizer did not improve merit: before=%.6e, after=%.6e", result.BeforeMerit, result.AfterMerit)
	}
}

type mockLogger struct {
	iterLogs []struct {
		Iter        int
		Merit       float64
		Improvement float64
		StepNorm    float64
		Variables   []float64
	}
	finalLogs []struct {
		Iter      int
		Merit     float64
		StepNorm  float64
		Variables []float64
		Status    string
	}
}

func (m *mockLogger) LogIter(iter int, merit, improvement, stepNorm float64, variables []float64, constraints []dls.ConstraintState) {
	m.iterLogs = append(m.iterLogs, struct {
		Iter        int
		Merit       float64
		Improvement float64
		StepNorm    float64
		Variables   []float64
	}{iter, merit, improvement, stepNorm, variables})
	_ = constraints
}

func (m *mockLogger) LogFinal(iter int, status string, merit float64, stepNorm float64, variables []float64, constraints []dls.ConstraintState) {
	m.finalLogs = append(m.finalLogs, struct {
		Iter      int
		Merit     float64
		StepNorm  float64
		Variables []float64
		Status    string
	}{iter, merit, stepNorm, variables, status})
	_ = constraints
}

func TestOptimizerApplyVariablesND(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	cfg := Config{
		Surfaces: singletSurfaces(),
		Variables: []Variable{
			{SurfaceID: 1, Param: "nd", Min: 1.4, Max: 1.9},
		},
		MeritTerms:   []MeritTerm{{FieldAngle: 0.0, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0}},
		GlassCatalog: gc,
		NumRays:      4,
	}

	opt := NewOptimizer(cfg)
	_, effectiveGC := opt.applyVariables([]float64{1.7})

	g, ok := effectiveGC.Lookup("N-BK7")
	if !ok {
		t.Fatal("N-BK7 not found in effective catalog after applyVariables")
	}
	if g.ND != 1.7 {
		t.Errorf("N-BK7 ND = %v, want 1.7", g.ND)
	}
}

func TestOptimizerApplyVariablesVD(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	cfg := Config{
		Surfaces: singletSurfaces(),
		Variables: []Variable{
			{SurfaceID: 1, Param: "vd", Min: 20, Max: 80},
		},
		MeritTerms:   []MeritTerm{{FieldAngle: 0.0, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0}},
		GlassCatalog: gc,
		NumRays:      4,
	}

	opt := NewOptimizer(cfg)
	_, effectiveGC := opt.applyVariables([]float64{50.0})

	g, ok := effectiveGC.Lookup("N-BK7")
	if !ok {
		t.Fatal("N-BK7 not found in effective catalog after applyVariables")
	}
	if g.VD != 50.0 {
		t.Errorf("N-BK7 VD = %v, want 50.0", g.VD)
	}
}

func TestOptimizerGetInitialStateGlass(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	cfg := Config{
		Surfaces: singletSurfaces(),
		Variables: []Variable{
			{Name: "bk7_nd", SurfaceID: 1, Param: "nd", Min: 1.4, Max: 1.9},
			{Name: "bk7_vd", SurfaceID: 1, Param: "vd", Min: 20, Max: 80},
		},
		MeritTerms:   []MeritTerm{{FieldAngle: 0.0, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0}},
		GlassCatalog: gc,
		NumRays:      4,
	}

	opt := NewOptimizer(cfg)
	x := opt.getInitialState()

	if math.Abs(x[0]-1.5168) > 1e-10 {
		t.Errorf("initial nd = %v, want 1.5168", x[0])
	}
	if math.Abs(x[1]-64.17) > 1e-10 {
		t.Errorf("initial vd = %v, want 64.17", x[1])
	}
}

func TestOptimizerGetInitialStateGlassNotFound(t *testing.T) {
	gc := glass.NewCatalog()

	cfg := Config{
		Surfaces: singletSurfaces(),
		Variables: []Variable{
			{Name: "nd", SurfaceID: 99, Param: "nd", Min: 1.4, Max: 1.9},
		},
		MeritTerms:   []MeritTerm{{FieldAngle: 0.0, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0}},
		GlassCatalog: gc,
		NumRays:      4,
	}

	opt := NewOptimizer(cfg)
	x := opt.getInitialState()

	expected := (1.4 + 1.9) / 2
	if math.Abs(x[0]-expected) > 1e-10 {
		t.Errorf("initial state = %v, want %v (midpoint fallback)", x[0], expected)
	}
}

func TestBuildVariableStatesGlass(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	cfg := Config{
		Surfaces: singletSurfaces(),
		Variables: []Variable{
			{Name: "bk7_nd", SurfaceID: 1, Param: "nd", Min: 1.4, Max: 1.9},
		},
		MeritTerms:   []MeritTerm{{FieldAngle: 0.0, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0}},
		GlassCatalog: gc,
		NumRays:      4,
	}

	opt := NewOptimizer(cfg)
	states := opt.buildVariableStates([]float64{1.7})

	if len(states) != 1 {
		t.Fatalf("expected 1 state, got %d", len(states))
	}
	if states[0].GlassName != "N-BK7" {
		t.Errorf("GlassName = %q, want N-BK7", states[0].GlassName)
	}
	if math.Abs(states[0].Before-1.5168) > 1e-10 {
		t.Errorf("Before = %v, want 1.5168", states[0].Before)
	}
	if math.Abs(states[0].After-1.7) > 1e-10 {
		t.Errorf("After = %v, want 1.7", states[0].After)
	}
	if states[0].SurfaceID != 1 {
		t.Errorf("SurfaceID = %d, want 1", states[0].SurfaceID)
	}
}

func TestOptimizerLoggerCalled(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "SK18", ND: 1.63854, VD: 55.42})
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "SF12", ND: 1.64831, VD: 33.84})

	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.05, Thickness: 1.524, Material: types.Material{Key: "SK18"}, Diameter: 10.0},
		{ID: 2, Type: types.Sphere, Curvature: 0, Thickness: 2.3368, Material: types.Material{}, Diameter: 10.0},
		{ID: 3, Type: types.Sphere, Curvature: -0.05, Thickness: 0.508, Material: types.Material{Key: "SF12"}, Diameter: 6.0},
		{ID: 4, Type: types.Sphere, Curvature: 0.094413, Thickness: 1.4986, Material: types.Material{}, Diameter: 6.0},
		{ID: 5, Type: types.Sphere, Curvature: 0, Thickness: 1.016, Material: types.Material{}, Diameter: 3.7825297358},
		{ID: 6, Type: types.Sphere, Curvature: 0.025, Thickness: 1.524, Material: types.Material{Key: "SK18"}, Diameter: 6.0},
		{ID: 7, Type: types.Sphere, Curvature: -0.15, Thickness: 21.36695183553, Material: types.Material{}, Diameter: 6.0},
		{ID: 8, Type: types.Sphere, Curvature: 0, Thickness: 0.0, Material: types.Material{}},
	}

	terms := []MeritTerm{
		{FieldAngle: 0.0, FieldDir: []float64{0, 1}, FieldWeight: 1.0, Wavelength: 0.0004861, WavWeight: 1.0, Weight: 1.0},
		{FieldAngle: 16.0, FieldDir: []float64{0, 1}, FieldWeight: 1.0, Wavelength: types.DefaultWavelength, WavWeight: 1.0, Weight: 1.0},
	}

	variables := []Variable{
		{Name: "s1_curvature", SurfaceID: 1, Param: "curvature", Min: 0.02, Max: 0.3},
		{Name: "s3_curvature", SurfaceID: 3, Param: "curvature", Min: -0.3, Max: -0.01},
	}

	logger := &mockLogger{}
	cfg := Config{
		Surfaces:     surfaces,
		Variables:    variables,
		MeritTerms:   terms,
		GlassCatalog: gc,
		NumRays:      32,
		Logger:       logger,
	}

	opt := NewOptimizer(cfg)
	result := opt.Optimize()

	if len(logger.iterLogs) == 0 {
		t.Error("LogIter was not called during optimization")
	}
	if len(logger.finalLogs) != 1 {
		t.Errorf("expected 1 LogFinal call, got %d", len(logger.finalLogs))
	}
	if len(logger.finalLogs) > 0 {
		fl := logger.finalLogs[0]
		if fl.Status != result.Status {
			t.Errorf("final log status = %q, want %q", fl.Status, result.Status)
		}
		if fl.Iter != result.Iterations {
			t.Errorf("final log iter = %d, want %d", fl.Iter, result.Iterations)
		}
	}
	if len(logger.iterLogs) == 0 {
		t.Error("LogIter was not called")
	}
	if len(logger.iterLogs) > result.Iterations {
		t.Errorf("LogIter called %d times, want at most %d (iterations)", len(logger.iterLogs), result.Iterations)
	}
}

// TestUpdatePupils verifies the dynamic-pupil recomputation: UpdatePupils
// derives a non-zero per-config pupil Z at the current variables, and the value
// follows a change in the variables (the aperture position moves during
// optimisation).
func TestUpdatePupils(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "SK18", ND: 1.63854, VD: 55.42})
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "SF12", ND: 1.64831, VD: 33.84})

	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 1 / 10.2871491742, Thickness: 1.524, Material: types.Material{Key: "SK18"}, Diameter: 10.0},
		{ID: 2, Type: types.Sphere, Curvature: 1 / -239.3967954752, Thickness: 2.3368, Material: types.Material{}, Diameter: 10.0},
		{ID: 3, Type: types.Sphere, Curvature: 1 / -12.8269871730, Thickness: 0.508, Material: types.Material{Key: "SF12"}, Diameter: 6.0},
		{ID: 4, Type: types.Sphere, Curvature: 1 / 10.5917184406, Thickness: 1.4986, Material: types.Material{}, Diameter: 6.0},
		{ID: 5, Type: types.Sphere, Curvature: 0.0, Thickness: 1.016, Material: types.Material{}, Diameter: 3.78},
		{ID: 6, Type: types.Sphere, Curvature: 1 / 61.8456294200, Thickness: 1.524, Material: types.Material{Key: "SK18"}, Diameter: 6.0},
		{ID: 7, Type: types.Sphere, Curvature: 1 / -10.0074859032, Thickness: 21.36695183553, Material: types.Material{}, Diameter: 6.0},
		{ID: 8, Type: types.Sphere, Curvature: 0.0, Thickness: 0.0, Material: types.Material{}, Diameter: 44.0},
	}

	cfg := Config{
		Surfaces: surfaces,
		Variables: []Variable{{
			Name:      "s1_c",
			SurfaceID: 1,
			Param:     "curvature",
			Min:       0.05,
			Max:       0.2,
		}},
		Fields:       []types.FieldItem{{ID: 0, AngleDeg: 0, Weight: 1}, {ID: 1, AngleDeg: 16, Weight: 1}},
		MeritTerms:   []MeritTerm{{FieldAngle: 0, FieldWeight: 1, Wavelength: 0.00058756, WavWeight: 1, Weight: 1}},
		GlassCatalog: gc,
		RefSurface:   8,
		NumRays:      32,
	}

	opt := NewOptimizer(cfg)
	opt.UpdatePupils([]float64{0.0})
	zLo := opt.configs[0].pupilZ
	if zLo == 0 {
		t.Fatal("UpdatePupils did not derive a pupil Z at x=0")
	}

	opt.UpdatePupils([]float64{1.0})
	zHi := opt.configs[0].pupilZ
	if zHi == 0 {
		t.Fatal("UpdatePupils did not derive a pupil Z at x=1")
	}
	if math.Abs(zLo-zHi) < 1e-6 {
		t.Errorf("pupil Z did not follow the variables: x=0 -> %v, x=1 -> %v", zLo, zHi)
	}
}

// TestSizeAutoAperturesCoversAllFields verifies that sizeAutoApertures measures
// the beam of every config field, not just the fields that happen to carry a
// grid merit term. A merit that drives the corner only through wavefront terms
// (e.g. wavefront_sphere_rms) must still size the auto_aperture surfaces to
// cover the corner beam; otherwise the corner's wavefront grid clips and the
// fit collapses to the degenerate penalty.
func TestSizeAutoAperturesCoversAllFields(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	surfs := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.02, Thickness: 5.0, Material: types.Material{Key: "N-BK7"}, Diameter: 10.0, AutoAperture: true},
		{ID: 2, Type: types.Sphere, Curvature: -0.02, Thickness: 50.0, Material: types.Material{}, Diameter: 10.0, AutoAperture: true},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 10.0, AutoAperture: true},
	}
	surface.Precompute(surfs)
	fields := []types.FieldItem{
		{ID: 0, AngleDeg: 0, Weight: 1},
		{ID: 1, AngleDeg: 14.3, Weight: 1},
	}
	cfg := Config{
		Surfaces:     surfs,
		Variables:    []Variable{},
		Fields:       fields,
		MeritTerms:   []MeritTerm{{Kind: MeritWavefrontSphereRMS, FieldAngle: 14.3, FieldIndex: 1, FieldWeight: 1, Wavelength: 0.00058756, WavWeight: 1, Weight: 1}},
		GlassCatalog: gc,
		NumRays:      64,
	}
	opt := NewOptimizer(cfg)
	ccfg := opt.primaryConfig()
	resized := make([]types.Surface, len(surfs))
	copy(resized, surfs)
	opt.restoreDiameters(ccfg, resized)
	opt.sizeAutoApertures(ccfg, resized, gc, nil)

	// The corner (14.3°) beam extent at each surface must be covered by the
	// sized diameter. Measure it directly with the beam-aware extent grid.
	cornerExtents := dls.TraceFieldGridExtents(gc, resized, 0, 0, 14.3, []float64{0, 1}, 0.00058756, 1.0, 64, 0, 1)
	for i := range resized {
		if !resized[i].AutoAperture {
			continue
		}
		need := cornerExtents[resized[i].ID]
		have := resized[i].Diameter / 2
		if need > 0 && have < need {
			t.Errorf("surface %d diameter/2 = %v, corner beam needs %v (corner not measured by sizeAutoApertures)", resized[i].ID, have, need)
		}
	}

	// The corner wavefront fit must run (real value, not the 0.001 penalty).
	term := &ccfg.meritTerms[0]
	val := opt.evaluateWavefrontTerm(ccfg, term, resized, gc)
	if val == 0.001 {
		t.Fatalf("corner wavefront fit collapsed to the degenerate penalty after sizing")
	}
	if val >= 1e6 || math.IsInf(val, 0) {
		t.Fatalf("corner wavefront fit returned degenerate value %v", val)
	}
}

// TestWavefrontTermFieldVignetting verifies that wavefront merit terms carry
// the field index and that evaluateWavefrontTerm applies the field's declared
// vignetting to the pupil-grid clip. A field whose vignetting clips the whole
// pupil must return the bounded degenerate penalty; the same design without
// the clip must return a real value.
func TestWavefrontTermFieldVignetting(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	surfs := singletSurfaces()
	surface.Precompute(surfs)
	fields := []types.FieldItem{
		{ID: 0, AngleDeg: 0, Weight: 1},
	}

	// Without declared vignetting the fit runs and returns a real value.
	cfg := Config{
		Surfaces:     surfs,
		Variables:    []Variable{},
		Fields:       fields,
		MeritTerms:   []MeritTerm{{Kind: MeritWavefrontAstigmatism, FieldAngle: 0, FieldIndex: 0, FieldWeight: 1, Wavelength: 0.00058756, WavWeight: 1, Weight: 1}},
		GlassCatalog: gc,
		NumRays:      64,
	}
	opt := NewOptimizer(cfg)
	ccfg := opt.primaryConfig()
	if ccfg.meritTerms[0].fieldIndex != 0 {
		t.Fatalf("NewOptimizer fieldIndex = %d, want 0", ccfg.meritTerms[0].fieldIndex)
	}
	real := opt.evaluateWavefrontTerm(ccfg, &ccfg.meritTerms[0], surfs, gc)
	if real >= 1.0 {
		t.Fatalf("wavefront term without vignetting = %v, want a real value (< 1)", real)
	}

	// A field whose vignetting clips the entire pupil (compression 1 => zero
	// ellipse semi-axes) must collapse the fit to the bounded penalty.
	clip := types.VignettingDef{CompressionX: 1.0, CompressionY: 1.0}
	cfgV := Config{
		Surfaces:     surfs,
		Variables:    []Variable{},
		Fields:       []types.FieldItem{{ID: 0, AngleDeg: 0, Weight: 1, Vignetting: &clip}},
		MeritTerms:   []MeritTerm{{Kind: MeritWavefrontAstigmatism, FieldAngle: 0, FieldIndex: 0, FieldWeight: 1, Wavelength: 0.00058756, WavWeight: 1, Weight: 1}},
		GlassCatalog: gc,
		NumRays:      64,
	}
	optV := NewOptimizer(cfgV)
	ccfgV := optV.primaryConfig()
	penalty := optV.evaluateWavefrontTerm(ccfgV, &ccfgV.meritTerms[0], surfs, gc)
	if penalty != 0.001 {
		t.Fatalf("wavefront term with full-clip vignetting = %v, want the degenerate penalty 0.001", penalty)
	}

	// buildMeritTermFromTypes (multi-config path) must also carry the field
	// index from types.MeritTerm.Field.
	ci := ConfigInput{ID: "c", Fields: fields}
	mt := buildMeritTermFromTypes(types.MeritTerm{Kind: MeritWavefrontAstigmatism, Field: 0, Wavelength: 0.00058756}, ci)
	if mt.fieldIndex != 0 {
		t.Fatalf("buildMeritTermFromTypes fieldIndex = %d, want 0", mt.fieldIndex)
	}
}

// TestOptimizerWavefrontKinds verifies the wavefront paraboloid merit kinds
// evaluate the same coefficients the standalone wavefront analysis fits: with
// each term's target equal to the wavefront.Compute paraboloid value of the
// (on-axis, pupil-independent) field, the merit must be ~0.
func TestOptimizerWavefrontKinds(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	system := types.System{Surfaces: []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.02, Thickness: 5.0, Material: types.Material{Key: "N-BK7"}, Diameter: 30.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.02, Thickness: 100.0, Material: types.Material{}, Diameter: 30.0},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 30.0},
	}}
	surface.Precompute(system.Surfaces)
	const wl = 0.00058756

	wf, err := wavefront.Compute(system, gc, []types.FieldDef{{Angle: 0, Direction: []float64{0, 1}}}, []float64{wl}, wavefront.Options{NumRays: 64, ReferenceSurface: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(wf.Fields) == 0 {
		t.Fatal("no wavefront result")
	}
	pab := wf.Fields[0].Paraboloid

	targets := map[string]float64{
		MeritWavefrontDefocus:     pab.Defocus,
		MeritWavefrontAstigmatism: pab.Astigmatism,
		MeritWavefrontTilt:        pab.Tilt,
		MeritWavefrontRMSResidual: pab.RMSResidual,
		MeritWavefrontX2:          pab.X2,
		MeritWavefrontY2:          pab.Y2,
		MeritWavefrontXY:          pab.XY,
		MeritWavefrontX:           pab.X,
		MeritWavefrontY:           pab.Y,
		MeritWavefrontConstant:    pab.Constant,
		// The sphere kinds read the reference-sphere residual (piston+tilt+
		// defocus removed, astigmatism retained) — the wavefront Statistics.RMS
		// / PV, the exact quantity psf reports as rms_opd and the Strehl
		// determinant.
		MeritWavefrontSphereRMS: wf.Fields[0].Statistics.RMS,
		MeritWavefrontSpherePV:  wf.Fields[0].Statistics.PV,
	}

	for kind, target := range targets {
		cfg := Config{
			Surfaces:  system.Surfaces,
			Variables: []Variable{},
			MeritTerms: []MeritTerm{{
				Kind:        kind,
				FieldAngle:  0.0,
				FieldWeight: 1.0,
				Wavelength:  wl,
				WavWeight:   1.0,
				Weight:      1.0,
				Target:      target,
			}},
			GlassCatalog: gc,
			NumRays:      64,
		}
		opt := NewOptimizer(cfg)
		x := opt.getInitialState()
		merit := opt.EvaluateMerit(x)
		if merit > 1e-10 {
			t.Errorf("%s: merit with target = wavefront-computed value = %v, want ~0", kind, merit)
		}
	}
}

// TestOptimizerWavefrontRefSurfaceImagePlane verifies the wavefront merit kinds
// tolerate a chief reference surface set to the image plane (the conventional
// last surface). A wavefront reference surface must lie before the image plane,
// so the evaluator falls back to the last optical surface instead of returning
// the 1e6 penalty; with the term's target equal to that surface's paraboloid
// value the merit must be ~0.
func TestOptimizerWavefrontRefSurfaceImagePlane(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	system := types.System{Surfaces: []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.02, Thickness: 5.0, Material: types.Material{Key: "N-BK7"}, Diameter: 30.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.02, Thickness: 100.0, Material: types.Material{}, Diameter: 30.0},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 30.0},
	}}
	surface.Precompute(system.Surfaces)
	const wl = 0.00058756

	wf, err := wavefront.Compute(system, gc, []types.FieldDef{{Angle: 0, Direction: []float64{0, 1}}}, []float64{wl}, wavefront.Options{NumRays: 64, ReferenceSurface: 2})
	if err != nil {
		t.Fatal(err)
	}
	pab := wf.Fields[0].Paraboloid

	cfg := Config{
		Surfaces:  system.Surfaces,
		Variables: []Variable{},
		MeritTerms: []MeritTerm{{
			Kind:        MeritWavefrontRMSResidual,
			FieldAngle:  0.0,
			FieldWeight: 1.0,
			Wavelength:  wl,
			WavWeight:   1.0,
			Weight:      1.0,
			Target:      pab.RMSResidual,
		}},
		GlassCatalog: gc,
		NumRays:      64,
		RefSurface:   3, // image plane: must fall back to surface 2
	}
	opt := NewOptimizer(cfg)
	merit := opt.EvaluateMerit(opt.getInitialState())
	if merit > 1e-10 {
		t.Errorf("merit with image-plane ref surface and target = surface-2 value = %v, want ~0", merit)
	}
}

// TestOptimizerWavefrontTermOffAxis verifies a wavefront merit term evaluates a
// finite value for an off-axis field when the pupil is frozen (the in-DLS
// path): the frozen grid must not clip the off-axis beam against a fixed
// aperture.
func TestOptimizerWavefrontTermOffAxis(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	system := types.System{Surfaces: []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.02, Thickness: 5.0, Material: types.Material{Key: "N-BK7"}, Diameter: 50.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.02, Thickness: 100.0, Material: types.Material{}, Diameter: 20.0},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 50.0},
	}}
	surface.Precompute(system.Surfaces)
	const wl = 0.00058756

	cfg := Config{
		Surfaces:  system.Surfaces,
		Variables: []Variable{},
		MeritTerms: []MeritTerm{{
			Kind:        MeritWavefrontRMSResidual,
			FieldAngle:  10.0,
			FieldWeight: 1.0,
			Wavelength:  wl,
			WavWeight:   1.0,
			Weight:      1.0,
			Target:      0.0,
		}},
		GlassCatalog: gc,
		NumRays:      64,
		RefSurface:   2,
	}
	opt := NewOptimizer(cfg)
	merit := opt.EvaluateMerit(opt.getInitialState())
	if merit >= 1e6 || math.IsNaN(merit) || math.IsInf(merit, 0) {
		t.Errorf("off-axis wavefront merit = %v, want finite (< 1e6)", merit)
	}
}

// optimizedTripletSurfaces returns the demo's new-merit optimum of the
// US2645157 triplet (us2645157-degraded.yaml after DLS): off-axis fields whose
// beam envelope exceeds what a coarse polar grid measures, so the old
// FinalApertures sizing (undersized, no clearance) left the lens vignetting the
// beam.
func optimizedTripletSurfaces() []types.Surface {
	return []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.09292859657818629, Thickness: 1.524, Material: types.Material{Key: "SK18"}, Diameter: 10.0, AutoAperture: true},
		{ID: 2, Type: types.Sphere, Curvature: -0.004177, Thickness: 2.3368, Material: types.Material{}, Diameter: 10.0, AutoAperture: true},
		{ID: 3, Type: types.Sphere, Curvature: -0.079717, Thickness: 0.508, Material: types.Material{Key: "SF12"}, Diameter: 6.0, AutoAperture: true},
		{ID: 4, Type: types.Sphere, Curvature: 0.09441, Thickness: 1.4986, Material: types.Material{}, Diameter: 6.0, AutoAperture: true},
		{ID: 5, Type: types.Sphere, Curvature: 0, Thickness: 1.016, Material: types.Material{}, Diameter: 3.7825297358},
		{ID: 6, Type: types.Sphere, Curvature: 0.019599, Thickness: 1.524, Material: types.Material{Key: "SK18"}, Diameter: 6.0, AutoAperture: true},
		{ID: 7, Type: types.Sphere, Curvature: -0.103709, Thickness: 21.36695183553, Material: types.Material{}, Diameter: 6.0, AutoAperture: true},
		{ID: 8, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}},
	}
}

// beamEnvelope measures the true per-surface beam extent with a dense chief
// hex grid (the same measurement chief --clear-aperture uses).
func beamEnvelope(t *testing.T, gc *glass.Catalog, surfaces []types.Surface) map[int]float64 {
	t.Helper()
	fields := []types.FieldDef{
		{Angle: 0.0, Direction: []float64{0, 1}},
		{Angle: 16.0, Direction: []float64{0, 1}},
		{Angle: 24.0, Direction: []float64{0, 1}},
	}
	pol := types.NewCircularJones(true)
	res := chief.DetermineChiefRaysGrid(
		types.System{Surfaces: surfaces, StopSurface: 0},
		fields, 8, 512, gc, pol,
		types.DefaultWavelength, false, types.GridHex, nil, nil, nil,
	)
	engine := ray.NewEngine(gc, nil)
	path := dls.BuildPath(surfaces)
	return chief.BeamEnvelope(res, engine, surfaces, path, types.DefaultWavelength, pol)
}

// TestFinalAperturesCoverBeam regresses the undersized auto_aperture diameters
// in the optimize output: FinalApertures must measure the true beam envelope
// (dynamic-pupil hex grid, auto-aperture checks skipped) and add the clearance,
// so the output lens never vignettes the off-axis beam. The old sizing used a
// coarse polar grid that under-measured the 24° envelope and applied no
// margin, leaving the front surface smaller than the beam.
func TestFinalAperturesCoverBeam(t *testing.T) {
	gc := tripletGC()
	surfaces := optimizedTripletSurfaces()
	surface.Precompute(surfaces)

	env := beamEnvelope(t, gc, surfaces)
	if env[1] < 4.0 {
		t.Fatalf("expected the optimized front-surface beam envelope > 4.0 mm, got %.3f (test setup)", env[1])
	}

	cfg := Config{
		Surfaces:       surfaces,
		Fields:         []types.FieldItem{{ID: 0, AngleDeg: 0}, {ID: 1, AngleDeg: 16}, {ID: 2, AngleDeg: 24}},
		RefSurface:     8,
		NumRays:        64,
		GlassCatalog:   gc,
		ApertureMargin: 1.0,
	}
	for _, fa := range []float64{0, 16, 24} {
		cfg.MeritTerms = append(cfg.MeritTerms, MeritTerm{Kind: "spot_rms", FieldAngle: fa, FieldWeight: 1, Wavelength: types.DefaultWavelength, WavWeight: 1, Weight: 1})
	}
	opt := NewOptimizer(cfg)
	opt.UpdatePupils(nil)
	aps := opt.FinalApertures(nil)

	for _, s := range surfaces {
		if !s.AutoAperture {
			continue
		}
		got := aps["config1"][s.ID]
		want := 2 * env[s.ID]
		if got < want {
			t.Errorf("auto_aperture surf%d diameter = %.3f, want >= %.3f (covers the true beam envelope)", s.ID, got, want)
		}
	}
}
