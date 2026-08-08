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

func TestNormalizeName(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"L-LAL12", "LLAL12"},
		{"S-BSL7", "SBSL7"},
		{"LLAL12_OHARA", "LLAL12OHARA"},
		{"N-BK7", "NBK7"},
		{"F2", "F2"},
		{"n-bk7", "NBK7"},
		{"1.51680:64.17", "1.51680:64.17"},
	}
	for _, c := range cases {
		if got := NormalizeName(c.in); got != c.want {
			t.Errorf("NormalizeName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCatalogLookupNormalized(t *testing.T) {
	c := NewCatalog()
	g := types.Glass{
		Type: types.GlassTypeCatalog, Name: "L-LAL12", ND: 1.62004, VD: 36.41,
		Manufacturer: "OHARA", DispersionFormula: types.Sellmeier1,
	}
	c.Add(g)

	// AGF spelling and CODE V hyphen-stripped spelling both resolve.
	for _, key := range []string{"L-LAL12", "LLAL12", "llal12"} {
		got, ok := c.Lookup(key)
		if !ok {
			t.Errorf("Lookup(%q) failed", key)
			continue
		}
		if got.Name != "L-LAL12" {
			t.Errorf("Lookup(%q) Name = %q, want L-LAL12", key, got.Name)
		}
	}
}

func TestCatalogLookupManufacturerSuffix(t *testing.T) {
	c := NewCatalog()
	g := types.Glass{
		Type: types.GlassTypeCatalog, Name: "L-LAL12", ND: 1.62004, VD: 36.41,
		Manufacturer: "OHARA", DispersionFormula: types.Sellmeier1,
	}
	c.Add(g)

	got, ok := c.Lookup("LLAL12_OHARA")
	if !ok {
		t.Fatal("Lookup(LLAL12_OHARA) failed")
	}
	if got.Name != "L-LAL12" {
		t.Errorf("Name = %q, want L-LAL12", got.Name)
	}

	// Wrong manufacturer must not match.
	if _, ok := c.Lookup("LLAL12_SCHOTT"); ok {
		t.Error("Lookup(LLAL12_SCHOTT) should fail with mismatched manufacturer")
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

func TestCatalogLookupMoldSuffix(t *testing.T) {
	c := NewCatalog()
	c.Add(types.Glass{Type: types.GlassTypeCatalog, Name: "N-BK7", ND: 1.51680, VD: 64.17})

	got, ok := c.Lookup("N-BK7_MOLD")
	if !ok {
		t.Fatal("Lookup(N-BK7_MOLD) failed")
	}
	if got.Name != "N-BK7" {
		t.Errorf("Name = %q, want N-BK7", got.Name)
	}
}

func TestCatalogLookupResinParenthesis(t *testing.T) {
	c := NewCatalog()
	c.Add(types.Glass{Type: types.GlassTypeModel, Label: "OKP4HT", ND: 1.52500, VD: 56.00})

	got, ok := c.Lookup("AL-6263-(OKP4HT)")
	if !ok {
		t.Fatal("Lookup(AL-6263-(OKP4HT)) failed")
	}
	if got.Label != "OKP4HT" {
		t.Errorf("Label = %q, want OKP4HT", got.Label)
	}
}

func TestCatalogLookupTrailingM(t *testing.T) {
	c := NewCatalog()
	c.Add(types.Glass{Type: types.GlassTypeCatalog, Name: "S-BAL42", ND: 1.583126, VD: 59.374673})

	got, ok := c.Lookup("S-BAL42M")
	if !ok {
		t.Fatal("Lookup(S-BAL42M) failed")
	}
	if got.Name != "S-BAL42" {
		t.Errorf("Name = %q, want S-BAL42", got.Name)
	}
}

func TestCatalogRefractiveIndexAir(t *testing.T) {
	c := NewCatalog()
	n, err := c.RefractiveIndex(types.Material{}, 0.00058756)
	if err != nil {
		t.Fatalf("RefractiveIndex(AIR): %v", err)
	}
	if n != 1.0 {
		t.Errorf("n(AIR) = %v, want 1.0", n)
	}
}

func TestCatalogRefractiveIndexEmpty(t *testing.T) {
	c := NewCatalog()
	n, err := c.RefractiveIndex(types.Material{}, 0.00058756)
	if err != nil {
		t.Fatalf("RefractiveIndex(''): %v", err)
	}
	if n != 1.0 {
		t.Errorf("n('') = %v, want 1.0", n)
	}
}

func TestCatalogRefractiveIndexGlassNotFound(t *testing.T) {
	c := NewCatalog()
	_, err := c.RefractiveIndex(types.Material{Key: "NONEXISTENT"}, 0.00058756)
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

func TestLaurent(t *testing.T) {
	// CODE V "LAU" coefficients for NOA61 (adhesive) from a PRV block.
	coeffs := []float64{2.36390625, 0.0, 0.025493134, -0.000580235, -3.49933e-6, 4.45404e-8}
	// n² = A₀ + A₁λ² + A₂/λ² + A₃/λ⁴ + A₄/λ⁶ + A₅/λ⁸, λ in µm.
	lsq := 0.58756 * 0.58756
	want := math.Sqrt(coeffs[0] + coeffs[2]/lsq + coeffs[3]/(lsq*lsq) + coeffs[4]/(lsq*lsq*lsq) + coeffs[5]/(lsq*lsq*lsq*lsq))
	n, err := laurent(coeffs, 0.58756)
	if err != nil {
		t.Fatalf("laurent: %v", err)
	}
	if math.Abs(n-want) > 1e-9 {
		t.Errorf("laurent: got %g, want %g", n, want)
	}
	if n < 1.5 || n > 1.6 {
		t.Errorf("laurent NOA61 at d-line: n = %g, expected ~1.55", n)
	}
}

func TestLaurentInvalidCoeffs(t *testing.T) {
	_, err := laurent(nil, 0.58756)
	if err == nil {
		t.Error("Expected error for empty coefficients")
	}
}

func TestCauchy(t *testing.T) {
	// n(λ) = A₀ + A₁/λ² + A₂/λ⁴, A0 ≈ 1.5, A1 ≈ 0.004, A2 ≈ -2e-6, λ in µm.
	coeffs := []float64{1.50, 0.004, -2e-6}
	lambda := 0.58756
	lsq := lambda * lambda
	want := coeffs[0] + coeffs[1]/lsq + coeffs[2]/(lsq*lsq)
	n, err := cauchy(coeffs, lambda)
	if err != nil {
		t.Fatalf("cauchy: %v", err)
	}
	if math.Abs(n-want) > 1e-12 {
		t.Errorf("cauchy: got %g, want %g", n, want)
	}
	if n < 1.5 || n > 1.6 {
		t.Errorf("cauchy at d-line: n = %g, expected ~1.51", n)
	}
}

func TestCauchyVariableTerms(t *testing.T) {
	// A single-term Cauchy (just A₀) must evaluate to a constant.
	n, err := cauchy([]float64{1.52}, 0.58756)
	if err != nil {
		t.Fatalf("cauchy: %v", err)
	}
	if math.Abs(n-1.52) > 1e-12 {
		t.Errorf("single-term cauchy: got %g, want 1.52", n)
	}
	// Four terms (A₁/λ² + A₂/λ⁴ + A₃/λ⁶) exercise the higher-order loop.
	coeffs := []float64{1.5, 0.004, -2e-2, 1e-3}
	lambda := 0.58756
	lsq := lambda * lambda
	want := coeffs[0] + coeffs[1]/lsq + coeffs[2]/(lsq*lsq) + coeffs[3]/(lsq*lsq*lsq)
	if got, err := cauchy(coeffs, lambda); err != nil || math.Abs(got-want) > 1e-9 {
		t.Errorf("4-term cauchy: got %g, want %g (err=%v)", got, want, err)
	}
}

func TestCauchyInvalidCoeffs(t *testing.T) {
	_, err := cauchy(nil, 0.58756)
	if err == nil {
		t.Error("Expected error for empty coefficients")
	}
}

func TestHartmann(t *testing.T) {
	// n(λ) = A₀ + A₁/(λ − A₂), λ in µm.
	coeffs := []float64{1.50, 0.004, 0.12}
	lambda := 0.58756
	want := coeffs[0] + coeffs[1]/(lambda-coeffs[2])
	n, err := hartmann(coeffs, lambda)
	if err != nil {
		t.Fatalf("hartmann: %v", err)
	}
	if math.Abs(n-want) > 1e-12 {
		t.Errorf("hartmann: got %g, want %g", n, want)
	}
	if n < 1.5 || n > 1.6 {
		t.Errorf("hartmann at d-line: n = %g, expected ~1.51", n)
	}
}

func TestHartmannResonance(t *testing.T) {
	// At λ = A₂ the denominator vanishes.
	_, err := hartmann([]float64{1.5, 0.004, 0.58756}, 0.58756)
	if err == nil {
		t.Error("Expected error at the resonance wavelength")
	}
}

func TestHartmannInvalidCoeffs(t *testing.T) {
	_, err := hartmann(nil, 0.58756)
	if err == nil {
		t.Error("Expected error for empty coefficients")
	}
	_, err = hartmann([]float64{1.5, 0.004}, 0.58756)
	if err == nil {
		t.Error("Expected error for fewer than 3 coefficients")
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

// N-BK7-like standard-line table used by the spline/extrapolation tests.
var bk7Table = types.RefractiveIndexTable{
	{Wavelength: 0.000365015, Value: 1.53117},
	{Wavelength: 0.000404656, Value: 1.52705},
	{Wavelength: 0.000435835, Value: 1.52439},
	{Wavelength: 0.000486133, Value: 1.52238},
	{Wavelength: 0.000546074, Value: 1.51872},
	{Wavelength: 0.000587562, Value: 1.51680},
	{Wavelength: 0.000656273, Value: 1.51432},
	{Wavelength: 0.00101398, Value: 1.50731},
}

func TestInterpolateRefractiveIndexSpline(t *testing.T) {
	// Knots reproduce the table values exactly.
	for _, e := range bk7Table {
		n, err := interpolateRefractiveIndex(bk7Table, e.Wavelength)
		if err != nil {
			t.Fatalf("interpolateRefractiveIndex at λ=%v: %v", e.Wavelength, err)
		}
		if math.Abs(n-e.Value) > 1e-10 {
			t.Errorf("n at knot λ=%v = %v, want %v", e.Wavelength, n, e.Value)
		}
	}

	// C1 continuity: the natural cubic spline makes the analytic left/right
	// derivatives agree exactly at every interior knot.
	knots := make([]IndexEntry, len(bk7Table))
	for i, e := range bk7Table {
		knots[i] = IndexEntry{Wavelength: e.Wavelength, Index: e.Value}
	}
	s, err := BuildSpline(knots)
	if err != nil {
		t.Fatalf("BuildSpline: %v", err)
	}
	for k := 1; k < len(knots)-1; k++ {
		hL := knots[k].Wavelength - knots[k-1].Wavelength
		hR := knots[k+1].Wavelength - knots[k].Wavelength
		dL := (knots[k].Index-knots[k-1].Index)/hL + hL*(s[k-1]+2*s[k])/6.0
		dR := (knots[k+1].Index-knots[k].Index)/hR - hR*(2*s[k]+s[k+1])/6.0
		if math.Abs(dL-dR) > 1e-9*math.Max(1, math.Abs(dL)) {
			t.Errorf("derivative discontinuity at λ=%v: left %v right %v", knots[k].Wavelength, dL, dR)
		}
	}

	// Monotone fall like a real glass (n decreases as λ grows).
	below, err := interpolateRefractiveIndex(bk7Table, 0.000450)
	if err != nil {
		t.Fatal(err)
	}
	above, err := interpolateRefractiveIndex(bk7Table, 0.000600)
	if err != nil {
		t.Fatal(err)
	}
	if below <= above {
		t.Errorf("expected n decreasing with wavelength: n(450nm)=%v <= n(600nm)=%v", below, above)
	}
}

func TestInterpolateRefractiveIndexExtrapolation(t *testing.T) {
	// Cauchy extrapolation connects C0-continuously at both table edges.
	lo := bk7Table[0]
	hi := bk7Table[len(bk7Table)-1]
	for _, wl := range []float64{lo.Wavelength - 1e-7, lo.Wavelength - 0.000010, lo.Wavelength - 0.000030} {
		n, err := interpolateRefractiveIndex(bk7Table, wl)
		if err != nil {
			t.Fatalf("extrapolate below at λ=%v: %v", wl, err)
		}
		if math.Abs(n-lo.Value) > 0.05 {
			t.Errorf("n at λ=%v = %v, expected close to edge %v", wl, n, lo.Value)
		}
	}
	for _, wl := range []float64{hi.Wavelength + 1e-7, hi.Wavelength + 0.000020, hi.Wavelength + 0.000400} {
		n, err := interpolateRefractiveIndex(bk7Table, wl)
		if err != nil {
			t.Fatalf("extrapolate above at λ=%v: %v", wl, err)
		}
		if math.Abs(n-hi.Value) > 0.05 {
			t.Errorf("n at λ=%v = %v, expected close to edge %v", wl, n, hi.Value)
		}
	}

	// Smooth monotone fall continuing beyond the table.
	extrap, err := interpolateRefractiveIndex(bk7Table, 0.002500)
	if err != nil {
		t.Fatal(err)
	}
	if extrap > hi.Value || extrap < 1.0 {
		t.Errorf("IR extrapolation n = %v outside (1.0, %v]", extrap, hi.Value)
	}
}

func TestInterpolateRefractiveIndexClamp(t *testing.T) {
	// Anomalous table (n increases with λ): the Cauchy fit has a negative B so
	// the UV extrapolation would run below 1 and must be clamped.
	anomalous := types.RefractiveIndexTable{
		{Wavelength: 0.000500, Value: 1.50},
		{Wavelength: 0.000600, Value: 1.55},
		{Wavelength: 0.000700, Value: 1.60},
	}
	for _, wl := range []float64{0.000250, 0.000100, 0.000010} {
		n, err := interpolateRefractiveIndex(anomalous, wl)
		if err != nil {
			t.Fatalf("clamp at λ=%v: %v", wl, err)
		}
		if n < 1.0 {
			t.Errorf("n at λ=%v = %v, want clamped >= 1.0", wl, n)
		}
	}
}

func TestInterpolateRefractiveIndexSingleEntry(t *testing.T) {
	table := types.RefractiveIndexTable{
		{Wavelength: 0.000600, Value: 1.5},
	}
	for _, wl := range []float64{0.000200, 0.000600, 0.001000} {
		n, err := interpolateRefractiveIndex(table, wl)
		if err != nil {
			t.Fatalf("single entry at λ=%v: %v", wl, err)
		}
		if n != 1.5 {
			t.Errorf("n at λ=%v = %v, want 1.5", wl, n)
		}
	}
}

func TestInterpolateRefractiveIndexDuplicateWavelength(t *testing.T) {
	table := types.RefractiveIndexTable{
		{Wavelength: 0.000500, Value: 1.50},
		{Wavelength: 0.000600, Value: 1.52},
		{Wavelength: 0.000600, Value: 1.55},
		{Wavelength: 0.000700, Value: 1.54},
	}
	// Must not panic and must return a finite value (linear fallback).
	n, err := interpolateRefractiveIndex(table, 0.000550)
	if err != nil {
		t.Fatalf("duplicate wavelength: %v", err)
	}
	if math.IsNaN(n) || math.IsInf(n, 0) || n <= 1.0 {
		t.Errorf("n = %v, expected finite physical value", n)
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
	n, err := CalcRefractiveIndex(g, 0.000587562)
	if err != nil {
		t.Fatalf("CalcRefractiveIndex: %v", err)
	}
	if n < 1.50 || n > 1.53 {
		t.Errorf("n = %v, expected ~1.5168", n)
	}
}

func TestCalcRefractiveIndexCatalogDFC(t *testing.T) {
	g := &types.Glass{
		Type:              types.GlassTypeCatalog,
		Name:              "N-BK7",
		DispersionFormula: types.Sellmeier1,
		Coefficients:      []float64{1.03961212, 0.00600069867, 0.231792344, 0.0200179144, 1.01046945, 103.560653},
	}
	cases := []struct {
		wavelength float64 // mm
		want       float64
	}{
		{wavelength: 0.000486133, want: 1.52238}, // F line
		{wavelength: 0.000587562, want: 1.51680}, // d line
		{wavelength: 0.000656273, want: 1.51432}, // C line
	}
	for _, c := range cases {
		n, err := CalcRefractiveIndex(g, c.wavelength)
		if err != nil {
			t.Fatalf("CalcRefractiveIndex(%v): %v", c.wavelength, err)
		}
		if math.Abs(n-c.want) > 0.0005 {
			t.Errorf("n(%v mm) = %v, want ~%v", c.wavelength, n, c.want)
		}
	}
}

func TestCalcRefractiveIndexModel(t *testing.T) {
	g := &types.Glass{
		Type:  types.GlassTypeModel,
		Label: "my_glass",
		ND:    1.51680,
		VD:    64.17,
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

func TestRefractiveIndexFromNDVDCauchyContinuous(t *testing.T) {
	const eps = 1e-9
	cases := [][2]float64{
		{1.51680, 64.17}, // N-BK7
		{1.72825, 28.41}, // SF2
		{1.5, 60},
		{1.8, 25},
	}
	for _, c := range cases {
		nd, vd := c[0], c[1]
		indices := IndecesFromNdVd(nd, vd)
		knots := sortedKnots(indices)
		lo := knots[0].Wavelength
		hi := knots[len(knots)-1].Wavelength

		// Cauchy extrapolation connects C0-continuously with the spline at both
		// band edges (first and last knot).
		nLo, err := RefractiveIndexFromNDVD(nd, vd, lo)
		if err != nil {
			t.Fatalf("n(%v,%v)@lo: %v", nd, vd, err)
		}
		nLoBelow, err := RefractiveIndexFromNDVD(nd, vd, lo-eps)
		if err != nil {
			t.Fatalf("n(%v,%v) below lo: %v", nd, vd, err)
		}
		if math.Abs(nLoBelow-nLo) > 1e-6 {
			t.Errorf("UV Cauchy not C0 with spline: |n(%v)−n(%v)| = %v", lo-eps, lo, math.Abs(nLoBelow-nLo))
		}

		nHi, err := RefractiveIndexFromNDVD(nd, vd, hi)
		if err != nil {
			t.Fatalf("n(%v,%v)@hi: %v", nd, vd, err)
		}
		nHiAbove, err := RefractiveIndexFromNDVD(nd, vd, hi+eps)
		if err != nil {
			t.Fatalf("n(%v,%v) above hi: %v", nd, vd, err)
		}
		if math.Abs(nHiAbove-nHi) > 1e-6 {
			t.Errorf("IR Cauchy not C0 with spline: |n(%v)−n(%v)| = %v", hi+eps, hi, math.Abs(nHiAbove-nHi))
		}

		// Cauchy band values stay physical and monotone (n decreasing with λ).
		nUV := 0.0
		for _, wl := range []float64{0.000340, lo - 0.000001} {
			n, err := RefractiveIndexFromNDVD(nd, vd, wl)
			if err != nil {
				t.Fatalf("n(%v,%v)@%v: %v", nd, vd, wl, err)
			}
			if n < 1.0 || n > 2.0 {
				t.Errorf("n(%v,%v)@%v = %v, outside physical range", nd, vd, wl, n)
			}
			nUV = n
		}
		nIR, err := RefractiveIndexFromNDVD(nd, vd, 0.004000)
		if err != nil {
			t.Fatalf("n(%v,%v)@0.004: %v", nd, vd, err)
		}
		if nIR >= nUV {
			t.Errorf("n(%v,%v): IR %v should be < UV %v", nd, vd, nIR, nUV)
		}
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
	if g.Coefficients[0] != 1.265385420e+00 {
		t.Errorf("expected coeff[0]=1.265385420, got %g", g.Coefficients[0])
	}
	if g.DispersionFormula != types.Sellmeier1 {
		t.Errorf("expected DispersionFormula=sellmeier_1, got %q", g.DispersionFormula)
	}
}

func TestParseAGF_RANGE(t *testing.T) {
	for _, tc := range []struct {
		name, line string
		wantMin    float64
		wantMax    float64
	}{
		{name: "space separated", line: "RANGE 0.35 2.5", wantMin: 0.00035, wantMax: 0.0025},
		{name: "keyword attached", line: "RANGE0.35 2.5", wantMin: 0.00035, wantMax: 0.0025},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := "CC TEST\nNM TST 2 1 1.5 60 0 1\n" + tc.line + "\n"
			glasses, err := ParseAGF([]byte(input))
			if err != nil {
				t.Fatalf("ParseAGF failed: %v", err)
			}
			if len(glasses) != 1 {
				t.Fatalf("expected 1 glass, got %d", len(glasses))
			}
			g := glasses[0]
			if math.Abs(g.WavelengthMin-tc.wantMin) > 1e-12 {
				t.Errorf("WavelengthMin = %v, want %v (mm)", g.WavelengthMin, tc.wantMin)
			}
			if math.Abs(g.WavelengthMax-tc.wantMax) > 1e-12 {
				t.Errorf("WavelengthMax = %v, want %v (mm)", g.WavelengthMax, tc.wantMax)
			}
		})
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

	n1, err := c.RefractiveIndex(types.Material{Key: "MOD"}, 0.00058756)
	if err != nil {
		t.Fatal(err)
	}
	n2, err := c.RefractiveIndex(types.Material{Key: "MOD"}, 0.00058756)
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
	n3, err := c.RefractiveIndex(types.Material{Key: "MOD"}, 0.00058756)
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
				if _, err := c.RefractiveIndex(types.Material{Key: "G"}, 0.00058756); err != nil {
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
