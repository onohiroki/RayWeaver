package psf

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/types"
)

// quadraticSpotSamples builds a perfect (aberration-free) converging bundle:
// every sample sits on the z = 0 plane and points at the on-axis focus at
// z = focus. Its spot radius is therefore exactly zero at u = focus and grows
// quadratically away from it, which makes the analytic minimiser checkable by
// hand.
func quadraticSpotSamples(focus float64, radius float64) []WavefrontSample {
	var out []WavefrontSample
	for i := 0; i < 7; i++ {
		for j := 0; j < 7; j++ {
			x := -radius + 2*radius*float64(i)/6
			y := -radius + 2*radius*float64(j)/6
			if math.Hypot(x, y) > radius {
				continue
			}
			dx, dy, dz := -x, -y, focus
			n := math.Sqrt(dx*dx + dy*dy + dz*dz)
			out = append(out, WavefrontSample{
				Position:  types.Vec3{X: x, Y: y, Z: 0},
				Direction: types.Vec3{X: dx / n, Y: dy / n, Z: dz / n},
				Area:      1,
				Intensity: 1,
			})
		}
	}
	return out
}

// TestSpotFocusCoefficientsMatchSpotDecompose pins the closed-form quadratic
// against the code it replaces: spotFocusCoefficients must reproduce the
// flux-weighted spot radius spotDecompose reports at *every* plane, and its
// minimiser must sit at the bundle's focus.
func TestSpotFocusCoefficientsMatchSpotDecompose(t *testing.T) {
	const focus = 50.0
	samples := quadraticSpotSamples(focus, 2.0)
	if len(samples) < 3 {
		t.Fatalf("test setup: %d samples", len(samples))
	}
	const planeZ = 100.0
	b, c, ok := spotFocusCoefficients(samples, planeZ)
	if !ok {
		t.Fatal("spotFocusCoefficients rejected a well-formed bundle")
	}
	a := spotDecompose(samples, planeZ, 0, 1).SpotRMS
	a2 := a * a

	for _, u := range []float64{-3, -0.5, 0, 0.25, 1.5, focus - planeZ - 2, focus - planeZ} {
		got := spotDecompose(samples, planeZ+u, 0, 1).SpotRMS
		want := a2 + b*u + c*u*u
		if math.Abs(got*got-want) > 1e-9 {
			t.Errorf("RMS² at u=%v: spotDecompose %v, quadratic fit %v", u, got*got, want)
		}
	}

	// The minimiser of the fitted quadratic is the focus: u* = -b/(2c).
	uStar := -b / (2 * c)
	if math.Abs(uStar-(focus-planeZ)) > 1e-6 {
		t.Errorf("analytic focus: got u*=%v, want %v", uStar, focus-planeZ)
	}
	if c <= 0 {
		t.Errorf("a converging bundle must have a positive quadratic coefficient, got %v", c)
	}
	// The fitted minimum is the true minimum: a neighbour plane must be worse.
	atMin := spotDecompose(samples, planeZ+uStar, 0, 1).SpotRMS
	atNeighbour := spotDecompose(samples, planeZ+uStar+0.1, 0, 1).SpotRMS
	if atNeighbour <= atMin {
		t.Errorf("spot radius at the fitted focus (%v) must be below its neighbour (%v)", atMin, atNeighbour)
	}
}

// TestSpotFocusShiftRecoversDeliberateDefocus is the end-to-end property: a
// singlet whose image plane sits 3 mm behind focus must be pulled back by
// SpotFocusShift, and the resulting plane must actually minimise the spot the
// geometric solve optimises.
func TestSpotFocusShiftRecoversDeliberateDefocus(t *testing.T) {
	sys, gc := singletSystemBFL(8, 98.1) // paraxial focus ≈ 95.1 mm
	fields := []types.FieldDef{{Angle: 0, Direction: []float64{0, 1}}}
	wl := []float64{testWavelength}

	res, err := SpotFocusShift(sys, gc, fields, wl, nil, Options{NumRays: 200})
	if err != nil {
		t.Fatalf("SpotFocusShift: %v", err)
	}
	if len(res.FieldsDropped) != 0 {
		t.Errorf("unexpected dropped fields: %v", res.FieldsDropped)
	}
	if res.FieldsUsed != 1 {
		t.Errorf("FieldsUsed: got %d, want 1", res.FieldsUsed)
	}
	// The plane is 3 mm past focus, so the correction is about -3 mm. The
	// singlet's spherical aberration moves the geometric minimum slightly
	// towards the circle of least confusion, hence the loose tolerance.
	if res.ShiftMM > -2.0 || res.ShiftMM < -4.0 {
		t.Errorf("shift %v mm should recover the ~-3 mm deliberate defocus", res.ShiftMM)
	}

	// The prescribed plane must be the spot-RMS minimum: neighbours on either
	// side can only be worse.
	at := spotRMSAt(t, sys, gc, fields, wl, res.ShiftMM)
	for _, d := range []float64{-0.5, -0.2, 0.2, 0.5} {
		neighbour := spotRMSAt(t, sys, gc, fields, wl, res.ShiftMM+d)
		if neighbour <= at {
			t.Errorf("RMS² at the solved plane (%v) must beat the plane %.1f mm away (%v)", at, d, neighbour)
		}
	}
}

// spotRMSAt evaluates the flux-weighted spot radius² of the single field at
// the image plane shifted by shift (the metric SpotFocusShift minimises).
func spotRMSAt(t *testing.T, sys types.System, gc *glass.Catalog, fields []types.FieldDef, wl []float64, shift float64) float64 {
	t.Helper()
	res, err := ComputeSpot(sys, gc, fields, wl, Options{NumRays: 200, PlaneShift: shift})
	if err != nil {
		t.Fatalf("ComputeSpot: %v", err)
	}
	if len(res) == 0 {
		t.Fatal("ComputeSpot returned no result")
	}
	return res[0].SpotRMS * res[0].SpotRMS
}

// TestSpotFocusShiftRejectsBadInput covers the contract the caller relies on
// for its paraxial fallback: an unsolvable request is an error, never a
// silently unshifted plane.
func TestSpotFocusShiftRejectsBadInput(t *testing.T) {
	sys, gc := singletSystem(8)
	wl := []float64{testWavelength}

	if _, err := SpotFocusShift(sys, gc, nil, wl, nil, Options{NumRays: 64}); err == nil {
		t.Error("no field: want an error")
	}
	fields := []types.FieldDef{{Angle: 0, Direction: []float64{0, 1}}}
	if _, err := SpotFocusShift(sys, gc, fields, wl, []float64{1, 1}, Options{NumRays: 64}); err == nil {
		t.Error("weight/field mismatch: want an error")
	}
	if _, err := SpotFocusShift(sys, gc, fields, wl, []float64{0}, Options{NumRays: 64}); err == nil {
		t.Error("non-positive total weight: want an error")
	}
}
