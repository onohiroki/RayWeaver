package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/hiroki/rayweaver/internal/escape"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/optimize"
	"github.com/hiroki/rayweaver/internal/types"
	"gopkg.in/yaml.v3"
)

// escapeFileSaver writes every recorded local minimum to a versioned YAML
// file. With base "result", the first minimum goes to result0.yaml, the second
// to result1.yaml, and so on (discovery order, matching the 0-based store
// index reported by the JSONL minimum events). When a recorded minimum is
// improved, the current resultN.yaml is first renamed to resultN.<version>.yaml
// so the older (worse) version is kept, then the better point is written to
// resultN.yaml. All writes are atomic (temp file + fsync + rename), so a
// killed process never leaves a partially-written file: every minimum found so
// far survives a SIGKILL.
type escapeFileSaver struct {
	mu             sync.Mutex
	stem           string
	ext            string
	build          func(escape.Point) types.Input
	progress       *escape.Progress
	keepInfeasible bool
	err            error
}

// newEscapeFileSaver creates a saver writing to base0.yaml, base1.yaml, ...
func newEscapeFileSaver(base string, build func(escape.Point) types.Input, progress *escape.Progress, keepInfeasible bool) *escapeFileSaver {
	stem, ext := splitSaveBase(base)
	return &escapeFileSaver{stem: stem, ext: ext, build: build, progress: progress, keepInfeasible: keepInfeasible}
}

// splitSaveBase separates a user-supplied base name into its stem and
// extension. A trailing .yaml/.yml suffix is kept as the extension; otherwise
// ".yaml" is used ("result" -> ("result", ".yaml")).
func splitSaveBase(base string) (string, string) {
	ext := filepath.Ext(base)
	if strings.EqualFold(ext, ".yaml") || strings.EqualFold(ext, ".yml") {
		return strings.TrimSuffix(base, ext), ext
	}
	return base, ".yaml"
}

// record implements escape.RecordHandler. The store invokes it while holding
// its lock (so invocations from parallel workers are already sequential); this
// saver additionally serialises on its own mutex.
func (s *escapeFileSaver) record(idx int, p escape.Point, isNew bool, version int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return
	}
	if !s.keepInfeasible && p.Status == escape.MinStatusInfeasibleBasin {
		return
	}
	input := s.build(p)
	input.GlobalMinimum = &types.GlobalMinimumInfo{
		Index:         idx,
		Merit:         p.Merit,
		Status:        types.GlobalMinimumStatus(statusString(p.Status)),
		InvalidReason: types.InvalidReason(reasonString(p.InvalidReason)),
	}
	data, err := yaml.Marshal(input)
	if err != nil {
		s.err = fmt.Errorf("escape: marshal minimum %d: %w", idx, err)
		return
	}
	current := fmt.Sprintf("%s%d%s", s.stem, idx, s.ext)
	if !isNew && version > 0 {
		archived := fmt.Sprintf("%s%d.%d%s", s.stem, idx, version, s.ext)
		if err := os.Rename(current, archived); err != nil && !os.IsNotExist(err) {
			s.err = fmt.Errorf("escape: rename %s -> %s: %w", current, archived, err)
			return
		}
	}
	if err := writeFileAtomic(current, data); err != nil {
		s.err = fmt.Errorf("escape: write %s: %w", current, err)
		return
	}
	s.progress.Event("minimum_saved", map[string]any{
		"index":   idx,
		"file":    current,
		"merit":   p.Merit,
		"new":     isNew,
		"version": version,
	})
}

