package optimize

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// backFocusApertureTriplet is the power-solve triplet with one auto_aperture
// surface set to diameter (the ID-5 surface, the one the back-focus solve of
// this fixture is aperture-sensitive to).
func backFocusApertureTriplet(diameter float64) []types.Surface {
	s := powerSolveTripletSurfaces()
	for i := range s {
		if s[i].ID == 5 {
			s[i].AutoAperture = true
			s[i].Diameter = diameter
		}
	}
	surface.Precompute(s)
	return s
}

func backFocusTargetThickness(surfaces []types.Surface, id int) float64 {
	for _, s := range surfaces {
		if s.ID == id {
			return s.Thickness
		}
	}
	return math.NaN()
}

// TestBackFocusSolveForwardsVirtualPupil covers the defect that shipped
// recorded escape minima with a stale image plane: the exported solve dropped
// the document's virtual entrance pupil, so it focused a different bundle than
// the merit grid, `psf` and `wavefront` sample — and when that bundle was
// clipped by a tight auto-aperture the wavefront fit failed ("singular normal
// matrix"), which the caller swallowed as a 0 mm shift. The solve must forward
// the pupil model (its result must match the wavefront analysis run with that
// model) and must report a failure instead of silently keeping the plane it
// was handed.
func TestBackFocusSolveForwardsVirtualPupil(t *testing.T) {
	gc := tripletGC()
	bf := &types.BackFocusSolveConfig{
		Enabled: true, Surface: 7, Type: "wavefront",
		WeightType: "uniform", NumRays: 48,
	}
	fieldDefs := []types.FieldDef{{Angle: 0}}
	fieldItems := []types.FieldItem{{ID: 0, AngleDeg: 0}}
	pupil := &types.PupilModelConfig{
		Mode: "virtual_entrance_pupil", AxialPosition: 0, Diameter: 8,
	}

	// Premise: the pupil model changes what the wavefront analysis sees, so
	// dropping it would be observable (and, on a clipped system, fatal).
	plain, err := wavefrontBackFocusShiftFor(backFocusApertureTriplet(40), bf, 0, 0, fieldDefs, gc, "", nil)
	if err != nil {
		t.Fatalf("test setup: plain wavefront solve failed: %v", err)
	}
	pupilShift, err := wavefrontBackFocusShiftFor(backFocusApertureTriplet(40), bf, 0, 0, fieldDefs, gc, "", pupil)
	if err != nil {
		t.Fatalf("test setup: virtual-pupil wavefront solve failed: %v", err)
	}
	if math.Abs(pupilShift.ShiftMM-plain.ShiftMM) < 1e-12 {
		t.Fatalf("test setup: the virtual pupil does not change the solved plane (%v)", plain.ShiftMM)
	}

	// The exported solve must reproduce the model's shift exactly: a missing
	// PupilModel would reproduce plain.ShiftMM instead.
	surfaces := backFocusApertureTriplet(40)
	before := backFocusTargetThickness(surfaces, 7)
	if !ApplyBackFocusSolve(surfaces, bf, "wavefront", 0, 0, fieldItems, nil, gc, "", pupil) {
		t.Fatal("ApplyBackFocusSolve reported failure although the wavefront analysis succeeded")
	}
	if got := backFocusTargetThickness(surfaces, 7) - before; math.Abs(got-pupilShift.ShiftMM) > 1e-9 {
		t.Errorf("solved shift %.9f does not match the virtual-pupil wavefront shift %.9f", got, pupilShift.ShiftMM)
	}

	// A failed analysis must be reported, never swallowed: the plane keeps the
	// thickness it had and the caller learns why.
	clipped := backFocusApertureTriplet(2.0)
	warnings := captureOptimizeWarnings(t)
	if ApplyBackFocusSolve(clipped, bf, "wavefront", 0, 0, fieldItems, nil, gc, "", pupil) {
		t.Error("ApplyBackFocusSolve reported success although the wavefront analysis failed")
	}
	if len(*warnings) == 0 || !strings.Contains(strings.Join(*warnings, "\n"), "back-focus wavefront solve") {
		t.Errorf("expected a loud wavefront failure, got warnings: %v", *warnings)
	}
}

