package main

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/hiroki/rayweaver/internal/optimize"
	"github.com/hiroki/rayweaver/internal/psf"
	"github.com/hiroki/rayweaver/internal/surface"
	"github.com/hiroki/rayweaver/internal/types"
	"github.com/hiroki/rayweaver/internal/wavefront"
)

// TestMeritWavefrontMeasurementConsistency is the golden measurement harness
// for the merit-vs-standalone wavefront comparison.
//
// It builds the optimizer exactly as the escape worker factory does
// (singleEscapeConfig + SetPowerSolveEnabled(false) + back-focus + merit
// schedule, config id "config1"), forces the terminal (local) merit mode, and
// compares the optimizer's own wavefront_sphere_rms term value with the
// standalone wavefront.AnalyzeField reference-sphere RMS on the same
// (variable-applied, back-focus-solved) surfaces but with different aperture
// sources:
//
//	(a) the input file's stored auto-aperture diameters, and
//	(b) the tool-sized apertures (FinalApertures, 512-ray BeamEnvelope).
//
// The merit itself sizes the auto apertures with an 8-ray extent
// (sizeAutoApertures) before every evaluation, so (b) is the comparable
// column. This harness established that the earlier apparent 2-4x
// "merit under-measures off-axis" gap was an artefact of comparing against the
// raw input's stored diameters: the merit matches the tool-sized apertures to
// within the 8-ray vs 512-ray sizing delta (<=8% here). A saved minimum
// carries the sized diameters, so the deliverable and the merit agree.
//
// The merit contribution is inverted back to a raw RMS with
// rms = sqrt(contribution / weight) * wavelength, valid because the term has
// no target and the wavefront kinds normalise by the term wavelength. The
// harness logs the ratios and requires the merit value to be positive; it
// skips when the (gitignored) input is absent.
func TestMeritWavefrontMeasurementConsistency(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "untrack-samples", "wavefront-broad-v7.yaml"))
	if err != nil {
		t.Skipf("probe input not available: %v", err)
	}
	input := parseYAML[types.Input](data)
	if input.Optimization == nil || input.Chief == nil || len(input.Configs) == 0 {
		t.Fatal("unexpected input structure (optimization/chief/configs missing)")
	}

	gc, _ := loadCatalogs(&input)
	surfaces := input.Configs[0].Surfaces
	surface.Precompute(surfaces)

	variables := buildOptimizeVariables(input.Optimization, gc)
	meritTerms := buildMeritTerms(input)
	gctx := buildGlassPhaseContext(&input)

	cfg := singleEscapeConfig(input, surfaces, variables, meritTerms, gc, gctx, validationNopLogger{})
	opt := optimize.NewOptimizer(cfg)
	opt.SetPowerSolveEnabled(false) // escape worker default (glass phase only)
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
	// Force the terminal (local) mode so the wavefront_sphere_rms terms are
	// active: metric "phase" = 1 selects the mode whose weight_to == 1.
	opt.SetEscapePhase(1.0)
	opt.UpdateMeritWeights(x, 0)

	bd := opt.MeritBreakdown(x)

	// Invert the wavefront_sphere_rms contributions (weight 20000, wl 0.0005876
	// as declared in wavefront-broad-v7.yaml).
	const wfWeight = 20000.0
	const wl = 0.0005876
	meritRMS := map[float64]float64{}
	for key, contrib := range bd {
		if !strings.Contains(key, "wavefront_sphere_rms") {
			continue
		}
		i := strings.Index(key, "(f")
		if i < 0 {
			continue
		}
		s := key[i+2:]
		if j := strings.IndexAny(s, ",)"); j >= 0 {
			s = s[:j]
		}
		angle, err := strconv.ParseFloat(s, 64)
		if err != nil {
			continue
		}
		meritRMS[math.Round(angle*10)/10] = math.Sqrt(contrib/wfWeight) * wl
	}

	// Reproduce the optimizer's surfaces: variables applied + the terminal
	// (wavefront) back-focus solve.
	meritSurfaces, _ := applyEscapeX(surfaces, variables, x, gc)
	stopSurface, refWl0 := 0, 0.0
	if input.Chief != nil {
		stopSurface = input.Chief.StopSurface
		refWl0 = input.Chief.ReferenceWavelength
	}
	optimize.ApplyBackFocusSolve(meritSurfaces, input.Optimization.BackFocusSolve,
		savedBackFocusType(input.Optimization), stopSurface, refWl0,
		input.Configs[0].Fields, input.Configs[0].Wavelengths, gc)

	// Tool-sized apertures (FinalApertures) for the comparable column.
	sizedSurfaces := append([]types.Surface(nil), meritSurfaces...)
	if aps := opt.FinalApertures(x)["config1"]; len(aps) > 0 {
		for i := range sizedSurfaces {
			if d, ok := aps[sizedSurfaces[i].ID]; ok {
				sizedSurfaces[i].Diameter = d
			}
		}
	}

	refSurf := psf.DefaultReferenceSurface(surfaces)
	refWl := effectiveReferenceWavelength(input.Chief)
	numRays := input.Optimization.NumRays
	if numRays <= 0 {
		numRays = 64
	}
	margin := input.Optimization.ApertureMargin
	if margin <= 0 {
		margin = 1.0
	}
	pm := input.Chief.PupilModel

	t.Logf("merit wavefront_sphere_rms (escape-faithful): %d fields", len(meritRMS))
	for _, f := range input.Chief.Fields {
		angle := f.Angle
		fd := types.FieldDef{Angle: angle, Direction: []float64{0, 1}, Vignetting: f.Vignetting}
		var fz *float64
		if pm != nil {
			z := pm.AxialPosition
			fz = &z
		}
		storedEntry, _ := wavefront.AnalyzeField(
			types.System{Surfaces: meritSurfaces}, gc, fd, refSurf, numRays, refWl, margin, fz, pm)
		sizedEntry, _ := wavefront.AnalyzeField(
			types.System{Surfaces: sizedSurfaces}, gc, fd, refSurf, numRays, refWl, margin, fz, pm)

		key := math.Round(angle*10) / 10
		mrms, has := meritRMS[key]
		if !has {
			t.Logf("field %6.2f°: no wavefront_sphere_rms merit term (skipped)", angle)
			continue
		}
		t.Logf("field %6.2f°: merit=%.6g mm | stored-aperture standalone=%.6g (x%.2f) | final-aperture standalone=%.6g (x%.2f)",
			angle, mrms, storedEntry.Statistics.RMS, safeRatio(storedEntry.Statistics.RMS, mrms),
			sizedEntry.Statistics.RMS, safeRatio(sizedEntry.Statistics.RMS, mrms))
		if mrms <= 0 {
			t.Errorf("field %.2f°: merit wavefront_sphere_rms term present but zero", angle)
		}
	}
}

func safeRatio(a, b float64) float64 {
	if b == 0 {
		return math.Inf(1)
	}
	return a / b
}