// writeFileAtomic writes data to path via a temp file in the same directory,
// an fsync, and a rename, so a crash never leaves a partial file at path.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// materializeSingleInput builds a clean, pipeline-compatible Input for the
// single-config system with the variable vector x applied to the surfaces.
// apertures carries the sized auto_aperture diameters at x (nil keeps the
// stored ones), so the saved minimum traces the same clear apertures the
// optimizer evaluated — matching the optimize command's FinalApertures
// write-back. The original input is not mutated; the glass catalog is only
// read (never written), so the saver is safe to run while DLS workers share
// the catalog.
//
// The sized apertures are applied before the back-focus solve so the solved
// image plane is the best focus of the same (sized-diameter) beam the optimizer
// evaluated; solving first would freeze the plane on the stored-diameter bundle
// and leave the saved plane offset from the evaluated one.
func materializeSingleInput(input types.Input, surfaces []types.Surface, variables []optimize.Variable, x []float64, gc *glass.Catalog, apertures map[int]float64) types.Input {
	out := cloneChiefForOutput(input)
	out.Configs = append([]types.Config{}, input.Configs...)
	if len(out.Configs) == 0 {
		out.Configs = []types.Config{{
			ID:     "config1",
			Name:   "Config1",
			Weight: 1.0,
			Active: true,
		}}
	}
	surf, newGlasses := applyEscapeX(surfaces, variables, x, gc)
	applyApertures(surf, apertures)
	// The solve focuses this minimum's own virtual pupil, not the declared one:
	// axial_position/diameter are optimisation variables, and a plane solved at
	// the declared z lands up to 0.8 mm from the minimum's best focus (v50:
	// restoring the declared 15 mm made the saved plane its fixed point to
	// 1e-8 mm while the document's own 19.71 mm asked for 0.30 mm of travel).
	applySavedBackFocusSolve(out, &out.Configs[0], surf, gc, pupilModelForX(&out, variables, x))
	out.Configs[0].Surfaces = surf
	applyPupilVariables(&out, variables, x)
	if len(newGlasses) > 0 && out.GlassCatalog != nil {
		gcCopy := *out.GlassCatalog
		gcCopy.Entries = append(append([]types.Glass{}, out.GlassCatalog.Entries...), newGlasses...)
		out.GlassCatalog = &gcCopy
	}
	return out
}

// materializeMultiInput builds a clean, pipeline-compatible Input for the
// multi-config system with the variable vector x applied to every config.
// apertures carries the sized auto_aperture diameters per config (nil keeps
// the stored ones).
func materializeMultiInput(input types.Input, opt *types.OptimizationConfig, x []float64, gc *glass.Catalog, apertures map[string]map[int]float64) types.Input {
	out := cloneChiefForOutput(input)
	out.Configs = append([]types.Config{}, input.Configs...)
	configSurfaces := applyEscapeMulti(input.Configs, opt, x)
	// This minimum's own virtual pupil (see materializeSingleInput).
	pupil := pupilModelForXMulti(&out, opt, x)
	for i := range out.Configs {
		if s, ok := configSurfaces[out.Configs[i].ID]; ok {
			applyApertures(s, apertures[out.Configs[i].ID])
			applySavedBackFocusSolve(input, &out.Configs[i], s, gc, pupil)
			out.Configs[i].Surfaces = s
		}
	}
	applyPupilVariablesMulti(&out, opt, x)
	return out
}

// applySavedBackFocusSolve re-applies the configured back-focus hard solve to a
// materialized minimum so the saved file carries the same image plane the
// optimizer evaluated (the escape's variable-only materialization would
// otherwise keep the template thickness). It mirrors the optimizer's solve type
// selection using the merit schedule's terminal phase (weight_to == 1), so the
// saved minima land on the wavefront/spot best focus the run ends on.
//
// pupilModel is the minimum's own virtual entrance pupil — input's declared
// model with that minimum's pupil-model variables applied (pupilModelForX /
// pupilModelForInput). It is a parameter, not read from input, precisely
// because the declared model is only the seed: taking it from input focused
// every minimum's plane at the declared z while the document it ships carries
// the optimised one (the v50 vp_z sweep, 15 declared against 7.7..28.8 saved).
func applySavedBackFocusSolve(input types.Input, cfg *types.Config, surfaces []types.Surface, gc *glass.Catalog, pupilModel *types.PupilModelConfig) {
	if input.Optimization == nil || input.Optimization.BackFocusSolve == nil || !input.Optimization.BackFocusSolve.Enabled {
		return
	}
	if cfg == nil || gc == nil {
		return
	}
	stopSurface, refWavelength := 0, 0.0
	if input.Chief != nil {
		stopSurface = input.Chief.StopSurface
		refWavelength = input.Chief.ReferenceWavelength
	}
	bfType := savedBackFocusType(input.Optimization)
	before := append([]types.Surface(nil), surfaces...)
	solved := optimize.ApplyBackFocusSolve(surfaces, input.Optimization.BackFocusSolve, bfType,
		stopSurface, refWavelength, cfg.Fields, cfg.Wavelengths, gc, chiefRayDefinition(input), pupilModel)
	if !solved && realRayFocusType(bfType) {
		// A recorded minimum must never ship the plane it was recorded with:
		// `escape extract` copies these surfaces verbatim, so a wavefront/spot
		// failure would leave an image plane tens of mm out of focus in the
		// deliverable. The paraxial focus is cheap and always evaluable, so it
		// is the fallback (the failure itself was already reported by
		// ApplyBackFocusSolve).
		if optimize.ApplyBackFocusSolve(surfaces, input.Optimization.BackFocusSolve, "paraxial",
			stopSurface, refWavelength, cfg.Fields, cfg.Wavelengths, gc, chiefRayDefinition(input), pupilModel) {
			optimize.Warnf("warning: back-focus solve used the paraxial focus for a recorded minimum")
		}
		return
	}
	if !solved {
		return
	}

	// The probe compares first applications, so take the configured-rays shift
	// before the fixed-point iteration below re-runs the solve.
	firstShift := maxThicknessDelta(before, surfaces)
	if realRayFocusType(bfType) {
		if math.Abs(firstShift) > backFocusFixedPointToleranceMM {
			iterateBackFocusToFixedPoint(input, cfg, surfaces, bfType, stopSurface, refWavelength, gc, pupilModel)
		}
		warnIfBackFocusSamplingUnstable(input, cfg, before, firstShift, bfType, stopSurface, refWavelength, gc, pupilModel)
	}
}

