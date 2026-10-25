package mcpsrv

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestStoreContentAddressed(t *testing.T) {
	ws := t.TempDir()
	st := &Store{root: ws, docs: ws + "/documents", maxInline: 1024}
	if err := os.MkdirAll(st.docs, 0o755); err != nil {
		t.Fatal(err)
	}
	a, err := st.Put("paraxial", []byte("a: 1\n"), "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.Put("paraxial", []byte("a: 1\n"), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != b.ID {
		t.Errorf("same bytes produced %s and %s", a.ID, b.ID)
	}
	list, err := st.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Errorf("index has %d entries, want 1 (content-addressed writes append once)", len(list))
	}
	if _, data, err := st.Get(a.ID); err != nil || string(data) != "a: 1\n" {
		t.Errorf("Get = %q, %v", data, err)
	}
}

func TestStoreResolveRejects(t *testing.T) {
	ws := t.TempDir()
	st := &Store{root: ws, docs: ws + "/documents", maxInline: 8}
	if err := os.MkdirAll(st.docs, 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		in   DocInput
		want string
	}{
		{"none", DocInput{}, "no input document"},
		{"two", DocInput{Document: "a: 1", DocumentID: "0123456789ab"}, "exactly one"},
		{"too-big", DocInput{Document: "0123456789"}, "over the 8 byte limit"},
		{"escape", DocInput{Path: "../../etc/passwd"}, "outside the MCP workspace"},
		{"bad-id", DocInput{DocumentID: "../x"}, "invalid document id"},
	}
	for _, tc := range cases {
		_, _, err := st.Resolve(tc.in)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
	}
}

func TestRunnerSeparatesStreams(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test drives /bin/sh")
	}
	r := &Runner{binary: "/bin/sh", dir: t.TempDir(), grace: time.Second, wait: 5 * time.Second}
	res, err := r.Run(context.Background(), Call{
		Command: "-c",
		Args:    []string{`printf 'the document\n'; printf 'rayweave[test]: Warning: careful\n' >&2; printf 'rayweave[test]: info line\n' >&2; exit 3`},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 3 {
		t.Errorf("exit code = %d, want 3", res.ExitCode)
	}
	if string(res.Stdout) != "the document\n" {
		t.Errorf("stdout = %q", res.Stdout)
	}
	if len(res.Warnings) != 1 || res.Warnings[0] != "careful" {
		t.Errorf("warnings = %v", res.Warnings)
	}
	if len(res.Logs) != 1 || res.Logs[0] != "info line" {
		t.Errorf("logs = %v", res.Logs)
	}
}

func TestRunnerCancellationUsesSIGTERM(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test drives /bin/sh; Windows cancellation is covered by the build")
	}
	r := &Runner{binary: "/bin/sh", dir: t.TempDir(), grace: 200 * time.Millisecond, wait: 5 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	res, err := r.Run(ctx, Call{Command: "-c", Args: []string{"sleep 30"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Cancelled {
		t.Error("the run is not marked cancelled")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("cancellation took %s; the child was not stopped by SIGTERM", elapsed)
	}
	if res.ExitCode == 0 {
		t.Error("a SIGTERM'd child should exit non-zero")
	}
}
