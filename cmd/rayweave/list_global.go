package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/hiroki/rayweaver/internal/types"
)

// GlobalEventRow is one run-level event of an escape/pso run log (only
// recovered from a run log, since a pipeline document does not record how the
// run progressed). Event is the JSONL event name; Detail summarises it — the
// OS signal for the `interrupt` / `interrupt_dls` stages, the memory-limit
// action for `memory_limit`, the retired worker for `worker_retire`, the
// measured resource state for `resource`, the message for `error` and for a
// `warn`; Elapsed is the seconds from run start (of the first occurrence when
// rows were folded). Count is how many log rows this one row summarises: it is
// set for a `warn`, which folds by message across the whole log (the recurring
// per-evaluation `back_focus:` diagnostics are dropped entirely, see
// backFocusWarnEvent), and left 0, hence omitted, for the rest, which are
// always single occurrences.
type GlobalEventRow struct {
	Event   string   `json:"event" yaml:"event"`
	Detail  string   `json:"detail,omitempty" yaml:"detail,omitempty"`
	Elapsed *float64 `json:"elapsed_s,omitempty" yaml:"elapsed_s,omitempty"`
	Count   int      `json:"count,omitempty" yaml:"count,omitempty"`
}

// detailCell renders Detail for a human-readable table or CSV cell: a row that
// summarises more than one occurrence appends the total as (×N). The
// structured (json/yaml) formats carry Count as its own field instead.
func (r GlobalEventRow) detailCell() string {
	if r.Count > 1 {
		return fmt.Sprintf("%s (×%d)", r.Detail, r.Count)
	}
	return r.Detail
}

// listGlobal renders the escape-function global optimisation results from
// output.global_result (the pipeline document produced by escape/pso).
func listGlobal(output types.Output, format string) {
	renderGlobalList(globalDataFromOutput(output), format)
}

// globalDataFromOutput flattens output.global_result into the shared
// globalListData render input: the effective escape parameters, the file
// directory shared by all minima, and one row per local minimum (merit-sorted
// rank index, status, --save file basename, per-config element powers and the
// minimum's variable values).
func globalDataFromOutput(output types.Output) globalListData {
	esc := output.GlobalResult
	if esc == nil {
		return globalListData{}
	}

	// Build minima rows. global_result.minima lists the feasible solutions;
	// infeasible basins are a separate list (only present with --keep-infeasible).
	// A streamed document writes minima append-only, so it keeps the
	// first-discovery values there and carries each improved point in
	// minima_improvements (indexed by the position it supersedes); the improved
	// entry replaces its row before the list is ranked.
	entries := effectiveGlobalMinima(esc)
	minima := make([]GlobalMinimumRow, 0, len(entries))
	files := make([]string, 0, len(entries))
	for _, m := range entries {
		var powers [][]float64
		for _, f := range m.Features {
			if len(f.ElementPowers) > 0 {
				powers = append(powers, f.ElementPowers)
			}
		}
		minima = append(minima, GlobalMinimumRow{
			Index:         m.Index,
			Merit:         m.Merit,
			Status:        string(m.Status),
			Reason:        string(m.InvalidReason),
			File:          fileBase(m.File),
			ElementPowers: powers,
			Variables:     m.Variables,
		})
		if m.File != "" {
			files = append(files, m.File)
		}
	}
	// Rank by effective merit, ascending. A one-shot document is already in
	// that order (this is a no-op for it); a streamed document is in discovery
	// order, so the stable sort is what makes the two render alike, with ties
	// kept in document order.
	sort.SliceStable(minima, func(i, j int) bool {
		return minima[i].Merit < minima[j].Merit
	})

	var infeasible []GlobalMinimumRow
	for _, m := range esc.InfeasibleBasins {
		infeasible = append(infeasible, GlobalMinimumRow{
			Index:     m.Index,
			Merit:     m.Merit,
			Status:    string(m.Status),
			Reason:    string(m.InvalidReason),
			File:      fileBase(m.File),
			Variables: m.Variables,
		})
		if m.File != "" {
			files = append(files, m.File)
		}
	}

	return globalListData{
		Found:            true,
		Params:           globalParamsSettings(&esc.Params),
		Minima:           minima,
		InfeasibleBasins: infeasible,
		BestIndex:        esc.BestIndex,
		BestMerit:        esc.BestMerit,
		FileDir:          commonFileDir(files),
		TimedOut:         esc.TimedOut,
		Interrupted:      esc.Interrupted,
	}
}

