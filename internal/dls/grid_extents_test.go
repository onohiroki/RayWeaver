package dls

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/pupil"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

func TestSurfaceExtentsKeepsPartialPaths(t *testing.T) {
	flat := types.Surface{ID: 1}
	flat.LocalToGlobal = types.NewIdentity()
	flat.GlobalToLocal = types.NewIdentity()

	decentered := types.Surface{ID: 2}
	decentered.LocalToGlobal = types.NewTranslation(types.Vec3{X: 10})
	decentered.GlobalToLocal = decentered.LocalToGlobal.Inverse()

	// The first ray reaches both surfaces. The second fails at surface 2 but
	// still reached surface 1 at (6, 8), so its prefix must survive. Surface 2
	// is translated by +10 in X, so its global hit (13, 4) is the local (3, 4)
	// with radius 5; a box measure would report 4.
	samples := []pupil.Sample{
		{OK: true, Surfaces: []types.SurfaceResult{
			{SurfaceID: 1, Position: types.Vec3{X: 3, Y: 4}},
			{SurfaceID: 2, Position: types.Vec3{X: 13, Y: 4}},
		}},
		{OK: false, Surfaces: []types.SurfaceResult{
			{SurfaceID: 1, Position: types.Vec3{X: 6, Y: 8}},
			{SurfaceID: 2, Interaction: types.Missed, ErrorCode: "missed_surface", Position: types.Vec3{X: 6, Y: 8}},
		}},
	}

	got := surfaceExtents(samples, []types.Surface{flat, decentered})

	if h := got[1]; math.Abs(h-10) > 1e-12 {
		t.Errorf("surface 1 extent = %v, want 10 (the failed ray's (6, 8) hit)", h)
	}
	if h := got[2]; math.Abs(h-5) > 1e-12 {
		t.Errorf("surface 2 extent = %v, want 5 (local radius of (3, 4) about the translated vertex)", h)
	}
}

func TestSurfaceExtentsIgnoresFailurePositions(t *testing.T) {
	s := types.Surface{ID: 2}
	s.LocalToGlobal = types.NewIdentity()
	s.GlobalToLocal = types.NewIdentity()

	// A failure entry carries the previous surface's position (or no hit at
	// all), so it must never be read as a beam on the surface it names.
	samples := []pupil.Sample{
		{Surfaces: []types.SurfaceResult{
			{SurfaceID: 2, Interaction: types.Missed, ErrorCode: "total_internal_reflection", Position: types.Vec3{X: 100}},
		}},
	}
	if got := surfaceExtents(samples, []types.Surface{s}); len(got) != 0 {
		t.Errorf("surfaceExtents = %v, want no entry for a failure-only surface", got)
	}
}

func TestSurfaceExtentsMeasuresVirtualHits(t *testing.T) {
	s := types.Surface{ID: 2}
	s.LocalToGlobal = types.NewIdentity()
	s.GlobalToLocal = types.NewIdentity()

	// A Virtual hit is a real point on the extended ray (unlike a Missed
	// entry), so it must contribute to the surface's envelope.
	samples := []pupil.Sample{
		{Surfaces: []types.SurfaceResult{
			{SurfaceID: 2, Interaction: types.Virtual, ErrorCode: "missed_surface", Position: types.Vec3{X: 3, Y: 4}},
		}},
	}
	if got := surfaceExtents(samples, []types.Surface{s}); math.Abs(got[2]-5) > 1e-12 {
		t.Errorf("virtual-hit extent = %v, want 5", got[2])
	}
}

func TestTraceFieldGridExtentsRelaxedExtendsPastMiss(t *testing.T) {
	gc := glass.NewCatalog()
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0, Thickness: 10.0, Material: types.Material{}, Diameter: 40.0, Role: "stop"},
		{ID: 2, Type: types.Sphere, Curvature: 0.2, Thickness: 10.0, Material: types.Material{}, Diameter: 40.0},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 40.0},
	}
	surface.Precompute(surfaces)

	// The on-axis bundle spans the 20 mm stop radius. Surface 2 is an R = 5 mm
	// sphere: most of the bundle misses it, so the physical trace measures only
	// the central rays (<= 5 mm). The relaxed trace extends every miss to the
	// vertex plane and keeps the full ~20 mm envelope.
	physical := TraceFieldGridExtents(gc, surfaces, 1, 0, 0, []float64{0, 1}, 0.00058756, 1.0, 64, 0, 1, 0)
	relaxed := TraceFieldGridExtentsRelaxed(gc, surfaces, 1, 0, 0, []float64{0, 1}, 0.00058756, 1.0, 64, 0, 1, 0, nil)

	if physical[2] > 6 {
		t.Fatalf("physical extent at surface 2 = %v, want <= 6 (only the hitting rays)", physical[2])
	}
	if relaxed[2] < 15 {
		t.Errorf("relaxed extent at surface 2 = %v, want the extended bundle (~20)", relaxed[2])
	}
	if relaxed[3] < 15 {
		t.Errorf("relaxed extent at surface 3 = %v, want the extended bundle (~20)", relaxed[3])
	}
}

func TestBeamClearance(t *testing.T) {
	surfaces := []types.Surface{{ID: 2, Diameter: 20}}

	if c, ok := BeamClearance(map[int]float64{2: 4}, surfaces, 2); !ok || math.Abs(c-6) > 1e-12 {
		t.Errorf("BeamClearance = (%v, %v), want (6, true)", c, ok)
	}
	if _, ok := BeamClearance(map[int]float64{1: 4}, surfaces, 2); ok {
		t.Error("BeamClearance reported an unreached surface as reached")
	}
	if _, ok := BeamClearance(map[int]float64{2: 4}, surfaces, 99); ok {
		t.Error("BeamClearance reported an unknown surface as reached")
	}
}
