package chief

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/raymath"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

func singletSystem() (types.System, *glass.Catalog) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 10.0, Material: types.Material{Key: "N-BK7"}, Diameter: 50.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 100.0, Material: types.Material{}, Diameter: 50.0},
	}
	surface.Precompute(surfaces)
	return types.System{Surfaces: surfaces}, gc
}

// vignettedSingletSystem is a singlet whose rear surface has a small clear
// aperture, so fan rays scanned beyond the beam's physical extent are
// vignetted (aperture_stop) and must be dropped from the fan.
func vignettedSingletSystem() (types.System, *glass.Catalog) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 10.0, Material: types.Material{Key: "N-BK7"}, Diameter: 50.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 100.0, Material: types.Material{}, Diameter: 8.0},
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
			Thickness: 10.0, Material: types.Material{Key: "N-BK7"}, Diameter: 200.0},
		{ID: 2, Type: types.Sphere, Curvature: 0, Thickness: 90.0, Material: types.Material{}, Diameter: 100.0},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 100.0},
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
		engine, surfaces, computeStopZ(surfaces, 3), false,
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
		{ID: 1, Type: types.Sphere, Curvature: 0.02, Thickness: 10.0, Material: types.Material{Key: "N-BK7"}, Diameter: 30.0},
		{ID: 2, Type: types.Sphere, Curvature: 0, Thickness: 40.0, Material: types.Material{}, Diameter: 30.0},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 20.0},
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
		engine, surfaces, computeStopZ(surfaces, 3), false,
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
	traceResult := engine.TraceRay(r, surfaces, false)
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
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 10.0, Material: types.Material{Key: "N-BK7"}, Diameter: 50.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 50.0, Material: types.Material{}, Diameter: 50.0},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 20.0},
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
		{ID: 1, Type: types.Sphere, Curvature: 0, Thickness: 1000.0, Material: types.Material{}, Diameter: 200.0},
		{ID: 2, Type: types.Sphere, Curvature: 1.0 / 1000.0, Thickness: 480.0, Material: types.Material{}, Diameter: 300.0, Reflect: true,
			Decenter: []types.DecenterStep{{Tilt: types.Vec3{Y: 180}, Scope: types.ScopeBoth}}},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 20.0, Material: types.Material{}, Diameter: 60.0},
		{ID: 4, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 50.0},
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

	fan := computeRayFan(engine, sys, dls.BuildPath(sys.Surfaces), origin, dir, 2, pol, 0.00058756, 10.0, 0.0, []float64{0, 90, 45}, 8)
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
	fan := computeRayFan(engine, sys, dls.BuildPath(sys.Surfaces), origin, dir, 2, pol, 0.00058756, 10.0, 0.0, []float64{0, 90}, 8)
	if len(fan.Meridional) == 0 || len(fan.Sagittal) == 0 {
		t.Error("default config must produce both meridional and sagittal for all fields")
	}
}

