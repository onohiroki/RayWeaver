package optimize

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// beamClipProbe builds the same three-surface singlet for each case: a stop
// (surface 1) plus two free apertures, with both fields reaching ~15.6 mm at
// surface 2 (the local radial envelope, which is what the aperture check uses).
func beamClipProbe(surface2Diameter float64, vig *types.VignettingDef, cons []types.ConstraintOperand) (*Optimizer, []string) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})

	surfs := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0.02, Thickness: 5.0, Material: types.Material{Key: "N-BK7"}, Diameter: 40.0, Role: "stop"},
		{ID: 2, Type: types.Sphere, Curvature: -0.02, Thickness: 40.0, Material: types.Material{}, Diameter: surface2Diameter},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 40.0},
	}
	surface.Precompute(surfs)
	fields := []types.FieldItem{
		{ID: 0, AngleDeg: 0, Weight: 1, Vignetting: vig},
		{ID: 1, AngleDeg: 10, Weight: 1, Vignetting: vig},
	}
	opt := NewOptimizer(Config{
		Surfaces: surfs,
		Fields:   fields,
		MeritTerms: []MeritTerm{
			{Kind: MeritSpotRMS, FieldAngle: 0, FieldIndex: 0, FieldWeight: 1, Wavelength: 0.00058756, WavWeight: 1, Weight: 1},
			{Kind: MeritSpotRMS, FieldAngle: 10, FieldIndex: 1, FieldWeight: 1, Wavelength: 0.00058756, WavWeight: 1, Weight: 1},
		},
		Constraints:  cons,
		GlassCatalog: gc,
		StopSurface:  1,
		RefSurface:   3,
		NumRays:      64,
	})
	var warns []string
	prev := Warnf
	Warnf = func(format string, args ...interface{}) { warns = append(warns, fmt.Sprintf(format, args...)) }
	defer func() { Warnf = prev }()
	opt.WarnBeamClipping(opt.getInitialState())
	return opt, warns
}

// TestWarnBeamClippingMeasuresSizedAutoAperture pins the wrong-diameter bug it
// used to have: the warning read the stored input diameter, so an
// auto_aperture surface whose placeholder was small was reported as clipping
// and told to "enable auto_aperture on it". The sizing covers the beam, so the
// delivered document is clean and the diagnostic must be silent.
func TestWarnBeamClippingMeasuresSizedAutoAperture(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	surfs := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0, Thickness: 5.0, Material: types.Material{Key: "N-BK7"}, Diameter: 20.0, Role: "stop"},
		{ID: 2, Type: types.Sphere, Curvature: 0, Thickness: 40.0, Material: types.Material{}, Diameter: 1.0, AutoAperture: true},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 80.0},
	}
	surface.Precompute(surfs)
	opt := NewOptimizer(Config{
		Surfaces: surfs,
		Fields: []types.FieldItem{
			{ID: 0, AngleDeg: 0, Weight: 1},
			{ID: 1, AngleDeg: 10, Weight: 1},
		},
		MeritTerms: []MeritTerm{
			{Kind: MeritSpotRMS, FieldAngle: 0, FieldIndex: 0, FieldWeight: 1, Wavelength: 0.00058756, WavWeight: 1, Weight: 1},
		},
		GlassCatalog: gc,
		StopSurface:  1,
		RefSurface:   3,
		NumRays:      64,
	})
	var warns []string
	prev := Warnf
	Warnf = func(format string, args ...interface{}) { warns = append(warns, fmt.Sprintf(format, args...)) }
	defer func() { Warnf = prev }()
	opt.WarnBeamClipping(opt.getInitialState())
	if len(warns) != 0 {
		t.Errorf("auto_aperture surface warned against its stored placeholder: %v", warns)
	}
}

// TestWarnBeamClippingExemptsImageSurface pins the second false positive: the
// reference/image surface is where the beam is supposed to spread, so a wide
// field leaving its diameter is not a clipped clear aperture (ray_validity
// already exempts it).
func TestWarnBeamClippingExemptsImageSurface(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	surfs := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0, Thickness: 5.0, Material: types.Material{Key: "N-BK7"}, Diameter: 20.0, Role: "stop"},
		{ID: 2, Type: types.Sphere, Curvature: 0, Thickness: 40.0, Material: types.Material{}, Diameter: 80.0},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 5.0},
	}
	surface.Precompute(surfs)
	opt := NewOptimizer(Config{
		Surfaces: surfs,
		Fields: []types.FieldItem{
			{ID: 0, AngleDeg: 0, Weight: 1},
			{ID: 1, AngleDeg: 10, Weight: 1},
		},
		MeritTerms: []MeritTerm{
			{Kind: MeritSpotRMS, FieldAngle: 10, FieldIndex: 1, FieldWeight: 1, Wavelength: 0.00058756, WavWeight: 1, Weight: 1},
		},
		GlassCatalog: gc,
		StopSurface:  1,
		RefSurface:   3,
		NumRays:      64,
	})
	var warns []string
	prev := Warnf
	Warnf = func(format string, args ...interface{}) { warns = append(warns, fmt.Sprintf(format, args...)) }
	defer func() { Warnf = prev }()
	opt.WarnBeamClipping(opt.getInitialState())
	for _, w := range warns {
		if strings.Contains(w, "surface 3") {
			t.Errorf("image surface reported as a clipped clear aperture: %v", warns)
		}
	}
}

