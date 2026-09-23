package dls

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

func TestEstimateEntrancePupilFromFirstSurface(t *testing.T) {
	// US2645157 first glass R=10.287...
	surfaces := []types.Surface{
		{ID: 1, Curvature: 1.0 / 10.2871491742, Material: types.Material{Key: "SK18"}},
		{ID: 2, Curvature: 1.0 / -239.39, Material: types.Material{}},
	}
	d := 2 * estimateEntrancePupilRadiusFromFirstSurface(surfaces)
	want := 2 * 10.2871491742
	if math.Abs(d-want) > 1e-9 {
		t.Errorf("estimate diameter = %v, want %v", d, want)
	}
	r := estimateEntrancePupilRadiusFromFirstSurface(surfaces)
	if math.Abs(r-want/2) > 1e-9 {
		t.Errorf("estimate radius = %v, want %v", r, want/2)
	}
}

func TestEstimateSkipsPlaneAndAir(t *testing.T) {
	surfaces := []types.Surface{
		{ID: 1, Curvature: 0, Material: types.Material{Key: "SK18"}}, // plane glass -> skip
		{ID: 2, Curvature: 0, Material: types.Material{}},            // plane air -> skip
		{ID: 3, Curvature: 1.0 / 12.0, Material: types.Material{Key: "N-BK7"}},
	}
	d := 2 * estimateEntrancePupilRadiusFromFirstSurface(surfaces)
	want := 24.0
	if math.Abs(d-want) > 1e-9 {
		t.Errorf("estimate with plane skip = %v, want %v", d, want)
	}
}

func TestEstimateMirror(t *testing.T) {
	surfaces := []types.Surface{
		{ID: 1, Curvature: 1.0 / -800, Material: types.Material{}, Reflect: true},
	}
	d := 2 * estimateEntrancePupilRadiusFromFirstSurface(surfaces)
	want := 1600.0
	if math.Abs(d-want) > 1e-9 {
		t.Errorf("mirror estimate = %v, want %v", d, want)
	}
}

func TestEstimateIgnoresAirSurface(t *testing.T) {
	surfaces := []types.Surface{
		{ID: 1, Curvature: 1.0 / 50.0, Material: types.Material{}}, // air -> skip
		{ID: 2, Curvature: 1.0 / 25.0, Material: types.Material{Key: "N-BK7"}},
	}
	d := 2 * estimateEntrancePupilRadiusFromFirstSurface(surfaces)
	want := 50.0
	if math.Abs(d-want) > 1e-9 {
		t.Errorf("estimate air skip = %v, want %v", d, want)
	}
}

func TestEstimateNoCandidateReturnsZero(t *testing.T) {
	surfaces := []types.Surface{
		{ID: 1, Curvature: 1.0 / 10.0, Material: types.Material{}},
		{ID: 2, Curvature: 0, Material: types.Material{Key: "N-BK7"}},
	}
	d := 2 * estimateEntrancePupilRadiusFromFirstSurface(surfaces)
	if d != 0 {
		t.Errorf("estimate with no candidate = %v, want 0", d)
	}
}

func TestApertureRadiusForGridUsesEstimateFallback(t *testing.T) {
	// No fixed aperture, no stop -> should use 0.85*|R|
	surfaces := []types.Surface{
		{ID: 1, Curvature: 1.0 / 20.0, Material: types.Material{Key: "N-BK7"}, Diameter: 0, AutoAperture: true},
		{ID: 2, Curvature: 1.0 / -30.0, Material: types.Material{}, Diameter: 0, AutoAperture: true},
		{ID: 3, Curvature: 0, Material: types.Material{}, Diameter: 0, AutoAperture: true},
	}
	// Need PhysicalZ for some helpers but not for estimate path; keep zero
	gc := &glass.Catalog{}
	r := ApertureRadiusForGrid(surfaces, 0, 0.00058756, gc, 1.0, 0)
	want := 20.0 * 0.85 // |R|*0.85
	if math.Abs(r-want) > 1e-9 {
		t.Errorf("ApertureRadiusForGrid estimate fallback = %v, want %v", r, want)
	}
	// Max 3x clamp not triggered with 0.85, but verify constant
	if r > 20.0*3.0+1e-9 {
		t.Errorf("radius exceeds 3x estimate")
	}
}

func TestApertureRadiusPrefersFixedOverEstimate(t *testing.T) {
	// Fixed aperture present -> should prefer fixedApertureAtPupil over estimate
	surfaces := []types.Surface{
		{ID: 1, Curvature: 1.0 / 20.0, Material: types.Material{Key: "N-BK7"}, Diameter: 10, AutoAperture: false, PhysicalZ: 0},
		{ID: 2, Curvature: 0, Material: types.Material{}, Diameter: 5, AutoAperture: false, PhysicalZ: 10},
	}
	gc := &glass.Catalog{}
	r := ApertureRadiusForGrid(surfaces, 0, 0.00058756, gc, 1.0, 0)
	// fixedApertureAtPupil will be <=2.5, not 17.0 estimate
	if math.Abs(r-17.0) < 1e-9 {
		t.Errorf("should prefer fixed aperture over estimate, got estimate %v", r)
	}
	if r <= 0 || r > 10 {
		t.Errorf("fixed aperture radius unexpected %v", r)
	}
}

