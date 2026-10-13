package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/hiroki/rayweaver/internal/types"
)

// This file implements the `list global` reader for an escape/pso JSONL run log
// (`escape --log FILE` / `pso --log FILE`, or the `--verbose` stream captured
// from stderr). The log is detected automatically: a run log is recognised when
// at least one non-empty line is a JSON object carrying one of the event names
// the escape search emits. Non-JSON lines are ignored, so a legacy capture that
// interleaved tagged plain-text warning lines with the compact stream still
// reads; a pipeline YAML document (and an `optimize --log` stream, which has no
// `event` key) never matches.
//
// The reconstruction is best-effort by construction: the pipeline document
// carries data the progress stream does not. Recoverable: the escape bump
// parameters, the run budget, the run aggregate, every discovered minimum's
// merit and classification, the `--save` file names and each worker's timing.
// Not recoverable: the per-minimum element powers (which need the surface data
// the log never contains), the tuning parameters that are not part of the
// `params` event (escape_iter_frac, w_span, stall_*, initial_perturb, the
// fingerprint threshold, the variable weights), and the worker completion flags
// (retired, timed_out, interrupted) and reason, which the compact `--verbose`
// stream drops as fields outside the fixed key order.

// globalLogEventNames are the event names emitted by internal/escape
// (including the resource guard and the cmd-level summary) and internal/pso.
// At least one line must carry one of them for an input to be read as a run log.
var globalLogEventNames = map[string]bool{
	"params":          true,
	"start":           true,
	"cycle":           true,
	"minimum":         true,
	"minimum_saved":   true,
	"worker_done":     true,
	"worker_retired":  true,
	"worker_retire":   true,
	"timeout":         true,
	"interrupted":     true,
	"interrupt":       true,
	"interrupt_dls":   true,
	"force_quit":      true,
	"done":            true,
	"global_complete": true,
	"error":           true,
	"resource":        true,
	"memory_limit":    true,
	"pso_init":        true,
	"pso_iter":        true,
	"pso_stall":       true,
	"pso_restart":     true,
}

// looksLikeGlobalJSONL reports whether data is an escape/pso JSONL run log.
// Non-JSON lines are ignored rather than disqualifying the whole input: a
// captured --verbose stream from a run whose warnings took the tagged
// plain-text path (or any other stray stderr line) is still a run log, and the
// reader skips those lines. A pipeline YAML document, an `optimize --log`
// stream and arbitrary JSONL carry no recognised event and never match.
func looksLikeGlobalJSONL(data []byte) bool {
	lines, recognized := 0, 0
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		lines++
		if !strings.HasPrefix(line, "{") || !strings.HasSuffix(line, "}") {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if ev, ok := rec["event"].(string); ok && globalLogEventNames[ev] {
			recognized++
		}
	}
	return lines > 0 && recognized > 0
}

// looksLikeJSONLObjects reports whether data is a JSON Lines stream — several
// non-empty lines, every one a JSON object — that carries no escape/pso event.
// `list` rejects such an input explicitly instead of silently falling through
// to YAML parsing (where a JSONL stream either errors or, worse, parses to an
// empty document and reports "no global result" with exit 0). A single-line
// JSON object is left alone: it is a valid YAML/JSON pipeline document, not a
// stream.
func looksLikeJSONLObjects(data []byte) bool {
	lines, objs := 0, 0
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		lines++
		if !strings.HasPrefix(line, "{") || !strings.HasSuffix(line, "}") {
			return false
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			return false
		}
		objs++
	}
	return lines > 1 && objs == lines
}

// parseJSONLForLog decodes a run log that may interleave non-JSON lines with
// the records (see looksLikeGlobalJSONL). Unlike parseJSONL it does not report
// each bad line — a legacy capture can hold tens of thousands of tagged
// plain-text warnings — and returns how many non-empty lines it skipped so the
// caller can warn once. The scanner buffer matches looksLikeGlobalJSONL's, so a
// very long event line is never silently dropped as a parse failure.
func parseJSONLForLog(data []byte) (records []any, skipped int) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var v any
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			skipped++
			continue
		}
		records = append(records, v)
	}
	return records, skipped
}

// globalLogMinimum is the aggregated record of one store entry, keyed in the
// log by its discovery index (the `--save` FILE<n> number), not by the
// merit-sorted rank used by global_result.minima[]. order is the first-seen
// order, the tiebreaker when two entries share a merit. reason and file come
// from the invalid_reason and (for a --save run) minimum_saved events.
type globalLogMinimum struct {
	merit    float64
	hasMerit bool
	status   string
	reason   string
	file     string
	order    int
}

