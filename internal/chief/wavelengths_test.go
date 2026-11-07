package chief

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

func TestSummarizeWavelengthsWhiteSpotAndLateral(t *testing.T) {
	stats := []types.WavelengthStats{
		{Value: 0.0005, ChiefHeight: 0, SpotStats: types.SpotStats{
			Centroid: types.Vec3{X: 0, Y: 0}, RMS_X: 1, RMS_Y: 1, RMS_R: math.Sqrt2,
			TotalRays: 10, TracedRays: 10,
		}},
		{Value: 0.0006, ChiefHeight: 2, SpotStats: types.SpotStats{
			Centroid: types.Vec3{X: 2, Y: 0}, RMS_X: 1, RMS_Y: 1, RMS_R: math.Sqrt2,
			TotalRays: 10, TracedRays: 10,
		}},
	}
	samples := []types.WavelengthSample{
		{Value: 0.0005, Weight: 0.5},
		{Value: 0.0006, Weight: 0.5},
	}
	white, lateral := SummarizeWavelengths(stats, samples, 0.0005)
	if white == nil || lateral == nil {
		t.Fatal("summaries missing")
	}
	if math.Abs(white.Centroid.X-1) > 1e-12 {
		t.Errorf("white centroid X = %v, want 1", white.Centroid.X)
	}
	// vx = 0.5·(1+1) + 0.5·(1+1) = 2, vy = 0.5·1 + 0.5·1 = 1; the combined
	// radius is sqrt(vx + vy) = sqrt(3).
	if math.Abs(white.RMS_R-math.Sqrt(3)) > 1e-12 {
		t.Errorf("white RMS = %v, want sqrt(3)", white.RMS_R)
	}
	if math.Abs(lateral.MaxMM-2) > 1e-12 {
		t.Errorf("lateral max = %v, want 2", lateral.MaxMM)
	}
}

func TestComputeWavelengthStatsDisperses(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 1.0 / 50, Thickness: 8,
			Material: types.Material{ND: 1.5168, VD: 64.17}, Diameter: 20},
		{ID: 2, Type: types.Sphere, Curvature: -1.0 / 50, Thickness: 42,
			Material: types.Material{}, Diameter: 20},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 10,
			Material: types.Material{}, Diameter: 40},
	}
	surface.Precompute(surfaces)
	system := types.System{Surfaces: surfaces, StopSurface: 1}
	chiefRay := types.Ray{
		Wavelength: 0.00058756,
		Initial: types.RayState{
			Origin:    types.Vec3{X: 0, Y: -17.632698070846494, Z: -100},
			Direction: types.Vec3{X: 0, Y: 0.17364817766693033, Z: 0.9848077530122081},
		},
		Path: []int{0, 1, 2, 3},
	}
	engine := ray.NewEngine(gc, nil)
	stats := computeWavelengthStats(engine, system, chiefRay, 3,
		types.NewCircularJones(true), nil,
		[]float64{0.00048613, 0.00058756, 0.00065627})
	if len(stats) != 3 {
		t.Fatalf("got %d wavelength rows", len(stats))
	}
	if stats[0].ChiefHeight == stats[2].ChiefHeight {
		t.Fatalf("F and C chief heights are identical (%v); the per-wavelength trace ignored dispersion",
			stats[0].ChiefHeight)
	}
}

func TestDetermineChiefRaysWavelengthStatsDisperse(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 1.0 / 50, Thickness: 8,
			Material: types.Material{Key: "N-BK7"}, Diameter: 20},
		{ID: 2, Type: types.Sphere, Curvature: -1.0 / 50, Thickness: 42,
			Material: types.Material{}, Diameter: 20},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 10,
			Material: types.Material{}, Diameter: 40},
	}
	surface.Precompute(surfaces)
	system := types.System{Surfaces: surfaces, StopSurface: 1}
	fields := []types.FieldDef{{Angle: 10, Direction: []float64{0, 1}}}
	results := DetermineChiefRaysGridMode(system, fields, 3, 16, gc,
		types.NewCircularJones(true), 0.00058756, false, types.GridPolar,
		nil, nil, []float64{0.00048613, 0.00058756, 0.00065627},
		nil, 0, 0, "")
	if len(results) != 1 || len(results[0].Wavelengths) != 3 {
		t.Fatalf("unexpected results: %d fields", len(results))
	}
	f, d, c := results[0].Wavelengths[0], results[0].Wavelengths[1], results[0].Wavelengths[2]
	if f.ChiefHeight == c.ChiefHeight {
		t.Fatalf("F and C chief heights are identical (%v); lateral colour is missing", f.ChiefHeight)
	}
	_ = d
}

func TestSummarizeWavelengthsExcludesZeroWeight(t *testing.T) {
	stats := []types.WavelengthStats{
		{Value: 0.0005, SpotStats: types.SpotStats{Centroid: types.Vec3{}, RMS_X: 1, RMS_Y: 1}},
		{Value: 0.0006, SpotStats: types.SpotStats{Centroid: types.Vec3{X: 100}, RMS_X: 1, RMS_Y: 1}},
	}
	samples := []types.WavelengthSample{{Value: 0.0005, Weight: 1}}
	white, _ := SummarizeWavelengths(stats, samples, 0.0005)
	if white == nil {
		t.Fatal("white summary missing")
	}
	if math.Abs(white.Centroid.X) > 1e-12 {
		t.Errorf("excluded wavelength leaked into the centroid: %v", white.Centroid.X)
	}
}
