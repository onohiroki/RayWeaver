package mcpsrv

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Call is one synchronous rayweave invocation: the subcommand, its flags, and
// the pipeline document for stdin.
type Call struct {
	Command string
	Args    []string
	Stdin   []byte
	// Progress, when non-nil, receives the parsed stderr stream. It is only
	// set when the client asked for progress notifications.
	Progress func(ProgressEvent)
}

// ProgressEvent is one coarse update derived from a diagnostics line.
type ProgressEvent struct {
	Message  string
	Progress float64
}

// RunResult is the outcome of a synchronous run. stdout and stderr are kept
// apart for the same reason the CLI never merges them: stdout is the pipeline
// document, stderr is diagnostics.
type RunResult struct {
	Stdout    []byte
	Stderr    []byte
	Warnings  []string
	Logs      []string
	ExitCode  int
	Elapsed   time.Duration
	Cancelled bool
}

// Runner invokes the rayweave binary. It never SIGKILLs: a cancelled run is
// asked to stop with the two-stage SIGTERM sequence the optimisers expect, so
// the document it was assembling still reaches stdout.
type Runner struct {
	binary string
	dir    string
	grace  time.Duration
	wait   time.Duration
}

var taggedLine = regexp.MustCompile(`^rayweave(?:\[[a-zA-Z]+\])?: (.*)$`)

// Run executes one subcommand and waits for it.
func (r *Runner) Run(ctx context.Context, c Call) (RunResult, error) {
	res := RunResult{}
	args := append([]string{c.Command}, c.Args...)
	cmd := exec.Command(r.binary, args...)
	cmd.Dir = r.dir
	cmd.Stdin = bytes.NewReader(c.Stdin)
	stdout := &lockedBuffer{}
	cmd.Stdout = stdout
	errPipe, err := cmd.StderrPipe()
	if err != nil {
		return res, err
	}
	sink := &lineSink{maxLogs: 200}

	started := time.Now()
	if err := cmd.Start(); err != nil {
		return res, fmt.Errorf("start rayweave: %w", err)
	}
	pid := cmd.Process.Pid

	progress := c.Progress
	var lastProgress time.Time
	var seq float64
	report := func(line string) {
		if progress == nil {
			return
		}
		msg, ok := progressMessage(line)
		if !ok || time.Since(lastProgress) < time.Second {
			return
		}
		lastProgress = time.Now()
		seq++
		progress(ProgressEvent{Message: msg, Progress: seq})
	}

	scanDone := make(chan struct{})
	go func() {
		defer close(scanDone)
		scanStderr(errPipe, sink, report)
	}()

	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	var waitErr error
	select {
	case waitErr = <-waitCh:
	case <-ctx.Done():
		res.Cancelled = true
		// First SIGTERM: let the running DLS finish. Second: abort it within one
		// iteration, which still leaves the document on stdout.
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case waitErr = <-waitCh:
		case <-time.After(r.grace):
			_ = cmd.Process.Signal(syscall.SIGTERM)
			select {
			case waitErr = <-waitCh:
			case <-time.After(r.wait):
				res.Elapsed = time.Since(started)
				res.Stdout = stdout.Bytes()
				res.Stderr = sink.stderr()
				res.Warnings = sink.warnings()
				res.Logs = sink.logs()
				return res, fmt.Errorf("cancelled: rayweave %s (pid %d) is still finishing its DLS and was not killed; it writes its document when it exits", c.Command, pid)
			}
		}
	}
	<-scanDone

	res.Elapsed = time.Since(started)
	res.Stdout = stdout.Bytes()
	res.Stderr = sink.stderr()
	res.Warnings = sink.warnings()
	res.Logs = sink.logs()
	if waitErr != nil {
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			res.ExitCode = ee.ExitCode()
		} else {
			return res, waitErr
		}
	}
	return res, nil
}

// scanStderr reads the diagnostics stream line by line, classifying each line
// and forwarding it to the progress reporter.
func scanStderr(r io.Reader, sink *lineSink, report func(string)) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for sc.Scan() {
		line := sc.Text()
		sink.add(line)
		if report != nil {
			report(line)
		}
	}
}

// progressMessage turns a diagnostics line into a short progress message. The
// compute commands write compact JSONL (`--verbose`), so the interesting
// numbers are lifted out of the event.
func progressMessage(line string) (string, bool) {
	body := line
	if m := taggedLine.FindStringSubmatch(line); m != nil {
		body = m[1]
	}
	if strings.HasPrefix(strings.TrimSpace(body), "{") {
		var ev map[string]any
		if json.Unmarshal([]byte(body), &ev) == nil {
			if msg := eventMessage(ev); msg != "" {
				return msg, true
			}
		}
	}
	if strings.HasPrefix(body, "Warning:") {
		return "", false
	}
	return body, body != ""
}

func eventMessage(ev map[string]any) string {
	num := func(k string) (float64, bool) {
		v, ok := ev[k].(float64)
		return v, ok
	}
	switch {
	case ev["iter"] != nil:
		it, _ := num("iter")
		m, ok := num("merit")
		if ok {
			return fmt.Sprintf("iteration %.0f, merit %.6g", it, m)
		}
		return fmt.Sprintf("iteration %.0f", it)
	case ev["cycle"] != nil:
		cy, _ := num("cycle")
		if m, ok := num("best_merit"); ok {
			return fmt.Sprintf("cycle %.0f, best merit %.6g", cy, m)
		}
		if m, ok := num("merit"); ok {
			return fmt.Sprintf("cycle %.0f, merit %.6g", cy, m)
		}
		return fmt.Sprintf("cycle %.0f", cy)
	case ev["event"] != nil:
		return fmt.Sprintf("%v", ev["event"])
	}
	return ""
}

// lineSink classifies and retains diagnostics. Warnings are kept in full; the
// raw log is a bounded ring because a verbose stream can be large.
type lineSink struct {
	mu       sync.Mutex
	warns    []string
	logLines []string
	maxLogs  int
}

func (s *lineSink) add(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	body := line
	if m := taggedLine.FindStringSubmatch(line); m != nil {
		body = m[1]
	}
	if strings.HasPrefix(body, "Warning:") {
		s.warns = append(s.warns, strings.TrimSpace(strings.TrimPrefix(body, "Warning:")))
	} else {
		s.logLines = append(s.logLines, body)
		if s.maxLogs > 0 && len(s.logLines) > s.maxLogs {
			s.logLines = s.logLines[len(s.logLines)-s.maxLogs:]
		}
	}
}

func (s *lineSink) warnings() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.warns...)
}

func (s *lineSink) logs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.logLines...)
}

// stderr reconstructs the captured diagnostics for the error message. It is the
// joined log ring plus the warnings, which is what a human sees on a terminal.
func (s *lineSink) stderr() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b strings.Builder
	for _, l := range s.logLines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	for _, w := range s.warns {
		b.WriteString("Warning: ")
		b.WriteString(w)
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// lockedBuffer lets the child's stdout be read while the copy goroutine may
// still be running (only happens after a cancelled run that outlived its wait).
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) Bytes() []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]byte(nil), l.b.Bytes()...)
}