// realRayFocusType reports whether bfType is one of the two real-ray
// back-focus solves. Both derive their plane from a pupil-grid trace, so both
// are fixed-point maps worth iterating to convergence on the save path and both
// are worth the sampling-stability probe; the paraxial solve is neither (it is
// analytic and exact at any sampling).
func realRayFocusType(bfType string) bool {
	return bfType == "wavefront" || bfType == "spot"
}

// backFocusFixedPointToleranceMM is the per-pass image-plane move below which
// the wavefront back-focus solve counts as settled.
const backFocusFixedPointToleranceMM = 0.01

// backFocusMaxPasses bounds the *additional* applications so a map that does
// not converge cannot wander (the initial application in the caller counts as
// pass 0).
const backFocusMaxPasses = 3

// iterateBackFocusToFixedPoint re-applies the wavefront / spot back-focus solve
// until it stops moving the image plane.
//
// The solve is a fixed-point map, not a one-shot correction: it derives the
// dynamic pupil and the best-focus search from the surfaces it is handed, so
// applying it to the materialised (pre-solve) surfaces lands short of its own
// optimum. Observed on a v50 smoke minimum: one application left the plane
// 0.149 mm from the point where re-solving from it returns 2.5e-7 mm. The
// document would be delivered out of focus under its own `all`-plane
// convention even though the solve ran.
//
// The save path already re-derives what the DLS could not know — the
// auto-aperture diameters are re-sized after the solve (see
// materializeSingleInput) — so delivering the solve's fixed point rather than
// its first shot is the same kind of correction: the recorded merit is a
// search statistic, the document is the deliverable.
func iterateBackFocusToFixedPoint(input types.Input, cfg *types.Config, surfaces []types.Surface, bfType string, stopSurface int, refWavelength float64, gc *glass.Catalog, pupilModel *types.PupilModelConfig) {
	shift := 0.0
	settled := false
	for pass := 1; pass <= backFocusMaxPasses; pass++ {
		snap := append([]types.Surface(nil), surfaces...)
		if !optimize.ApplyBackFocusSolve(surfaces, input.Optimization.BackFocusSolve, bfType,
			stopSurface, refWavelength, cfg.Fields, cfg.Wavelengths, gc, chiefRayDefinition(input), pupilModel) {
			return
		}
		shift = maxThicknessDelta(snap, surfaces)
		if math.Abs(shift) <= backFocusFixedPointToleranceMM {
			settled = true
			break
		}
	}
	if !settled {
		optimize.Warnf("warning: back-focus solve did not settle after %d passes (%.4f mm still moving); the recorded minimum's image plane is not at its own fixed point",
			backFocusMaxPasses, shift)
	}
}

