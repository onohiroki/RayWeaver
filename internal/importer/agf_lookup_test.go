package importer

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
)

func makeAGFGlasses() []types.Glass {
	return []types.Glass{
		{Name: "N-BK7", Type: types.GlassTypeCatalog, ND: 1.51680, VD: 64.17, Manufacturer: "SCHOTT",
			DispersionFormula: types.Sellmeier1, Coefficients: []float64{1.039, 0.006, 0.231, 0.020, 1.010, 103.560}},
		{Name: "L-LAL12", Type: types.GlassTypeCatalog, ND: 1.62004, VD: 36.41, Manufacturer: "OHARA",
			DispersionFormula: types.Sellmeier1, Coefficients: []float64{1.341, 0.009, 0.213, 0.047, 0.983, 117.458}},
		{Name: "F2", Type: types.GlassTypeCatalog, ND: 1.62004, VD: 36.37, Manufacturer: "SCHOTT",
			DispersionFormula: types.Sellmeier1, Coefficients: []float64{1.345, 0.009, 0.209, 0.047, 0.937, 111.886}},
	}
}

func TestEnhance_Zemax_ExactMatch(t *testing.T) {
	entries := []types.Glass{
		{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.51680, VD: 64.17},
		{Type: types.GlassTypeModel, Label: "F2", ND: 1.62004, VD: 36.37},
	}
	agf := makeAGFGlasses()

	got := EnhanceGlassEntriesFromAGF(entries, agf)

	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	if got[0].Type != types.GlassTypeCatalog {
		t.Errorf("expected entry[0] type=catalog, got %q", got[0].Type)
	}
	if got[0].Name != "N-BK7" {
		t.Errorf("expected entry[0] Name=N-BK7, got %q", got[0].Name)
	}
	if got[0].Label != "N-BK7" {
		t.Errorf("expected entry[0] Label=N-BK7 (unchanged), got %q", got[0].Label)
	}
	if len(got[0].Coefficients) != 6 {
		t.Errorf("expected 6 coefficients, got %d", len(got[0].Coefficients))
	}
	if got[0].Manufacturer != "SCHOTT" {
		t.Errorf("expected Manufacturer=SCHOTT, got %q", got[0].Manufacturer)
	}
}

func TestEnhance_Zemax_NoMatch(t *testing.T) {
	entries := []types.Glass{
		{Type: types.GlassTypeModel, Label: "UNKNOWN_GLASS", ND: 1.5, VD: 60.0},
	}
	agf := makeAGFGlasses()

	got := EnhanceGlassEntriesFromAGF(entries, agf)

	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got))
	}
	if got[0].Type != types.GlassTypeModel {
		t.Errorf("expected entry[0] type=model (unchanged), got %q", got[0].Type)
	}
}

func TestEnhance_CodeV_NormalizedMatch(t *testing.T) {
	entries := []types.Glass{
		{Type: types.GlassTypeModel, Label: "LLAL12", ND: 1.62004, VD: 36.41},
	}
	agf := makeAGFGlasses()

	got := EnhanceGlassEntriesFromAGF(entries, agf)

	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got))
	}
	if got[0].Type != types.GlassTypeCatalog {
		t.Errorf("expected type=catalog, got %q", got[0].Type)
	}
	if got[0].Name != "L-LAL12" {
		t.Errorf("expected Name=L-LAL12, got %q", got[0].Name)
	}
	if got[0].Manufacturer != "OHARA" {
		t.Errorf("expected Manufacturer=OHARA, got %q", got[0].Manufacturer)
	}
}

func TestEnhance_CodeV_UnderscoreSplitMatch(t *testing.T) {
	entries := []types.Glass{
		{Type: types.GlassTypeModel, Label: "LLAL12_OHARA", ND: 1.62004, VD: 36.41},
	}
	agf := makeAGFGlasses()

	got := EnhanceGlassEntriesFromAGF(entries, agf)

	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got))
	}
	if got[0].Type != types.GlassTypeCatalog {
		t.Errorf("expected type=catalog, got %q", got[0].Type)
	}
	if got[0].Name != "L-LAL12" {
		t.Errorf("expected Name=L-LAL12, got %q", got[0].Name)
	}
	if got[0].Label != "LLAL12_OHARA" {
		t.Errorf("expected Label=LLAL12_OHARA (unchanged), got %q", got[0].Label)
	}
}

func TestEnhance_CodeV_UnderscoreSplit_NoMatchWrongMfr(t *testing.T) {
	entries := []types.Glass{
		{Type: types.GlassTypeModel, Label: "LLAL12_SCHOTT", ND: 1.62004, VD: 36.41},
	}
	agf := makeAGFGlasses()

	got := EnhanceGlassEntriesFromAGF(entries, agf)

	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got))
	}
	// OHARA != SCHOTT, so no match
	if got[0].Type != types.GlassTypeModel {
		t.Errorf("expected type=model (manufacturer mismatch), got %q", got[0].Type)
	}
}

func TestEnhance_CodeV_ExactMatchFirst(t *testing.T) {
	entries := []types.Glass{
		{Type: types.GlassTypeModel, Label: "F2", ND: 1.62004, VD: 36.37},
	}
	agf := makeAGFGlasses()

	got := EnhanceGlassEntriesFromAGF(entries, agf)

	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got))
	}
	if got[0].Type != types.GlassTypeCatalog {
		t.Errorf("expected type=catalog, got %q", got[0].Type)
	}
	if got[0].Name != "F2" {
		t.Errorf("expected Name=F2, got %q", got[0].Name)
	}
}

func TestEnhance_EmptyEntries(t *testing.T) {
	got := EnhanceGlassEntriesFromAGF(nil, makeAGFGlasses())
	if len(got) != 0 {
		t.Errorf("expected empty, got %d", len(got))
	}
}

func TestEnhance_EmptyAGF(t *testing.T) {
	entries := []types.Glass{
		{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.51680, VD: 64.17},
	}
	got := EnhanceGlassEntriesFromAGF(entries, nil)
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got))
	}
	if got[0].Type != types.GlassTypeModel {
		t.Errorf("expected type=model (unchanged), got %q", got[0].Type)
	}
}

func TestLookupGlassCodeVNames(t *testing.T) {
	cases := []struct {
		name  string
		wantN float64
		ok    bool
	}{
		{"NBK7", 1.5168, true},        // N-BK7, hyphen stripped
		{"NBK7_SCHOTT", 1.5168, true}, // + manufacturer suffix
		{"SF5", 1.6727, true},         // plain key
		{"HLAF2", 1.7432, true},       // H_LAF2, underscore stripped
		{"NOTAGLASS", 0, false},
	}
	for _, c := range cases {
		nd, _, ok := LookupGlass(c.name)
		if ok != c.ok {
			t.Errorf("%s: ok = %v, want %v", c.name, ok, c.ok)
			continue
		}
		if c.ok && math.Abs(nd-c.wantN) > 1e-9 {
			t.Errorf("%s: nd = %g, want %g", c.name, nd, c.wantN)
		}
	}
}
