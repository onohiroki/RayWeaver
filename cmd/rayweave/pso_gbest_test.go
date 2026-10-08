package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hiroki/rayweaver/internal/pso"
	"github.com/hiroki/rayweaver/internal/types"
)

// The swarm's global best can be ranked on the plain merit or on the
// penalised fitness, and that choice decides which basin a cycle starts its
// DLS from. These tests pin the CLI/YAML precedence for `--gbest`, its
// write-back rule, the rejection of an unknown scale and the scale the run
// reports in its `pso_init` event.

// runGBestCLI runs `rayweave pso` with the given extra arguments on
// phaseFlagInput. When wantFail is set the run is expected to exit non-zero
// and its stderr is returned.
func runGBestCLI(t *testing.T, wantFail bool, args ...string) (stdout, stderr, logText string) {
	t.Helper()
	logFile := filepath.Join(t.TempDir(), "progress.jsonl")
	raw, _ := json.Marshal(append(append([]string{}, args...), "--log", logFile))
	cmd := exec.Command(os.Args[0], "-test.run=^TestEscapePhaseCLIHelper$")
	cmd.Env = append(os.Environ(), "GO_ESCAPE_PHASE_HELPER=1", "GO_ESCAPE_PHASE_ARGS="+string(raw))
	cmd.Stdin = strings.NewReader(phaseFlagInput)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if wantFail {
		if err == nil {
			t.Fatalf("rayweave %v succeeded, want failure", args)
		}
		return out.String(), errBuf.String(), ""
	}
	if err != nil {
		t.Fatalf("rayweave %v failed: %v\nstderr:\n%s", args, err, errBuf.String())
	}
	logBytes, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read progress log: %v", err)
	}
	return out.String(), errBuf.String(), string(logBytes)
}

// psoInitEvent is the subset of a pso_init line these tests read.
type psoInitEvent struct {
	Event          string `json:"event"`
	GbestCriterion string `json:"gbest_criterion"`
}

func TestGBestFlagWriteBack(t *testing.T) {
	// CLI wins and is echoed back into optimization.pso.gbest.
	stdout, _, _ := runGBestCLI(t, false, "pso", "--gbest", "penalised")
	out := parseYAML[types.Output]([]byte(stdout))
	if out.Optimization == nil || out.Optimization.PSO == nil {
		t.Fatal("no optimization.pso section in the output")
	}
	if got := out.Optimization.PSO.Gbest; got != pso.GbestPenalised {
		t.Errorf("optimization.pso.gbest = %q, want %q", got, pso.GbestPenalised)
	}
}

func TestGBestUnsetIsNotWrittenBack(t *testing.T) {
	// An unset flag must not inject the built-in default into the document:
	// the criterion lives in the log, not in every output.
	stdout, _, _ := runGBestCLI(t, false, "pso")
	out := parseYAML[types.Output]([]byte(stdout))
	if out.Optimization == nil || out.Optimization.PSO == nil {
		t.Fatal("no optimization.pso section in the output")
	}
	if got := out.Optimization.PSO.Gbest; got != "" {
		t.Errorf("optimization.pso.gbest = %q, want empty (flag and YAML both unset)", got)
	}
}

func TestGBestUnknownScaleRejected(t *testing.T) {
	// A typo must fail the run rather than silently fall back to the default
	// criterion and produce a run nobody can compare.
	_, stderr, _ := runGBestCLI(t, true, "pso", "--gbest", "raw")
	if !strings.Contains(stderr, "gbest must be") {
		t.Errorf("stderr does not explain the rejected scale:\n%s", stderr)
	}
}

func TestGBestCriterionReportedInLog(t *testing.T) {
	stdout, _, logText := runGBestCLI(t, false, "pso", "--gbest", "penalised")
	if stdout == "" {
		t.Fatal("empty stdout")
	}
	var found bool
	for _, line := range strings.Split(strings.TrimSpace(logText), "\n") {
		if line == "" {
			continue
		}
		var ev psoInitEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("bad JSONL line %q: %v", line, err)
		}
		if ev.Event == "pso_init" {
			found = true
			if ev.GbestCriterion != pso.GbestPenalised {
				t.Errorf("pso_init.gbest_criterion = %q, want %q", ev.GbestCriterion, pso.GbestPenalised)
			}
		}
	}
	if !found {
		t.Error("no pso_init event in the progress log")
	}
}
