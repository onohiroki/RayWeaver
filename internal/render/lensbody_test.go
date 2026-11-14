package render

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
)

func airSurf(id int) types.Surface {
	return types.Surface{ID: id, Diameter: 10, Thickness: 1, Material: types.Material{Key: "AIR"}}
}

func glassSurf(id int, curvature, thickness, diameter float64) types.Surface {
	return types.Surface{
		ID:        id,
		Curvature: curvature,
		Thickness: thickness,
		Diameter:  diameter,
		Material:  types.Material{Key: "1.81:22.8"},
	}
}

// TestCementedRimStaysOnItsSideOfTheBond pins down the drawing of a bonded
// interface: the shared surface must be drawn at its own semi-diameter by both
// elements (the same curve, the same rim point), and neither element's rim may
// reach past the bond into the neighbouring element. The rim must also keep
// its axial land on the taller diameter — a larger free front gets a
// parallel-to-axis edge and then steps diagonally in to the bond, not the
// other way round. Two old bugs are covered: the ±1 mm Z-flange closed a
// concave bonded edge with a wedge drawn into the front element, and the land
// was placed on the bonded (shorter) diameter.
func TestCementedRimStaysOnItsSideOfTheBond(t *testing.T) {
	cases := []struct {
		name     string
		surfaces []types.Surface
	}{
		{
			// A positive front element bonded to a thinner negative one
			// (the min0 zoom minimum: free front semi-diameter 4.863,
			// bond 4.455, free rear 3.756).
			name: "bond shorter than the free front",
			surfaces: []types.Surface{
				airSurf(0),
				glassSurf(1, 0.040308021466415274, 3.58, 9.7258001469441),
				glassSurf(2, -0.07500029073876925, 1, 8.910256506572802),
				{
					ID:        3,
					Curvature: 0.15595926989291908,
					Thickness: 3.71,
					Diameter:  7.51200624499206,
					Material:  types.Material{Key: "AIR"},
				},
			},
		},
		{
			// The bond is the tallest diameter: the land must still be pulled
			// in on each element's own side.
			name: "bond taller than both free surfaces",
			surfaces: []types.Surface{
				airSurf(0),
				glassSurf(1, 0.05, 3, 8.4),
				glassSurf(2, -0.06, 1, 9.6),
				{ID: 3, Curvature: 0.12, Thickness: 3, Diameter: 7.2, Material: types.Material{Key: "AIR"}},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			surfs := tc.surfaces
			zPos := computeZPositions(surfs)
			elems := findElements(surfs, globalMaxSemiDiameter(surfs))
			if len(elems) != 2 {
				t.Fatalf("found %d elements, want 2", len(elems))
			}
			front, back := elems[0], elems[1]
			if !front.r2Cemented || !back.r1Cemented {
				t.Fatalf("doublet not detected as cemented: front.r2=%v back.r1=%v",
					front.r2Cemented, back.r1Cemented)
			}

			ef := computeElemEdges(front, zPos[front.r1Idx], zPos[front.r2Idx])
			eb := computeElemEdges(back, zPos[back.r1Idx], zPos[back.r2Idx])

			bondSemi := surfaceSemiDiameter(front.r2Surf, globalMaxSemiDiameter(surfs))
			if !closeTo(ef.h2eff, bondSemi) || !closeTo(eb.h1eff, bondSemi) {
				t.Errorf("bond drawn at %v / %v, want its own semi-diameter %v",
					ef.h2eff, eb.h1eff, bondSemi)
			}

			// The bonded rim is one point shared by both elements (top).
			fb := ef.topPts[0]
			bb := eb.topPts[len(eb.topPts)-1]
			if !closeTo(fb.X, bb.X) || !closeTo(fb.Y, bb.Y) {
				t.Errorf("bonded rims differ: front %v back %v", fb, bb)
			}

			bondZ := func(y float64) float64 {
				return zPos[front.r2Idx] + globalSag(front.r2Surf, math.Abs(y))
			}
			check := func(name string, pts []vec2, onItsSide func(z, bond float64) bool) {
				for _, p := range pts {
					if math.Abs(p.Y) > bondSemi+1e-9 {
						continue
					}
					if !onItsSide(p.X, bondZ(p.Y)) {
						t.Errorf("%s rim point %v crosses the bond (bond z %.6f)", name, p, bondZ(p.Y))
					}
				}
			}
			check("front", ef.topPts, func(z, bond float64) bool { return z <= bond+1e-9 })
			check("front", ef.bottomPts, func(z, bond float64) bool { return z <= bond+1e-9 })
			check("back", eb.topPts, func(z, bond float64) bool { return z >= bond-1e-9 })
			check("back", eb.bottomPts, func(z, bond float64) bool { return z >= bond-1e-9 })

			// A larger free front keeps a parallel-to-axis edge before the rim
			// steps in: the land is on the taller rim, the diagonal on the
			// shorter one.
			checkAxialLandOnTallerRim(t, "front top", ef.topPts, ef.h1eff, ef.h2eff)
			checkAxialLandOnTallerRim(t, "front bottom", ef.bottomPts, ef.h1eff, ef.h2eff)
			checkAxialLandOnTallerRim(t, "back top", eb.topPts, eb.h1eff, eb.h2eff)
			checkAxialLandOnTallerRim(t, "back bottom", eb.bottomPts, eb.h1eff, eb.h2eff)
		})
	}
}

// checkAxialLandOnTallerRim asserts the rim polyline consists of an axial land
// (two points at the taller |Y| with different Z) and a connection to the
// shorter rim. The land must not sit on the shorter side: that draws a lens
// edge that steps in immediately and then runs parallel to the axis at the
// wrong diameter.
func checkAxialLandOnTallerRim(t *testing.T, name string, pts []vec2, h1, h2 float64) {
	t.Helper()
	tall, short := math.Max(h1, h2), math.Min(h1, h2)
	var onTall []vec2
	reachesShort := false
	for _, p := range pts {
		switch {
		case closeTo(math.Abs(p.Y), tall):
			onTall = append(onTall, p)
		case closeTo(math.Abs(p.Y), short):
			reachesShort = true
		}
	}
	if len(onTall) != 2 || closeTo(onTall[0].X, onTall[1].X) {
		t.Errorf("%s rim has no axial land on the taller rim: %v", name, pts)
	}
	if !reachesShort {
		t.Errorf("%s rim does not reach the shorter rim: %v", name, pts)
	}
}

func closeTo(a, b float64) bool {
	return math.Abs(a-b) <= 1e-9
}
