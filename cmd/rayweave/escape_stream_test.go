package main

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/hiroki/rayweaver/internal/escape"
	"github.com/hiroki/rayweaver/internal/types"
	"gopkg.in/yaml.v3"
)

// streamTestInput is a small but complete pipeline document: an optimization
// section (so the streamed params block reports something), a chief section and
// a configs section (whose keys are the ones the stream defers to the tail).
const streamTestInput = `metadata:
  tool:
    name: RayWeaver
    url: https://example.invalid
    schema_version: 1
optimization:
  method: dls
  max_iter: 20
  escape:
    max_cycles: 2
    escape_workers: 1
    distance_threshold: 0.1
    h_initial: 0.5
    w_initial: 0.4
chief:
  fields:
    - angle: 0
      direction: [0, 1]
  reference_surface: 8
  num_rays: 64
configs:
  - id: config1
    name: Config1
    weight: 1
    active: true
    surfaces:
      - id: 1
        curvature: 0.02
        thickness: 5
        material:
          key: N-BK7
      - id: 2
        thickness: 100
`

// streamTestBuild materialises a minimum the way the run closures do: the entry
// index is the list position, the --save file name is keyed on the store index.
// DiffractionOrder is 1 (the value an unset field parses back to), so the entry
// survives the marshal/parse cycle unchanged.
func streamTestBuild(position, storeIdx int, p escape.Point) types.EscapeMinimum {
	return types.EscapeMinimum{
		Index: position,
		Merit: p.Merit,
		File:  fmt.Sprintf("min%d.yaml", storeIdx),
		// variables has no omitempty, so nil would round-trip to an empty
		// slice; start from the shape the document parses back to.
		Variables: []types.EscapeVarState{},
		Surfaces:  []types.Surface{{ID: 1, Curvature: p.X[0], Thickness: 5, DiffractionOrder: 1}},
	}
}

// roundTripInput returns the input as it survives one marshal/parse cycle, the
// reference against which a stitched document is compared: yaml renders an
// unset slice as `[]`, which parses back as an empty non-nil slice, so that
// difference alone must not be blamed on the stream.
func roundTripInput(t *testing.T, in types.Input) types.Input {
	t.Helper()
	b, err := yaml.Marshal(types.Output{Input: in})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	return parseYAML[types.Output](b).Input
}

// boundaryWriter records the length of the stream after every Write, i.e. the
// document boundaries a reader can observe.
type boundaryWriter struct {
	buf   bytes.Buffer
	marks []int
}

func (w *boundaryWriter) Write(p []byte) (int, error) {
	n, err := w.buf.Write(p)
	w.marks = append(w.marks, w.buf.Len())
	return n, err
}

