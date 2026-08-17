package render

import (
	"strings"
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
)

func fanPoint(n int, errAt int) types.FanPoint {
	fp := types.FanPoint{Path: make([]types.SurfaceResult, 0, n)}
	for i := 0; i < n; i++ {
		sr := types.SurfaceResult{
			SurfaceID: i + 1,
			Position:  types.Vec3{Z: float64(i + 1), Y: float64(i + 1)},
		}
		if i == errAt {
			sr.ErrorCode = "aperture_stop"
		}
		fp.Path = append(fp.Path, sr)
	}
	return fp
}

func pathSegments(path string) int {
	return strings.Count(path, " L ") + 1
}

func TestBuildFanPathsInvalidModes(t *testing.T) {
	clean := fanPoint(4, -1)
	midErr := fanPoint(4, 2)   // error on the 3rd surface
	lastErr := fanPoint(4, 3)  // error on the last surface
	firstErr := fanPoint(4, 0) // error on the very first surface

	cr := types.ChiefRayResult{
		FieldAngle: 10,
		RayFan: &types.RayFan{
			Meridional: []types.FanPoint{clean, midErr, lastErr, firstErr},
			Rotated: []types.RotatedFan{{
				AngleDeg: 45,
				Points:   []types.FanPoint{midErr, clean},
			}},
		},
	}

	collect := func(mode FanInvalidMode) []string {
		paths := buildRayPaths(nil, []types.ChiefRayResult{cr}, 11, mode)
		out := make([]string, 0, len(paths))
		for _, r := range paths {
			out = append(out, r.path)
		}
		return out
	}

	t.Run("hide", func(t *testing.T) {
		paths := collect(FanInvalidHide)
		// clean (meridional + rotated), lastErr/firstErr/midErr all hidden.
		if len(paths) != 2 {
			t.Fatalf("got %d fan paths, want 2 (clean rays only): %v", len(paths), paths)
		}
		for _, p := range paths {
			if seg := pathSegments(p); seg != 4 {
				t.Errorf("clean ray segment count = %d, want 4", seg)
			}
		}
	})

	t.Run("show", func(t *testing.T) {
		paths := collect(FanInvalidShow)
		if len(paths) != 6 {
			t.Fatalf("got %d fan paths, want 6 (all rays full)", len(paths))
		}
		for _, p := range paths {
			if seg := pathSegments(p); seg != 4 {
				t.Errorf("segment count = %d, want 4 (full path)", seg)
			}
		}
	})

	t.Run("clip", func(t *testing.T) {
		paths := collect(FanInvalidClip)
		if len(paths) != 5 {
			t.Fatalf("got %d fan paths, want 5 (firstErr dropped)", len(paths))
		}
		var full, clipped int
		for _, p := range paths {
			switch pathSegments(p) {
			case 4:
				full++ // clean (x2) + lastErr: error on the last surface, no truncation
			case 3:
				clipped++ // midErr (meridional + rotated): up to the erroring surface
			default:
				t.Fatalf("unexpected path %q (%d segments)", p, pathSegments(p))
			}
		}
		if full != 3 || clipped != 2 {
			t.Errorf("full=%d clipped=%d, want full=3 clipped=2", full, clipped)
		}
	})
}

func TestBuildRayPathsResultsUnaffected(t *testing.T) {
	// Traced-result rays are independent of the invalid-fan mode.
	res := types.RayResult{
		ID: "marginal_f0_Yplus",
		Surfaces: []types.SurfaceResult{
			{SurfaceID: 1, Position: types.Vec3{Z: 1, Y: 1}},
			{SurfaceID: 2, Position: types.Vec3{Z: 2, Y: 2}, ErrorCode: "aperture_stop"},
			{SurfaceID: 3, Position: types.Vec3{Z: 3, Y: 3}},
		},
	}
	for _, mode := range []FanInvalidMode{FanInvalidHide, FanInvalidShow, FanInvalidClip} {
		paths := buildRayPaths([]types.RayResult{res}, nil, 11, mode)
		if len(paths) != 1 {
			t.Fatalf("mode %d: got %d paths, want 1 result ray", mode, len(paths))
		}
		if seg := pathSegments(paths[0].path); seg != 3 {
			t.Errorf("mode %d: result ray segment count = %d, want 3", mode, seg)
		}
	}
}