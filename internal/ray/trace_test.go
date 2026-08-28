package ray

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

func simpleSingletEngine() (*Engine, []types.Surface) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	engine := NewEngine(gc, nil)
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 10.0, Material: types.Material{Key: "N-BK7"}, Diameter: 50.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 100.0, Material: types.Material{}, Diameter: 50.0},
	}
	surface.Precompute(surfaces)
	return engine, surfaces
}

func ghostSingletEngine() (*Engine, []types.Surface) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	engine := NewEngine(gc, nil)
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.01, Thickness: 10.0, Material: types.Material{Key: "N-BK7"}, Diameter: 50.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.01, Thickness: 100.0, Material: types.Material{}, Diameter: 50.0},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 50.0},
	}
	surface.Precompute(surfaces)
	return engine, surfaces
}

// testSnell is an independent Snell application used to verify that a
// backward-travelling (ghost) ray refracts with the physically correct
// incident/emergent media (rather than the path-order ones).
func testSnell(d, n types.Vec3, n1, n2 float64) types.Vec3 {
	cosTheta1 := -d.Dot(n)
	if cosTheta1 < 0 {
		cosTheta1 = -cosTheta1
		n = n.Negate()
	}
	eta := n1 / n2
	cosTheta2Sq := 1 - eta*eta*(1-cosTheta1*cosTheta1)
	if cosTheta2Sq < 0 {
		return types.Vec3{}
	}
	cosTheta2 := math.Sqrt(cosTheta2Sq)
	return d.Scale(eta).Add(n.Scale(eta*cosTheta1 - cosTheta2)).Normalize()
}

// TestTraceRayGhostBackwardRefraction verifies that a ghost ray passing back
// through a refracting surface uses the correct (travel-direction) media: the
// backward pass through the singlet's back surface must refract air→glass,
// matching an independent Snell computation.
func TestTraceRayGhostBackwardRefraction(t *testing.T) {
	engine, surfaces := ghostSingletEngine()
	ray := types.Ray{
		ID:         "ghost",
		Wavelength: 0.00058756,
		Path:       []int{0, 1, 2, 3, 2, 1, 2, 3},
		Initial: types.RayState{
			Origin:    types.Vec3{X: 0, Y: 5.0, Z: -100.0},
			Direction: types.Vec3{X: 0, Y: 0, Z: 1.0},
		},
	}
	result := engine.TraceRay(ray, surfaces, false)
	if result.Error != "" {
		t.Fatalf("TraceRay error: %v", result.Error)
	}

	// path [0,1,2,3,2,1,2,3] → interactions:
	//   0,1,2 transmit · 3 reflect · 2 transmit (backward) · 1 reflect · 2,3 transmit
	want := []types.InteractionType{
		types.Transmit, types.Transmit, types.Transmit, types.Reflect,
		types.Transmit, types.Reflect, types.Transmit, types.Transmit,
	}
	if len(result.Surfaces) != len(want) {
		t.Fatalf("got %d surface results, want %d", len(result.Surfaces), len(want))
	}
	for i, w := range want {
		if result.Surfaces[i].Interaction != w {
			t.Errorf("surface %d interaction = %v, want %v", result.Surfaces[i].SurfaceID, result.Surfaces[i].Interaction, w)
		}
	}

	// The backward pass at surface 2 (index 4) travels from air into glass.
	incident := result.Surfaces[3].Direction // direction after the s3 reflection
	hit := result.Surfaces[4].Position
	var s2v types.Surface
	for _, s := range surfaces {
		if s.ID == 2 {
			s2v = s
		}
	}
	center := types.Vec3{X: 0, Y: 0, Z: s2v.PhysicalZ + s2v.Radius()}
	normal := hit.Subtract(center).Normalize()
	nGlass, _ := engine.Glass.RefractiveIndex(types.Material{Key: "N-BK7"}, ray.Wavelength)
	expected := testSnell(incident, normal, 1.0, nGlass)
	got := result.Surfaces[4].Direction
	if math.Abs(got.X-expected.X) > 1e-6 || math.Abs(got.Y-expected.Y) > 1e-6 || math.Abs(got.Z-expected.Z) > 1e-6 {
		t.Errorf("backward s2 direction = %v, want independent Snell %v", got, expected)
	}
}

