package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hiroki/rayweaver/internal/types"
	"gopkg.in/yaml.v3"
)

// The global cycle exposes its closing phase as a value-taking boolean CLI
// flag on both `escape` and `pso` (the same reason --stream takes a value: a Go
// bool flag cannot consume a following "true"/"false"). These tests pin the
// CLI/YAML/default precedence, the write-back rule, and one full run of each
// subcommand proving the flag reaches the cycle.

// phaseFlagInput is a minimal but complete document for both subcommands: one
// lens, two curvature variables, a spot merit, and an escape and a pso section
// with a one-worker budget so a run finishes immediately.
const phaseFlagInput = `glass_catalog:
  entries:
    - {type: model, name: N-BK7, nd: 1.5168, vd: 64.17}
chief:
  fields:
    - {angle: 0, direction: [0, 1]}
  reference_surface: 3
  reference_wavelength: 0.0005876
  num_rays: 32
  grid_type: hex
configs:
  - id: cfg1
    active: true
    merit:
      type: weighted_sum
      terms:
        - kind: spot_rms
          field: 0
          wavelength: 0.0005876
          weight: 1.0
    surfaces:
      - {id: 1, type: sphere, radius: 50.0, thickness: 5.0, material: N-BK7, diameter: 30.0}
      - {id: 2, type: sphere, radius: -50.0, thickness: 100.0, material: AIR, diameter: 30.0}
      - {id: 3, type: sphere, radius: 0, thickness: 0, material: AIR, diameter: 30.0}
optimization:
  method: dls
  max_iter: 30
  escape:
    max_cycles: 2
    global_workers: 1
    distance_threshold: 0.1
    h_initial: 0.5
    w_initial: 0.4
  pso:
    max_cycles: 2
    global_workers: 1
    distance_threshold: 0.1
    swarm_size: 6
    pso_iterations: 8
  variables:
    - name: s1_curvature
      target: {type: surface, id: 1, param: curvature}
      min: 0.005
      max: 0.05
      active: true
    - name: s2_curvature
      target: {type: surface, id: 2, param: curvature}
      min: -0.05
      max: -0.005
      active: true
`

// TestEscapePhaseCLIHelper is the subprocess entry point (same pattern as
// TestQueryCLIHelper): the parent invokes the test binary with -test.run and
// the argument list in the environment, feeds phaseFlagInput on stdin and lets
// main() write the pipeline document to stdout.
func TestEscapePhaseCLIHelper(t *testing.T) {
	if os.Getenv("GO_ESCAPE_PHASE_HELPER") != "1" {
		return
	}
	var args []string
	_ = json.Unmarshal([]byte(os.Getenv("GO_ESCAPE_PHASE_ARGS")), &args)
	os.Args = append([]string{"rayweave"}, args...)
	main()
	os.Exit(0)
}

// runEscapePhaseCLI runs one subcommand on phaseFlagInput and returns the
// pipeline document together with the full JSONL progress log the run wrote.
func runEscapePhaseCLI(t *testing.T, args ...string) (string, string) {
	t.Helper()
	logFile := filepath.Join(t.TempDir(), "progress.jsonl")
	raw, _ := json.Marshal(append(append([]string{}, args...), "--log", logFile))
	cmd := exec.Command(os.Args[0], "-test.run=^TestEscapePhaseCLIHelper$")
	cmd.Env = append(os.Environ(), "GO_ESCAPE_PHASE_HELPER=1", "GO_ESCAPE_PHASE_ARGS="+string(raw))
	cmd.Stdin = strings.NewReader(phaseFlagInput)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("rayweave %v failed: %v\nstderr:\n%s", args, err, errBuf.String())
	}
	logBytes, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read progress log: %v", err)
	}
	return out.String(), string(logBytes)
}

// phaseLogEvent is the subset of an escape progress line these tests read.
type phaseLogEvent struct {
	Event  string `json:"event"`
	Phase  string `json:"phase"`
	Status string `json:"status"`
	// The phase switch rides the start event only when disabled.
	CleanDLS *bool `json:"clean_dls"`
}

func parsePhaseLog(t *testing.T, raw string) []phaseLogEvent {
	t.Helper()
	var events []phaseLogEvent
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		if line == "" {
			continue
		}
		var ev phaseLogEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("bad JSONL line %q: %v", line, err)
		}
		events = append(events, ev)
	}
	return events
}

