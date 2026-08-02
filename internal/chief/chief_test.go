package chief

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/dls"
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
	results := DetermineChiefRaysGrid(sys, fields, 2, 16, gc, pol, 0.00058756, false, types.GridPolar, nil, nil, nil)
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
	results := DetermineChiefRaysGrid(sys, fields, 2, 16, gc, pol, 0.00058756, false, types.GridPolar, nil, nil, nil)
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
	r := surface.MinApertureRadius(surfaces)
	if r != 10.0 {
		t.Errorf("MinApertureRadius = %v, want 10", r)
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

	path := dls.BuildPath(surfaces)
	result := searchOriginForTarget(
		rayDir.Y, rayDir, -100.0, 3, 0,
		path, 0.00058756,
		types.JonesVector{Ex: complex(1, 0), Ey: complex(0, 1)},
		engine, surfaces, 3, false,
		func(sr types.SurfaceResult) float64 { return sr.Position.Y },
	)

	// Should find a non-zero origin (the ray must be off-axis)
	geoEst := -(computeStopZ(surfaces, 3) - (-100.0)) * (rayDir.Y / math.Sqrt(1-rayDir.Y*rayDir.Y))
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
	stopZ := computeStopZ(surfaces, 3)
	tanComp := rayDir.Y / math.Sqrt(1-rayDir.Y*rayDir.Y)
	geoEst := -(stopZ - (-100.0)) * tanComp

	// At 8° with stopZ=50, geoEst = -150*0.1405 = -21.08
	// At surface 1 (z=0): Y = -21.08 + 100*0.1405 = -7.03mm
	// Semi of surface 1 = 15mm → OK.

	// But we need the search to find an origin that gives Y=0 at the stop (surface 3).
	// Test the function and verify the result is reasonable.
	result := searchOriginForTarget(rayDir.Y, rayDir, -100.0, 3, 0,
		dls.BuildPath(surfaces), 0.00058756,
		types.JonesVector{Ex: complex(1, 0), Ey: complex(0, 1)},
		engine, surfaces, 3, false,
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
		Path:       dls.BuildPath(surfaces),
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
		false, types.GridPolar, pt, nil, nil)
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

// TestFieldExplicitPath verifies that a field's `path` is honored: a valid
// fold path is prepended with the implicit object plane and traces to the
// image, while a path that stops at the mirror (no reflection back to the
// image) leaves the grid points un-traced.
func TestFieldExplicitPath(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0, Thickness: 1000.0, Material: "AIR", Diameter: 200.0},
		{ID: 2, Type: types.Sphere, Curvature: 1.0 / 1000.0, Thickness: 480.0, Material: "AIR", Diameter: 300.0,
			Decenter: []types.DecenterStep{{Tilt: types.Vec3{Y: 180}, Reflect: true}}},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 20.0, Material: "AIR", Diameter: 60.0},
		{ID: 4, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: "AIR", Diameter: 50.0},
	}
	surface.Precompute(surfaces)
	sys := types.System{Surfaces: surfaces}
	pol := types.JonesVector{Ex: complex(1, 0), Ey: complex(0, 1)}

	// Valid explicit path (no leading object-plane 0; the code prepends it).
	fields := []types.FieldDef{
		{Angle: 0, Direction: []float64{0, 1}, Path: []int{1, 2, 3, 4}},
	}
	results := DetermineChiefRaysGrid(sys, fields, 4, 16, gc, pol, 0.00058756, false, types.GridPolar, nil, nil, nil)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	gotPath := results[0].ChiefRay.Path
	wantPath := []int{0, 1, 2, 3, 4}
	if len(gotPath) != len(wantPath) {
		t.Fatalf("chief ray path = %v, want %v", gotPath, wantPath)
	}
	for i := range wantPath {
		if gotPath[i] != wantPath[i] {
			t.Fatalf("chief ray path = %v, want %v", gotPath, wantPath)
		}
	}
	// All on-axis grid points must reach the image plane.
	for _, gp := range results[0].GridPoints {
		if gp.ImageX == nil || gp.ImageY == nil {
			t.Errorf("on-axis grid point not traced: pupil=(%.1f,%.1f)", gp.PupilX, gp.PupilY)
		}
	}

	// A path that stops at the mirror cannot return to the image: the grid
	// points must fail to trace.
	badFields := []types.FieldDef{
		{Angle: 0, Direction: []float64{0, 1}, Path: []int{0, 1, 2}},
	}
	bad := DetermineChiefRaysGrid(sys, badFields, 4, 16, gc, pol, 0.00058756, false, types.GridPolar, nil, nil, nil)
	if len(bad) != 1 {
		t.Fatalf("expected 1 bad result, got %d", len(bad))
	}
	traced := 0
	for _, gp := range bad[0].GridPoints {
		if gp.ImageX != nil && gp.ImageY != nil {
			traced++
		}
	}
	if traced != 0 {
		t.Errorf("bad path traced %d/%d grid points, want 0 (image unreachable)", traced, len(bad[0].GridPoints))
	}
}

