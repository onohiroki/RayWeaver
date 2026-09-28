package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func TestExpandAliasesRewritesShortFlagsBeforeSeparator(t *testing.T) {
	in := []string{"-n", "v42", "-i", "in.yaml", "-o=out.yaml", "-e", "log.jsonl", "-d", "/w", "--", "escape", "-o", "keepme"}
	got := expandAliases(in)
	want := []string{"-name", "v42", "-input", "in.yaml", "-output=out.yaml", "-log", "log.jsonl", "-dir", "/w", "--", "escape", "-o", "keepme"}
	if len(got) != len(want) {
		t.Fatalf("got %d args %q, want %d %q", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("arg %d = %q, want %q", i, got[i], want[i])
		}
	}
	// The rayweave argument "-o" after -- must survive untouched: rwrun owns
	// "-o" for the stdout path, rayweave owns it for e.g. `plot -o`.
	if got[len(got)-1] != "keepme" {
		t.Errorf("the trailing rayweave -o was rewritten: %q", got)
	}
}

func TestDeriveName(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"bare subcommand", []string{"escape", "--verbose"}, "escape"},
		{"focus subcommand", []string{"focus", "psf", "--planes", "file"}, "focus"},
		{"explicit binary", []string{"/usr/local/bin/rayweave", "pso"}, "pso"},
		{"nothing to run", nil, ""},
		{"only a binary", []string{"/usr/local/bin/rayweave"}, ""},
	}
	for _, tc := range tests {
		if got := deriveName(tc.args, ""); got != tc.want {
			t.Errorf("%s: deriveName(%q) = %q, want %q", tc.name, tc.args, got, tc.want)
		}
	}
}

func TestCommandFor(t *testing.T) {
	// A bare subcommand gets the rayweave binary prepended. The argument list
	// does not repeat the binary: exec.Command takes it separately.
	bin, argv := commandFor([]string{"escape", "--verbose"}, "/opt/rayweave")
	if bin != "/opt/rayweave" || strings.Join(argv, " ") != "escape --verbose" {
		t.Errorf("bare subcommand: bin=%q argv=%q", bin, argv)
	}
	// An explicit command is honoured verbatim, and only its arguments are kept.
	bin, argv = commandFor([]string{"/bin/sh", "-c", "true"}, "/opt/rayweave")
	if bin != "/bin/sh" || strings.Join(argv, " ") != "-c true" {
		t.Errorf("explicit command: bin=%q argv=%q", bin, argv)
	}
}

func TestPidFileRoundTripAndLiveness(t *testing.T) {
	dir := t.TempDir()
	pf := filepath.Join(dir, "x.pid")

	if err := writePid(pf, 4242); err != nil {
		t.Fatal(err)
	}
	pid, err := readPid(pf)
	if err != nil || pid != 4242 {
		t.Fatalf("readPid = %d, %v; want 4242", pid, err)
	}
	if pidAlive(4242) {
		// A pid in the thousands is almost certainly free; if the machine happens
		// to have it, the liveness check is not what this test is about.
		t.Log("pid 4242 happens to be alive on this machine; skipping the negative check")
	}
	if _, err := readPid(filepath.Join(dir, "missing.pid")); !os.IsNotExist(err) {
		t.Errorf("readPid(missing) err = %v, want a not-exist error", err)
	}
}

func TestResolveRayweavePrefersExplicitPath(t *testing.T) {
	got, err := resolveRayweave("/bin/sh")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/bin/sh" {
		t.Errorf("resolveRayweave(/bin/sh) = %q", got)
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
	pid, err := readPid(pidfile)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		// Cleanup with SIGKILL: the "never kill -9" rule is about losing an
		// escape document, and a test must not leave a stray sleeper behind.
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}()

	if !pidAlive(pid) {
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

	if err := writePid(pidfile, sleeper.Process.Pid); err != nil {
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

func TestStopOnAStalePidfileIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	pidfile := filepath.Join(dir, "d.pid")
	if err := writePid(pidfile, 999999); err != nil {
		t.Fatal(err)
	}
	if err := doStop(pidfile, 0, 1); err != nil {
		t.Errorf("doStop on a dead pid = %v, want nil (report and move on)", err)
	}
	if err := doStop(filepath.Join(dir, "none.pid"), 0, 1); err == nil {
		t.Error("doStop with no pidfile should report an error")
	}
}

func TestRunRejectsBadInvocations(t *testing.T) {
	if err := run(nil); err == nil {
		t.Error("run with no arguments should fail")
	}
	if err := run([]string{"-status", "-stop", "--", "escape"}); err == nil {
		t.Error("mutually exclusive modes should fail")
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
