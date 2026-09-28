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

// This file implements the `list escape` reader for an escape/pso JSONL run log
// (`escape --log FILE` / `pso --log FILE`, or the `--verbose` stream captured
// from stderr). The log is detected automatically: a run log is recognised when
// every non-empty line is a JSON object and at least one carries one of the
// event names the escape search emits. A pipeline YAML document (and an
// `optimize --log` stream, which has no `event` key) never matches.
//
// The reconstruction is best-effort by construction: the pipeline document
// carries data the progress stream does not. Recoverable: the escape bump
// parameters, the run budget, the run aggregate, every discovered minimum's
// merit and classification, and each worker's completion state and timing. Not
// recoverable: the per-minimum `--save` file names and the element powers
// (both need the surface data, which the log never contains), and the tuning
// parameters that are not part of the `params` event (escape_iter_frac, w_span,
// stall_*, initial_perturb, the fingerprint threshold, the variable weights).
// The compact `--verbose` stream additionally drops the fields outside the fixed
// key order (min_status, retired, timed_out, interrupted, reason), so those are
// reconstructed only from a full `--log` file.

// escapeLogEventNames are the event names emitted by internal/escape
// (including the resource guard and the cmd-level summary) and internal/pso.
// At least one line must carry one of them for an input to be read as a run log.
var escapeLogEventNames = map[string]bool{
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
	"escape_complete": true,
	"error":           true,
	"resource":        true,
	"memory_limit":    true,
	"pso_init":        true,
	"pso_iter":        true,
	"pso_stall":       true,
	"pso_restart":     true,
}

// looksLikeEscapeJSONL reports whether data is an escape/pso JSONL run log.
func looksLikeEscapeJSONL(data []byte) bool {
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
			return false
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			return false
		}
		if ev, ok := rec["event"].(string); ok && escapeLogEventNames[ev] {
			recognized++
		}
	}
	return lines > 0 && recognized > 0
}

// escapeLogMinimum is the aggregated record of one store entry, keyed in the
// log by its discovery index (the `--save` FILE<n> number), not by the
// merit-sorted rank used by escape_result.minima[]. order is the first-seen
// order, the tiebreaker when two entries share a merit. reason and file come
// from the invalid_reason and (for a --save run) minimum_saved events.
type escapeLogMinimum struct {
	merit    float64
	hasMerit bool
	status   string
	reason   string
	file     string
	order    int
}

// escapeLogWorker is the aggregated lifecycle record of one escape worker.
type escapeLogWorker struct {
	id          int
	maxCycle    int // highest cycle index seen (-1 when none)
	escaped     int
	recorded    int
	best        float64
	hasBest     bool
	elapsed     float64
	reason      string
	retired     bool
	interrupted bool
	timedOut    bool
}

// escapeLogRunEvents are the run-level events `list escape` lists verbatim
// (with a one-line Detail) in its Log Events section, in the order the log
// recorded them: the three signal-handling stages, the resource guard's actions
// and samples, and the run's error report. The compact --verbose stream drops
// every field outside the fixed key order, so only the event name survives
// there (and `resource` samples carry nothing at all).
var escapeLogRunEvents = map[string]bool{
	"interrupt":     true,
	"interrupt_dls": true,
	"force_quit":    true,
	"memory_limit":  true,
	"worker_retire": true,
	"resource":      true,
	"error":         true,
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
	}
	return ""
}

// runListEscapeLog renders `list escape` for an escape/pso JSONL run log. The
// log holds no system definition, so only the `escape` target can be served; a
// bare `list < run.jsonl` therefore lists the escape run.
func runListEscapeLog(data []byte, positional []string, format string) {
	if err := escapeLogTargetError(positional); err != nil {
		errOut("Error: %v", err)
		os.Exit(1)
	}
	renderEscapeList(buildEscapeListDataFromLog(data), format)
}

// escapeLogTargetError validates the requested targets against a run log. An
// empty list defaults to the escape target.
func escapeLogTargetError(positional []string) error {
	if len(positional) == 0 {
		return nil
	}
	for _, t := range positional {
		if t != "escape" {
			return fmt.Errorf("an escape/pso JSONL run log carries no system definition; only the \"escape\" target can be listed from it (got %q)", t)
		}
	}
	return nil
}

