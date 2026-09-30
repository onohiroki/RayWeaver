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
	plain, err := wavefrontBackFocusShiftFor(backFocusApertureTriplet(40), bf, 0, 0, nil, fieldDefs, gc, "", nil)
	if err != nil {
		t.Fatalf("test setup: plain wavefront solve failed: %v", err)
	}
	pupilShift, err := wavefrontBackFocusShiftFor(backFocusApertureTriplet(40), bf, 0, 0, nil, fieldDefs, gc, "", pupil)
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

	out, err := wavefrontBackFocusShiftFor(surfaces, bf, 0, 0, nil, both, gc, "", nil)
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
	alone, err := wavefrontBackFocusShiftFor(surfaces, bf, 0, 0, nil, survivor, gc, "", nil)
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
