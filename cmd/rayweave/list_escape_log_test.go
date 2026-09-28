package main

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// runListEscapeLogCLI runs `list escape` (or `list`) over an escape/pso JSONL
// run log and returns the captured stdout.
func runListEscapeLogCLI(t *testing.T, log string, extra ...string) []byte {
	t.Helper()
	args := append([]string{"rayweave", "list", "escape"}, extra...)
	return runCommand(t, args, func() { runList([]byte(log)) })
}

// escapeLogFixture is a trimmed full `--log` stream: 2 workers, 3 cycles of
// budget, one retired worker and one timeout, four discovery indices of which
// one is an infeasible basin and one is improved.
const escapeLogFixture = `{"cycle":0,"elapsed":0.1,"time":"2026-09-28T10:00:00Z","event":"params","h":0.1,"h_mult":2.0,"w":0.5,"w_mult":1.3,"distance_threshold":0.1}
{"cycle":0,"elapsed":0.2,"time":"2026-09-28T10:00:00Z","event":"start","max_cycles":3,"max_seconds":600,"workers":2}
{"cycle":0,"elapsed":1.0,"time":"2026-09-28T10:00:01Z","event":"cycle","merit":0.5,"worker":0,"phase":"escape_dls","status":"accepted","dls_status":"converged"}
{"cycle":0,"elapsed":1.5,"time":"2026-09-28T10:00:01Z","event":"minimum","cycle":0,"worker":0,"kind":"new","index":0,"merit":0.004,"min_status":"feasible_local_minimum","invalid_reason":""}
{"cycle":0,"elapsed":2.0,"time":"2026-09-28T10:00:02Z","event":"cycle","merit":0.9,"worker":1,"phase":"escape_dls","status":"accepted","dls_status":"converged"}
{"cycle":0,"elapsed":2.4,"time":"2026-09-28T10:00:02Z","event":"minimum","cycle":0,"worker":1,"kind":"new","index":1,"merit":0.009,"min_status":"infeasible_basin","invalid_reason":"insufficient_field_throughput"}
{"cycle":1,"elapsed":3.0,"time":"2026-09-28T10:00:03Z","event":"cycle","merit":0.3,"worker":0,"phase":"escape_dls","status":"accepted","dls_status":"converged"}
{"cycle":1,"elapsed":3.4,"time":"2026-09-28T10:00:03Z","event":"minimum","cycle":1,"worker":0,"kind":"new","index":2,"merit":0.002,"min_status":"feasible_local_minimum","invalid_reason":""}
{"cycle":1,"elapsed":4.0,"time":"2026-09-28T10:00:04Z","event":"cycle","merit":0.3,"worker":0,"phase":"clean_dls","status":"accepted","dls_status":"converged"}
{"cycle":2,"elapsed":5.0,"time":"2026-09-28T10:00:05Z","event":"cycle","merit":0.2,"worker":0,"phase":"escape_dls","status":"accepted","dls_status":"converged"}
{"cycle":2,"elapsed":5.4,"time":"2026-09-28T10:00:05Z","event":"minimum","cycle":2,"worker":0,"kind":"improved","index":2,"merit":0.001,"min_status":"feasible_local_minimum","invalid_reason":""}
{"cycle":3,"elapsed":6.0,"time":"2026-09-28T10:00:06Z","event":"worker_retired","worker":1,"reason":"memory_pressure","cycle":1,"escaped":1,"recorded":1,"best_merit":0.009,"metrics":{"heap_alloc_mb":1024}}
{"cycle":3,"elapsed":6.1,"time":"2026-09-28T10:00:06Z","event":"worker_done","worker":1,"escaped":1,"recorded":1,"retired":true,"interrupted":false,"timed_out":false}
{"cycle":3,"elapsed":7.0,"time":"2026-09-28T10:00:07Z","event":"timeout","worker":0,"cycle":3,"max_cycles":3}
{"cycle":3,"elapsed":7.1,"time":"2026-09-28T10:00:07Z","event":"worker_done","worker":0,"escaped":3,"recorded":2,"retired":false,"interrupted":false,"timed_out":true}
{"cycle":3,"elapsed":7.5,"time":"2026-09-28T10:00:07Z","event":"done","workers":2,"cycles":3,"escapes":4,"minima":2,"best_merit":0.001,"timed_out":true,"interrupted":false}
{"cycle":3,"elapsed":7.6,"time":"2026-09-28T10:00:07Z","event":"escape_complete","workers":2,"cycles":3,"escapes":4,"minima_count":2,"best_merit":0.001,"timed_out":true,"interrupted":false,"minima":[{"index":0,"merit":0.001,"best":true},{"index":1,"merit":0.004,"best":false}]}
`

