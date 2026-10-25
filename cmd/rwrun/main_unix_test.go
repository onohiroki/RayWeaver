//go:build !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/hiroki/rayweaver/internal/runctl"
)

func TestResolveRayweavePrefersExplicitPath(t *testing.T) {
	got, err := runctl.Resolve("/bin/sh")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/bin/sh" {
		t.Errorf("Resolve(/bin/sh) = %q", got)
	}
}

// TestDetachedChildIsInItsOwnSession is the regression this whole tool exists
// for: a caller-side process-group teardown must not reach the run. The child has
// to end up in its own session and process group (pgid == pid) rather than the
// launcher's group.
//
// Note: the child is reparented to init only once rwrun itself exits, so ppid
// cannot be asserted here (the test binary is still its parent) - the process
// group is the property that decides whether a group-directed SIGTERM lands.
func TestDetachedChildIsInItsOwnSession(t *testing.T) {
	dir := t.TempDir()
	pidfile := filepath.Join(dir, "t.pid")
	out := filepath.Join(dir, "t.out")
	logf := filepath.Join(dir, "t.stderr")

	if err := runDetached("/bin/sleep", []string{"300"}, dir, "", out, logf, pidfile, "t"); err != nil {
		t.Fatal(err)
	}
	pid, err := runctl.ReadPid(pidfile)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		// Cleanup with SIGKILL: the "never kill -9" rule is about losing an
		// escape document, and a test must not leave a stray sleeper behind.
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}()

	if !runctl.PidAlive(pid) {
		t.Fatalf("pid %d is not alive right after the launch", pid)
	}
	pgid := procField(t, pid, "pgid")
	if pgid != strconv.Itoa(pid) {
		t.Errorf("child pgid = %s, want %d (setsid must make it its own group leader)", pgid, pid)
	}
	if pgid == strconv.Itoa(os.Getpid()) {
		t.Errorf("child is in the launcher's process group %s - a group teardown would kill it", pgid)
	}
	// The redirections must be in place: stdout and stderr are separate files.
	for _, p := range []string{out, logf} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s to exist: %v", p, err)
		}
	}
}

func TestStopRefusesARecycledPid(t *testing.T) {
	dir := t.TempDir()
	pidfile := filepath.Join(dir, "s.pid")

	// A live process that is not a rayweave run: the pidfile is stale and the
	// pid may have been recycled, so -stop must not signal it.
	sleeper := exec.Command("/bin/sleep", "300")
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sleeper.Process.Kill(); _, _ = sleeper.Process.Wait() }()

	if err := runctl.WritePid(pidfile, sleeper.Process.Pid); err != nil {
		t.Fatal(err)
	}
	err := doStop(pidfile, 0, 1)
	if err == nil {
		t.Fatal("doStop accepted a non-rayweave pid; a recycled pid must never be signalled")
	}
	if !strings.Contains(err.Error(), "stale") {
		t.Errorf("error = %v, want it to mention a stale pidfile", err)
	}
	if err := sleeper.Process.Signal(syscall.Signal(0)); err != nil {
		t.Errorf("the unrelated process was killed: %v", err)
	}
}

// procField returns one whitespace-stripped field of `ps -o FIELD= -p pid`.
func procField(t *testing.T, pid int, field string) string {
	t.Helper()
	out, err := exec.Command("ps", "-o", field+"=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		t.Fatalf("ps -o %s -p %d: %v", field, pid, err)
	}
	return strings.TrimSpace(string(out))
}
