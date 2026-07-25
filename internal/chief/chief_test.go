package chief

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

func singletSystem() (types.System, *glass.Catalog) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 10.0, Material: "N-BK7", Diameter: 50.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 100.0, Material: "AIR", Diameter: 50.0},
	}
	surface.Precompute(surfaces)
	return types.System{Surfaces: surfaces}, gc
}

func TestGenerateGridPointsPolar(t *testing.T) {
	pts := GenerateGridPoints(7, 10.0, types.GridPolar)
	if len(pts) == 0 {
		t.Error("GenerateGridPoints returned empty slice")
	}
}

func TestGenerateGridPointsSquare(t *testing.T) {
	// numRays=5 → n=2 → 2x2=4 points (all within unit circle)
	pts := GenerateGridPoints(5, 10.0, types.GridSquare)
	if len(pts) != 4 {
		t.Errorf("Square grid (n=5->n=2): got %d points, want 4", len(pts))
	}
	// numRays=20 → n=4 → 4x4=16 points with circle clipping
	pts2 := GenerateGridPoints(20, 10.0, types.GridSquare)
	if len(pts2) == 0 || len(pts2) > 16 {
		t.Errorf("Square grid (n=20->n=4): got %d, want <=16", len(pts2))
	}
}

func TestGenerateGridPointsHex(t *testing.T) {
	pts := GenerateGridPoints(7, 10.0, types.GridHex)
	if len(pts) == 0 {
		t.Error("Hex grid returned empty")
	}
}

func TestComputeSpotStats(t *testing.T) {
	pts := []types.GridPoint{
		{ImageX: float64ptr(1.0), ImageY: float64ptr(0.0), Intensity: 1.0},
		{ImageX: float64ptr(-1.0), ImageY: float64ptr(0.0), Intensity: 1.0},
		{ImageX: float64ptr(0.0), ImageY: float64ptr(1.0), Intensity: 1.0},
		{ImageX: float64ptr(0.0), ImageY: float64ptr(-1.0), Intensity: 1.0},
	}
	stats := computeSpotStats(pts, 0, 0)
	if stats.Centroid.X != 0.0 || stats.Centroid.Y != 0.0 {
		t.Errorf("Centroid = %v, want (0,0)", stats.Centroid)
	}
	if math.Abs(stats.RMS_R-1.0) > 1e-6 {
		t.Errorf("RMS_R = %v, want 1.0", stats.RMS_R)
	}
}

func float64ptr(v float64) *float64 {
	return &v
}

func TestDetermineChiefRaysAngle(t *testing.T) {
	sys, gc := singletSystem()
	pol := types.JonesVector{Ex: complex(1, 0), Ey: complex(0, 1)}
	fields := []types.FieldDef{
		{Angle: 0.0, Direction: []float64{0, 1}},
	}
	results := DetermineChiefRaysGrid(sys, fields, 2, 16, gc, pol, 0.00058756, false, types.GridPolar, nil)
	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}
	if results[0].FieldAngle != 0.0 {
		t.Errorf("FieldAngle = %v, want 0", results[0].FieldAngle)
	}
}

func TestDetermineChiefRaysImageHeight(t *testing.T) {
	sys, gc := singletSystem()
	pol := types.JonesVector{Ex: complex(1, 0), Ey: complex(0, 1)}
	fields := []types.FieldDef{
		{ImageHeight: 5.0, Direction: []float64{0, 1}},
	}
	results := DetermineChiefRaysGrid(sys, fields, 2, 16, gc, pol, 0.00058756, false, types.GridPolar, nil)
	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}
	imgY := results[0].ImageHeight.Y
	if imgY < 4.0 || imgY > 6.0 {
		t.Errorf("ImageHeight.Y = %v, want ~5.0", imgY)
	}
}

func TestFindMinApertureRadius(t *testing.T) {
	surfaces := []types.Surface{
		{Diameter: 50.0},
		{Diameter: 0.0},
		{Diameter: 20.0},
	}
	r := FindMinApertureRadius(surfaces)
	if r != 10.0 {
		t.Errorf("FindMinApertureRadius = %v, want 10", r)
	}
}
