package glass

import (
	"math"
	"sync"
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
)

func TestNewCatalog(t *testing.T) {
	c := NewCatalog()
	if c == nil {
		t.Fatal("NewCatalog returned nil")
	}
	if len(c.ByName) != 0 {
		t.Error("New catalog should be empty")
	}
}

func TestCatalogAddAndLookupModel(t *testing.T) {
	c := NewCatalog()
	g := types.Glass{Type: types.GlassTypeModel, Label: "my_glass", ND: 1.5168, VD: 64.17}
	c.Add(g)
	got, ok := c.Lookup("my_glass")
	if !ok {
		t.Fatal("Lookup by label failed")
	}
	if got.Name != "" {
		t.Errorf("model glass Name should be empty, got %q", got.Name)
	}
	if got.Label != "my_glass" {
		t.Errorf("Label = %q, want my_glass", got.Label)
	}
}

func TestCatalogAddAndLookupCatalog(t *testing.T) {
	c := NewCatalog()
	g := types.Glass{Type: types.GlassTypeCatalog, Name: "N-BK7", ND: 1.5168, VD: 64.17}
	c.Add(g)
	got, ok := c.Lookup("N-BK7")
	if !ok {
		t.Fatal("Lookup by name failed")
	}
	if got.Name != "N-BK7" {
		t.Errorf("Name = %q, want N-BK7", got.Name)
	}
}

func TestCatalogLookupNotFound(t *testing.T) {
	c := NewCatalog()
	_, ok := c.Lookup("NONEXISTENT")
	if ok {
		t.Error("Expected false for nonexistent glass")
	}
}

func TestCatalogLookupAlias(t *testing.T) {
	c := NewCatalog()
	g := types.Glass{Type: types.GlassTypeCatalog, Name: "N-BK7", ND: 1.5168, VD: 64.17, Aliases: []string{"BK7"}}
	c.Add(g)
	got, ok := c.Lookup("BK7")
	if !ok {
		t.Fatal("Lookup via alias failed")
	}
	if got.Name != "N-BK7" {
		t.Errorf("Name = %q, want N-BK7", got.Name)
	}
}

func TestCatalogLookupModelAutoKey(t *testing.T) {
	c := NewCatalog()
	g := types.Glass{Type: types.GlassTypeModel, ND: 1.51680, VD: 64.17}
	c.Add(g)
	key := "1.51680:64.17"
	got, ok := c.Lookup(key)
	if !ok {
		t.Fatalf("Lookup by auto key %q failed", key)
	}
	if got.ND != 1.51680 || got.VD != 64.17 {
		t.Errorf("got nd=%v vd=%v", got.ND, got.VD)
	}
}

func TestCatalogRefractiveIndexAir(t *testing.T) {
	c := NewCatalog()
	n, err := c.RefractiveIndex("AIR", 0.00058756)
	if err != nil {
		t.Fatalf("RefractiveIndex(AIR): %v", err)
	}
	if n != 1.0 {
		t.Errorf("n(AIR) = %v, want 1.0", n)
	}
}

func TestCatalogRefractiveIndexEmpty(t *testing.T) {
	c := NewCatalog()
	n, err := c.RefractiveIndex("", 0.00058756)
	if err != nil {
		t.Fatalf("RefractiveIndex(''): %v", err)
	}
	if n != 1.0 {
		t.Errorf("n('') = %v, want 1.0", n)
	}
}

func TestCatalogRefractiveIndexGlassNotFound(t *testing.T) {
	c := NewCatalog()
	_, err := c.RefractiveIndex("NONEXISTENT", 0.00058756)
	if err == nil {
		t.Error("Expected error for nonexistent glass")
	}
}

func TestSellmeier1(t *testing.T) {
	coeffs := []float64{1.03961212, 0.00600069867, 0.231792344, 0.0200179144, 1.01046945, 103.560653}
	n, err := sellmeier1(coeffs, 0.58756)
	if err != nil {
		t.Fatalf("sellmeier1: %v", err)
	}
	if n < 1.50 || n > 1.53 {
		t.Errorf("Sellmeier1 BK7 at d-line = %v, expected ~1.5168", n)
	}
}