// TestWarnBeamClippingExemptsStopFreeCap pins the implicit-aperture rule: with
// no stop_surface the tightest fixed surface is the dynamic-pupil cap, and the
// grid is launched at its edge. The off-axis rim landing outside the projected
// circle is the design's vignetting, not a diameter nobody sized, so the
// diagnostic must stay quiet (it flagged the f/4 sample's 12.5 mm limiter).
func TestWarnBeamClippingExemptsStopFreeCap(t *testing.T) {
	gc := glass.NewCatalog()
	gc.Add(types.Glass{Type: types.GlassTypeModel, Label: "N-BK7", ND: 1.5168, VD: 64.17})
	surfs := []types.Surface{
		{ID: 1, Type: types.Sphere, Curvature: 0, Thickness: 5.0, Material: types.Material{Key: "N-BK7"}, Diameter: 80.0},
		{ID: 2, Type: types.Sphere, Curvature: 0, Thickness: 40.0, Material: types.Material{}, Diameter: 20.0},
		{ID: 3, Type: types.Sphere, Curvature: 0, Thickness: 20.0, Material: types.Material{}, Diameter: 1.0, AutoAperture: true},
		{ID: 4, Type: types.Sphere, Curvature: 0, Thickness: 0, Material: types.Material{}, Diameter: 80.0},
	}
	surface.Precompute(surfs)
	opt := NewOptimizer(Config{
		Surfaces: surfs,
		Fields: []types.FieldItem{
			{ID: 0, AngleDeg: 0, Weight: 1},
			{ID: 1, AngleDeg: 10, Weight: 1},
		},
		MeritTerms: []MeritTerm{
			{Kind: MeritSpotRMS, FieldAngle: 10, FieldIndex: 1, FieldWeight: 1, Wavelength: 0.00058756, WavWeight: 1, Weight: 1},
		},
		GlassCatalog: gc,
		RefSurface:   4,
		NumRays:      64,
	})
	var warns []string
	prev := Warnf
	Warnf = func(format string, args ...interface{}) { warns = append(warns, fmt.Sprintf(format, args...)) }
	defer func() { Warnf = prev }()
	opt.WarnBeamClipping(opt.getInitialState())
	for _, w := range warns {
		if strings.Contains(w, "surface 2 ") {
			t.Errorf("stop-free dynamic-pupil cap reported as clipping: %v", warns)
		}
	}
}

// beam leaves is reported with the surface, field and deficit; a generous
// aperture is silent; a config that already manages the clearance is silent;
// and a deliberately vignetted field is measured on its prescribed pupil, not
// the full aperture — measuring the full pupil would report the vignetting
// itself as clipping and train the user to ignore the warning.
func TestWarnBeamClipping(t *testing.T) {
	// The apertures comfortably cover the beam: nothing to report.
	if _, warns := beamClipProbe(40.0, nil, nil); len(warns) != 0 {
		t.Errorf("generous apertures warned: %v", warns)
	}

	// Surface 2 at 25 mm (radius 12.5) is inside the beam at both fields.
	_, warns := beamClipProbe(25.0, nil, nil)
	if len(warns) != 3 {
		t.Fatalf("warns = %d (%v), want a header plus one line per field", len(warns), warns)
	}
	for _, want := range []string{"surface 2", "field 0 (0.00 deg)", "field 1 (10.00 deg)", "over by"} {
		if !strings.Contains(warns[0]+warns[1]+warns[2], want) {
			t.Errorf("warnings %v do not mention %q", warns, want)
		}
	}
	// The deficit is the shortfall of the diameter, not a rounding artefact.
	// Worst first: at this probe the 10 deg field's footprint is marginally the
	// larger one (the two are within 0.2 mm of each other, so the order is a
	// property of the hex grid's sampling, not of the design).
	if !strings.Contains(warns[1], "over by 3.09") || !strings.Contains(warns[2], "over by 2.91") {
		t.Errorf("deficits do not match the measured envelope: %v", warns[1:])
	}

	// A beam_clearance constraint says the user is managing the clearance.
	guard := []types.ConstraintOperand{{
		ID: "clearance", Kind: types.ConstraintInequalityLower, Measure: types.MeasureBeamClearance,
		Surface: 2, Field: 1, Lower: 0, Weight: 1, Active: true,
	}}
	if _, warns := beamClipProbe(25.0, nil, guard); len(warns) != 0 {
		t.Errorf("beam_clearance-guarded config warned: %v", warns)
	}

	// Both fields vignetted to half the pupil: the prescribed beam at surface 2
	// is ~8.7 mm, so a 20 mm aperture covers it. Without the vignetting the same
	// aperture would be reported.
	vig := &types.VignettingDef{CompressionX: 0.5, CompressionY: 0.5}
	if _, warns := beamClipProbe(20.0, vig, nil); len(warns) != 0 {
		t.Errorf("vignetted field measured against the full aperture: %v", warns)
	}
	if _, warns := beamClipProbe(20.0, nil, nil); len(warns) != 3 {
		t.Errorf("full-pupil beam with a 20 mm aperture warned %d times, want 3", len(warns))
	}
}