// TestBackFocusPupilModelFollowsTheVariable pins the resolver used by the
// in-run solve: the pupil a back-focus solve samples is the document's model
// with *this evaluation's* pupil-model variable values on top, never the
// declared model alone. axial_position/diameter are optimisation variables
// (the vp_z demo sweeps -20..+50 mm), so the declared value is only the seed.
func TestBackFocusPupilModelFollowsTheVariable(t *testing.T) {
	declared := &types.PupilModelConfig{
		Mode: "virtual_entrance_pupil", AxialPosition: 15, Diameter: 12.5,
	}
	cfg := &config{pupilModel: declared}

	got := backFocusPupilModel(cfg, appliedPupil{active: true, z: 22.57, dia: 11})
	if got == nil {
		t.Fatal("an active pupil produced no model for the solve")
	}
	if got.AxialPosition != 22.57 {
		t.Errorf("solve pupil axial_position = %v, want the variable's 22.57", got.AxialPosition)
	}
	if got.Diameter != 11 {
		t.Errorf("solve pupil diameter = %v, want the variable's 11", got.Diameter)
	}
	if declared.AxialPosition != 15 || declared.Diameter != 12.5 {
		t.Errorf("the declared model was mutated: %+v, want 15/12.5", declared)
	}

	// No active pupil for this evaluation: the declared model still is what
	// the analysis samples (a document without variables, or one whose values
	// never landed).
	if got := backFocusPupilModel(cfg, appliedPupil{z: 99}); got == nil || got.AxialPosition != 15 {
		t.Errorf("inactive pupil must keep the declared model, got %+v", got)
	}

	// An active variable with no declared model (a pupil model that exists
	// only in the optimisation vector): build a virtual one.
	bare := backFocusPupilModel(&config{}, appliedPupil{active: true, z: -7, dia: 9})
	if bare == nil || bare.Mode != "virtual_entrance_pupil" || bare.AxialPosition != -7 || bare.Diameter != 9 {
		t.Errorf("variable-only pupil = %+v, want a virtual model at -7/9", bare)
	}

	// Neither present: nil, so the solve falls back to the plain wavefront
	// analysis exactly as it did before virtual pupils existed.
	if m := backFocusPupilModel(&config{}, appliedPupil{}); m != nil {
		t.Errorf("no pupil anywhere produced %+v, want nil", m)
	}
}

// TestBackFocusSolveFollowsThePupilVariable is the solver-side half of the
// stale-pupil defect: within a run, the image plane must be solved for the
// pupil-model *variables of the current evaluation*, because that is the
// bundle the merit grid, `psf`, `wavefront` and the gate all trace. Passing
// the declared model focused every iteration for the seed z instead; on the
// v50 vp_z sweep (declared 15, saved 7.7..28.8) that delivered minima
// 0.01..0.77 mm from their own best focus.
//
// The fixture is an off-axis field, which is what makes the plane depend on
// the pupil *position* at all: an on-axis bundle is invariant under a z slide,
// an off-axis one's footprint moves with it.
func TestBackFocusSolveFollowsThePupilVariable(t *testing.T) {
	const declaredZ, variableZ = -2.0, -20.0

	gc := zeroPupilGC()
	base := zeroPupilSurfaces()
	fields := []types.FieldItem{{ID: 1, AngleDeg: 16, Weight: 1}}
	bf := &types.BackFocusSolveConfig{
		Enabled: true, Type: "wavefront", WeightType: "uniform", NumRays: 32,
	}

	// Reference planes over the pre-solve surfaces: applyVariables performs
	// exactly one solve and touches nothing else here (no auto_aperture, no
	// power solve), so these are the values the solver must reproduce.
	solveAt := func(z float64) float64 {
		work := append([]types.Surface(nil), base...)
		surface.Precompute(work)
		if !ApplyBackFocusSolve(work, bf, "wavefront", 0, 0, fields, nil, gc, "",
			&types.PupilModelConfig{Mode: "virtual_entrance_pupil", AxialPosition: z, Diameter: 10}) {
			t.Fatalf("test setup: the reference solve at pupil z=%v failed", z)
		}
		return backFocusTargetThickness(work, 2)
	}
	want := solveAt(variableZ)
	declaredPlane := solveAt(declaredZ)
	if math.Abs(want-declaredPlane) < 1e-3 {
		t.Fatalf("test setup: the fixture is not pupil-sensitive (%.6f at both %v and %v)",
			want, declaredZ, variableZ)
	}

	opt := NewOptimizer(Config{
		Surfaces:     base,
		GlassCatalog: gc,
		Variables: []Variable{
			{Name: "pmz", SurfaceID: 0, Param: "pupil_model_axial_position", Min: -40, Max: 50},
		},
		PupilModel: &types.PupilModelConfig{
			Mode: "virtual_entrance_pupil", AxialPosition: declaredZ, Diameter: 10,
		},
		Fields: fields,
		MeritTerms: []MeritTerm{{
			FieldAngle: 16, FieldWeight: 1, Wavelength: 0.00058756, WavWeight: 1, Weight: 1,
		}},
		NumRays: 24,
	})
	opt.SetBackFocusSolve(bf)
	entries := opt.backFocusTargets["config1"]
	if len(entries) == 0 || entries[0].solveID != 2 {
		t.Fatalf("back-focus target = %+v, want the singlet's rear surface 2", entries)
	}

	x := opt.getInitialState()
	if x[0] != declaredZ {
		t.Fatalf("initial axial_position = %v, want the declared %v", x[0], declaredZ)
	}
	x[0] = variableZ

	surfMap, _, _ := opt.applyVariables(x)
	got := backFocusTargetThickness(surfMap["config1"], 2)

	if math.Abs(got-want) > 1e-9 {
		t.Errorf("solved plane = %.9f, want %.9f (the variable's own pupil); solving at the declared %v would give %.9f — the pupil-model variable would be ignored",
			got, want, declaredZ, declaredPlane)
	}
}