// TestEscapeStreamDocument drives the whole append-only path — header, one
// record per discovered solution, one replacement, completion — and checks the
// result against the document the legacy one-shot write would have produced:
// same input sections, same params, and the same minima once the improved
// entries are applied. It also checks the mid-run property the feature exists
// for: every write boundary is a parseable YAML document with no `---`
// separator, so a run killed between two writes leaves a readable file.
func TestEscapeStreamDocument(t *testing.T) {
	in := parseYAML[types.Output]([]byte(streamTestInput)).Input
	if in.Optimization == nil || in.Optimization.Escape == nil {
		t.Fatal("test input needs an optimization.escape section")
	}
	cfg := *in.Optimization.Escape
	params := escape.ReportParams(cfg, nil)

	var w boundaryWriter
	st := newEscapeStream(&w, escapeStreamTails{})
	st.header(in, params)

	// Store index 0 is discovered first (merit 5) and becomes list position 0.
	st.record(0, escape.Point{X: []float64{0.02}, Merit: 5.0}, true, streamTestBuild)
	// Store index 1 is an infeasible basin: never streamed, so store indices
	// and list positions diverge from here on.
	st.record(1, escape.Point{X: []float64{0.03}, Merit: 0.1, Status: escape.MinStatusInfeasibleBasin}, true, streamTestBuild)
	// Store index 2 is discovered second (merit 1) and becomes position 1.
	st.record(2, escape.Point{X: []float64{0.04}, Merit: 1.0}, true, streamTestBuild)
	// Both points are later improved (Store.Replace). The list is append-only,
	// so nothing is rewritten: the final values arrive with the completion.
	st.record(0, escape.Point{X: []float64{0.021}, Merit: 0.5}, false, streamTestBuild)
	st.record(2, escape.Point{X: []float64{0.041}, Merit: 0.9}, false, streamTestBuild)
	if err := st.Err(); err != nil {
		t.Fatalf("stream write failed: %v", err)
	}
	if len(w.marks) != 3 {
		t.Fatalf("expected header + 2 records, got %d writes", len(w.marks))
	}

	res := escape.Result{
		Params:     escape.BuildParams(cfg, nil),
		MaxSeconds: cfg.MaxSeconds,
		Workers:    escape.NumWorkers(cfg),
		Cycles:     escape.MaxCycles(cfg),
		Minima: []escape.Point{
			{X: []float64{0.021}, Merit: 0.5},
			{X: []float64{0.041}, Merit: 0.9},
		},
		MinimaIdx: []int{0, 2},
		BestIdx:   0,
		BestMerit: 0.5,
	}
	basins := []types.EscapeMinimum{streamTestBuild(0, 1, escape.Point{X: []float64{0.03}, Merit: 0.1, Status: escape.MinStatusInfeasibleBasin})}
	st.complete(streamCompletion{
		BestIndex:          bestStreamIndex(res, st),
		BestMerit:          res.BestMerit,
		InfeasibleBasins:   basins,
		MinimaImprovements: st.improvements(res, streamTestBuild),
	}, streamTailKeys(in, st))
	if err := st.Err(); err != nil {
		t.Fatalf("completion write failed: %v", err)
	}
	full := w.marks[len(w.marks)-1]
	if full != w.buf.Len() {
		t.Fatalf("last write boundary %d != document length %d", full, w.buf.Len())
	}

	// Every boundary — including each mid-run prefix — is one YAML document.
	if bytes.Contains(w.buf.Bytes(), []byte("\n---")) {
		t.Error("streamed document contains a document separator")
	}
	for _, mark := range w.marks[:len(w.marks)-1] {
		var partial types.Output
		if err := yaml.Unmarshal(w.buf.Bytes()[:mark], &partial); err != nil {
			t.Errorf("prefix of %d bytes does not parse: %v", mark, err)
		}
	}

	got := parseYAML[types.Output](w.buf.Bytes())
	if got.EscapeResult == nil {
		t.Fatal("streamed document has no escape_result section")
	}

	// The stitched document round-trips to the input: the deferred keys came
	// back at completion and no key was emitted twice (a duplicate would have
	// failed the parse above). The reference is the input through one ordinary
	// marshal/parse cycle, because yaml renders an unset slice as `[]` and the
	// parse turns it back into an empty non-nil slice.
	if want := roundTripInput(t, in); !reflect.DeepEqual(got.Input, want) {
		t.Errorf("streamed input differs from the source document:\n got  %+v\n want %+v", got.Input, want)
	}

	// Params come from the same path the completed document uses.
	wantResult := assembleEscapeResult(res, []types.EscapeMinimum{
		resMin(res, 0), resMin(res, 1),
	}, basins)
	if !reflect.DeepEqual(got.EscapeResult.Params, wantResult.Params) {
		t.Errorf("params = %+v, want %+v", got.EscapeResult.Params, wantResult.Params)
	}
	if got.EscapeResult.BestMerit != res.BestMerit {
		t.Errorf("best_merit = %v, want %v", got.EscapeResult.BestMerit, res.BestMerit)
	}
	if got.EscapeResult.TimedOut != wantResult.TimedOut || got.EscapeResult.Interrupted != wantResult.Interrupted {
		t.Errorf("run outcome = (timed_out %v, interrupted %v), want (%v, %v)",
			got.EscapeResult.TimedOut, got.EscapeResult.Interrupted, wantResult.TimedOut, wantResult.Interrupted)
	}
	if !reflect.DeepEqual(got.EscapeResult.InfeasibleBasins, basins) {
		t.Errorf("infeasible basins = %+v, want %+v", got.EscapeResult.InfeasibleBasins, basins)
	}

	// best_index addresses the streamed (discovery-ordered) list, and the
	// improved entry it names carries best_merit — the invariant `escape
	// extract --index $(best_index)` relies on.
	effective := effectiveEscapeMinima(got.EscapeResult)
	if got.EscapeResult.BestIndex < 0 || got.EscapeResult.BestIndex >= len(effective) {
		t.Fatalf("best_index = %d out of range (minima %d)", got.EscapeResult.BestIndex, len(effective))
	}
	if effective[got.EscapeResult.BestIndex].Merit != res.BestMerit {
		t.Errorf("minima[best_index].merit = %v, want best_merit %v",
			effective[got.EscapeResult.BestIndex].Merit, res.BestMerit)
	}

	// With the improved entries applied, the minima are exactly what the
	// one-shot document reports: first-discovery values are superseded.
	if !reflect.DeepEqual(effective, wantResult.Minima) {
		t.Errorf("effective minima = %+v, want %+v", effective, wantResult.Minima)
	}
	// ...while the raw list still holds what was streamed first (append-only).
	if got.EscapeResult.Minima[0].Merit != 5.0 || got.EscapeResult.Minima[1].Merit != 1.0 {
		t.Errorf("streamed minima = %v, %v; want the first-discovery values 5, 1",
			got.EscapeResult.Minima[0].Merit, got.EscapeResult.Minima[1].Merit)
	}
}