// globalLogWorker is the aggregated lifecycle record of one escape worker.
type globalLogWorker struct {
	id          int
	maxCycle    int // highest cycle index seen (-1 when none)
	explored    int
	recorded    int
	best        float64
	hasBest     bool
	elapsed     float64
	reason      string
	retired     bool
	interrupted bool
	timedOut    bool
}

// runEventWarn is the structured warning event: every non-fatal message the
// optimizer and the glass package report through their Warnf sinks becomes one
// of these while a progress stream is active (see runEscapeCore).
const runEventWarn = "warn"

// globalLogRunEvents are the run-level events `list global` lists verbatim
// (with a one-line Detail) in its Log Events section, in the order the log
// recorded them: the three signal-handling stages, the resource guard's actions
// and samples, the run's error report and the structured warnings. The compact
// --verbose stream drops every field outside the fixed key order, so only the
// event name survives there (and `resource` samples carry nothing at all).
var globalLogRunEvents = map[string]bool{
	"interrupt":     true,
	"interrupt_dls": true,
	"force_quit":    true,
	"memory_limit":  true,
	"worker_retire": true,
	"resource":      true,
	"error":         true,
	runEventWarn:    true,
}

// elapsedSeconds reads an event's run-relative time. A full `--log` stream
// carries it numerically under `elapsed`; the compact stream only has the
// `HH:MM` form under `e`, which is converted to whole seconds. Nil when the
// field is missing or malformed.
func elapsedSeconds(rec map[string]any) *float64 {
	if v, ok := asNum(rec["elapsed"]); ok {
		return &v
	}
	hhmmss, ok := rec["e"].(string)
	if !ok {
		return nil
	}
	parts := strings.Split(hhmmss, ":")
	if len(parts) < 2 {
		return nil
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil {
		return nil
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil
	}
	sec := float64(h*3600 + m*60)
	return &sec
}

// escapeEventDetail renders the payload of a run-level event as one short cell:
// the OS signal for the interrupt stages, the memory-limit move, the retired
// worker, the sampled resource state, or the error message. It returns "" when
// the event carries no such field (e.g. every resource field in a compact
// stream, or the payload-less `force_quit`).
func escapeEventDetail(name string, rec map[string]any) string {
	num := func(key string) (float64, bool) { return asNum(rec[key]) }
	str := func(key string) string { s, _ := rec[key].(string); return s }

	switch name {
	case "interrupt", "interrupt_dls":
		return str("signal")
	case "memory_limit":
		parts := []string{str("kind")}
		if r := str("reason"); r != "" {
			parts = append(parts, r)
		}
		before, hasBefore := num("before_mb")
		after, hasAfter := num("after_mb")
		if hasBefore || hasAfter {
			parts = append(parts, fmt.Sprintf("%.0f→%.0f MB", before, after))
		}
		return strings.Join(parts, " ")
	case "worker_retire":
		parts := []string{}
		if w, ok := num("worker"); ok {
			parts = append(parts, fmt.Sprintf("worker %.0f", w))
		}
		if r := str("reason"); r != "" {
			parts = append(parts, "reason="+r)
		}
		if c, ok := num("cycle"); ok {
			parts = append(parts, fmt.Sprintf("cycle=%.0f", c))
		}
		return strings.Join(parts, " ")
	case "resource":
		parts := []string{}
		if v, ok := num("heap_alloc_mb"); ok {
			parts = append(parts, fmt.Sprintf("heap=%.1f MB", v))
		}
		if v, ok := num("pressure_level"); ok {
			parts = append(parts, fmt.Sprintf("pressure=%.0f", v))
		}
		if v, ok := num("compaction_rate_per_s"); ok {
			parts = append(parts, fmt.Sprintf("compaction=%.2f/s", v))
		}
		if n := str("note"); n != "" {
			parts = append(parts, n)
		}
		return strings.Join(parts, " ")
	case "error":
		return str("message")
	case runEventWarn:
		return str("message")
	}
	return ""
}

// escapeLogFoldEvents are the run-level events whose repeated occurrences are
// folded into a single Log Events row (see foldRunEvent). Only `warn` qualifies:
// a recurring diagnostic would otherwise contribute one table row per
// occurrence — tens of thousands on a long run — while the sampled/resource and
// signal events are rare and each carries its own state.
var escapeLogFoldEvents = map[string]bool{runEventWarn: true}

// foldRunEvent appends one run-level event row, folding a repeated warning into
// the row already recorded for the same message anywhere in the log, not only
// into the row just before it. An 8-worker run emits the same diagnostic in
// interleaved bursts (and on every merit evaluation), so consecutive-only
// folding left tens of thousands of rows; keying by the normalised message
// bounds the section to one row per distinct warning. folded maps a normalised
// warn message to its row's index in events, so the row keeps the position and
// elapsed time of the first occurrence while Count accumulates every copy.
// Lifecycle events (interrupt, resource, error, ...) never fold: each carries
// its own state.
func foldRunEvent(events []GlobalEventRow, row GlobalEventRow, folded map[string]int) []GlobalEventRow {
	if !escapeLogFoldEvents[row.Event] {
		return append(events, row)
	}
	key := row.Event + "\x00" + runEventKey(row.Detail)
	if i, ok := folded[key]; ok {
		events[i].Count++
		return events
	}
	row.Count = 1
	folded[key] = len(events)
	return append(events, row)
}

// backFocusWarnEvent reports whether a structured warning is the per-evaluation
// back-focus diagnostic (`back_focus: …`) the spot/wavefront hard solve emits
// for every dropped field set, on every merit evaluation. On a long run its few
// dozen distinct messages still span tens of thousands of rows — it describes a
// merit-evaluation artifact, not a run-level event — so `list global` omits it.
// The run-level totals live in opt_results.back_focus, and the raw --log /
// --verbose stream keeps every message. Other warnings (a glass-file load
// failure, an unknown solve type, an unsettled power_solve) are rare and stay.
func backFocusWarnEvent(detail string) bool {
	return strings.HasPrefix(detail, "back_focus:")
}

// runEventKey normalises a run-level event detail for the fold comparison. A
// throttled warning appends its sequence as a trailing " (event N)", which is
// what differs between copies of one otherwise identical message; dropping that
// suffix lets them compare equal. Any other detail — or
// one without a numeric sequence in the expected shape — is returned unchanged.
func runEventKey(detail string) string {
	const prefix = " (event "
	i := strings.LastIndex(detail, prefix)
	if i < 0 || !strings.HasSuffix(detail, ")") {
		return detail
	}
	seq := detail[i+len(prefix) : len(detail)-1]
	if seq == "" {
		return detail
	}
	for _, c := range seq {
		if c < '0' || c > '9' {
			return detail
		}
	}
	return detail[:i]
}

// runListGlobalLog renders `list global` for an escape/pso JSONL run log. The
// log holds no system definition, so only the `global` target can be served; a
// bare `list < run.jsonl` therefore lists the global run.
func runListGlobalLog(data []byte, positional []string, format string) {
	if err := globalLogTargetError(positional); err != nil {
		errOut("Error: %v", err)
		os.Exit(1)
	}
	renderGlobalList(buildGlobalListDataFromLog(data), format)
}

// globalLogTargetError validates the requested targets against a run log. An
// empty list defaults to the global target.
func globalLogTargetError(positional []string) error {
	if len(positional) == 0 {
		return nil
	}
	for _, t := range positional {
		if t != "global" {
			return fmt.Errorf("an escape/pso JSONL run log carries no system definition; only the \"global\" target can be listed from it (got %q)", t)
		}
	}
	return nil
}

// buildGlobalListDataFromLog reconstructs the `list global` view from a run
// log. Events are folded in file order, so the last value of a repeated key
// wins: a minimum's merit is the best one recorded for its discovery index, and
// a worker's counts are the ones from its completion event.
func buildGlobalListDataFromLog(data []byte) globalListData {
	events, skipped := parseJSONLForLog(data)
	if skipped > 0 {
		errOut("Warning: skipped %d non-JSON line(s) while reading the run log", skipped)
	}
	d := globalListData{Found: len(events) > 0, FromLog: true}
	if !d.Found {
		return d
	}

	var params types.GlobalParamsInfo
	var run globalRunInfo
	var haveRun bool
	var completeBest float64
	var haveCompleteBest bool
	var completeMinima []GlobalMinimumRow
	var completeInfeasible []GlobalMinimumRow

	minima := map[int]*globalLogMinimum{}
	// savedFiles maps a store (discovery) index to the --save file last written
	// for it. The saver reports it per record, so an improved minimum replaces
	// its earlier name; the mapping is applied after the scan because the
	// minimum_saved event precedes the matching minimum event.
	savedFiles := map[int]string{}
	workers := map[int]*globalLogWorker{}
	foldedWarns := map[string]int{}
	var minOrder int

	workerFor := func(id int) *globalLogWorker {
		w, ok := workers[id]
		if !ok {
			w = &globalLogWorker{id: id, maxCycle: -1}
			workers[id] = w
		}
		return w
	}
	noteCycle := func(w *globalLogWorker, v any) {
		c, ok := asNum(v)
		if !ok {
			return
		}
		if n := int(c); n > w.maxCycle {
			w.maxCycle = n
		}
	}

	for _, e := range events {
		rec, ok := e.(map[string]any)
		if !ok {
			continue
		}
		elapsed, _ := asNum(rec["elapsed"])
		name, _ := rec["event"].(string)
		// Every run-level event is listed in log order, including the ones
		// whose payload also drives the summary below. The recurring
		// back-focus diagnostics are dropped (see backFocusWarnEvent).
		if globalLogRunEvents[name] {
			detail := escapeEventDetail(name, rec)
			if name != runEventWarn || !backFocusWarnEvent(detail) {
				d.Events = foldRunEvent(d.Events, GlobalEventRow{
					Event:   name,
					Detail:  detail,
					Elapsed: elapsedSeconds(rec),
				}, foldedWarns)
			}
		}
		switch rec["event"] {
		case "params":
			params.HInitial, _ = asNum(rec["h"])
			params.WInitial, _ = asNum(rec["w"])
			params.HMult, _ = asNum(rec["h_mult"])
			params.WMult, _ = asNum(rec["w_mult"])
			params.DistanceThreshold, _ = asNum(rec["distance_threshold"])

		case "start":
			if v, ok := asNum(rec["workers"]); ok {
				params.GlobalWorkers = int(v)
			}
			if v, ok := asNum(rec["max_cycles"]); ok {
				params.MaxCycles = int(v)
			}
			if v, ok := asNum(rec["max_seconds"]); ok {
				params.MaxSeconds = v
			}

		case "done", "global_complete":
			haveRun = true
			if v, ok := asNum(rec["workers"]); ok && int(v) > 0 {
				run.Workers = int(v)
			}
			if v, ok := asNum(rec["cycles"]); ok && int(v) > 0 {
				run.Cycles = int(v)
			}
			if v, ok := asNum(rec["explorations"]); ok && int(v) > 0 {
				run.Explorations = int(v)
			}
			if v, ok := asNum(rec["minima_count"]); ok && int(v) > 0 {
				run.Minima = int(v)
			} else if v, ok := asNum(rec["minima"]); ok && int(v) > 0 {
				// The `done` event reports the count under `minima`.
				run.Minima = int(v)
			}
			if v, ok := asNum(rec["best_merit"]); ok {
				completeBest, haveCompleteBest = v, true
			}
			if b, ok := rec["timed_out"].(bool); ok && b {
				d.TimedOut = true
			}
			if b, ok := rec["interrupted"].(bool); ok && b {
				d.Interrupted = true
			}
			run.Elapsed = elapsed
			// global_complete repeats the merit-sorted minima; they are the
			// fallback when the `minimum` events are absent (a log captured
			// without them carries no classification).
			if arr, ok := rec["minima"].([]any); ok {
				for i, item := range arr {
					m, ok := item.(map[string]any)
					if !ok {
						continue
					}
					merit, _ := asNum(m["merit"])
					completeMinima = append(completeMinima, GlobalMinimumRow{Index: i, Merit: merit})
				}
			}
			if arr, ok := rec["infeasible"].([]any); ok {
				for i, item := range arr {
					m, ok := item.(map[string]any)
					if !ok {
						continue
					}
					merit, _ := asNum(m["merit"])
					reason, _ := m["reason"].(string)
					completeInfeasible = append(completeInfeasible, GlobalMinimumRow{
						Index:  i,
						Merit:  merit,
						Status: string(types.StatusInfeasibleBasin),
						Reason: reason,
					})
				}
			}

		case "minimum_saved":
			// The saver reports the exact file it just wrote, keyed by the
			// store index the minimum events use too.
			idx, ok := asNum(rec["index"])
			file, hasFile := rec["file"].(string)
			if ok && hasFile && file != "" {
				savedFiles[int(idx)] = file
			}

		case "minimum":
			idx, _ := asNum(rec["index"])
			merit, _ := asNum(rec["merit"])
			status, _ := rec["min_status"].(string)
			reason, _ := rec["invalid_reason"].(string)
			key := int(idx)
			m, ok := minima[key]
			if !ok {
				m = &globalLogMinimum{order: minOrder}
				minOrder++
				minima[key] = m
			}
			switch {
			case !m.hasMerit || merit < m.merit:
				m.merit, m.hasMerit, m.status, m.reason = merit, true, status, reason
			case m.status == "":
				m.status, m.reason = status, reason
			}

		case "cycle":
			if id, ok := asNum(rec["worker"]); ok {
				noteCycle(workerFor(int(id)), rec["cycle"])
			}

		case "worker_done":
			id, _ := asNum(rec["worker"])
			w := workerFor(int(id))
			if v, ok := asNum(rec["explored"]); ok {
				w.explored = int(v)
			}
			if v, ok := asNum(rec["recorded"]); ok {
				w.recorded = int(v)
			}
			if b, ok := rec["retired"].(bool); ok && b {
				w.retired = true
			}
			if b, ok := rec["interrupted"].(bool); ok && b {
				w.interrupted = true
			}
			if b, ok := rec["timed_out"].(bool); ok && b {
				w.timedOut = true
			}
			w.elapsed = elapsed

		case "worker_retired":
			id, _ := asNum(rec["worker"])
			w := workerFor(int(id))
			w.retired = true
			if v, ok := asNum(rec["explored"]); ok {
				w.explored = int(v)
			}
			if v, ok := asNum(rec["recorded"]); ok {
				w.recorded = int(v)
			}
			if v, ok := asNum(rec["best_merit"]); ok {
				w.best, w.hasBest = v, true
			}
			if s, ok := rec["reason"].(string); ok && s != "" {
				w.reason = s
			}
			noteCycle(w, rec["cycle"])
			w.elapsed = elapsed

		case "timeout":
			d.TimedOut = true
			if id, ok := asNum(rec["worker"]); ok {
				w := workerFor(int(id))
				w.timedOut = true
				noteCycle(w, rec["cycle"])
			}

		case "interrupted":
			d.Interrupted = true
			if id, ok := asNum(rec["worker"]); ok {
				w := workerFor(int(id))
				w.interrupted = true
				noteCycle(w, rec["cycle"])
			}

		case "interrupt", "interrupt_dls", "force_quit":
			d.Interrupted = true
		}
	}

	// Split the discovered points the way the pipeline document does:
	// global_result.minima lists the feasible solutions, the infeasible basins
	// are a separate list. Both are re-ranked by merit; the --save file a
	// minimum_saved event reported travels with its point.
	var feasible, infeasible []*globalLogMinimum
	var files []string
	for key, m := range minima {
		if file := savedFiles[key]; file != "" {
			m.file = file
			files = append(files, file)
		}
		if types.GlobalMinimumStatus(m.status) == types.StatusInfeasibleBasin {
			infeasible = append(infeasible, m)
			continue
		}
		feasible = append(feasible, m)
	}
	byMerit := func(recs []*globalLogMinimum) {
		sort.Slice(recs, func(i, j int) bool {
			if recs[i].merit != recs[j].merit {
				return recs[i].merit < recs[j].merit
			}
			return recs[i].order < recs[j].order
		})
	}
	byMerit(feasible)
	byMerit(infeasible)

	minimaRows := make([]GlobalMinimumRow, 0, len(feasible))
	for i, m := range feasible {
		rows := GlobalMinimumRow{Index: i, Merit: m.merit, Status: m.status}
		if m.file != "" {
			rows.File = fileBase(m.file)
		}
		minimaRows = append(minimaRows, rows)
	}
	if len(minimaRows) == 0 {
		minimaRows = completeMinima
	}

	infeasibleRows := make([]GlobalMinimumRow, 0, len(infeasible))
	for i, m := range infeasible {
		row := GlobalMinimumRow{Index: i, Merit: m.merit, Status: m.status, Reason: m.reason}
		if m.file != "" {
			row.File = fileBase(m.file)
		}
		infeasibleRows = append(infeasibleRows, row)
	}
	if len(infeasibleRows) == 0 {
		infeasibleRows = completeInfeasible
	}

	// Workers: ascending worker id, so the table reads in launch order.
	ids := make([]int, 0, len(workers))
	for id := range workers {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	workerRows := make([]GlobalWorkerRow, 0, len(ids))
	for _, id := range ids {
		workerRows = append(workerRows, workerRow(workers[id]))
	}

	if len(minimaRows) > 0 {
		d.BestIndex = 0
		d.BestMerit = minimaRows[0].Merit
	} else if haveCompleteBest {
		d.BestMerit = completeBest
	}
	if run.Minima == 0 {
		run.Minima = len(minimaRows)
	}
	if run.Workers == 0 {
		run.Workers = len(workerRows)
	}
	if haveRun {
		d.Run = &run
	}
	d.Params = globalParamsSettings(&params)
	d.Minima = minimaRows
	d.InfeasibleBasins = infeasibleRows
	d.FileDir = commonFileDir(files)
	d.Workers = workerRows
	return d
}
