package pupil

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
)

// TestLaunchRemapIntoEllipse checks the affine remap that re-lays a full
// nominal grid over a measured effective (vignetted) pupil ellipse: the sample
// count and order stay identical (so the grid is unchanged as a sampling of
// the pupil), every remapped cell lands inside the ellipse, its extent fills
// the ellipse rather than a subset of it, and the cell areas carry the map's
// determinant — the area×det weight the flux of the remapped cell.
func TestLaunchRemapIntoEllipse(t *testing.T) {
	const R = 10.0
	eff := &types.VignettingDef{
		DecenterX:    0.2,
		DecenterY:    -0.1,
		CompressionX: 1 - 0.6,
		CompressionY: 1 - 0.35,
		Tangent:      math.Tan(20 * math.Pi / 180),
	}
	spec := LaunchSpec{
		NumRays:        256,
		GridType:       types.GridHex,
		ApertureRadius: R,
		RayDir:         types.Vec3{Z: 1},
		ZStart:         -100,
		OPLMode:        OPLLaunch,
	}
	plain := Launch(spec)
	if len(plain) == 0 {
		t.Fatal("plain launch produced no samples")
	}
	spec.Remap = eff
	mapped := Launch(spec)
	if len(mapped) != len(plain) {
		t.Fatalf("remapped launch produced %d samples, want %d (the grid is re-laid, not thinned)",
			len(mapped), len(plain))
	}

	det := (1 - eff.CompressionX) * (1 - eff.CompressionY)
	theta := math.Atan(eff.Tangent)
	ct, st := math.Cos(theta), math.Sin(theta)
	a := (1 - eff.CompressionX) * R
	b := (1 - eff.CompressionY) * R
	cx, cy := eff.DecenterX*R, eff.DecenterY*R

	// The mapped grid fills the ellipse exactly, so its edge cells sit ON the
	// boundary; inflate by a hair (the reported ellipse carries the same
	// effectiveVignettingSafety inflation) to test membership robustly.
	inflated := *eff
	inflated.CompressionX -= 0.001
	inflated.CompressionY -= 0.001

	var sumX, sumY float64
	maxU, maxV := 0.0, 0.0   // extent of the remapped grid in the ellipse frame
	maxU0, maxV0 := 0.0, 0.0 // extent of the plain grid it was mapped from
	for i := range mapped {
		// The map itself: grid point -> centre + R(theta)·(px/R·a, py/R·b),
		// cell area scaled by the determinant.
		ux, vy := plain[i].PupilX/R*a, plain[i].PupilY/R*b
		wantX := cx + ux*ct - vy*st
		wantY := cy + ux*st + vy*ct
		if math.Abs(mapped[i].PupilX-wantX) > 1e-12*R || math.Abs(mapped[i].PupilY-wantY) > 1e-12*R {
			t.Fatalf("sample %d mapped to (%v, %v), want (%v, %v)", i,
				mapped[i].PupilX, mapped[i].PupilY, wantX, wantY)
		}
		if want := plain[i].Area * det; math.Abs(mapped[i].Area-want) > 1e-9*math.Max(1, want) {
			t.Fatalf("sample %d area = %v, want %v (area×det)", i, mapped[i].Area, want)
		}
		x, y := mapped[i].PupilX, mapped[i].PupilY
		if !inflated.Contains(x, y, R) {
			t.Fatalf("sample %d (%v, %v) falls outside the remap ellipse", i, x, y)
		}
		// Extent in the ellipse frame: the grid covers most of the nominal
		// disc (its own edge points fall short of R by a fraction of a cell),
		// so the remapped extent must scale with the grid's own, not with the
		// exact semi-axes.
		u := (x-cx)*ct + (y-cy)*st
		v := -(x-cx)*st + (y-cy)*ct
		if math.Abs(u) > maxU {
			maxU = math.Abs(u)
		}
		if math.Abs(v) > maxV {
			maxV = math.Abs(v)
		}
		if math.Abs(plain[i].PupilX) > maxU0 {
			maxU0 = math.Abs(plain[i].PupilX)
		}
		if math.Abs(plain[i].PupilY) > maxV0 {
			maxV0 = math.Abs(plain[i].PupilY)
		}
		sumX += x
		sumY += y
	}
	if math.Abs(sumX/float64(len(mapped))-cx) > 0.01*R || math.Abs(sumY/float64(len(mapped))-cy) > 0.01*R {
		t.Errorf("remapped centre = (%v, %v), want (%v, %v)", sumX/float64(len(mapped)),
			sumY/float64(len(mapped)), cx, cy)
	}
	if math.Abs(maxU-maxU0/R*a) > 1e-9*a || math.Abs(maxV-maxV0/R*b) > 1e-9*b {
		t.Errorf("remapped extent = (%v, %v), want the grid's own scaled to the ellipse (%v, %v)",
			maxU, maxV, maxU0/R*a, maxV0/R*b)
	}
	if maxU < 0.9*a || maxV < 0.9*b {
		t.Errorf("remapped grid only spans (%v, %v) of the ellipse semi-axes (%v, %v)",
			maxU, maxV, a, b)
	}
}

// TestLaunchRemapDegenerateEllipse makes sure a non-positive semi-axis
// launches nothing rather than silently keeping the nominal grid: the caller
// asked for a specific ellipse, and returning the unremapped grid would sample
// the wrong pupil while still looking healthy.
func TestLaunchRemapDegenerateEllipse(t *testing.T) {
	const R = 10.0
	spec := LaunchSpec{
		NumRays:        64,
		GridType:       types.GridHex,
		ApertureRadius: R,
		RayDir:         types.Vec3{Z: 1},
		ZStart:         -100,
		OPLMode:        OPLLaunch,
		Remap:          &types.VignettingDef{CompressionX: 1, CompressionY: 0.5}, // a = 0
	}
	if got := Launch(spec); len(got) != 0 {
		t.Errorf("degenerate ellipse produced %d samples, want 0", len(got))
	}
}
