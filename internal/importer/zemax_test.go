package importer

import (
	"math"
	"testing"
)

func TestZemax_BasicTextFormat(t *testing.T) {
	input := `! Test
VERS 2023-01-01
MODE SEQ
NAME Test
UNIT MM
STOP 3
WAVL 0.00058756
WAVL 0.00048613
FIELD 0 0 0.0 1.0
FIELD 1 0 5.0 1.0
SURF 0
  TYPE STANDARD
  CURV 0.0
  THIC INFINITY
  GLAS AIR
  DIAM 0.0
SURF 1
  TYPE STANDARD
  CURV 0.02
  THIC 5.0
  GLAS BK7
  DIAM 20.0
SURF 2
  TYPE STANDARD
  CURV -0.01
  THIC 20.0
  GLAS AIR
  DIAM 20.0
SURF 3
  TYPE STANDARD
  CURV 0.0
  THIC 0.0
  GLAS AIR
  DIAM 10.0
`
	result, err := ParseZemax(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Surfaces) != 3 {
		t.Fatalf("expected 3 surfaces, got %d", len(result.Surfaces))
	}
	if result.StopSurface != 3 {
		t.Errorf("stop: expected 3, got %d", result.StopSurface)
	}
	if result.Surfaces[0].Thickness != 5.0 {
		t.Errorf("surface 1 thick: expected 5.0, got %g", result.Surfaces[0].Thickness)
	}
	if result.Surfaces[0].Material != "BK7" {
		t.Errorf("surface 1 material: expected BK7, got %q", result.Surfaces[0].Material)
	}
	if result.Surfaces[0].Diameter != 40.0 {
		t.Errorf("surface 1 diameter: expected 40.0 (2*20), got %g", result.Surfaces[0].Diameter)
	}
	if result.Surfaces[1].Diameter != 40.0 {
		t.Errorf("surface 2 diameter: expected 40.0 (2*20), got %g", result.Surfaces[1].Diameter)
	}
	if result.Surfaces[2].Diameter != 20.0 {
		t.Errorf("surface 3 diameter: expected 20.0 (2*10), got %g", result.Surfaces[2].Diameter)
	}
	if len(result.Wavelengths) != 2 {
		t.Errorf("expected 2 wavelengths, got %d", len(result.Wavelengths))
	}
	if len(result.Fields) != 2 {
		t.Errorf("expected 2 fields, got %d", len(result.Fields))
	}
}

func TestZemax_DISZ_Thickness(t *testing.T) {
	input := `SURF 0
  TYPE STANDARD
  CURV 0.0
  DISZ INFINITY
  GLAS AIR
  DIAM 0.0
SURF 1
  TYPE STANDARD
  CURV 0.02
  DISZ 5.0
  GLAS BK7
  DIAM 20.0
SURF 2
  TYPE STANDARD
  CURV 0.0
  DISZ 0.0
  GLAS AIR
  DIAM 10.0
`
	result, err := ParseZemax(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Surfaces) != 2 {
		t.Fatalf("expected 2 surfaces, got %d", len(result.Surfaces))
	}
	if result.Surfaces[0].Thickness != 5.0 {
		t.Errorf("surface 1 thick: expected 5.0 from DISZ, got %g", result.Surfaces[0].Thickness)
	}
	if result.Surfaces[1].Thickness != 0.0 {
		t.Errorf("surface 2 thick: expected 0.0 from DISZ, got %g", result.Surfaces[1].Thickness)
	}
}

func TestZemax_WWGT(t *testing.T) {
	input := `WAVL 0.00058756
WAVL 0.00048613
WAVL 0.00065627
WWGT 1.0 0.8 0.6
SURF 0
  TYPE STANDARD
  CURV 0.0
  THIC INFINITY
  GLAS AIR
SURF 1
  TYPE STANDARD
  CURV 0.02
  THIC 5.0
  GLAS BK7
  DIAM 20.0
`
	result, err := ParseZemax(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Wavelengths) != 3 {
		t.Fatalf("expected 3 wavelengths, got %d", len(result.Wavelengths))
	}
	weights := []float64{1.0, 0.8, 0.6}
	for i, w := range weights {
		if math.Abs(result.Wavelengths[i].Weight-w) > 1e-10 {
			t.Errorf("wavelength %d weight: expected %g, got %g", i, w, result.Wavelengths[i].Weight)
		}
	}
}

func TestZemax_FTYP_ImageHeight(t *testing.T) {
	input := `
FIELD 0 0 0.0
FIELD 1 1 10.0
FIELD 2 2 5.0
SURF 0
  TYPE STANDARD
  CURV 0.0
  THIC INFINITY
SURF 1
  TYPE STANDARD
  CURV 0.02
  THIC 5.0
  GLAS BK7
`
	result, err := ParseZemax(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Fields) != 3 {
		t.Fatalf("expected 3 fields, got %d", len(result.Fields))
	}
	if result.Fields[0].AngleDeg != 0 {
		t.Errorf("field 0 angle: expected 0, got %g", result.Fields[0].AngleDeg)
	}
	if result.Fields[1].ImageHeight != 10.0 {
		t.Errorf("field 1 image_height: expected 10.0, got %g", result.Fields[1].ImageHeight)
	}
}

func TestZemax_EVENASPH(t *testing.T) {
	input := `SURF 0
  TYPE STANDARD
  CURV 0.0
  THIC INFINITY
SURF 1
  TYPE EVENASPH
  CURV 0.02
  THIC 5.0
  GLAS BK7
  DIAM 20.0
  CONI -1.5
  PARM 2 0.001
  PARM 3 -0.0002
  PARM 4 1e-05
  PARM 5 -1e-06
`
	result, err := ParseZemax(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Surfaces) != 1 {
		t.Fatalf("expected 1 surface, got %d", len(result.Surfaces))
	}
	s := result.Surfaces[0]
	if s.Type != "asphere_polynomial" {
		t.Errorf("type: expected asphere_polynomial, got %s", s.Type)
	}
	if s.Conic != -1.5 {
		t.Errorf("conic: expected -1.5, got %g", s.Conic)
	}
	if s.Diameter != 40.0 {
		t.Errorf("diameter: expected 40.0 (2*20), got %g", s.Diameter)
	}
	want := []float64{0.001, -0.0002, 1e-05, -1e-06}
	if len(s.Coefficients) != len(want) {
		t.Fatalf("coefficients len: expected %d, got %d (vals=%v)", len(want), len(s.Coefficients), s.Coefficients)
	}
	for i := range want {
		if s.Coefficients[i] != want[i] {
			t.Errorf("coefficient[%d]: expected %g, got %g", i, want[i], s.Coefficients[i])
		}
	}
}

func TestZemax_DefaultWavelengthAndField(t *testing.T) {
	input := `SURF 0
  TYPE STANDARD
  CURV 0.0
  THIC INFINITY
SURF 1
  TYPE STANDARD
  CURV 0.02
  THIC 5.0
`
	result, err := ParseZemax(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Wavelengths) != 1 {
		t.Errorf("expected 1 default wavelength, got %d", len(result.Wavelengths))
	}
	if result.Wavelengths[0].Value != 0.00058756 {
		t.Errorf("default wavelength: expected 0.00058756, got %g", result.Wavelengths[0].Value)
	}
	if len(result.Fields) != 1 {
		t.Errorf("expected 1 default field, got %d", len(result.Fields))
	}
}
