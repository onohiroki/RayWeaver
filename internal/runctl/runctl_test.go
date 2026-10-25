package runctl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStopFilePath(t *testing.T) {
	if got, want := StopFilePath("v42.pid"), "v42.pid.stop"; got != want {
		t.Errorf("StopFilePath = %q, want %q", got, want)
	}
}

func TestPidFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.pid")
	if err := WritePid(path, 4242); err != nil {
		t.Fatal(err)
	}
	pid, err := ReadPid(path)
	if err != nil {
		t.Fatal(err)
	}
	if pid != 4242 {
		t.Errorf("ReadPid = %d, want 4242", pid)
	}
}

func TestStopMissingPidFile(t *testing.T) {
	_, err := Stop(StopOptions{PidFile: filepath.Join(t.TempDir(), "missing.pid")})
	if err == nil {
		t.Fatal("Stop with a missing pidfile returned no error")
	}
}

func TestStopStalePidIsNotAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.pid")
	// A pid no process can have: the kernel recycles pids into a much smaller
	// range, and PidAlive treats only an existing process as alive.
	if err := WritePid(path, 1<<30); err != nil {
		t.Fatal(err)
	}
	var reports []string
	res, err := Stop(StopOptions{
		PidFile: path,
		Report:  func(format string, args ...any) { reports = append(reports, format) },
	})
	if err != nil {
		t.Fatalf("Stop on a stale pidfile: %v", err)
	}
	if res.Exited || res.Signalled {
		t.Errorf("stale pid reported as stopped/exited: %+v", res)
	}
	if len(reports) == 0 {
		t.Error("no report for the stale pid")
	}
}

func TestLaunchNeedsAStderrPath(t *testing.T) {
	dir := t.TempDir()
	_, err := Launch(LaunchOptions{Binary: os.Args[0], Dir: dir, Stderr: filepath.Join(dir, "no", "such", "dir", "err.log")})
	if err == nil {
		t.Fatal("Launch with an unusable stderr path returned no error")
	}
}

func TestStopUsesTheDefaultGraceAndWait(t *testing.T) {
	// The defaults are inside Stop; this only asserts they are short enough to
	// be usable in a test's lifetime when the pid is already gone.
	start := time.Now()
	path := filepath.Join(t.TempDir(), "run.pid")
	if err := WritePid(path, 1<<30); err != nil {
		t.Fatal(err)
	}
	if _, err := Stop(StopOptions{PidFile: path}); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("stale-pid Stop took %s; it should not wait", elapsed)
	}
}

func TestCreateStopFileTruncates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "stop")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := createStopFile(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Errorf("stop file was not truncated: %q", data)
	}
}

func TestEnvWithStopFileReplacesAnInheritedValue(t *testing.T) {
	t.Setenv("RAYWEAVE_STOP_FILE", "old")
	env := EnvWithStopFile("/new/stop")
	var values []string
	for _, e := range env {
		if strings.HasPrefix(e, "RAYWEAVE_STOP_FILE=") {
			values = append(values, e)
		}
	}
	if len(values) != 1 || values[0] != "RAYWEAVE_STOP_FILE=/new/stop" {
		t.Errorf("stop-file entries = %v, want exactly the new value", values)
	}
}
