package stop

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

const testWavelength = 0.0005876

// doublet is two air-spaced plano-convex-ish elements: the 5 mm air gap between
// surfaces 2 and 3 is the only place a stop can go, and its entrance pupil is
// in front of it.
func doublet(gap float64) []types.Surface {
	return []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 1.0 / 50.0, Thickness: 5, Material: types.Material{ND: 1.5, VD: 50}, Diameter: 30},
		{ID: 2, Type: types.Sphere, Curvature: -1.0 / 50.0, Thickness: gap, Material: types.Material{}, Diameter: 30},
		{ID: 3, Type: types.Sphere, Curvature: 1.0 / 50.0, Thickness: 5, Material: types.Material{ND: 1.5, VD: 50}, Diameter: 30},
		{ID: 4, Type: types.Sphere, Thickness: 100, Material: types.Material{}, Diameter: 30},
	}
}

func request(surfaces [][]types.Surface, ids []string, ref int, epz, epd float64, anchor Anchor) Request {
	return Request{
		ConfigIDs:  ids,
		Surfaces:   surfaces,
		Reference:  ref,
		EPZ:        epz,
		EPD:        epd,
		Anchor:     anchor,
		Wavelength: testWavelength,
		Glass:      glass.NewCatalog(),
	}
}

// TestInteriorStopReproducesPupil is the contract of the command: the inserted
// stop's paraxial entrance pupil is the virtual model's pupil.
func TestInteriorStopReproducesPupil(t *testing.T) {
	src := doublet(5)
	out, rep, err := Materialize(request([][]types.Surface{src}, []string{"cfg"}, 0, 6, 10, Rear))
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if len(out) != 1 || len(out[0]) != len(src)+1 {
		t.Fatalf("surface counts = %v, want one config with %d surfaces", len(out), len(src)+1)
	}
	inserted := out[0][2]
	if inserted.ID != 5 || inserted.Role != "stop" || inserted.AutoAperture || !inserted.Material.IsAir() || inserted.Curvature != 0 {
		t.Errorf("inserted surface = %+v, want a plane air stop with ID 5 and role stop", inserted)
	}
	if got := out[0][1].Thickness + out[0][2].Thickness; math.Abs(got-5) > 1e-9 {
		t.Errorf("split gap = %v, want the original 5 mm", got)
	}
	surface.Precompute(out[0])
	loc, radius, ok := paraxial.EntrancePupil(out[0], 5, testWavelength, glass.NewCatalog())
	if !ok {
		t.Fatal("the inserted stop has no paraxial entrance pupil")
	}
	if math.Abs(loc-6) > 1e-6 {
		t.Errorf("entrance-pupil Z = %v, want 6", loc)
	}
	if math.Abs(2*radius-10) > 1e-6 {
		t.Errorf("entrance-pupil diameter = %v, want 10", 2*radius)
	}
	if len(rep.Configs) != 1 {
		t.Fatalf("report configs = %d, want 1", len(rep.Configs))
	}
	cr := rep.Configs[0]
	if !cr.Reference || cr.StopSurface != 5 || math.Abs(cr.EPZErrorMM) > 1e-6 || math.Abs(cr.EPDErrorMM) > 1e-6 {
		t.Errorf("report = %+v, want a reference stop at the requested pupil", cr)
	}
	if cr.StopZ <= 5 || cr.StopZ >= 10 {
		t.Errorf("stop Z = %v, want it inside the 5..10 mm air gap", cr.StopZ)
	}
}

// TestFrontStopBecomesFirstSurface checks the trivial case: an entrance pupil
// at or before the first surface is the stop plane itself, and the document
// frame is shifted by that distance.
func TestFrontStopBecomesFirstSurface(t *testing.T) {
	src := doublet(5)
	out, rep, err := Materialize(request([][]types.Surface{src}, []string{"cfg"}, 0, -20, 10, Rear))
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if len(out[0]) != len(src)+1 {
		t.Fatalf("surface count = %d, want %d", len(out[0]), len(src)+1)
	}
	s := out[0][0]
	if s.Role != "stop" || math.Abs(s.Thickness-20) > 1e-12 || math.Abs(s.Diameter-10) > 1e-12 {
		t.Errorf("front stop = %+v, want a 10 mm stop 20 mm in front of the old first surface", s)
	}
	if math.Abs(rep.FrameShiftMM-20) > 1e-12 {
		t.Errorf("frame shift = %v, want 20", rep.FrameShiftMM)
	}
	if cr := rep.Configs[0]; cr.StopZ != 0 || math.Abs(cr.StopDiameter-10) > 1e-12 || math.Abs(cr.Magnification-1) > 1e-12 {
		t.Errorf("report = %+v, want the stop at the frame origin with magnification 1", cr)
	}
	surface.Precompute(out[0])
	loc, radius, ok := paraxial.EntrancePupil(out[0], s.ID, testWavelength, glass.NewCatalog())
	if !ok || math.Abs(loc) > 1e-9 || math.Abs(2*radius-10) > 1e-9 {
		t.Errorf("entrance pupil = (%v, %v, %v), want (0, 5, true)", loc, radius, ok)
	}
}