// buildEscapeListDataFromLog reconstructs the `list escape` view from a run
// log. Events are folded in file order, so the last value of a repeated key
// wins: a minimum's merit is the best one recorded for its discovery index, and
// a worker's counts are the ones from its completion event.
func buildEscapeListDataFromLog(data []byte) escapeListData {
	events := parseJSONL(data)
	d := escapeListData{Found: len(events) > 0, FromLog: true}
	if !d.Found {
		return d
	}

	var params types.EscapeParamsInfo
	var run escapeRunInfo
	var haveRun bool
	var completeBest float64
	var haveCompleteBest bool
	var completeMinima []EscapeMinimumRow
	var completeInfeasible []EscapeMinimumRow

	minima := map[int]*escapeLogMinimum{}
	// savedFiles maps a store (discovery) index to the --save file last written
	// for it. The saver reports it per record, so an improved minimum replaces
	// its earlier name; the mapping is applied after the scan because the
	// minimum_saved event precedes the matching minimum event.
	savedFiles := map[int]string{}
	workers := map[int]*escapeLogWorker{}
	var minOrder int

	workerFor := func(id int) *escapeLogWorker {
		w, ok := workers[id]
		if !ok {
			w = &escapeLogWorker{id: id, maxCycle: -1}
			workers[id] = w
		}
		return w
	}
	noteCycle := func(w *escapeLogWorker, v any) {
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
		// whose payload also drives the summary below.
		if escapeLogRunEvents[name] {
			d.Events = append(d.Events, EscapeEventRow{
				Event:   name,
				Detail:  escapeEventDetail(name, rec),
				Elapsed: elapsedSeconds(rec),
			})
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
				params.EscapeWorkers = int(v)
			}
			if v, ok := asNum(rec["max_cycles"]); ok {
				params.MaxCycles = int(v)
			}
			if v, ok := asNum(rec["max_seconds"]); ok {
				params.MaxSeconds = v
			}

		case "done", "escape_complete":
			haveRun = true
			if v, ok := asNum(rec["workers"]); ok && int(v) > 0 {
				run.Workers = int(v)
			}
			if v, ok := asNum(rec["cycles"]); ok && int(v) > 0 {
				run.Cycles = int(v)
			}
			if v, ok := asNum(rec["escapes"]); ok && int(v) > 0 {
				run.Escapes = int(v)
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
			// escape_complete repeats the merit-sorted minima; they are the
			// fallback when the `minimum` events are absent (a log captured
			// without them carries no classification).
			if arr, ok := rec["minima"].([]any); ok {
				for i, item := range arr {
					m, ok := item.(map[string]any)
					if !ok {
						continue
					}
					merit, _ := asNum(m["merit"])
					completeMinima = append(completeMinima, EscapeMinimumRow{Index: i, Merit: merit})
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
					completeInfeasible = append(completeInfeasible, EscapeMinimumRow{
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
				m = &escapeLogMinimum{order: minOrder}
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
			if v, ok := asNum(rec["escaped"]); ok {
				w.escaped = int(v)
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
			if v, ok := asNum(rec["escaped"]); ok {
				w.escaped = int(v)
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
	// escape_result.minima lists the feasible solutions, the infeasible basins
	// are a separate list. Both are re-ranked by merit; the --save file a
	// minimum_saved event reported travels with its point.
	var feasible, infeasible []*escapeLogMinimum
	var files []string
	for key, m := range minima {
		if file := savedFiles[key]; file != "" {
			m.file = file
			files = append(files, file)
		}
		if types.EscapeMinimumStatus(m.status) == types.StatusInfeasibleBasin {
			infeasible = append(infeasible, m)
			continue
		}
		feasible = append(feasible, m)
	}
	byMerit := func(recs []*escapeLogMinimum) {
		sort.Slice(recs, func(i, j int) bool {
			if recs[i].merit != recs[j].merit {
				return recs[i].merit < recs[j].merit
			}
			return recs[i].order < recs[j].order
		})
	}
	byMerit(feasible)
	byMerit(infeasible)

	minimaRows := make([]EscapeMinimumRow, 0, len(feasible))
	for i, m := range feasible {
		rows := EscapeMinimumRow{Index: i, Merit: m.merit, Status: m.status}
		if m.file != "" {
			rows.File = fileBase(m.file)
		}
		minimaRows = append(minimaRows, rows)
	}
	if len(minimaRows) == 0 {
		minimaRows = completeMinima
	}

	infeasibleRows := make([]EscapeMinimumRow, 0, len(infeasible))
	for i, m := range infeasible {
		row := EscapeMinimumRow{Index: i, Merit: m.merit, Status: m.status, Reason: m.reason}
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
	workerRows := make([]EscapeWorkerRow, 0, len(ids))
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
	d.Params = escapeParamsSettings(&params)
	d.Minima = minimaRows
	d.InfeasibleBasins = infeasibleRows
	d.FileDir = commonFileDir(files)
	d.Workers = workerRows
	return d
}
