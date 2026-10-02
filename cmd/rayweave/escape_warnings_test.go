package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hiroki/rayweaver/internal/escape"
	"github.com/hiroki/rayweaver/internal/glass"
	"github.com/hiroki/rayweaver/internal/optimize"
	"github.com/hiroki/rayweaver/internal/types"
)

// captureStderr runs fn with os.Stderr swapped for a pipe and returns what was
// written to it — the same trick as captureStdout, for the warnings that keep
// their plain tagged form.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	oldErr := os.Stderr
	rErr, wErr, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = wErr

	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		io.Copy(&buf, rErr)
	}()

	fn()

	wErr.Close()
	os.Stderr = oldErr
	<-done
	rErr.Close()
	return buf.String()
}

// The optimizer's and the glass package's Warnf sinks default to a tagged
// plain-text line on stderr — the same stream the compact --verbose JSONL
// uses. Routed, a warning becomes a structured "warn" event on every
// registered stream, so `--log` records it too; the tagged line is kept only
// when stderr is *not* itself a JSONL stream, otherwise a captured stream
// mixes human-readable lines with JSON and no consumer can parse it
// (`list escape`, jq and query --jsonl all reject the mixed input).
func TestRouteWarningsToProgress(t *testing.T) {
	prevOptWarn, prevGlassWarn := optimize.Warnf, glass.Warnf
	t.Cleanup(func() { optimize.Warnf, glass.Warnf = prevOptWarn, prevGlassWarn })

	for _, tc := range []struct {
		name           string
		streamOnStderr bool
		wantPlainLine  bool
	}{
		// --verbose: stderr carries the stream, so nothing may be written to
		// it in plain text.
		{"verbose", true, false},
		// --log alone: stderr is not a stream, the warnings must stay readable
		// there instead of going silent.
		{"log without verbose", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var optCalled, glassCalled bool
			optimize.Warnf = func(string, ...any) { optCalled = true }
			glass.Warnf = func(string, ...any) { glassCalled = true }
			t.Cleanup(func() { optimize.Warnf, glass.Warnf = prevOptWarn, prevGlassWarn })

			var stream bytes.Buffer
			p := escape.NewProgress()
			if tc.streamOnStderr {
				p.AddCompactWriter(&stream) // the --verbose stderr stream
			} else {
				p.AddWriter(&stream) // the --log file
			}
			restore := routeWarningsToProgress(p, tc.streamOnStderr)

			plain := captureStderr(t, func() {
				optimize.Warnf("back_focus: %d of %d field(s) dropped", 1, 5)
				glass.Warnf("cannot read AGF file %s", "SCHOTT.agf")
			})
			if optCalled || glassCalled {
				t.Fatal("routeWarningsToProgress left a plain-text sink installed")
			}

			lines := strings.Split(strings.TrimSpace(stream.String()), "\n")
			if len(lines) != 2 {
				t.Fatalf("stream lines = %d, want 2:\n%s", len(lines), stream.String())
			}
			for i, want := range []string{
				"back_focus: 1 of 5 field(s) dropped",
				"cannot read AGF file SCHOTT.agf",
			} {
				var ev struct {
					Event   string `json:"event"`
					Message string `json:"message"`
				}
				if err := json.Unmarshal([]byte(lines[i]), &ev); err != nil {
					t.Fatalf("line %d is not JSON: %v (%s)", i, err, lines[i])
				}
				if ev.Event != "warn" {
					t.Errorf("line %d event = %q, want warn", i, ev.Event)
				}
				if ev.Message != want {
					t.Errorf("line %d message = %q, want %q", i, ev.Message, want)
				}
			}

			for _, want := range []string{
				"back_focus: 1 of 5 field(s) dropped",
				"cannot read AGF file SCHOTT.agf",
			} {
				if strings.Contains(plain, want) != tc.wantPlainLine {
					t.Errorf("plain stderr line %q present = %v, want %v (%q)",
						want, strings.Contains(plain, want), tc.wantPlainLine, plain)
				}
			}

			// Restore reinstates the plain-text sinks.
			restore()
			optCalled, glassCalled = false, false
			optimize.Warnf("plain again")
			glass.Warnf("plain again")
			if !optCalled || !glassCalled {
				t.Error("restore did not reinstate the previous Warnf sinks")
			}
		})
	}
}

// loadCatalogs installs the default tagged stderr sink — but only when the
// sinks are not already owned by a progress stream. escape/pso create their
// reporter before loading the catalog so an AGF warning lands in the JSONL
// stream; without the guard this very call would put the plain-text sink back,
// dropping that load warning out of the capture and bypassing the stream for
// the rest of the run.
func TestLoadCatalogsKeepsRoutedWarnSinks(t *testing.T) {
	prevOptWarn, prevGlassWarn := optimize.Warnf, glass.Warnf
	t.Cleanup(func() {
		optimize.Warnf, glass.Warnf = prevOptWarn, prevGlassWarn
		warnSinksRouted = false
	})

	var optCalled, glassCalled bool
	optimize.Warnf = func(string, ...any) { optCalled = true }
	glass.Warnf = func(string, ...any) { glassCalled = true }

	logPath := filepath.Join(t.TempDir(), "run.jsonl")
	_, finish := newEscapeProgress(false, logPath)
	t.Cleanup(finish)
	if !warnSinksRouted {
		t.Fatal("newEscapeProgress with --log must mark the Warnf sinks as routed")
	}

	loadCatalogs(&types.Input{})

	plain := captureStderr(t, func() {
		optCalled, glassCalled = false, false
		optimize.Warnf("back_focus: %d of %d field(s) dropped", 1, 5)
		glass.Warnf("cannot read AGF file %s", "SCHOTT.agf")
	})
	if optCalled || glassCalled {
		t.Fatal("loadCatalogs replaced a progress-routed Warnf sink with the plain-text one")
	}
	// stderr carries no stream here (--verbose off), so the tagged line stays.
	for _, want := range []string{
		"back_focus: 1 of 5 field(s) dropped",
		"cannot read AGF file SCHOTT.agf",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("plain stderr missing %q: %q", want, plain)
		}
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read the progress stream: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, `"event":"warn"`) {
		t.Errorf("stream carries no warn event:\n%s", got)
	}
	for _, want := range []string{
		"back_focus: 1 of 5 field(s) dropped",
		"cannot read AGF file SCHOTT.agf",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("stream missing %q:\n%s", want, got)
		}
	}

	finish()
	if warnSinksRouted {
		t.Error("finish did not release the Warnf sinks")
	}
}
