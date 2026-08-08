package importer

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
)

func TestZemax_BasicTextFormat(t *testing.T) {
	input := `! Test
VERS 2023-01-01
MODE SEQ
NAME Test
UNIT MM
STOP 3
WAVL 0.58756
WAVL 0.48613
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
	input := `WAVL 0.58756
WAVL 0.48613
WAVL 0.65627
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

func TestZemax_WavelengthMicroToMilli(t *testing.T) {
	input := `WAVL 0.48613
WAVL 0.58756
WAVM 1 0.65627 1
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
	want := []float64{0.00048613, 0.00058756, 0.00065627}
	if len(result.Wavelengths) != len(want) {
		t.Fatalf("expected %d wavelengths, got %d", len(want), len(result.Wavelengths))
	}
	for i, w := range want {
		if math.Abs(result.Wavelengths[i].Value-w) > 1e-12 {
			t.Errorf("wavelength %d: expected %g, got %g", i, w, result.Wavelengths[i].Value)
		}
	}
}

func TestZemax_FieldTypeImageHeight(t *testing.T) {
	input := `FTYP 3 0 3 3 0 0 0
YFLN 0 15 21 0 0 0 0 0 0 0 0 0
XFLN 0 0 0 0 0 0 0 0 0 0 0 0
SURF 1
  TYPE STANDARD
  CURV 0.02
  THIC 5.0
`
	result, err := ParseZemax(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.FieldType != 3 {
		t.Fatalf("field type: expected 3, got %d", result.FieldType)
	}
	if len(result.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(result.Fields))
	}
	if result.Fields[0].ImageHeight != 15 || result.Fields[0].AngleDeg != 0 {
		t.Errorf("field 0: expected image height 15, got ih=%g angle=%g", result.Fields[0].ImageHeight, result.Fields[0].AngleDeg)
	}
	if result.Fields[1].ImageHeight != 21 {
		t.Errorf("field 1: expected image height 21, got %g", result.Fields[1].ImageHeight)
	}
}

func TestParseZemax_FieldTypeAngle(t *testing.T) {
	input := `FTYP 0 0 0 0 0 0 0
YFLN 0 15 21 0 0 0 0 0 0 0 0 0
SURF 1
  TYPE STANDARD
  CURV 0.02
  THIC 5.0
`
	result, err := ParseZemax(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Fields[0].AngleDeg != 15 || result.Fields[1].AngleDeg != 21 {
		t.Errorf("angle fields: expected 15/21, got %g/%g", result.Fields[0].AngleDeg, result.Fields[1].AngleDeg)
	}
}

func TestZemax_InlineModelGlass(t *testing.T) {
	input := `SURF 1
  TYPE STANDARD
  CURV 0.02
  THIC 5.0
  GLAS ___BLANK 1 0 1.76499 15.0 7.48E-1 0 0 0 0 0
`
	result, err := ParseZemax(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Surfaces[0].Material != "___BLANK" {
		t.Errorf("material: expected ___BLANK, got %q", result.Surfaces[0].Material)
	}
	var g *types.Glass
	for i := range result.GlassEntries {
		if result.GlassEntries[i].Label == "___BLANK" {
			g = &result.GlassEntries[i]
		}
	}
	if g == nil {
		t.Fatal("expected ___BLANK glass entry")
	}
	if math.Abs(g.ND-1.76499) > 1e-5 || math.Abs(g.VD-15.0) > 1e-5 {
		t.Errorf("inline nd/vd: expected 1.76499/15.0, got %g/%g", g.ND, g.VD)
	}
}

func TestZemax_MultiConfigOverrides(t *testing.T) {
	input := `STOP 1
YFL 0 15 21
SURF 1
  TYPE STANDARD
  CURV 0.02
  THIC 5.0
  GLAS BK7
  DIAM 20.0
SURF 2
  TYPE STANDARD
  CURV 0.0
  THIC 0.0
THIC 1 1 4.5 0 0 0 1 1 1 0 0
THIC 1 2 2.5 0 0 0 1 1 1 0 0
THIC 2 2 3.0 0 0 0 1 1 1 0 0
`
	result, err := ParseZemax(input)
	if err != nil {
		t.Fatal(err)
	}
	idx := ConfigIndexes(result)
	if len(idx) != 2 || idx[0] != 1 || idx[1] != 2 {
		t.Errorf("config indexes: expected [1 2], got %v", idx)
	}
	// Config 2 should override surface 1 thickness to 2.5 and surface 2 to 3.0.
	s := ConfigSurfaceSet(result, 2)
	if s[0].ID != 1 {
		t.Fatalf("unexpected first surface id %d", s[0].ID)
	}
	if math.Abs(s[0].Thickness-2.5) > 1e-9 || math.Abs(s[1].Thickness-3.0) > 1e-9 {
		t.Errorf("config 2 override: expected surf1=2.5 surf2=3.0, got %g/%g", s[0].Thickness, s[1].Thickness)
	}
	// Base (config 0) keeps the DISZ value.
	base := ConfigSurfaceSet(result, 0)
	if math.Abs(base[0].Thickness-5.0) > 1e-9 {
		t.Errorf("base config thickness: expected 5.0, got %g", base[0].Thickness)
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

func TestZemax_MirrorFold(t *testing.T) {
	// A two-mirror Cassegrain-like system: MIRROR material, negative spacing
	// after the first mirror, positive after the second.
	input := `SURF 1
  TYPE STANDARD
  CURV 0.0
  THIC 16.0
SURF 2
  TYPE STANDARD
  CURV -0.021872266966754158
  THIC -16.0
  GLAS MIRROR
SURF 3
  TYPE STANDARD
  CURV -0.052083333333333336
  THIC 24.035
  GLAS MIRROR
SURF 4
  TYPE STANDARD
  CURV 0.0
  THIC 0.0
`
	result, err := ParseZemax(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Surfaces) != 4 {
		t.Fatalf("expected 4 surfaces, got %d", len(result.Surfaces))
	}
	// First mirror: curvature flipped, thickness positive, fold decenter.
	m1 := result.Surfaces[1]
	if math.Abs(m1.Curvature-0.021872266966754158) > 1e-15 {
		t.Errorf("M1 curvature: expected +0.02187, got %g", m1.Curvature)
	}
	if m1.Thickness != 16.0 {
		t.Errorf("M1 thickness: expected +16, got %g", m1.Thickness)
	}
	if m1.Material != "AIR" {
		t.Errorf("M1 material: expected AIR, got %q", m1.Material)
	}
	if !m1.Reflects() {
		t.Error("M1 should Reflects()")
	}
	// Second mirror: curvature NOT flipped (2nd reflection).
	m2 := result.Surfaces[2]
	if math.Abs(m2.Curvature+0.052083333333333336) > 1e-15 {
		t.Errorf("M2 curvature: expected -0.05208 (not flipped), got %g", m2.Curvature)
	}
	if m2.Thickness != 24.035 {
		t.Errorf("M2 thickness: expected 24.035, got %g", m2.Thickness)
	}
	if !m2.Reflects() {
		t.Error("M2 should Reflects()")
	}
}

func TestZemax_CoordBreakTransfer(t *testing.T) {
	// A COORDBRK dummy with a tilt-about-X transform is removed and its
	// transform is transferred to the next real surface.
	input := `SURF 1
  TYPE STANDARD
  CURV 0.0
  THIC 70.0
SURF 2
  TYPE COORDBRK
  CURV 0.0
  PARM 1 0
  PARM 2 0
  PARM 3 16.44
  PARM 4 0
  PARM 5 0
  PARM 6 0
  THIC 0
SURF 3
  TYPE STANDARD
  CURV 0.006325110689437066
  THIC 0.0
  GLAS MIRROR
SURF 4
  TYPE STANDARD
  CURV 0.0
  THIC 0.0
`
	result, err := ParseZemax(input)
	if err != nil {
		t.Fatal(err)
	}
	// COORDBRK surface is removed: 3 real surfaces remain.
	if len(result.Surfaces) != 3 {
		t.Fatalf("expected 3 surfaces (COORDBRK removed), got %d", len(result.Surfaces))
	}
	// The COORDBRK thickness is folded into the preceding surface.
	if result.Surfaces[0].Thickness != 70.0 {
		t.Errorf("surface 1 thickness: expected 70, got %g", result.Surfaces[0].Thickness)
	}
	// The mirror receives the transferred tilt (16.44 about X) as its first
	// decenter step, followed by the fold.
	m := result.Surfaces[1]
	if len(m.Decenter) < 2 {
		t.Fatalf("mirror should have COORDBRK tilt + fold, got %d steps", len(m.Decenter))
	}
	if math.Abs(m.Decenter[0].Tilt.X-16.44) > 1e-9 {
		t.Errorf("COORDBRK tilt.X: expected 16.44, got %g", m.Decenter[0].Tilt.X)
	}
	if !m.Decenter[1].Scope.Bends() || m.Decenter[1] != (types.DecenterStep{Tilt: types.Vec3{Y: 180}, Scope: types.ScopeBoth}) {
		t.Error("mirror should have a fold step (scope: both)")
	}
}