// back-focus sampling-stability probe.
const (
	// backFocusSamplingToleranceMM is the image-plane shift difference between
	// the configured ray count and twice it above which the wavefront
	// back-focus solve counts as sampling-unstable.
	backFocusSamplingToleranceMM = 0.05
	// backFocusSamplingRelTolerance is that tolerance as a fraction of the
	// larger of the two shifts. A solve that already moves the plane by
	// several mm can afford a proportionally larger disagreement - the peak
	// search's own resolution scales with it - while a solve whose shift is
	// near zero is judged on the absolute value alone, which is exactly the
	// failure being caught: 0.03 mm at 128 rays against 0.30 mm at 400 on a
	// v49 minimum (a different extremum), as opposed to -5.885 against
	// -5.951 mm (1.1%, the same extremum).
	backFocusSamplingRelTolerance = 0.02
)

// backFocusSamplingUnstable reports whether the plane solved at the configured
// ray count and the plane solved at twice it disagree beyond tolerance.
func backFocusSamplingUnstable(applied, probe float64) bool {
	tol := backFocusSamplingToleranceMM
	if rel := backFocusSamplingRelTolerance * math.Max(math.Abs(applied), math.Abs(probe)); rel > tol {
		tol = rel
	}
	return math.Abs(probe-applied) > tol
}

// backFocusSamplingWarned dedupes the warning: a minimum is materialised twice
// per record (the standalone --save file and the aggregated document), so
// without this the identical message is emitted twice within the same
// millisecond.
var backFocusSamplingWarned sync.Map

// warnIfBackFocusSamplingUnstable re-runs the wavefront / spot back-focus
// solve at twice the configured ray count from the same starting plane. The
// wavefront solve maximises the coherent PSF peak, which is sampling-sensitive
// for aberrated field sets: at back_focus_solve.num_rays 128 the search can
// settle on a different extremum than at 200+, quietly shipping a minimum
// ~0.3 mm out of focus while every other gate still passes (the delivered MTF
// collapses). The spot solve is a closed-form quadratic of the traced rays and
// is far more stable, but it reads the same sampling, so it is probed the same
// way. This runs once per recorded minimum, so the extra solve is free.
func warnIfBackFocusSamplingUnstable(input types.Input, cfg *types.Config, before []types.Surface, appliedShift float64, bfType string, stopSurface int, refWavelength float64, gc *glass.Catalog, pupilModel *types.PupilModelConfig) {
	src := input.Optimization.BackFocusSolve
	// Above this the doubling is too expensive to be worth probing, and a
	// system that is unstable at 4000 rays has a different problem.
	if src.NumRays > 2000 {
		return
	}
	probe := *src
	if probe.NumRays > 0 {
		probe.NumRays *= 2
	} else {
		probe.NumRays = 400 // twice the 200-ray default of the real-ray solves
	}
	retry := append([]types.Surface(nil), before...)
	if !optimize.ApplyBackFocusSolve(retry, &probe, bfType, stopSurface, refWavelength,
		cfg.Fields, cfg.Wavelengths, gc, chiefRayDefinition(input), pupilModel) {
		return
	}
	altShift := maxThicknessDelta(before, retry)
	if !backFocusSamplingUnstable(appliedShift, altShift) {
		return
	}
	msg := fmt.Sprintf(
		"warning: back-focus solve is sampling-unstable: the image plane moves %.4f mm at num_rays %d but %.4f mm at %d rays (tolerance %.2f mm or 2%% of the larger shift); raise optimization.back_focus_solve.num_rays so the recorded minimum is not shipped out of focus",
		appliedShift, src.NumRays, altShift, probe.NumRays,
		math.Max(backFocusSamplingToleranceMM, backFocusSamplingRelTolerance*math.Max(math.Abs(appliedShift), math.Abs(altShift))))
	if _, loaded := backFocusSamplingWarned.LoadOrStore(msg, struct{}{}); !loaded {
		optimize.Warnf("%s", msg)
	}
}

// savedBackFocusType resolves the focus type to use for a saved minimum: the
// merit schedule's terminal phase type (the mode whose weight_to == 1) when one
// is declared, else the fixed back-focus type (default paraxial).
func savedBackFocusType(opt *types.OptimizationConfig) string {
	if opt != nil && opt.MeritSchedule != nil {
		best, bestW := "", 0.0
		for _, m := range opt.MeritSchedule.Modes {
			if m.BackFocusType != "" && m.WeightTo >= bestW {
				best, bestW = m.BackFocusType, m.WeightTo
			}
		}
		if best != "" {
			return best
		}
	}
	if opt != nil && opt.BackFocusSolve != nil && opt.BackFocusSolve.Type != "" {
		return opt.BackFocusSolve.Type
	}
	return "paraxial"
}
