package main

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hiroki/rayweaver/internal/escape"
	"github.com/hiroki/rayweaver/internal/surface"
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
	s := newEscapeFileSaver(base, build, nil, true)

	s.record(0, escape.Point{X: []float64{1.0}, Merit: 5.0}, true, 0)
	cur := base + "0.yaml"
	if _, err := os.Stat(cur); err != nil {
		t.Fatalf("expected %s after a new record: %v", cur, err)
	}
	if got := thicknessOf(t, cur); got != 1.0 {
		t.Fatalf("min0 thickness = %v, want 1.0", got)
	}

	// Improved version of minimum 0: the old file is renamed to .1.yaml and
	// the better point is written back to min0.yaml.
	s.record(0, escape.Point{X: []float64{0.5}, Merit: 2.0}, false, 1)
	archived := base + "0.1.yaml"
	if _, err := os.Stat(archived); err != nil {
		t.Fatalf("expected archived %s after improvement: %v", archived, err)
	}
	if got := thicknessOf(t, archived); got != 1.0 {
		t.Fatalf("archived thickness = %v, want 1.0 (old version)", got)
	}
	if got := thicknessOf(t, cur); got != 0.5 {
		t.Fatalf("current min0 thickness = %v, want 0.5 (improved)", got)
	}

	// A second distinct minimum.
	s.record(1, escape.Point{X: []float64{2.0}, Merit: 3.0}, true, 0)
	cur2 := base + "1.yaml"
	if _, err := os.Stat(cur2); err != nil {
		t.Fatalf("expected %s after a second new record: %v", cur2, err)
	}

	// Second improvement of minimum 0: current min0.yaml (v2) -> min0.2.yaml.
	s.record(0, escape.Point{X: []float64{0.25}, Merit: 1.0}, false, 2)
	archived2 := base + "0.2.yaml"
	if _, err := os.Stat(archived2); err != nil {
		t.Fatalf("expected archived %s after second improvement: %v", archived2, err)
	}
	if got := thicknessOf(t, archived2); got != 0.5 {
		t.Fatalf("second-archived thickness = %v, want 0.5", got)
	}
	if got := thicknessOf(t, cur); got != 0.25 {
		t.Fatalf("current min0 thickness = %v, want 0.25", got)
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

// backFocusOrderInput is a singlet whose image plane sits far from focus, with
// a wavefront back-focus solve configured and an auto_aperture surface the
// caller resizes. It exercises the aperture -> back-focus ordering: solving
// before the sized aperture is applied freezes the plane on the stored-diameter
// bundle.
func backFocusOrderInput() string {
	return `glass_catalog:
  entries:
    - {type: model, name: N-BK7, nd: 1.5168, vd: 64.17}
chief:
  fields:
    - {angle: 0, direction: [0, 1]}
  reference_surface: 3
  num_rays: 24
optimization:
  back_focus_solve:
    enabled: true
    type: wavefront
    weight_type: uniform
    num_rays: 32
configs:
  - id: cfg1
    active: true
    fields:
      - {id: 0, angle_deg: 0, weight: 1}
    surfaces:
      - {id: 1, type: sphere, radius: 50.0, thickness: 5.0, material: N-BK7, diameter: 6.0, auto_aperture: true}
      - {id: 2, type: sphere, radius: -50.0, thickness: 100.0, material: AIR, diameter: 40.0}
      - {id: 3, type: sphere, radius: 0, thickness: 0, material: AIR, diameter: 40.0}
`
}

// targetThickness returns the thickness of the back-focus target (here the
// last non-air surface before the image plane) of a surface list.
func targetThickness(surfaces []types.Surface) float64 {
	for _, s := range surfaces {
		if s.ID == 2 {
			return s.Thickness
		}
	}
	return math.NaN()
}

// TestMaterializeSizesAperturesBeforeBackFocus pins the ordering in
// materializeSingleInput: the sized auto_aperture diameters must be applied
// before the back-focus solve so the saved plane is the best focus of the same
// (sized) beam the optimizer evaluated. It compares materializeSingleInput
// against the two explicit orderings.
func TestMaterializeSizesAperturesBeforeBackFocus(t *testing.T) {
	input := parseYAML[types.Input]([]byte(backFocusOrderInput()))
	gc, _ := loadCatalogs(&input, "")
	empty := ""
	surfaces := configSurfaces(input.Configs, &empty)
	surface.Precompute(surfaces)

	// The sized diameter the optimizer would persist (larger than the stored
	// 6 mm, so the two orderings sample different beams).
	sized := map[int]float64{1: 40.0}

	got := materializeSingleInput(input, surfaces, nil, nil, gc, sized)
	gotThk := targetThickness(got.Configs[0].Surfaces)

	// Correct order: size, then solve.
	right := append([]types.Surface(nil), surfaces...)
	applyApertures(right, sized)
	applySavedBackFocusSolve(input, &input.Configs[0], right, gc)
	rightThk := targetThickness(right)

	// Legacy order: solve, then size.
	wrong := append([]types.Surface(nil), surfaces...)
	applySavedBackFocusSolve(input, &input.Configs[0], wrong, gc)
	applyApertures(wrong, sized)
	wrongThk := targetThickness(wrong)

	if rightThk == wrongThk {
		t.Fatalf("test setup: the two orderings coincide (%.6f); the system is not aperture-sensitive", rightThk)
	}
	if math.Abs(gotThk-rightThk) > 1e-9 {
		t.Errorf("materializeSingleInput target thickness = %.6f, want %.6f (size before solve)", gotThk, rightThk)
	}
	if math.Abs(gotThk-wrongThk) <= 1e-9 {
		t.Errorf("materializeSingleInput target thickness = %.6f matches the solve-before-size ordering", gotThk)
	}
	// The sized diameter must survive into the saved surfaces.
	for _, s := range got.Configs[0].Surfaces {
		if s.ID == 1 && math.Abs(s.Diameter-40.0) > 1e-9 {
			t.Errorf("surface 1 diameter = %v, want the sized 40.0", s.Diameter)
		}
	}
}
