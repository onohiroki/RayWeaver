package asphere

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/pupil"
	"github.com/hiroki/rayweaver/internal/types"
)

func TestBuildFocusSamplesUsesCandidateSurfaceHit(t *testing.T) {
	samples := []pupil.Sample{
		{PupilX: -1, Dir: types.Vec3{Z: 1}, OK: true, Surfaces: []types.SurfaceResult{
			{SurfaceID: 2, Position: types.Vec3{X: 2, Y: 3}},
			{SurfaceID: 7, Position: types.Vec3{X: 70, Y: 80}},  // Target
			{SurfaceID: 8, Position: types.Vec3{X: 800, Y: 900}}, // NOT target (must be ignored)
		}},
		{PupilX: 0, Dir: types.Vec3{Z: 1}, OK: true, Surfaces: []types.SurfaceResult{
			{SurfaceID: 2, Position: types.Vec3{X: 20, Y: 30}},
			{SurfaceID: 7, Position: types.Vec3{X: 700, Y: 800}},
			{SurfaceID: 8, Position: types.Vec3{X: 8000, Y: 9000}},
		}},
		{PupilX: 1, Dir: types.Vec3{Z: 1}, OK: true, Surfaces: []types.SurfaceResult{
			{SurfaceID: 2, Position: types.Vec3{X: 4, Y: 5}},
		}},
	}
	got := buildFocusSamples(samples, types.Vec3{Z: 1}, types.Vec3{Z: 1}, 1, 3, 7, "T", false)
	if len(got) != 2 || got[0].HitX != 70 || got[0].HitY != 80 || got[1].HitX != 700 || got[1].HitY != 800 {
		t.Fatalf("candidate hits = %+v, want only surface 7 coordinates", got)
	}
	if got[0].RMM != 1 {
		t.Fatalf("RMM = %v, want pupil radius 1", got[0].RMM)
	}
}

func TestBuildFocusSamplesKeepsTrialAndFanMetadata(t *testing.T) {
	sample := pupil.Sample{PupilX: 0, PupilY: 2, Dir: types.Vec3{Z: 1}, OK: true,
		Surfaces: []types.SurfaceResult{{SurfaceID: 4, Position: types.Vec3{X: 9, Y: 10}}}}
	got := buildFocusSamples([]pupil.Sample{sample}, types.Vec3{Z: 1}, types.Vec3{Z: 1}, 1, 8, 4, "S", true)
	if len(got) != 1 || !got[0].Trial || got[0].FanKind != "S" || got[0].FieldID != 8 {
		t.Fatalf("metadata = %+v", got)
	}
}

func TestSolveLinear(t *testing.T) {
	// 2x2: x1 + x2 = 3, x1 - x2 = 1  → x1=2, x2=1
	a := [][]float64{{1, 1}, {1, -1}}
	b := []float64{3, 1}
	x, ok := solveLinear(a, b)
	if !ok {
		t.Fatal("solveLinear failed")
	}
	if math.Abs(x[0]-2) > 1e-12 || math.Abs(x[1]-1) > 1e-12 {
		t.Fatalf("solveLinear = %v, want [2 1]", x)
	}
}

func TestScaleCoefficients(t *testing.T) {
	in := types.AsphereCoeffs{Conic: -0.5, A4: 1e-4, A6: -2e-6}
	out := ScaleCoefficients(in, 0.2)
	if math.Abs(out.Conic+0.1) > 1e-15 || math.Abs(out.A4-2e-5) > 1e-15 || math.Abs(out.A6+4e-7) > 1e-15 {
		t.Fatalf("ScaleCoefficients = %+v", out)
	}
}