func TestSellmeier1InvalidCoeffs(t *testing.T) {
	_, err := sellmeier1(nil, 0.58756)
	if err == nil {
		t.Error("Expected error for empty coefficients")
	}
}

func TestInterpolateRefractiveIndex(t *testing.T) {
	entries := []types.RefractiveIndexEntry{
		{Wavelength: 0.000486, Value: 1.522},
		{Wavelength: 0.000656, Value: 1.514},
	}
	n, err := interpolateRefractiveIndex(entries, 0.00058756)
	if err != nil {
		t.Fatalf("interpolateRefractiveIndex: %v", err)
	}
	if n < 1.515 || n > 1.521 {
		t.Errorf("Interpolated n = %v, expected between 1.514 and 1.522", n)
	}
}

func TestCalcRefractiveIndexSellmeier(t *testing.T) {
	g := &types.Glass{
		Type:              types.GlassTypeCatalog,
		Name:              "N-BK7",
		ND:                1.5168,
		VD:                64.17,
		DispersionFormula: types.Sellmeier1,
		Coefficients:      []float64{1.03961212, 0.00600069867, 0.231792344, 0.0200179144, 1.01046945, 103.560653},
	}
	n, err := CalcRefractiveIndex(g, 0.58756)
	if err != nil {
		t.Fatalf("CalcRefractiveIndex: %v", err)
	}
	if n < 1.50 || n > 1.53 {
		t.Errorf("n = %v, expected ~1.5168", n)
	}
}

func TestCalcRefractiveIndexModel(t *testing.T) {
	g := &types.Glass{
		Type: types.GlassTypeModel,
		Label: "my_glass",
		ND:   1.51680,
		VD:   64.17,
	}
	n, err := CalcRefractiveIndex(g, 0.000587562)
	if err != nil {
		t.Fatalf("CalcRefractiveIndex (Model): %v", err)
	}
	if math.Abs(n-1.51680) > 0.001 {
		t.Errorf("n(d-line) = %v, expected ~1.51680", n)
	}
}

func TestCalcRefractiveIndexModelMissingNDVD(t *testing.T) {
	g := &types.Glass{
		Type:  types.GlassTypeModel,
		Label: "bad_glass",
	}
	_, err := CalcRefractiveIndex(g, 0.00058756)
	if err == nil {
		t.Error("Expected error for model glass missing nd/vd")
	}
}

func TestCalcRefractiveIndexTabulated(t *testing.T) {
	g := &types.Glass{
		Type:  types.GlassTypeTabulated,
		Label: "test_table",
		RefractiveIndices: types.RefractiveIndexTable{
			{Wavelength: 0.000486, Value: 1.522},
			{Wavelength: 0.000656, Value: 1.514},
		},
	}
	n, err := CalcRefractiveIndex(g, 0.00058756)
	if err != nil {
		t.Fatalf("CalcRefractiveIndex: %v", err)
	}
	if n < 1.515 || n > 1.521 {
		t.Errorf("n = %v, expected between 1.514 and 1.522", n)
	}
}

func TestCalcRefractiveIndexTabulatedMissingLabel(t *testing.T) {
	g := &types.Glass{
		Type: types.GlassTypeModel,
	}
	_, err := CalcRefractiveIndex(g, 0.00058756)
	if err == nil {
		t.Error("Expected error for model glass with no data")
	}
}

func TestCalcRefractiveIndexNoData(t *testing.T) {
	g := &types.Glass{Name: "test"}
	_, err := CalcRefractiveIndex(g, 0.00058756)
	if err == nil {
		t.Error("Expected error for glass with no type")
	}
}

func TestIndecesFromNdVdN_BK7(t *testing.T) {
	nd := 1.51680
	vd := 64.17

	indices := IndecesFromNdVd(nd, vd)

	cases := []struct {
		name string
		key  string
		want float64
	}{
		{"nC", "C", 1.51432},
		{"nF", "F", 1.52238},
		{"ng", "g", 1.52668},
		{"nd", "d", 1.51680},
	}
	for _, c := range cases {
		got := indices[c.key].Index
		diff := math.Abs(got-c.want) / c.want
		if diff > 0.001 {
			t.Errorf("%s = %v, want %v (relative error %v > 0.1%%)", c.name, got, c.want, diff)
		}
	}
}

