package wavefront

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/chief"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// wavefrontTestSystem is a singlet with an image plane (last surface), the
// same layout the rest of the test suite uses.
func wavefrontTestSystem(gc *glass.Catalog) types.System {
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.02, Thickness: 5.0, Material: types.Material{Key: "N-BK7"}, Diameter: 30.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.02, Thickness: 100.0, Material: types.Material{}, Diameter: 30.0},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 30.0},
	}
	surface.Precompute(surfaces)
	return types.System{Surfaces: surfaces}
}

func wavefrontTestGC() *glass.Catalog {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	return gc
}

// TestFitFieldParaboloidFrozenMatchesDynamic verifies the frozen-pupil entry
// (the optimizer's DLS-consistent path) reproduces the chief-settled dynamic
// pupil result when given the settled pupil Z. Both on-axis (pupil-independent
// grid) and off-axis fields are checked.
func TestFitFieldParaboloidFrozenMatchesDynamic(t *testing.T) {
	gc := wavefrontTestGC()
	system := wavefrontTestSystem(gc)
	const wl = 0.00058756
	const refSurface = 2

	for _, fieldAngle := range []float64{0, 5} {
		fd := types.FieldDef{Angle: fieldAngle, Direction: []float64{0, 1}}
		results := chief.DetermineChiefRaysGrid(system, []types.FieldDef{fd}, refSurface, 64,
			gc, types.NewCircularJones(true), wl, false, types.GridPolar, nil, nil, nil)
		if len(results) == 0 || results[0].EntrancePupil == nil {
			t.Fatalf("field %v: no entrance pupil from chief", fieldAngle)
		}
		pupilZ := results[0].EntrancePupil.Center.Z

		frozen, err := FitFieldParaboloid(system, gc, fd, refSurface, 64, wl, 1.0, &pupilZ)
		if err != nil {
			t.Fatalf("field %v frozen: %v", fieldAngle, err)
		}
		dyn, err := FitFieldParaboloid(system, gc, fd, refSurface, 64, wl, 1.0, nil)
		if err != nil {
			t.Fatalf("field %v dynamic: %v", fieldAngle, err)
		}
		if math.IsNaN(frozen.X2) || math.IsInf(frozen.X2, 0) {
			t.Fatalf("field %v: non-finite paraboloid", fieldAngle)
		}

		for name, a := range map[string]float64{
			"x2": frozen.X2, "y2": frozen.Y2, "xy": frozen.XY,
			"x": frozen.X, "y": frozen.Y, "constant": frozen.Constant,
		} {
			var b float64
			switch name {
			case "x2":
				b = dyn.X2
			case "y2":
				b = dyn.Y2
			case "xy":
				b = dyn.XY
			case "x":
				b = dyn.X
			case "y":
				b = dyn.Y
			case "constant":
				b = dyn.Constant
			}
			if d := closeEnough(a, b, 1e-6, 1e-8); d != 0 {
				t.Errorf("field %v %s: frozen %v vs dynamic %v (diff %v)", fieldAngle, name, a, b, d)
			}
		}
		if frozen.RMSResidual <= 0 {
			t.Errorf("field %v: RMSResidual = %v, want > 0 (singlet spherical aberration)", fieldAngle, frozen.RMSResidual)
		}
	}
}

func closeEnough(a, b, relTol, absTol float64) float64 {
	if math.Abs(a-b) <= relTol*math.Max(math.Abs(a), math.Abs(b))+absTol {
		return 0
	}
	return math.Abs(a - b)
}

// TestFitFieldParaboloidMatchesCompute verifies the fit matches the full
// wavefront analysis (paraboloid section) of the on-axis field, where the
// frozen-pupil grid is pupil-independent.
func TestFitFieldParaboloidMatchesCompute(t *testing.T) {
	gc := wavefrontTestGC()
	system := wavefrontTestSystem(gc)
	const wl = 0.00058756

	pab, err := FitFieldParaboloid(system, gc, types.FieldDef{Angle: 0, Direction: []float64{0, 1}}, 2, 64, wl, 1.0, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Compute(system, gc, []types.FieldDef{{Angle: 0, Direction: []float64{0, 1}}}, []float64{wl}, Options{NumRays: 64, ReferenceSurface: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Fields) == 0 {
		t.Fatal("no wavefront result")
	}
	want := res.Fields[0].Paraboloid
	if d := closeEnough(pab.X2, want.X2, 1e-6, 1e-9); d != 0 {
		t.Errorf("x2: %v vs %v (diff %v)", pab.X2, want.X2, d)
	}
	if d := closeEnough(pab.Y2, want.Y2, 1e-6, 1e-9); d != 0 {
		t.Errorf("y2: %v vs %v (diff %v)", pab.Y2, want.Y2, d)
	}
	if d := closeEnough(pab.Defocus, want.Defocus, 1e-6, 1e-9); d != 0 {
		t.Errorf("defocus: %v vs %v (diff %v)", pab.Defocus, want.Defocus, d)
	}
	if d := closeEnough(pab.Astigmatism, want.Astigmatism, 1e-6, 1e-9); d != 0 {
		t.Errorf("astigmatism: %v vs %v (diff %v)", pab.Astigmatism, want.Astigmatism, d)
	}
	if d := closeEnough(pab.Tilt, want.Tilt, 1e-6, 1e-9); d != 0 {
		t.Errorf("tilt: %v vs %v (diff %v)", pab.Tilt, want.Tilt, d)
	}
	if d := closeEnough(pab.RMSResidual, want.RMSResidual, 1e-5, 1e-9); d != 0 {
		t.Errorf("rms_residual: %v vs %v (diff %v)", pab.RMSResidual, want.RMSResidual, d)
	}
}
