package constraint

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

func testGlassCatalog() *glass.Catalog {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	return gc
}

// Flat all-AIR system: stop is surface 3 (dia 8). At an oblique field the
// pupil grid is shifted off-axis and clips the top edge of surfaces 1 and 2.
func clippedSystem() []types.Surface {
	return []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0, Thickness: 10.0, Material: types.Material{}, Diameter: 20.0},
		{ID: 2, Type: types.Sphere, Curvature: 0, Thickness: 10.0, Material: types.Material{}, Diameter: 10.0},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 100.0, Material: types.Material{}, Diameter: 8.0},
	}
}

func surfaceDiameter(surfaces []types.Surface, id int) float64 {
	for _, s := range surfaces {
		if s.ID == id {
			return s.Diameter
		}
	}
	return 0
}

func TestBeamMeasuresOnAxis(t *testing.T) {
	gc := testGlassCatalog()
	surfaces := clippedSystem()
	surface.Precompute(surfaces)

	for _, id := range []int{1, 2, 3} {
		clearance := Evaluate(types.ConstraintOperand{Measure: types.MeasureBeamClearance, Surface: id, Active: true}, surfaces, 0, gc, 16, 1.0, 0, 0)
		if clearance <= 0 {
			t.Errorf("field 0 surface %d beam_clearance = %v, want > 0", id, clearance)
		}
		diameter := Evaluate(types.ConstraintOperand{Measure: types.MeasureBeamDiameter, Surface: id, Active: true}, surfaces, 0, gc, 16, 1.0, 0, 0)
		if diameter <= 0 {
			t.Errorf("field 0 surface %d beam_diameter = %v, want > 0", id, diameter)
		}
	}

	if vf := Evaluate(types.ConstraintOperand{Measure: types.MeasureVignettingFactor, Active: true}, surfaces, 0, gc, 16, 1.0, 0, 0); vf != 1.0 {
		t.Errorf("field 0 vignetting_factor = %v, want 1.0", vf)
	}
}

func TestBeamMeasuresClipped(t *testing.T) {
	gc := testGlassCatalog()
	surfaces := clippedSystem()
	surface.Precompute(surfaces)

	clearance := Evaluate(types.ConstraintOperand{Measure: types.MeasureBeamClearance, Surface: 2, Active: true}, surfaces, 20, gc, 16, 1.0, 0, 0)
	if clearance >= 0 {
		t.Errorf("field 20 surface 2 beam_clearance = %v, want < 0", clearance)
	}

	diameter := Evaluate(types.ConstraintOperand{Measure: types.MeasureBeamDiameter, Surface: 2, Active: true}, surfaces, 20, gc, 16, 1.0, 0, 0)
	if diameter <= surfaceDiameter(surfaces, 2) {
		t.Errorf("field 20 surface 2 beam_diameter = %v, want > %v (surface aperture)", diameter, surfaceDiameter(surfaces, 2))
	}

	if vf := Evaluate(types.ConstraintOperand{Measure: types.MeasureVignettingFactor, Active: true}, surfaces, 20, gc, 16, 1.0, 0, 0); vf >= 1.0 {
		t.Errorf("field 20 vignetting_factor = %v, want < 1.0", vf)
	}
}

func TestBeamMeasuresConsistent(t *testing.T) {
	gc := testGlassCatalog()
	surfaces := clippedSystem()
	surface.Precompute(surfaces)

	for _, field := range []float64{0, 10, 20} {
		for _, id := range []int{1, 2, 3} {
			clearance := Evaluate(types.ConstraintOperand{Measure: types.MeasureBeamClearance, Surface: id, Active: true}, surfaces, field, gc, 16, 1.0, 0, 0)
			diameter := Evaluate(types.ConstraintOperand{Measure: types.MeasureBeamDiameter, Surface: id, Active: true}, surfaces, field, gc, 16, 1.0, 0, 0)
			want := surfaceDiameter(surfaces, id)/2 - diameter/2
			if math.Abs(clearance-want) > 1e-9 {
				t.Errorf("field %.0f surface %d: clearance %.6f != dia/2 - beam/2 = %.6f", field, id, clearance, want)
			}
		}
	}
}