// TestEscapeStreamImprovementsAbsentWhenNothingWasReplaced checks that a run
// with no Store.Replace writes no minima_improvements element at all, and that
// best_index still addresses the streamed list.
func TestEscapeStreamImprovementsAbsentWhenNothingWasReplaced(t *testing.T) {
	in := parseYAML[types.Output]([]byte(streamTestInput)).Input
	params := escape.ReportParams(*in.Optimization.Escape, nil)

	var buf bytes.Buffer
	st := newEscapeStream(&buf, escapeStreamTails{})
	st.header(in, params)
	st.record(0, escape.Point{X: []float64{0.02}, Merit: 7.5}, true, streamTestBuild)

	res := escape.Result{
		Minima:    []escape.Point{{X: []float64{0.02}, Merit: 7.5}},
		MinimaIdx: []int{0},
		BestIdx:   0,
		BestMerit: 7.5,
	}
	st.complete(streamCompletion{
		BestIndex:          bestStreamIndex(res, st),
		BestMerit:          res.BestMerit,
		MinimaImprovements: st.improvements(res, streamTestBuild),
	}, streamTailKeys(in, st))
	if err := st.Err(); err != nil {
		t.Fatalf("stream write failed: %v", err)
	}
	if bytes.Contains(buf.Bytes(), []byte("minima_improvements")) {
		t.Error("minima_improvements written although nothing was replaced")
	}
	if bytes.Contains(buf.Bytes(), []byte("timed_out")) || bytes.Contains(buf.Bytes(), []byte("interrupted")) {
		t.Error("false run-outcome flags must be omitted")
	}
	got := parseYAML[types.Output](buf.Bytes())
	if got.EscapeResult == nil || len(got.EscapeResult.Minima) != 1 {
		t.Fatalf("escape_result = %+v, want one minimum", got.EscapeResult)
	}
	if !reflect.DeepEqual(effectiveEscapeMinima(got.EscapeResult), got.EscapeResult.Minima) {
		t.Error("effectiveEscapeMinima must not alter a document without improvements")
	}
}

