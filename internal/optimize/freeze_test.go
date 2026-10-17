package optimize

import (
	"math"
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
)

const freezeTestWavelength = 0.00058756

// freezeTestModel builds a single-config model on the front-stop singlet with
// one image-height field and one curvature variable, so a field angle can be
// checked against a geometry the variable moves.
func freezeTestModel(t *testing.T, imageHeight float64) *Optimizer {
	t.Helper()
	opt := NewOptimizer(Config{
		Surfaces:     pupilLaunchSurfaces(),
		GlassCatalog: zeroPupilGC(),
		StopSurface:  1,
		RefSurface:   3,
		Fields: []types.FieldItem{{
			ID: 0, ImageHeight: imageHeight, Weight: 1, Direction: []float64{0, 1},
		}},
		MeritTerms: []MeritTerm{{
			Kind: MeritSpotRMS, FieldIndex: 0, FieldWeight: 1,
			Wavelength: freezeTestWavelength, WavWeight: 1, Weight: 1,
		}},
		Variables:      []Variable{{Name: "c2", SurfaceID: 2, Param: "curvature", Min: -0.1, Max: 0.1}},
		NumRays:        16,
		ApertureMargin: 0.2,
	})
	if len(opt.configs) != 1 || len(opt.configs[0].fieldResolutions) != 1 || len(opt.configs[0].meritTerms) != 1 {
		t.Fatalf("setup: %d configs, %d resolutions, %d terms",
			len(opt.configs), len(opt.configs[0].fieldResolutions), len(opt.configs[0].meritTerms))
	}
	return opt
}

// freezeTestTarget is an image height the front-stop singlet can form: half of
// what the on-axis-launch chief ray reaches at 15 deg (measured through the
// entrance-pupil centre, the singlet's entrance pupil sitting on the stop).
func freezeTestTarget() float64 {
	zero := 0.0
	return traceChiefImageHeight(pupilLaunchSurfaces(), 15.0, freezeTestWavelength, zeroPupilGC(), &zero) / 2
}

// TestFreezeResolvesImageHeightField pins that a field declared by image_height
// is resolved to its angle at construction, reported, and read by the merit —
// not read as the declared angle, which is 0 for such a field.
func TestFreezeResolvesImageHeightField(t *testing.T) {
	target := freezeTestTarget()
	opt := freezeTestModel(t, target)
	cfg := &opt.configs[0]

	rows := opt.FieldResolutionDiagnostics()
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want exactly one", rows)
	}
	r := rows[0]
	if !r.Resolved {
		t.Fatalf("row = %+v, want resolved", r)
	}
	if r.Requested != target {
		t.Errorf("requested = %v, want the declared %v", r.Requested, target)
	}
	if math.Abs(r.Reached-target) > 0.01*target {
		t.Errorf("delivered %v, want within 1%% of %v", r.Reached, target)
	}
	if r.AngleDeg <= 0 {
		t.Errorf("angle = %v, want > 0 (a non-zero image height is not on-axis)", r.AngleDeg)
	}
	if r.Config != "config1" || r.FieldIndex != 0 || r.FieldID != 0 {
		t.Errorf("row identifies the field as %+v", r)
	}

	// The term reads the frozen angle, not the field's declared angle 0.
	cfgSurfaces, tempGC, _ := opt.applyVariables(opt.InitialState())
	if got := opt.termFieldAngle(cfg, &cfg.meritTerms[0], cfgSurfaces[cfg.id], effectiveGC(opt.gc, tempGC)); got != r.AngleDeg {
		t.Errorf("termFieldAngle = %v, want the frozen %v", got, r.AngleDeg)
	}

	// A field declared by angle is not reported: it needs no resolution.
	if rows := (&Optimizer{configs: []config{{fields: []types.FieldItem{{ID: 0, AngleDeg: 5}}}}}).FieldResolutionDiagnostics(); len(rows) != 0 {
		t.Errorf("angle-declared field reported as %+v, want nothing", rows)
	}
}

// TestFreezePinsImageHeightFieldAngle pins the freeze itself: the angle must not
// follow the variables. Re-resolving at a moved geometry returns a different
// angle — what the merit used to pick up per evaluation, and what a spot-only
// merit is lowered by (a wide field collapsing toward the axis).
func TestFreezePinsImageHeightFieldAngle(t *testing.T) {
	target := freezeTestTarget()
	opt := freezeTestModel(t, target)
	cfg := &opt.configs[0]
	frozen := cfg.fieldResolutions[0].angle

	// Move the geometry well away from the start point, staying inside the
	// variable's box.
	x := append([]float64(nil), opt.InitialState()...)
	x[0] += 0.02
	movedSurfaces, tempGC, _ := opt.applyVariables(x)
	movedGC := effectiveGC(opt.gc, tempGC)
	if v := opt.variables[0]; x[0] < v.Min || x[0] > v.Max {
		t.Fatalf("setup: %v leaves [%v, %v]", x[0], v.Min, v.Max)
	}
	moved, _, _ := opt.resolveImageHeightField(cfg, movedSurfaces[cfg.id], target, freezeTestWavelength, movedGC)
	if math.Abs(moved-frozen) < 1e-3 {
		t.Fatalf("setup: the moved geometry resolves %v against the frozen %v; the test cannot show the pin", moved, frozen)
	}
	if got := opt.termFieldAngle(cfg, &cfg.meritTerms[0], movedSurfaces[cfg.id], movedGC); got != frozen {
		t.Errorf("termFieldAngle at the moved geometry = %v, want the frozen %v", got, frozen)
	}
}

// TestFreezeKeepsUnreachableImageHeightFieldOffAxis pins that a declared height
// the system cannot form is reported unresolved with the closest reachable
// angle — never angle 0, the on-axis field, which a spot-only merit rewards.
func TestFreezeKeepsUnreachableImageHeightFieldOffAxis(t *testing.T) {
	const target = 1e6
	opt := freezeTestModel(t, target)
	cfg := &opt.configs[0]

	rows := opt.FieldResolutionDiagnostics()
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want exactly one", rows)
	}
	r := rows[0]
	if r.Resolved {
		t.Errorf("row = %+v, want unresolved", r)
	}
	if r.AngleDeg <= 0 {
		t.Errorf("angle = %v, want > 0 (an unreachable field must not fold onto the axis)", r.AngleDeg)
	}
	if r.Requested != target {
		t.Errorf("requested = %v, want %v", r.Requested, target)
	}
	if cfg.fieldResolutions[0].angle != r.AngleDeg {
		t.Errorf("the merit angle %v differs from the reported %v", cfg.fieldResolutions[0].angle, r.AngleDeg)
	}
}
