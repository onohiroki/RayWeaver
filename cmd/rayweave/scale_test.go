package main

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
	"gopkg.in/yaml.v3"
)

// TestScalePolynomialAsphereCoefficients guards the polynomial-asphere scaling
// law: a_n*h^n must scale as s, so a_n -> a_n/s^(n-1).  Multiplying instead
// (the old behaviour) destroys the asphere and clips every off-axis ray.
func TestScalePolynomialAsphereCoefficients(t *testing.T) {
	yamlData := `glass_catalog:
  entries:
    - {type: model, name: N-BK7, nd: 1.5168, vd: 64.17}
configs:
  - id: cfg1
    active: true
    surfaces:
      - {id: 1, type: asphere_polynomial, radius: 60.0, conic: -0.5, thickness: 6.0, material: N-BK7, diameter: 30.0, coefficients: [1.0e-4, -2.0e-7, 0.0]}
      - {id: 2, type: sphere, radius: -80.0, thickness: 90.0, material: AIR, diameter: 30.0}
      - {id: 3, type: sphere, radius: 0, thickness: 0, material: AIR, diameter: 30.0}
`
	out := runScaleWithArgs(t, []string{"--efl", "40"}, []byte(yamlData))

	var doc struct {
		Configs []types.Config `yaml:"configs"`
	}
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("yaml.Unmarshal scaled: %v", err)
	}
	got := doc.Configs[0].Surfaces[0]
	s := got.Thickness / 6.0
	wantA4 := 1.0e-4 / math.Pow(s, 3)
	wantA6 := -2.0e-7 / math.Pow(s, 5)
	if math.Abs(got.Coefficients[0]-wantA4) > 1e-18 {
		t.Errorf("a4 = %g, want %g", got.Coefficients[0], wantA4)
	}
	if math.Abs(got.Coefficients[1]-wantA6) > 1e-24 {
		t.Errorf("a6 = %g, want %g", got.Coefficients[1], wantA6)
	}

	// The physical sag must be scale-invariant: sag'(s*h) = s*sag(h).
	var orig struct {
		Configs []types.Config `yaml:"configs"`
	}
	if err := yaml.Unmarshal([]byte(yamlData), &orig); err != nil {
		t.Fatalf("yaml.Unmarshal original: %v", err)
	}
	origSag := surface.SagFunc(orig.Configs[0].Surfaces[0])
	newSag := surface.SagFunc(got)
	for _, h := range []float64{1, 5, 10} {
		if want, have := s*origSag(h), newSag(s*h); math.Abs(want-have) > 1e-9*math.Max(1, math.Abs(want)) {
			t.Errorf("sag at h=%g: scaled %g, want %g", h, have, want)
		}
	}
}

// TestScaleZernikeAsphereCoefficients checks that a Zernike asphere's
// coefficients stay put while the normalisation radius scales: the normalised
// coordinate h/norm_radius is unchanged.
func TestScaleZernikeAsphereCoefficients(t *testing.T) {
	yamlData := `glass_catalog:
  entries:
    - {type: model, name: N-BK7, nd: 1.5168, vd: 64.17}
configs:
  - id: cfg1
    active: true
    surfaces:
      - {id: 1, type: asphere_zernike, radius: 60.0, conic: 0, thickness: 6.0, material: N-BK7, diameter: 30.0, norm_radius: 12.0, coefficients: [1.0e-3, 2.0e-4, -3.0e-5]}
      - {id: 2, type: sphere, radius: -80.0, thickness: 90.0, material: AIR, diameter: 30.0}
      - {id: 3, type: sphere, radius: 0, thickness: 0, material: AIR, diameter: 30.0}
`
	out := runScaleWithArgs(t, []string{"--efl", "40"}, []byte(yamlData))

	var doc struct {
		Configs []types.Config `yaml:"configs"`
	}
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("yaml.Unmarshal scaled: %v", err)
	}
	got := doc.Configs[0].Surfaces[0]
	s := got.Thickness / 6.0
	want := []float64{1.0e-3, 2.0e-4, -3.0e-5}
	for i := range want {
		if got.Coefficients[i] != want[i] {
			t.Errorf("coefficient[%d] = %g, want %g", i, got.Coefficients[i], want[i])
		}
	}
	if math.Abs(got.NormRadius-12.0*s) > 1e-12 {
		t.Errorf("norm_radius = %g, want %g", got.NormRadius, 12.0*s)
	}
}

// TestScaleFieldLengths checks that image-height field definitions, object
// distances and a virtual pupil are scaled with the system.
func TestScaleFieldLengths(t *testing.T) {
	yamlData := `glass_catalog:
  entries:
    - {type: model, name: N-BK7, nd: 1.5168, vd: 64.17}
configs:
  - id: cfg1
    active: true
    fields:
      - {id: 0, angle_deg: 0, weight: 1}
      - {id: 1, image_height: 10.0, weight: 1}
    surfaces:
      - {id: 1, type: sphere, radius: 50.0, thickness: 5.0, material: N-BK7, diameter: 30.0}
      - {id: 2, type: sphere, radius: -50.0, thickness: 100.0, material: AIR, diameter: 30.0}
      - {id: 3, type: sphere, radius: 0, thickness: 0, material: AIR, diameter: 30.0}
chief:
  fields:
    - {angle: 0}
    - {image_height: 10.0}
  reference_surface: 3
  pupil_model:
    mode: virtual_entrance_pupil
    axial_position: 20.0
    diameter: 8.0
`
	out := runScaleWithArgs(t, []string{"--efl", "40"}, []byte(yamlData))

	var doc types.Input
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("yaml.Unmarshal scaled: %v", err)
	}
	s := doc.Configs[0].Surfaces[0].Thickness / 5.0
	if got := doc.Configs[0].Fields[1].ImageHeight; math.Abs(got-10*s) > 1e-12 {
		t.Errorf("config field image_height = %g, want %g", got, 10*s)
	}
	if doc.Chief == nil {
		t.Fatal("chief section missing")
	}
	if got := doc.Chief.Fields[1].ImageHeight; math.Abs(got-10*s) > 1e-12 {
		t.Errorf("chief field image_height = %g, want %g", got, 10*s)
	}
	if doc.Chief.PupilModel == nil {
		t.Fatal("pupil_model missing")
	}
	if got := doc.Chief.PupilModel.AxialPosition; math.Abs(got-20*s) > 1e-12 {
		t.Errorf("pupil axial_position = %g, want %g", got, 20*s)
	}
	if got := doc.Chief.PupilModel.Diameter; math.Abs(got-8*s) > 1e-12 {
		t.Errorf("pupil diameter = %g, want %g", got, 8*s)
	}
}
