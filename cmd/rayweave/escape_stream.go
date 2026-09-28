package main

import (
	"bytes"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/hiroki/rayweaver/internal/escape"
	"github.com/hiroki/rayweaver/internal/optimize"
	"github.com/hiroki/rayweaver/internal/types"
	"gopkg.in/yaml.v3"
)

// escapeStreamTails names the top-level Input keys the run still mutates after
// the header has been emitted. A YAML mapping may define a key only once, so
// those keys are deferred to the completion write instead. configs is always
// deferred (the best solution lands there); chief only when a pupil_model_*
// variable rewrites chief.pupil_model during the run. glass_catalog is never
// deferred: the best-solution glass list is always empty (applyGlassOverrides
// returns no glasses), so nothing appends to it after the header.
type escapeStreamTails struct {
	chief bool
}

// streamParamsBlock is the part of escape_result written together with the
// header: the resolved parameters, known before the search starts.
type streamParamsBlock struct {
	Params types.EscapeParamsInfo `yaml:"params"`
}

// streamCompletion is the rest of escape_result, written once the search has
// finished. The field order is the emission order: best index and merit, then
// the run outcome, then the lists. minima_improvements comes last so it lands
// immediately before the deferred top-level keys (configs).
type streamCompletion struct {
	BestIndex          int                   `yaml:"best_index"`
	BestMerit          float64               `yaml:"best_merit"`
	TimedOut           bool                  `yaml:"timed_out,omitempty"`
	Interrupted        bool                  `yaml:"interrupted,omitempty"`
	InfeasibleBasins   []types.EscapeMinimum `yaml:"infeasible_basins,omitempty"`
	MinimaImprovements []types.EscapeMinimum `yaml:"minima_improvements,omitempty"`
}

// escapeStream writes one pipeline document as an append-only sequence of
// writes: the settled top-level part first, one escape_result.minima entry per
// discovered solution, and the completion (run outcome, improved minima,
// deferred top-level keys) when the search ends. No write rewrites or seeks, so
// a document read while the run is going (or after it was killed) is a single
// YAML document at every write boundary, and only a kill in the middle of one
// write can corrupt it. Writes are serialised on mu; the record callback runs
// under the escape store's lock, so every append is already sequential.
type escapeStream struct {
	mu       sync.Mutex
	w        io.Writer
	tails    escapeStreamTails
	err      error
	pos      map[int]int  // store index -> position in escape_result.minima
	improved map[int]bool // store index whose point was replaced in the run
}

// newEscapeStream creates a stream writing to w (stdout in production).
func newEscapeStream(w io.Writer, tails escapeStreamTails) *escapeStream {
	return &escapeStream{
		w:        w,
		tails:    tails,
		pos:      make(map[int]int),
		improved: make(map[int]bool),
	}
}

// header writes the settled top-level part of the document — every Input key
// the run will not touch again — followed by escape_result with its params and
// an (empty) minima list left open. It is the first write of the run, so
// metadata.created_at records the run start rather than the run end.
func (s *escapeStream) header(in types.Input, params types.EscapeParamsInfo) {
	head := in
	head.Configs = nil // the best solution lands there only at completion
	if s.tails.chief {
		head.Chief = nil
	}
	block, err := marshalIndent(types.Output{Input: head}, 0)
	if err != nil {
		s.setErr(err)
		return
	}
	paramsBlock, err := marshalIndent(streamParamsBlock{params}, 4)
	if err != nil {
		s.setErr(err)
		return
	}
	var buf bytes.Buffer
	buf.Write(block)
	buf.WriteString("escape_result:\n")
	buf.Write(paramsBlock)
	buf.WriteString("    minima:\n")
	s.write(buf.Bytes())
}

// record implements escape.RecordHandler. A brand-new minimum is appended to
// escape_result.minima in discovery order. A replacement (Store.Replace) is
// only remembered: the list is append-only, so the improved point is written
// once — as escape_result.minima_improvements, when the run completes — see
// improvements. build is called only for points that are actually streamed.
// Infeasible basins are never streamed: they are a separate list written at
// completion (and only with --keep-infeasible).
func (s *escapeStream) record(idx int, p escape.Point, isNew bool, build func(position, storeIdx int, p escape.Point) types.EscapeMinimum) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return
	}
	if !isNew {
		if _, streamed := s.pos[idx]; streamed {
			s.improved[idx] = true
		}
		return
	}
	if p.Status == escape.MinStatusInfeasibleBasin {
		return
	}
	position := len(s.pos)
	m := build(position, idx, p)
	s.pos[idx] = position
	block, err := marshalIndent([]types.EscapeMinimum{m}, 8)
	if err != nil {
		s.setErr(err)
		return
	}
	s.write(block)
}

// improvements materialises the final version of every minimum the store
// replaced during the run (Store.Replace), ordered like the streamed list.
// Points that never made it into minima (infeasible basins, or a point whose
// store index was skipped) are ignored: the streamed entry they would have
// superseded does not exist.
func (s *escapeStream) improvements(res escape.Result, build func(position, storeIdx int, p escape.Point) types.EscapeMinimum) []types.EscapeMinimum {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.improved) == 0 || s.err != nil {
		return nil
	}
	// res.Minima is merit-sorted; map the store index back to its point.
	at := make(map[int]int, len(res.MinimaIdx))
	for i, storeIdx := range res.MinimaIdx {
		at[storeIdx] = i
	}
	idxs := make([]int, 0, len(s.improved))
	for storeIdx := range s.improved {
		idxs = append(idxs, storeIdx)
	}
	sort.Slice(idxs, func(i, j int) bool {
		return s.pos[idxs[i]] < s.pos[idxs[j]]
	})
	var out []types.EscapeMinimum
	for _, storeIdx := range idxs {
		position, ok := s.pos[storeIdx]
		if !ok {
			continue
		}
		i, ok := at[storeIdx]
		if !ok {
			continue // the point is no longer a feasible minimum
		}
		out = append(out, build(position, storeIdx, res.Minima[i]))
	}
	return out
}

