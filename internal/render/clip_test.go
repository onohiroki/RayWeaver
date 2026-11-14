package render

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
)

func TestClipPolylineZ(t *testing.T) {
	box := func(pts []vec2) []vec2 {
		return clipPolylineZ(pts, 0, 10)
	}

	// Fully inside: unchanged.
	in := []vec2{{X: 1, Y: 1}, {X: 5, Y: 2}, {X: 9, Y: 3}}
	if got := box(in); len(got) != 3 || got[0] != in[0] || got[2] != in[2] {
		t.Errorf("inside polyline = %v, want %v", got, in)
	}

	// Crossing both edges: the endpoints land on the bounds.
	got := box([]vec2{{X: -10, Y: 0}, {X: 20, Y: 30}})
	if len(got) != 2 || math.Abs(got[0].X) > 1e-9 || math.Abs(got[1].X-10) > 1e-9 {
		t.Fatalf("crossing polyline = %v, want x=0 and x=10", got)
	}
	if math.Abs(got[0].Y-10) > 1e-9 || math.Abs(got[1].Y-20) > 1e-9 {
		t.Errorf("crossing y = %v, want the interpolated 10 and 20", got)
	}

	// Fully outside: dropped.
	if got := box([]vec2{{X: -20, Y: 0}, {X: -10, Y: 5}}); got != nil {
		t.Errorf("outside polyline = %v, want nil", got)
	}
}

// TestMultiPanelClipsRaysToTheDrawingBox pins the guarantee behind "panels
// never overlap": every point the renderer draws stays inside the panel's
// horizontal box, and the band height covers it.
func TestMultiPanelClipsRaysToTheDrawingBox(t *testing.T) {
	path := []types.SurfaceResult{
		{Position: types.Vec3{Y: 50, Z: -1000}}, // far object-side tail
		{Position: types.Vec3{Y: 20, Z: 0}},
		{Position: types.Vec3{Y: 15, Z: 8}},
		{Position: types.Vec3{Y: 10, Z: 28}},
		{Position: types.Vec3{Y: 5, Z: 32}},
	}
	cfg := Config{
		Surfaces: panelSurfaces(20, 10),
		Results:  []types.RayResult{{ID: "marginal_f0_0", Surfaces: path}},
	}
	panels := []Panel{
		{Config: cfg, ImagePlaneZ: planeZ(20)},
		{Config: Config{Surfaces: panelSurfaces(60, 12), Results: []types.RayResult{{ID: "marginal_f0_0", Surfaces: path}}}, ImagePlaneZ: planeZ(60)},
	}

	scenes, _ := multiViewports(panels)
	for i, sc := range scenes {
		if len(sc.rayPaths) == 0 {
			t.Fatalf("panel %d has no rays after clipping", i)
		}
		for _, rp := range sc.rayPaths {
			for _, p := range rp.points {
				if p.X < sc.minZ || p.X > sc.maxZ {
					t.Errorf("panel %d: ray point z=%g outside the box [%g, %g]", i, p.X, sc.minZ, sc.maxZ)
				}
				if math.Abs(p.Y) > sc.maxY+1e-9 {
					t.Errorf("panel %d: ray point y=%g outside the band half-height %g", i, p.Y, sc.maxY)
				}
			}
		}
	}
}
