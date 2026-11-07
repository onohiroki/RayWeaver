package spectral

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
)

func almost(a, b float64) bool { return math.Abs(a-b) < 1e-12 }

func TestDesignSamplesNormalisesAndExcludes(t *testing.T) {
	items := []types.WavelengthItem{
		{Value: 0.00058756, Label: "d", Weight: 1},
		{Value: 0.00048613, Label: "F", Weight: 1},
		{Value: 0.00065627, Label: "C", Weight: 1},
		{Value: 0.00043583, Label: "g", Weight: 0.5},
		{Value: 0.00058756, Label: "dup", Weight: 9},
		{Value: 0.00050000, Label: "off", Weight: 0},
	}
	got := DesignSamples(items)
	if len(got) != 4 {
		t.Fatalf("got %d samples, want 4: %+v", len(got), got)
	}
	var sum float64
	for _, s := range got {
		sum += s.Weight
	}
	if !almost(sum, 1) {
		t.Errorf("weights sum to %v, want 1", sum)
	}
	if !almost(got[3].RawWeight, 0.5) {
		t.Errorf("g raw weight = %v, want 0.5", got[3].RawWeight)
	}
	if got[0].Label != "d" || got[3].Label != "g" {
		t.Errorf("labels not preserved: %+v", got)
	}
}

func TestDesignSamplesLegacyAllZeroWeights(t *testing.T) {
	got := DesignSamples([]types.WavelengthItem{{Value: 0.0005}, {Value: 0.0006}})
	if len(got) != 2 || !almost(got[0].Weight, 0.5) {
		t.Fatalf("all-zero weights should mean equal weights, got %+v", got)
	}
}

func TestSpectralSamplesRangeStep(t *testing.T) {
	cfg := &types.SpectralConfig{Curve: "FLAT", Range: []float64{400, 500}, Step: 10}
	got, err := SpectralSamples(cfg, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 11 {
		t.Fatalf("got %d samples, want 11", len(got))
	}
	var sum float64
	for _, s := range got {
		sum += s.Weight
	}
	if !almost(sum, 1) {
		t.Errorf("weights sum to %v, want 1", sum)
	}
	if !almost(got[0].Value, 0.0004) || !almost(got[len(got)-1].Value, 0.0005) {
		t.Errorf("range endpoints wrong: %v .. %v", got[0].Value, got[len(got)-1].Value)
	}
}

func TestSpectralSamplesEntries(t *testing.T) {
	cfg := &types.SpectralConfig{Entries: []types.SpectralEntry{
		{Wavelength: 486.13, Relative: 1},
		{Wavelength: 656.27, Relative: 1},
	}}
	got, err := SpectralSamples(cfg, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !almost(got[0].Weight, 0.5) {
		t.Fatalf("entries not used: %+v", got)
	}
}

func TestExpandTermsDesignSum(t *testing.T) {
	r := Resolver{
		Design: []types.WavelengthSample{
			{Value: 0.0005, Weight: 0.75, RawWeight: 3},
			{Value: 0.0006, Weight: 0.25, RawWeight: 1},
		},
		Reference: 0.00058756,
	}
	terms := []types.MeritTerm{{Kind: "spot_rms", WavelengthSet: "design", Weight: 2}}
	out, err := ExpandTerms(terms, r, "sum")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("got %d terms, want 2", len(out))
	}
	if !almost(out[0].WavelengthWeight, 0.75) || !almost(out[0].Weight, 2) {
		t.Errorf("sum expansion weights wrong: %+v", out[0])
	}
	out, err = ExpandTerms(terms, r, "none")
	if err != nil {
		t.Fatal(err)
	}
	if !almost(out[1].WavelengthWeight, 1) {
		t.Errorf("none expansion should keep raw weight, got %+v", out[1])
	}
}

func TestExpandTermsAggregateKeepsSet(t *testing.T) {
	r := Resolver{
		Design: []types.WavelengthSample{{Value: 0.0005, Weight: 0.5}, {Value: 0.0006, Weight: 0.5}},
	}
	out, err := ExpandTerms([]types.MeritTerm{{
		Kind: "polychromatic_spot_rms", WavelengthSet: "design",
	}}, r, "sum")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || len(out[0].ResolvedWavelengths) != 2 {
		t.Fatalf("aggregate term should carry the set: %+v", out)
	}
	if !almost(out[0].Wavelength, r.Reference) {
		t.Errorf("aggregate wavelength = %v, want reference %v", out[0].Wavelength, r.Reference)
	}
}

func TestExpandTermsWhiteDiffractionKinds(t *testing.T) {
	r := Resolver{Design: []types.WavelengthSample{{Value: 0.0005, Weight: 1}}}
	for _, kind := range []string{"polychromatic_strehl", "polychromatic_mtf_sag", "polychromatic_mtf_tan"} {
		out, err := ExpandTerms([]types.MeritTerm{{Kind: kind, WavelengthSet: "design"}}, r, "sum")
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if len(out) != 1 || len(out[0].ResolvedWavelengths) != 1 {
			t.Fatalf("%s should be an aggregate term: %+v", kind, out)
		}
	}
}

func TestExpandTermsRejectsNonExpandable(t *testing.T) {
	r := Resolver{Design: []types.WavelengthSample{{Value: 0.0005, Weight: 1}}}
	if _, err := ExpandTerms([]types.MeritTerm{{
		Kind: "lateral_color", WavelengthSet: "design",
	}}, r, "sum"); err == nil {
		t.Fatal("expected an error for lateral_color + wavelength_set")
	}
}