// TestEscapeStreamChiefTail checks that a pupil_model_* variable defers the
// chief section to the completion write, so the tail carries the pupil model
// the run optimised instead of the header's stale copy.
func TestEscapeStreamChiefTail(t *testing.T) {
	in := parseYAML[types.Output]([]byte(streamTestInput)).Input
	optimised := parseYAML[types.Output]([]byte(strings.Replace(streamTestInput,
		"  reference_surface: 8",
		"  reference_surface: 8\n  pupil_model:\n    mode: virtual_entrance_pupil\n    axial_position: 6\n    diameter: 10",
		1))).Input
	params := escape.ReportParams(*in.Optimization.Escape, nil)

	var buf bytes.Buffer
	st := newEscapeStream(&buf, escapeStreamTails{chief: true})
	st.header(in, params)
	st.complete(streamCompletion{BestIndex: -1}, streamTailKeys(optimised, st))
	if err := st.Err(); err != nil {
		t.Fatalf("stream write failed: %v", err)
	}

	if strings.Count(string(buf.Bytes()), "chief:") != 1 {
		t.Error("chief section must be written exactly once")
	}
	if strings.Count(string(buf.Bytes()), "configs:") != 1 {
		t.Error("configs section must be written exactly once")
	}
	got := parseYAML[types.Output](buf.Bytes())
	if !reflect.DeepEqual(got.Chief, roundTripInput(t, optimised).Chief) {
		t.Errorf("tail chief = %+v, want the optimised %+v", got.Chief, optimised.Chief)
	}
	if !reflect.DeepEqual(got.Configs, roundTripInput(t, optimised).Configs) {
		t.Errorf("tail configs = %+v, want %+v", got.Configs, optimised.Configs)
	}
}

// TestBestStreamIndex checks that best_index is the position inside the
// streamed minima list, never the store index (infeasible points consume store
// indices without ever being streamed).
func TestBestStreamIndex(t *testing.T) {
	in := parseYAML[types.Output]([]byte(streamTestInput)).Input
	var buf bytes.Buffer
	st := newEscapeStream(&buf, escapeStreamTails{})
	st.header(in, escape.ReportParams(*in.Optimization.Escape, nil))

	// No minimum found yet: nothing to point at.
	if got := bestStreamIndex(escape.Result{MinimaIdx: []int{0}, BestIdx: 0}, st); got != -1 {
		t.Errorf("empty stream: bestStreamIndex = %d, want -1", got)
	}

	// The first streamed point sits at store index 5 (indices 0..4 belong to
	// points that were never recorded), so its list position is 0.
	st.record(5, escape.Point{X: []float64{0.02}, Merit: 1.0}, true, streamTestBuild)
	res := escape.Result{MinimaIdx: []int{5}, BestIdx: 0}
	if got := bestStreamIndex(res, st); got != 0 {
		t.Errorf("bestStreamIndex = %d, want the list position 0 (not the store index 5)", got)
	}
	// A best point that was never streamed (an infeasible basin) has no
	// position.
	res = escape.Result{MinimaIdx: []int{404}, BestIdx: 0}
	if got := bestStreamIndex(res, st); got != -1 {
		t.Errorf("bestStreamIndex for an unstreamed point = %d, want -1", got)
	}
	// Out-of-range BestIdx (an empty result) must not panic.
	if got := bestStreamIndex(escape.Result{MinimaIdx: nil, BestIdx: 0}, st); got != -1 {
		t.Errorf("bestStreamIndex with no minima = %d, want -1", got)
	}
}