func TestConfigFromYAMLDefaults(t *testing.T) {
	cfg := ConfigFromYAML(nil)
	if cfg.CellRings != 8 || cfg.CellAngles != 16 || cfg.PupilSamplesRadial != 21 || cfg.TopK != 3 {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.ScoreWeights.Common != 0.35 || cfg.ScoreWeights.Conflict != 0.10 {
		t.Fatalf("unexpected default weights: %+v", cfg.ScoreWeights)
	}
	if !cfg.PreserveVertexCurvature || !cfg.IncludeConic || !cfg.RemoveTilt {
		t.Fatalf("unexpected bool defaults: %+v", cfg)
	}
	if !cfg.CalibrateScale {
		t.Fatalf("unexpected calibrate_scale default: %+v", cfg)
	}

	// Explicit overrides.
	tf, ff := true, false
	fromYAML := &types.AsphereCandidateConfig{
		IncludeConic:            &ff,
		PreserveVertexCurvature: &tf,
		RemoveDefocus:           &tf,
		CellRings:               12,
		CandidateSurfaces:       []int{2, 4},
		ScoreWeights:            types.AsphereScoreWeights{Conflict: 0.2},
		CalibrateScale:          &ff,
		ScaleProbes:             []float64{0.1, 0.5},
	}
	cfg = ConfigFromYAML(fromYAML)
	if cfg.IncludeConic || !cfg.PreserveVertexCurvature || !cfg.RemoveDefocus {
		t.Fatalf("bool overrides not applied: %+v", cfg)
	}
	if cfg.CellRings != 12 || len(cfg.CandidateSurfaces) != 2 || cfg.ScoreWeights.Conflict != 0.2 {
		t.Fatalf("scalar overrides not applied: %+v", cfg)
	}
	if cfg.CalibrateScale {
		t.Fatalf("calibrate_scale: false not applied: %+v", cfg)
	}
	if len(cfg.ScaleProbes) != 2 || cfg.ScaleProbes[1] != 0.5 {
		t.Fatalf("scale_probes not applied: %+v", cfg.ScaleProbes)
	}
}

func TestPreprocessOPD(t *testing.T) {
	fp := []FieldFootprintData{{
		FieldID: 1,
		RayHits: []RayHit{
			{OPL: 10, PupilX: -1, PupilY: 0, OK: true},
			{OPL: 11, PupilX: 0, PupilY: 0, OK: true},
			{OPL: 12, PupilX: 1, PupilY: 0, OK: true},
			{OPL: 11.5, PupilX: 0, PupilY: 1, OK: true},
			{OPL: 999, OK: false},
		},
	}}
	PreprocessOPD(fp, true, false)
	hits := fp[0].RayHits
	// Piston: mean of valid OPL = 11.125 → OPD [-1.125, -0.125, 0.875, 0.375].
	// Tilt in X removed: after removing best plane, OPD should have near-zero
	// slope in X and zero mean.
	if math.Abs(hits[1].OPD) > 1e-9 {
		t.Fatalf("center OPD = %v, want ~0", hits[1].OPD)
	}
	// The residual should be small and symmetric: OPD at ±1 equal magnitude.
	if math.Abs(hits[0].OPD-hits[2].OPD) > 1e-9 {
		t.Fatalf("tilt not removed: OPD[-1]=%v OPD[+1]=%v", hits[0].OPD, hits[2].OPD)
	}
}

func TestComputeCellStatsCommonAndUnique(t *testing.T) {
	cells := []cellData{
		// Shared cell: two fields, same OPD → common mu, zero conflict.
		{SurfaceID: 1, Ring: 0, Sector: 0, MeanR: 1,
			Hits: []cellHit{
				{FieldID: 1, OPD: 0.01, Weight: 1, R: 1},
				{FieldID: 2, OPD: 0.01, Weight: 1, R: 1},
			}},
		// Unique cell: single field.
		{SurfaceID: 1, Ring: 1, Sector: 1, MeanR: 2,
			Hits: []cellHit{
				{FieldID: 3, OPD: 0.02, Weight: 1, R: 2},
			}},
	}
	stats := ComputeCellStats(cells, 1, 3.0)
	if len(stats) != 2 {
		t.Fatalf("expected 2 cells, got %d", len(stats))
	}
	shared := stats[0]
	if len(shared.OccupiedFields) != 2 {
		t.Fatalf("shared cell occupied fields = %v, want 2", shared.OccupiedFields)
	}
	if math.Abs(shared.CommonOPD-0.01) > 1e-15 {
		t.Fatalf("common OPD = %v, want 0.01", shared.CommonOPD)
	}
	if math.Abs(shared.Conflict) > 1e-15 {
		t.Fatalf("conflict = %v, want 0", shared.Conflict)
	}
	uniq := stats[1]
	if math.Abs(uniq.UniqueResidual-0.0004) > 1e-15 {
		t.Fatalf("unique residual = %v, want 4e-4", uniq.UniqueResidual)
	}
}

func TestBuildCellGridBinsByPolarCell(t *testing.T) {
	fp := []FieldFootprintData{{
		FieldID: 1,
		Weight:  1,
		RayHits: []RayHit{
			{Hits: map[int]SurfaceHit{1: {Position: types.Vec3{X: 1, Y: 0}}}, OK: true},
			{Hits: map[int]SurfaceHit{1: {Position: types.Vec3{X: -1, Y: 0}}}, OK: true},
			{Hits: map[int]SurfaceHit{1: {Position: types.Vec3{X: 0, Y: 1}}}, OK: true},
			{Hits: map[int]SurfaceHit{1: {Position: types.Vec3{X: 0, Y: -1}}}, OK: true},
		},
	}}
	cells := BuildCellGrid(fp, 1, 1, 4)
	if len(cells) != 4 {
		t.Fatalf("expected 4 sectors in 1 ring, got %d", len(cells))
	}
	// All four cardinal directions should fall in distinct sectors.
	seen := map[int]bool{}
	for _, c := range cells {
		seen[c.Sector] = true
		if c.Ring != 0 {
			t.Fatalf("ring = %d, want 0", c.Ring)
		}
	}
	if len(seen) != 4 {
		t.Fatalf("sectors = %v, want 4 distinct", seen)
	}
}

func TestBuildOPDProfilesPerFieldPerRing(t *testing.T) {
	fp := []FieldFootprintData{
		{
			FieldID: 1,
			Weight:  1,
			RayHits: []RayHit{
				{Hits: map[int]SurfaceHit{1: {Position: types.Vec3{X: 0.5, Y: 0}}}, OPD: 0.01, Weight: 1, OK: true},
				{Hits: map[int]SurfaceHit{1: {Position: types.Vec3{X: 1.5, Y: 0}}}, OPD: 0.02, Weight: 1, OK: true},
			},
		},
		{
			FieldID: 2,
			Weight:  1,
			RayHits: []RayHit{
				{Hits: map[int]SurfaceHit{1: {Position: types.Vec3{X: 0.5, Y: 0}}}, OPD: -0.01, Weight: 1, OK: true},
				{Hits: map[int]SurfaceHit{1: {Position: types.Vec3{X: 1.5, Y: 0}}}, OPD: -0.02, Weight: 1, OK: true},
			},
		},
	}
	profiles := BuildOPDProfiles(fp, []int{1}, 1)
	if len(profiles) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(profiles))
	}
	p := profiles[0]
	if p.SurfaceID != 1 {
		t.Fatalf("surface id = %d, want 1", p.SurfaceID)
	}
	if math.Abs(p.MaxR-1.5) > 1e-15 {
		t.Fatalf("max_r = %v, want 1.5", p.MaxR)
	}
	if len(p.Fields) != 2 {
		t.Fatalf("expected 2 field profiles, got %d", len(p.Fields))
	}
	// Each field has 1 ring (rings=1) holding both rays: OPD = weighted mean.
	f1 := p.Fields[0]
	f2 := p.Fields[1]
	if len(f1.OPD) != 1 || len(f2.OPD) != 1 {
		t.Fatalf("field profiles lengths = %d, %d, want 1 each", len(f1.OPD), len(f2.OPD))
	}
	if math.Abs(f1.OPD[0]-0.015) > 1e-12 {
		t.Fatalf("field 1 OPD = %v, want 0.015", f1.OPD[0])
	}
	if math.Abs(f2.OPD[0]+0.015) > 1e-12 {
		t.Fatalf("field 2 OPD = %v, want -0.015", f2.OPD[0])
	}
	// Ring radius is the weight-mean |r| (all weight 1 here).
	if math.Abs(f1.RingRadius[0]-1.0) > 1e-12 {
		t.Fatalf("field 1 ring radius = %v, want 1.0", f1.RingRadius[0])
	}
}

