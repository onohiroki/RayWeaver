package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hiroki/rayweaver/internal/escape"
	"github.com/hiroki/rayweaver/internal/optimize"
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

// backFocusPupilInput is backFocusOrderInput's virtual-pupil sibling: a singlet
// under a *declared* virtual entrance pupil whose axial_position is an
// optimisation variable, with an off-axis field so the solved plane genuinely
// depends on the pupil position (an on-axis bundle is invariant under a z
// slide, an off-axis one's footprint moves with it). It exercises the save
// path's pupil plumbing. The image plane is deliberately 60 mm across: a 16°
// field lands 27.7 mm off-axis there, so a 30 mm plane vignettes every ray of
// the bundle the solve is asked to analyse.
func backFocusPupilInput() string {
	return `glass_catalog:
  entries:
    - {type: model, name: N-BK7, nd: 1.5168, vd: 64.17}
chief:
  fields:
    - {angle: 16, direction: [0, 1]}
  reference_surface: 3
  num_rays: 24
  pupil_model:
    mode: virtual_entrance_pupil
    axial_position: -2.0
    diameter: 10.0
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
      - {id: 0, angle_deg: 16, weight: 1}
    surfaces:
      - {id: 1, type: sphere, radius: 50.0, thickness: 5.0, material: N-BK7, diameter: 30.0}
      - {id: 2, type: sphere, radius: -50.0, thickness: 100.0, material: AIR, diameter: 30.0}
      - {id: 3, type: sphere, radius: 0, thickness: 0, material: AIR, diameter: 60.0}
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
	applySavedBackFocusSolve(input, &input.Configs[0], right, gc, pupilModelForInput(&input))
	rightThk := targetThickness(right)

	// Legacy order: solve, then size.
	wrong := append([]types.Surface(nil), surfaces...)
	applySavedBackFocusSolve(input, &input.Configs[0], wrong, gc, pupilModelForInput(&input))
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

// captureOptimizeWarnings redirects the optimize package warning hook for the
// duration of a test and collects everything reported through it.
func captureOptimizeWarnings(t *testing.T) *[]string {
	t.Helper()
	got := &[]string{}
	prev := optimize.Warnf
	optimize.Warnf = func(format string, args ...interface{}) {
		*got = append(*got, fmt.Sprintf(format, args...))
	}
	t.Cleanup(func() { optimize.Warnf = prev })
	return got
}

// TestSavedMinimumNeverShipsAStaleImagePlane covers the save-path half of the
// silent back-focus failure: when the wavefront analysis cannot be evaluated
// (a pupil grid clipped down to a degenerate fit - the "singular normal
// matrix" a tight auto-aperture produced on the 6-element v44 minima), the
// save path must still move the image plane using the paraxial focus and say
// so. The pre-fix behaviour applied a 0 mm shift in silence, so the recorded
// minimum kept the input image plane while the merit had solved another one -
// and `escape extract` copies these surfaces verbatim into the deliverable.
func TestSavedMinimumNeverShipsAStaleImagePlane(t *testing.T) {
	input := parseYAML[types.Input](([]byte)(backFocusOrderInput()))
	for i := range input.Configs[0].Surfaces {
		if input.Configs[0].Surfaces[i].ID == 1 {
			// Close the entrance down until the wavefront fit cannot run.
			input.Configs[0].Surfaces[i].Diameter = 0.4
		}
	}
	gc, _ := loadCatalogs(&input, "")
	warnings := captureOptimizeWarnings(t)
	empty := ""
	surfaces := configSurfaces(input.Configs, &empty)
	surface.Precompute(surfaces)
	before := targetThickness(surfaces)

	applySavedBackFocusSolve(input, &input.Configs[0], surfaces, gc, pupilModelForInput(&input))
	after := targetThickness(surfaces)

	if math.IsNaN(after) {
		t.Fatal("back-focus target surface not found")
	}
	if after == before {
		t.Errorf("image plane still at the input plane (%.6f mm): the failed wavefront solve was swallowed", before)
	}
	joined := strings.Join(*warnings, "\n")
	if !strings.Contains(joined, "back-focus wavefront solve") {
		t.Errorf("expected the wavefront failure to be reported, got: %v", *warnings)
	}
	if !strings.Contains(joined, "paraxial focus") {
		t.Errorf("expected the paraxial fallback to be reported, got: %v", *warnings)
	}
}

// TestBackFocusSamplingUnstable pins the probe's classification. The v49
// failure it exists to catch (0.032 mm of shift at 128 rays against 0.287 at
// 400 - a different coherent-peak extremum) must warn, while a disagreement
// that is merely proportional to a large shift (the v50 smoke's -5.885 mm at
// 400 rays against -5.951 at 800: 1.1%, the same extremum) must not.
func TestBackFocusSamplingUnstable(t *testing.T) {
	cases := []struct {
		name           string
		applied, probe float64
		wantUnstable   bool
	}{
		{"identical", 0.297, 0.297, false},
		{"v49 num_rays 128 vs 400", 0.0321, 0.2870, true},
		{"v50 smoke 400 vs 800", -5.8848, -5.9509, false},
		{"small shift, absolute floor", 0.0, 0.06, true},
		{"below absolute floor", 0.10, 0.14, false},
		{"converged tail", 0.284, 0.297, false},
	}
	for _, c := range cases {
		if got := backFocusSamplingUnstable(c.applied, c.probe); got != c.wantUnstable {
			t.Errorf("%s: applied=%.4f probe=%.4f -> unstable=%v, want %v",
				c.name, c.applied, c.probe, got, c.wantUnstable)
		}
	}
}

// TestSavedBackFocusLandsOnItsFixedPoint pins the save path's iteration: the
// wavefront solve derives its pupil and its best-focus search from the surfaces
// it is handed, so one application to the materialised (pre-solve) surfaces
// lands short of the point where re-solving from the delivered plane returns
// ~0. Measured on a v50 smoke minimum: the gap was 0.149 mm, i.e. the document
// was delivered out of focus under its own `all`-plane convention even though
// the solve had run. focusInput's singlet sits ~50 mm out of focus, so the
// first application moves the plane a long way and a missing iteration shows up
// as a large residual.
func TestSavedBackFocusLandsOnItsFixedPoint(t *testing.T) {
	input := parseYAML[types.Input]([]byte(focusInput("")))
	input.Optimization = &types.OptimizationConfig{
		BackFocusSolve: &types.BackFocusSolveConfig{
			Enabled: true, Type: "wavefront", WeightType: "uniform", NumRays: 32,
		},
	}
	gc, _ := loadCatalogs(&input, "")
	empty := ""
	surfaces := configSurfaces(input.Configs, &empty)
	surface.Precompute(surfaces)

	work := append([]types.Surface(nil), surfaces...)
	applySavedBackFocusSolve(input, &input.Configs[0], work, gc, pupilModelForInput(&input))

	again := append([]types.Surface(nil), work...)
	if !optimize.ApplyBackFocusSolve(again, input.Optimization.BackFocusSolve, "wavefront", 0, 0,
		input.Configs[0].Fields, input.Configs[0].Wavelengths, gc, chiefRayDefinition(input), nil) {
		t.Fatal("test setup: the follow-up wavefront solve did not run")
	}
	residual := math.Abs(maxThicknessDelta(work, again))
	if residual > backFocusFixedPointToleranceMM {
		t.Errorf("delivered image plane is %.4f mm from the solve's fixed point (tolerance %.3f mm): the save path must iterate until it settles",
			residual, backFocusFixedPointToleranceMM)
	}
}

// TestSavedMinimumSolvesWithItsOwnPupil pins the save path's pupil plumbing:
// axial_position is an optimisation variable, so a materialised minimum must
// have its image plane solved for *its* pupil, not for the document's declared
// one. The pre-fix code read input.Chief.PupilModel inside
// applySavedBackFocusSolve, so every --save file was focused for the declared
// z while the file itself carried the optimised one (v50: declared 15 against
// saved 7.7..28.8 — the delivered plane sat 0.01..0.77 mm from its own best
// focus, 0.30 mm on the second-best minimum).
//
// The pin is the delivered document's own fixed point: re-solving it under
// the pupil it carries must be a no-op, while re-solving it under the declared
// pupil must move a long way (that contrast is what the defect looked like).
func TestSavedMinimumSolvesWithItsOwnPupil(t *testing.T) {
	const declaredZ, variableZ = -2.0, -20.0

	input := parseYAML[types.Input]([]byte(backFocusPupilInput()))
	gc, _ := loadCatalogs(&input, "")
	empty := ""
	surfaces := configSurfaces(input.Configs, &empty)
	surface.Precompute(surfaces)

	variables := []optimize.Variable{
		{Name: "pmz", SurfaceID: 0, Param: "pupil_model_axial_position", Min: -40, Max: 50},
	}
	got := materializeSingleInput(input, surfaces, variables, []float64{variableZ}, gc, nil)

	if pupilModelForInput(&got) == nil {
		t.Fatal("the saved document carries no pupil model")
	}
	if got.Chief.PupilModel.AxialPosition != variableZ {
		t.Errorf("saved axial_position = %v, want the minimum's %v",
			got.Chief.PupilModel.AxialPosition, variableZ)
	}

	solveAgain := func(pupil *types.PupilModelConfig) (float64, bool) {
		work := append([]types.Surface(nil), got.Configs[0].Surfaces...)
		ok := optimize.ApplyBackFocusSolve(work, input.Optimization.BackFocusSolve, "wavefront", 0, 0,
			input.Configs[0].Fields, input.Configs[0].Wavelengths, gc, chiefRayDefinition(input), pupil)
		return math.Abs(maxThicknessDelta(got.Configs[0].Surfaces, work)), ok
	}

	own, ok := solveAgain(pupilModelForInput(&got))
	if !ok {
		t.Fatal("test setup: the follow-up wavefront solve did not run")
	}
	if own > backFocusFixedPointToleranceMM {
		t.Errorf("delivered plane is %.4f mm from its own pupil's fixed point (tolerance %.3f mm): the save path solved at the declared pupil",
			own, backFocusFixedPointToleranceMM)
	}

	declared, ok := solveAgain(&types.PupilModelConfig{
		Mode:          "virtual_entrance_pupil",
		AxialPosition: declaredZ,
		Diameter:      input.Chief.PupilModel.Diameter,
	})
	if !ok {
		t.Fatal("test setup: the declared-pupil follow-up solve did not run")
	}
	if declared <= backFocusFixedPointToleranceMM {
		t.Fatalf("test setup: the fixture is not pupil-sensitive (declared-pupil residual %.4f mm)",
			declared)
	}
}
