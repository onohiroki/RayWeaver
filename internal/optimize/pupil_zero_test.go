package optimize

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// A virtual entrance pupil may sit at exactly Z=0 and the -20..+50 sweep
// crosses it, so no code path may use z as an "unset" sentinel: presence is
// carried by appliedPupil.active / config.pupilResolved / a non-nil *float64.
// These tests pin each of those down.

func zeroPupilSurfaces() []types.Surface {
	surfs := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.02, Thickness: 5.0, Material: types.Material{Key: "N-BK7"}, Diameter: 30.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.02, Thickness: 40.0, Material: types.Material{}, Diameter: 30.0},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 30.0},
	}
	surface.Precompute(surfs)
	return surfs
}

func zeroPupilGC() *glass.Catalog {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	return gc
}

func TestPupilFromModelActiveAtZero(t *testing.T) {
	p := pupilFromModel(&types.PupilModelConfig{
		Mode: "virtual_entrance_pupil", AxialPosition: 0, Diameter: 8,
	})
	if !p.active {
		t.Error("a virtual entrance pupil at Z=0 must be active: z must not signal presence")
	}
	if p.z != 0 {
		t.Errorf("applied z = %v, want the model's 0", p.z)
	}
	if p.dia != 8 {
		t.Errorf("applied diameter = %v, want 8", p.dia)
	}
	if pupilFromModel(nil).active {
		t.Error("an absent pupil model must not activate the applied pupil")
	}
	if pupilFromModel(&types.PupilModelConfig{Mode: "dynamic"}).active {
		t.Error("only virtual_entrance_pupil activates the applied pupil")
	}
}

// TestGridCentringUsesVirtualPupilAtZero: the build-time (dynamic) seed must
// not shadow a virtual pupil resolved at exactly 0 mm.
func TestGridCentringUsesVirtualPupilAtZero(t *testing.T) {
	o := NewOptimizer(Config{
		Surfaces: zeroPupilSurfaces(),
		PupilZ:   7.0,
		PupilModel: &types.PupilModelConfig{
			Mode: "virtual_entrance_pupil", AxialPosition: 0, Diameter: 8,
		},
		MeritTerms: []MeritTerm{{
			FieldAngle: 0.0, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0,
		}},
		NumRays: 16,
	})
	cfg := &o.configs[0]
	if cfg.pupilZ != 7.0 {
		t.Fatalf("build-time pupil Z = %v, want the configured seed 7", cfg.pupilZ)
	}
	if !cfg.pupilResolved {
		t.Fatal("a virtual pupil model must seed pupilResolved: it may sit at 0")
	}

	x := o.getInitialState()
	_, _, pupils := o.applyVariables(x)
	p := pupils[cfg.id]
	if !p.active {
		t.Fatal("applyVariables dropped the virtual pupil's active flag")
	}
	if got := o.gridCentring(cfg, p, 20.0); got != 0 {
		t.Errorf("gridCentring = %v, want the virtual pupil at 0 (the seed %v must not win)", got, cfg.pupilZ)
	}
}

// TestResolvedPupilZPresence: presence of a pupil is a flag, never a value.
func TestResolvedPupilZPresence(t *testing.T) {
	if p := resolvedPupilZ(nil); p != nil {
		t.Errorf("resolvedPupilZ(nil) = %v, want nil", *p)
	}
	// A non-zero Z that was never resolved must still be reported absent.
	if p := resolvedPupilZ(&config{pupilZ: 7}); p != nil {
		t.Errorf("resolvedPupilZ(unresolved) = %v, want nil", *p)
	}
	p := resolvedPupilZ(&config{pupilZ: 0, pupilResolved: true})
	if p == nil {
		t.Fatal("a pupil resolved at Z=0 must be reported present")
	}
	if *p != 0 {
		t.Errorf("resolvedPupilZ = %v, want 0", *p)
	}
}

// TestFinalPupilModelsReportsZero: moving the pupil to Z=0 during the run must
// be written back; the input model's axial_position must not survive.
func TestFinalPupilModelsReportsZero(t *testing.T) {
	o := NewOptimizer(Config{
		Surfaces: zeroPupilSurfaces(),
		Variables: []Variable{
			{Name: "pmz", SurfaceID: 0, Param: "pupil_model_axial_position", Min: -20, Max: 50},
		},
		PupilModel: &types.PupilModelConfig{
			Mode: "virtual_entrance_pupil", AxialPosition: 8, Diameter: 12.5,
		},
		MeritTerms: []MeritTerm{{
			FieldAngle: 0.0, FieldWeight: 1.0, Wavelength: 0.00058756, WavWeight: 1.0, Weight: 1.0,
		}},
		NumRays: 16,
	})
	x := o.getInitialState()
	if x[0] != 8 {
		t.Fatalf("initial axial_position = %v, want the model's 8", x[0])
	}
	x[0] = 0

	models := o.FinalPupilModels(x)
	m, ok := models["config1"]
	if !ok {
		t.Fatalf("no pupil model written back, got %v", models)
	}
	if m.AxialPosition != 0 {
		t.Errorf("written-back axial_position = %v, want 0 (the run moved the pupil to Z=0)", m.AxialPosition)
	}
	if m.Diameter != 12.5 {
		t.Errorf("written-back diameter = %v, want 12.5", m.Diameter)
	}
}

// TestTraceChiefImageHeightAtZeroPupil: distortion_pct / lateral_color launch
// the chief ray through the pupil centre. A pupil resolved at 0 mm must still
// be honoured; only an absent pupil (nil) may keep the historical axis launch.
func TestTraceChiefImageHeightAtZeroPupil(t *testing.T) {
	gc, surfs := zeroPupilGC(), zeroPupilSurfaces()
	const fieldAngle, wl = 15.0, 0.0005876

	if h := traceChiefImageHeight(surfs, fieldAngle, wl, gc, nil); h != 0 {
		t.Errorf("chief image height with no pupil = %v, want 0 (axis launch misses the lens)", h)
	}

	zero := 0.0
	h0 := traceChiefImageHeight(surfs, fieldAngle, wl, gc, &zero)
	if h0 == 0 {
		t.Fatal("a resolved pupil at Z=0 did not aim the chief ray through the pupil centre")
	}
	// Continuity across the origin: Z=0 must behave like an arbitrarily small
	// positive Z, not like "no pupil".
	eps := 1e-9
	hEps := traceChiefImageHeight(surfs, fieldAngle, wl, gc, &eps)
	if hEps == 0 {
		t.Fatal("test setup: the near-zero pupil trace failed")
	}
	if math.Abs(h0-hEps) > 1e-6 {
		t.Errorf("chief image height jumps across Z=0: %v at 0 vs %v at %v", h0, hEps, eps)
	}
	// And the same through the merit path.
	cfg := &config{id: "c", pupilZ: 0, pupilResolved: true}
	term := &meritTerm{kind: MeritDistortionPct, fieldAngle: fieldAngle, wavelength: wl}
	if v := evaluateKindValue(MeritDistortionPct, term, surfs, gc, nil, cfg); v == 0 {
		t.Error("evaluateKindValue(distortion_pct) = 0 with a config pupil resolved at Z=0")
	}
}