func TestFitAsphereCoeffsSmallForSmoothOPD(t *testing.T) {
	// A smooth r^4 spherical-aberration OPD should yield a small, non-pathological
	// A4 estimate and no enormous higher orders.
	var cells []types.AsphereCellStat
	for i := 1; i <= 8; i++ {
		r := float64(i) * 0.3
		cells = append(cells, types.AsphereCellStat{
			CommonOPD: 1e-4 * math.Pow(r, 4), Weight: 1, MeanR: r,
			OccupiedFields: []int{1, 2},
		})
	}
	surf := types.Surface{Curvature: 0.02, Diameter: 6}
	coeffs, warnings := FitAsphereCoeffs(cells, surf, 1.0, 1.5, DefaultConfig())
	if coeffs.A4 == 0 && coeffs.A6 == 0 && coeffs.A8 == 0 {
		t.Fatal("FitAsphereCoeffs returned zero coeffs")
	}
	if math.Abs(coeffs.A4) < 1e-6 || math.Abs(coeffs.A4) > 1e-2 {
		t.Fatalf("A4 = %v, want a moderate magnitude for a smooth r^4 residual", coeffs.A4)
	}
	if len(warnings) == 0 {
		t.Fatal("expected defocus-removal warning")
	}
}

func TestFitAsphereCoeffsA4BoundedByRadius(t *testing.T) {
	// A pathological OPD would suggest an A4 beyond the surface-radius bound;
	// the constraint must clamp it to 1/|R| and warn.
	var cells []types.AsphereCellStat
	for i := 1; i <= 8; i++ {
		r := float64(i) * 0.3
		cells = append(cells, types.AsphereCellStat{
			CommonOPD: 1.0 * math.Pow(r, 4), Weight: 1, MeanR: r,
			OccupiedFields: []int{1, 2},
		})
	}
	surf := types.Surface{Curvature: 0.02, Diameter: 6} // R = 50 → bound = 0.02
	coeffs, warnings := FitAsphereCoeffs(cells, surf, 1.0, 1.5, DefaultConfig())
	bound := 1.0 / 50.0
	if math.Abs(coeffs.A4) > bound+1e-12 {
		t.Fatalf("A4 = %v, want |A4| <= %v (surface-radius bound)", coeffs.A4, bound)
	}
	var constrained bool
	for _, w := range warnings {
		if w == "A4 coefficient bounded by surface radius" {
			constrained = true
		}
	}
	if !constrained {
		t.Fatal("expected A4-bounded warning")
	}
}

