package optimize

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
	"github.com/hiroki/rayweaver/internal/wavefront"
)

// vignettingTestSurfaces is a stop-free singlet whose only aperture limit is the
// 30 mm clear diameter, so a declared vignetting ellipse is the only thing that
// can shrink a field's pupil.
func vignettingTestSurfaces() []types.Surface {
	surfs := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.02, Thickness: 5.0, Material: types.Material{Key: "N-BK7"}, Diameter: 30.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.02, Thickness: 100.0, Material: types.Material{}, Diameter: 30.0},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 30.0},
	}
	surface.Precompute(surfs)
	return surfs
}

func vignettingTestGC() *glass.Catalog {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	return gc
}

// TestGridKindFieldVignetting verifies that the grid-trace merit kinds see the
// field's declared vignetting ellipse, exactly like the chief / wavefront / psf
// commands: a field clipped to 20% of its pupil area must report a much smaller
// spot than the same field without the clip.
func TestGridKindFieldVignetting(t *testing.T) {
	gc := vignettingTestGC()
	surfs := vignettingTestSurfaces()

	run := func(vig *types.VignettingDef) float64 {
		cfg := Config{
			Surfaces:  surfs,
			Variables: []Variable{},
			Fields:    []types.FieldItem{{ID: 0, AngleDeg: 6.0, Weight: 1, Vignetting: vig}},
			MeritTerms: []MeritTerm{{
				Kind: MeritSpotRMS, FieldAngle: 6.0, FieldIndex: 0, FieldWeight: 1,
				Wavelength: 0.00058756, WavWeight: 1, Weight: 1,
			}},
			GlassCatalog: gc,
			NumRays:      256,
		}
		opt := NewOptimizer(cfg)
		ccfg := opt.primaryConfig()
		term := &ccfg.meritTerms[0]
		if term.fieldIndex != 0 {
			t.Fatalf("fieldIndex = %d, want 0", term.fieldIndex)
		}
		return opt.evaluateGridKind(ccfg, term, surfs, gc, nil, appliedPupil{})
	}

	full := run(nil)
	// compression 0.1056 per axis = 80% of the pupil area, a realistic
	// prescribed off-axis vignetting.
	clip := types.VignettingDef{CompressionX: 0.1056, CompressionY: 0.1056}
	vignetted := run(&clip)

	if full <= 0 {
		t.Fatalf("unclipped spot_rms = %v, want a positive value", full)
	}
	if vignetted <= 0 {
		t.Fatalf("vignetted spot_rms = %v, want a positive value", vignetted)
	}
	// Clipping the pupil edge of a real spherical-aberration spot shrinks the
	// RMS (the outer, aberrated annulus is what carries it), so the merit must
	// move; an unplumbed vignetting would return the identical value.
	if math.Abs(vignetted-full) < 1e-9 {
		t.Errorf("spot_rms unchanged by the declared vignetting (%.6g): the grid kinds do not see it", full)
	}
	// gridForTerm (the cached path) must apply the same clip.
	opt := NewOptimizer(Config{
		Surfaces:  surfs,
		Variables: []Variable{},
		Fields:    []types.FieldItem{{ID: 0, AngleDeg: 6.0, Weight: 1, Vignetting: &clip}},
		MeritTerms: []MeritTerm{{
			Kind: MeritSpotRMS, FieldAngle: 6.0, FieldIndex: 0, FieldWeight: 1,
			Wavelength: 0.00058756, WavWeight: 1, Weight: 1,
		}},
		GlassCatalog: gc,
		NumRays:      256,
	})
	ccfg := opt.primaryConfig()
	cached := opt.gridForTerm(newEvalGridCache(), gc, surfs, ccfg, &ccfg.meritTerms[0], appliedPupil{})
	nOK := 0
	for _, p := range cached {
		if p.OK {
			nOK++
		}
	}
	if nOK == 0 || nOK >= 256 {
		t.Errorf("vignetted grid traced %d/%d rays, want a clipped but non-empty grid", nOK, 256)
	}
	// The merit grid is polar, whose rings crowd the pupil rim, so removing the
	// outer 20% of the area drops more than 20% of the points (the chief
	// command's uniform-density hex grid keeps the area ratio). Only the
	// qualitative clip is asserted here.
	if frac := float64(nOK) / 256; frac < 0.4 || frac > 0.95 {
		t.Errorf("vignetted grid kept %d/256 rays (%.0f%%), want a clearly clipped grid", nOK, frac*100)
	}
}