// TestTraceRayGhostReflectIntensity verifies that a path-encoded reflection at
// a lens surface whose material is AIR (a glass→air interface approached from
// inside the glass) reports the Fresnel reflectance rather than an ideal 1.0
// (which is reserved for fold mirrors).
func TestTraceRayGhostReflectIntensity(t *testing.T) {
	engine, surfaces := ghostSingletEngine()
	ray := types.Ray{
		ID:         "ghost2",
		Wavelength: 0.00058756,
		Path:       []int{0, 1, 2, 1, 2, 3},
		Initial: types.RayState{
			Origin:    types.Vec3{X: 0, Y: 2.0, Z: -100.0},
			Direction: types.Vec3{X: 0, Y: 0, Z: 1.0},
		},
	}
	result := engine.TraceRay(ray, surfaces, false)
	if result.Error != "" {
		t.Fatalf("TraceRay error: %v", result.Error)
	}
	// path [0,1,2,1,2,3] → surface results [s0,s1,s2,s1,s2,s3];
	// the reflection at s2 (index 2) is at the AIR-material back surface.
	r := result.Surfaces[2]
	if r.SurfaceID != 2 || r.Interaction != types.Reflect {
		t.Fatalf("expected REFLECT at surface 2, got surface %d interaction %v", r.SurfaceID, r.Interaction)
	}
	if r.IntensityS < 0.03 || r.IntensityS > 0.06 {
		t.Errorf("ghost reflect IntensityS = %v, want Fresnel ≈ 0.042 (glass→air)", r.IntensityS)
	}
}

func TestTraceRayOnAxis(t *testing.T) {
	engine, surfaces := simpleSingletEngine()
	ray := types.Ray{
		ID:         "onaxis",
		Wavelength: 0.00058756,
		Path:       []int{1, 2},
		Initial: types.RayState{
			Origin:    types.Vec3{X: 0, Y: 0, Z: -100.0},
			Direction: types.Vec3{X: 0, Y: 0, Z: 1.0},
		},
	}
	result := engine.TraceRay(ray, surfaces, false)
	if result.Error != "" {
		t.Fatalf("TraceRay error: %v", result.Error)
	}
	if len(result.Surfaces) != 2 {
		t.Fatalf("Expected 2 surface results, got %d", len(result.Surfaces))
	}
	if result.Surfaces[0].Position.Z < -1 || result.Surfaces[0].Position.Z > 1 {
		t.Errorf("Surface 1 position Z = %v, want near 0", result.Surfaces[0].Position.Z)
	}
}

func TestTraceRayOffAxis(t *testing.T) {
	engine, surfaces := simpleSingletEngine()
	ray := types.Ray{
		ID:         "offaxis",
		Wavelength: 0.00058756,
		Path:       []int{1, 2},
		Initial: types.RayState{
			Origin:    types.Vec3{X: 0, Y: 5.0, Z: -100.0},
			Direction: types.Vec3{X: 0, Y: 0, Z: 1.0},
		},
	}
	result := engine.TraceRay(ray, surfaces, false)
	if result.Error != "" {
		t.Fatalf("TraceRay error: %v", result.Error)
	}
	if result.Surfaces[0].Position.Y == 0 {
		t.Error("Off-axis ray should not hit surface 1 at Y=0")
	}
}

func TestTraceRayMissesAperture(t *testing.T) {
	engine, surfaces := simpleSingletEngine()
	ray := types.Ray{
		ID:         "miss",
		Wavelength: 0.00058756,
		Path:       []int{1, 2},
		Initial: types.RayState{
			Origin:    types.Vec3{X: 0, Y: 100.0, Z: -100.0},
			Direction: types.Vec3{X: 0, Y: 0, Z: 1.0},
		},
	}
	result := engine.TraceRay(ray, surfaces, false)
	if result.Error == "" {
		t.Error("Expected error for ray that misses aperture")
	}
}

func TestNewEngine(t *testing.T) {
	gc := glass.NewCatalog()
	engine := NewEngine(gc, nil)
	if engine == nil {
		t.Fatal("NewEngine returned nil")
	}
	if engine.Glass != gc {
		t.Error("Glass catalog mismatch")
	}
}