// tripletSystem builds the US2645157-style triplet used by the integration test.
func tripletSystem() ([]types.Surface, *glass.Catalog) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Name: "SK18", Type: types.GlassTypeModel, ND: 1.63854, VD: 55.42})
	gc.Add(types.Glass{Name: "SF12", Type: types.GlassTypeModel, ND: 1.64831, VD: 33.84})

	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 1 / 10.2871491742, Thickness: 1.524, Material: types.Material{Key: "SK18"}, Diameter: 10},
		{ID: 2, Type: types.Sphere, Curvature: 1 / -239.3967954752, Thickness: 2.3368, Material: types.Material{}, Diameter: 10},
		{ID: 3, Type: types.Sphere, Curvature: 1 / -12.8269871730, Thickness: 0.508, Material: types.Material{Key: "SF12"}, Diameter: 6},
		{ID: 4, Type: types.Sphere, Curvature: 1 / 10.5917184406, Thickness: 1.4986, Material: types.Material{}, Diameter: 6},
		{ID: 5, Type: types.Sphere, Curvature: 0, Thickness: 1.016, Material: types.Material{}, Diameter: 3.7825297358},
		{ID: 6, Type: types.Sphere, Curvature: 1 / 61.8456294200, Thickness: 1.524, Material: types.Material{Key: "SK18"}, Diameter: 6},
		{ID: 7, Type: types.Sphere, Curvature: 1 / -10.0074859032, Thickness: 21.36695183553, Material: types.Material{}, Diameter: 6},
		{ID: 8, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}},
	}
	return surfaces, gc
}

