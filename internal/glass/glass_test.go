package glass

import (
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

func TestCatalogAddAndLookup(t *testing.T) {
	c := NewCatalog()
	g := types.Glass{Name: "N-BK7", ND: 1.5168, VD: 64.17}
	c.Add(g)
	got, ok := c.Lookup("N-BK7")
	if !ok {
		t.Fatal("Lookup failed for N-BK7")
	}
	if got.Name != "N-BK7" {
		t.Errorf("Name = %v, want N-BK7", got.Name)
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
	g := types.Glass{Name: "N-BK7", ND: 1.5168, VD: 64.17, Aliases: []string{"BK7"}}
	c.Add(g)
	got, ok := c.Lookup("BK7")
	if !ok {
		t.Fatal("Lookup via alias failed")
	}
	if got.Name != "N-BK7" {
		t.Errorf("Name = %v, want N-BK7", got.Name)
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

func TestApproximateFromNDVD(t *testing.T) {
	n, err := approximateFromNDVD(1.5168, 64.17, 0.00058756)
	if err != nil {
		t.Fatalf("approximateFromNDVD: %v", err)
	}
	if n < 1.50 || n > 1.53 {
		t.Errorf("n(d-line) = %v, expected ~1.5168", n)
	}
}

func TestSellmeier1(t *testing.T) {
	coeffs := []float64{1.03961212, 0.00600069867, 0.231792344, 0.0200179144, 1.01046945, 103.560653}
	// Sellmeier formula treats λ in µm. 587.56 nm → 0.58756 µm.
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

func TestEstimatePartialDispersion(t *testing.T) {
	nF, nC, err := estimatePartialDispersion(1.5168, 64.17)
	if err != nil {
		t.Fatalf("estimatePartialDispersion: %v", err)
	}
	if nF <= nC {
		t.Error("nF should be > nC (normal dispersion)")
	}
}

func TestCalcRefractiveIndexSellmeier(t *testing.T) {
	g := &types.Glass{
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

func TestCalcRefractiveIndexCauchy(t *testing.T) {
	g := &types.Glass{
		Name:              "N-BK7",
		ND:                1.5168,
		VD:                64.17,
		DispersionFormula: types.Constant,
	}
	n, err := CalcRefractiveIndex(g, 0.00058756)
	if err != nil {
		t.Fatalf("CalcRefractiveIndex (Cauchy): %v", err)
	}
	if n < 1.50 || n > 1.53 {
		t.Errorf("n = %v, expected ~1.5168", n)
	}
}

func TestCalcRefractiveIndexTableInterp(t *testing.T) {
	g := &types.Glass{
		Name: "test",
		RefractiveIndices: []types.RefractiveIndexEntry{
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

func TestCalcRefractiveIndexNoData(t *testing.T) {
	g := &types.Glass{Name: "test"}
	_, err := CalcRefractiveIndex(g, 0.00058756)
	if err == nil {
		t.Error("Expected error for glass with no dispersion data")
	}
}