// TestEffectiveEscapeMinimaAndListOrder covers the reader side: `list escape`
// substitutes the improved entries by position, then ranks by effective merit
// while keeping each row's document index (the one `escape extract --index`
// takes). A one-shot document, already merit-ordered and without
// improvements, renders unchanged.
func TestEffectiveEscapeMinimaAndListOrder(t *testing.T) {
	minimum := func(index int, merit float64) types.EscapeMinimum {
		return types.EscapeMinimum{
			Index:    index,
			Merit:    merit,
			Surfaces: []types.Surface{{ID: 1, Curvature: 0.01}},
			Features: []types.ConfigFeatures{{ID: "config1", ElementPowers: []float64{1, -1}}},
		}
	}
	// Discovery order, with positions 1 and 2 improved by the run.
	streamed := types.Output{EscapeResult: &types.EscapeResult{
		BestIndex: 1,
		BestMerit: 0.05,
		Minima:    []types.EscapeMinimum{minimum(0, 0.8), minimum(1, 0.4), minimum(2, 0.5)},
		MinimaImprovements: []types.EscapeMinimum{
			minimum(1, 0.05),
			minimum(2, 0.1),
		},
	}}
	effective := effectiveEscapeMinima(streamed.EscapeResult)
	wantEffective := []types.EscapeMinimum{minimum(0, 0.8), minimum(1, 0.05), minimum(2, 0.1)}
	if !reflect.DeepEqual(effective, wantEffective) {
		t.Errorf("effectiveEscapeMinima = %+v, want %+v", effective, wantEffective)
	}

	rows := escapeDataFromOutput(streamed).Minima
	// fileBase renders an unset --save file as the table's "-" placeholder.
	wantRows := []EscapeMinimumRow{
		{Index: 1, Merit: 0.05, File: "-", ElementPowers: [][]float64{{1, -1}}},
		{Index: 2, Merit: 0.1, File: "-", ElementPowers: [][]float64{{1, -1}}},
		{Index: 0, Merit: 0.8, File: "-", ElementPowers: [][]float64{{1, -1}}},
	}
	if !reflect.DeepEqual(rows, wantRows) {
		t.Errorf("list rows = %+v, want %+v", rows, wantRows)
	}
	// best_index addresses the document list, whose improved entry carries
	// best_merit: the extract index the demo script uses stays valid.
	if effective[streamed.EscapeResult.BestIndex].Merit != streamed.EscapeResult.BestMerit {
		t.Errorf("minima[best_index].merit = %v, want best_merit %v",
			effective[streamed.EscapeResult.BestIndex].Merit, streamed.EscapeResult.BestMerit)
	}

	// One-shot document: already merit-ordered, no improvements — unchanged.
	oneShot := types.Output{EscapeResult: &types.EscapeResult{
		BestIndex: 0,
		BestMerit: 0.05,
		Minima:    []types.EscapeMinimum{minimum(0, 0.05), minimum(1, 0.1), minimum(2, 0.8)},
	}}
	rows = escapeDataFromOutput(oneShot).Minima
	if !reflect.DeepEqual(rows, []EscapeMinimumRow{
		{Index: 0, Merit: 0.05, File: "-", ElementPowers: [][]float64{{1, -1}}},
		{Index: 1, Merit: 0.1, File: "-", ElementPowers: [][]float64{{1, -1}}},
		{Index: 2, Merit: 0.8, File: "-", ElementPowers: [][]float64{{1, -1}}},
	}) {
		t.Errorf("one-shot rows changed by the sort: %+v", rows)
	}
}

// TestEscapeStreamEnabled checks the CLI/YAML/default precedence the three
// principles require: the flag wins when given, then optimization.escape.stream,
// then the built-in default (stream on). Only the flag is written back.
func TestEscapeStreamEnabled(t *testing.T) {
	off := false
	on := true
	unset := func() { optEscapeStream, optEscapeStreamSet = "", false }
	unset()
	t.Cleanup(unset)

	// No flag, no YAML value: streaming on, nothing to write back.
	if enabled, fromFlag := escapeStreamEnabled(nil, "escape"); !enabled || fromFlag {
		t.Errorf("default = (%v, %v), want (true, false)", enabled, fromFlag)
	}
	// No flag, YAML off: honoured, and still nothing to write back.
	if enabled, fromFlag := escapeStreamEnabled(&types.EscapeConfig{Stream: &off}, "escape"); enabled || fromFlag {
		t.Errorf("YAML off = (%v, %v), want (false, false)", enabled, fromFlag)
	}
	// Flag on overrides the YAML off value and is written back.
	optEscapeStream, optEscapeStreamSet = "true", true
	if enabled, fromFlag := escapeStreamEnabled(&types.EscapeConfig{Stream: &off}, "escape"); !enabled || !fromFlag {
		t.Errorf("flag on = (%v, %v), want (true, true)", enabled, fromFlag)
	}
	// Flag off overrides the YAML on value and is written back.
	optEscapeStream, optEscapeStreamSet = "false", true
	if enabled, fromFlag := escapeStreamEnabled(&types.EscapeConfig{Stream: &on}, "escape"); enabled || !fromFlag {
		t.Errorf("flag off = (%v, %v), want (false, true)", enabled, fromFlag)
	}
	// PSO reads its own flag pair, not the escape one.
	optEscapeStream, optEscapeStreamSet = "true", true
	optPSOStream, optPSOStreamSet = "false", true
	t.Cleanup(func() { optPSOStream, optPSOStreamSet = "", false })
	if enabled, fromFlag := escapeStreamEnabled(nil, "pso"); enabled || !fromFlag {
		t.Errorf("pso flag off = (%v, %v), want (false, true)", enabled, fromFlag)
	}
	// An unset escape flag does not leak into a pso run.
	optEscapeStream, optEscapeStreamSet = "", false
	if enabled, fromFlag := escapeStreamEnabled(nil, "pso"); enabled || !fromFlag {
		t.Errorf("pso = (%v, %v), want (false, true)", enabled, fromFlag)
	}
}