func TestTraceRayPreservesOPL(t *testing.T) {
	engine, surfaces := simpleSingletEngine()
	ray := types.Ray{
		ID:         "opl",
		Wavelength: 0.00058756,
		Path:       []int{1, 2},
		Initial: types.RayState{
			Origin:    types.Vec3{X: 0, Y: 0, Z: -100.0},
			Direction: types.Vec3{X: 0, Y: 0, Z: 1.0},
		},
	}
	result := engine.TraceRay(ray, surfaces, false)
	if result.Error != "" {
		t.Fatalf("TraceRay error: %v", result.Error)
	}
	if result.OPLTotal <= 0 {
		t.Error("OPL should be positive")
	}
	// OPL should be > geometric distance (n>1 for glass)
	if result.OPLTotal < (4.0 + 10.0 + 100.0) {
		t.Errorf("OPL = %v, should be > geometric path", result.OPLTotal)
	}
}

func foldedMirrorSurfaces() ([]types.Surface, *glass.Catalog) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0, Thickness: 1000.0, Material: types.Material{}, Diameter: 200.0},
		{ID: 2, Type: types.Sphere, Curvature: 1.0 / 1000.0, Thickness: 480.0, Material: types.Material{}, Diameter: 300.0, Reflect: true,
			Decenter: []types.DecenterStep{{Tilt: types.Vec3{X: 0, Y: 180, Z: 0}, Scope: types.ScopeBoth}}},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 20.0, Material: types.Material{}, Diameter: 60.0},
		{ID: 4, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 50.0},
	}
	surface.Precompute(surfaces)
	return surfaces, gc
}

func TestTraceRayReflectFlag(t *testing.T) {
	surfaces, gc := foldedMirrorSurfaces()
	engine := NewEngine(gc, nil)
	ray := types.Ray{
		ID:         "mirror",
		Wavelength: 0.00058756,
		Path:       []int{1, 2, 3, 4},
		Initial: types.RayState{
			Origin:    types.Vec3{X: 0, Y: 0, Z: -100.0},
			Direction: types.Vec3{X: 0, Y: 0, Z: 1.0},
		},
	}
	result := engine.TraceRay(ray, surfaces, false)
	if result.Error != "" {
		t.Fatalf("TraceRay error: %v", result.Error)
	}
	if result.Surfaces[1].Interaction != types.Reflect {
		t.Errorf("Surface 2 interaction = %v, want Reflect", result.Surfaces[1].Interaction)
	}
	if result.Surfaces[2].Interaction != types.Transmit {
		t.Errorf("Surface 3 interaction = %v, want Transmit", result.Surfaces[2].Interaction)
	}
	// Ideal uncoated mirror in air: intensity = 1.0
	if result.Surfaces[1].IntensityS != 1.0 {
		t.Errorf("Mirror IntensityS = %v, want 1.0", result.Surfaces[1].IntensityS)
	}
	// Ray reflects at the mirror's physical Z=1000 and returns toward the image.
	if result.Surfaces[1].Position.Z < 999 || result.Surfaces[1].Position.Z > 1001 {
		t.Errorf("Mirror hit Z = %v, want ~1000", result.Surfaces[1].Position.Z)
	}
	if result.Surfaces[2].Position.Z > 521 || result.Surfaces[2].Position.Z < 519 {
		t.Errorf("Surface 3 hit Z = %v, want ~520", result.Surfaces[2].Position.Z)
	}
}

func TestTraceRayReflectFlagTilted(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	engine := NewEngine(gc, nil)
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0, Thickness: 1000.0, Material: types.Material{}, Diameter: 200.0},
		{ID: 2, Type: types.Sphere, Curvature: 1.0 / 1000.0, Thickness: 480.0, Material: types.Material{}, Diameter: 300.0, Reflect: true,
			Decenter: []types.DecenterStep{{Tilt: types.Vec3{X: 0, Y: 180, Z: 0}, Scope: types.ScopeBoth}}},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 20.0, Material: types.Material{}, Diameter: 60.0},
		{ID: 4, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 50.0},
	}
	surface.Precompute(surfaces)

	// Parallel bundle: concave mirror (f=R/2=500) focuses all heights at
	// physical Z = 1000-500 = 500, on axis.
	for _, h := range []float64{0, 20, 50, 80} {
		ray := types.Ray{
			ID:         "bundle",
			Wavelength: 0.00058756,
			Path:       []int{1, 2, 3, 4},
			Initial: types.RayState{
				Origin:    types.Vec3{X: h, Y: 0, Z: -100.0},
				Direction: types.Vec3{X: 0, Y: 0, Z: 1.0},
			},
		}
		result := engine.TraceRay(ray, surfaces, false)
		if result.Error != "" {
			t.Fatalf("h=%v TraceRay error: %v", h, result.Error)
		}
		last := result.Surfaces[len(result.Surfaces)-1]
		if last.SurfaceID != 4 {
			t.Fatalf("h=%v last surface = %d, want 4", h, last.SurfaceID)
		}
		if math.Abs(last.Position.Y) > 1e-6 {
			t.Errorf("h=%v image Y = %v, want ~0", h, last.Position.Y)
		}
		if math.Abs(last.Position.Z-500.0) > 1.0 {
			t.Errorf("h=%v image Z = %v, want ~500", h, last.Position.Z)
		}
	}
}

