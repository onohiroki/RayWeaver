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

	collect := func(mode InvalidRayMode) []string {
		paths := buildRayPaths(nil, []types.ChiefRayResult{cr}, 11, mode)
		out := make([]string, 0, len(paths))
		for _, r := range paths {
			out = append(out, r.path)
		}
		return out
	}

	t.Run("hide", func(t *testing.T) {
		paths := collect(InvalidRayHide)
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
		paths := collect(InvalidRayShow)
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
		paths := collect(InvalidRayClip)
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

func TestBuildRayPathsResultsFollowInvalidMode(t *testing.T) {
	// Traced-result rays (a marginal ray that misses the stop, for example)
	// follow the same invalid-ray policy as fan rays.
	res := types.RayResult{
		ID: "marginal_f0_Yplus",
		Surfaces: []types.SurfaceResult{
			{SurfaceID: 1, Position: types.Vec3{Z: 1, Y: 1}},
			{SurfaceID: 2, Position: types.Vec3{Z: 2, Y: 2}, ErrorCode: "aperture_stop"},
			{SurfaceID: 3, Position: types.Vec3{Z: 3, Y: 3}},
		},
	}
	cases := []struct {
		mode     InvalidRayMode
		want     int // drawn paths
		segments int
	}{
		{InvalidRayHide, 0, 0},
		{InvalidRayShow, 1, 3},
		{InvalidRayClip, 1, 2},
	}
	for _, tc := range cases {
		paths := buildRayPaths([]types.RayResult{res}, nil, 11, tc.mode)
		if len(paths) != tc.want {
			t.Fatalf("mode %d: got %d paths, want %d", tc.mode, len(paths), tc.want)
		}
		if tc.want > 0 {
			if seg := pathSegments(paths[0].path); seg != tc.segments {
				t.Errorf("mode %d: segment count = %d, want %d", tc.mode, seg, tc.segments)
			}
		}
	}

	// An error on the first surface leaves a single point: nothing to draw.
	firstErr := types.RayResult{ID: "x", Surfaces: []types.SurfaceResult{
		{SurfaceID: 1, Position: types.Vec3{Z: 1, Y: 1}, ErrorCode: "aperture_stop"},
		{SurfaceID: 2, Position: types.Vec3{Z: 2, Y: 2}},
	}}
	if paths := buildRayPaths([]types.RayResult{firstErr}, nil, 11, InvalidRayClip); len(paths) != 0 {
		t.Errorf("clip with a first-surface error drew %d paths, want 0", len(paths))
	}
}