// commonFileDir returns the directory shared by every non-empty path, or "" when
// none is set, they disagree, or they all sit in the working directory (whose
// reported "." carries no information).
func commonFileDir(paths []string) string {
	dir := ""
	seen := false
	for _, p := range paths {
		if p == "" {
			continue
		}
		d := filepath.Dir(p)
		if d == "." {
			d = ""
		}
		if !seen {
			dir, seen = d, true
			continue
		}
		if d != dir {
			return ""
		}
	}
	return dir
}

// globalRunSettings flattens a run-log aggregate into key-value rows.
func globalRunSettings(r *globalRunInfo) []propRow {
	var rows []propRow
	rows = appendIntProp(rows, "Workers", r.Workers)
	rows = appendIntProp(rows, "Cycles", r.Cycles)
	rows = appendIntProp(rows, "Explorations", r.Explorations)
	rows = appendIntProp(rows, "Minima", r.Minima)
	rows = appendNumProp(rows, "Run Elapsed", r.Elapsed)
	return rows
}

// escapeWorkerStatus maps a worker's completion flags onto its display state.
// The precedence follows how the escape loop stops a worker: an explicit
// resource-guard retirement outranks an interrupt, which outranks the wall
// clock budget; anything else finished its cycles normally.
func escapeWorkerStatus(w *globalLogWorker) string {
	switch {
	case w.retired:
		return "retired"
	case w.interrupted:
		return "interrupted"
	case w.timedOut:
		return "timeout"
	}
	return "completed"
}

// workerRow converts the accumulated log record into a display row.
func workerRow(w *globalLogWorker) GlobalWorkerRow {
	row := GlobalWorkerRow{
		Worker:   w.id,
		Status:   escapeWorkerStatus(w),
		Cycles:   w.maxCycle + 1,
		Explored: w.explored,
		Recorded: w.recorded,
		Elapsed:  w.elapsed,
		Reason:   w.reason,
	}
	if w.maxCycle < 0 {
		row.Cycles = 0
	}
	if w.hasBest {
		m := w.best
		row.BestMerit = &m
	}
	return row
}

