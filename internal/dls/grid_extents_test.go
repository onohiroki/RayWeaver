package dls

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/pupil"
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