func TestLooksLikeEscapeJSONL(t *testing.T) {
	if !looksLikeEscapeJSONL([]byte(escapeLogFixture)) {
		t.Fatal("escape log fixture was not recognised as JSONL")
	}
	// A pipeline document is not a run log.
	if looksLikeEscapeJSONL([]byte(singletYAML)) {
		t.Error("singlet pipeline YAML was recognised as a run log")
	}
	// An optimize --log stream has no "event" key.
	optLog := "{\"iter\":0,\"merit\":0.5,\"status\":\"running\"}\n{\"iter\":1,\"merit\":0.2,\"status\":\"converged\"}\n"
	if looksLikeEscapeJSONL([]byte(optLog)) {
		t.Error("optimize --log stream was recognised as an escape run log")
	}
	// A single JSON object without a known event is not a run log either.
	if looksLikeEscapeJSONL([]byte("{\"a\":1}\n{\"b\":2}\n")) {
		t.Error("arbitrary JSONL was recognised as an escape run log")
	}
	// Compact (--verbose) lines, where only the fixed key order survives.
	compact := "{\"cycle\":0,\"e\":\"00:00\",\"t\":\"10:00:00\",\"event\":\"params\",\"h\":1.00000e-01,\"w\":5.00000e-01}\n" +
		"{\"e\":\"00:01\",\"t\":\"10:00:01\",\"event\":\"worker_done\",\"worker\":0,\"escaped\":1,\"recorded\":1}\n"
	if !looksLikeEscapeJSONL([]byte(compact)) {
		t.Error("compact run log was not recognised as JSONL")
	}
	// A pso run log differs from an escape one only in the extra pso_* events.
	psoLog := "{\"e\":\"00:00\",\"t\":\"10:00:00\",\"event\":\"params\",\"h\":1.00000e-01}\n" +
		"{\"swarm_size\":2.00000e+01,\"n_vars\":7.00000e+00,\"pso_iterations\":5.00000e+01,\"inertia\":9.00000e-01,\"event\":\"pso_init\"}\n" +
		"{\"iter\":1.00000e+01,\"gbest_merit\":1.00000e-01,\"phase\":\"pso\",\"event\":\"pso_iter\"}\n"
	if !looksLikeEscapeJSONL([]byte(psoLog)) {
		t.Error("pso run log was not recognised as JSONL")
	}
}

// escapeSignalLogFixture is a full `--log` stream of a run stopped by two
// signals and then force-quit, with the resource guard tightening the memory
// limit, retiring a worker and reporting a save error.
const escapeSignalLogFixture = `{"elapsed":0.2,"time":"2026-09-28T10:00:00Z","event":"start","max_cycles":40,"workers":4}
{"elapsed":1.0,"time":"2026-09-28T10:00:01Z","event":"minimum","cycle":0,"worker":0,"kind":"new","index":0,"merit":0.001,"min_status":"feasible_local_minimum"}
{"elapsed":5.0,"time":"2026-09-28T10:00:05Z","event":"resource","cycle":2,"workers":4,"heap_alloc_mb":512.3,"heap_sys_mb":700.1,"pressure_level":1,"compaction_rate_per_s":0.35}
{"elapsed":6.0,"time":"2026-09-28T10:00:06Z","event":"memory_limit","kind":"tighten","reason":"heap_pressure","before_mb":2048,"after_mb":1536,"metrics":{}}
{"elapsed":6.5,"time":"2026-09-28T10:00:06Z","event":"worker_retire","worker":2,"reason":"compaction_rate","cycle":3,"workers_before":4,"workers_after":3,"min_workers":1}
{"elapsed":7.0,"time":"2026-09-28T10:00:07Z","event":"resource","cycle":3,"workers":3,"heap_alloc_mb":410.0,"pressure_level":2,"note":"trend_change"}
{"elapsed":8.0,"time":"2026-09-28T10:00:08Z","event":"worker_retired","worker":2,"reason":"compaction_rate","cycle":3,"escaped":3,"recorded":2,"best_merit":0.004}
{"elapsed":9.0,"time":"2026-09-28T10:00:09Z","event":"interrupt","signal":"terminated"}
{"elapsed":9.5,"time":"2026-09-28T10:00:09Z","event":"worker_done","worker":0,"escaped":5,"recorded":3,"retired":false,"interrupted":true,"timed_out":false}
{"elapsed":12.0,"time":"2026-09-28T10:00:12Z","event":"interrupt_dls","signal":"terminated"}
{"elapsed":12.5,"time":"2026-09-28T10:00:12Z","event":"error","message":"save minima0.yaml: write failed, no space left on device"}
{"elapsed":13.0,"time":"2026-09-28T10:00:13Z","event":"worker_done","worker":0,"escaped":5,"recorded":3,"retired":false,"interrupted":true,"timed_out":false}
{"elapsed":14.0,"time":"2026-09-28T10:00:14Z","event":"force_quit"}
`

