package optimize

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// beamClearanceMeritProbe builds a singlet whose beam reaches ~15.6 mm at
// surface 2, so the clear aperture there is the only thing the term measures.
func beamClearanceMeritProbe(diameter, target float64) *Optimizer {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	surfs := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.02, Thickness: 5.0, Material: types.Material{Key: "N-BK7"}, Diameter: 40.0, Role: "stop"},
		{ID: 2, Type: types.Sphere, Curvature: -0.02, Thickness: 40.0, Material: types.Material{}, Diameter: diameter},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 40.0},
	}
	surface.Precompute(surfs)
	return NewOptimizer(Config{
		Surfaces: surfs,
		Fields:   []types.FieldItem{{ID: 0, AngleDeg: 0, Weight: 1}},
		MeritTerms: []MeritTerm{{
			Kind: MeritBeamClearance, FieldAngle: 0, FieldIndex: 0, FieldWeight: 1,
			Wavelength: 0.00058756, WavWeight: 1, Weight: 1,
			SurfaceSet: []int{2}, Target: target,
		}},
		GlassCatalog: gc,
		StopSurface:  1,
		RefSurface:   3,
		NumRays:      16,
	})
}

func TestBeamClearanceMerit(t *testing.T) {
	merit := func(diameter, target float64) float64 {
		opt := beamClearanceMeritProbe(diameter, target)
		return opt.EvaluateMerit(opt.getInitialState())
	}

	// A 40 mm aperture is well clear of the ~31 mm beam: the clamped value
	// equals target 0, so the term contributes nothing.
	if m := merit(40, 0); m != 0 {
		t.Errorf("clear-aperture merit = %v, want 0", m)
	}

	// A 25 mm aperture clips: the term is a positive hinge.
	clipped := merit(25, 0)
	if clipped <= 0 || math.IsInf(clipped, 1) || math.IsNaN(clipped) {
		t.Fatalf("clipped merit = %v, want a finite positive value", clipped)
	}

	// A required clearance penalises an aperture that is merely close, not
	// clipping.
	if m := merit(40, 100); m <= 0 {
		t.Errorf("required-clearance merit = %v, want > 0", m)
	}

	// Widening the aperture must reduce the penalty.
	if wider := merit(30, 0); wider >= clipped {
		t.Errorf("merit did not fall when the aperture grew: 25 mm=%v, 30 mm=%v", clipped, wider)
	}
}
