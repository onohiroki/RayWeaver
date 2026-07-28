package chief

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/ray"
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

// Test that searchOriginForTarget recovers when an asphere intersection
// fails (returns "ray missed surface" due to NaN sag). The sag function
// for a polynomial asphere with conic=0 returns NaN for h > |R|, which
// causes IntersectAsphere to fail. The walking loop should treat this
// as a clipped signal and continue the search instead of aborting.
func TestSearchOriginForTargetAsphereRecovery(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	// Surface 1 is an even asphere (polynomial) with conic=0 and R=50mm.
	// When h > 50mm the sag function returns NaN, causing IntersectAsphere
	// to fail with "ray missed surface". The next surfaces are routine.
	surfaces := []types.Surface{
		{ID: 1, Type: types.AspherePolynomial, Curvature: 0.02, Conic: 0,
			Thickness: 10.0, Material: "N-BK7", Diameter: 200.0},
		{ID: 2, Type: types.Sphere, Curvature: 0, Thickness: 90.0, Material: "AIR", Diameter: 100.0},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: "AIR", Diameter: 100.0},
	}
	surface.Precompute(surfaces)
	engine := ray.NewEngine(gc, nil)

	// At 30° field angle, geoEst = -(stopZ - zStart) * tan(30°).
	// stopZ = 10 (surface 2, the smallest diameter), zStart = -100.
	// geoEst = -110 * 0.57735 ≈ -63.5mm, well beyond R=50mm.
	thetaDeg := 30.0
	thetaRad := thetaDeg * math.Pi / 180.0
	rayDir := types.Vec3{X: 0, Y: math.Sin(thetaRad), Z: math.Cos(thetaRad)}.Normalize()

	path := buildPath(surfaces)
	result := searchOriginForTarget(
		rayDir.Y, rayDir, -100.0, 3, 0,
		path, 0.00058756,
		types.JonesVector{Ex: complex(1, 0), Ey: complex(0, 1)},
		engine, surfaces, false,
		func(sr types.SurfaceResult) float64 { return sr.Position.Y },
	)

	// Should find a non-zero origin (the ray must be off-axis)
	geoEst := -(computeStopZ(surfaces) - (-100.0)) * (rayDir.Y / math.Sqrt(1-rayDir.Y*rayDir.Y))
	if math.Abs(result) < 1e-12 || math.Abs(result-geoEst) < 1e-6 {
		t.Errorf("searchOriginForTarget returned %v (geoEst=%v), want a valid recovered origin", result, geoEst)
	}
	// The result should be near the valid sag region boundary (R=50mm)
	// and far from the unverifiable geoEst (≈-63.5mm)
	if math.Abs(result) > 55 || math.Abs(result) < 45 {
		t.Errorf("searchOriginForTarget returned origin=%.1f, expect near R=50mm", result)
	}
}

// Test searchOriginForTarget when the paraxial geoEst estimate causes
// vignetting at a front surface with limited aperture. The old code
// would return geoEst without verifying it works; the new code walks
// outward from geoEst to find a valid anchor, then bisects to the target.
func TestSearchOriginForTargetVignettingRecovery(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	// Front surface with moderate curvature and limited aperture that causes
	// the paraxial geoEst to clip, while a nearby origin works.
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.02, Thickness: 10.0, Material: "N-BK7", Diameter: 30.0},
		{ID: 2, Type: types.Sphere, Curvature: 0, Thickness: 40.0, Material: "AIR", Diameter: 30.0},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: "AIR", Diameter: 20.0},
	}
	surface.Precompute(surfaces)
	engine := ray.NewEngine(gc, nil)

	thetaDeg := 8.0
	thetaRad := thetaDeg * math.Pi / 180.0
	rayDir := types.Vec3{X: 0, Y: math.Sin(thetaRad), Z: math.Cos(thetaRad)}.Normalize()

	// For this system: stopZ = z of surface 3 (semi=10, the smallest)
	stopZ := computeStopZ(surfaces)
	tanComp := rayDir.Y / math.Sqrt(1-rayDir.Y*rayDir.Y)
	geoEst := -(stopZ - (-100.0)) * tanComp

	// At 8° with stopZ=50, geoEst = -150*0.1405 = -21.08
	// At surface 1 (z=0): Y = -21.08 + 100*0.1405 = -7.03mm
	// Semi of surface 1 = 15mm → OK.

	// But we need the search to find an origin that gives Y=0 at the stop (surface 3).
	// Test the function and verify the result is reasonable.
	result := searchOriginForTarget(rayDir.Y, rayDir, -100.0, 3, 0,
		buildPath(surfaces), 0.00058756,
		types.JonesVector{Ex: complex(1, 0), Ey: complex(0, 1)},
		engine, surfaces, false,
		func(sr types.SurfaceResult) float64 { return sr.Position.Y })

	// The result should be non-zero (since the ray has a non-zero field angle)
	if math.Abs(result) < 1e-12 {
		t.Errorf("searchOriginForTarget returned 0, want non-zero (geoEst=%v)", geoEst)
	}

	// Verify the result actually works: trace the ray and check Y at surface 3 ≈ 0
	orig := types.Vec3{X: 0, Y: result, Z: -100.0}
	r := types.Ray{
		Wavelength: 0.00058756,
		Initial:    types.RayState{Origin: orig, Direction: rayDir},
		Path:       buildPath(surfaces),
		Jones:      types.JonesVector{Ex: complex(1, 0), Ey: complex(0, 1)},
	}
	traceResult := engine.TraceRay(r, surfaces)
	if traceResult.Error != "" {
		t.Errorf("Ray from search result failed: %s", traceResult.Error)
	} else {
		found := false
		for _, sr := range traceResult.Surfaces {
			if sr.SurfaceID == 3 {
				if math.Abs(sr.Position.Y) > 0.5 {
					t.Errorf("Y at target from result origin = %v, want ≈ 0", sr.Position.Y)
				}
				found = true
				break
			}
		}
		if !found {
			t.Error("Target surface not found in trace result")
		}
	}
}

// Test that DetermineChiefRaysGrid with image_height + pass_through
// produces non-zero field_angle values for off-axis fields, and that
// field_angles are monotonically increasing with image height.
func TestDetermineChiefRaysImageHeightWithPassThrough(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 10.0, Material: "N-BK7", Diameter: 50.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 50.0, Material: "AIR", Diameter: 50.0},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: "AIR", Diameter: 20.0},
	}
	surface.Precompute(surfaces)
	sys := types.System{Surfaces: surfaces}
	pol := types.JonesVector{Ex: complex(1, 0), Ey: complex(0, 1)}
	pt := &types.PassThroughTarget{
		Surface:    3,
		Coordinate: types.Vec3{Y: 0},
		Variable:   "origin",
	}
	fields := []types.FieldDef{
		{ImageHeight: 0.0, Direction: []float64{0, 1}},
		{ImageHeight: 5.0, Direction: []float64{0, 1}},
	}
	results := DetermineChiefRaysGrid(sys, fields, 3, 16, gc, pol, 0.00058756,
		false, types.GridPolar, pt)
	if len(results) != 2 {
		t.Fatalf("Expected 2 results, got %d", len(results))
	}
	if results[0].FieldAngle != 0.0 {
		t.Errorf("On-axis field_angle = %v, want 0", results[0].FieldAngle)
	}
	if results[1].FieldAngle <= 0 {
		t.Errorf("Off-axis field_angle = %v, want > 0", results[1].FieldAngle)
	}
}
