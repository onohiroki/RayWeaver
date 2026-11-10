package optimize

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/cam"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/paraxial"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// camRingSurfaces is the three-element layout the cam package uses: three
// airspaces (after surfaces 2 and 4, and the back focus after 6) against three
// conditions — EFL, focus on the plane, plane held at one Z.
func camRingSurfaces() []types.Surface {
	return []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.02, Thickness: 6, Material: types.Material{Key: "N-BK7"}, Diameter: 40},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 12, Material: types.Material{}, Diameter: 40},
		{ID: 3, Type: types.Sphere, Curvature: 0.03, Thickness: 5, Material: types.Material{Key: "N-BK7"}, Diameter: 40},
		{ID: 4, Type: types.Sphere, Curvature: -0.02, Thickness: 10, Material: types.Material{}, Diameter: 40},
		{ID: 5, Type: types.Sphere, Curvature: 0.015, Thickness: 6, Material: types.Material{Key: "N-BK7"}, Diameter: 40},
		{ID: 6, Type: types.Sphere, Curvature: -0.03, Thickness: 4, Material: types.Material{}, Diameter: 40},
		{ID: 7, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 40},
	}
}

// TestCamCouplingSolvesTheLinkageEveryEvaluation pins optimization.cam_solve:
// the cam airspaces are part of the geometry, so every evaluation solves them
// before the merit is measured. Without the coupling the two positions share
// one geometry (no airspace freedom) and their EFLs cannot differ, which is
// exactly the trade a zoom optimizer must not be allowed to make.
func TestCamCouplingSolvesTheLinkageEveryEvaluation(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	base := camRingSurfaces()
	surface.Precompute(base)
	baseEFL := paraxial.Compute(types.System{Surfaces: base}, types.DefaultWavelength, gc, 0, nil).FocalLength
	planeZ := base[len(base)-1].PhysicalZ
	if baseEFL == 0 {
		t.Fatal("fixture is afocal")
	}

	mkConfig := func(id string) ConfigInput {
		s := camRingSurfaces()
		surface.Precompute(s)
		return ConfigInput{
			ID: id, Weight: 1, StopSurface: 1, RefSurface: 7,
			Surfaces:    s,
			Fields:      []types.FieldItem{{ID: 0, AngleDeg: 0, Weight: 1}},
			Wavelengths: []types.WavelengthItem{{ID: 0, Value: 0.00058756, Weight: 1}},
			MeritTerms:  []types.MeritTerm{{Kind: MeritSpotRMS, Field: 0, Weight: 1}},
		}
	}
	// One shared curvature the optimizer may move: it changes both positions'
	// EFL together unless the cam re-solves the airspaces.
	shared := []types.SharedVariable{{
		Name: "s5_curvature", Min: -0.05, Max: 0.05, Active: true,
		Bindings: []types.SharedVariableBinding{
			{Config: "wide", ID: 5, Param: "curvature"},
			{Config: "tele", ID: 5, Param: "curvature"},
		},
	}}
	opts := cam.Options{
		Surfaces:       []int{2, 4, 6},
		ImagePlane:     7,
		ImagePlaneZ:    planeZ,
		HoldImagePlane: true,
		StopSurface:    1,
		Wavelength:     types.DefaultWavelength,
	}
	newOpt := func() *Optimizer {
		return NewMultiOptimizer([]ConfigInput{mkConfig("wide"), mkConfig("tele")},
			shared, nil, nil, gc, 1, 0.01, 1e-6, 1e-6, 1.0, 16, 100, 0, nil, nil, 0, 0, false, false, nil, nil)
	}
	state := func(o *Optimizer, x []float64, id string) (float64, float64) {
		surfaces, _, _ := o.applyVariables(x)
		s := surfaces[id]
		surface.Precompute(s)
		res := paraxial.Compute(types.System{Surfaces: s, StopSurface: 1}, types.DefaultWavelength, gc, 0, nil)
		return res.FocalLength, s[len(s)-1].PhysicalZ
	}

	x0 := newOpt().InitialState()
	targets := map[string]float64{"wide": baseEFL * 0.96, "tele": baseEFL * 1.04}

	coupled := newOpt()
	coupled.SetCamCoupling(opts, targets)
	gotWide, zWide := state(coupled, x0, "wide")
	gotTele, zTele := state(coupled, x0, "tele")
	if math.Abs(gotWide-targets["wide"]) > 1e-3 {
		t.Errorf("wide EFL = %v, want the cam target %v", gotWide, targets["wide"])
	}
	if math.Abs(gotTele-targets["tele"]) > 1e-3 {
		t.Errorf("tele EFL = %v, want the cam target %v", gotTele, targets["tele"])
	}
	// One detector: both positions land on the same image-plane Z.
	if math.Abs(zWide-planeZ) > 1e-6 || math.Abs(zTele-planeZ) > 1e-6 {
		t.Errorf("image plane Z = %v / %v, want the held %v", zWide, zTele, planeZ)
	}
	// The airspaces must differ per position — that is the linkage.
	sw, _, _ := coupled.applyVariables(x0)
	wideGap := sw["wide"][1].Thickness
	teleGap := sw["tele"][1].Thickness
	if math.Abs(wideGap-teleGap) < 1e-6 {
		t.Errorf("both positions share airspace %v; the cam was not applied", wideGap)
	}
}
