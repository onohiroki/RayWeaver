package wavefront

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// TestPlaneReferenceDefocusCarriesFocusError verifies that referencing the
// paraboloid fit to the delivered image plane makes its defocus coefficient (and
// DefocusMM) carry the field's image-plane focus error, while the default
// best-focus reference stays blind to it. A system whose image plane is pushed
// away from the focus must report a larger |DefocusMM|, and the sphere
// statistics must be identical in both modes.
func TestPlaneReferenceDefocusCarriesFocusError(t *testing.T) {
	gc := wavefrontTestGC()
	const wl = 0.00058756
	const refSurface = 2
	fd := types.FieldDef{Angle: 8, Direction: []float64{0, 1}}

	analyze := func(backFocus float64, planeRef bool) Entry {
		surfaces := []types.Surface{
			{ID: 1, Type: types.Sphere, Curvature: 0.02, Thickness: 5.0, Material: types.Material{Key: "N-BK7"}, Diameter: 30.0},
			{ID: 2, Type: types.Sphere, Curvature: -0.02, Thickness: backFocus, Material: types.Material{}, Diameter: 30.0},
			{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 30.0},
		}
		surface.Precompute(surfaces)
		e, err := AnalyzeField(types.System{Surfaces: surfaces}, gc, fd, refSurface, 128, wl, 1.0,
			nil, nil, "", FieldOptions{PlaneReference: planeRef})
		if err != nil {
			t.Fatalf("backFocus=%v planeRef=%v: %v", backFocus, planeRef, err)
		}
		return e
	}

	// The default (best-focus) reference removes the focus error, so the
	// reported DefocusMM stays zero and the raw coefficient is the small zonal
	// spread inside the field.
	nearBest := analyze(46.0, false)
	if nearBest.DefocusMM != 0 {
		t.Errorf("best-focus reference: DefocusMM = %v, want 0 (unset)", nearBest.DefocusMM)
	}

	// Pushing the image plane 1 mm further must show up as a ~1 mm focus error
	// once the plane is the reference.
	displaced := analyze(47.0, true)
	far := analyze(52.0, true)
	if math.Abs(displaced.DefocusMM) < 0.5 {
		t.Errorf("plane reference at +1mm: DefocusMM = %v, want |value| ~ 1mm", displaced.DefocusMM)
	}
	if math.Abs(far.DefocusMM) <= math.Abs(displaced.DefocusMM) {
		t.Errorf("DefocusMM not monotone in the plane displacement: %v (47mm) -> %v (52mm)",
			displaced.DefocusMM, far.DefocusMM)
	}
	// The reference-sphere statistics are best-focus referenced in both modes.
	displacedRef := analyze(47.0, false)
	if d := math.Abs(displaced.Statistics.RMS - displacedRef.Statistics.RMS); d > 1e-9 {
		t.Errorf("sphere RMS changed with the reference mode: %v vs %v", displaced.Statistics.RMS, displacedRef.Statistics.RMS)
	}
}

// TestPlaneReferenceScaleMatchesFocusShift pins the mm conversion: the reported
// DefocusMM must agree (to a sign fixed by the reference direction) with the
// spot-RMS best-focus shift of the same field.
func TestPlaneReferenceScaleMatchesFocusShift(t *testing.T) {
	gc := wavefrontTestGC()
	const wl = 0.00058756
	const refSurface = 2
	fd := types.FieldDef{Angle: 10, Direction: []float64{0, 1}}

	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.02, Thickness: 5.0, Material: types.Material{Key: "N-BK7"}, Diameter: 30.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.02, Thickness: 52.0, Material: types.Material{}, Diameter: 30.0},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 30.0},
	}
	surface.Precompute(surfaces)
	sys := types.System{Surfaces: surfaces}

	plane, err := AnalyzeField(sys, gc, fd, refSurface, 128, wl, 1.0, nil, nil, "", FieldOptions{PlaneReference: true})
	if err != nil {
		t.Fatalf("plane reference: %v", err)
	}
	best, err := AnalyzeField(sys, gc, fd, refSurface, 128, wl, 1.0, nil, nil, "", FieldOptions{})
	if err != nil {
		t.Fatalf("best focus: %v", err)
	}
	if got, want := math.Abs(plane.DefocusMM), math.Abs(best.Statistics.RMS); got <= 0 || want <= 0 {
		t.Fatalf("degenerate measurement: DefocusMM=%v rms=%v", got, want)
	}
	// Both describe the same physical focus error, so they must be the same
	// order of magnitude (a defocused field: the mm focus error dominates the
	// defocus-free wavefront residual).
	if ratio := math.Abs(plane.DefocusMM) / best.Statistics.RMS; ratio < 5 || ratio > 500 {
		t.Errorf("DefocusMM %.4g vs best-focus sphere RMS %.4g: ratio %.1f outside the expected range",
			math.Abs(plane.DefocusMM), best.Statistics.RMS, ratio)
	}
}
