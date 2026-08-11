package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hiroki/rayweaver/internal/escape"
	"github.com/hiroki/rayweaver/internal/types"
	"gopkg.in/yaml.v3"
)

func TestSplitSaveBase(t *testing.T) {
	cases := []struct{ in, stem, ext string }{
		{in: "result", stem: "result", ext: ".yaml"},
		{in: "result.yaml", stem: "result", ext: ".yaml"},
		{in: "result.yml", stem: "result", ext: ".yml"},
		{in: "out/YAML", stem: "out/YAML", ext: ".yaml"},
		{in: "dir/result.YAML", stem: "dir/result", ext: ".YAML"},
	}
	for _, c := range cases {
		stem, ext := splitSaveBase(c.in)
		if stem != c.stem || ext != c.ext {
			t.Errorf("splitSaveBase(%q) = (%q, %q), want (%q, %q)", c.in, stem, ext, c.stem, c.ext)
		}
	}
}

func TestEscapeFileSaverVersioning(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "min")
	build := func(p escape.Point) types.Input {
		return types.Input{
			Metadata: newMetadata(),
			Configs: []types.Config{{
				ID: "c1",
				Surfaces: []types.Surface{
					{ID: 1, Type: types.Sphere, Thickness: p.X[0], Material: types.Material{}},
				},
			}},
		}
	}
	s := newEscapeFileSaver(base, build)

	s.record(0, escape.Point{X: []float64{1.0}, Merit: 5.0}, true, 0)
	cur := base + "1.yaml"
	if _, err := os.Stat(cur); err != nil {
		t.Fatalf("expected %s after a new record: %v", cur, err)
	}
	if got := thicknessOf(t, cur); got != 1.0 {
		t.Fatalf("min1 thickness = %v, want 1.0", got)
	}

	// Improved version of minimum 0: the old file is renamed to .1.yaml and
	// the better point is written back to min1.yaml.
	s.record(0, escape.Point{X: []float64{0.5}, Merit: 2.0}, false, 1)
	archived := base + "1.1.yaml"
	if _, err := os.Stat(archived); err != nil {
		t.Fatalf("expected archived %s after improvement: %v", archived, err)
	}
	if got := thicknessOf(t, archived); got != 1.0 {
		t.Fatalf("archived thickness = %v, want 1.0 (old version)", got)
	}
	if got := thicknessOf(t, cur); got != 0.5 {
		t.Fatalf("current min1 thickness = %v, want 0.5 (improved)", got)
	}

	// A second distinct minimum.
	s.record(1, escape.Point{X: []float64{2.0}, Merit: 3.0}, true, 0)
	cur2 := base + "2.yaml"
	if _, err := os.Stat(cur2); err != nil {
		t.Fatalf("expected %s after a second new record: %v", cur2, err)
	}

	// Second improvement of minimum 0: current min1.yaml (v2) -> min1.2.yaml.
	s.record(0, escape.Point{X: []float64{0.25}, Merit: 1.0}, false, 2)
	archived2 := base + "1.2.yaml"
	if _, err := os.Stat(archived2); err != nil {
		t.Fatalf("expected archived %s after second improvement: %v", archived2, err)
	}
	if got := thicknessOf(t, archived2); got != 0.5 {
		t.Fatalf("second-archived thickness = %v, want 0.5", got)
	}
	if got := thicknessOf(t, cur); got != 0.25 {
		t.Fatalf("current min1 thickness = %v, want 0.25", got)
	}
}

func thicknessOf(t *testing.T, path string) float64 {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var in types.Input
	if err := yaml.Unmarshal(data, &in); err != nil {
		t.Fatalf("yaml.Unmarshal %s: %v", path, err)
	}
	return in.Configs[0].Surfaces[0].Thickness
}

func TestWriteFileAtomicReplacesAndCleansTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.yaml")
	if err := writeFileAtomic(path, []byte("one")); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}
	if err := writeFileAtomic(path, []byte("two")); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "two" {
		t.Fatalf("content = %q, want %q", data, "two")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("leftover temp file %s", e.Name())
		}
	}
}
