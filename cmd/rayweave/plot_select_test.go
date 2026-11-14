package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
)

// selectionConfigs returns a document whose active/inactive mix and empty
// config exercise every selection rule.
func selectionConfigs() []types.Config {
	cfg := func(id string, active bool, surfaces int) types.Config {
		c := types.Config{ID: id, Active: active}
		for i := 0; i < surfaces; i++ {
			c.Surfaces = append(c.Surfaces, types.Surface{ID: i + 1, Diameter: 10, Thickness: 1})
		}
		return c
	}
	return []types.Config{
		cfg("wide", true, 2),
		cfg("mid", true, 2),
		cfg("tele", false, 2),
		cfg("extra", true, 2),
		cfg("empty", true, 0),
	}
}

func TestPlotTargetsSingleConfig(t *testing.T) {
	cfgs := selectionConfigs()

	indices, multi, warnings, err := plotTargets(cfgs, "", false, nil, 5)
	if err != nil {
		t.Fatalf("default: %v", err)
	}
	if multi || len(warnings) != 0 || !reflect.DeepEqual(indices, []int{0}) {
		t.Errorf("default = %v, multi=%v, warnings=%v; want [0], false, none", indices, multi, warnings)
	}

	indices, multi, _, err = plotTargets(cfgs, "tele", false, nil, 5)
	if err != nil || multi || !reflect.DeepEqual(indices, []int{2}) {
		t.Errorf("--config tele = %v, multi=%v, err=%v; want [2], false", indices, multi, err)
	}

	if _, _, _, err = plotTargets(cfgs, "nope", false, nil, 5); err == nil {
		t.Error("unknown --config did not fail")
	}
}

func TestPlotTargetsMultiDefaults(t *testing.T) {
	cfgs := selectionConfigs()

	// Active configs in document order; the surfaceless one is skipped.
	indices, multi, warnings, err := plotTargets(cfgs, "", true, nil, 5)
	if err != nil {
		t.Fatalf("--multi-config: %v", err)
	}
	if !multi {
		t.Error("multi mode not reported")
	}
	if want := []int{0, 1, 3}; !reflect.DeepEqual(indices, want) {
		t.Errorf("indices = %v, want %v", indices, want)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "empty") {
		t.Errorf("warnings = %v, want one surfaceless-config warning", warnings)
	}

	// A document that never sets active still draws every config.
	for i := range cfgs {
		cfgs[i].Active = false
	}
	indices, _, _, err = plotTargets(cfgs, "", true, nil, 5)
	if err != nil {
		t.Fatalf("inactive document: %v", err)
	}
	if want := []int{0, 1, 2, 3}; !reflect.DeepEqual(indices, want) {
		t.Errorf("indices = %v, want every config in document order %v", indices, want)
	}
}

func TestPlotTargetsMultiLimit(t *testing.T) {
	var cfgs []types.Config
	for i := 0; i < 7; i++ {
		cfgs = append(cfgs, types.Config{
			ID:       string(rune('a' + i)),
			Active:   true,
			Surfaces: []types.Surface{{ID: 1, Diameter: 10, Thickness: 1}},
		})
	}

	indices, multi, warnings, err := plotTargets(cfgs, "", true, nil, 5)
	if err != nil || !multi {
		t.Fatalf("--multi-config: indices=%v multi=%v err=%v", indices, multi, err)
	}
	if want := []int{0, 1, 2, 3, 4}; !reflect.DeepEqual(indices, want) {
		t.Errorf("indices = %v, want the first %d %v", indices, 5, want)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "first 5") {
		t.Errorf("warnings = %v, want a truncation warning", warnings)
	}
}

func TestPlotTargetsExplicitSelection(t *testing.T) {
	cfgs := selectionConfigs()

	indices, multi, warnings, err := plotTargets(cfgs, "", false, []string{"wide", "extra"}, 5)
	if err != nil {
		t.Fatalf("--configs wide,extra: %v", err)
	}
	if !multi {
		t.Error("--configs did not enable multi-config mode")
	}
	if want := []int{0, 3}; !reflect.DeepEqual(indices, want) {
		t.Errorf("indices = %v, want %v", indices, want)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}

	// Indexes resolve like ids, and duplicates are dropped in first-seen order.
	indices, _, _, err = plotTargets(cfgs, "", true, []string{"2", "0", "2"}, 5)
	if err != nil {
		t.Fatalf("--configs 2,0,2: %v", err)
	}
	if want := []int{2, 0}; !reflect.DeepEqual(indices, want) {
		t.Errorf("indices = %v, want %v", indices, want)
	}

	// An explicit selection is not capped, and a surfaceless config is skipped.
	indices, _, warnings, err = plotTargets(cfgs, "", true, []string{"wide", "empty", "extra"}, 1)
	if err != nil {
		t.Fatalf("explicit with empty surface list: %v", err)
	}
	if want := []int{0, 3}; !reflect.DeepEqual(indices, want) {
		t.Errorf("indices = %v, want %v (no cap on an explicit list)", indices, want)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "empty") {
		t.Errorf("warnings = %v, want one surfaceless-config warning", warnings)
	}

	if _, _, _, err = plotTargets(cfgs, "", true, []string{"nope"}, 5); err == nil {
		t.Error("unknown --configs entry did not fail")
	}
}

func TestPlotTargetsRejectsBothSelectors(t *testing.T) {
	cfgs := selectionConfigs()
	if _, _, _, err := plotTargets(cfgs, "wide", true, nil, 5); err == nil {
		t.Error("--config with --multi-config did not fail")
	}
	if _, _, _, err := plotTargets(cfgs, "wide", false, []string{"mid"}, 5); err == nil {
		t.Error("--config with --configs did not fail")
	}
}

func TestSplitConfigList(t *testing.T) {
	got := splitConfigList(" a, b ,,c ")
	if want := []string{"a", "b", "c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("splitConfigList = %v, want %v", got, want)
	}
	if got := splitConfigList("  , "); len(got) != 0 {
		t.Errorf("splitConfigList of blanks = %v, want none", got)
	}
}