// renderGlobalList prints the escape listing in the requested format. The
// input is source-agnostic: globalDataFromOutput fills it from a pipeline
// document, buildGlobalListDataFromLog from an escape/pso JSONL run log.
func renderGlobalList(d globalListData, format string) {
	if !d.Found {
		switch format {
		case "yaml", "json":
			emitEmptyList(format, "params")
		default:
			fmt.Println("Global: (no global result)")
		}
		return
	}

	params := d.Params
	minima := d.Minima

	switch format {
	case "yaml", "json":
		emitListOutput(format, globalListOutput{
			Params:           params,
			Minima:           minima,
			InfeasibleBasins: d.InfeasibleBasins,
			BestIndex:        d.BestIndex,
			BestMerit:        d.BestMerit,
			FileDir:          d.FileDir,
			TimedOut:         d.TimedOut,
			Interrupted:      d.Interrupted,
			Run:              d.Run,
			Workers:          d.Workers,
			Events:           d.Events,
		})
	case "csv":
		printPropsSection("Global Parameters:", params)
		if d.Run != nil {
			fmt.Println()
			printPropsSection("Global Run:", globalRunSettings(d.Run))
		}
		if len(minima) > 0 {
			fmt.Println("Local Minima:")
			header := []string{"index", "merit", "status"}
			showFile := escapeShowFileColumn(d, minima)
			if showFile {
				header = append(header, "file")
			}
			fmt.Println(strings.Join(quoteCSV(header), ","))
			for _, m := range minima {
				cells := []string{
					strconv.Itoa(m.Index),
					strconv.FormatFloat(m.Merit, 'g', -1, 64),
					m.Status,
				}
				if showFile {
					cells = append(cells, m.File)
				}
				fmt.Println(strings.Join(quoteCSV(cells), ","))
			}
		}
		if len(d.InfeasibleBasins) > 0 {
			fmt.Println()
			fmt.Println("Infeasible Basins:")
			showFile := hasAnySavedFile(d.InfeasibleBasins)
			header := []string{"index", "merit"}
			if showFile {
				header = append(header, "file")
			}
			header = append(header, "reason")
			fmt.Println(strings.Join(quoteCSV(header), ","))
			for _, m := range d.InfeasibleBasins {
				cells := []string{
					strconv.Itoa(m.Index),
					strconv.FormatFloat(m.Merit, 'g', -1, 64),
				}
				if showFile {
					cells = append(cells, m.File)
				}
				cells = append(cells, m.Reason)
				fmt.Println(strings.Join(quoteCSV(cells), ","))
			}
		}
		if hasAnyElementPowers(minima) {
			fmt.Println()
			fmt.Println("Element Powers:")
			numElems := maxElementPowerCount(minima)
			header := []string{"index", "config"}
			for i := 1; i <= numElems; i++ {
				header = append(header, fmt.Sprintf("element_%d", i))
			}
			fmt.Println(strings.Join(quoteCSV(header), ","))
			for _, m := range minima {
				if len(m.ElementPowers) == 0 {
					continue
				}
				for ci, ep := range m.ElementPowers {
					cells := []string{
						strconv.Itoa(m.Index),
						strconv.Itoa(ci),
					}
					for _, v := range ep {
						cells = append(cells, strconv.FormatFloat(v, 'g', -1, 64))
					}
					fmt.Println(strings.Join(quoteCSV(cells), ","))
				}
			}
		}
		if len(d.Workers) > 0 {
			fmt.Println()
			fmt.Println("Workers:")
			fmt.Println("worker,status,cycles,explored,recorded,best_merit,elapsed_s,reason")
			for _, w := range d.Workers {
				cells := []string{
					strconv.Itoa(w.Worker),
					w.Status,
					strconv.Itoa(w.Cycles),
					strconv.Itoa(w.Explored),
					strconv.Itoa(w.Recorded),
					optionalFloatCSV(w.BestMerit),
					optionalFloatCSV(&w.Elapsed),
					w.Reason,
				}
				fmt.Println(strings.Join(quoteCSV(cells), ","))
			}
		}
		if len(d.Events) > 0 {
			fmt.Println()
			fmt.Println("Log Events:")
			fmt.Println("event,detail,elapsed_s")
			for _, e := range d.Events {
				cells := []string{e.Event, e.detailCell(), optionalFloatCSV(e.Elapsed)}
				fmt.Println(strings.Join(quoteCSV(cells), ","))
			}
		}
	default: // "table"
		// Build escape result props.
		var resultProps []propRow
		resultProps = appendIntProp(resultProps, "Best Index", d.BestIndex)
		resultProps = appendNumProp(resultProps, "Best Merit", d.BestMerit)
		if d.FileDir != "" {
			resultProps = appendStrProp(resultProps, "File Directory", d.FileDir)
		}
		if d.TimedOut {
			resultProps = appendBoolTrueProp(resultProps, "Timed Out", d.TimedOut)
		}
		if d.Interrupted {
			resultProps = appendBoolTrueProp(resultProps, "Interrupted", d.Interrupted)
		}

		printPropsSection("Global Parameters:", params)
		if len(resultProps) > 0 {
			printPropsSection("Global Result:", resultProps)
		}
		if d.Run != nil {
			printPropsSection("Global Run:", globalRunSettings(d.Run))
		}

		if len(minima) > 0 {
			fmt.Println("Local Minima:")
			showFile := escapeShowFileColumn(d, minima)
			cols := []tableColumn{
				{header: "Index", right: true},
				{header: "Merit", right: true},
				{header: "Status"},
			}
			if showFile {
				cols = append(cols, tableColumn{header: "File"})
			}
			for _, m := range minima {
				cols[0].cells = append(cols[0].cells, strconv.Itoa(m.Index))
				cols[1].cells = append(cols[1].cells, formatTableFloat(m.Merit))
				cols[2].cells = append(cols[2].cells, m.Status)
				if showFile {
					cols[3].cells = append(cols[3].cells, m.File)
				}
			}
			fmt.Print(renderTable(cols))
			fmt.Println()
		}

		if len(d.InfeasibleBasins) > 0 {
			fmt.Println("Infeasible Basins:")
			showFile := hasAnySavedFile(d.InfeasibleBasins)
			cols := []tableColumn{
				{header: "Index", right: true},
				{header: "Merit", right: true},
			}
			fileCol := len(cols)
			if showFile {
				cols = append(cols, tableColumn{header: "File"})
			}
			reasonCol := len(cols)
			cols = append(cols, tableColumn{header: "Reason"})
			for _, m := range d.InfeasibleBasins {
				cols[0].cells = append(cols[0].cells, strconv.Itoa(m.Index))
				cols[1].cells = append(cols[1].cells, formatTableFloat(m.Merit))
				if showFile {
					cols[fileCol].cells = append(cols[fileCol].cells, m.File)
				}
				reason := m.Reason
				if reason == "" {
					reason = "-"
				}
				cols[reasonCol].cells = append(cols[reasonCol].cells, reason)
			}
			fmt.Print(renderTable(cols))
			fmt.Println()
		}

		if hasAnyElementPowers(minima) {
			fmt.Println("Element Powers:")
			numElems := maxElementPowerCount(minima)
			numCols := make([]tableColumn, 2+numElems)
			numCols[0] = tableColumn{header: "Index", right: true}
			numCols[1] = tableColumn{header: "Config"}
			for i := 0; i < numElems; i++ {
				numCols[2+i] = tableColumn{header: fmt.Sprintf("Element %d", i+1), right: true}
			}
			for _, m := range minima {
				if len(m.ElementPowers) == 0 {
					continue
				}
				for ci, ep := range m.ElementPowers {
					numCols[0].cells = append(numCols[0].cells, strconv.Itoa(m.Index))
					numCols[1].cells = append(numCols[1].cells, strconv.Itoa(ci))
					for ei := 0; ei < numElems; ei++ {
						if ei < len(ep) {
							numCols[2+ei].cells = append(numCols[2+ei].cells, formatTableFloat(ep[ei]))
						} else {
							numCols[2+ei].cells = append(numCols[2+ei].cells, "-")
						}
					}
				}
			}
			fmt.Print(renderTable(numCols))
			fmt.Println()
		}

		if len(d.Workers) > 0 {
			fmt.Println("Workers:")
			cols := []tableColumn{
				{header: "Worker", right: true},
				{header: "Status"},
				{header: "Cycles", right: true},
				{header: "Explored", right: true},
				{header: "Recorded", right: true},
				{header: "Best Merit", right: true},
				{header: "Elapsed[s]", right: true},
			}
			if hasAnyWorkerReason(d.Workers) {
				cols = append(cols, tableColumn{header: "Reason"})
			}
			for _, w := range d.Workers {
				cols[0].cells = append(cols[0].cells, strconv.Itoa(w.Worker))
				cols[1].cells = append(cols[1].cells, w.Status)
				cols[2].cells = append(cols[2].cells, strconv.Itoa(w.Cycles))
				cols[3].cells = append(cols[3].cells, strconv.Itoa(w.Explored))
				cols[4].cells = append(cols[4].cells, strconv.Itoa(w.Recorded))
				cols[5].cells = append(cols[5].cells, formatOptionalFloat(w.BestMerit))
				cols[6].cells = append(cols[6].cells, formatTableFloat(w.Elapsed))
				if hasAnyWorkerReason(d.Workers) {
					cell := w.Reason
					if cell == "" {
						cell = "-"
					}
					cols[7].cells = append(cols[7].cells, cell)
				}
			}
			fmt.Print(renderTable(cols))
			fmt.Println()
		}

		if len(d.Events) > 0 {
			fmt.Println("Log Events:")
			cols := []tableColumn{
				{header: "Event"},
				{header: "Detail"},
				{header: "Elapsed[s]", right: true},
			}
			for _, e := range d.Events {
				detail := e.detailCell()
				if detail == "" {
					detail = "-"
				}
				cols[0].cells = append(cols[0].cells, e.Event)
				cols[1].cells = append(cols[1].cells, detail)
				cols[2].cells = append(cols[2].cells, formatOptionalFloat(e.Elapsed))
			}
			fmt.Print(renderTable(cols))
			fmt.Println()
		}
	}
}

