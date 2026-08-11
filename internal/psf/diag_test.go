package psf

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

func tripletSystem() (types.System, *glass.Catalog) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "SK18", ND: 1.63854, VD: 55.42})
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "SF12", ND: 1.64831, VD: 33.84})
	// US2645157 triplet, surfaces as float64 curvature.
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 1 / 10.2871491742, Thickness: 1.524, Material: types.Material{Key: "SK18"}, Diameter: 10.0},
		{ID: 2, Type: types.Sphere, Curvature: 1 / -239.3967954752, Thickness: 2.3368, Material: types.Material{}, Diameter: 10.0},
		{ID: 3, Type: types.Sphere, Curvature: 1 / -12.8269871730, Thickness: 0.508, Material: types.Material{Key: "SF12"}, Diameter: 6.0},
		{ID: 4, Type: types.Sphere, Curvature: 1 / 10.5917184406, Thickness: 1.4986, Material: types.Material{}, Diameter: 6.0},
		{ID: 5, Type: types.Sphere, Curvature: 0, Thickness: 1.016, Material: types.Material{}, Diameter: 3.7825297358},
		{ID: 6, Type: types.Sphere, Curvature: 1 / 61.8456294200, Thickness: 1.524, Material: types.Material{Key: "SK18"}, Diameter: 6.0},
		{ID: 7, Type: types.Sphere, Curvature: 1 / -10.0074859032, Thickness: 21.36695183553, Material: types.Material{}, Diameter: 6.0},
		{ID: 8, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}}, // image plane, no aperture
	}
	surface.Precompute(surfaces)
	return types.System{Surfaces: surfaces, StopSurface: 5}, gc
}

// TestPupilCoverage verifies the entrance-pupil grid produces valid wavefront
// samples for on- and off-axis fields, that every sample carries a positive
// finite Delaunay area weight and a nonzero field, and that the best-fit-sphere
// wavefront OPD is finite and non-negative.
func TestPupilCoverage(t *testing.T) {
	type tc struct {
		name  string
		sys   types.System
		gc    *glass.Catalog
		field types.FieldDef
	}
	var cases []tc
	{
		s, g := tripletSystem()
		cases = append(cases,
			tc{"triplet-f0", s, g, types.FieldDef{Angle: 0, Direction: []float64{0, 1}}},
			tc{"triplet-f16", s, g, types.FieldDef{Angle: 16, Direction: []float64{0, 1}}},
			tc{"triplet-f24", s, g, types.FieldDef{Angle: 24, Direction: []float64{0, 1}}},
		)
	}
	{
		s, g := singletSystem(8)
		cases = append(cases, tc{"singlet-f0", s, g, types.FieldDef{Angle: 0, Direction: []float64{0, 1}}})
	}
	for _, c := range cases {
		ref := DefaultReferenceSurface(c.sys.Surfaces)
		engine := ray.NewEngine(c.gc, nil)
		fg, err := ComputeFieldGrid(c.sys, c.gc, c.field, ref, 400, types.DefaultWavelength, types.GridPolar)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		samples, stats := TraceWavefront(c.sys, engine, fg, c.field, ref, types.DefaultWavelength, types.NewCircularJones(true))

		if stats.Valid == 0 {
			t.Errorf("%s: no valid wavefront samples", c.name)
			continue
		}
		for i, s := range samples {
			if !(s.Area > 0) || math.IsNaN(s.Area) {
				t.Errorf("%s: sample %d invalid area %v", c.name, i, s.Area)
			}
			if s.Field.Magnitude() <= 0 {
				t.Errorf("%s: sample %d zero field", c.name, i)
			}
		}
		rms, pv := wavefrontOPD(samples, types.Vec3{Z: imagePlaneZ(c.sys.Surfaces)}, 1.0)
		if math.IsNaN(rms) || math.IsNaN(pv) || rms < 0 || pv < 0 {
			t.Errorf("%s: invalid OPD rms=%v pv=%v", c.name, rms, pv)
		}
	}
}