func TestListEscapeFromLogEvents(t *testing.T) {
	out := runListEscapeLogCLI(t, escapeSignalLogFixture, "--format", "json")
	var doc struct {
		Interrupted bool `json:"interrupted"`
		Events      []struct {
			Event   string   `json:"event"`
			Detail  string   `json:"detail"`
			Elapsed *float64 `json:"elapsed_s"`
		} `json:"events"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if !doc.Interrupted {
		t.Error("interrupted should still be reported in Escape Result")
	}

	// Every run-level event is listed once, in log order.
	want := []struct {
		event   string
		detail  string
		elapsed float64
	}{
		{"resource", "heap=512.3 MB pressure=1 compaction=0.35/s", 5.0},
		{"memory_limit", "tighten heap_pressure 2048→1536 MB", 6.0},
		{"worker_retire", "worker 2 reason=compaction_rate cycle=3", 6.5},
		{"resource", "heap=410.0 MB pressure=2 trend_change", 7.0},
		{"interrupt", "terminated", 9.0},
		{"interrupt_dls", "terminated", 12.0},
		{"error", "save minima0.yaml: write failed, no space left on device", 12.5},
		{"force_quit", "", 14.0},
	}
	if len(doc.Events) != len(want) {
		t.Fatalf("events = %d, want %d: %+v", len(doc.Events), len(want), doc.Events)
	}
	for i, w := range want {
		got := doc.Events[i]
		if got.Event != w.event {
			t.Errorf("events[%d].event = %q, want %q", i, got.Event, w.event)
		}
		if got.Detail != w.detail {
			t.Errorf("events[%d].detail = %q, want %q", i, got.Detail, w.detail)
		}
		if got.Elapsed == nil || *got.Elapsed != w.elapsed {
			t.Errorf("events[%d].elapsed_s = %v, want %v", i, got.Elapsed, w.elapsed)
		}
	}
}

func TestListEscapeFromLogEventsTableAndCSV(t *testing.T) {
	out := runListEscapeLogCLI(t, escapeSignalLogFixture)
	text := string(out)
	if !strings.Contains(text, "Log Events:") {
		t.Fatalf("table output missing the Log Events section:\n%s", text)
	}
	for _, want := range []string{
		"Event",
		"Detail",
		"Elapsed[s]",
		"interrupt",
		"interrupt_dls",
		"terminated",
		"memory_limit",
		"tighten heap_pressure 2048→1536 MB",
		"worker_retire",
		"worker 2 reason=compaction_rate cycle=3",
		"save minima0.yaml: write failed, no space left on device",
		"force_quit",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("Log Events table missing %q:\n%s", want, text)
		}
	}

	csvOut := runListEscapeLogCLI(t, escapeSignalLogFixture, "--format", "csv")
	csvText := string(csvOut)
	if !strings.Contains(csvText, "Log Events:\nevent,detail,elapsed_s\n") {
		t.Errorf("csv output missing the Log Events header:\n%s", csvText)
	}
	if !strings.Contains(csvText, "interrupt,terminated,9\n") {
		t.Errorf("csv signal row wrong:\n%s", csvText)
	}
	if !strings.Contains(csvText, `error,"save minima0.yaml: write failed, no space left on device",12.5`) {
		t.Errorf("csv error row wrong:\n%s", csvText)
	}
}

func TestListEscapeFromLogEventsCompact(t *testing.T) {
	// A compact stream keeps only the event name; the detail columns are
	// dropped and the elapsed time is read from the HH:MM clock.
	log := `{"e":"00:00","t":"10:00:00","event":"start","max_cycles":1.00000e+01,"workers":1.00000e+00}
{"e":"02:05","t":"10:02:05","event":"interrupt"}
{"e":"02:06","t":"10:02:06","event":"interrupt_dls"}
`
	out := runListEscapeLogCLI(t, log, "--format", "json")
	var doc struct {
		Events []struct {
			Event   string   `json:"event"`
			Detail  string   `json:"detail"`
			Elapsed *float64 `json:"elapsed_s"`
		} `json:"events"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if len(doc.Events) != 2 {
		t.Fatalf("events = %d, want 2: %+v", len(doc.Events), doc.Events)
	}
	if doc.Events[0].Event != "interrupt" || doc.Events[0].Detail != "" {
		t.Errorf("events[0] = %+v, want interrupt with no detail", doc.Events[0])
	}
	if doc.Events[0].Elapsed == nil || *doc.Events[0].Elapsed != 7500 {
		t.Errorf("events[0].elapsed_s = %v, want 7500 (the compact HH:MM clock, 2h05m)",
			formatOptionalFloat(doc.Events[0].Elapsed))
	}
	if doc.Events[1].Event != "interrupt_dls" {
		t.Errorf("events[1] = %+v", doc.Events[1])
	}
}

func TestListEscapeNoEventsForCleanLog(t *testing.T) {
	// A log without any run-level event must not gain an empty section.
	out := runListEscapeLogCLI(t, escapeLogFixture, "--format", "json")
	if strings.Contains(string(out), "\"events\"") {
		t.Errorf("events key must be absent for a log with no run-level events:\n%s", out)
	}
	// The pipeline path never has it either.
	if strings.Contains(string(runListEscapeLogCLI(t, "escape_result:\n  best_index: 0\n", "--format", "json")), "events") {
		t.Error("events key must be absent for a pipeline document")
	}
}

func TestListEscapeFromLogYAML(t *testing.T) {
	out := runListEscapeLogCLI(t, escapeLogFixture, "--format", "yaml")
	var doc struct {
		Params []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"params"`
		Minima []struct {
			Index  int     `json:"index"`
			Merit  float64 `json:"merit"`
			Status string  `json:"status"`
		} `json:"minima"`
		BestIndex   int     `json:"best_index"`
		BestMerit   float64 `json:"best_merit"`
		TimedOut    bool    `json:"timed_out"`
		Interrupted bool    `json:"interrupted"`
		Run         *struct {
			Workers int     `json:"workers"`
			Cycles  int     `json:"cycles"`
			Escapes int     `json:"escapes"`
			Minima  int     `json:"minima_count"`
			Elapsed float64 `json:"elapsed_s"`
		} `json:"run"`
		Workers []struct {
			Worker    int      `json:"worker"`
			Status    string   `json:"status"`
			Cycles    int      `json:"cycles"`
			Escaped   int      `json:"escaped"`
			Recorded  int      `json:"recorded"`
			BestMerit *float64 `json:"best_merit"`
			Elapsed   float64  `json:"elapsed_s"`
			Reason    string   `json:"reason"`
		} `json:"workers"`
	}
	if err := json.Unmarshal(convertYAMLtoJSON(t, out), &doc); err != nil {
		t.Fatalf("unmarshal list output: %v\n%s", err, out)
	}

	// Params come from the params/start events only.
	want := map[string]string{
		"Max Cycles":         "3",
		"Escape Workers":     "2",
		"Max Seconds":        "600.000000",
		"Distance Threshold": "0.100000",
		"H Initial":          "0.100000",
		"W Initial":          "0.500000",
		"H Mult":             "2.000000",
		"W Mult":             "1.300000",
	}
	got := map[string]string{}
	for _, p := range doc.Params {
		got[p.Name] = p.Value
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("param %s = %q, want %q", k, got[k], v)
		}
	}
	// Parameters absent from the log must not appear.
	if _, ok := got["Stall Early Stop"]; ok {
		t.Error("Stall Early Stop must not be shown for a run log (not in the params event)")
	}

	// Minima: merit-sorted rank, infeasible basin excluded, improved merit kept.
	if len(doc.Minima) != 2 {
		t.Fatalf("minima count = %d, want 2 (%+v)", len(doc.Minima), doc.Minima)
	}
	if doc.Minima[0].Index != 0 || doc.Minima[0].Merit != 0.001 {
		t.Errorf("minima[0] = %+v, want index 0 merit 0.001", doc.Minima[0])
	}
	if doc.Minima[0].Status != "feasible_local_minimum" {
		t.Errorf("minima[0].status = %q, want feasible_local_minimum", doc.Minima[0].Status)
	}
	if doc.Minima[1].Index != 1 || doc.Minima[1].Merit != 0.004 {
		t.Errorf("minima[1] = %+v, want index 1 merit 0.004", doc.Minima[1])
	}
	if doc.BestIndex != 0 || doc.BestMerit != 0.001 {
		t.Errorf("best = %d/%v, want 0/0.001", doc.BestIndex, doc.BestMerit)
	}
	if !doc.TimedOut || doc.Interrupted {
		t.Errorf("timed_out/interrupted = %v/%v, want true/false", doc.TimedOut, doc.Interrupted)
	}

	// Run aggregate.
	if doc.Run == nil {
		t.Fatal("run section missing")
	}
	if doc.Run.Workers != 2 || doc.Run.Cycles != 3 || doc.Run.Escapes != 4 || doc.Run.Minima != 2 {
		t.Errorf("run = %+v, want workers 2 cycles 3 escapes 4 minima 2", *doc.Run)
	}
	if doc.Run.Elapsed != 7.6 {
		t.Errorf("run.elapsed_s = %v, want 7.6", doc.Run.Elapsed)
	}

	// Worker lifecycle.
	if len(doc.Workers) != 2 {
		t.Fatalf("workers = %d, want 2", len(doc.Workers))
	}
	w0, w1 := doc.Workers[0], doc.Workers[1]
	if w0.Worker != 0 || w0.Status != "timeout" || w0.Cycles != 4 || w0.Escaped != 3 || w0.Recorded != 2 {
		t.Errorf("workers[0] = %+v", w0)
	}
	if w0.Elapsed != 7.1 {
		t.Errorf("workers[0].elapsed_s = %v, want 7.1", w0.Elapsed)
	}
	if w0.BestMerit != nil {
		t.Errorf("workers[0].best_merit = %v, want absent", *w0.BestMerit)
	}
	if w1.Worker != 1 || w1.Status != "retired" || w1.Cycles != 2 || w1.Reason != "memory_pressure" {
		t.Errorf("workers[1] = %+v", w1)
	}
	if w1.BestMerit == nil || *w1.BestMerit != 0.009 {
		t.Errorf("workers[1].best_merit = %v, want 0.009", w1.BestMerit)
	}
}

func TestListEscapeFromLogTable(t *testing.T) {
	out := runListEscapeLogCLI(t, escapeLogFixture)
	text := string(out)
	for _, want := range []string{
		"Escape Parameters:",
		"Escape Result:",
		"Escape Run:",
		"Local Minima:",
		"Workers:",
		"Run Elapsed",
		"Timed Out",
		"timeout",
		"retired",
		"memory_pressure",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("table output missing %q:\n%s", want, text)
		}
	}
	// A run log has no saved-file names, so the File column is dropped.
	if strings.Contains(text, "File Directory") || strings.Contains(text, "File\n") {
		t.Errorf("run log table must not show saved-file information:\n%s", text)
	}
	// Element powers are not recoverable from a run log.
	if strings.Contains(text, "Element Powers") {
		t.Errorf("run log table must not show element powers:\n%s", text)
	}
}

func TestListEscapeFromLogCSV(t *testing.T) {
	out := runListEscapeLogCLI(t, escapeLogFixture, "--format", "csv")
	text := string(out)
	if !strings.Contains(text, "Local Minima:\nindex,merit,status\n") {
		t.Errorf("csv minima header should drop the file column:\n%s", text)
	}
	if !strings.Contains(text, "Escape Run:") {
		t.Errorf("csv output missing the Escape Run section:\n%s", text)
	}
	if !strings.Contains(text, "worker,status,cycles,escaped,recorded,best_merit,elapsed_s,reason\n") {
		t.Errorf("csv output missing the workers header:\n%s", text)
	}
	if !strings.Contains(text, "1,retired,2,1,1,0.009,6.1,memory_pressure") {
		t.Errorf("csv worker row wrong:\n%s", text)
	}
}

func TestListEscapeFromLogMinimal(t *testing.T) {
	// A log truncated before escape_complete: the minima still come from the
	// `minimum` events, and the best merit from them rather than the summary.
	log := `{"elapsed":0.1,"event":"params","h":0.1,"w":0.5,"h_mult":2.0,"w_mult":1.3,"distance_threshold":0.1}
{"elapsed":0.2,"event":"start","max_cycles":10,"workers":4}
{"elapsed":1.0,"event":"minimum","cycle":0,"worker":0,"kind":"new","index":3,"merit":0.5,"min_status":"feasible_local_minimum"}
{"elapsed":2.0,"event":"minimum","cycle":1,"worker":1,"kind":"new","index":7,"merit":0.25,"min_status":"feasible_local_minimum"}
`
	out := runListEscapeLogCLI(t, log, "--format", "json")
	var doc struct {
		BestMerit float64 `json:"best_merit"`
		Minima    []struct {
			Index  int     `json:"index"`
			Merit  float64 `json:"merit"`
			Status string  `json:"status"`
		} `json:"minima"`
		Run     *struct{}         `json:"run"`
		Workers []json.RawMessage `json:"workers"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if len(doc.Minima) != 2 || doc.Minima[0].Merit != 0.25 || doc.Minima[0].Index != 0 {
		t.Fatalf("minima = %+v, want the merit-sorted 0.25 first", doc.Minima)
	}
	if doc.Minima[1].Merit != 0.5 || doc.Minima[1].Index != 1 {
		t.Errorf("minima[1] = %+v, want merit 0.5 at rank 1", doc.Minima[1])
	}
	if doc.BestMerit != 0.25 {
		t.Errorf("best_merit = %v, want 0.25", doc.BestMerit)
	}
	if doc.Run != nil {
		t.Error("run section must be absent without a done/escape_complete event")
	}
}

func TestListEscapeFromLogCompleteOnlyFallback(t *testing.T) {
	// A log with only the final summary (no `minimum` events): the
	// merit-sorted minima array of escape_complete is the fallback source.
	log := `{"elapsed":0.2,"event":"start","max_cycles":10,"workers":2}
{"elapsed":9.0,"event":"escape_complete","workers":2,"cycles":10,"escapes":12,"minima_count":2,"best_merit":0.002,"minima":[{"index":0,"merit":0.002,"best":true},{"index":1,"merit":0.03,"best":false}]}
`
	out := runListEscapeLogCLI(t, log, "--format", "json")
	var doc struct {
		BestMerit float64 `json:"best_merit"`
		Minima    []struct {
			Index int     `json:"index"`
			Merit float64 `json:"merit"`
		} `json:"minima"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if len(doc.Minima) != 2 || doc.Minima[0].Merit != 0.002 || doc.Minima[1].Merit != 0.03 {
		t.Fatalf("minima = %+v, want the escape_complete fallback list", doc.Minima)
	}
	if doc.BestMerit != 0.002 {
		t.Errorf("best_merit = %v, want 0.002", doc.BestMerit)
	}
}

func TestListEscapeFromLogCompactDropsFields(t *testing.T) {
	// A compact --verbose stream loses min_status and the worker_done flags
	// (they are outside the fixed key order), so the listing degrades
	// gracefully instead of failing.
	log := `{"cycle":0,"e":"00:00","t":"10:00:00","event":"params","h":1.00000e-01,"w":5.00000e-01,"h_mult":2.00000e+00,"w_mult":1.30000e+00,"distance_threshold":1.00000e-01}
{"e":"00:00","t":"10:00:00","event":"start","max_cycles":1.00000e+01,"workers":4.00000e+00}
{"cycle":0,"e":"00:01","t":"10:00:01","event":"minimum","cycle":0,"worker":0,"index":0,"merit":1.23400e-03}
{"e":"00:02","t":"10:00:02","event":"worker_done","worker":0,"escaped":2.00000e+00,"recorded":1.00000e+00}
`
	out := runListEscapeLogCLI(t, log, "--format", "json")
	var doc struct {
		Minima []struct {
			Index  int     `json:"index"`
			Merit  float64 `json:"merit"`
			Status string  `json:"status"`
		} `json:"minima"`
		Workers []struct {
			Worker  int    `json:"worker"`
			Status  string `json:"status"`
			Escaped int    `json:"escaped"`
		} `json:"workers"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if len(doc.Minima) != 1 || doc.Minima[0].Merit != 0.001234 {
		t.Fatalf("minima = %+v, want one entry at 0.001234", doc.Minima)
	}
	if doc.Minima[0].Status != "" {
		t.Errorf("status = %q, want empty (dropped by the compact stream)", doc.Minima[0].Status)
	}
	if len(doc.Workers) != 1 || doc.Workers[0].Escaped != 2 {
		t.Fatalf("workers = %+v, want one worker with 2 escapes", doc.Workers)
	}
	if doc.Workers[0].Status != "completed" {
		t.Errorf("status = %q, want completed (no flags in the compact stream)", doc.Workers[0].Status)
	}
}

func TestEscapeLogTargetError(t *testing.T) {
	// A run log has no system definition, so a non-escape target is rejected
	// rather than silently listing nothing.
	if err := escapeLogTargetError(nil); err != nil {
		t.Errorf("bare invocation should default to escape: %v", err)
	}
	if err := escapeLogTargetError([]string{"escape"}); err != nil {
		t.Errorf("escape target should be accepted: %v", err)
	}
	for _, target := range []string{"surfaces", "all", "default", "focus"} {
		if err := escapeLogTargetError([]string{target}); err == nil {
			t.Errorf("target %q should be rejected for a run log", target)
		}
	}
}

func TestListEscapeFromPipelineUnchanged(t *testing.T) {
	// The pipeline path keeps its shape: workers/run are absent, and the File
	// column is always shown.
	pipeline := `escape_result:
  best_index: 0
  best_merit: 0.001234
  params:
    h_initial: 0.1
    w_initial: 0.5
    max_cycles: 10
    escape_workers: 4
  minima:
    - index: 0
      merit: 0.001234
      status: feasible_local_minimum
      file: /tmp/esc/minima0.yaml
      features:
        - id: config1
          element_powers: [0.0184, -0.0092]
    - index: 1
      merit: 0.0021
      status: feasible_local_minimum
      file: /tmp/esc/minima3.yaml
      features:
        - id: config1
          element_powers: [0.0179, -0.0088]
`
	out := runListEscapeLogCLI(t, pipeline)
	text := string(out)
	for _, want := range []string{"Escape Parameters:", "Escape Result:", "Local Minima:", "Element Powers:", "minima0.yaml", "minima3.yaml"} {
		if !strings.Contains(text, want) {
			t.Errorf("pipeline listing missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Workers:") || strings.Contains(text, "Escape Run:") {
		t.Errorf("pipeline listing must not show run-log-only sections:\n%s", text)
	}

	outY := runListEscapeLogCLI(t, pipeline, "--format", "json")
	var doc map[string]any
	if err := json.Unmarshal(outY, &doc); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, outY)
	}
	if _, ok := doc["workers"]; ok {
		t.Error("workers key must be absent for a pipeline document")
	}
	if _, ok := doc["run"]; ok {
		t.Error("run key must be absent for a pipeline document")
	}
}

// convertYAMLtoJSON re-encodes a YAML document as JSON for decoding with
// encoding/json in the tests.
func convertYAMLtoJSON(t *testing.T, data []byte) []byte {
	t.Helper()
	var v any
	if err := yaml.Unmarshal(data, &v); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return out
}
