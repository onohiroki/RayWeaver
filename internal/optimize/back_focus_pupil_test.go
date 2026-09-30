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
	plainShift, err := wavefrontBackFocusShiftFor(backFocusApertureTriplet(40), bf, 0, 0, nil, fieldDefs, gc, "", nil)
	if err != nil {
		t.Fatalf("test setup: plain wavefront solve failed: %v", err)
	}
	pupilShift, err := wavefrontBackFocusShiftFor(backFocusApertureTriplet(40), bf, 0, 0, nil, fieldDefs, gc, "", pupil)
	if err != nil {
		t.Fatalf("test setup: virtual-pupil wavefront solve failed: %v", err)
	}
	if math.Abs(pupilShift-plainShift) < 1e-12 {
		t.Fatalf("test setup: the virtual pupil does not change the solved plane (%v)", plainShift)
	}

	// The exported solve must reproduce the model's shift exactly: a missing
	// PupilModel would reproduce plainShift instead.
	surfaces := backFocusApertureTriplet(40)
	before := backFocusTargetThickness(surfaces, 7)
	if !ApplyBackFocusSolve(surfaces, bf, "wavefront", 0, 0, fieldItems, nil, gc, "", pupil) {
		t.Fatal("ApplyBackFocusSolve reported failure although the wavefront analysis succeeded")
	}
	if got := backFocusTargetThickness(surfaces, 7) - before; math.Abs(got-pupilShift) > 1e-9 {
		t.Errorf("solved shift %.9f does not match the virtual-pupil wavefront shift %.9f", got, pupilShift)
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