// TestApertureRadiusConsistentWithParaxial verifies that
// ApertureRadiusForGrid(stopSurface=0, margin=1.0) produces the same
// entrance-pupil radius as paraxial.Compute for stop-free systems.
func TestApertureRadiusConsistentWithParaxial(t *testing.T) {
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 1.0 / 60.0, Thickness: 3.0, Material: types.Material{ND: 1.64, VD: 55.0}, Diameter: 36.0, AutoAperture: true},
		{ID: 2, Type: types.Sphere, Curvature: 0.0, Thickness: 10.0, Material: types.Material{}, Diameter: 15.8},
		{ID: 3, Type: types.Sphere, Curvature: -1.0 / 60.0, Thickness: 0.5, Material: types.Material{}, Diameter: 36.0, AutoAperture: true},
	}
	surface.Precompute(surfaces)
	gc := glass.NewCatalog()
	wl := 0.00058756

	// Debug: check paraxial result
	sys := types.System{Surfaces: surfaces}
	for i, s := range surfaces {
		t.Logf("surface[%d]: id=%d curv=%v paraxR=%v physZ=%v thick=%v", i, s.ID, s.Curvature, s.ParaxialRadius, s.PhysicalZ, s.Thickness)
	}
	pr := paraxial.Compute(sys, wl, gc, 0, nil)
	t.Logf("paraxial: fl=%v epd=%v na=%v", pr.FocalLength, pr.EntrancePupilDiameter, pr.ImageSpaceNA)

	rStopFree := paraxial.EntrancePupilRadiusStopFree(surfaces, wl, gc)
	t.Logf("EntrancePupilRadiusStopFree = %v", rStopFree)

	rGrid := ApertureRadiusForGrid(surfaces, 0, wl, gc, 1.0, 0)
	t.Logf("ApertureRadiusForGrid = %v", rGrid)

	if rGrid <= 0 {
		t.Fatalf("ApertureRadiusForGrid = %v, want > 0", rGrid)
	}
	if rStopFree <= 0 {
		t.Fatalf("EntrancePupilRadiusStopFree = %v, want > 0", rStopFree)
	}
	if math.Abs(rGrid-rStopFree) > 1e-9 {
		t.Errorf("ApertureRadiusForGrid=%v != EntrancePupilRadiusStopFree=%v", rGrid, rStopFree)
	}

	// Also verify paraxial.Compute returns a non-zero EPD for this system.
	if pr.EntrancePupilDiameter <= 0 {
		t.Errorf("paraxial.Compute EPD = %v, want > 0 for stop-free system", pr.EntrancePupilDiameter)
	}
	epFromGrid := 2 * rGrid
	if math.Abs(pr.EntrancePupilDiameter-epFromGrid) > 1e-9 {
		t.Errorf("paraxial.Compute EPD=%v != 2*ApertureRadiusForGrid=%v", pr.EntrancePupilDiameter, epFromGrid)
	}
}

func TestEntrancePupilRadiusMatchesCompute(t *testing.T) {
	// Stopped doublet: Surface 2 is the stop with a fixed aperture.
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 1.0 / 60.0, Thickness: 3.0, Material: types.Material{ND: 1.64, VD: 55.0}, Diameter: 36.0, AutoAperture: true},
		{ID: 2, Type: types.Sphere, Curvature: 0.0, Thickness: 10.0, Material: types.Material{}, Diameter: 15.8},
		{ID: 3, Type: types.Sphere, Curvature: -1.0 / 60.0, Thickness: 0.5, Material: types.Material{}, Diameter: 36.0, AutoAperture: true},
	}
	surface.Precompute(surfaces)
	gc := glass.NewCatalog()
	wl := 0.00058756

	// Test with explicit stop on Surface 2.
	sys := types.System{Surfaces: surfaces, StopSurface: 2}
	pr := paraxial.Compute(sys, wl, gc, 0, nil)
	if pr.EntrancePupilDiameter <= 0 {
		t.Fatalf("paraxial.Compute EPD = %v, want > 0 for stopped system", pr.EntrancePupilDiameter)
	}

	rEPD := paraxial.EntrancePupilRadius(surfaces, 2, wl, gc)
	t.Logf("EntrancePupilRadius (stop) = %v, Compute EPD/2 = %v", rEPD, pr.EntrancePupilDiameter/2)

	if math.Abs(rEPD-pr.EntrancePupilDiameter/2) > 1e-9 {
		t.Errorf("EntrancePupilRadius=%v != Compute EPD/2=%v", rEPD, pr.EntrancePupilDiameter/2)
	}

	// Test without stop (stop-free path).
	rStopFree := paraxial.EntrancePupilRadius(surfaces, 0, wl, gc)
	prStopFree := paraxial.Compute(types.System{Surfaces: surfaces}, wl, gc, 0, nil)
	if prStopFree.EntrancePupilDiameter > 0 {
		if math.Abs(rStopFree-prStopFree.EntrancePupilDiameter/2) > 1e-9 {
			t.Errorf("EntrancePupilRadius(stop-free)=%v != Compute EPD/2=%v", rStopFree, prStopFree.EntrancePupilDiameter/2)
		}
	}
}