// TestWavefrontTermVirtualPupilDiameter verifies that a virtual entrance pupil
// reaches the frozen wavefront grid: with the applied pupil diameter the merit
// must reproduce the standalone (dynamic-pupil, pupil-model aware) analysis
// instead of the paraxial/fixed-aperture radius it used to pick up.
func TestWavefrontTermVirtualPupilDiameter(t *testing.T) {
	gc := vignettingTestGC()
	surfs := vignettingTestSurfaces()
	const wl = 0.00058756
	const epd = 6.0 // virtual entrance pupil diameter

	pm := &types.PupilModelConfig{Mode: "virtual_entrance_pupil", AxialPosition: 0.0, Diameter: epd}
	ccfg := &config{id: "c", refSurface: 2, pupilModel: pm, fields: []types.FieldItem{{ID: 0, AngleDeg: 0, Weight: 1}}}
	term := &meritTerm{kind: MeritWavefrontSphereRMS, fieldAngle: 0, fieldIndex: 0, wavelength: wl}

	opt := NewOptimizer(Config{Surfaces: surfs, GlassCatalog: gc, NumRays: 128})
	withPupil := opt.evaluateWavefrontTerm(ccfg, term, surfs, gc, nil, appliedPupil{dia: epd})
	withoutPupil := opt.evaluateWavefrontTerm(ccfg, term, surfs, gc, nil, appliedPupil{})

	// The standalone analysis (pupil model aware) is the reference the merit has
	// to agree with.
	want, err := wavefront.AnalyzeField(types.System{Surfaces: surfs}, gc,
		types.FieldDef{Angle: 0, Direction: []float64{0, 1}}, 2, 128, wl, 1.0,
		nil, pm, "", wavefront.FieldOptions{EPDOverride: epd})
	if err != nil {
		t.Fatalf("standalone wavefront: %v", err)
	}
	if d := math.Abs(withPupil - want.Statistics.RMS); d > 1e-9 {
		t.Errorf("merit wavefront_sphere_rms = %.6g with the virtual pupil, standalone = %.6g (delta %.2e)",
			withPupil, want.Statistics.RMS, d)
	}
	if math.Abs(withPupil-withoutPupil) < 1e-12 {
		t.Logf("note: the two grid radii happened to give the same RMS on this system (pupil %g)", epd)
	}
}

// TestWavefrontPlaneReferenceOption verifies that optimization.wavefront_reference
// reaches the merit: with image_plane the wavefront_defocus term reports the
// field's focus error in mm (non-zero for a defocused field), while the default
// best-focus reference reports the small zonal coefficient.
func TestWavefrontPlaneReferenceOption(t *testing.T) {
	gc := vignettingTestGC()
	surfs := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.02, Thickness: 5.0, Material: types.Material{Key: "N-BK7"}, Diameter: 30.0},
		{ID: 2, Type: types.Sphere, Curvature: -0.02, Thickness: 52.0, Material: types.Material{}, Diameter: 30.0},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 30.0},
	}
	surface.Precompute(surfs)
	const wl = 0.00058756

	opt := NewOptimizer(Config{Surfaces: surfs, GlassCatalog: gc, NumRays: 128})
	term := &meritTerm{kind: MeritWavefrontDefocus, fieldAngle: 8, fieldIndex: 0, wavelength: wl}

	best := opt.evaluateWavefrontTerm(&config{id: "c", refSurface: 2}, term, surfs, gc, nil, appliedPupil{})
	plane := opt.evaluateWavefrontTerm(&config{id: "c", refSurface: 2, wavefrontPlaneRef: true}, term, surfs, gc, nil, appliedPupil{})

	if math.Abs(best) >= 0.05 {
		t.Errorf("best-focus wavefront_defocus = %v, want the small zonal coefficient", best)
	}
	if math.Abs(plane) < 0.5 {
		t.Errorf("image-plane wavefront_defocus = %v, want the field's focus error in mm", plane)
	}
}
