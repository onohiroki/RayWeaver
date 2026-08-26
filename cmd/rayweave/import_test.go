package main

import (
	"testing"

	"github.com/hiroki/rayweaver/internal/importer"
	"github.com/hiroki/rayweaver/internal/types"
)

func TestImportedReferenceWavelength_ExplicitREF(t *testing.T) {
	r := &importer.ParseResult{
		ReferenceWavelengthIdx: 1,
		Wavelengths: []types.WavelengthItem{
			{Value: 0.0006563},
			{Value: 0.0005876},
			{Value: 0.0004861},
		},
	}
	got := importedReferenceWavelength(r)
	if got != 0.0005876 {
		t.Errorf("expected 0.0005876, got %g", got)
	}
}

func TestImportedReferenceWavelength_DefaultOdd(t *testing.T) {
	r := &importer.ParseResult{
		ReferenceWavelengthIdx: -1,
		Wavelengths: []types.WavelengthItem{
			{Value: 0.0006563},
			{Value: 0.0005876},
			{Value: 0.0004861},
		},
	}
	got := importedReferenceWavelength(r)
	want := 0.0005876 // middle after ascending sort [486, 587, 656]
	if got != want {
		t.Errorf("expected %g, got %g", want, got)
	}
}

func TestImportedReferenceWavelength_DefaultEven(t *testing.T) {
	r := &importer.ParseResult{
		ReferenceWavelengthIdx: -1,
		Wavelengths: []types.WavelengthItem{
			{Value: 0.001000},
			{Value: 0.000800},
			{Value: 0.000600},
			{Value: 0.000400},
		},
	}
	got := importedReferenceWavelength(r)
	want := 0.000800 // index 2 after ascending sort [400, 600, 800, 1000]
	if got != want {
		t.Errorf("expected %g, got %g", want, got)
	}
}

func TestImportedReferenceWavelength_DefaultSingle(t *testing.T) {
	r := &importer.ParseResult{
		ReferenceWavelengthIdx: -1,
		Wavelengths: []types.WavelengthItem{
			{Value: 0.0005876},
		},
	}
	got := importedReferenceWavelength(r)
	want := 0.0005876
	if got != want {
		t.Errorf("expected %g, got %g", want, got)
	}
}

func TestImportedReferenceWavelength_DefaultEmpty(t *testing.T) {
	r := &importer.ParseResult{
		ReferenceWavelengthIdx: -1,
		Wavelengths:            []types.WavelengthItem{},
	}
	got := importedReferenceWavelength(r)
	if got != types.DefaultWavelength {
		t.Errorf("expected DefaultWavelength %g, got %g", types.DefaultWavelength, got)
	}
}

func TestImportedReferenceWavelength_OutOfRangeREF(t *testing.T) {
	r := &importer.ParseResult{
		ReferenceWavelengthIdx: 10,
		Wavelengths: []types.WavelengthItem{
			{Value: 0.0006563},
			{Value: 0.0005876},
			{Value: 0.0004861},
		},
	}
	got := importedReferenceWavelength(r)
	want := 0.0005876 // out of range falls through to default
	if got != want {
		t.Errorf("expected %g, got %g", want, got)
	}
}