// complete closes the document: the rest of escape_result, then the top-level
// Input keys the run deferred (configs, and chief when a pupil_model_*
// variable rewrote it). It is one Write, so the final document is either
// complete or ends where the previous write did.
func (s *escapeStream) complete(c streamCompletion, tail types.Input) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return
	}
	block, err := marshalIndent(c, 4)
	if err != nil {
		s.setErr(err)
		return
	}
	out, err := yaml.Marshal(types.Output{Input: tail})
	if err != nil {
		s.setErr(err)
		return
	}
	var buf bytes.Buffer
	buf.Write(block)
	// An Input with every key unset marshals to "{}" — appending that after an
	// open mapping would be a syntax error, so it is skipped.
	if strings.TrimSpace(string(out)) != "{}" {
		buf.Write(out)
	}
	s.write(buf.Bytes())
}

// Err returns the first write (or marshalling) error, nil otherwise.
func (s *escapeStream) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// bestStreamIndex maps the run's best point to its position in the streamed
// minima list. The store index is not the list position (infeasible points
// consume store indices too, and the list is discovery-ordered while the
// result list is merit-ordered), so the position has to be looked up. It is -1
// when the run found no minimum or that point was never streamed.
func bestStreamIndex(res escape.Result, st *escapeStream) int {
	if res.BestIdx < 0 || res.BestIdx >= len(res.MinimaIdx) {
		return -1
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	position, ok := st.pos[res.MinimaIdx[res.BestIdx]]
	if !ok {
		return -1
	}
	return position
}

// streamTailKeys collects the deferred top-level Input keys of a finished run:
// configs (whose surfaces now hold the best solution) and chief when a
// pupil_model_* variable rewrote chief.pupil_model while the search ran.
func streamTailKeys(in types.Input, st *escapeStream) types.Input {
	tail := types.Input{Configs: in.Configs}
	if st.tails.chief {
		tail.Chief = in.Chief
	}
	return tail
}

func (s *escapeStream) setErr(err error) {
	if s.err == nil {
		s.err = err
	}
}

// write appends one block. A failed write sticks: later writes are skipped so
// a broken stream does not grow a document missing its middle.
func (s *escapeStream) write(b []byte) {
	if s.err != nil || len(b) == 0 {
		return
	}
	if _, err := s.w.Write(b); err != nil {
		s.setErr(err)
	}
}

// marshalIndent marshals v and indents every non-empty line by n spaces, so a
// fragment generated at document level drops into an already-open block at
// column n. Shifting every line by the same amount preserves the relative
// indentation yaml.v3 chose, which is what makes the stitched document parse
// back to exactly the marshalled value. Empty lines stay empty (a block scalar
// keeps its content).
func marshalIndent(v any, n int) ([]byte, error) {
	b, err := yaml.Marshal(v)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return b, nil
	}
	s := strings.TrimSuffix(string(b), "\n")
	pad := strings.Repeat(" ", n)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = pad + l
		}
	}
	return []byte(strings.Join(lines, "\n") + "\n"), nil
}

// escapeStreamEnabled resolves the streaming switch under the three principles:
// the --stream flag wins when it was given, else optimization.escape.stream
// when the document sets it, else the built-in default (stream on). fromFlag
// reports that the CLI supplied the value, so only then is it written back to
// the output document.
func escapeStreamEnabled(cfg *types.EscapeConfig, command string) (enabled, fromFlag bool) {
	if v, set := escapeStreamFlag(command); set {
		return v, true
	}
	if cfg != nil && cfg.Stream != nil {
		return *cfg.Stream, false
	}
	return true, false
}

// escapeStreamFlag returns the --stream flag of the running subcommand and
// whether it was given. The value is an explicit "true"/"false" string so that
// both `--stream false` and `--stream=false` work (a Go bool flag only
// understands the latter); a bare `--stream` is rejected like any other flag
// missing its argument. escape and pso parse their own flag sets but share the
// same EscapeConfig (runPSO aliases optimization.pso.escape into it), so the
// two pairs are kept separate only to mirror the flag declarations.
func escapeStreamFlag(command string) (value, set bool) {
	raw, given := optEscapeStream, optEscapeStreamSet
	if command == "pso" {
		raw, given = optPSOStream, optPSOStreamSet
	}
	if !given {
		return false, false
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		errOut("Error: --stream %q: expected true or false", raw)
		os.Exit(1)
	}
	return v, true
}

// hasPupilModelVariables reports whether any optimisation variable rewrites
// chief.pupil_model (single-config path), which is what makes the chief
// section a deferred (tail) key.
func hasPupilModelVariables(variables []optimize.Variable) bool {
	for _, v := range variables {
		switch v.Param {
		case "pupil_model_axial_position", "pupil_model_diameter":
			return true
		}
	}
	return false
}

// hasPupilModelVariablesMulti is the multi-config counterpart: only local
// variables are applied to the shared chief pupil model (see
// applyPupilVariablesMulti).
func hasPupilModelVariablesMulti(opt *types.OptimizationConfig) bool {
	if opt == nil {
		return false
	}
	for _, lv := range opt.LocalVariables {
		if lv.Active && lv.Target.Type == "pupil_model" {
			return true
		}
	}
	return false
}