func TestComputeRayFanDropsVignettedRays(t *testing.T) {
	sys, gc := vignettedSingletSystem()
	engine := ray.NewEngine(gc, nil)
	pol := types.JonesVector{Ex: complex(1, 0), Ey: complex(0, 1)}
	origin := types.Vec3{X: 0, Y: 0, Z: -100}
	dir := types.Vec3{X: 0, Y: 0, Z: 1}

	// The fan scans ±apertureRadius (10 mm) but the rear surface's clear
	// aperture is only 8 mm: outer samples are vignetted and must be dropped.
	fan := computeRayFan(engine, sys, dls.BuildPath(sys.Surfaces), origin, dir, 2, pol, 0.00058756, 10.0, -100.0, []float64{0, 90}, 16)
	if fan == nil {
		t.Fatal("computeRayFan returned nil")
	}
	if len(fan.Meridional) == 0 {
		t.Fatal("expected some meridional fan points to survive")
	}
	if len(fan.Meridional) >= 16 || len(fan.Sagittal) >= 16 {
		t.Errorf("vignetted fan rays were not dropped: meridional=%d, sagittal=%d (want < 16)",
			len(fan.Meridional), len(fan.Sagittal))
	}
	// Every surviving point must have traced cleanly through the system.
	for _, fp := range fan.Meridional {
		if len(fp.Path) < 2 {
			t.Errorf("meridional fan point has no traced path (surfaces=%d)", len(fp.Path))
		}
		for _, sr := range fp.Path {
			if sr.ErrorCode != "" {
				t.Errorf("vignetted meridional fan point kept with error %q at surface %d (py=%v)",
					sr.ErrorCode, sr.SurfaceID, fp.PupilY)
			}
		}
	}
	for _, fp := range fan.Sagittal {
		for _, sr := range fp.Path {
			if sr.ErrorCode != "" {
				t.Errorf("vignetted sagittal fan point kept with error %q at surface %d (px=%v)",
					sr.ErrorCode, sr.SurfaceID, fp.PupilX)
			}
		}
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

// TestBackwardChiefObjectPoint verifies the finite-conjugate (object-height)
// backward construction: the forward chief ray from the object point must pass
// through the stop centre.
func TestBackwardChiefObjectPoint(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.02, Thickness: 5.0, Material: types.Material{Key: "N-BK7"}, Diameter: 50.0},
		{ID: 2, Type: types.Sphere, Curvature: 0, Thickness: 20.0, Material: types.Material{}, Diameter: 50.0},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 10.0},
	}
	surface.Precompute(surfaces)
	e := ray.NewEngine(gc, nil)
	path := []int{0, 1, 2, 3}
	const wl = 0.00058756
	const objectZ = -100.0

	for _, h := range []float64{3.0, 5.0, -4.0} {
		op, dir, ok := backwardChiefObjectPoint(e, surfaces, path, 3, types.Vec3{}, h, 0, 1, objectZ, wl)
		if !ok {
			t.Fatalf("h=%v: backwardChiefObjectPoint failed", h)
		}
		if math.Abs(op.Y-h) > 1e-9 || op.Z != objectZ {
			t.Errorf("h=%v: object point = (%v,%v,%v), want Y=%v Z=%v", h, op.X, op.Y, op.Z, h, objectZ)
		}
		fwd := types.Ray{
			Wavelength: wl,
			Initial:    types.RayState{Origin: op, Direction: dir},
			Path:       path,
			Jones:      types.NewCircularJones(true),
		}
		res := e.TraceRay(fwd, surfaces, false)
		if res.Error != "" {
			t.Fatalf("h=%v: forward ray failed: %v", h, res.Error)
		}
		var stopHit *types.SurfaceResult
		for i := range res.Surfaces {
			if res.Surfaces[i].SurfaceID == 3 {
				stopHit = &res.Surfaces[i]
			}
		}
		if stopHit == nil {
			t.Fatalf("h=%v: no stop hit", h)
		}
		if dist := math.Hypot(stopHit.Position.X, stopHit.Position.Y); dist > 1e-3 {
			t.Errorf("h=%v: stop miss %v mm", h, dist)
		}
	}
}

// TestAngleGridOriginsOnWavefront verifies that an off-axis angle field's pupil
// grid rays launch from a common wavefront plane (perpendicular to the ray
// direction), so the OPL carries no launch-geometry tilt. Every grid origin
// must satisfy (origin - gridCentre)·rayDir == 0 within tolerance, and the
// chief ray (through the grid centre) must pass through the entrance-pupil
// centre.
func TestAngleGridOriginsOnWavefront(t *testing.T) {
	sys, gc := singletSystem()
	pol := types.JonesVector{Ex: complex(1, 0), Ey: complex(0, 1)}
	fields := []types.FieldDef{
		{Angle: 20.0, Direction: []float64{0, 1}},
	}
	results := DetermineChiefRaysGrid(sys, fields, 2, 64, gc, pol, 0.00058756, false, types.GridPolar, nil, nil, nil)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	grid := results[0].GridPoints
	if len(grid) == 0 {
		t.Fatal("no grid points")
	}

	// The grid centre is where the chief ray starts; recompute it via the same
	// geometry used at trace time: the entrance-pupil centre on the optical
	// axis and the field direction.
	rayDir := results[0].ChiefRay.Initial.Direction
	pupilZ := results[0].EntrancePupil.Center.Z
	zStart := -100.0
	gcpt := raymath.WavefrontGridCenter(types.Vec3{Z: pupilZ}, rayDir, zStart)

	// All origins must lie on the wavefront plane through the grid centre.
	// Origins that failed to trace (ImageX == nil) carry an ErrorCode and may
	// have a zeroed Origin, so only check rays that actually traced.
	traced := 0
	for _, gp := range grid {
		if gp.ImageX == nil {
			continue
		}
		traced++
		d := gp.Origin.Subtract(gcpt).Dot(rayDir)
		if math.Abs(d) > 1e-6 {
			t.Fatalf("origin %v not on wavefront through %v: (origin-c)·dir = %v",
				gp.Origin, gcpt, d)
		}
	}
	if traced == 0 {
		t.Fatal("no rays traced")
	}

	// The chief ray (from the grid centre) must reach the reference surface
	// (image plane, surface 2 in the singlet) — i.e. the grid is correctly
	// centred on the entrance pupil.
	if results[0].ImageHeight.Y == 0 {
		t.Error("chief ray image height is zero; grid not centred on pupil")
	}
}