// TestEscapePhaseFlagsPrecedence mirrors TestEscapeStreamEnabled for the two
// cycle-phase switches: the flag wins when given, then the YAML value, then the
// built-in default (the phase is on). Only the flag is reported for write-back.
func TestEscapePhaseFlagsPrecedence(t *testing.T) {
	unset := func() {
		optEscapePhases = phaseFlagValues{}
		optPSOPhases = phaseFlagValues{}
	}
	unset()
	t.Cleanup(unset)

	off, on := false, true

	// No flag, no YAML: the phase is on, nothing to write back.
	if v, wb := cleanDLSEnabled(nil, "escape"); !v || wb {
		t.Errorf("default clean_dls = (%v, %v), want (true, false)", v, wb)
	}
	if v, wb := cleanDLSEnabled(nil, "pso"); !v || wb {
		t.Errorf("default clean_dls = (%v, %v), want (true, false)", v, wb)
	}
	// No flag, YAML off: honoured, and still nothing to write back.
	if v, wb := cleanDLSEnabled(&types.GlobalConfig{CleanDLS: &off}, "escape"); v || wb {
		t.Errorf("YAML clean_dls off = (%v, %v), want (false, false)", v, wb)
	}
	// An unset YAML key stays the default even with a section present.
	if v, wb := cleanDLSEnabled(&types.GlobalConfig{}, "escape"); !v || wb {
		t.Errorf("YAML silent = (%v, %v), want (true, false)", v, wb)
	}

	// Flag off overrides a YAML on value and is written back.
	optEscapePhases.cleanDLS, optEscapePhases.cleanDLSSet = "false", true
	if v, wb := cleanDLSEnabled(&types.GlobalConfig{CleanDLS: &on}, "escape"); v || !wb {
		t.Errorf("flag off = (%v, %v), want (false, true)", v, wb)
	}
	// Flag on overrides a YAML off value and is written back.
	optEscapePhases.cleanDLS, optEscapePhases.cleanDLSSet = "true", true
	if v, wb := cleanDLSEnabled(&types.GlobalConfig{CleanDLS: &off}, "escape"); !v || !wb {
		t.Errorf("flag on = (%v, %v), want (true, true)", v, wb)
	}
	// pso reads its own flag pair, not the escape one.
	optEscapePhases = phaseFlagValues{}
	optPSOPhases.cleanDLS, optPSOPhases.cleanDLSSet = "false", true
	if v, wb := cleanDLSEnabled(nil, "pso"); v || !wb {
		t.Errorf("pso flag off = (%v, %v), want (false, true)", v, wb)
	}
	if v, wb := cleanDLSEnabled(nil, "escape"); !v || wb {
		t.Errorf("escape = (%v, %v), want (true, false): an unset flag must not leak", v, wb)
	}
}

// TestApplyCyclePhaseFlagsWriteBack pins the write-back half of the third
// principle: a flag that was given lands in optimization.escape, and an unset
// one leaves the document silent about its phase (so a default run's output is
// byte-for-byte what it was before the switches existed).
func TestApplyCyclePhaseFlagsWriteBack(t *testing.T) {
	unset := func() {
		optEscapePhases = phaseFlagValues{}
		optPSOPhases = phaseFlagValues{}
	}
	unset()
	t.Cleanup(unset)

	parse := func() *types.Input {
		t.Helper()
		in := parseYAML[types.Output]([]byte(phaseFlagInput)).Input
		return &in
	}

	// Nothing given: the phase key is not injected.
	in := parse()
	applyCyclePhaseFlags(&in.Optimization.Escape.GlobalConfig, "escape")
	if in.Optimization.Escape.CleanDLS != nil {
		t.Errorf("no flags: clean_dls=%v, want nil", in.Optimization.Escape.CleanDLS)
	}

	// A flag given lands in the section the run writes back to.
	optEscapePhases.cleanDLS, optEscapePhases.cleanDLSSet = "false", true
	in = parse()
	applyCyclePhaseFlags(&in.Optimization.Escape.GlobalConfig, "escape")
	if in.Optimization.Escape.CleanDLS == nil || *in.Optimization.Escape.CleanDLS {
		t.Errorf("clean_dls = %v, want *false", in.Optimization.Escape.CleanDLS)
	}

	// A pso run writes into optimization.pso and never touches the escape
	// section, even though the document carries one.
	unset()
	optPSOPhases.cleanDLS, optPSOPhases.cleanDLSSet = "false", true
	in = parse()
	applyCyclePhaseFlags(&in.Optimization.PSO.GlobalConfig, "pso")
	if in.Optimization.PSO.CleanDLS == nil || *in.Optimization.PSO.CleanDLS {
		t.Errorf("pso clean_dls = %v, want *false", in.Optimization.PSO.CleanDLS)
	}
	if in.Optimization.Escape.CleanDLS != nil {
		t.Errorf("escape clean_dls = %v, want nil (a pso run must not write there)",
			in.Optimization.Escape.CleanDLS)
	}
}

