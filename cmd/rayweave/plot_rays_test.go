package main

import (
	"reflect"
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
)

func TestHasPerConfigRays(t *testing.T) {
	legacy := types.Output{Results: []types.RayResult{{ID: "top"}}}
	if hasPerConfigRays(legacy.Configs) {
		t.Error("top-level results reported as per-config rays")
	}

	perConfig := types.Output{Input: types.Input{Configs: []types.Config{
		{},
		{ChiefRays: []types.ChiefRayResult{{FieldAngle: 2}}},
	}}}
	if !hasPerConfigRays(perConfig.Configs) {
		t.Error("per-config chief_rays not detected")
	}
}

func TestPlotPanelRays(t *testing.T) {
	topResults := []types.RayResult{{ID: "top"}}
	topChief := []types.ChiefRayResult{{FieldAngle: 1}}
	ownResults := []types.RayResult{{ID: "own"}}
	ownChief := []types.ChiefRayResult{{FieldAngle: 2}}

	perConfig := types.Output{
		Input: types.Input{Configs: []types.Config{
			{Results: ownResults, ChiefRays: ownChief},
			{}, // this config was skipped by trace
		}},
		Results:   topResults,
		ChiefRays: topChief,
	}
	// A per-config document never falls back to the top-level copy: that copy
	// describes another config.
	if r, c := plotPanelRays(perConfig, 0, false, true); !reflect.DeepEqual(r, ownResults) || !reflect.DeepEqual(c, ownChief) {
		t.Errorf("panel 0 rays = %v / %v, want its own copy", r, c)
	}
	if r, c := plotPanelRays(perConfig, 1, true, true); len(r) != 0 || len(c) != 0 {
		t.Errorf("panel 1 rays = %v / %v, want none", r, c)
	}

	// A legacy document (no per-config sections) keeps the top-level contract
	// in single-config mode and draws no rays in multi-config mode.
	legacy := types.Output{Input: types.Input{Configs: []types.Config{{}}}, Results: topResults, ChiefRays: topChief}
	if r, c := plotPanelRays(legacy, 0, false, false); !reflect.DeepEqual(r, topResults) || !reflect.DeepEqual(c, topChief) {
		t.Errorf("legacy single mode rays = %v / %v, want the top-level copy", r, c)
	}
	if r, c := plotPanelRays(legacy, 0, true, false); r != nil || c != nil {
		t.Errorf("legacy multi mode rays = %v / %v, want none", r, c)
	}
}