// TestVignettingClipsGrid verifies that a field's vignetting ellipse drops
// pupil samples outside it, shrinking the traced grid and shifting the chief
// ray to the vignetted-pupil centroid.
func TestVignettingClipsGrid(t *testing.T) {
	sys, gc := singletSystem()
	pol := types.JonesVector{Ex: complex(1, 0), Ey: complex(0, 1)}

	full := []types.FieldDef{
		{Angle: 0.0, Direction: []float64{0, 1}},
	}
	fullRes := DetermineChiefRaysGrid(sys, full, 2, 64, gc, pol, 0.00058756, false, types.GridPolar, nil, nil, nil)

	// Clip the pupil to a small central disk: heavy Y compression.
	clip := &types.VignettingDef{CompressionX: 0.7, CompressionY: 0.7}
	vig := []types.FieldDef{
		{Angle: 0.0, Direction: []float64{0, 1}, Vignetting: clip},
	}
	vigRes := DetermineChiefRaysGrid(sys, vig, 2, 64, gc, pol, 0.00058756, false, types.GridPolar, nil, nil, nil)

	if len(fullRes) != 1 || len(vigRes) != 1 {
		t.Fatalf("expected 1 result each, got %d/%d", len(fullRes), len(vigRes))
	}
	nFull := countTraced(fullRes[0].GridPoints)
	nVig := countTraced(vigRes[0].GridPoints)
	if nVig >= nFull {
		t.Errorf("vignetted grid (%d traced) not smaller than full grid (%d)", nVig, nFull)
	}
	if nVig == 0 {
		t.Fatal("vignetted grid traced nothing")
	}

	// Every vignetted sample must lie inside the clip ellipse.
	ep := vigRes[0].EntrancePupil
	if ep == nil || ep.Radius <= 0 {
		t.Fatal("no entrance pupil for vignetted field")
	}
	for _, gp := range vigRes[0].GridPoints {
		if gp.ImageX == nil {
			continue
		}
		dx := gp.PupilX - ep.Center.X
		dy := gp.PupilY - ep.Center.Y
		if !clip.Contains(dx, dy, ep.Radius) {
			t.Errorf("vignetted sample (%g,%g) outside clip ellipse (R=%g)", dx, dy, ep.Radius)
		}
	}
}

func countTraced(pts []types.GridPoint) int {
	n := 0
	for _, gp := range pts {
		if gp.ImageX != nil {
			n++
		}
	}
	return n
}

// passThroughTripletSystem is a weak three-surface singlet (R≈333 on both
// lens surfaces) whose plane surface 3 is the reference surface and surface 2
// the stop. The weakness keeps the through-stop backward construction reachable
// up to ~20° field angle, so the pass-through tests exercise the backward path
// rather than its forward fallback.
func passThroughTripletSystem() (types.System, *glass.Catalog) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.003, Thickness: 10.0, Material: types.Material{Key: "N-BK7"}, Diameter: 100.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.003, Thickness: 80.0, Material: types.Material{}, Diameter: 100.0},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 40.0},
	}
	surface.Precompute(surfaces)
	return types.System{Surfaces: surfaces}, gc
}

