package optimize

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// TestLateralColorNonZero verifies the lateral_color evaluator actually
// measures transverse colour for an angle field. It used to launch the chief ray
// on the optical axis at zStart, which for a non-zero field angle arrives at the
// lens at 100*tan(theta) — far outside every aperture — so the trace failed and
// the term silently evaluated to exactly 0, leaving the chromatic objective inert.
func TestLateralColorNonZero(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	surfs := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.02, Thickness: 5.0, Material: types.Material{Key: "N-BK7"}, Diameter: 30.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.02, Thickness: 40.0, Material: types.Material{}, Diameter: 30.0},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 30.0},
	}
	surface.Precompute(surfs)

	const fieldAngle = 15.0
	const wl1, wl2 = 0.0004861, 0.0006563

	// Without a pupil (the historical axis launch) the trace misses the lens.
	if got := evaluateLateralColor(fieldAngle, wl1, wl2, surfs, gc, 0); got != 0 {
		t.Errorf("lateral_color with no pupil = %v, want 0 (the axis launch misses the lens)", got)
	}

	// With the entrance-pupil Z the chief ray reaches the image plane and the
	// term reports a real (non-zero) transverse colour.
	pupilZ := 2.0
	got := evaluateLateralColor(fieldAngle, wl1, wl2, surfs, gc, pupilZ)
	if got == 0 {
		t.Fatal("lateral_color with the entrance pupil = 0: the term is still inert")
	}
	// Transverse colour of a singlet is a fraction of a millimetre at 15 deg;
	// anything wildly larger means the chief ray is landing somewhere wrong.
	if math.Abs(got) > 1.0 {
		t.Errorf("lateral_color = %v mm, implausibly large for a singlet at 15 deg", got)
	}
	// The chief ray itself must now reach the image plane.
	if h := traceChiefImageHeight(surfs, fieldAngle, wl1, gc, pupilZ); h == 0 {
		t.Error("chief image height = 0 with the entrance pupil: the ray is still failing")
	}

	// The merit path must pass the config's pupil Z through.
	cfg := &config{id: "c", pupilZ: pupilZ}
	term := &meritTerm{kind: MeritLateralColor, fieldAngle: fieldAngle, wavelength: wl1, comparisonWavelength: wl2}
	if v := evaluateKindValue(MeritLateralColor, term, surfs, gc, nil, cfg); v == 0 {
		t.Error("evaluateKindValue(lateral_color) = 0 with a config pupil Z")
	}
	// A nil config must not panic.
	_ = evaluateKindValue(MeritLateralColor, term, surfs, gc, nil, nil)
}