func TestBeamMeasuresWideningAperture(t *testing.T) {
	gc := testGlassCatalog()
	base := clippedSystem()
	surface.Precompute(base)

	wide := make([]types.Surface, len(base))
	copy(wide, base)
	wide[1].Diameter = 14.0
	surface.Precompute(wide)

	cBase := Evaluate(types.ConstraintOperand{Measure: types.MeasureBeamClearance, Surface: 2, Active: true}, base, 10, gc, 16, 1.0, 0, 0)
	cWide := Evaluate(types.ConstraintOperand{Measure: types.MeasureBeamClearance, Surface: 2, Active: true}, wide, 10, gc, 16, 1.0, 0, 0)
	if math.Abs((cWide-cBase)-2.0) > 1e-6 {
		t.Errorf("clearance increase on widening aperture = %v, want 2.0", cWide-cBase)
	}

	vBase := Evaluate(types.ConstraintOperand{Measure: types.MeasureVignettingFactor, Active: true}, base, 20, gc, 16, 1.0, 0, 0)
	vWide := Evaluate(types.ConstraintOperand{Measure: types.MeasureVignettingFactor, Active: true}, wide, 20, gc, 16, 1.0, 0, 0)
	if vWide < vBase {
		t.Errorf("vignetting_factor decreased after widening aperture: %v -> %v", vBase, vWide)
	}
}

func TestNewMeasuresInactive(t *testing.T) {
	gc := testGlassCatalog()
	surfaces := clippedSystem()
	surface.Precompute(surfaces)

	for _, op := range []types.ConstraintOperand{
		{Measure: types.MeasureBeamClearance, Surface: 2, Active: false},
		{Measure: types.MeasureVignettingFactor, Active: false},
		{Measure: types.MeasureBeamDiameter, Surface: 2, Active: false},
	} {
		if v := Evaluate(op, surfaces, 10.0, gc, 16, 1.0, 0, 0); v != 0 {
			t.Errorf("inactive %s returned %v, want 0", op.Measure, v)
		}
	}
}

// TestEdgeThicknessBackSurface is a regression test for the improvement report
// (3.4): the edge_thickness constraint previously reused `target` as the back
// surface ID. It now takes it via the explicit `back_surface` field.
func TestEdgeThicknessBackSurface(t *testing.T) {
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 5.0, Material: types.Material{Key: "N-BK7"}, Diameter: 20.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 100.0, Material: types.Material{}, Diameter: 20.0},
	}
	surface.Precompute(surfaces)

	// edge = front.Thickness + sag(back) - sag(front) at h = 10.
	h := 10.0
	sag := func(c float64) float64 {
		if c == 0 {
			return 0
		}
		R := 1 / c
		return h * h / (R * (1 + math.Sqrt(1-h*h/(R*R))))
	}
	want := 5.0 + sag(-0.01) - sag(0.01)

	op := types.ConstraintOperand{
		Kind:        types.ConstraintInequalityLower,
		Measure:     types.MeasureEdgeThickness,
		Surface:     1,
		BackSurface: 2,
		Lower:       1.0,
		Weight:      1.0,
		Active:      true,
	}
	got := evaluateEdgeThickness(surfaces, op.Surface, op.BackSurface)
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("evaluateEdgeThickness(s1,s2) = %v, want %v", got, want)
	}
}

// TestNextSurfaceID pins the array-order back-surface resolution: the next
// surface in the slice (not ID+1), and 0 for the last / unknown surface.
func TestNextSurfaceID(t *testing.T) {
	surfaces := []types.Surface{{ID: 1}, {ID: 2}, {ID: 4}, {ID: 5}}
	if got := nextSurfaceID(surfaces, 2); got != 4 {
		t.Errorf("nextSurfaceID(2) = %d, want 4", got)
	}
	if got := nextSurfaceID(surfaces, 5); got != 0 {
		t.Errorf("nextSurfaceID(last) = %d, want 0", got)
	}
	if got := nextSurfaceID(surfaces, 99); got != 0 {
		t.Errorf("nextSurfaceID(unknown) = %d, want 0", got)
	}
}

// TestEdgeThicknessAutoDerivesBackSurface checks that an edge_thickness
// constraint with no back_surface uses the next surface in array order even
// when the IDs are non-contiguous (silently returning 0 would be a bug).
func TestEdgeThicknessAutoDerivesBackSurface(t *testing.T) {
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0, Thickness: 5.0, Material: types.Material{}, Diameter: 20.0},
		{ID: 2, Type: types.Sphere, Curvature: 0.01, Thickness: 5.0, Material: types.Material{}, Diameter: 20.0},
		{ID: 4, Type: types.Sphere, Curvature: -0.01, Thickness: 100.0, Material: types.Material{}, Diameter: 20.0},
		{ID: 5, Type: types.Sphere, Curvature: 0, Thickness: 0.0, Material: types.Material{}, Diameter: 20.0},
	}
	want := evaluateEdgeThickness(surfaces, 2, 4)
	op := types.ConstraintOperand{
		Kind:    types.ConstraintInequalityLower,
		Measure: types.MeasureEdgeThickness,
		Surface: 2,
		Lower:   1.0,
		Weight:  1.0,
		Active:  true,
	}
	got := Evaluate(op, surfaces, 0, nil, 0, 0, 0, 0)
	if math.Abs(got-want) > 1e-12 {
		t.Errorf("Evaluate(edge_thickness, surface 2) = %v, want %v (auto back surface 4)", got, want)
	}
}
