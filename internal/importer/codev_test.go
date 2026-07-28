package importer

import (
	"math"
	"os"
	"testing"
)

func parseSEQ(t *testing.T, path string) *ParseResult {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ParseCodeV(string(data))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestCodeV_KeywordFormat(t *testing.T) {
	result := parseSEQ(t, "../../samples/test_triplet.seq")

	if len(result.Surfaces) != 8 {
		t.Errorf("expected 8 surfaces, got %d", len(result.Surfaces))
	}
	if result.StopSurface != 5 {
		t.Errorf("expected stop surface 5, got %d", result.StopSurface)
	}
	if result.ImageSurface != 8 {
		t.Errorf("expected image surface 8, got %d", result.ImageSurface)
	}
	if len(result.Wavelengths) != 3 {
		t.Errorf("expected 3 wavelengths, got %d", len(result.Wavelengths))
	}
	// The keyword format file uses ANG/FIELD — since we removed that support,
	// expect default single field.
	if len(result.Fields) != 1 {
		t.Errorf("expected 1 default field, got %d", len(result.Fields))
	}
	wantWL := []float64{0.00058756, 0.00048613, 0.00065627}
	for i, wl := range result.Wavelengths {
		if math.Abs(wl.Value-wantWL[i]) > 1e-8 {
			t.Errorf("wavelength %d: expected %g, got %g", i, wantWL[i], wl.Value)
		}
	}
	if len(result.GlassEntries) != 2 {
		t.Errorf("expected 2 glass entries, got %d: %v", len(result.GlassEntries), result.GlassEntries)
	}
}

func TestCodeV_CompactFormat(t *testing.T) {
	result := parseSEQ(t, "../../US3524697_fisheye_init.seq")

	if len(result.Surfaces) != 18 {
		t.Errorf("expected 18 surfaces, got %d", len(result.Surfaces))
	}
	if result.StopSurface != 9 {
		t.Errorf("expected stop surface 9, got %d", result.StopSurface)
	}
	if result.ImageSurface != 18 {
		t.Errorf("expected image surface 18, got %d", result.ImageSurface)
	}
	if len(result.Wavelengths) != 1 {
		t.Errorf("expected 1 wavelength, got %d", len(result.Wavelengths))
	}
	if len(result.Fields) != 6 {
		t.Errorf("expected 6 fields, got %d", len(result.Fields))
	}
	if math.Abs(result.Wavelengths[0].Value-0.5875618) > 1e-10 {
		t.Errorf("wavelength: expected 0.5875618, got %g", result.Wavelengths[0].Value)
	}
	angles := []float64{0, 30, 45, 60, 90, 100}
	for i, f := range result.Fields {
		if math.Abs(f.AngleDeg-angles[i]) > 1e-10 {
			t.Errorf("field %d angle: expected %g, got %g", i, angles[i], f.AngleDeg)
		}
		if f.Weight != 1.0 {
			t.Errorf("field %d weight: expected 1.0, got %g", i, f.Weight)
		}
	}
	if len(result.GlassEntries) != 10 {
		t.Errorf("expected 10 glass entries, got %d", len(result.GlassEntries))
	}
}

func TestCodeV_DIMI(t *testing.T) {
	result := parseSEQ(t, "../../US3524697_fisheye_init.seq")
	s1 := result.Surfaces[0]
	s1Curv := s1.Curvature
	s1R := 0.0
	if s1Curv != 0 {
		s1R = 1.0 / s1Curv
	}
	expectedR := 88.722 * 25.4 // 2253.54 mm
	if math.Abs(s1R-expectedR)/expectedR > 0.001 {
		t.Errorf("surface 1 radius: expected %g mm, got %g mm (curv=%g)", expectedR, s1R, s1Curv)
	}
	expectedThick := 4.5 * 25.4 // 114.3 mm
	if math.Abs(s1.Thickness-expectedThick) > 0.1 {
		t.Errorf("surface 1 thickness: expected %g mm, got %g mm", expectedThick, s1.Thickness)
	}
}

func TestCodeV_CIRDiameter(t *testing.T) {
	result := parseSEQ(t, "../../US3524697_fisheye_init.seq")
	s5 := result.Surfaces[4]
	expectedDiam := 6.0 * 2 * 25.4 // 304.8 mm
	if math.Abs(s5.Diameter-expectedDiam) > 0.1 {
		t.Errorf("surface 5 diameter: expected %g mm, got %g mm", expectedDiam, s5.Diameter)
	}
	s9 := result.Surfaces[8]
	expectedDiam9 := 1.24695418336559 * 2 * 25.4 // ~63.345 mm
	if math.Abs(s9.Diameter-expectedDiam9) > 0.01 {
		t.Errorf("surface 9 diameter: expected %g mm, got %g mm", expectedDiam9, s9.Diameter)
	}
}

func TestCodeV_AsphereCoeffs(t *testing.T) {
	result := parseSEQ(t, "../../US3524697_fisheye_init.seq")
	s1 := result.Surfaces[0]
	if s1.Type != "asphere_polynomial" {
		t.Errorf("surface 1: expected asphere_polynomial, got %s", s1.Type)
	}
	if s1.Conic != 0 {
		t.Errorf("surface 1 conic: expected 0, got %g", s1.Conic)
	}
	if len(s1.Coefficients) != 0 {
		t.Errorf("surface 1: expected 0 coefficients (all zero), got %v", s1.Coefficients)
	}
	s8 := result.Surfaces[7]
	if s8.Type != "asphere_polynomial" {
		t.Errorf("surface 8: expected asphere_polynomial, got %s", s8.Type)
	}
}

func TestCodeV_AsphereCoeffsNonZero(t *testing.T) {
	input := `SEQ
S 100 5 1.5:60
  ASP
  K -1.5
  A 0.001; B -0.0002; C 1e-05; D -1e-06
  E 1e-07; F -1e-08; G 1e-09; H -1e-10; J 1e-11
END
`
	result, err := ParseCodeV(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Surfaces) != 1 {
		t.Fatalf("expected 1 surface, got %d", len(result.Surfaces))
	}
	s := result.Surfaces[0]
	if s.Type != "asphere_polynomial" {
		t.Errorf("expected asphere_polynomial, got %s", s.Type)
	}
	if s.Conic != -1.5 {
		t.Errorf("conic: expected -1.5, got %g", s.Conic)
	}
	// PolynomialAsphereSag indexing: coeffs[0]=r^4 (=A), coeffs[1]=r^6 (=B), ...
	// A=0.001 (r^4)→idx0, B=-0.0002(r^6)→idx1, C=1e-05(r^8)→idx2, D=-1e-06(r^10)→idx3,
	// E=1e-07(r^12)→idx4, F=-1e-08(r^14)→idx5, G=1e-09(r^16)→idx6, H=-1e-10(r^18)→idx7,
	// J=1e-11(r^20)→idx8
	want := []float64{0.001, -0.0002, 1e-05, -1e-06, 1e-07, -1e-08, 1e-09, -1e-10, 1e-11}
	if len(s.Coefficients) != len(want) {
		t.Fatalf("coefficients len: expected %d, got %d (vals=%v)", len(want), len(s.Coefficients), s.Coefficients)
	}
	for i := range want {
		if s.Coefficients[i] != want[i] {
			t.Errorf("coefficient[%d]: expected %g, got %g", i, want[i], s.Coefficients[i])
		}
	}
}

func TestCodeV_YANFields(t *testing.T) {
	input := `SEQ
YAN 0 10 20 30
WTF 1 0.5 0.5 0.5
S 50 2
S 0 10
END
`
	result, err := ParseCodeV(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Fields) != 4 {
		t.Fatalf("expected 4 fields, got %d", len(result.Fields))
	}
	angles := []float64{0, 10, 20, 30}
	weights := []float64{1, 0.5, 0.5, 0.5}
	for i, f := range result.Fields {
		if f.AngleDeg != angles[i] {
			t.Errorf("field %d angle: expected %g, got %g", i, angles[i], f.AngleDeg)
		}
		if f.Weight != weights[i] {
			t.Errorf("field %d weight: expected %g, got %g", i, weights[i], f.Weight)
		}
	}
}

func TestCodeV_SOSI(t *testing.T) {
	input := `SEQ
SO 0 1e10
S 100 5
S -50 10
SI 0 0
END
`
	result, err := ParseCodeV(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Surfaces) != 3 {
		t.Fatalf("expected 3 surfaces, got %d", len(result.Surfaces))
	}
	if result.Surfaces[0].ID != 1 {
		t.Errorf("surface 0 id: expected 1, got %d", result.Surfaces[0].ID)
	}
	if result.Surfaces[2].ID != 3 {
		t.Errorf("surface 2 id: expected 3, got %d", result.Surfaces[2].ID)
	}
	if result.ImageSurface != 3 {
		t.Errorf("image surface: expected 3, got %d", result.ImageSurface)
	}
}

func TestCodeV_InlineGlassNDVD(t *testing.T) {
	input := `SEQ
S 100 5 1.5:60
S 50 3 1.7:30
END
`
	result, err := ParseCodeV(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.GlassEntries) != 2 {
		t.Fatalf("expected 2 glass entries, got %d", len(result.GlassEntries))
	}
	if result.GlassEntries[0].ND != 1.5 {
		t.Errorf("glass 0 nd: expected 1.5, got %g", result.GlassEntries[0].ND)
	}
	if result.GlassEntries[0].VD != 60 {
		t.Errorf("glass 0 vd: expected 60, got %g", result.GlassEntries[0].VD)
	}
	if result.GlassEntries[1].ND != 1.7 {
		t.Errorf("glass 1 nd: expected 1.7, got %g", result.GlassEntries[1].ND)
	}
	if result.GlassEntries[1].VD != 30 {
		t.Errorf("glass 1 vd: expected 30, got %g", result.GlassEntries[1].VD)
	}
}

func TestCodeV_PIM(t *testing.T) {
	input := `SEQ
S 100 5
S -50 20
  PIM
SI 0 0
END
`
	result, err := ParseCodeV(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Surfaces) != 3 {
		t.Fatalf("expected 3 surfaces, got %d", len(result.Surfaces))
	}
	// PIM surface should exist (just a flag, no structural change)
	if result.Surfaces[1].ID != 2 {
		t.Errorf("surface 1 id: expected 2, got %d", result.Surfaces[1].ID)
	}
}

func TestCodeV_WTW(t *testing.T) {
	input := `SEQ
WL 0.58756 0.48613
WTW 1.0 0.8
S 100 5
SI 0 0
END
`
	result, err := ParseCodeV(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Wavelengths) != 2 {
		t.Fatalf("expected 2 wavelengths, got %d", len(result.Wavelengths))
	}
	if result.Wavelengths[0].Weight != 1.0 {
		t.Errorf("wl 0 weight: expected 1.0, got %g", result.Wavelengths[0].Weight)
	}
	if result.Wavelengths[1].Weight != 0.8 {
		t.Errorf("wl 1 weight: expected 0.8, got %g", result.Wavelengths[1].Weight)
	}
}

func TestCodeV_LineContinuation(t *testing.T) {
	input := `SEQ
YAN 0 10 20
S 100 5
S -50 3
CIR 6
S 200 2
SI 0 0
END
`
	result, err := ParseCodeV(input)
	if err != nil {
		t.Fatal(err)
	}
	_ = result
}

func TestCodeV_NoSEQ(t *testing.T) {
	input := `SO 0 1e10
S 100 5
S -50 10
SI 0 0
`
	result, err := ParseCodeV(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Surfaces) != 3 {
		t.Fatalf("expected 3 surfaces, got %d", len(result.Surfaces))
	}
}

func TestCodeV_Defaults(t *testing.T) {
	input := `SEQ
S 100 5
END
`
	result, err := ParseCodeV(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Wavelengths) == 0 {
		t.Error("expected default wavelength")
	}
	if result.Wavelengths[0].Value != 0.00058756 {
		t.Errorf("default wavelength: expected 0.00058756, got %g", result.Wavelengths[0].Value)
	}
	if len(result.Fields) == 0 {
		t.Error("expected default field")
	}
	if result.Fields[0].AngleDeg != 0 {
		t.Errorf("default field angle: expected 0, got %g", result.Fields[0].AngleDeg)
	}
}

func TestCodeV_ASPBlockAlphaCoeffsSemicoLon(t *testing.T) {
	input := `SEQ
S 100 5 1.5:60
  ASP
  K -1.0
  A 1e-4; B 2e-5; C 3e-6
END
`
	result, err := ParseCodeV(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Surfaces) != 1 {
		t.Fatalf("expected 1 surface, got %d", len(result.Surfaces))
	}
	s := result.Surfaces[0]
	if s.Type != "asphere_polynomial" {
		t.Errorf("expected asphere_polynomial, got %s", s.Type)
	}
	if s.Conic != -1.0 {
		t.Errorf("conic: expected -1.0, got %g", s.Conic)
	}
	// A=1e-4(r^4)→idx0, B=2e-5(r^6)→idx1, C=3e-6(r^8)→idx2
	want := []float64{1e-4, 2e-5, 3e-6}
	if len(s.Coefficients) != len(want) {
		t.Fatalf("coefficients len: expected %d, got %d (vals=%v)", len(want), len(s.Coefficients), s.Coefficients)
	}
	for i := range want {
		if s.Coefficients[i] != want[i] {
			t.Errorf("coeff[%d]: expected %g, got %g", i, want[i], s.Coefficients[i])
		}
	}
}
