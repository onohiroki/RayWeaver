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
	mu    sync.Mutex
	stem  string
	ext   string
	build func(escape.Point) types.Input
	err   error
}

// newEscapeFileSaver creates a saver writing to base0.yaml, base1.yaml, ...
func newEscapeFileSaver(base string, build func(escape.Point) types.Input) *escapeFileSaver {
	stem, ext := splitSaveBase(base)
	return &escapeFileSaver{stem: stem, ext: ext, build: build}
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
	input := s.build(p)
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
	fmt.Fprintf(os.Stderr, "escape: minimum %d saved to %s (merit=%.6e)\n", idx, current, p.Merit)
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
	out := input
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
	out.Configs[0].Surfaces = surf
	if len(newGlasses) > 0 && out.GlassCatalog != nil {
		gcCopy := *out.GlassCatalog
		gcCopy.Entries = append(append([]types.Glass{}, out.GlassCatalog.Entries...), newGlasses...)
		out.GlassCatalog = &gcCopy
	}
	return out
}

// materializeMultiInput builds a clean, pipeline-compatible Input for the
// multi-config system with the variable vector x applied to every config.
func materializeMultiInput(input types.Input, opt *types.OptimizationConfig, x []float64) types.Input {
	out := input
	out.Configs = append([]types.Config{}, input.Configs...)
	configSurfaces := applyEscapeMulti(input.Configs, opt, x)
	for i := range out.Configs {
		if s, ok := configSurfaces[out.Configs[i].ID]; ok {
			out.Configs[i].Surfaces = s
		}
	}
	return out
}
