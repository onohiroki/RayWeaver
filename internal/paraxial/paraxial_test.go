package paraxial

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

func TestComputeSingletEFL(t *testing.T) {
	sys, gc := singletSystem()
	result := Compute(sys, 0.00058756, gc, 0, nil)
	// Singlet R=100, n=1.5168, t=10, thick lens EFL should be positive (converging)
	if result.FocalLength <= 0 {
		t.Errorf("FocalLength = %v, want positive (converging)", result.FocalLength)
	}
}

func TestComputeWithObjectHeight(t *testing.T) {
	sys, gc := singletSystem()
	result := Compute(sys, 0.00058756, gc, 10.0, nil)
	if result.Magnification == 0 {
		t.Error("Magnification should be non-zero for finite conjugate")
	}
}

func TestComputeWithChiefRays(t *testing.T) {
	sys, gc := singletSystem()
	chiefRays := []types.ChiefRayResult{
		{
			FieldAngle: 0.0,
			ChiefRay: types.Ray{
				Initial: types.RayState{
					Origin:    types.Vec3{Y: 0},
					Direction: types.Vec3{Z: 1},
				},
			},
		},
	}
	result := Compute(sys, 0.00058756, gc, 0, chiefRays)
	if result.FocalLength <= 0 {
		t.Errorf("FocalLength = %v, want positive", result.FocalLength)
	}
}

func TestComputeDynamicPupilDiameter(t *testing.T) {
	sys, gc := singletSystem()
	// No stop surface and no chief pupil: EPD must remain unset (no inference).
	if r := Compute(sys, 0.00058756, gc, 0, nil); r.EntrancePupilDiameter != 0 {
		t.Errorf("EntrancePupilDiameter without stop or chief pupil = %v, want 0", r.EntrancePupilDiameter)
	}
	// A chief dynamic-pupil entrance pupil (radius) provides the EPD even
	// though the system has no explicit stop.
	chiefRays := []types.ChiefRayResult{
		{EntrancePupil: &types.Pupil{Center: types.Vec3{Z: 12.5}, Radius: 10.0}},
	}
	r := Compute(sys, 0.00058756, gc, 0, chiefRays)
	if want := 20.0; math.Abs(r.EntrancePupilDiameter-want) > 1e-9 {
		t.Errorf("EntrancePupilDiameter = %v, want %v", r.EntrancePupilDiameter, want)
	}
	if math.Abs(r.EntrancePupilLocation-12.5) > 1e-9 {
		t.Errorf("EntrancePupilLocation = %v, want 12.5", r.EntrancePupilLocation)
	}
}

func TestStopSurfaceID(t *testing.T) {
	surfaces := []types.Surface{
		{ID: 1, Diameter: 50.0},
		{ID: 2, Diameter: 10.0},
		{ID: 3, Diameter: 20.0},
	}
	// Without an explicit stop there is no stop (never inferred).
	if idx := stopSurfaceIndex(surfaces, 0); idx != -1 {
		t.Errorf("Stop index without explicit stop = %d, want -1 (no implicit stop)", idx)
	}
	// An explicit stop is honoured.
	if idx := stopSurfaceIndex(surfaces, 2); idx != 1 {
		t.Errorf("Explicit stop index = %d, want 1", idx)
	}
}

func TestComputeTotalTrack(t *testing.T) {
	sys, gc := singletSystem()
	result := Compute(sys, 0.00058756, gc, 0, nil)
	want := 10.0 + 100.0
	if math.Abs(result.TotalTrack-want) > 1e-10 {
		t.Errorf("TotalTrack = %v, want %v", result.TotalTrack, want)
	}
}

func mirrorSystem() (types.System, *glass.Catalog) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0, Thickness: 1000.0, Material: "AIR", Diameter: 200.0},
		{ID: 2, Type: types.Sphere, Curvature: 1.0 / 1000.0, Thickness: 480.0, Material: "AIR", Diameter: 300.0, Reflect: true,
			Decenter: []types.DecenterStep{{Tilt: types.Vec3{X: 0, Y: 180, Z: 0}, Scope: types.ScopeBoth}}},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 20.0, Material: "AIR", Diameter: 60.0},
		{ID: 4, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: "AIR", Diameter: 50.0},
	}
	surface.Precompute(surfaces)
	return types.System{Surfaces: surfaces}, gc
}