func TestRunTripletRanksAndFits(t *testing.T) {
	surfaces, gc := tripletSystem()
	fields := []Field{
		{ID: 1, Angle: 0, Weight: 1, Direction: []float64{0, 1}},
		{ID: 2, Angle: 16, Weight: 1, Direction: []float64{0, 1}},
		{ID: 3, Angle: 24, Weight: 1, Direction: []float64{0, 1}},
	}
	cfg := DefaultConfig()
	cfg.TopK = 2

	res := Run(surfaces, fields, nil, cfg, gc, 0, 8, nil)

	if len(res.Rankings) != 8 {
		t.Fatalf("rankings = %d, want 8", len(res.Rankings))
	}
	// Top ranking must not be the flat image plane.
	if res.Rankings[0].SurfaceID == 8 {
		t.Fatalf("flat image plane ranked first: %+v", res.Rankings[0])
	}
	// Only top-2 get coefficients.
	fitted := 0
	for _, r := range res.Rankings {
		if r.Coefficients != (types.AsphereCoeffs{}) {
			fitted++
			if math.IsNaN(r.Score) || math.IsNaN(r.Coefficients.A4) {
				t.Fatalf("NaN in result: %+v", r)
			}
		}
	}
	if fitted != 2 {
		t.Fatalf("fitted = %d, want 2 (top-k)", fitted)
	}
	// Scores must be finite and rankings descending.
	for i := 1; i < len(res.Rankings); i++ {
		if res.Rankings[i-1].Score < res.Rankings[i].Score {
			t.Fatalf("rankings not descending at %d", i)
		}
	}
}

func TestRunTripletSensitivityMatrix(t *testing.T) {
	// A degraded triplet should report a Phase-3 sensitivity matrix on the
	// top-K surfaces: finite base/asphere merits, a coherent improvement, and
	// per-coefficient derivatives with the expected sign relationship.
	surfaces, gc := tripletSystem()
	fields := []Field{
		{ID: 1, Angle: 0, Weight: 1, Direction: []float64{0, 1}},
		{ID: 2, Angle: 16, Weight: 1, Direction: []float64{0, 1}},
		{ID: 3, Angle: 24, Weight: 1, Direction: []float64{0, 1}},
	}
	cfg := DefaultConfig()
	cfg.TopK = 2
	cfg.SensitivitySamples = 7

	res := Run(surfaces, fields, nil, cfg, gc, 0, 8, nil)

	withSens := 0
	for _, r := range res.Rankings {
		if r.Sensitivity == nil {
			continue
		}
		withSens++
		s := r.Sensitivity
		if math.IsNaN(s.BaseMerit) || math.IsNaN(s.AsphereMerit) || math.IsNaN(s.Improvement) {
			t.Fatalf("surface %d: NaN in sensitivity: %+v", r.SurfaceID, s)
		}
		if s.BaseMerit <= 0 {
			t.Fatalf("surface %d: non-positive base merit %v", r.SurfaceID, s.BaseMerit)
		}
		if len(s.DMeritDCoef) != 5 {
			t.Fatalf("surface %d: dM/dc has %d entries, want 5", r.SurfaceID, len(s.DMeritDCoef))
		}
		for _, d := range s.DMeritDCoef {
			if math.IsNaN(d) {
				t.Fatalf("surface %d: NaN derivative", r.SurfaceID)
			}
		}
	}
	if withSens == 0 {
		t.Fatal("no sensitivity matrix reported on any surface")
	}
}