// TestSearchBackwardTangentSigned verifies the signed handling of negative
// field angles in the through-stop backward search. The forward chief ray is
// the negated backward-emergent direction, so u carries the opposite sign of
// the emergent forward field angle: a positive target must be found with
// negative u and a negative target with positive u, and the resulting emergent
// forward angle must equal the (signed) target. Mirror-symmetric targets must
// give mirror-symmetric |u|.
func TestSearchBackwardTangentSigned(t *testing.T) {
	sys, gc := passThroughTripletSystem()
	e := ray.NewEngine(gc, nil)
	const wl = 0.00058756

	stopCenter, frontSeq, ok := backwardFrontSetup(sys.Surfaces, 2, types.Vec3{})
	if !ok {
		t.Fatal("backwardFrontSetup failed for the test system")
	}

	forwardAngle := func(u float64, isX bool) (float64, bool) {
		dir := types.Vec3{X: 0, Y: 0, Z: -1}
		if isX {
			dir.X = u
		} else {
			dir.Y = u
		}
		_, emDir, ok := e.TraceBackward(sys.Surfaces, frontSeq, stopCenter, dir, wl)
		if !ok {
			return 0, false
		}
		if isX {
			return raymath.RadToDeg(math.Atan2(-emDir.X, math.Abs(emDir.Z))), true
		}
		return raymath.RadToDeg(math.Atan2(-emDir.Y, math.Abs(emDir.Z))), true
	}

	cases := []struct {
		isX bool
		deg float64
	}{
		{false, 10}, {false, -10}, {false, 20}, {false, -20},
		{true, 10}, {true, -10},
		{false, 0}, {false, -1e-15},
	}
	for _, tc := range cases {
		u, ok := searchBackwardTangent(e, sys.Surfaces, frontSeq, stopCenter,
			raymath.DegToRad(tc.deg), tc.isX, wl)
		if !ok {
			t.Errorf("isX=%v deg=%v: searchBackwardTangent failed", tc.isX, tc.deg)
			continue
		}
		if math.Abs(tc.deg) < 1e-12 {
			if u != 0 {
				t.Errorf("isX=%v deg=%v: u = %v, want 0", tc.isX, tc.deg, u)
			}
			continue
		}
		if (u > 0) == (tc.deg > 0) {
			t.Errorf("isX=%v deg=%v: u = %v, want the sign opposite to the target", tc.isX, tc.deg, u)
		}
		got, ok := forwardAngle(u, tc.isX)
		if !ok {
			t.Errorf("isX=%v deg=%v: forward trace of u=%v failed", tc.isX, tc.deg, u)
			continue
		}
		if math.Abs(got-tc.deg) > 1e-6 {
			t.Errorf("isX=%v deg=%v: emergent forward angle = %v, want %v", tc.isX, tc.deg, got, tc.deg)
		}
	}

	uPos, _ := searchBackwardTangent(e, sys.Surfaces, frontSeq, stopCenter, raymath.DegToRad(10), false, wl)
	uNeg, _ := searchBackwardTangent(e, sys.Surfaces, frontSeq, stopCenter, raymath.DegToRad(-10), false, wl)
	if math.Abs(uPos+uNeg) > 1e-6 {
		t.Errorf("u(+10)=%v u(-10)=%v, want mirror (±same |u|)", uPos, uNeg)
	}
}

