package optimize

import (
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

func rayValidityProbe(c1, diameter2 float64) *Optimizer {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: c1, Thickness: 20.0, Material: types.Material{Key: "N-BK7"}, Diameter: 40.0, Role: "stop"},
		{ID: 2, Type: types.Sphere, Curvature: 0.2, Thickness: 40.0, Material: types.Material{}, Diameter: diameter2},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 60.0},
	}
	surface.Precompute(surfaces)
	return NewOptimizer(Config{
		Surfaces:     surfaces,
		GlassCatalog: gc,
		Variables: []Variable{
			{SurfaceID: 1, Param: "curvature", Min: 0.005, Max: 0.05},
		},
		Fields:      []types.FieldItem{{ID: 0, AngleDeg: 0, Weight: 1}},
		StopSurface: 1,
		RefSurface:  3,
		NumRays:     16,
		Mu:          1e-3,
		Tol:         1e-9,
		Epsilon:     1e-9,
	})
}

// TestRayValidityMetric pins the Layer R state signal: 0 while a clear aperture
// clips the beam, 1 when every aperture has the full clearance scale, and a
// monotone rise as the first surface strengthens.
func TestRayValidityMetric(t *testing.T) {
	validity := func(c1, diameter2 float64) float64 {
		opt := rayValidityProbe(c1, diameter2)
		return opt.rayValidity(opt.getInitialState())
	}

	if v := validity(0.01, 60); v < 0.99 {
		t.Errorf("clear aperture validity = %v, want 1", v)
	}
	if v := validity(0.01, 10); v > 0.01 {
		t.Errorf("clipped aperture validity = %v, want 0", v)
	}
	weak := validity(0.02, 34)
	strong := validity(0.05, 34)
	if !(strong > weak) {
		t.Errorf("validity did not rise as the beam narrowed: c=0.02 -> %v, c=0.05 -> %v", weak, strong)
	}
	for _, c := range [][2]float64{{0.01, 10}, {0.01, 60}, {0.05, 34}} {
		if v := validity(c[0], c[1]); v < 0 || v > 1 {
			t.Errorf("validity(%v, %v) = %v, want [0,1]", c[0], c[1], v)
		}
	}
}

// TestRayValiditySchedule drives the paraxial-to-real mode blend: a clipped
// design stays in the analytic mode, a clear one selects the real mode.
func TestRayValiditySchedule(t *testing.T) {
	schedule := func(c1, diameter2 float64) (string, map[string]float64, float64) {
		opt := rayValidityProbe(c1, diameter2)
		opt.SetMeritSchedule(&types.MeritScheduleConfig{
			Metric:     "ray_validity",
			Curve:      "linear",
			AnchorFrom: 0.1,
			AnchorTo:   0.9,
			Modes: []types.MeritScheduleMode{
				{Name: "analytic", WeightFrom: 1, WeightTo: 0},
				{Name: "real", WeightFrom: 0, WeightTo: 1},
			},
		})
		active, weights, _, metric, _ := opt.MeritScheduleState()
		return active, weights, metric
	}

	active, weights, metric := schedule(0.01, 10)
	if active != "analytic" || weights["real"] > 1e-9 {
		t.Errorf("clipped design: active=%q weights=%v metric=%v, want analytic with real=0", active, weights, metric)
	}
	active, weights, metric = schedule(0.05, 34)
	if active != "real" || weights["analytic"] > 1e-9 {
		t.Errorf("clear design: active=%q weights=%v metric=%v, want real with analytic=0", active, weights, metric)
	}
}