// highIndexTIRSetup builds a flat-entry / convex-exit lens with a high-refractive-
// index model glass (nd=5.0) so that a moderately angled ray undergoes TIR at the
// exit surface.
func highIndexTIRSetup() (*Engine, []types.Surface) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "hi-n", ND: 5.0, VD: 15.0})
	engine := NewEngine(gc, nil)
	surfaces := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0, Thickness: 5.0,
			Material: types.Material{ND: 5.0, VD: 15.0}, Diameter: 40.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.1, Thickness: 100.0,
			Material: types.Material{}, Diameter: 40.0},
	}
	surface.Precompute(surfaces)
	return engine, surfaces
}

func TestTraceRayLenientMissesAperture(t *testing.T) {
	engine, surfaces := simpleSingletEngine()
	r := types.Ray{
		ID:         "offaxis",
		Wavelength: 0.00058756,
		Lenient:    true,
		Path:       []int{1, 2},
		Initial: types.RayState{
			Origin:    types.Vec3{X: 0, Y: 100.0, Z: -100.0},
			Direction: types.Vec3{X: 0, Y: 0, Z: 1.0},
		},
	}
	result := engine.TraceRay(r, surfaces, false)
	if result.Error != "" {
		t.Fatalf("Lenient ray should not error on aperture miss: %v", result.Error)
	}
	for _, sr := range result.Surfaces {
		if sr.SurfaceID == 1 && sr.ErrorCode != string(ErrApertureStop) {
			t.Errorf("Surface 1 ErrorCode = %q, want %q", sr.ErrorCode, string(ErrApertureStop))
		}
	}
}

func TestTraceRayStrictMissesAperture(t *testing.T) {
	engine, surfaces := simpleSingletEngine()
	r := types.Ray{
		ID:         "offaxis",
		Wavelength: 0.00058756,
		Lenient:    false,
		Path:       []int{1, 2},
		Initial: types.RayState{
			Origin:    types.Vec3{X: 0, Y: 100.0, Z: -100.0},
			Direction: types.Vec3{X: 0, Y: 0, Z: 1.0},
		},
	}
	result := engine.TraceRay(r, surfaces, false)
	if result.Error == "" {
		t.Fatal("Strict ray should error on aperture miss")
	}
	if result.ErrorCode != string(ErrApertureStop) {
		t.Errorf("ErrorCode = %q, want %q", result.ErrorCode, string(ErrApertureStop))
	}
}

func TestTraceRayLenientMissedSurface(t *testing.T) {
	engine, surfaces := simpleSingletEngine()
	// Y=150 is beyond the sphere radius (~100) so the ray misses geometry entirely
	r := types.Ray{
		ID:         "miss",
		Wavelength: 0.00058756,
		Lenient:    true,
		Path:       []int{1, 2},
		Initial: types.RayState{
			Origin:    types.Vec3{X: 0, Y: 150.0, Z: -100.0},
			Direction: types.Vec3{X: 0, Y: 0, Z: 1.0},
		},
	}
	result := engine.TraceRay(r, surfaces, false)
	if result.Error != "" {
		t.Fatalf("Lenient ray should not error on geometric miss: %v", result.Error)
	}
	for _, sr := range result.Surfaces {
		if sr.SurfaceID == 1 && sr.ErrorCode != string(ErrMissedSurface) {
			t.Errorf("Surface 1 ErrorCode = %q, want %q", sr.ErrorCode, string(ErrMissedSurface))
		}
	}
}

