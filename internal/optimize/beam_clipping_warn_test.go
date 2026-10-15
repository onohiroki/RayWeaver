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
// (surface 1) plus two free apertures, with the 10 deg corner field reaching
// ~15.4 mm and the on-axis field ~15.0 mm at surface 2.
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

// TestWarnBeamClipping pins the G7 diagnostic: a clear aperture the prescribed
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
	// Worst first: the 10 deg field needs more than the on-axis field.
	if !strings.Contains(warns[1], "over by 2.9") || !strings.Contains(warns[2], "over by 2.4") {
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
