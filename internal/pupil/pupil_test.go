package pupil

import (
	"testing"

	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/types"
)

func TestTraceReleaseReturnsAndClearsSlab(t *testing.T) {
	spec := LaunchSpec{
		NumRays:        16,
		GridType:       types.GridPolar,
		ApertureRadius: 1,
		RayDir:         types.Vec3{Z: 1},
		ZStart:         -10,
		OPLMode:        OPLLaunch,
	}
	engine := ray.NewEngine(nil, nil)
	path := []int{0}
	first := Launch(spec)
	Trace(engine, path, nil, first, 0.00058756, types.NewCircularJones(true), 2)
	if len(first) == 0 || !first[0].OK || len(first[0].Surfaces) != 1 {
		t.Fatalf("first trace sample = %+v, want a valid object-plane result", first[0])
	}
	firstOPL := first[0].OPL
	if first[0].Slab == nil {
		t.Fatal("Trace did not attach its shared slab to the samples")
	}

	Release(first)
	for i := range first {
		if first[i].Slab != nil || first[i].Surfaces != nil {
			t.Errorf("Release left slab references on sample %d", i)
		}
	}
	// Scalar results survive release; only the shared detail storage is
	// invalidated. Callers can finish consuming aggregates without retaining
	// the much larger per-surface slab.
	if first[0].OPL != firstOPL || !first[0].OK {
		t.Errorf("Release changed scalar sample result: OPL/OK = %v/%v", first[0].OPL, first[0].OK)
	}

	second := Launch(spec)
	Trace(engine, path, nil, second, 0.00058756, types.NewCircularJones(true), 1)
	if len(second) == 0 || !second[0].OK || second[0].Surfaces[0].SurfaceID != 0 {
		t.Fatalf("trace after slab release = %+v, want valid object-plane result", second[0])
	}
	Release(second)
}
