package vignette

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/chief"
	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/ray"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

func tripletSurfaces() []types.Surface {
	return []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 1 / 10.2871491742, Thickness: 1.524, Material: types.Material{Key: "SK18"}, Diameter: 20.0, AutoAperture: true},
		{ID: 2, Type: types.Sphere, Curvature: 1 / -239.3967954752, Thickness: 2.3368, Material: types.Material{}, Diameter: 20.0, AutoAperture: true},
		{ID: 3, Type: types.Sphere, Curvature: 1 / -12.8269871730, Thickness: 0.508, Material: types.Material{Key: "SF12"}, Diameter: 20.0, AutoAperture: true},
		{ID: 4, Type: types.Sphere, Curvature: 1 / 10.5917184406, Thickness: 1.4986, Material: types.Material{}, Diameter: 20.0, AutoAperture: true},
		{ID: 5, Type: types.Sphere, Curvature: 0.0, Thickness: 1.016, Material: types.Material{}, Diameter: 3.78},
		{ID: 6, Type: types.Sphere, Curvature: 1 / 61.8456294200, Thickness: 1.524, Material: types.Material{Key: "SK18"}, Diameter: 20.0, AutoAperture: true},
		{ID: 7, Type: types.Sphere, Curvature: 1 / -10.0074859032, Thickness: 21.36695183553, Material: types.Material{}, Diameter: 20.0, AutoAperture: true},
		{ID: 8, Type: types.Sphere, Curvature: 0.0, Thickness: 0.0, Material: types.Material{}, Diameter: 44.0},
	}
}

func tripletGC() *glass.Catalog {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "SK18", ND: 1.63854, VD: 55.42})
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "SF12", ND: 1.64831, VD: 33.84})
	return gc
}

func tripletOptions(minGlassPath float64) Options {
	return Options{
		Fields: []types.FieldDef{
			{Angle: 0.0, Direction: []float64{0, 1}},
			{Angle: 23.0, Direction: []float64{0, 1}},
		},
		RefSurface:   8,
		NumRays:      256,
		GridType:     types.GridHex,
		Wavelength:   0.00058756,
		MinGlassPath: minGlassPath,
		MarginMM:     0.2,
		Iterations:   3,
	}
}

func surfaceByID(s []types.Surface, id int) *types.Surface {
	for i := range s {
		if s[i].ID == id {
			return &s[i]
		}
	}
	return nil
}

// TestVignetteSizesAutoApertureOnly verifies the core sizing rules: only
// auto_aperture: true surfaces are resized, the fixed aperture (surface 5) and
// the reference surface (8) keep their diameters, and the on-axis field has no
// vignetting while the wide field is vignetted.
func TestVignetteSizesAutoApertureOnly(t *testing.T) {
	res := Run(tripletSurfaces(), tripletOptions(0.5), tripletGC())

	for _, id := range []int{1, 2, 3, 4, 6, 7} {
		s := surfaceByID(res.Surfaces, id)
		if s == nil || s.Diameter <= 0 || s.Diameter >= 19.0 {
			t.Errorf("auto_aperture s%d diameter = %v, want sized in (0,19)", id, func() float64 {
				if s == nil {
					return -1
				}
				return s.Diameter
			}())
		}
	}
	if s := surfaceByID(res.Surfaces, 5); s == nil || math.Abs(s.Diameter-3.78) > 1e-6 {
		t.Errorf("fixed aperture s5 diameter = %v, want 3.78", func() float64 {
			if s == nil {
				return -1
			}
			return s.Diameter
		}())
	}
	if s := surfaceByID(res.Surfaces, 8); s == nil || math.Abs(s.Diameter-44.0) > 1e-6 {
		t.Errorf("reference s8 diameter = %v, want 44", func() float64 {
			if s == nil {
				return -1
			}
			return s.Diameter
		}())
	}

	if len(res.Fields) != 2 {
		t.Fatalf("expected 2 field reports, got %d", len(res.Fields))
	}
	if res.Fields[0].Vignetting != 1.0 {
		t.Errorf("field 0 vignetting = %v, want 1.0 (on-axis reference)", res.Fields[0].Vignetting)
	}
	if res.Fields[1].Vignetting <= 0 || res.Fields[1].Vignetting >= 1.0 {
		t.Errorf("field 23° vignetting = %v, want in (0,1) (vignetted)", res.Fields[1].Vignetting)
	}
	if res.Fields[0].EntrancePupilZ == 0 || res.Fields[1].EntrancePupilZ == 0 {
		t.Errorf("entrance pupil Z not computed: f0=%v f1=%v", res.Fields[0].EntrancePupilZ, res.Fields[1].EntrancePupilZ)
	}
}

// TestVignetteGlassPathNarrowing verifies that a stricter min_glass_path makes
// more rays fail (lower vignetting) for the wide field.
func TestVignetteGlassPathNarrowing(t *testing.T) {
	resLoose := Run(tripletSurfaces(), tripletOptions(0.5), tripletGC())
	resStrict := Run(tripletSurfaces(), tripletOptions(5.0), tripletGC())

	if resStrict.Fields[1].Vignetting > resLoose.Fields[1].Vignetting+1e-9 {
		t.Errorf("vignetting with stricter glass path = %v, want <= loose %v", resStrict.Fields[1].Vignetting, resLoose.Fields[1].Vignetting)
	}
}

