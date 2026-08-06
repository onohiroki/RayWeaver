package importer

import (
	"math"
	"testing"
)

func TestFlattenNegativeThickness_SingleSmall(t *testing.T) {
	input := `STOP 1
SURF 1
  TYPE STANDARD
  CURV 0.0
  THIC -0.2
  DIAM 4.0
SURF 2
  TYPE STANDARD
  CURV 0.5
  THIC 3.0
  GLAS SK16
SURF 3
  TYPE STANDARD
  CURV 0.0
  THIC 0.0
`
	result, err := ParseZemax(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Surfaces) != 3 {
		t.Fatalf("expected 3 surfaces, got %d", len(result.Surfaces))
	}
	// The single negative thickness on the stop reference plane is flipped.
	if result.Surfaces[0].Thickness != 0.2 {
		t.Errorf("surface 1 thickness: expected +0.2, got %g", result.Surfaces[0].Thickness)
	}
	// The stop itself is preserved.
	if result.StopSurface != 1 {
		t.Errorf("stop surface: expected 1, got %d", result.StopSurface)
	}
	// Downstream surfaces keep their thicknesses.
	if result.Surfaces[1].Thickness != 3.0 {
		t.Errorf("surface 2 thickness: expected 3.0, got %g", result.Surfaces[1].Thickness)
	}
}

func TestFlattenNegativeThickness_AllPositiveUntouched(t *testing.T) {
	input := `SURF 1
  TYPE STANDARD
  CURV 0.02
  THIC 5.0
  GLAS BK7
SURF 2
  TYPE STANDARD
  CURV 0.0
  THIC 0.0
`
	result, err := ParseZemax(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Surfaces[0].Thickness != 5.0 {
		t.Errorf("thickness changed though all positive: %g", result.Surfaces[0].Thickness)
	}
}

func TestFlattenNegativeThickness_MultipleNegativesUntouched(t *testing.T) {
	input := `SURF 1
  TYPE STANDARD
  CURV 0.0
  THIC -1.0
SURF 2
  TYPE STANDARD
  CURV 0.0
  THIC -2.0
SURF 3
  TYPE STANDARD
  CURV 0.0
  THIC 0.0
`
	result, err := ParseZemax(input)
	if err != nil {
		t.Fatal(err)
	}
	// A folded/return path (several negatives) must not be flattened.
	if result.Surfaces[0].Thickness != -1.0 || result.Surfaces[1].Thickness != -2.0 {
		t.Errorf("multi-negative fold path was modified: %g/%g",
			result.Surfaces[0].Thickness, result.Surfaces[1].Thickness)
	}
}

func TestFlattenNegativeThickness_HugeNegativeUntouched(t *testing.T) {
	input := `SURF 1
  TYPE STANDARD
  CURV 0.0
  THIC -90.0
SURF 2
  TYPE STANDARD
  CURV 0.0
  THIC 0.0
`
	result, err := ParseZemax(input)
	if err != nil {
		t.Fatal(err)
	}
	// Telecentric/return layouts with a large negative thickness stay as-is.
	if math.Abs(result.Surfaces[0].Thickness+90.0) > 1e-12 {
		t.Errorf("huge negative was flattened: %g", result.Surfaces[0].Thickness)
	}
}

func TestFlattenNegativeThickness_ConfigOverride(t *testing.T) {
	input := `SURF 1
  TYPE STANDARD
  CURV 0.02
  THIC 5.0
  GLAS BK7
SURF 2
  TYPE STANDARD
  CURV 0.0
  THIC 0.0
THIC 1 1 -0.5 0 0 0 1 1 1 0 0
THIC 1 2 4.0 0 0 0 1 1 1 0 0
`
	result, err := ParseZemax(input)
	if err != nil {
		t.Fatal(err)
	}
	// Config 1's single small negative override is flattened.
	if got := result.ConfigThickness[1][1]; got != 0.5 {
		t.Errorf("config 1 override: expected +0.5, got %g", got)
	}
	// Config 2's positive override is untouched.
	if got := result.ConfigThickness[2][1]; got != 4.0 {
		t.Errorf("config 2 override: expected 4.0, got %g", got)
	}
}