// hasAnyWorkerReason reports whether any worker carries a retirement reason
// (the Reason column is dropped when none do).
func hasAnyWorkerReason(workers []GlobalWorkerRow) bool {
	for _, w := range workers {
		if w.Reason != "" {
			return true
		}
	}
	return false
}

// hasAnySavedFile reports whether any row recovered a --save file name.
func hasAnySavedFile(rows []GlobalMinimumRow) bool {
	for _, r := range rows {
		if r.File != "" && r.File != "-" {
			return true
		}
	}
	return false
}

// escapeShowFileColumn reports whether the minima table carries a File column.
// A pipeline document always shows it (a run without --save renders as "-");
// a run log shows it only when its minimum_saved events yielded file names.
func escapeShowFileColumn(d globalListData, rows []GlobalMinimumRow) bool {
	return !d.FromLog || hasAnySavedFile(rows)
}

// fileBase returns the basename of a file path, or "-" when empty.
func fileBase(path string) string {
	if path == "" {
		return "-"
	}
	return filepath.Base(path)
}

// hasAnyElementPowers reports whether any minimum has element powers.
func hasAnyElementPowers(minima []GlobalMinimumRow) bool {
	for _, m := range minima {
		if len(m.ElementPowers) > 0 {
			return true
		}
	}
	return false
}

// maxElementPowerCount returns the maximum number of element powers per config
// across all minima.
func maxElementPowerCount(minima []GlobalMinimumRow) int {
	n := 0
	for _, m := range minima {
		for _, ep := range m.ElementPowers {
			if len(ep) > n {
				n = len(ep)
			}
		}
	}
	return n
}