// TestMarshalIndentRoundTrip checks the fragment stitch: a value marshalled at
// document level and shifted by n spaces must parse back to the same value once
// it sits at column n of an enclosing block, empty lines included.
func TestMarshalIndentRoundTrip(t *testing.T) {
	type wrapper struct {
		Minima []types.EscapeMinimum `yaml:"minima"`
	}
	item := types.EscapeMinimum{
		Index:     3,
		Merit:     1.25,
		Status:    "infeasible_basin",
		Surfaces:  []types.Surface{{ID: 1, Curvature: 0.02, Thickness: 5, DiffractionOrder: 1}},
		Variables: []types.EscapeVarState{{Name: "v", Param: "curvature", After: 0.5}},
		Features:  []types.ConfigFeatures{{ID: "config1", ElementPowers: []float64{1, -2}}},
	}
	fragment, err := marshalIndent([]types.EscapeMinimum{item}, 4)
	if err != nil {
		t.Fatal(err)
	}
	var got wrapper
	if err := yaml.Unmarshal(append([]byte("minima:\n"), fragment...), &got); err != nil {
		t.Fatalf("shifted fragment does not parse: %v", err)
	}
	if !reflect.DeepEqual(got.Minima, []types.EscapeMinimum{item}) {
		t.Errorf("round trip = %+v, want %+v", got.Minima, item)
	}
	// n = 0 is the identity.
	plain, err := marshalIndent([]types.EscapeMinimum{item}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(plain) == 0 || plain[len(plain)-1] != '\n' {
		t.Errorf("unshifted fragment = %q, want a newline-terminated block", plain)
	}
}

// TestReportParamsMatchesAssembleEscapeResult pins the two params writers to
// the same inputs: the header's block is built before the run from the config
// and the model's variables, and must equal what the completed document
// reports for the values ParallelEscape derives from the same config.
func TestReportParamsMatchesAssembleEscapeResult(t *testing.T) {
	cfg := types.EscapeConfig{
		MaxCycles:         3,
		EscapeWorkers:     2,
		MaxSeconds:        90,
		DistanceThreshold: 0.15,
		HInitial:          0.5,
		WInitial:          0.4,
		HMult:             2,
		WMult:             1.3,
	}
	res := escape.Result{
		Params:     escape.BuildParams(cfg, nil),
		MaxSeconds: cfg.MaxSeconds,
		Workers:    escape.NumWorkers(cfg),
		Cycles:     escape.MaxCycles(cfg),
	}
	got := assembleEscapeResult(res, nil, nil).Params
	want := escape.ReportParams(cfg, nil)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("assembleEscapeResult params = %+v, want the header's %+v", got, want)
	}
}

// resMin builds the one-shot entry for res.Minima[i] the way the completed
// document does: position = index in the merit-sorted result list, file = the
// discovery (store) index.
func resMin(res escape.Result, i int) types.EscapeMinimum {
	return streamTestBuild(i, res.MinimaIdx[i], res.Minima[i])
}
