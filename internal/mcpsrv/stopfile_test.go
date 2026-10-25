package mcpsrv

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hiroki/rayweaver/internal/runctl"
)

// TestStopFileStopsEscapeGracefully drives the CLI directly with the control
// file a Windows launcher would set. The watcher, the staged stop and the
// document write are platform-neutral, so this runs on every platform; only the
// launcher side (writing the file) is Windows-specific.
func TestStopFileStopsEscapeGracefully(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "in.yaml")
	if err := os.WriteFile(input, []byte(shortEscape(t, "1000")), 0o644); err != nil {
		t.Fatal(err)
	}
	stopFile := filepath.Join(dir, "stop")
	if err := os.WriteFile(stopFile, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(rayweaveBinary(t), "escape")
	cmd.Env = runctl.EnvWithStopFile(stopFile)
	stdin, err := os.Open(input)
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})

	time.Sleep(time.Second)
	if err := os.WriteFile(stopFile, []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("escape exited with %v\nstderr:\n%s", err, stderr.String())
		}
	case <-time.After(90 * time.Second):
		t.Fatalf("escape did not finish after the stage-1 stop request\nstderr:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "interrupted: true") {
		t.Errorf("the stopped document does not say interrupted: true\n%s", firstN(stdout.String(), 500))
	}
}