// captureOptimizeWarnings redirects the package warning hook for the duration
// of the test and collects everything reported through it.
func captureOptimizeWarnings(t *testing.T) *[]string {
	t.Helper()
	got := &[]string{}
	prev := Warnf
	Warnf = func(format string, args ...interface{}) {
		*got = append(*got, fmt.Sprintf(format, args...))
	}
	t.Cleanup(func() { Warnf = prev })
	return got
}

// TestBackFocusPartialSolveKeepsTheSurvivingFields covers the A1 fix: the
// in-run wavefront back-focus solve used to be all-or-nothing, so a single
// degenerate field — a clipped off-axis bundle under a tight aperture, the
// "only 0 valid grid rays" / "paraboloid fit: singular normal matrix" failure
// seen in the v45 smoke — aborted the whole solve and left the image plane
// stale while the merit kept improving against it. With Options.Partial one
// bad field must be dropped and reported, the solve must still return the
// best focus of the fields that did analyse, and that shift must equal the
// shift of the surviving field run on its own (uniform weighting over one
// survivor).
func TestBackFocusPartialSolveKeepsTheSurvivingFields(t *testing.T) {
	gc := tripletGC()
	bf := &types.BackFocusSolveConfig{
		Enabled: true, Surface: 7, Type: "wavefront",
		WeightType: "uniform", NumRays: 48,
	}
	both := []types.FieldDef{{Angle: 0}, {Angle: 16}}
	surfaces := backFocusApertureTriplet(2.0)

	out, err := wavefrontBackFocusShiftFor(surfaces, bf, 0, 0, both, gc, "", nil)
	if err != nil {
		t.Fatalf("partial solve reported a total failure although one field may analyse: %v", err)
	}
	if out.FieldsUsed == 0 && len(out.FieldsDropped) == 0 {
		t.Fatal("test setup: neither a field count nor a dropped list came back")
	}
	if out.FieldsUsed+len(out.FieldsDropped) != len(both) {
		t.Errorf("used %d + dropped %d != %d requested fields",
			out.FieldsUsed, len(out.FieldsDropped), len(both))
	}
	if len(out.FieldsDropped) == 0 {
		t.Skip("both fields analysed on this fixture; the drop path is not exercised")
	}

	// The surviving field alone must reproduce the partial solve's shift.
	survivor := []types.FieldDef{}
	for i, fd := range both {
		dropped := false
		for _, d := range out.FieldsDropped {
			if strings.Contains(d, fmt.Sprintf("field %d ", i)) || strings.Contains(d, fmt.Sprintf("field %d @", i)) {
				dropped = true
			}
		}
		if !dropped {
			survivor = append(survivor, fd)
		}
	}
	if len(survivor) == 0 {
		t.Fatalf("every field was dropped but the solve reported success: %v", out.FieldsDropped)
	}
	alone, err := wavefrontBackFocusShiftFor(surfaces, bf, 0, 0, survivor, gc, "", nil)
	if err != nil {
		t.Fatalf("surviving-field solve failed: %v", err)
	}
	if math.Abs(out.ShiftMM-alone.ShiftMM) > 1e-9 {
		t.Errorf("partial shift %.9f != surviving field's own shift %.9f (dropped: %v)",
			out.ShiftMM, alone.ShiftMM, out.FieldsDropped)
	}

	// The failure must stay visible on stderr rather than being swallowed.
	warnings := captureOptimizeWarnings(t)
	clipped := backFocusApertureTriplet(2.0)
	ok := ApplyBackFocusSolve(clipped, bf, "wavefront", 0, 0,
		[]types.FieldItem{{ID: 0, AngleDeg: 0}, {ID: 1, AngleDeg: 16}}, nil, gc, "", nil)
	if !ok {
		t.Fatal("ApplyBackFocusSolve failed although at least one field analyses")
	}
	if len(*warnings) == 0 {
		t.Error("the dropped field was reported silently; a partial solve must warn")
	}
}
