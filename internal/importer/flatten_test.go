package importer

import "testing"

func TestZeroNegativeDummy_SingleDummyZeroed(t *testing.T) {
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
	// The negative thickness on the zero-power dummy plane is set to 0.
	if result.Surfaces[0].Thickness != 0 {
		t.Errorf("surface 1 thickness: expected 0, got %g", result.Surfaces[0].Thickness)
	}
	// The dummy held the stop, so the stop is dropped.
	if result.StopSurface != 0 {
		t.Errorf("stop surface: expected 0 (dropped), got %d", result.StopSurface)
	}
	// Downstream surfaces keep their thicknesses.
	if result.Surfaces[1].Thickness != 3.0 {
		t.Errorf("surface 2 thickness: expected 3.0, got %g", result.Surfaces[1].Thickness)
	}
}

func TestZeroNegativeDummy_AllPositiveUntouched(t *testing.T) {
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

func TestZeroNegativeDummy_MultipleDummiesZeroed(t *testing.T) {
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
	// Every zero-power non-mirror negative is zeroed regardless of count.
	if result.Surfaces[0].Thickness != 0 || result.Surfaces[1].Thickness != 0 {
		t.Errorf("dummy negatives not zeroed: %g/%g",
			result.Surfaces[0].Thickness, result.Surfaces[1].Thickness)
	}
}

func TestZeroNegativeDummy_PoweredSurfaceKept(t *testing.T) {
	input := `SURF 1
  TYPE STANDARD
  CURV 0.5
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
	// A powered surface (Curvature != 0) is a real element, not a dummy.
	if result.Surfaces[0].Thickness != -90.0 {
		t.Errorf("powered negative was zeroed: %g", result.Surfaces[0].Thickness)
	}
}

func TestZeroNegativeDummy_MirrorMaterialKept(t *testing.T) {
	input := `SURF 1
  TYPE STANDARD
  CURV 0.0
  THIC -40.0
  GLAS MIRROR
SURF 2
  TYPE STANDARD
  CURV 0.0
  THIC 0.0
`
	result, err := ParseZemax(input)
	if err != nil {
		t.Fatal(err)
	}
	// A mirror is a real fold, not a dummy: it becomes a reflect decenter
	// surface whose (previously negative) thickness is made positive.
	if result.Surfaces[0].Thickness != 40.0 {
		t.Errorf("mirror thickness: expected +40 (folded), got %g", result.Surfaces[0].Thickness)
	}
	if !result.Surfaces[0].Reflects() {
		t.Error("mirror surface should carry a reflect decenter step")
	}
	if result.Surfaces[0].Material != "AIR" {
		t.Errorf("mirror material: expected AIR, got %q", result.Surfaces[0].Material)
	}
}

func TestZeroNegativeDummy_StopOnNonZeroedKept(t *testing.T) {
	input := `STOP 2
SURF 1
  TYPE STANDARD
  CURV 0.0
  THIC -0.2
SURF 2
  TYPE STANDARD
  CURV 0.0
  THIC 3.0
SURF 3
  TYPE STANDARD
  CURV 0.0
  THIC 0.0
`
	result, err := ParseZemax(input)
	if err != nil {
		t.Fatal(err)
	}
	// Surface 1 is zeroed; the stop sits on surface 2 and is preserved.
	if result.Surfaces[0].Thickness != 0 {
		t.Errorf("dummy not zeroed: %g", result.Surfaces[0].Thickness)
	}
	if result.StopSurface != 2 {
		t.Errorf("stop surface: expected 2 (kept), got %d", result.StopSurface)
	}
}

func TestZeroNegativeDummy_ConfigOverride(t *testing.T) {
	input := `SURF 1
  TYPE STANDARD
  CURV 0.0
  THIC 5.0
SURF 2
  TYPE STANDARD
  CURV 0.02
  THIC 4.0
SURF 3
  TYPE STANDARD
  CURV 0.0
  THIC 0.0
THIC 1 1 -0.5 0 0 0 1 1 1 0 0
THIC 2 1 -2.0 0 0 0 1 1 1 0 0
THIC 3 1 4.0 0 0 0 1 1 1 0 0
`
	result, err := ParseZemax(input)
	if err != nil {
		t.Fatal(err)
	}
	// Surface 1 is a curv-0 dummy: its negative override is zeroed.
	if got := result.ConfigThickness[1][1]; got != 0 {
		t.Errorf("config 1 surf 1 override: expected 0, got %g", got)
	}
	// Surface 2 is powered: its negative override stays as-is.
	if got := result.ConfigThickness[1][2]; got != -2.0 {
		t.Errorf("config 1 surf 2 override: expected -2.0, got %g", got)
	}
	// Positive override is untouched.
	if got := result.ConfigThickness[1][3]; got != 4.0 {
		t.Errorf("config 1 surf 3 override: expected 4.0, got %g", got)
	}
}
