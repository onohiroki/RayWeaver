package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hiroki/rayweaver/internal/runctl"
)

// captureStdout runs fn with os.Stdout redirected and returns what it printed.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	fn()
	_ = w.Close()
	os.Stdout = old
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// TestWaitFinishedIsImmediate pins the common case: the run already exited
// (or the pid is stale), so -wait reports and returns at once instead of
// sleeping out its budget.
func TestWaitFinishedIsImmediate(t *testing.T) {
	dir := t.TempDir()
	pidfile := filepath.Join(dir, "done.pid")
	if err := runctl.WritePid(pidfile, 999999); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "done.log")
	if err := os.WriteFile(logPath, []byte("line1\nline2\nfeasible 58\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	var code int
	out := captureStdout(t, func() {
		var err error
		code, err = waitForRun(waitOptions{
			pidfile: pidfile, logPath: logPath,
			timeout: time.Minute, poll: 10 * time.Millisecond, tail: 2,
		})
		if err != nil {
			t.Fatalf("waitForRun: %v", err)
		}
	})
	if code != waitFinished {
		t.Fatalf("code = %d, want %d", code, waitFinished)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("a dead pid took %s; -wait must not sleep", d)
	}
	if !strings.Contains(out, "FINISHED") {
		t.Errorf("report = %q, want FINISHED", out)
	}
	if !strings.Contains(out, "feasible 58") || strings.Contains(out, "line1") {
		t.Errorf("tail = %q, want the last 2 lines only", out)
	}
}

// TestWaitExpiresOnALiveRun pins the timeout outcome: the run is still alive,
// so -wait returns 124 and says so instead of pretending it finished.
func TestWaitExpiresOnALiveRun(t *testing.T) {
	dir := t.TempDir()
	pidfile := filepath.Join(dir, "live.pid")
	if err := runctl.WritePid(pidfile, os.Getpid()); err != nil {
		t.Fatal(err)
	}
	var code int
	out := captureStdout(t, func() {
		var err error
		code, err = waitForRun(waitOptions{
			pidfile: pidfile,
			timeout: 150 * time.Millisecond,
			poll:    10 * time.Millisecond,
		})
		if err != nil {
			t.Fatalf("waitForRun: %v", err)
		}
	})
	if code != waitExpired {
		t.Fatalf("code = %d, want %d", code, waitExpired)
	}
	if !strings.Contains(out, "RUNNING") || !strings.Contains(out, "re-run") {
		t.Errorf("report = %q, want RUNNING and the re-run hint", out)
	}
}

// TestWaitWithoutAPidfileIsItsOwnExitCode keeps "never launched" distinct from
// "still running": a harness must not treat a missing run as a timeout.
func TestWaitWithoutAPidfileIsItsOwnExitCode(t *testing.T) {
	code, err := waitForRun(waitOptions{pidfile: filepath.Join(t.TempDir(), "none.pid"), timeout: time.Second})
	if err == nil {
		t.Fatal("a missing pidfile must report an error")
	}
	if code != waitNoPidfile {
		t.Fatalf("code = %d, want %d", code, waitNoPidfile)
	}
}

// TestWaitJSONParses pins the machine-readable form a script consumes.
func TestWaitJSONParses(t *testing.T) {
	dir := t.TempDir()
	pidfile := filepath.Join(dir, "j.pid")
	if err := runctl.WritePid(pidfile, 999999); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(dir, "j.yaml")
	if err := os.WriteFile(outPath, []byte("metadata: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var code int
	out := captureStdout(t, func() {
		var err error
		code, err = waitForRun(waitOptions{
			pidfile: pidfile, output: outPath, timeout: time.Second, json: true,
		})
		if err != nil {
			t.Fatalf("waitForRun: %v", err)
		}
	})
	if code != waitFinished {
		t.Fatalf("code = %d, want %d", code, waitFinished)
	}
	var rep waitReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("report is not JSON: %v\n%s", err, out)
	}
	if rep.State != "finished" || rep.PID != 999999 {
		t.Errorf("report = %+v, want finished/999999", rep)
	}
	if rep.Output == nil || rep.Output.Bytes == 0 {
		t.Errorf("output artefact = %+v, want the written file's size", rep.Output)
	}
}

// TestTailLines pins the bounded tail: the last n lines, and nothing when the
// file is empty or absent.
func TestTailLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log")
	var b strings.Builder
	for i := 1; i <= 100; i++ {
		b.WriteString("line")
		b.WriteString(strings.Repeat("x", i%3))
		b.WriteString("\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	got := tailLines(path, 3)
	if len(got) != 3 || !strings.HasPrefix(got[2], "line") {
		t.Fatalf("tailLines(3) = %q", got)
	}
	if len(tailLines(path, 0)) != 0 {
		t.Error("tailLines(0) must return nothing")
	}
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if len(tailLines(empty, 3)) != 0 {
		t.Error("an empty log has no tail")
	}
	if len(tailLines(filepath.Join(dir, "missing"), 3)) != 0 {
		t.Error("a missing log has no tail")
	}
}

// TestWaitIsExclusiveWithOtherModes keeps the modes unambiguous.
func TestWaitIsExclusiveWithOtherModes(t *testing.T) {
	for _, argv := range [][]string{
		{"-wait", "-status", "--", "escape"},
		{"-wait", "-stop", "--", "escape"},
		{"-wait", "-fg", "--", "escape"},
	} {
		if err := run(argv); err == nil {
			t.Errorf("run(%q) should reject the mode combination", argv)
		}
	}
}
