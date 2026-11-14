package optimize

import (
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// brokenClearanceProbe is a design in which the on-axis bundle misses surface
// 2's physical sphere (R = 5 mm) over most of the pupil: the sequential merit
// grid has no rays there, so a spot-only merit would be blind. The
// beam_clearance term reads the relaxed envelope (virtual hits, with the
// outermost missed rays re-solved as a node system) and penalises the beam
// leaving surface 2's 10 mm clear aperture.
func brokenClearanceProbe(c1 float64) *Optimizer {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: c1, Thickness: 20.0, Material: types.Material{Key: "N-BK7"}, Diameter: 40.0, Role: "stop"},
		{ID: 2, Type: types.Sphere, Curvature: 0.2, Thickness: 40.0, Material: types.Material{}, Diameter: 10.0},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 40.0},
	}
	surface.Precompute(surfaces)
	return NewOptimizer(Config{
		Surfaces:     surfaces,
		GlassCatalog: gc,
		Variables: []Variable{
			{SurfaceID: 1, Param: "curvature", Min: 0.005, Max: 0.05},
		},
		Fields: []types.FieldItem{{ID: 0, AngleDeg: 0, Weight: 1}},
		MeritTerms: []MeritTerm{{
			Kind: MeritBeamClearance, FieldAngle: 0, FieldIndex: 0, FieldWeight: 1,
			Wavelength: 0.00058756, WavWeight: 1, Weight: 1,
			SurfaceSet: []int{2}, Target: 0,
		}},
		StopSurface: 1,
		RefSurface:  3,
		NumRays:     16,
		MaxIter:     20,
		Mu:          1e-3,
		Tol:         1e-9,
		Epsilon:     1e-9,
	})
}

// TestBeamClearanceMeritDrivesABrokenDesign is the end-to-end check for the
// feasibility-continuation gradient: the starting design's rays miss surface 2,
// yet the merit still has a slope (a stronger first surface narrows the beam),
// and DLS follows it. Before this work the same state produced a flat,
// degenerate merit and the optimizer could not move.
func TestBeamClearanceMeritDrivesABrokenDesign(t *testing.T) {
	broken := brokenClearanceProbe(0.01)
	before := broken.EvaluateMerit(broken.getInitialState())

	// The gradient exists where the rays miss: a stronger first surface bends
	// the bundle away from surface 2's aperture and lowers the merit.
	better := brokenClearanceProbe(0.045)
	improved := better.EvaluateMerit(better.getInitialState())
	if !(improved < before) {
		t.Fatalf("merit did not fall with a stronger first surface: c=0.01 -> %v, c=0.045 -> %v", before, improved)
	}

	result := broken.Optimize()
	if result.AfterMerit >= result.BeforeMerit {
		t.Errorf("DLS did not reduce the merit: before %v, after %v", result.BeforeMerit, result.AfterMerit)
	}
	if len(result.Variables) != 1 {
		t.Fatalf("variables = %d, want 1", len(result.Variables))
	}
	if result.Variables[0].After <= result.Variables[0].Before {
		t.Errorf("first-surface curvature did not increase: %v -> %v",
			result.Variables[0].Before, result.Variables[0].After)
	}
}