func TestIndecesFromNdVdOrder(t *testing.T) {
	nd := 1.51680
	vd := 64.17
	indices := IndecesFromNdVd(nd, vd)

	if indices["C"].Index >= nd {
		t.Error("nC should be less than nd (normal dispersion)")
	}
	if nd >= indices["F"].Index {
		t.Error("nF should be greater than nd")
	}
	if indices["F"].Index >= indices["g"].Index {
		t.Error("ng should be greater than nF")
	}
}

func TestIndecesFromNdVdVdZero(t *testing.T) {
	nd := 1.5
	vd := 0.0
	indices := IndecesFromNdVd(nd, vd)

	for name, e := range indices {
		if e.Index != nd {
			t.Errorf("%s index = %v, want %v (vd=0)", name, e.Index, nd)
		}
	}
}

func TestSplineInterpolate(t *testing.T) {
	nd := 1.51680
	vd := 64.17
	indices := IndecesFromNdVd(nd, vd)
	knots := sortedKnots(indices)

	s, err := BuildSpline(knots)
	if err != nil {
		t.Fatalf("BuildSpline: %v", err)
	}

	for _, k := range knots {
		got := EvalSpline(knots, s, k.Wavelength)
		if math.Abs(got-k.Index) > 1e-12 {
			t.Errorf("Spline at λ=%v: got %v, want %v", k.Wavelength, got, k.Index)
		}
	}
}

func TestCauchyFit(t *testing.T) {
	nd := 1.51680
	vd := 64.17
	indices := IndecesFromNdVd(nd, vd)
	knots := sortedKnots(indices)

	ca := FitCauchy(knots, 3)

	// Cauchy is an approximation over a wide range; verify at d-line (anchor point)
	n := ca.Eval(0.000587562)
	if math.Abs(n-1.51680) > 0.001 {
		t.Errorf("Cauchy at d-line: got %v, want ~1.51680", n)
	}

	// Extrapolation: should return reasonable values (not NaN, physically sensible)
	for _, wl := range []float64{0.000330, 0.000350, 0.002200, 0.004000} {
		n := ca.Eval(wl)
		if n <= 1.0 || n > 2.0 || math.IsNaN(n) {
			t.Errorf("Cauchy at λ=%v: unreasonable value %v", wl, n)
		}
	}
	nIR := ca.Eval(0.002500)
	nVis := ca.Eval(0.000550)
	if nIR > nVis {
		t.Errorf("Cauchy: n at 2500nm (%v) should be < n at 550nm (%v)", nIR, nVis)
	}
}

func TestRefractiveIndexFromNDVD(t *testing.T) {
	nd := 1.51680
	vd := 64.17

	n, err := RefractiveIndexFromNDVD(nd, vd, 0.000587562)
	if err != nil {
		t.Fatalf("RefractiveIndexFromNDVD: %v", err)
	}
	if math.Abs(n-1.51680) > 1e-10 {
		t.Errorf("n(d-line) = %v, want 1.51680", n)
	}
}