// TestDetermineChiefRaysGridNegativeAnglePassThrough verifies that negative
// field angles with pass_through are not collapsed to the on-axis ray: the
// +10° and -10° chief rays must be mirror images with the correct signed
// object-space direction and each must close through the stop centre.
func TestDetermineChiefRaysGridNegativeAnglePassThrough(t *testing.T) {
	sys, gc := passThroughTripletSystem()
	e := ray.NewEngine(gc, nil)
	pol := types.JonesVector{Ex: complex(1, 0), Ey: complex(0, 1)}
	pt := &types.PassThroughTarget{Surface: 2, Coordinate: types.Vec3{}}

	fields := []types.FieldDef{
		{Angle: 10, Direction: []float64{0, 1}},
		{Angle: -10, Direction: []float64{0, 1}},
	}
	results := DetermineChiefRaysGrid(sys, fields, 3, 64, gc, pol, 0.00058756,
		false, types.GridPolar, pt, nil, nil)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	want := []float64{math.Sin(raymath.DegToRad(10)), -math.Sin(raymath.DegToRad(10))}
	for i, cr := range []types.Ray{results[0].ChiefRay, results[1].ChiefRay} {
		if math.Abs(cr.Initial.Direction.Y-want[i]) > 1e-6 {
			t.Errorf("field_angle=%v: dirY = %v, want %v", results[i].FieldAngle, cr.Initial.Direction.Y, want[i])
		}
		if cr.Initial.Direction.Y == 0 {
			t.Errorf("field_angle=%v: chief ray collapsed to on-axis", results[i].FieldAngle)
		}
		tr := e.TraceRay(cr, sys.Surfaces, false)
		if tr.Error != "" {
			t.Errorf("field_angle=%v: chief ray failed: %v", results[i].FieldAngle, tr.Error)
			continue
		}
		var stopHit *types.SurfaceResult
		for j := range tr.Surfaces {
			if tr.Surfaces[j].SurfaceID == 2 {
				stopHit = &tr.Surfaces[j]
			}
		}
		if stopHit == nil {
			t.Errorf("field_angle=%v: no stop hit", results[i].FieldAngle)
			continue
		}
		if dist := math.Hypot(stopHit.Position.X, stopHit.Position.Y); dist > 1e-3 {
			t.Errorf("field_angle=%v: stop miss %v mm", results[i].FieldAngle, dist)
		}
	}

	// The two fields must land on mirror-symmetric sides of the reference plane.
	if results[0].SpotStats == nil || results[1].SpotStats == nil {
		t.Fatal("missing spot stats")
	}
	y0, y1 := results[0].SpotStats.Centroid.Y, results[1].SpotStats.Centroid.Y
	if y0*y1 >= 0 {
		t.Errorf("spot centroid Y = %v/%v, want opposite signs", y0, y1)
	}
	if math.Abs(y0+y1) > 1e-6 {
		t.Errorf("spot centroid Y = %v/%v, want mirror", y0, y1)
	}
}

// TestDetermineChiefRaysGridNegativeImageHeight verifies that a negative
// image_height is not collapsed to the on-axis ray: the recovered field angles
// must carry the sign of the requested height and the chief rays must be mirror
// images closing through the stop centre.
func TestDetermineChiefRaysGridNegativeImageHeight(t *testing.T) {
	sys, gc := passThroughTripletSystem()
	e := ray.NewEngine(gc, nil)
	pol := types.JonesVector{Ex: complex(1, 0), Ey: complex(0, 1)}
	pt := &types.PassThroughTarget{Surface: 2, Coordinate: types.Vec3{}}

	fields := []types.FieldDef{
		{ImageHeight: 5, Direction: []float64{0, 1}},
		{ImageHeight: -5, Direction: []float64{0, 1}},
	}
	results := DetermineChiefRaysGrid(sys, fields, 3, 64, gc, pol, 0.00058756,
		false, types.GridPolar, pt, nil, nil)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	a0, a1 := results[0].FieldAngle, results[1].FieldAngle
	if a0 <= 0 || a1 >= 0 {
		t.Errorf("field angles = %v/%v, want opposite signs for +5/-5 image heights", a0, a1)
	}
	if math.Abs(a0+a1) > 0.5 {
		t.Errorf("field angles = %v/%v, want mirror magnitudes", a0, a1)
	}
	if math.Abs(results[0].ChiefRay.Initial.Direction.Y+results[1].ChiefRay.Initial.Direction.Y) > 1e-6 {
		t.Errorf("chief ray dirY = %v/%v, want mirror",
			results[0].ChiefRay.Initial.Direction.Y, results[1].ChiefRay.Initial.Direction.Y)
	}
	for i, cr := range []types.Ray{results[0].ChiefRay, results[1].ChiefRay} {
		tr := e.TraceRay(cr, sys.Surfaces, false)
		if tr.Error != "" {
			t.Errorf("field %v: chief ray failed: %v", results[i].FieldAngle, tr.Error)
			continue
		}
		var ref *types.SurfaceResult
		for j := range tr.Surfaces {
			if tr.Surfaces[j].SurfaceID == 3 {
				ref = &tr.Surfaces[j]
			}
		}
		if ref == nil {
			t.Errorf("field %v: no reference-surface hit", results[i].FieldAngle)
			continue
		}
		wantY := []float64{5, -5}[i]
		if math.Abs(ref.Position.Y-wantY) > 0.05 {
			t.Errorf("field %v: chief ray Y at reference = %v, want %v", results[i].FieldAngle, ref.Position.Y, wantY)
		}
	}
}