func TestTraceRayLenientTIR(t *testing.T) {
	engine, surfaces := highIndexTIRSetup()

	// Strict mode: TIR should fail
	strict := types.Ray{
		ID:         "tir-strict",
		Wavelength: 0.00058756,
		Lenient:    false,
		Path:       []int{0, 1, 2},
		Initial: types.RayState{
			Origin:    types.Vec3{X: 0, Y: -49.75, Z: -100.0},
			Direction: types.Vec3{X: 0, Y: 0.5, Z: 0.866},
		},
	}
	strictResult := engine.TraceRay(strict, surfaces, false)
	if strictResult.Error == "" {
		t.Fatal("Strict mode should fail on TIR")
	}
	if strictResult.ErrorCode != string(ErrTIR) {
		t.Errorf("Strict ErrorCode = %q, want %q", strictResult.ErrorCode, string(ErrTIR))
	}

	// Lenient mode: TIR → treated as reflection, trace continues
	lax := types.Ray{
		ID:         "tir-lenient",
		Wavelength: 0.00058756,
		Lenient:    true,
		Path:       []int{0, 1, 2},
		Initial: types.RayState{
			Origin:    types.Vec3{X: 0, Y: -49.75, Z: -100.0},
			Direction: types.Vec3{X: 0, Y: 0.5, Z: 0.866},
		},
	}
	result := engine.TraceRay(lax, surfaces, false)
	if result.Error != "" {
		t.Fatalf("Lenient ray should not error on TIR: %v", result.Error)
	}
	if len(result.Surfaces) < 3 {
		t.Fatalf("Expected at least 3 surface results (0,1,2), got %d", len(result.Surfaces))
	}
	var ti types.SurfaceResult
	found := false
	for _, sr := range result.Surfaces {
		if sr.SurfaceID == 2 {
			ti = sr
			found = true
		}
	}
	if !found {
		t.Fatal("Surface 2 not found in results")
	}
	if ti.Interaction != types.Reflect {
		t.Errorf("Surface 2 interaction = %v, want Reflect (TIR in lenient mode)", ti.Interaction)
	}
	if ti.ErrorCode != string(ErrTIR) {
		t.Errorf("Surface 2 ErrorCode = %q, want %q", ti.ErrorCode, string(ErrTIR))
	}
	if ti.IntensityS != 1.0 || ti.IntensityP != 1.0 {
		t.Errorf("TIR intensities = (%v, %v), want (1.0, 1.0)", ti.IntensityS, ti.IntensityP)
	}
}

func TestTraceRayLenientGlassPathShort(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	engine := NewEngine(gc, nil)
	s := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0, Thickness: 5.0,
			Material: types.Material{Key: "N-BK7"}, Diameter: 50.0,
			MinGlassPath: 15.0},
		{ID: 2, Type: types.Sphere, Curvature: 0, Thickness: 100.0,
			Material: types.Material{}, Diameter: 50.0},
	}
	surface.Precompute(s)

	// On-axis ray through 5mm glass: glass path = 5mm < 15mm → fails
	r := types.Ray{
		ID:         "gpath",
		Wavelength: 0.00058756,
		Lenient:    true,
		Path:       []int{0, 1, 2},
		Initial: types.RayState{
			Origin:    types.Vec3{X: 0, Y: 0, Z: -50.0},
			Direction: types.Vec3{X: 0, Y: 0, Z: 1.0},
		},
	}
	result := engine.TraceRay(r, s, false)
	if result.Error != "" {
		t.Fatalf("Lenient ray should not error on short glass path: %v", result.Error)
	}
	for _, sr := range result.Surfaces {
		if sr.SurfaceID == 2 && sr.ErrorCode != string(ErrGlassPathShort) {
			t.Errorf("Surface 2 ErrorCode = %q, want %q", sr.ErrorCode, string(ErrGlassPathShort))
		}
	}
}

