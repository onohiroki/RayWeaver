package optimize

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

const (
	polyF = 0.00048613
	polyC = 0.00065627
)

func polyLens() []types.Surface {
	return []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.02, Thickness: 8.0,
			Material: types.Material{Key: "N-BK7"}, Diameter: 20.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 60.0,
			Material: types.Material{}, Diameter: 30.0},
	}
}

func polyGlass() *glass.Catalog {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	return gc
}

func TestAxialColorAndSecondarySpectrum(t *testing.T) {
	surfaces := polyLens()
	surface.Precompute(surfaces)
	gc := polyGlass()

	got := evaluateAxialColor(polyF, polyC, surfaces, gc)
	sys := types.System{Surfaces: surfaces}
	want := paraxial.Compute(sys, polyC, gc, 0, nil).SecondPrincipalFocus -
		paraxial.Compute(sys, polyF, gc, 0, nil).SecondPrincipalFocus
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("evaluateAxialColor = %v, want %v", got, want)
	}
	if math.Abs(got) < 1e-6 {
		t.Fatalf("axial colour = %v, want a non-zero dispersion shift", got)
	}

	set := []types.WavelengthSample{
		{Value: 0.00043583, Weight: 0.25},
		{Value: polyF, Weight: 0.25},
		{Value: 0.00058756, Weight: 0.25},
		{Value: polyC, Weight: 0.25},
	}
	sec := evaluateSecondarySpectrum(polyF, polyC, set, surfaces, gc)
	if sec < 0 || math.IsNaN(sec) || math.IsInf(sec, 0) {
		t.Fatalf("secondary spectrum = %v", sec)
	}
	if sec > math.Abs(got) {
		t.Errorf("secondary spectrum %v exceeds the F-C axial colour %v", sec, math.Abs(got))
	}
}

func TestFocalLengthUsesTermWavelength(t *testing.T) {
	surfaces := polyLens()
	surface.Precompute(surfaces)
	gc := polyGlass()
	fF := evaluateFocalLength(polyF, surfaces, gc)
	fC := evaluateFocalLength(polyC, surfaces, gc)
	if math.Abs(fF-fC) < 1e-9 {
		t.Fatalf("EFL at F and C are equal (%v); the term wavelength was ignored", fF)
	}
}

func TestWhiteBackFocusSpotSolve(t *testing.T) {
	surfaces := append(polyLens(), types.Surface{
		ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 10,
		Material: types.Material{}, Diameter: 40, AutoAperture: true,
	})
	surface.Precompute(surfaces)
	gc := polyGlass()
	opt := NewOptimizer(Config{
		Surfaces:            surfaces,
		Fields:              []types.FieldItem{{ID: 0, AngleDeg: 0, Weight: 1}},
		StopSurface:         1,
		NumRays:             32,
		GlassCatalog:        gc,
		Wavelengths:         polyWavelengths(),
		ReferenceWavelength: 0.00058756,
		MeritTerms: []MeritTerm{{
			Kind:        MeritSpotRMS,
			FieldIndex:  0,
			FieldWeight: 1,
			Wavelength:  0.00058756,
			WavWeight:   1,
			Weight:      1,
		}},
	})
	opt.SetBackFocusSolve(&types.BackFocusSolveConfig{
		Enabled: true, Type: "white", Objective: "spot",
		WavelengthSet: "design", WeightType: "uniform", NumRays: 32,
	})
	opt.EvaluateMerit(nil)
	diag := opt.BackFocusDiagnostics()
	if diag == nil {
		t.Fatal("back-focus diagnostics missing")
	}
	if diag.Method != "white" {
		t.Fatalf("back-focus method = %q, want white", diag.Method)
	}
}

func polyWavelengths() []types.WavelengthItem {
	return []types.WavelengthItem{
		{ID: 0, Value: polyF, Label: "F", Weight: 1},
		{ID: 1, Value: 0.00058756, Label: "d", Weight: 1},
		{ID: 2, Value: polyC, Label: "C", Weight: 1},
	}
}

func TestWhiteDiffractionTermsEvaluate(t *testing.T) {
	surfaces := append(polyLens(), types.Surface{
		ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 10,
		Material: types.Material{}, Diameter: 40, AutoAperture: true,
	})
	surface.Precompute(surfaces)
	gc := polyGlass()
	set := []types.WavelengthSample{
		{Value: polyF, Weight: 0.5},
		{Value: polyC, Weight: 0.5},
	}
	opt := NewOptimizer(Config{
		Surfaces:            surfaces,
		Fields:              []types.FieldItem{{ID: 0, AngleDeg: 0, Weight: 1}},
		StopSurface:         1,
		NumRays:             32,
		GlassCatalog:        gc,
		Wavelengths:         polyWavelengths(),
		ReferenceWavelength: 0.00058756,
		MeritTerms: []MeritTerm{
			{Kind: MeritPolychromaticStrehl, FieldIndex: 0, FieldWeight: 1,
				Wavelength: 0.00058756, WavWeight: 1, Weight: 1, Wavelengths: set},
			{Kind: MeritPolychromaticMTFSag, FieldIndex: 0, FieldWeight: 1,
				Wavelength: 0.00058756, WavWeight: 1, Weight: 1,
				Frequency: 10, Target: 0.2, Wavelengths: set},
		},
	})
	opt.SetDiffractionMTF(&types.DiffractionMTFConfig{
		NumRays: 32, GridSize: 16, Polarizations: []string{"RCP"},
	})
	got := opt.EvaluateMerit(nil)
	if got <= 0 || math.IsNaN(got) || math.IsInf(got, 0) {
		t.Fatalf("white diffraction merit = %v", got)
	}
}

func TestPolychromaticSpotTermEvaluates(t *testing.T) {
	surfaces := polyLens()
	surface.Precompute(surfaces)
	set := []types.WavelengthSample{
		{Value: polyF, Weight: 0.5},
		{Value: polyC, Weight: 0.5},
	}
	gc := polyGlass()
	term := MeritTerm{
		Kind:        MeritPolychromaticSpot,
		FieldIndex:  0,
		FieldWeight: 1,
		Wavelength:  0.00058756,
		WavWeight:   1,
		Weight:      1,
		Wavelengths: set,
	}
	opt := NewOptimizer(Config{
		Surfaces:     surfaces,
		Fields:       []types.FieldItem{{ID: 0, AngleDeg: 0, Weight: 1}},
		StopSurface:  1,
		NumRays:      32,
		MeritTerms:   []MeritTerm{term},
		GlassCatalog: gc,
	})
	got := opt.EvaluateMerit(nil)
	if got <= 0 || math.IsNaN(got) || math.IsInf(got, 0) {
		t.Fatalf("polychromatic spot merit = %v", got)
	}

	// A single-wavelength term at the same reference must also evaluate.
	single := term
	single.Wavelengths = nil
	single.Wavelength = polyF
	optSingle := NewOptimizer(Config{
		Surfaces:     surfaces,
		Fields:       []types.FieldItem{{ID: 0, AngleDeg: 0, Weight: 1}},
		StopSurface:  1,
		NumRays:      32,
		MeritTerms:   []MeritTerm{single},
		GlassCatalog: gc,
	})
	if v := optSingle.EvaluateMerit(nil); v <= 0 || math.IsNaN(v) {
		t.Fatalf("single-wavelength spot merit = %v", v)
	}
}
