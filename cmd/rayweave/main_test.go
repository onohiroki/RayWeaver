package main

import (
	"reflect"
	"testing"

	"github.com/hiroki/rayweaver/internal/chief"
	"github.com/hiroki/rayweaver/internal/types"
)

func TestResolveRayFanConfig(t *testing.T) {
	cases := []struct {
		name       string
		rayFan     bool
		fanPlane   string
		rotation   []float64
		wantAngles []float64
		wantNil    bool
	}{
		{name: "no fan", rayFan: false, wantNil: true},
		{name: "ray-fan default", rayFan: true, wantAngles: []float64{0, 90}},
		{name: "plane yz", rayFan: false, fanPlane: "yz", wantAngles: []float64{90}},
		{name: "plane xz", rayFan: false, fanPlane: "xz", wantAngles: []float64{0}},
		{name: "rotation", rayFan: false, rotation: []float64{0, 45, 90}, wantAngles: []float64{0, 45, 90}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := resolveRayFanConfig(c.rayFan, c.fanPlane, c.rotation)
			if c.wantNil {
				if cfg != nil {
					t.Errorf("expected nil config, got %+v", cfg)
				}
				return
			}
			if cfg == nil {
				t.Fatal("expected non-nil config")
			}
			if !reflect.DeepEqual(cfg.Angles, c.wantAngles) {
				t.Errorf("Angles = %v, want %v", cfg.Angles, c.wantAngles)
			}
			if cfg.NumRays != 256 {
				t.Errorf("NumRays = %d, want 256", cfg.NumRays)
			}
		})
	}
}

func TestExpandFanRotationArgs(t *testing.T) {
	in := []string{"--fan-rotation", "0", "45", "90", "--config", "cfg1"}
	want := []string{"--fan-rotation", "0", "--fan-rotation", "45", "--fan-rotation", "90", "--config", "cfg1"}
	got := expandFanRotationArgs(in)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("expandFanRotationArgs = %v, want %v", got, want)
	}
}

func floatPtr(v float64) *float64 { return &v }

func TestExtractMarginalRaysHasX(t *testing.T) {
	mkGrid := func(xs, ys []float64) []types.GridPoint {
		pts := make([]types.GridPoint, len(xs))
		for i := range pts {
			pts[i].ImageX = floatPtr(xs[i])
			pts[i].ImageY = floatPtr(ys[i])
		}
		return pts
	}

	// Pure X-direction field must yield X marginal rays.
	xResult := chief.Result{
		FieldDir: types.Vec3{X: 1, Y: 0},
		GridPoints: mkGrid(
			[]float64{0.1, 0.9, -0.8, 0.0},
			[]float64{0.2, 0.3, -0.4, 0.5},
		),
	}
	// Pure Y-direction field must NOT yield X marginal rays.
	yResult := chief.Result{
		FieldDir: types.Vec3{X: 0, Y: 1},
		GridPoints: mkGrid(
			[]float64{0.9, -0.8, 0.1},
			[]float64{0.9, -0.7, 0.2},
		),
	}

	rays := extractMarginalRays([]chief.Result{xResult, yResult}, 0.00058756, nil, types.JonesVector{})
	ids := map[string]bool{}
	for _, r := range rays {
		ids[r.ID] = true
	}
	if !ids["marginal_f0_Xplus"] || !ids["marginal_f0_Xminus"] {
		t.Errorf("pure-X field: expected X marginal rays, got %v", ids)
	}
	if ids["marginal_f1_Xplus"] || ids["marginal_f1_Xminus"] {
		t.Errorf("pure-Y field: X marginal rays must not be generated, got %v", ids)
	}
	if !ids["marginal_f1_Yplus"] || !ids["marginal_f1_Yminus"] {
		t.Errorf("pure-Y field: expected Y marginal rays, got %v", ids)
	}
}