func TestRefractiveIndexFromNDVDOutsideRange(t *testing.T) {
	_, err := RefractiveIndexFromNDVD(1.5, 60, 0.000300)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- AGF NM format parsing ---

func TestParseAGF_NMFormat(t *testing.T) {
	input := `CC SCHOTT June 2025
NM N-BK7 2 520636.252 1.51680 64.17 0 1
CD 1.265385420E+00 8.131040780E-03 1.441910730E-02 5.433032260E-02 1.003230280E+00 1.028211660E+02
ED
`
	glasses, err := ParseAGF([]byte(input))
	if err != nil {
		t.Fatalf("ParseAGF failed: %v", err)
	}
	if len(glasses) != 1 {
		t.Fatalf("expected 1 glass, got %d", len(glasses))
	}
	g := glasses[0]
	if g.Name != "N-BK7" {
		t.Errorf("expected Name=N-BK7, got %q", g.Name)
	}
	if g.Type != types.GlassTypeCatalog {
		t.Errorf("expected Type=catalog, got %q", g.Type)
	}
	if math.Abs(g.ND-1.51680) > 1e-4 {
		t.Errorf("expected ND≈1.51680, got %f", g.ND)
	}
	if math.Abs(g.VD-64.17) > 1e-2 {
		t.Errorf("expected VD≈64.17, got %f", g.VD)
	}
	if len(g.Coefficients) < 6 {
		t.Fatalf("expected ≥6 coefficients, got %d", len(g.Coefficients))
	}
	if g.Coefficients[0] != 1.265385420E+00 {
		t.Errorf("expected coeff[0]=1.265385420, got %g", g.Coefficients[0])
	}
	if g.DispersionFormula != types.Sellmeier1 {
		t.Errorf("expected DispersionFormula=sellmeier_1, got %q", g.DispersionFormula)
	}
}

func TestParseAGF_NMFormat_MultipleGlasses(t *testing.T) {
	input := `CC SCHOTT June 2025
NM N-BK7 2 520636.252 1.51680 64.17 0 1
CD 1.265385420E+00 8.131040780E-03 1.441910730E-02 5.433032260E-02 1.003230280E+00 1.028211660E+02
ED
NM F2 2 620364.360 1.62004 36.37 0 1
CD 1.345333590E+00 9.977438710E-03 2.090731760E-01 4.704507670E-02 9.373571620E-01 1.118867640E+02
ED
`
	glasses, err := ParseAGF([]byte(input))
	if err != nil {
		t.Fatalf("ParseAGF failed: %v", err)
	}
	if len(glasses) != 2 {
		t.Fatalf("expected 2 glasses, got %d", len(glasses))
	}
	if glasses[0].Name != "N-BK7" {
		t.Errorf("expected glass[0]=N-BK7, got %q", glasses[0].Name)
	}
	if glasses[1].Name != "F2" {
		t.Errorf("expected glass[1]=F2, got %q", glasses[1].Name)
	}
}

func TestParseAGF_DetectManufacturer_CC_Keyword(t *testing.T) {
	input := `CC SCHOTT June 2025 preferred
NM N-BK7 2 520636.252 1.51680 64.17 0 1
CD 1.265385420E+00 8.131040780E-03 1.441910730E-02 5.433032260E-02 1.003230280E+00 1.028211660E+02
ED
`
	glasses, err := ParseAGF([]byte(input))
	if err != nil {
		t.Fatalf("ParseAGF failed: %v", err)
	}
	if len(glasses) != 1 {
		t.Fatalf("expected 1 glass, got %d", len(glasses))
	}
	if glasses[0].Manufacturer != "SCHOTT" {
		t.Errorf("expected Manufacturer=SCHOTT, got %q", glasses[0].Manufacturer)
	}
}

func TestParseAGF_DetectManufacturer_CC_FirstToken(t *testing.T) {
	input := `CC CUSTOM_OPTICS 2025
NM N-BK7 2 520636.252 1.51680 64.17 0 1
CD 1.265385420E+00 8.131040780E-03 1.441910730E-02 5.433032260E-02 1.003230280E+00 1.028211660E+02
ED
`
	glasses, err := ParseAGF([]byte(input))
	if err != nil {
		t.Fatalf("ParseAGF failed: %v", err)
	}
	if len(glasses) != 1 {
		t.Fatalf("expected 1 glass, got %d", len(glasses))
	}
	expected := "CUSTOM_OPTICS"
	if glasses[0].Manufacturer != expected {
		t.Errorf("expected Manufacturer=%q, got %q", expected, glasses[0].Manufacturer)
	}
}

func TestParseAGF_DetectManufacturer_CC_Keyword_OHARA(t *testing.T) {
	input := `CC
NM PBM2R 2 1 1.620040 36.407735 0 1 1
CD 1.341394220E+00 9.803016690E-03 2.131855100E-01 4.705870880E-02 9.832880160E-01 1.174582700E+02
ED
`
	glasses, err := ParseAGF([]byte(input), "OHARA_260701.AGF")
	if err != nil {
		t.Fatalf("ParseAGF failed: %v", err)
	}
	if len(glasses) != 1 {
		t.Fatalf("expected 1 glass, got %d", len(glasses))
	}
	if glasses[0].Manufacturer != "OHARA" {
		t.Errorf("expected Manufacturer=OHARA, got %q", glasses[0].Manufacturer)
	}
}

func TestParseAGF_DecodeUTF16LE(t *testing.T) {
	utf16Bytes := []byte{0xFF, 0xFE}
	utf16Bytes = append(utf16Bytes, []byte("N\000A\000M\000E\000 \000N\000-\000B\000K\0007\000\n\000")...)
	glasses, err := ParseAGF(utf16Bytes)
	if err != nil {
		t.Fatalf("ParseAGF failed: %v", err)
	}
	if len(glasses) != 0 {
		t.Logf("got %d glasses from NAME format (may vary)", len(glasses))
	}
}

func TestParseAGF_CRLF(t *testing.T) {
	input := "CC SCHOTT\r\nNM N-BK7 2 520636.252 1.51680 64.17 0 1\r\nCD 1.265385420E+00 8.131040780E-03 1.441910730E-02 5.433032260E-02 1.003230280E+00 1.028211660E+02\r\nED\r\n"
	glasses, err := ParseAGF([]byte(input))
	if err != nil {
		t.Fatalf("ParseAGF failed: %v", err)
	}
	if len(glasses) != 1 {
		t.Fatalf("expected 1 glass, got %d", len(glasses))
	}
	if glasses[0].Name != "N-BK7" {
		t.Errorf("expected N-BK7, got %q", glasses[0].Name)
	}
}

func TestParseAGF_NAMEFormat_StillWorks(t *testing.T) {
	input := `NAME N-BK7
MANUFACTURER SCHOTT
ND 1.51680
VD 64.17
CO 1.265385420E+00 8.131040780E-03 1.441910730E-02 5.433032260E-02 1.003230280E+00 1.028211660E+02
ED
`
	glasses, err := ParseAGF([]byte(input))
	if err != nil {
		t.Fatalf("ParseAGF failed: %v", err)
	}
	if len(glasses) != 1 {
		t.Fatalf("expected 1 glass, got %d", len(glasses))
	}
	g := glasses[0]
	if g.Name != "N-BK7" {
		t.Errorf("expected Name=N-BK7, got %q", g.Name)
	}
	if g.Type != types.GlassTypeCatalog {
		t.Errorf("expected Type=catalog, got %q", g.Type)
	}
	if g.Manufacturer != "SCHOTT" {
		t.Errorf("expected Manufacturer=SCHOTT, got %q", g.Manufacturer)
	}
	if math.Abs(g.ND-1.51680) > 1e-4 {
		t.Errorf("expected ND≈1.51680, got %f", g.ND)
	}
	if len(g.Coefficients) < 6 {
		t.Errorf("expected ≥6 coefficients, got %d", len(g.Coefficients))
	}
}

// TestCatalogRefractiveIndexCache verifies the per-(glass, nd/vd, wavelength)
// cache distinguishes model glasses whose nd/vd change between evaluations
// (as happens during nd/vd optimisation), while still returning correct values.
func TestCatalogRefractiveIndexCache(t *testing.T) {
	c := NewCatalog()
	c.Add(types.Glass{Type: types.GlassTypeModel, Label: "MOD", ND: 1.5, VD: 60})

	n1, err := c.RefractiveIndex("MOD", 0.00058756)
	if err != nil {
		t.Fatal(err)
	}
	n2, err := c.RefractiveIndex("MOD", 0.00058756)
	if err != nil {
		t.Fatal(err)
	}
	if n1 != n2 {
		t.Errorf("cached and fresh index differ: %v vs %v", n1, n2)
	}

	// Simulate an nd/vd optimisation step: the surface material key is stable
	// but the underlying model glass changes. The cache key includes nd/vd so
	// the new value must not be shadowed by the old cached one.
	c.ByName["MOD"].ND = 1.8
	n3, err := c.RefractiveIndex("MOD", 0.00058756)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(n3-n1) < 1e-6 {
		t.Errorf("changed nd did not invalidate cache: n = %v (old %v)", n3, n1)
	}
}

// TestCatalogRefractiveIndexCacheConcurrent exercises the sync.Map cache from
// multiple goroutines to confirm no races or corrupt values.
func TestCatalogRefractiveIndexCacheConcurrent(t *testing.T) {
	c := NewCatalog()
	for i := 0; i < 8; i++ {
		c.Add(types.Glass{Type: types.GlassTypeModel, Label: "G", ND: 1.5 + float64(i)*0.01, VD: 60})
	}

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if _, err := c.RefractiveIndex("G", 0.00058756); err != nil {
					t.Errorf("RefractiveIndex: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestNDVDStoredValues(t *testing.T) {
	g := &types.Glass{Type: types.GlassTypeModel, Label: "m", ND: 1.51680, VD: 64.17}
	nd, vd, ok := NDVD(g)
	if !ok {
		t.Fatal("expected ok=true for stored nd/vd")
	}
	if nd != 1.51680 || vd != 64.17 {
		t.Errorf("got nd=%v vd=%v, want 1.5168 64.17", nd, vd)
	}
}

func TestNDVDCatalogFromCoefficients(t *testing.T) {
	g := &types.Glass{
		Type:              types.GlassTypeCatalog,
		Name:              "N-BK7",
		DispersionFormula: types.Sellmeier1,
		Coefficients:      []float64{1.03961212, 0.00600069867, 0.231792344, 0.0200179144, 1.01046945, 103.560653},
	}
	nd, vd, ok := NDVD(g)
	if !ok {
		t.Fatal("expected ok=true for coefficient-only catalog glass")
	}
	if math.Abs(nd-1.5168)/1.5168 > 0.001 {
		t.Errorf("nd = %v, want ~1.5168", nd)
	}
	if math.Abs(vd-64.17)/64.17 > 0.01 {
		t.Errorf("vd = %v, want ~64.17", vd)
	}
}

func TestNDVDTabulated(t *testing.T) {
	g := &types.Glass{
		Type:  types.GlassTypeTabulated,
		Label: "table",
		RefractiveIndices: types.RefractiveIndexTable{
			{Wavelength: 0.000486133, Value: 1.52238},
			{Wavelength: 0.000587562, Value: 1.51680},
			{Wavelength: 0.000656273, Value: 1.51432},
		},
	}
	nd, vd, ok := NDVD(g)
	if !ok {
		t.Fatal("expected ok=true for tabulated glass")
	}
	if math.Abs(nd-1.5168)/1.5168 > 0.001 {
		t.Errorf("nd = %v, want ~1.5168", nd)
	}
	wantVD := (1.51680 - 1) / (1.52238 - 1.51432)
	if math.Abs(vd-wantVD)/wantVD > 0.001 {
		t.Errorf("vd = %v, want ~%.2f", vd, wantVD)
	}
}

func TestNDVDConstant(t *testing.T) {
	g := &types.Glass{
		Type:              types.GlassTypeCatalog,
		Name:              "CONST",
		DispersionFormula: types.Constant,
		ND:                1.6,
	}
	nd, vd, ok := NDVD(g)
	if !ok {
		t.Fatal("expected ok=true for constant glass with nd")
	}
	if nd != 1.6 {
		t.Errorf("nd = %v, want 1.6", nd)
	}
	if vd != 0 {
		t.Errorf("vd = %v, want 0 (no dispersion)", vd)
	}
}

func TestNDVDNoData(t *testing.T) {
	g := &types.Glass{Type: types.GlassTypeModel, Label: "empty"}
	if _, _, ok := NDVD(g); ok {
		t.Error("expected ok=false for glass with no nd/vd or dispersion data")
	}
}
