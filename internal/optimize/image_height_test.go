package optimize

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// pupilLaunchSurfaces is a front-stop singlet: the aperture stop is surface 1,
// so the entrance pupil sits on the first vertex (Z = 0). An image-height field
// probe launched from the optical axis leaves z = -100 at (0,0) and reaches the
// stop plane ~100*tan(theta) off-axis, where it is clipped; only a launch
// through the entrance-pupil centre reaches the image plane. This is the
// geometry that made imageHeightToFieldAngle collapse every off-axis field to
// ~0 on the wide-angle zoom layouts.
func pupilLaunchSurfaces() []types.Surface {
	surfs := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.0303, Thickness: 6.0, Material: types.Material{Key: "N-BK7"}, Diameter: 20.0, Role: "stop"},
		{ID: 2, Type: types.Sphere, Curvature: -0.0303, Thickness: 26.0, Material: types.Material{}, Diameter: 60.0},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 60.0},
	}
	surface.Precompute(surfs)
	return surfs
}

// TestImageHeightToFieldAngleLaunchesThroughPupil pins the field-angle search
// for image-height fields: the probe runs through the entrance-pupil centre,
// the solved angle reproduces the target image height, and a signed target
// recovers a signed angle. The axis launch (no pupil) is shown to be clipped,
// which is the failure the pupil-centre launch fixes.
func TestImageHeightToFieldAngleLaunchesThroughPupil(t *testing.T) {
	gc, surfs := zeroPupilGC(), pupilLaunchSurfaces()
	const wl = 0.00058756
	zero := 0.0

	h15 := traceChiefImageHeight(surfs, 15.0, wl, gc, &zero)
	if h15 <= 0 {
		t.Fatalf("setup: chief image height at 15 deg = %v, want > 0", h15)
	}
	if traceChiefImageHeight(surfs, 15.0, wl, gc, nil) != 0 {
		t.Fatal("setup: the axis launch must be clipped by the front stop")
	}

	target := h15 / 2
	o := &Optimizer{apertureMargin: 0.2}
	cfg := &config{id: "c", stopSurface: 1, pupilResolved: true, pupilZ: 0}

	angle := o.imageHeightToFieldAngle(cfg, surfs, target, wl, gc)
	if angle <= 0 {
		t.Fatalf("angle for image height %v = %v, want > 0 (field folded onto the axis?)", target, angle)
	}
	if got := traceChiefImageHeight(surfs, angle, wl, gc, &zero); math.Abs(got-target) > 1e-4 {
		t.Errorf("image height at the solved angle = %v, want %v", got, target)
	}

	// A negative image height is a valid field; the magnitude must match.
	neg := o.imageHeightToFieldAngle(cfg, surfs, -target, wl, gc)
	if neg >= 0 {
		t.Errorf("angle for a negative image height = %v, want < 0", neg)
	}
	if d := math.Abs(-neg - angle); d > 1e-6 {
		t.Errorf("|angle(-target)| - angle(target) = %v, want 0", d)
	}
}

// TestImageHeightToFieldAngleUnreachableKeepsFieldOffAxis: when the target is
// beyond the field's traceable range, the search must return the largest angle
// that still traces — not 0, which would silently fold the field onto the axis.
func TestImageHeightToFieldAngleUnreachableKeepsFieldOffAxis(t *testing.T) {
	gc, surfs := zeroPupilGC(), pupilLaunchSurfaces()
	const wl = 0.00058756
	zero := 0.0

	o := &Optimizer{apertureMargin: 0.2}
	cfg := &config{id: "c", stopSurface: 1, pupilResolved: true, pupilZ: 0}

	angle := o.imageHeightToFieldAngle(cfg, surfs, 1e6, wl, gc)
	if angle <= 0 {
		t.Fatalf("unreachable target returned angle %v, want the largest traceable angle > 0", angle)
	}
	// The returned angle must still trace.
	if traceChiefImageHeight(surfs, angle, wl, gc, &zero) == 0 {
		t.Errorf("returned best-effort angle %v does not trace", angle)
	}
}