// TestVignetteSizingCoversChiefEnvelope pins the sizing measurement: every
// auto_aperture diameter the solve settles must cover chief.BeamEnvelope at the
// settled state — the same surface-local, prefixed-prefix measurement
// `chief --clear-aperture` and the optimizer's final sizing use. The local
// measurement this replaced took the global max(|X|,|Y|) and dropped a ray at
// its first failure, so a fixed aperture or a missed surface downstream starved
// the envelope of every surface before it: on the zoom study the wide field's
// 20.3/45.0 mm surfaces stayed at 9.7/21.0 mm and the tool "settled" a 0.176
// prescribed survival where 0.297 was reachable.
func TestVignetteSizingCoversChiefEnvelope(t *testing.T) {
	gc := tripletGC()
	opts := tripletOptions(0.5)
	// Converge first: Run sizes from the analysis of the previous state, so a
	// single call may lag the final pupil re-centring by one pass.
	res := Run(tripletSurfaces(), opts, gc)
	res = Run(res.Surfaces, opts, gc)

	work := append([]types.Surface{}, res.Surfaces...)
	surface.Precompute(work)
	pol := types.NewCircularJones(true)
	results := chief.DetermineChiefRaysGridMode(
		types.System{Surfaces: work, StopSurface: opts.StopSurface},
		opts.Fields, opts.RefSurface, opts.NumRays, gc, pol, opts.Wavelength,
		true, opts.GridType, nil, nil, nil, nil, 0, 0, opts.RayDefinition,
	)
	env := chief.BeamEnvelope(results, ray.NewEngine(gc, nil), work, dls.BuildPath(work), opts.Wavelength, pol)

	for i := range work {
		if !work[i].AutoAperture {
			continue
		}
		e := env[work[i].ID]
		if e <= 0 {
			continue
		}
		want := 2*e + 2*opts.MarginMM
		if work[i].Diameter < want-1e-6 {
			t.Errorf("surface %d diameter = %.6f, want >= %.6f (chief beam envelope %.6f + margin)",
				work[i].ID, work[i].Diameter, want, e)
		}
	}
}

// TestVignetteRespectsMaxDiameter pins the mechanical bound in the vignette
// sizing: max_diameter caps the auto_aperture diameter instead of growing it to
// the beam (the beam beyond it is the deliberate vignetting case).
func TestVignetteRespectsMaxDiameter(t *testing.T) {
	surfs := tripletSurfaces()
	for i := range surfs {
		if surfs[i].ID == 6 {
			surfs[i].MaxDiameter = 1.0
		}
	}
	res := Run(surfs, tripletOptions(0.5), tripletGC())
	s6 := surfaceByID(res.Surfaces, 6)
	if s6 == nil || math.Abs(s6.Diameter-1.0) > 1e-9 {
		t.Errorf("capped surface 6 diameter = %v, want max_diameter 1", func() float64 {
			if s6 == nil {
				return -1
			}
			return s6.Diameter
		}())
	}
	if s1 := surfaceByID(res.Surfaces, 1); s1 == nil || s1.Diameter <= 1.0 {
		t.Errorf("surface 1 diameter = %v, want uncapped (the cap is per surface)", s1)
	}
}

// TestVignetteConverges verifies that a second run from the settled state does
// not move the auto_aperture diameters much (near a fixed point; the small
// residual comes from the chief grid's pupil re-centring depending on the
// current diameters).
func TestVignetteConverges(t *testing.T) {
	res1 := Run(tripletSurfaces(), tripletOptions(0.5), tripletGC())
	res2 := Run(res1.Surfaces, tripletOptions(0.5), tripletGC())

	for _, id := range []int{1, 2, 3, 4, 6, 7} {
		s1 := surfaceByID(res1.Surfaces, id)
		s2 := surfaceByID(res2.Surfaces, id)
		if s1 == nil || s2 == nil {
			continue
		}
		if math.Abs(s1.Diameter-s2.Diameter) > 2.0 {
			t.Errorf("s%d diameter moved between runs: %v -> %v (not converged)", id, s1.Diameter, s2.Diameter)
		}
	}
}

// TestVignetteAppliesMinGlassPath verifies the uniform min_glass_path is
// applied to glass element entry surfaces only.
func TestVignetteAppliesMinGlassPath(t *testing.T) {
	res := Run(tripletSurfaces(), tripletOptions(0.5), tripletGC())
	if s := surfaceByID(res.Surfaces, 1); s == nil || math.Abs(s.MinGlassPath-0.5) > 1e-12 {
		t.Errorf("s1 (glass entry) min_glass_path = %v, want 0.5", func() float64 {
			if s == nil {
				return -1
			}
			return s.MinGlassPath
		}())
	}
	if s := surfaceByID(res.Surfaces, 5); s == nil || s.MinGlassPath != 0 {
		t.Errorf("s5 (AIR) min_glass_path = %v, want 0", s.MinGlassPath)
	}
}

// TestPrecomputeSmoke ensures surface precomputation is not required by callers.
func TestPrecomputeSmoke(t *testing.T) {
	s := tripletSurfaces()
	res := Run(s, tripletOptions(0.5), tripletGC())
	if len(res.Surfaces) == 0 {
		t.Fatal("no surfaces returned")
	}
	// The caller's slice must not be mutated (Run works on a copy).
	if s[0].Diameter != 20.0 {
		t.Errorf("input surfaces mutated: s1 diameter = %v, want 20", s[0].Diameter)
	}
}

// TestApplyMinGlassPathExercised guards the precompute path used by Run.
func TestPrecomputeSurface(t *testing.T) {
	s := tripletSurfaces()
	surface.Precompute(s)
	if s[0].PhysicalZ != 0 {
		t.Errorf("s1 PhysicalZ = %v, want 0", s[0].PhysicalZ)
	}
}
