package importer

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
)

// osloNXTInput is a minimal OSLO NXT lens carrying a model glass ("MOD G1")
// whose refractive indices follow the name on the GLA line. The first NXT
// block is the object plane; the GLA block follows it.
const osloNXTInput = `LEN NEW "MODEL" 0.98445 12
TH  1.0e+10
NXT
WV 0.58756
WV2 0.48613
WV3 0.65627
GLA MOD G1         1.693 1.704333 1.688217
RD   0.744379931517
TH   0.095
NXT
AIR
RD   0.0
TH   0.0
END
`

func TestParseOslo_ModelGlassIndices(t *testing.T) {
	result, err := ParseOslo(osloNXTInput)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Surfaces) != 2 {
		t.Fatalf("expected 2 surfaces (GLA + image), got %d", len(result.Surfaces))
	}
	if result.Surfaces[0].Material.Key != "MOD G1" {
		t.Errorf("surface 0 material: expected %q, got %q", "MOD G1", result.Surfaces[0].Material.Key)
	}

	var g *types.Glass
	for i := range result.GlassEntries {
		if result.GlassEntries[i].Label == "MOD G1" {
			g = &result.GlassEntries[i]
		}
	}
	if g == nil {
		t.Fatal("expected MOD G1 glass entry")
	}
	if g.Type != types.GlassTypeTabulated {
		t.Errorf("glass type: expected tabulated, got %q", g.Type)
	}
	if len(g.RefractiveIndices) != 3 {
		t.Fatalf("expected 3 refractive index entries, got %d", len(g.RefractiveIndices))
	}
	want := []float64{1.693, 1.704333, 1.688217}
	for i, w := range want {
		if math.Abs(g.RefractiveIndices[i].Value-w) > 1e-9 {
			t.Errorf("index %d: expected %g, got %g", i, w, g.RefractiveIndices[i].Value)
		}
	}
}

func TestParseOslo_CatalogGlassNoIndices(t *testing.T) {
	input := `LEN NEW "CAT" 0.98445 12
TH  1.0e+10
NXT
GLA BK7
RD   0.02
TH   5.0
NXT
AIR
RD   0.0
TH   0.0
END
`
	result, err := ParseOslo(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Surfaces[0].Material.Key != "BK7" {
		t.Errorf("surface material: expected %q, got %q", "BK7", result.Surfaces[1].Material.Key)
	}
	var g *types.Glass
	for i := range result.GlassEntries {
		if result.GlassEntries[i].Label == "BK7" {
			g = &result.GlassEntries[i]
		}
	}
	if g == nil {
		t.Fatal("expected BK7 glass entry")
	}
	if g.Type != types.GlassTypeModel {
		t.Errorf("glass type: expected model, got %q", g.Type)
	}
	if len(g.RefractiveIndices) != 0 {
		t.Errorf("expected no refractive indices for catalog glass, got %d", len(g.RefractiveIndices))
	}
}

func TestParseOslo_NumericNameModelGlass(t *testing.T) {
	input := `LEN NEW "NUM" 0.98445 12
TH  1.0e+10
NXT
GLA 1.506000   1.506 1.506
RD   0.02
TH   5.0
NXT
AIR
RD   0.0
TH   0.0
END
`
	result, err := ParseOslo(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Surfaces[0].Material.Key != "1.506000" {
		t.Errorf("surface material: expected %q, got %q", "1.506000", result.Surfaces[1].Material.Key)
	}
	var g *types.Glass
	for i := range result.GlassEntries {
		if result.GlassEntries[i].Label == "1.506000" {
			g = &result.GlassEntries[i]
		}
	}
	if g == nil {
		t.Fatal("expected numeric-name glass entry")
	}
	if len(g.RefractiveIndices) != 2 {
		t.Fatalf("expected 2 refractive index entries, got %d", len(g.RefractiveIndices))
	}
}

func TestParseOslo_NXTWavelengthMicroToMilli(t *testing.T) {
	input := `LEN NEW "WL" 0.98445 12
TH  1.0e+10
NXT
WV 0.58756 0.48613 0.65627
WW 1.0 0.8 0.6
GLA BK7
RD  0.5
TH  1.0
NXT
AIR
RD  0.0
TH  0.0
END
`
	result, err := ParseOslo(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Wavelengths) != 3 {
		t.Fatalf("expected 3 wavelengths, got %d", len(result.Wavelengths))
	}
	want := []float64{0.00058756, 0.00048613, 0.00065627}
	for i, w := range want {
		if math.Abs(result.Wavelengths[i].Value-w) > 1e-12 {
			t.Errorf("wavelength %d: expected %g, got %g", i, w, result.Wavelengths[i].Value)
		}
	}
	if math.Abs(result.Wavelengths[1].Weight-0.8) > 1e-12 {
		t.Errorf("wavelength 1 weight: expected 0.8, got %g", result.Wavelengths[1].Weight)
	}
}

func TestDecodeDispersionCode(t *testing.T) {
	cases := []struct {
		code   string
		wantND float64
		wantVD float64
		wantOK bool
	}{
		{"748523", 1.748, 52.3, true},
		{"515546", 1.515, 54.6, true},
		{"804339", 1.804, 33.9, true},
		{"500.700", 1.500, 70.0, true},
		{"569.631", 1.569, 63.1, true},
		{"517000.520000", 1.517, 52.0, true},
		{"BK7", 0, 0, false},
		{"74852", 0, 0, false},   // 5 digits
		{"7485234", 0, 0, false}, // 7 digits
	}
	for _, c := range cases {
		nd, vd, ok := decodeDispersionCode(c.code)
		if ok != c.wantOK {
			t.Errorf("%s: ok=%v want %v", c.code, ok, c.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if math.Abs(nd-c.wantND) > 1e-9 || math.Abs(vd-c.wantVD) > 1e-9 {
			t.Errorf("%s: got nd=%.6f vd=%.6f want %.6f/%.6f", c.code, nd, vd, c.wantND, c.wantVD)
		}
	}
}

func TestGlassResolutionHelpers(t *testing.T) {
	cases := []struct {
		name   string
		wantND float64
		wantVD float64
		ok     bool
	}{
		{"PMMA", 1.49180, 57.40, true},
		{"330R", 1.50940, 56.20, true},
		{"OKP4HT", 1.52500, 56.00, true},
		{"N-BK7_MOLD", 1.5168, 64.17, true},
		{"AL-6263-(OKP4HT)", 1.52500, 56.00, true},
		{"AL-6261-(OKP4)", 1.52500, 56.00, true},
		// Hoya legacy H- glasses.
		{"H-ZF72", 1.92286, 18.90, true},
		{"H-ZLAF70", 1.90366, 31.32, true},
		{"H-LAK51", 1.69680, 55.44, true},
		{"H-ZF4", 1.72825, 28.32, true},
		{"H-QK3", 1.48749, 70.44, true},
		{"H-ZLAF55_MOLD", 1.83480, 42.73, true},
		{"H_LAF2", 1.74320, 49.31, true}, // underscore spelling of H-LAF2
		{"TOTALLY_UNKNOWN", 0, 0, false},
	}
	for _, c := range cases {
		nd, vd, ok := resolveCommonGlass(c.name)
		if ok != c.ok {
			t.Errorf("%s: ok=%v want %v", c.name, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if math.Abs(nd-c.wantND) > 1e-5 || math.Abs(vd-c.wantVD) > 1e-5 {
			t.Errorf("%s: got nd=%.6f vd=%.6f want %.6f/%.6f", c.name, nd, vd, c.wantND, c.wantVD)
		}
	}
}
