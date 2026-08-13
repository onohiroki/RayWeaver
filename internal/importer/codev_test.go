package importer

import (
	"math"
	"os"
	"strings"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/types"
)

func parseSEQ(t *testing.T, path string) *ParseResult {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Fixtures are gitignored (*.seq) and may be absent on a fresh
			// checkout; skip instead of failing.
			t.Skipf("fixture %s not present (gitignored); skipping", path)
		}
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
	if math.Abs(result.Wavelengths[0].Value-0.0005875618) > 1e-12 {
		t.Errorf("wavelength: expected 0.0005875618 (587.5618 nm), got %g", result.Wavelengths[0].Value)
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

func TestCodeV_YIMFields(t *testing.T) {
	input := `SEQ
YIM 0.0 1.1203446 1.8544681
WTF 1.0 0.8 0.6
S 50 2
SI 0 0
END
`
	result, err := ParseCodeV(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Fields) != 3 {
		t.Fatalf("expected 3 fields, got %d", len(result.Fields))
	}
	want := []float64{0, 1.1203446, 1.8544681}
	weights := []float64{1.0, 0.8, 0.6}
	for i, f := range result.Fields {
		if f.ImageHeight != want[i] {
			t.Errorf("field %d image_height: expected %g, got %g", i, want[i], f.ImageHeight)
		}
		if f.AngleDeg != 0 {
			t.Errorf("field %d angle_deg: expected 0, got %g", i, f.AngleDeg)
		}
		if f.Weight != weights[i] {
			t.Errorf("field %d weight: expected %g, got %g", i, weights[i], f.Weight)
		}
	}
}

func TestCodeV_YRIWithDefaultField(t *testing.T) {
	// When only YRI is present (no YAN/YIM), fields should still be created
	input := `SEQ
YRI 5.0 10.0
S 50 2
SI 0 0
END
`
	result, err := ParseCodeV(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(result.Fields))
	}
	if result.Fields[0].ImageHeight != 5.0 {
		t.Errorf("field 0 image_height: expected 5.0, got %g", result.Fields[0].ImageHeight)
	}
	if result.Fields[1].ImageHeight != 10.0 {
		t.Errorf("field 1 image_height: expected 10.0, got %g", result.Fields[1].ImageHeight)
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

func TestCodeV_LowercaseKeywords(t *testing.T) {
	input := `tit 'singlet'
dim m
so 0. 100.
s 0.1 1 nbk7_schott
s -.05 15
sto
s 0.02 2
si 0 0
`
	result, err := ParseCodeV(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Surfaces) != 4 {
		t.Fatalf("expected 4 surfaces, got %d", len(result.Surfaces))
	}
	if result.Surfaces[0].Thickness != 1 {
		t.Errorf("surface 1 thickness: expected 1, got %g", result.Surfaces[0].Thickness)
	}
	if result.Surfaces[0].Material.Key != "nbk7_schott" {
		t.Errorf("surface 1 material: expected nbk7_schott (case kept), got %q", result.Surfaces[0].Material.String())
	}
	// Lowercase "sto" marks the current surface as the stop.
	if result.StopSurface != 2 {
		t.Errorf("stop surface: expected 2, got %d", result.StopSurface)
	}
}

func TestCodeV_EPDDiameter(t *testing.T) {
	input := `SEQ
EPD 50.0
S 100 5 1.5:60
S -50 10
STO
S 200 2
SI 0 0
END
`
	result, err := ParseCodeV(input)
	if err != nil {
		t.Fatal(err)
	}
	// The stop surface gets the EPD as its aperture when no diameter is given.
	if result.StopSurface != 2 {
		t.Fatalf("stop surface: expected 2, got %d", result.StopSurface)
	}
	if result.EntrancePupilDiameter != 50.0 {
		t.Errorf("entrance pupil diameter: expected 50.0, got %g", result.EntrancePupilDiameter)
	}
	if result.Surfaces[1].Diameter != 50.0 {
		t.Errorf("stop surface diameter: expected 50.0, got %g", result.Surfaces[1].Diameter)
	}
}

func TestCodeV_EPDNotOverrideExplicitAperture(t *testing.T) {
	input := `SEQ
EPD 50.0
S 100 5 1.5:60
  CIR 20.0
STO
S 200 2
SI 0 0
END
`
	result, err := ParseCodeV(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Surfaces[0].Diameter != 40.0 {
		t.Errorf("explicit CIR aperture overwritten by EPD: got %g", result.Surfaces[0].Diameter)
	}
}

func TestCodeV_CIREDGDoesNotClobberClearAperture(t *testing.T) {
	input := `SEQ
S 100 5 1.5:60
  CIR 6.0
  CIR EDG 7.0
S -50 10
SI 0 0
END
`
	result, err := ParseCodeV(input)
	if err != nil {
		t.Fatal(err)
	}
	// The clear CIR wins; EDG (edge) is a fallback that cannot zero it.
	if result.Surfaces[0].Diameter != 12.0 {
		t.Errorf("surface 1 diameter: expected 12.0 (clear CIR), got %g", result.Surfaces[0].Diameter)
	}
}

func TestCodeV_PRVTubulatedGlass(t *testing.T) {
	input := `RDM;LEN "test"
PRV
  PWL 656.3 587.6 486.1
  'XYZ' 1.50 1.51 1.52
END
SO 0.0 0.1e14
S 100 5 'XYZ'
SI 0 0
`
	result, err := ParseCodeV(input)
	if err != nil {
		t.Fatal(err)
	}
	var found *types.Glass
	for i := range result.GlassEntries {
		if result.GlassEntries[i].Label == "XYZ" {
			found = &result.GlassEntries[i]
			break
		}
	}
	if found == nil {
		t.Fatal("XYZ glass not registered")
	}
	if found.Type != types.GlassTypeTabulated {
		t.Errorf("XYZ type: expected tabulated, got %s", found.Type)
	}
	if len(found.RefractiveIndices) != 3 {
		t.Fatalf("XYZ table len: expected 3, got %d", len(found.RefractiveIndices))
	}
	// PWL values (nm) become millimetres in the table (internal ray unit).
	if found.RefractiveIndices[1].Wavelength != 0.0005876 {
		t.Errorf("table wavelength 1: expected 0.0005876, got %g", found.RefractiveIndices[1].Wavelength)
	}
	if found.RefractiveIndices[1].Value != 1.51 {
		t.Errorf("table index 1: expected 1.51, got %g", found.RefractiveIndices[1].Value)
	}
}

func TestCodeV_PRVAfterSEQ(t *testing.T) {
	input := `SEQ
PRV
  PWL 656.3 587.6 486.1
  'XYZ' 1.50 1.51 1.52
END
S 100 5 'XYZ'
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
	var found *types.Glass
	for i := range result.GlassEntries {
		if result.GlassEntries[i].Label == "XYZ" {
			found = &result.GlassEntries[i]
			break
		}
	}
	if found == nil {
		t.Fatal("XYZ glass not registered from a PRV block after the SEQ keyword")
	}
	if found.Type != types.GlassTypeTabulated {
		t.Errorf("XYZ type: expected tabulated, got %s", found.Type)
	}
}

func TestCodeV_PRVIndex(t *testing.T) {
	input := `SEQ
PRV
  PWL 656.3 587.6 486.1
  'XYZ' 1.50 1.51 1.52
END
S 100 5 'XYZ'
S -50 10
SI 0 0
END
`
	result, err := ParseCodeV(input)
	if err != nil {
		t.Fatal(err)
	}
	var found *types.Glass
	for i := range result.GlassEntries {
		if result.GlassEntries[i].Label == "XYZ" {
			found = &result.GlassEntries[i]
			break
		}
	}
	if found == nil {
		t.Fatal("XYZ glass not registered")
	}
	// At 587.6nm (0.0005876 mm) the tabulated value is an exact knot: 1.51.
	n, err := glass.CalcRefractiveIndex(found, 0.0005876)
	if err != nil {
		t.Fatalf("CalcRefractiveIndex: %v", err)
	}
	if math.Abs(n-1.51) > 1e-6 {
		t.Errorf("XYZ index at 587.6nm: expected 1.51, got %g", n)
	}
	// A wavelength between knots interpolates (656.3..587.6 -> ~1.505).
	nMid, err := glass.CalcRefractiveIndex(found, 0.00062)
	if err != nil {
		t.Fatalf("CalcRefractiveIndex mid: %v", err)
	}
	if nMid < 1.50 || nMid > 1.51 {
		t.Errorf("XYZ index at 620nm: expected between 1.50 and 1.51, got %g", nMid)
	}
}

func TestCodeV_PRVFormulaGlass(t *testing.T) {
	input := `SEQ
PRV
  PWL 550.0
  'NOA61' LAU 2.36390625 0.0 0.025493134 -0.000580235 -3.49933e-6 4.45404e-8
END
S 100 5 'NOA61'
S -50 10
SI 0 0
END
`
	result, err := ParseCodeV(input)
	if err != nil {
		t.Fatal(err)
	}
	var found *types.Glass
	for i := range result.GlassEntries {
		if result.GlassEntries[i].Label == "NOA61" {
			found = &result.GlassEntries[i]
			break
		}
	}
	if found == nil {
		t.Fatal("NOA61 formula glass not registered")
	}
	if found.DispersionFormula != types.Laurent {
		t.Errorf("NOA61 formula: expected laurent, got %s", found.DispersionFormula)
	}
	if len(found.Coefficients) != 6 {
		t.Fatalf("NOA61 coefficients: expected 6, got %d", len(found.Coefficients))
	}
	// The Laurent formula evaluates to n ≈ 1.56 at 587.6 nm (0.0005876 mm).
	n, err := glass.CalcRefractiveIndex(found, 0.0005876)
	if err != nil {
		t.Fatalf("CalcRefractiveIndex: %v", err)
	}
	if math.Abs(n-1.5597) > 0.001 {
		t.Errorf("NOA61 index at 587.6nm: expected ~1.5597, got %g", n)
	}
}

func TestCodeV_PRVFormulaTypes(t *testing.T) {
	tests := []struct {
		name     string
		kw       string
		expected types.DispersionFormula
	}{
		{"SLM", "SLM", types.Sellmeier1},
		{"GMS", "GMS", types.Sellmeier1},
		{"CAU", "CAU", types.Cauchy},
		{"HAR", "HAR", types.Hartmann},
		{"LAU", "LAU", types.Laurent},
		{"GML", "GML", types.Laurent},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			formula, ok := codeVFormulaType(tc.kw)
			if !ok {
				t.Fatalf("codeVFormulaType(%s): not recognised", tc.kw)
			}
			if formula != tc.expected {
				t.Errorf("codeVFormulaType(%s): got %s, want %s", tc.kw, formula, tc.expected)
			}
		})
	}
	if _, ok := codeVFormulaType("BOGUS"); ok {
		t.Error("codeVFormulaType(BOGUS): expected not recognised")
	}
}

// parseCodeVFormulaGlass runs a minimal CODE V SEQ with a single PRV formula
// glass and returns the registered Glass entry (registered and used as "FGL").
func parseCodeVFormulaGlass(t *testing.T, kw string, coeffs []string) *types.Glass {
	t.Helper()
	var buf strings.Builder
	buf.WriteString("SEQ\nPRV\n  PWL 550.0\n  'FGL' ")
	buf.WriteString(kw)
	for _, c := range coeffs {
		buf.WriteString(" " + c)
	}
	buf.WriteString("\nEND\nS 100 5 'FGL'\nS -50 10\nSI 0 0\nEND\n")
	result, err := ParseCodeV(buf.String())
	if err != nil {
		t.Fatalf("ParseCodeV: %v", err)
	}
	for i := range result.GlassEntries {
		if result.GlassEntries[i].Label == "FGL" {
			return &result.GlassEntries[i]
		}
	}
	t.Fatal("FGL formula glass not registered")
	return nil
}

func TestCodeV_PRVFormulaSellmeier(t *testing.T) {
	// N-BK7-style Sellmeier coefficients (µm): B1 C1 B2 C2 B3 C3.
	g := parseCodeVFormulaGlass(t, "SLM", []string{
		"1.03961212", "0.00600069867", "0.231792344", "0.0200179144", "1.01046945", "103.560653",
	})
	if g.DispersionFormula != types.Sellmeier1 {
		t.Errorf("expected sellmeier_1, got %s", g.DispersionFormula)
	}
	n, err := glass.CalcRefractiveIndex(g, 0.0005876)
	if err != nil {
		t.Fatalf("CalcRefractiveIndex: %v", err)
	}
	if math.Abs(n-1.5168) > 0.002 {
		t.Errorf("Sellmeier index at 587.6nm: expected ~1.5168, got %g", n)
	}
}

func TestCodeV_PRVFormulaCauchy(t *testing.T) {
	g := parseCodeVFormulaGlass(t, "CAU", []string{"1.50", "0.004", "-2e-6"})
	if g.DispersionFormula != types.Cauchy {
		t.Errorf("expected cauchy, got %s", g.DispersionFormula)
	}
	lambdaMM := 0.0005876
	lambda := lambdaMM * 1000
	lsq := lambda * lambda
	want := 1.50 + 0.004/lsq - 2e-6/(lsq*lsq)
	n, err := glass.CalcRefractiveIndex(g, lambdaMM)
	if err != nil {
		t.Fatalf("CalcRefractiveIndex: %v", err)
	}
	if math.Abs(n-want) > 1e-9 {
		t.Errorf("Cauchy index at 587.6nm: got %g, want %g", n, want)
	}
}

func TestCodeV_PRVFormulaHartmann(t *testing.T) {
	g := parseCodeVFormulaGlass(t, "HAR", []string{"1.50", "0.004", "0.12"})
	if g.DispersionFormula != types.Hartmann {
		t.Errorf("expected hartmann, got %s", g.DispersionFormula)
	}
	lambdaMM := 0.0005876
	lambda := lambdaMM * 1000
	want := 1.50 + 0.004/(lambda-0.12)
	n, err := glass.CalcRefractiveIndex(g, lambdaMM)
	if err != nil {
		t.Fatalf("CalcRefractiveIndex: %v", err)
	}
	if math.Abs(n-want) > 1e-9 {
		t.Errorf("Hartmann index at 587.6nm: got %g, want %g", n, want)
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

func TestCodeV_CONKConic(t *testing.T) {
	input := `SEQ
S -1058.98937 -276.006853 REFL
CON
K -1.314566
S 0 -0.981318
CON
K 0.889123
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
	// The CON marker + separate K line sets the conic on the current surface.
	if math.Abs(result.Surfaces[0].Conic+1.314566) > 1e-9 {
		t.Errorf("surface 1 conic: expected -1.314566, got %g", result.Surfaces[0].Conic)
	}
	if math.Abs(result.Surfaces[1].Conic-0.889123) > 1e-9 {
		t.Errorf("surface 2 conic: expected 0.889123, got %g", result.Surfaces[1].Conic)
	}
}

func TestCodeV_CONKConicSemicolon(t *testing.T) {
	input := `SEQ
S -1058.98937 -276.006853 REFL
K 0.226106; A 0.368950E-10
SI 0 0
END
`
	result, err := ParseCodeV(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Surfaces) != 2 {
		t.Fatalf("expected 2 surfaces, got %d", len(result.Surfaces))
	}
	s := result.Surfaces[0]
	if math.Abs(s.Conic-0.226106) > 1e-9 {
		t.Errorf("surface 1 conic: expected 0.226106, got %g", s.Conic)
	}
	// The asphere letter after the ';' is captured too (r^4 term, idx 0).
	if len(s.Coefficients) != 1 || math.Abs(s.Coefficients[0]-0.368950e-10) > 1e-18 {
		t.Errorf("surface 1 coeff[0]: expected 0.368950E-10, got %v", s.Coefficients)
	}
}

func TestCodeV_CONValueOnLine(t *testing.T) {
	input := `SEQ
S -1058.98937 -276.006853 REFL
CON -1.0
SI 0 0
END
`
	result, err := ParseCodeV(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Surfaces) != 2 {
		t.Fatalf("expected 2 surfaces, got %d", len(result.Surfaces))
	}
	if math.Abs(result.Surfaces[0].Conic+1.0) > 1e-9 {
		t.Errorf("surface 1 conic: expected -1.0, got %g", result.Surfaces[0].Conic)
	}
}

func TestCodeV_REFPrimary(t *testing.T) {
	input := `SEQ
WL 656.3 587.6 486.1
REF 2
S 100 5
SI 0 0
END
`
	result, err := ParseCodeV(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Wavelengths) != 3 {
		t.Fatalf("expected 3 wavelengths, got %d", len(result.Wavelengths))
	}
	// REF 2 (1-based) marks the 587.6nm entry (0-based index 1) as primary.
	if result.Wavelengths[0].Primary {
		t.Error("wavelength 0 should not be primary")
	}
	if !result.Wavelengths[1].Primary {
		t.Error("wavelength 1 (587.6nm) should be primary via REF 2")
	}
	if result.Wavelengths[2].Primary {
		t.Error("wavelength 2 should not be primary")
	}
	if result.ReferenceWavelengthIdx != 1 {
		t.Errorf("reference wavelength idx: expected 1, got %d", result.ReferenceWavelengthIdx)
	}
}

func TestCodeV_REFBeforeWL(t *testing.T) {
	input := `SEQ
REF 3
WL 656.3 587.6 486.1
S 100 5
SI 0 0
END
`
	result, err := ParseCodeV(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Wavelengths) != 3 {
		t.Fatalf("expected 3 wavelengths, got %d", len(result.Wavelengths))
	}
	if !result.Wavelengths[2].Primary {
		t.Error("wavelength 2 should be primary even when REF precedes WL")
	}
}

func TestCodeV_REFNoWL(t *testing.T) {
	input := `SEQ
REF 2
S 100 5
SI 0 0
END
`
	result, err := ParseCodeV(input)
	if err != nil {
		t.Fatal(err)
	}
	// With no WL list, only the default single wavelength exists; REF is
	// out of range and must be ignored without error.
	if len(result.Wavelengths) != 1 {
		t.Fatalf("expected 1 default wavelength, got %d", len(result.Wavelengths))
	}
	if result.Wavelengths[0].Primary {
		t.Error("default wavelength must not be marked primary")
	}
}

func TestCodeV_REFKeywordNotClobberedByAsp(t *testing.T) {
	input := `SEQ
S 100 5 1.5:60
  ASP
  K -1.5
SI 0 0
END
`
	result, err := ParseCodeV(input)
	if err != nil {
		t.Fatal(err)
	}
	// The asp-block K handling must still work unchanged.
	if len(result.Surfaces) != 2 {
		t.Fatalf("expected 2 surfaces, got %d", len(result.Surfaces))
	}
	if math.Abs(result.Surfaces[0].Conic+1.5) > 1e-9 {
		t.Errorf("surface 1 conic: expected -1.5, got %g", result.Surfaces[0].Conic)
	}
}

// parseRDMSEQ parses a minimal two-surface lens whose surface rows are written
// in a given RDM mode and returns the curvature of the first surface.
func parseRDMSEQ(t *testing.T, rdmLine string, s1 string) (float64, error) {
	t.Helper()
	input := "SEQ\n" + rdmLine + "\nSO 0 100\nS " + s1 + " nbk7_schott\nS -0.05 15\nSI 0 0\nEND\n"
	result, err := ParseCodeV(input)
	if err != nil {
		return 0, err
	}
	return result.Surfaces[0].Curvature, nil
}

func TestCodeV_RDMDefaultIsCurvature(t *testing.T) {
	// RDM absent → curvature mode: the surface value is used directly.
	c, err := parseRDMSEQ(t, "", "0.1 1")
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(c-0.1) > 1e-12 {
		t.Errorf("RDM-absent curvature: expected 0.1 (curvature mode), got %g", c)
	}
}

func TestCodeV_RDMNIsCurvature(t *testing.T) {
	// "RDM N" selects curvature mode (CODE V 10.x/11.x export header).
	for _, rdm := range []string{"RDM N", "rdm n", "RDM NO", "RDM N;LEN \"VERSION: 10.7\""} {
		c, err := parseRDMSEQ(t, rdm, "0.01779 1")
		if err != nil {
			t.Fatalf("%s: %v", rdm, err)
		}
		if math.Abs(c-0.01779) > 1e-9 {
			t.Errorf("%s: expected curvature 0.01779, got %g", rdm, c)
		}
	}
}

func TestCodeV_RDMYIsRadius(t *testing.T) {
	// "RDM Y" (and bare "RDM") select radius mode: value converted via 1/r.
	for _, rdm := range []string{"RDM Y", "rdm y", "RDM", "RDM;LEN \"VERSION: 11.2\""} {
		c, err := parseRDMSEQ(t, rdm, "61.47 6")
		if err != nil {
			t.Fatalf("%s: %v", rdm, err)
		}
		want := 1.0 / 61.47
		if math.Abs(c-want) > 1e-12 {
			t.Errorf("%s: expected radius 61.47 → curvature %g, got %g", rdm, want, c)
		}
	}
}

func TestCodeV_RDMSurfaceKeywordUnaffected(t *testing.T) {
	// "RDY <surf> <radius>" (and the RD/RDM keyword forms) always insert a
	// radius regardless of the entry mode; curvature mode must not change them.
	input := `SEQ
RDM N
SO 0 100
S 0 1 nbk7_schott
S -0.05 15
RDY 1 61.47
SI 0 0
END
`
	result, err := ParseCodeV(input)
	if err != nil {
		t.Fatal(err)
	}
	want := 1.0 / 61.47
	if math.Abs(result.Surfaces[0].Curvature-want) > 1e-12 {
		t.Errorf("RDY surface radius: expected curvature %g, got %g", want, result.Surfaces[0].Curvature)
	}
}

func TestCodeV_RDMSingletFocalPositive(t *testing.T) {
	// The singlet sample (RDM absent, S values 0.1 / -0.05) must read as a
	// curvature-mode biconvex lens with a positive EFL, so FNO→EPD sizing and
	// the chief grid resolve.
	input := `SEQ
SO 0 100
S 0.1 1 nbk7_schott
S -0.05 15
SI 0 0
FNO 5.0
WL 650 550 450
END
`
	result, err := ParseCodeV(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Surfaces) < 2 {
		t.Fatalf("expected ≥2 surfaces, got %d", len(result.Surfaces))
	}
	if math.Abs(result.Surfaces[0].Curvature-0.1) > 1e-12 {
		t.Errorf("surface 1: expected curvature 0.1, got %g", result.Surfaces[0].Curvature)
	}
	if math.Abs(result.Surfaces[1].Curvature+0.05) > 1e-12 {
		t.Errorf("surface 2: expected curvature -0.05, got %g", result.Surfaces[1].Curvature)
	}
	if result.FNO != 5.0 {
		t.Errorf("FNO: expected 5, got %g", result.FNO)
	}
}

// TestCodeV_SchmidtFold verifies the Schmidt-camera mirror system: a REFL
// surface with a negative spacing is converted into a fold decenter step, the
// mirror and following surfaces get their curvature sign flipped (odd
// reflection count), and all thicknesses become positive.
func TestCodeV_SchmidtFold(t *testing.T) {
	input := `RDM N;LEN "VERSION: 10.7"
SEQ
SO 0 100
S 0.000562598189395594 6.30393959108 569.631
S 0.0 170.946831705
S -0.004721047778140267 -96.2601569305 REFL
S -0.02441573206343559 -5.94371447159 569.631
S 0.0 -1.731355379851178
SI 0.0 0.0
END
`
	result, err := ParseCodeV(input)
	if err != nil {
		t.Fatal(err)
	}
	// Corrector and air space keep their signs.
	if math.Abs(result.Surfaces[0].Curvature-0.000562598189395594) > 1e-15 {
		t.Errorf("corrector curvature changed: %g", result.Surfaces[0].Curvature)
	}
	// Mirror: curvature flipped, thickness positive, fold decenter, material AIR.
	m := result.Surfaces[2]
	if math.Abs(m.Curvature-0.004721047778140267) > 1e-15 {
		t.Errorf("mirror curvature: expected +0.004721, got %g", m.Curvature)
	}
	if m.Thickness != 96.2601569305 {
		t.Errorf("mirror thickness: expected +96.26, got %g", m.Thickness)
	}
	if !m.Material.IsAir() {
		t.Errorf("mirror material: expected AIR, got %q", m.Material.String())
	}
	if !m.Reflects() {
		t.Error("mirror should Reflects()")
	}
	// Field lens after mirror: curvature flipped too.
	if math.Abs(result.Surfaces[3].Curvature-0.02441573206343559) > 1e-15 {
		t.Errorf("field lens curvature: expected +0.0244, got %g", result.Surfaces[3].Curvature)
	}
	// All thicknesses positive.
	for _, s := range result.Surfaces {
		if s.Thickness < 0 {
			t.Errorf("surface %d: negative thickness after fold: %g", s.ID, s.Thickness)
		}
	}
}

// TestCodeV_ThreeMirrorFolds verifies curvature sign alternation across three
// mirrors: flip on the 1st, keep on the 2nd, flip on the 3rd.
func TestCodeV_ThreeMirrorFolds(t *testing.T) {
	input := `SEQ
SO 0 100
S 0 300.000000
S -1058.98937 -276.006853 REFL
CON
K -1.314566
S 0 -0.981318
S -324.06500 319.160560 REFL
CON
K 0.889123
S -453.96516 -321.621031 REFL
ASP
K 0.226106; A 0.368950E-10
SI 0 15.407921
END
`
	result, err := ParseCodeV(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Surfaces) != 6 {
		t.Fatalf("expected 6 surfaces, got %d", len(result.Surfaces))
	}
	// M1: flipped.
	if !result.Surfaces[1].Reflects() {
		t.Error("M1 should Reflects()")
	}
	if result.Surfaces[1].Curvature <= 0 {
		t.Errorf("M1 curvature: expected positive (flipped), got %g", result.Surfaces[1].Curvature)
	}
	// M2: not flipped (second reflection).
	if !result.Surfaces[3].Reflects() {
		t.Error("M2 should Reflects()")
	}
	if result.Surfaces[3].Curvature >= 0 {
		t.Errorf("M2 curvature: expected negative (not flipped), got %g", result.Surfaces[3].Curvature)
	}
	// M3: flipped.
	if !result.Surfaces[4].Reflects() {
		t.Error("M3 should Reflects()")
	}
	if result.Surfaces[4].Curvature <= 0 {
		t.Errorf("M3 curvature: expected positive (flipped), got %g", result.Surfaces[4].Curvature)
	}
	// All thicknesses positive.
	for _, s := range result.Surfaces {
		if s.Thickness < 0 {
			t.Errorf("surface %d: negative thickness after fold: %g", s.ID, s.Thickness)
		}
	}
}

// TestCodeV_DARDecenter verifies that CODE V DAR/YDE/ADE statements become a
// per-surface DecenterStep on the preceding surface.
func TestCodeV_DARDecenter(t *testing.T) {
	input := `SEQ
SO 0 100
S -1058.98937 -276.006853 REFL
DAR
YDE 48.695345; ADE 8.438645
SI 0 0
END
`
	result, err := ParseCodeV(input)
	if err != nil {
		t.Fatal(err)
	}
	m := result.Surfaces[0]
	if len(m.Decenter) == 0 {
		t.Fatal("mirror should carry a decenter")
	}
	// First step: DAR decenter (shift Y, tilt X).
	if math.Abs(m.Decenter[0].Shift.Y-48.695345) > 1e-9 {
		t.Errorf("DAR shift.Y: expected 48.695345, got %g", m.Decenter[0].Shift.Y)
	}
	if math.Abs(m.Decenter[0].Tilt.X-8.438645) > 1e-9 {
		t.Errorf("DAR tilt.X: expected 8.438645, got %g", m.Decenter[0].Tilt.X)
	}
	// Second step: the mirror fold (scope: both) after the DAR decenter; the
	// surface itself is flagged `reflect: true`.
	if len(m.Decenter) < 2 || !m.Decenter[1].Scope.Bends() || m.Decenter[1] != (types.DecenterStep{Tilt: types.Vec3{Y: 180}, Scope: types.ScopeBoth}) {
		t.Error("mirror should have a fold step (scope: both) after the DAR decenter")
	}
	if !m.Reflects() {
		t.Error("DAR mirror should Reflects()")
	}
}

func TestCodeV_CCYIgnored(t *testing.T) {
	// CCY is a variable/control-designation keyword in CODE V, not the conic
	// constant (K is). A "CCY <value>" line must not set the conic.
	input := `SEQ
S -1058.98937 -276.006853 REFL
CCY 0.5
SI 0 0
END
`
	result, err := ParseCodeV(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Surfaces) != 2 {
		t.Fatalf("expected 2 surfaces, got %d", len(result.Surfaces))
	}
	if result.Surfaces[0].Conic != 0 {
		t.Errorf("CCY must not set the conic constant: got %g", result.Surfaces[0].Conic)
	}
	// And the compact-mode statement walker must ignore CCY too.
	input = `SEQ
S 0.02 5.0
CCY 0.3; THC 0
SI 0 0
END
`
	result, err = ParseCodeV(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Surfaces[0].Conic != 0 {
		t.Errorf("compact CCY must not set the conic constant: got %g", result.Surfaces[0].Conic)
	}
}

func TestCodeV_VignettingRows(t *testing.T) {
	input := `SEQ
YAN 0 30
WTF 1 1
VUX -0.1 0.0
VLX -0.1 0.0
VUY 0.1 0.0
VLY 0.05 0.0
S 100 5
SI 0 0
END
`
	result, err := ParseCodeV(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(result.Fields))
	}
	// Field 1: VUY=0.1, VLY=0.05 -> decenterY=(0.05-0.1)/2=-0.025,
	// compressionY=(0.1+0.05)/2=0.075; VUX=VLX=-0.1 -> decenterX=0,
	// compressionX=-0.1.
	f0 := result.Fields[0].Vignetting
	if f0 == nil {
		t.Fatal("field 1 should carry vignetting")
	}
	if math.Abs(f0.DecenterY+0.025) > 1e-12 || math.Abs(f0.CompressionY-0.075) > 1e-12 {
		t.Errorf("field 1 Y: expected decenter=-0.025 compression=0.075, got %+v", f0)
	}
	if f0.DecenterX != 0 || math.Abs(f0.CompressionX+0.1) > 1e-12 {
		t.Errorf("field 1 X: expected decenter=0 compression=-0.1, got %+v", f0)
	}
	// Field 2: all four zero -> no vignetting.
	if result.Fields[1].Vignetting != nil {
		t.Errorf("field 2 should have no vignetting, got %+v", result.Fields[1].Vignetting)
	}
}

func TestCodeV_ZoomPositions(t *testing.T) {
	input := `SEQ
ZOOM 3
YAN 0 30
S 0.02 5.0 1.5:60
SI 0 0
ZOO THI S1 5.0 7.0 9.0
ZOO RDY S1 50.0 60.0 70.0
ZOO K S1 0.0 -0.1 -0.2
ZOO CIR S1 10.0 12.0 14.0
GO
`
	result, err := ParseCodeV(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Surfaces) != 2 {
		t.Fatalf("expected 2 surfaces, got %d", len(result.Surfaces))
	}
	idx := ConfigIndexes(result)
	if len(idx) != 2 || idx[0] != 2 || idx[1] != 3 {
		t.Fatalf("expected configs [2 3], got %v", idx)
	}
	// Config 2 (position 2): THI 7.0, radius 60 -> curv 1/60, conic -0.1,
	// CIR 12 -> diameter 24.
	c1 := ConfigSurfaceSet(result, 2)
	if len(c1) != 2 {
		t.Fatalf("config 1: expected 2 surfaces, got %d", len(c1))
	}
	if math.Abs(c1[0].Thickness-7.0) > 1e-12 {
		t.Errorf("config 1 THI: expected 7.0, got %g", c1[0].Thickness)
	}
	if math.Abs(c1[0].Curvature-(1.0/60.0)) > 1e-12 {
		t.Errorf("config 1 RDY: expected curv %g, got %g", 1.0/60.0, c1[0].Curvature)
	}
	if math.Abs(c1[0].Conic+0.1) > 1e-12 {
		t.Errorf("config 1 K: expected -0.1, got %g", c1[0].Conic)
	}
	if math.Abs(c1[0].Diameter-24.0) > 1e-12 {
		t.Errorf("config 1 CIR: expected diameter 24, got %g", c1[0].Diameter)
	}
	// Base (config 0) is unchanged.
	if result.Surfaces[0].Thickness != 5.0 {
		t.Errorf("base THI must stay 5.0, got %g", result.Surfaces[0].Thickness)
	}
	// Config 3 (position 3).
	c2 := ConfigSurfaceSet(result, 3)
	if math.Abs(c2[0].Thickness-9.0) > 1e-12 || math.Abs(c2[0].Conic+0.2) > 1e-12 {
		t.Errorf("config 3 overlays not applied: %+v", c2[0])
	}
}

func TestCodeV_ZoomVignetting(t *testing.T) {
	input := `SEQ
ZOOM 2
YAN 0 30
S 0.02 5.0
SI 0 0
ZOO VUY F1 0.0 0.1
ZOO VLY F1 0.0 0.1
ZOO VUX F1 0.0 0.05
ZOO VLX F1 0.0 0.05
GO
`
	result, err := ParseCodeV(input)
	if err != nil {
		t.Fatal(err)
	}
	fv := ConfigFields(result, 2)
	if len(fv) != 2 {
		t.Fatalf("config 2: expected 2 fields, got %d", len(fv))
	}
	v := fv[0].Vignetting
	if v == nil {
		t.Fatal("config 2 field 1 should carry zoom vignetting")
	}
	if math.Abs(v.DecenterY) > 1e-12 || math.Abs(v.CompressionY-0.1) > 1e-12 {
		t.Errorf("config 2 field 1: expected compressionY=0.1, got %+v", v)
	}
	if math.Abs(v.CompressionX-0.05) > 1e-12 {
		t.Errorf("config 2 field 1: expected compressionX=0.05, got %+v", v)
	}
	if fv[1].Vignetting != nil {
		t.Errorf("config 2 field 2 should have no vignetting, got %+v", fv[1].Vignetting)
	}
	// Base fields keep no vignetting (all-zero rows were not written).
	if result.Fields[0].Vignetting != nil {
		t.Errorf("base field 1 should have no vignetting, got %+v", result.Fields[0].Vignetting)
	}
}
