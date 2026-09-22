package main

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/hiroki/rayweaver/internal/chief"
	"github.com/hiroki/rayweaver/internal/dls"
	"github.com/hiroki/rayweaver/internal/optimize"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
)

// validationNopLogger satisfies dls.Logger; the test never runs a DLS solve,
// the logger only rides along in the config.
type validationNopLogger struct{}

func (validationNopLogger) LogIter(int, float64, float64, float64, []float64, []dls.ConstraintState) {
}
func (validationNopLogger) LogFinal(int, string, float64, float64, []float64, []dls.ConstraintState) {
}

// surfaceThickness returns the stored thickness of the surface with the given ID.
func surfaceThickness(surfaces []types.Surface, id int) (float64, bool) {
	for _, s := range surfaces {
		if s.ID == id {
			return s.Thickness, true
		}
	}
	return 0, false
}

// TestEscapeValidationOnSavedMinimum drives the real escape feasibility
// validation path (singleEscapeConfig → NewOptimizer → validationSurfaces →
// effectivePupilModel → the pupil-grid throughput trace) on broad-min43.yaml,
// a minimum of the v22 run that was rejected as insufficient_field_throughput
// despite being the run's best throughput-class basin. Three fixes must all be
// present for it to pass min_throughput_ratio:
//
//  1. PupilModel forwarded into the optimizer config, so FinalPupilModels is
//     populated and the grid is centred on the optimised virtual entrance
//     pupil instead of falling back to the static input model;
//  2. FinalApertures diameters overlaid, so the tool-sized auto apertures do
//     not clip the beam the merit traced (the stored diameters do);
//  3. FinalConfigs geometry, so the back-focus hard solve traces the same
//     image plane as the saved minimum (the raw variable projection keeps the
//     stale template thickness on a freshly-degraded input).
//
// Expected verdict on this file: the worst field (23°) sits near 0.30
// (76/256), comfortably above the configured min_throughput_ratio of 0.1.
// The 23° loss is intrinsic to the design — opening every aperture 10× leaves
// the identical survivor set (the drops become glass_path_too_short /
// missed_surface), so the number is stable against aperture retuning.
//
// The evidence file lives in the gitignored untrack-samples/ directory; the
// test skips when it is absent.
func TestEscapeValidationOnSavedMinimum(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "untrack-samples", "escape-broad-v22", "broad-min43.yaml"))
	if err != nil {
		t.Skipf("saved minimum not available: %v", err)
	}
	input := parseYAML[types.Input](data)
	if input.Optimization == nil || input.Chief == nil || len(input.Configs) == 0 {
		t.Fatal("unexpected saved-minimum structure (optimization/chief/configs missing)")
	}

	gc, _ := loadCatalogs(&input)
	surfaces := input.Configs[0].Surfaces
	surface.Precompute(surfaces)

	variables := buildOptimizeVariables(input.Optimization, gc)
	meritTerms := buildMeritTerms(input)
	gctx := buildGlassPhaseContext(&input)

	// Mirror runEscapeSingle and its worker factory exactly.
	cfg := singleEscapeConfig(input, surfaces, variables, meritTerms, gc, gctx, validationNopLogger{})
	if cfg.PupilModel == nil {
		t.Fatal("singleEscapeConfig dropped the virtual entrance pupil model: " +
			"FinalPupilModels would be empty and validation would fall back to the stale static pupil")
	}
	opt := optimize.NewOptimizer(cfg)
	opt.SetPowerSolveEnabled(false)
	if input.Optimization.BackFocusSolve != nil && input.Optimization.BackFocusSolve.Enabled {
		opt.SetBackFocusSolve(input.Optimization.BackFocusSolve)
	}
	if input.Optimization.MeritSchedule != nil {
		opt.SetMeritSchedule(input.Optimization.MeritSchedule)
	}
	for _, terms := range gctx.merit {
		opt.SetGlassMerit("config1", terms)
	}

	x := opt.InitialState()

	// validationSurfaces must take the final-state hooks; the variable-only
	// fallback (what validation traced before the fixes) is a failure.
	surf := validationSurfaces(opt, x, "config1", func() []types.Surface {
		t.Fatal("optimizer does not expose FinalConfigs/FinalApertures")
		return nil
	})

	// Fix 1: the optimised virtual pupil, as a distinct copy from the static
	// model (the fallback returns the input pointer itself).
	pm := effectivePupilModel(opt, x, "config1", input.Chief.PupilModel)
	if pm == input.Chief.PupilModel {
		t.Fatal("effectivePupilModel fell back to the static input pupil (FinalPupilModels empty?)")
	}
	t.Logf("virtual pupil: axial_position=%.4f diameter=%.4f", pm.AxialPosition, pm.Diameter)
	if math.Abs(pm.AxialPosition-input.Chief.PupilModel.AxialPosition) > 1e-9 ||
		math.Abs(pm.Diameter-input.Chief.PupilModel.Diameter) > 1e-9 {
		t.Errorf("optimised pupil (z=%v d=%v) does not match the pupil_model_* variables (z=%v d=%v)",
			pm.AxialPosition, pm.Diameter,
			input.Chief.PupilModel.AxialPosition, input.Chief.PupilModel.Diameter)
	}

	// Fix 2: tool-sized auto apertures. The stored diameters max out at 36;
	// the beam envelope sizes sit near 44.
	aps := opt.FinalApertures(x)["config1"]
	sized := 0
	for i := range surf {
		if !surf[i].AutoAperture {
			continue
		}
		want, ok := aps[surf[i].ID]
		if !ok {
			t.Errorf("surface %d: auto aperture missing from FinalApertures", surf[i].ID)
			continue
		}
		if surf[i].Diameter != want {
			t.Errorf("surface %d: diameter %v != sized aperture %v", surf[i].ID, surf[i].Diameter, want)
		}
		if want > 40 {
			sized++
		}
	}
	if sized == 0 {
		t.Errorf("no auto aperture sized beyond the stored diameters (max 36): %v", aps)
	}
	t.Logf("sized auto apertures: %v", aps)

	// Fix 3: the traced image plane matches the saved minimum. The saved file
	// carries the terminal wavefront solve; the fresh optimizer re-solves
	// paraxially (the static type, as at validation setup), so allow a small
	// method delta — but nothing like the 24 mm template divergence.
	bfID := input.Optimization.BackFocusSolve.Surface
	rawSurf, _ := applyEscapeX(surfaces, variables, x, gc)
	rawTh, rawOK := surfaceThickness(rawSurf, bfID)
	gotTh, gotOK := surfaceThickness(surf, bfID)
	if !rawOK || !gotOK {
		t.Fatalf("back-focus target surface %d not found (raw=%v validated=%v)", bfID, rawOK, gotOK)
	}
	t.Logf("back-focus surface %d thickness: raw projection=%v validated=%v", bfID, rawTh, gotTh)
	if math.Abs(gotTh-rawTh) > 3.0 {
		t.Errorf("validated image plane thickness %v diverges from the minimum's %v", gotTh, rawTh)
	}

	// The throughput verdict itself: replicate validateFn's grid trace.
	fieldDefs := input.Chief.Fields
	refSurf := chiefRefSurface(input)
	numRays := input.Optimization.NumRays
	if numRays <= 0 {
		numRays = 64
	}
	validationNumRays := numRays
	threshold := 0.3
	if input.Optimization.Escape != nil {
		if input.Optimization.Escape.ValidationNumRays > 0 {
			validationNumRays = input.Optimization.Escape.ValidationNumRays
		}
		if input.Optimization.Escape.MinThroughputRatio > 0 {
			threshold = input.Optimization.Escape.MinThroughputRatio
		}
	}
	wl := effectiveReferenceWavelength(input.Chief)
	stopSurface := 0
	if input.Chief.StopSurface != 0 {
		stopSurface = input.Chief.StopSurface
	}
	t.Logf("validation grid: %d rays/field, threshold %.2f, ref surface %d",
		validationNumRays, threshold, refSurf)

	sys := types.System{Surfaces: surf, StopSurface: stopSurface}
	results := chief.DetermineChiefRaysGrid(
		sys, fieldDefs, refSurf, validationNumRays, gc,
		types.NewCircularJones(true), wl,
		false, types.GridPolar, nil, nil, nil, pm, 0, 0,
	)
	if len(results) == 0 {
		t.Fatal("no chief-grid results for the field set")
	}
	worst := math.Inf(1)
	for _, r := range results {
		total := len(r.GridPoints)
		if total == 0 {
			t.Errorf("field %.2f°: empty grid (validation: field_unreachable)", r.FieldAngle)
			continue
		}
		valid := 0
		reasons := map[string]int{}
		for _, gp := range r.GridPoints {
			if gp.ImageX != nil && gp.ImageY != nil && gp.ErrorCode == "" {
				valid++
				continue
			}
			code := gp.ErrorCode
			if code == "" {
				code = "(no image coords)"
			}
			reasons[code]++
		}
		ratio := float64(valid) / float64(total)
		t.Logf("field %5.2f°: %d/%d = %.3f  drop reasons: %v", r.FieldAngle, valid, total, ratio, reasons)
		if ratio < worst {
			worst = ratio
		}
		if ratio < threshold {
			t.Errorf("field %.2f°: throughput %.3f below min_throughput_ratio %.2f — "+
				"validation would reject as insufficient_field_throughput", r.FieldAngle, ratio, threshold)
		}
	}
	// Pre-fix this minimum validated at 0/261 (rejected); the fixed path
	// measures ~0.297 — require a solid margin above the 0.1 threshold.
	if worst < 0.2 {
		t.Errorf("worst-field throughput %.3f far below the ~0.30 measured for this minimum", worst)
	} else {
		t.Logf("worst-field throughput: %.3f (threshold %.2f) → feasible", worst, threshold)
	}
}