// TestEscapePhaseFlagsCLI runs the real flag parser end to end for each
// subcommand and checks the three observable effects: the write-back into the
// document, the params block of global_result, and the cycle behaviour the
// switches actually change (the clean phase reporting itself as skipped).
func TestEscapePhaseFlagsCLI(t *testing.T) {
	t.Run("clean dls on for escape", func(t *testing.T) {
		stdout, logText := runEscapePhaseCLI(t, "escape")
		out := parseYAML[types.Output]([]byte(stdout))

		if out.Optimization == nil || out.Optimization.Escape == nil {
			t.Fatal("no optimization section in the output")
		}
		if got := out.Optimization.Escape.CleanDLS; got != nil {
			t.Errorf("optimization.escape.clean_dls = %v, want nil (flag not given)", got)
		}
		if out.GlobalResult == nil {
			t.Fatal("no global_result section")
		}
		if got := out.GlobalResult.Params.CleanDLS; got != nil {
			t.Errorf("params.clean_dls = %v, want nil (still on)", got)
		}
		// The escape run reports its bump geometry; a PSO run never does.
		if out.GlobalResult.Params.HInitial == 0 {
			t.Error("params.h_initial = 0, want the escape geometry reported")
		}

		var accepted int
		for _, ev := range parsePhaseLog(t, logText) {
			if ev.Event == "cycle" && ev.Phase == "clean_dls" && ev.Status == "accepted" {
				accepted++
			}
		}
		if accepted == 0 {
			t.Error("expected accepted clean_dls events on a default escape run")
		}
	})

	t.Run("clean dls off for pso", func(t *testing.T) {
		stdout, logText := runEscapePhaseCLI(t, "pso", "--clean-dls", "false")
		out := parseYAML[types.Output]([]byte(stdout))

		if out.Optimization == nil || out.Optimization.PSO == nil {
			t.Fatal("no optimization.pso section in the output")
		}
		if got := out.Optimization.PSO.CleanDLS; got == nil || *got {
			t.Errorf("optimization.pso.clean_dls = %v, want *false", got)
		}
		if out.GlobalResult == nil {
			t.Fatal("no global_result section")
		}
		if got := out.GlobalResult.Params.CleanDLS; got == nil || *got {
			t.Errorf("params.clean_dls = %v, want *false", got)
		}
		// A pso run reports no escape geometry anywhere: not in its own
		// section and not in the shared params block.
		if got := out.GlobalResult.Params.HInitial; got != 0 {
			t.Errorf("params.h_initial = %v, want 0 (pso has no bump geometry)", got)
		}
		psoRaw, err := yaml.Marshal(out.Optimization.PSO)
		if err != nil {
			t.Fatalf("marshal optimization.pso: %v", err)
		}
		if strings.Contains(string(psoRaw), "h_initial") || strings.Contains(string(psoRaw), "w_initial") {
			t.Error("optimization.pso mentions escape bump geometry")
		}

		events := parsePhaseLog(t, logText)
		var start *phaseLogEvent
		var skipped, acceptedClean int
		for i := range events {
			ev := &events[i]
			switch {
			case ev.Event == "start":
				start = ev
			case ev.Event == "cycle" && ev.Phase == "clean_dls" && ev.Status == "skipped":
				skipped++
			case ev.Event == "cycle" && ev.Phase == "clean_dls" && ev.Status == "accepted":
				acceptedClean++
			}
		}
		if start == nil {
			t.Fatal("no start event in the progress log")
		}
		if start.CleanDLS == nil || *start.CleanDLS {
			t.Errorf("start.clean_dls = %v, want *false", start.CleanDLS)
		}
		if skipped == 0 {
			t.Error("expected phase=clean_dls status=skipped events")
		}
		if acceptedClean != 0 {
			t.Errorf("%d accepted clean_dls events, want 0 (the phase is off)", acceptedClean)
		}
	})
}

// TestDefaultRunReportsNoPhaseKeys guards the "silent by default" rule at the
// document level: a run that never mentions the switch must produce an output
// without it, both in optimization.escape and in global_result.params.
func TestDefaultRunReportsNoPhaseKeys(t *testing.T) {
	stdout, _ := runEscapePhaseCLI(t, "escape")
	out := parseYAML[types.Output]([]byte(stdout))

	if out.Optimization == nil || out.Optimization.Escape == nil {
		t.Fatal("no optimization section in the output")
	}
	if out.Optimization.Escape.CleanDLS != nil {
		t.Errorf("default run wrote clean_dls=%v into optimization.escape, want nil",
			out.Optimization.Escape.CleanDLS)
	}
	if out.GlobalResult == nil {
		t.Fatal("no global_result section")
	}
	if out.GlobalResult.Params.CleanDLS != nil {
		t.Errorf("default params block = clean_dls:%v, want omitted",
			out.GlobalResult.Params.CleanDLS)
	}
	// The document still round-trips through the same YAML the input did.
	raw, err := yaml.Marshal(out)
	if err != nil {
		t.Fatalf("marshal output: %v", err)
	}
	if strings.Contains(string(raw), "clean_dls") {
		t.Error("default run's output mentions a phase switch it was never given")
	}
	// A stale escape_bumps key from an older document is never echoed back.
	if strings.Contains(string(raw), "escape_bumps") {
		t.Error("output echoes the removed escape_bumps key")
	}
}
