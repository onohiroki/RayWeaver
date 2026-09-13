package main

import (
	"fmt"
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
	input.EscapeMinimum = &types.EscapeMinimumInfo{
		Index:         idx,
		Merit:         p.Merit,
		Status:        types.EscapeMinimumStatus(statusString(p.Status)),
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
// single-config system with the variable vector x applied to the surfaces. The
// original input is not mutated; the glass catalog is only read (never
// written), so the saver is safe to run while DLS workers share the catalog.
func materializeSingleInput(input types.Input, surfaces []types.Surface, variables []optimize.Variable, x []float64, gc *glass.Catalog) types.Input {
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
	applySavedBackFocusSolve(input, &out.Configs[0], surf, gc)
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
func materializeMultiInput(input types.Input, opt *types.OptimizationConfig, x []float64, gc *glass.Catalog) types.Input {
	out := cloneChiefForOutput(input)
	out.Configs = append([]types.Config{}, input.Configs...)
	configSurfaces := applyEscapeMulti(input.Configs, opt, x)
	for i := range out.Configs {
		if s, ok := configSurfaces[out.Configs[i].ID]; ok {
			applySavedBackFocusSolve(input, &out.Configs[i], s, gc)
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
// saved minima land on the wavefront best focus the run ends on.
func applySavedBackFocusSolve(input types.Input, cfg *types.Config, surfaces []types.Surface, gc *glass.Catalog) {
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
	optimize.ApplyBackFocusSolve(surfaces, input.Optimization.BackFocusSolve, bfType, stopSurface, refWavelength, cfg.Fields, cfg.Wavelengths, gc)
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
