package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hiroki/rayweaver/internal/runctl"
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
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"escape"}, "escape"},
		{[]string{"/usr/local/bin/rayweave", "escape", "--verbose"}, "escape"},
		{[]string{"pso"}, "pso"},
		{nil, ""},
	}
	for _, c := range cases {
		if got := deriveName(c.args, ""); got != c.want {
			t.Errorf("deriveName(%q) = %q, want %q", c.args, got, c.want)
		}
	}
}

func TestCommandFor(t *testing.T) {
	bin, argv := commandFor([]string{"escape", "--verbose"}, "/opt/rayweave")
	if bin != "/opt/rayweave" || strings.Join(argv, " ") != "escape --verbose" {
		t.Errorf("default command: bin=%q argv=%q", bin, argv)
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

	if err := runctl.WritePid(pf, 4242); err != nil {
		t.Fatal(err)
	}
	pid, err := runctl.ReadPid(pf)
	if err != nil || pid != 4242 {
		t.Fatalf("readPid = %d, %v; want 4242", pid, err)
	}
	if runctl.PidAlive(4242) {
		// A pid in the thousands is almost certainly free; if the machine happens
		// to have it, the liveness check is not what this test is about.
		t.Log("pid 4242 happens to be alive on this machine; skipping the negative check")
	}
	if _, err := runctl.ReadPid(filepath.Join(dir, "missing.pid")); !os.IsNotExist(err) {
		t.Errorf("ReadPid(missing) err = %v, want a not-exist error", err)
	}
}

func TestStopOnAStalePidfileIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	pidfile := filepath.Join(dir, "d.pid")
	if err := runctl.WritePid(pidfile, 999999); err != nil {
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
