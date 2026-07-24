package glass

import (
	"math"
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
	n, err := RefractiveIndexFromNDVD(1.51680, 64.17, 0.000300)
	if err != nil {
		t.Fatalf("RefractiveIndexFromNDVD: %v", err)
	}
	if n != 1.51680 {
		t.Errorf("n = %v, want nd (outside range fallback)", n)
	}
}