// TestProbeAxisCrossing verifies the low-angle probe finds the aperture as the
// Z where its centroid chief ray crosses the optical axis, and that a lone
// field driven through the same geometry reports the same aperture on its
// entrance pupil (integration consistency with the run's own seed).
func TestProbeAxisCrossing(t *testing.T) {
	sys, gc := singletSystem()
	pol := types.NewCircularJones(true)
	const wl = 0.00058756
	engine := ray.NewEngine(gc, nil)
	apertureRadius := dls.ApertureRadiusForGrid(sys.Surfaces, 0, wl, gc, 1.0)
	if apertureRadius <= 0 {
		t.Fatal("no aperture radius for singlet")
	}
	// Use the same seed the dynamic pipeline uses so the probe result and the
	// integrated single-field run agree.
	seedZ := seedPupilZs(sys, []types.FieldDef{{Angle: 1.0, Direction: []float64{0, 1}}})[0]
	probeZ, ok := probePupilZ(sys, engine, 2, 64, apertureRadius, pol, wl, types.GridPolar, seedZ)
	if !ok {
		t.Fatal("probePupilZ failed for singlet")
	}
	zLo, zHi, _ := surfaceZRange(sys.Surfaces)
	if probeZ < zLo-1 || probeZ > zHi+1 {
		t.Fatalf("probeZ = %v, want within the lens window [%v,%v]", probeZ, zLo, zHi)
	}

	// Integration: a lone 1° field drives the grid through the same axis
	// crossing, so its entrance-pupil Z must match the probe's within tolerance.
	one := DetermineChiefRaysGrid(sys, []types.FieldDef{{Angle: 1.0, Direction: []float64{0, 1}}}, 2, 64, gc, pol, wl, false, types.GridPolar, nil, nil, nil)
	if len(one) != 1 || one[0].EntrancePupil == nil {
		t.Fatal("1° field produced no result / entrance pupil")
	}
	if math.Abs(one[0].EntrancePupil.Center.Z-probeZ) > 1.0 {
		t.Errorf("1° field entrance-pupil Z = %v, probe Z = %v (differ by %v)", one[0].EntrancePupil.Center.Z, probeZ, math.Abs(one[0].EntrancePupil.Center.Z-probeZ))
	}
}

// TestSingleFieldEntrancePupilFromProbe verifies that a stop-free single
// field now reports a non-zero entrance-pupil centre (supplied by the probe),
// which downstream grids previously lost (they were centred at the origin).
func TestSingleFieldEntrancePupilFromProbe(t *testing.T) {
	sys, gc := singletSystem()
	pol := types.NewCircularJones(true)
	results := DetermineChiefRaysGrid(sys, []types.FieldDef{{Angle: 0.0, Direction: []float64{0, 1}}}, 2, 64, gc, pol, 0.00058756, false, types.GridPolar, nil, nil, nil)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].ProbeOK {
		t.Fatal("expected the probe to run for a stop-free single field")
	}
	if results[0].EntrancePupil == nil {
		t.Fatal("no entrance pupil")
	}
	// The probe must place the on-axis field's entrance pupil at a finite,
	// in-lens Z (previously left as the zero value).
	if math.Abs(results[0].EntrancePupil.Center.Z) < 1e-6 {
		t.Errorf("single-field entrance-pupil centre Z = %v, want != 0 (probe-supplied)", results[0].EntrancePupil.Center.Z)
	}
}