// TestTraceRayIncludeErrorSurfaces verifies that, with IncludeErrorSurfaces
// set, a non-lenient trace that stops at a surface still records that surface
// as a MISSED entry carrying the error code (so the partial result shows where
// the ray stopped). Without the flag the error surface is not appended.
func TestTraceRayIncludeErrorSurfaces(t *testing.T) {
	engine, surfaces := simpleSingletEngine()

	// Shrink the singlet aperture so an off-axis ray stops at surface 1.
	surfaces[0].Diameter = 10.0
	surface.Precompute(surfaces)

	apertureRay := types.Ray{
		ID:         "aperture",
		Wavelength: 0.00058756,
		Path:       []int{0, 1, 2},
		Initial: types.RayState{
			Origin:    types.Vec3{X: 0, Y: 6.0, Z: -100.0},
			Direction: types.Vec3{X: 0, Y: 0, Z: 1.0},
		},
	}

	// Without the flag: the erroring surface is not appended.
	plain := engine.TraceRay(apertureRay, surfaces, false)
	if plain.Error == "" || plain.ErrorCode != string(ErrApertureStop) {
		t.Fatalf("expected aperture_stop error, got %q/%q", plain.Error, plain.ErrorCode)
	}
	for _, sr := range plain.Surfaces {
		if sr.SurfaceID == 1 {
			t.Fatalf("error surface appended without IncludeErrorSurfaces")
		}
	}

	// With the flag: the erroring surface is appended as MISSED + error code.
	apertureRay.IncludeErrorSurfaces = true
	withErr := engine.TraceRay(apertureRay, surfaces, false)
	if withErr.Error == "" || withErr.ErrorCode != string(ErrApertureStop) {
		t.Fatalf("expected aperture_stop error, got %q/%q", withErr.Error, withErr.ErrorCode)
	}
	last := withErr.Surfaces[len(withErr.Surfaces)-1]
	if last.SurfaceID != 1 || last.Interaction != types.Missed {
		t.Fatalf("last surface = id %d interaction %s, want id 1 MISSED",
			last.SurfaceID, last.Interaction)
	}
	if last.ErrorCode != string(ErrApertureStop) {
		t.Errorf("last surface ErrorCode = %q, want %q", last.ErrorCode, string(ErrApertureStop))
	}
	if last.Position.Y == 0 && last.Position.Z == 0 {
		t.Errorf("error surface position is zero, want the out-of-aperture hit point")
	}

	// TIR: a ray starting inside the glass, exiting at beyond the critical
	// angle, must record the exit surface as MISSED + total_internal_reflection.
	surf := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0, Thickness: 4.0,
			Material: types.Material{Key: "N-BK7"}, Diameter: 30.0},
		{ID: 2, Type: types.Sphere, Curvature: 0, Thickness: 10.0,
			Material: types.Material{}, Diameter: 30.0},
	}
	surface.Precompute(surf)
	tirRay := types.Ray{
		ID:                   "tir",
		Wavelength:           0.00058756,
		Path:                 []int{0, 2},
		IncludeErrorSurfaces: true,
		Initial: types.RayState{
			Origin:    types.Vec3{X: 0, Y: 0, Z: 3.0},
			Direction: types.Vec3{X: 0, Y: 0.87, Z: 0.5},
		},
	}
	tirResult := engine.TraceRay(tirRay, surf, false)
	if tirResult.Error == "" || tirResult.ErrorCode != string(ErrTIR) {
		t.Fatalf("expected TIR error, got %q/%q", tirResult.Error, tirResult.ErrorCode)
	}
	tirLast := tirResult.Surfaces[len(tirResult.Surfaces)-1]
	if tirLast.SurfaceID != 2 || tirLast.Interaction != types.Missed {
		t.Fatalf("last TIR surface = id %d interaction %s, want id 2 MISSED",
			tirLast.SurfaceID, tirLast.Interaction)
	}
	if tirLast.ErrorCode != string(ErrTIR) {
		t.Errorf("TIR surface ErrorCode = %q, want %q", tirLast.ErrorCode, string(ErrTIR))
	}

	// Surface-not-found: the missing surface is recorded from the ray origin.
	missing := types.Ray{
		ID:                   "missing",
		Wavelength:           0.00058756,
		Path:                 []int{0, 99},
		IncludeErrorSurfaces: true,
		Initial: types.RayState{
			Origin:    types.Vec3{X: 0, Y: 1, Z: -50.0},
			Direction: types.Vec3{X: 0, Y: 0, Z: 1.0},
		},
	}
	missingResult := engine.TraceRay(missing, surfaces, false)
	if missingResult.Error == "" || missingResult.ErrorCode != string(ErrSurfaceNotFound) {
		t.Fatalf("expected surface_not_found error, got %q/%q", missingResult.Error, missingResult.ErrorCode)
	}
	missLast := missingResult.Surfaces[len(missingResult.Surfaces)-1]
	if missLast.SurfaceID != 99 || missLast.ErrorCode != string(ErrSurfaceNotFound) {
		t.Errorf("missing surface = id %d code %q, want id 99 %q",
			missLast.SurfaceID, missLast.ErrorCode, string(ErrSurfaceNotFound))
	}
}
