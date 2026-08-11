package ray

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/polarization"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// TestPolarizationOnAxisSinglet verifies the field is propagated through a
// singlet: |E|² at the image-side surface is reduced from the input power by
// the Fresnel amplitude transmittances and the field stays transverse.
func TestPolarizationOnAxisSinglet(t *testing.T) {
	engine, surfaces := simpleSingletEngine()
	ray := types.Ray{
		Wavelength: types.DefaultWavelength,
		Initial:    types.RayState{Origin: types.Vec3{X: 0, Y: 0, Z: -20}, Direction: types.Vec3{Z: 1}},
		Path:       []int{0, 1, 2},
		Jones:      types.NewCircularJones(true),
	}
	res := engine.TraceRay(ray, surfaces)
	if res.Error != "" {
		t.Fatalf("trace error: %s", res.Error)
	}
	s2 := res.Surfaces[len(res.Surfaces)-1]
	in := 2.0 // |RCP|² = |1|² + |i|²
	if s2.Field.Magnitude() <= 0 {
		t.Fatal("field magnitude zero at final surface")
	}
	if s2.Field.AbsSq() >= in {
		t.Errorf("|E|² = %v, should be reduced below input %v by Fresnel losses", s2.Field.AbsSq(), in)
	}
	perp := s2.Field.DotVec(s2.Direction)
	if math.Abs(real(perp)) > 1e-6 || math.Abs(imag(perp)) > 1e-6 {
		t.Errorf("field not transverse: E·d = %v", perp)
	}
}

// TestPolarizationRCPPreserved verifies a rotationally symmetric system keeps
// the circular polarization state: the output field must stay proportional to
// the input (1, i), i.e. Ey/Ex ≈ i.
func TestPolarizationRCPPreserved(t *testing.T) {
	engine, surfaces := simpleSingletEngine()
	ray := types.Ray{
		Wavelength: types.DefaultWavelength,
		Initial:    types.RayState{Origin: types.Vec3{X: 0, Y: 0, Z: -20}, Direction: types.Vec3{Z: 1}},
		Path:       []int{0, 1, 2},
		Jones:      types.NewCircularJones(true), // RCP (1, i)
	}
	res := engine.TraceRay(ray, surfaces)
	if res.Error != "" {
		t.Fatalf("trace error: %s", res.Error)
	}
	f := res.Surfaces[len(res.Surfaces)-1].Field
	if math.Abs(real(f.X)) < 1e-9 && math.Abs(imag(f.X)) < 1e-9 {
		t.Fatalf("Ex is zero, field = %v", f)
	}
	ratio := f.Y / f.X
	// Ey/Ex should be i (preserving the input circular state).
	if math.Abs(real(ratio)) > 0.02 || math.Abs(imag(ratio)-1) > 0.02 {
		t.Errorf("Ey/Ex = %v, want ≈ i (RCP preserved)", ratio)
	}
}

// TestPolarizationBrewster verifies p-polarized light passes an air→glass
// interface at Brewster's angle with no reflection loss.
func TestPolarizationBrewster(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	engine := NewEngine(gc, nil)
	n2 := 1.5168
	thetaB := math.Atan(n2)
	d := types.Vec3{Y: math.Sin(thetaB), Z: math.Cos(thetaB)}.Normalize()
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{Key: "N-BK7"}, Diameter: 50.0},
	}
	surface.Precompute(surfaces)

	// p-polarized input in the transverse frame of the ray (v lies in the
	// meridional YZ plane → p-polarization).
	u, v := polarization.TransverseFrame(d)
	field := polarization.TransverseField(u, v, types.JonesVector{Ex: complex(0, 0), Ey: complex(1, 0)})
	ray := types.Ray{
		Wavelength:   types.DefaultWavelength,
		Initial:      types.RayState{Origin: types.Vec3{Y: -20 * math.Tan(thetaB), Z: -20}, Direction: d},
		Path:         []int{0, 1},
		Jones:        types.NewLinearJones(0),
		InitialField: &field,
	}
	res := engine.TraceRay(ray, surfaces)
	if res.Error != "" {
		t.Fatalf("trace error: %s", res.Error)
	}
	s1 := res.Surfaces[len(res.Surfaces)-1]
	if s1.IntensityP < 0.99 {
		t.Errorf("p-intensity at Brewster = %v, want ≈ 1", s1.IntensityP)
	}
	if math.Abs(s1.Field.Magnitude()-1) > 0.02 {
		t.Errorf("field magnitude at Brewster = %v, want ≈ 1", s1.Field.Magnitude())
	}
}
