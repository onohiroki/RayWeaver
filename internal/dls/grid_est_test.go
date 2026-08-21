package dls

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/pupil"
	"github.com/hiroki/rayweaver/internal/types"
)

func TestEstimateEntrancePupilFromFirstSurface(t *testing.T) {
	// US2645157 first glass R=10.287...
	surfaces := []types.Surface{
		{ID: 1, Curvature: 1.0 / 10.2871491742, Material: types.Material{Key: "SK18"}},
		{ID: 2, Curvature: 1.0 / -239.39, Material: types.Material{}},
	}
	d := estimateEntrancePupilDiameterFromFirstSurface(surfaces)
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
		{ID: 2, Curvature: 0, Material: types.Material{}},           // plane air -> skip
		{ID: 3, Curvature: 1.0 / 12.0, Material: types.Material{Key: "N-BK7"}},
	}
	d := estimateEntrancePupilDiameterFromFirstSurface(surfaces)
	want := 24.0
	if math.Abs(d-want) > 1e-9 {
		t.Errorf("estimate with plane skip = %v, want %v", d, want)
	}
}

func TestEstimateMirror(t *testing.T) {
	surfaces := []types.Surface{
		{ID: 1, Curvature: 1.0 / -800, Material: types.Material{}, Reflect: true},
	}
	d := estimateEntrancePupilDiameterFromFirstSurface(surfaces)
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
	d := estimateEntrancePupilDiameterFromFirstSurface(surfaces)
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
	d := estimateEntrancePupilDiameterFromFirstSurface(surfaces)
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
	r := ApertureRadiusForGrid(surfaces, 0, 0.00058756, gc, 1.0)
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
	r := ApertureRadiusForGrid(surfaces, 0, 0.00058756, gc, 1.0)
	// fixedApertureAtPupil will be <=2.5, not 17.0 estimate
	if math.Abs(r-17.0) < 1e-9 {
		t.Errorf("should prefer fixed aperture over estimate, got estimate %v", r)
	}
	if r <= 0 || r > 10 {
		t.Errorf("fixed aperture radius unexpected %v", r)
	}
}

func TestRecommendedThresholdsVariable(t *testing.T) {
	cases := []struct {
		fov  float64
		wantMin int
	}{
		{70, 80},
		{45, 180},
		{20, 320},
	}
	for _, c := range cases {
		gotMin, _ := RecommendedThresholds(c.fov)
		if gotMin != c.wantMin {
			t.Errorf("FOV %v: min %v want %v", c.fov, gotMin, c.wantMin)
		}
	}
}

func TestMaxFieldOfViewBothConjugates(t *testing.T) {
	angleFields := []types.FieldDef{{Angle: 10}, {Angle: -20}}
	if got := MaxFieldOfView(angleFields); math.Abs(got-40) > 1e-9 {
		t.Errorf("angle FOV = %v want 40", got)
	}
	finiteFields := []types.FieldDef{
		{Height: 10, ObjectZ: -100},
		{Height: -5, ObjectZ: -100},
	}
	got := MaxFieldOfView(finiteFields)
	// max angle = atan(10/100) ~5.71deg => full FOV ~11.42
	want := 2 * math.Atan2(10, 100) * 180 / math.Pi
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("finite FOV = %v want %v", got, want)
	}
	// Mixed
	mixed := []types.FieldDef{{Angle: 15}, {Height: 20, ObjectZ: -100}}
	// max is 15 deg
	if got := MaxFieldOfView(mixed); math.Abs(got-30) > 1e-9 {
		t.Errorf("mixed FOV = %v want 30", got)
	}
}

func TestReestimateFromSamples(t *testing.T) {
	initial := 10.0
	samples := []pupil.Sample{
		{PupilX: 3, PupilY: 4, OK: true},  // r=5
		{PupilX: 6, PupilY: 8, OK: true},  // r=10
		{PupilX: 12, PupilY: 0, OK: false}, // excluded (missed/glass_path/numerical)
		{PupilX: 9, PupilY: 0, OK: true},  // r=9
	}
	got := ReestimateApertureRadiusFromSamples(samples, initial)
	if math.Abs(got-10) > 1e-9 {
		t.Errorf("reestimate = %v want 10", got)
	}
	// Clamp to 3x
	samples2 := []pupil.Sample{
		{PupilX: 100, PupilY: 0, OK: true},
	}
	got2 := ReestimateApertureRadiusFromSamples(samples2, initial)
	if math.Abs(got2-30) > 1e-9 {
		t.Errorf("clamped reestimate = %v want 30", got2)
	}
	// No survivors
	if got := ReestimateApertureRadiusFromSamples([]pupil.Sample{{PupilX: 0, OK: false}}, 10); got != 0 {
		t.Errorf("no survivors should be 0, got %v", got)
	}
}