func TestComputeMirrorEFL(t *testing.T) {
	sys, gc := mirrorSystem()
	result := Compute(sys, 0.00058756, gc, 0, nil)
	// Folded concave mirror R=+1000 (f=500): physical EFL = 500mm, converging.
	if math.Abs(result.FocalLength-500.0) > 1.0 {
		t.Errorf("FocalLength = %v, want ~500 (folded mirror)", result.FocalLength)
	}
	if result.FocalLength <= 0 {
		t.Errorf("FocalLength = %v, want positive (converging)", result.FocalLength)
	}
	// Physical track: corrector at 0, image at 500.
	if math.Abs(result.TotalTrack-500.0) > 1e-6 {
		t.Errorf("TotalTrack = %v, want ~500", result.TotalTrack)
	}
}

// TestComputeSchmidtFoldEFL locks the folded Schmidt flattener design: the
// spherical primary (R=+800, folded via tilt-180 decenter) plus corrector
// plate and field flattener must reproduce the design EFL ~385.5 mm, F/1.93,
// 200 mm entrance pupil and 400 mm physical track.
func TestComputeSchmidtFoldEFL(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	surfaces := []types.Surface{
		{ID: 1, Type: types.AspherePolynomial, Curvature: 0, Thickness: 5.0, Material: "N-BK7", Diameter: 200.0,
			Coefficients: []float64{-7.012596538707627e-10, -1.8934227166178542e-14}},
		{ID: 2, Type: types.Sphere, Curvature: 0, Thickness: 795.0, Material: "AIR", Diameter: 200.0},
		{ID: 3, Type: types.Sphere, Curvature: 1.0 / 800.0, Thickness: 340.0, Material: "AIR", Diameter: 300.0, Reflect: true,
			Decenter: []types.DecenterStep{{Tilt: types.Vec3{Y: 180}, Scope: types.ScopeBoth}}},
		{ID: 4, Type: types.Sphere, Curvature: 1.0 / 1097.9971323070158, Thickness: 3.00737900841865, Material: "N-BK7", Diameter: 72.0},
		{ID: 5, Type: types.Sphere, Curvature: 1.0 / 4527.917674010644, Thickness: 12.0, Material: "AIR", Diameter: 72.0},
		{ID: 6, Type: types.Sphere, Curvature: 1.0 / 1208.2550812192317, Thickness: 3.007046907695411, Material: "N-BK7", Diameter: 72.0},
		{ID: 7, Type: types.Sphere, Curvature: 1.0 / 4873.116517849829, Thickness: 42.0, Material: "AIR", Diameter: 72.0},
		{ID: 8, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: "AIR"},
	}
	surface.Precompute(surfaces)
	sys := types.System{Surfaces: surfaces, StopSurface: 1}
	result := Compute(sys, 0.00058756, gc, 0, nil)

	if math.Abs(result.FocalLength-385.5) > 1.5 {
		t.Errorf("FocalLength = %v, want ~385.5 (Schmidt design)", result.FocalLength)
	}
	if math.Abs(result.InfConjImageSpaceFNumber-1.93) > 0.02 {
		t.Errorf("F/# = %v, want ~1.93", result.InfConjImageSpaceFNumber)
	}
	if math.Abs(result.EntrancePupilDiameter-200.0) > 1.0 {
		t.Errorf("EntrancePupilDiameter = %v, want ~200", result.EntrancePupilDiameter)
	}
	if math.Abs(result.TotalTrack-400.0) > 1.0 {
		t.Errorf("TotalTrack = %v, want ~400 (physical track)", result.TotalTrack)
	}
}

// TestStopSurfaceExplicit: an explicit system.StopSurface selects the stop,
// and the stop Z is the physical (folded) Z. Without an explicit stop there is
// no stop (the smallest diameter is never inferred).
func TestStopSurfaceExplicit(t *testing.T) {
	surfaces := []types.Surface{
		{ID: 1, Diameter: 200.0},
		{ID: 2, Diameter: 10.0},
		{ID: 3, Diameter: 20.0},
	}
	surface.Precompute(surfaces)
	// Without an explicit stop, no stop is inferred.
	idx := stopSurfaceIndex(surfaces, 0)
	if idx != -1 {
		t.Errorf("stop index without explicit stop = %d, want -1 (no implicit stop)", idx)
	}
	// With an explicit stop_surface, surface 1 wins even though it is larger.
	idx = stopSurfaceIndex(surfaces, 1)
	if idx != 0 {
		t.Errorf("explicit stop index = %d, want 0 (surface 1)", idx)
	}
	z := computeStopZ(surfaces, 1)
	if z != 0 {
		t.Errorf("explicit stop Z = %v, want 0 (surface 1 physical Z)", z)
	}
}