func TestComputeRayFanAngleMapping(t *testing.T) {
	sys, gc := singletSystem()
	engine := ray.NewEngine(gc, nil)
	pol := types.JonesVector{Ex: complex(1, 0), Ey: complex(0, 1)}
	origin := types.Vec3{X: 0, Y: 0, Z: -100}
	dir := types.Vec3{X: 0, Y: 0, Z: 1}

	fan := computeRayFan(engine, sys, dls.BuildPath(sys.Surfaces), origin, dir, 2, pol, 0.00058756, 10.0, []float64{0, 90, 45}, 8)
	if fan == nil {
		t.Fatal("computeRayFan returned nil")
	}
	if len(fan.Meridional) == 0 {
		t.Error("expected meridional (90deg) fan points")
	}
	if len(fan.Sagittal) == 0 {
		t.Error("expected sagittal (0deg) fan points")
	}
	if len(fan.Rotated) != 1 {
		t.Fatalf("expected 1 rotated fan, got %d", len(fan.Rotated))
	}
	if math.Abs(fan.Rotated[0].AngleDeg-45) > 1e-9 {
		t.Errorf("rotated angle = %v, want 45", fan.Rotated[0].AngleDeg)
	}
	if len(fan.Rotated[0].Points) == 0 {
		t.Error("expected rotated fan points")
	}
	// Each point must carry a full traced path for lens rendering.
	for _, fp := range fan.Meridional {
		if len(fp.Path) < 2 {
			t.Errorf("meridional fan point has no traced path (surfaces=%d)", len(fp.Path))
		}
	}
}

func TestComputeRayFanDefaultBothPlanes(t *testing.T) {
	sys, gc := singletSystem()
	engine := ray.NewEngine(gc, nil)
	pol := types.JonesVector{Ex: complex(1, 0), Ey: complex(0, 1)}
	origin := types.Vec3{X: 0, Y: 0, Z: -100}
	dir := types.Vec3{X: 0, Y: 0, Z: 1}

	// On-axis field still produces both planes under the default config.
	fan := computeRayFan(engine, sys, dls.BuildPath(sys.Surfaces), origin, dir, 2, pol, 0.00058756, 10.0, []float64{0, 90}, 8)
	if len(fan.Meridional) == 0 || len(fan.Sagittal) == 0 {
		t.Error("default config must produce both meridional and sagittal for all fields")
	}
}

func TestLongitudinalAberration(t *testing.T) {
	cases := []struct {
		name string
		sr   types.SurfaceResult
		cosA float64
		sinA float64
		want float64
	}{
		{
			name: "meridional Y crossing",
			sr:   types.SurfaceResult{Position: types.Vec3{X: 0, Y: 1, Z: 100}, Direction: types.Vec3{X: 0, Y: -0.5, Z: 0.866}},
			cosA: 0, sinA: 1,
			want: 100 + (1/0.5)*0.866 - 100, // t = -1/-0.5 = 2; z = 100+2*0.866
		},
		{
			name: "sagittal X crossing",
			sr:   types.SurfaceResult{Position: types.Vec3{X: -2, Y: 0, Z: 50}, Direction: types.Vec3{X: 0.3, Y: 0, Z: 0.954}},
			cosA: 1, sinA: 0,
			want: 50 + (2/0.3)*0.954 - 50, // t = 2/0.3; z = 50 + t*0.954
		},
		{
			name: "degenerate parallel direction",
			sr:   types.SurfaceResult{Position: types.Vec3{X: 0, Y: 1, Z: 100}, Direction: types.Vec3{X: 0, Y: 0, Z: 1}},
			cosA: 0, sinA: 1,
			want: 0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := longitudinalAberration(c.sr, c.cosA, c.sinA)
			if math.Abs(got-c.want) > 1e-9 {
				t.Errorf("longitudinalAberration = %v, want %v", got, c.want)
			}
		})
	}
}