// TestFrontAnchorNeedsSurfaceInFront: a front anchor is meaningless when the
// stop is the first surface, because there is no surface in front of it.
func TestFrontAnchorNeedsSurfaceInFront(t *testing.T) {
	src := doublet(5)
	if _, _, err := Materialize(request([][]types.Surface{src}, []string{"cfg"}, 0, 0, 10, Front)); err == nil {
		t.Fatal("front anchor at the first surface must fail")
	}
}

// TestAnchorsFollowTheirNeighbour checks both multi-config placement rules: the
// rear anchor keeps the distance to the surface behind the stop (so the stop
// moves with a lengthening rear gap), the front anchor keeps the distance to
// the surface in front (so it stays put in a fixed front half).
func TestAnchorsFollowTheirNeighbour(t *testing.T) {
	ref := doublet(5)
	tall := doublet(15) // the air gap after surface 2 grows to 15 mm
	epz := 6.0

	for _, tc := range []struct {
		name   string
		anchor Anchor
		want   func(refStop float64) float64
	}{
		{"rear", Rear, func(refStop float64) float64 { return refStop + 10 }},
		{"front", Front, func(refStop float64) float64 { return refStop }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, rep, err := Materialize(request([][]types.Surface{ref, tall}, []string{"a", "b"}, 0, epz, 10, tc.anchor))
			if err != nil {
				t.Fatalf("Materialize: %v", err)
			}
			if len(rep.Configs) != 2 {
				t.Fatalf("report configs = %d, want 2", len(rep.Configs))
			}
			refStop := rep.Configs[0].StopZ
			want := tc.want(refStop)
			if got := rep.Configs[1].StopZ; math.Abs(got-want) > 1e-6 {
				t.Errorf("config b stop Z = %v, want %v (%s anchor)", got, want, tc.name)
			}
			// Every config's delivered EPD is the requested one, even though
			// the magnification differs.
			for ci := range out {
				surface.Precompute(out[ci])
				_, radius, ok := paraxial.EntrancePupil(out[ci], rep.StopID, testWavelength, glass.NewCatalog())
				if !ok || math.Abs(2*radius-10) > 1e-6 {
					t.Errorf("config %d EPD = %v (ok=%v), want 10", ci, 2*radius, ok)
				}
			}
		})
	}
}

// TestNoAirGap rejects a system whose stop cannot be hosted in air.
func TestNoAirGap(t *testing.T) {
	src := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 1.0 / 50.0, Thickness: 5, Material: types.Material{ND: 1.5, VD: 50}, Diameter: 30},
		{ID: 2, Type: types.Sphere, Thickness: 100, Material: types.Material{}, Diameter: 30},
	}
	if _, _, err := Materialize(request([][]types.Surface{src}, []string{"cfg"}, 0, 6, 10, Rear)); err == nil {
		t.Fatal("a system with no air gap must fail")
	}
}

// TestIDCollision rejects an explicit ID already in use.
func TestIDCollision(t *testing.T) {
	src := doublet(5)
	req := request([][]types.Surface{src}, []string{"cfg"}, 0, 6, 10, Rear)
	req.ID = 2
	if _, _, err := Materialize(req); err == nil {
		t.Fatal("an already-used surface ID must fail")
	}
}

// TestDeterministic: two runs on the same request agree bit for bit.
func TestDeterministic(t *testing.T) {
	src := doublet(5)
	a, ra, err := Materialize(request([][]types.Surface{src}, []string{"cfg"}, 0, 6, 10, Rear))
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	b, rb, err := Materialize(request([][]types.Surface{src}, []string{"cfg"}, 0, 6, 10, Rear))
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if len(a) != len(b) || len(a[0]) != len(b[0]) {
		t.Fatal("different surface counts")
	}
	for i := range a[0] {
		if a[0][i].ID != b[0][i].ID || a[0][i].Thickness != b[0][i].Thickness || a[0][i].Diameter != b[0][i].Diameter {
			t.Errorf("surface %d differs between runs", i)
		}
	}
	if ra.Configs[0].StopZ != rb.Configs[0].StopZ || ra.Configs[0].StopDiameter != rb.Configs[0].StopDiameter {
		t.Error("stop solution differs between runs")
	}
}