// TestCrossingFallbackProbe verifies that an off-axis field whose chief-ray
// crossing is ill-conditioned falls back to the probe aperture Z rather than
// staying pinned to a stale seed.
func TestCrossingFallbackProbe(t *testing.T) {
	sys, gc := singletSystem()
	pol := types.NewCircularJones(true)
	// Two results with identical, parallel (non-crossing) chief rays, so the
	// in-lens crossing fails and the probe aperture must take over.
	results := make([]Result, 2)
	for i := range results {
		results[i] = Result{ChiefRay: types.Ray{
			Wavelength: 0.00058756,
			Initial:    types.RayState{Origin: types.Vec3{X: 0, Y: float64(i) * 100, Z: -100}, Direction: types.Vec3{Z: 1}},
			Path:       []int{0, 1, 2},
			Jones:      pol,
		}}
	}
	engine := ray.NewEngine(gc, nil)
	cur := []float64{99.0, 99.0} // stale seeds
	const probeZ = 12.0
	next := recomputeEntrancePupils(results, cur, engine, sys.Surfaces, probeZ, true)
	if math.Abs(next[1]-probeZ) > 1e-9 {
		t.Errorf("fallback: next[1] = %v, want probeZ %v", next[1], probeZ)
	}
	// Without a probe the stale seed is kept.
	nextNoProbe := recomputeEntrancePupils(results, cur, engine, sys.Surfaces, 0, false)
	if math.Abs(nextNoProbe[1]-cur[1]) > 1e-9 {
		t.Errorf("no-probe: next[1] = %v, want stale seed %v", nextNoProbe[1], cur[1])
	}
}

// TestPerFieldPupilIndependence verifies the probe is only a fallback: when an
// off-axis field has a clean in-lens crossing, the crossing value is kept (the
// probe is not used), so each field keeps its own entrance pupil.
func TestPerFieldPupilIndependence(t *testing.T) {
	sys, gc := singletSystem()
	pol := types.NewCircularJones(true)
	engine := ray.NewEngine(gc, nil)
	zLo, zHi, _ := surfaceZRange(sys.Surfaces)
	const probeZ = -0.917 // a deliberately different fallback Z

	// Two chief rays aimed to converge inside the lens give a clean crossing.
	mk := func(y0, zStart float64) types.Ray {
		dir := types.Vec3{X: 0, Y: -y0, Z: zStart}.Normalize()
		return types.Ray{Wavelength: 0.00058756, Initial: types.RayState{Origin: types.Vec3{Y: y0, Z: -100}, Direction: dir}, Path: []int{0, 1, 2}, Jones: pol, Lenient: true}
	}
	results := []Result{{ChiefRay: mk(2.0, 103.0)}, {ChiefRay: mk(-2.0, 103.0)}}
	// Sanity: the two chief-ray crossings are clean and distinct per field.
	p0 := fullChiefPath(engine, results[0].ChiefRay, sys.Surfaces)
	p1 := fullChiefPath(engine, results[1].ChiefRay, sys.Surfaces)
	cross, ok := inLensCrossingZ(p0, p1, zLo, zHi)
	if !ok {
		t.Fatal("test rays do not cross cleanly inside the lens")
	}

	next := recomputeEntrancePupils(results, []float64{probeZ, probeZ}, engine, sys.Surfaces, probeZ, true)
	// The crossing must win over the probe: next[1] is the (distinct) crossing.
	if math.Abs(next[1]-cross) > 1e-6 {
		t.Errorf("off-axis pupil = %v, want its crossing %v (probe %v must not override a clean crossing)", next[1], cross, probeZ)
	}
	if math.Abs(next[1]-probeZ) < 1e-6 {
		t.Errorf("off-axis pupil collapsed to the probe Z %v", probeZ)
	}
}

func TestProbeSkippedForFiniteConjugateAndPassThrough(t *testing.T) {
	sys, gc := singletSystem()
	pol := types.NewCircularJones(true)
	const wl = 0.00058756

	finite := DetermineChiefRaysGrid(sys, []types.FieldDef{{
		Height: 1.0, ObjectZ: -100.0, Direction: []float64{0, 1},
	}}, 2, 64, gc, pol, wl, false, types.GridPolar, nil, nil, nil)
	if len(finite) != 1 {
		t.Fatalf("finite-conjugate run returned %d results, want 1", len(finite))
	}
	if finite[0].ProbeOK {
		t.Error("finite-conjugate-only field unexpectedly ran the angle probe")
	}

	passThrough := &types.PassThroughTarget{Surface: 1, Coordinate: types.Vec3{}}
	constrained := DetermineChiefRaysGrid(sys, []types.FieldDef{{
		Angle: 0.0, Direction: []float64{0, 1},
	}}, 2, 64, gc, pol, wl, false, types.GridPolar, passThrough, nil, nil)
	if len(constrained) != 1 {
		t.Fatalf("pass-through run returned %d results, want 1", len(constrained))
	}
	if constrained[0].ProbeOK {
		t.Error("pass-through run unexpectedly ran the angle probe")
	}
}
