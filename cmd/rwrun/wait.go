package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hiroki/rayweaver/internal/runctl"
)

// The three outcomes of -wait. They are distinct exit codes because a caller
// that bills per turn has to tell "finished" from "still running" without
// parsing the text: 124 means the wait expired, not that the run failed.
const (
	waitFinished  = 0
	waitNoPidfile = 2
	waitExpired   = 124
)

// waitOptions is one -wait invocation.
type waitOptions struct {
	pidfile string
	output  string
	logPath string
	timeout time.Duration
	poll    time.Duration
	tail    int
	json    bool
}

// waitForRun blocks until the recorded pid exits or the timeout elapses, then
// reports the outcome in one result. It exists because a harness bills per
// turn: polling a long run costs a turn per check, while one blocking call
// costs one turn for the whole wait. The run itself is never signalled - that
// is -stop's job - so this only observes.
func waitForRun(o waitOptions) (int, error) {
	pid, err := runctl.ReadPid(o.pidfile)
	if err != nil {
		if os.IsNotExist(err) {
			return waitNoPidfile, fmt.Errorf("no pidfile %s (never launched, or already purged)", runctl.AbsPath(o.pidfile))
		}
		return 1, err
	}
	start := launchTime(o.pidfile)
	var deadline time.Time
	if o.timeout > 0 {
		deadline = time.Now().Add(o.timeout)
	}
	poll := o.poll
	if poll <= 0 {
		poll = 2 * time.Second
	}
	// A run whose pid is already gone is reported as finished at once: the
	// caller may have missed the exit (an evicted harness, a rebooted shell),
	// and waiting for it again would be a lie.
	for runctl.PidAlive(pid) {
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			break
		}
		time.Sleep(poll)
	}
	alive := runctl.PidAlive(pid)
	code := waitFinished
	if alive {
		code = waitExpired
	}
	printWaitReport(o, pid, alive, start)
	return code, nil
}

// launchTime is when the run was recorded. The pidfile is written once, right
// after the child starts, so its mtime is the closest start time that survives
// the child's exit (the child is reparented to init and its exit status is
// never collected).
func launchTime(pidfile string) time.Time {
	if st, err := os.Stat(pidfile); err == nil {
		return st.ModTime()
	}
	return time.Now()
}

// waitArtefact is one output file as -wait reports it.
type waitArtefact struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
	MTime string `json:"mtime,omitempty"`
}

// waitReport is the machine-readable form (-json).
type waitReport struct {
	State    string `json:"state"`
	PID      int    `json:"pid"`
	Elapsed  string `json:"elapsed"`
	ElapsedS int64  `json:"elapsed_s"`
	// ExitKnown is false for every detached run: the child is reparented and
	// its exit status is never collected, so a caller must judge the outcome
	// from the document and the log.
	ExitKnown bool          `json:"exit_known"`
	Output    *waitArtefact `json:"output,omitempty"`
	Log       *waitArtefact `json:"log,omitempty"`
	Tail      []string      `json:"tail,omitempty"`
}

func printWaitReport(o waitOptions, pid int, alive bool, start time.Time) {
	elapsed := time.Since(start)
	if elapsed < 0 {
		elapsed = 0
	}
	elapsed = elapsed.Round(time.Second)
	tail := tailLines(o.logPath, o.tail)

	if o.json {
		rep := waitReport{
			State:     "finished",
			PID:       pid,
			Elapsed:   elapsed.String(),
			ElapsedS:  int64(elapsed.Seconds()),
			ExitKnown: false,
			Output:    artefactOf(o.output),
			Log:       artefactOf(o.logPath),
			Tail:      tail,
		}
		if alive {
			rep.State = "running"
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(rep)
		return
	}

	if alive {
		fmt.Printf("state   : RUNNING  (still running after %s)\n", elapsed)
	} else {
		fmt.Printf("state   : FINISHED  (pid %d no longer running)\n", pid)
	}
	fmt.Printf("elapsed : %s\n", elapsed)
	fmt.Printf("exit    : not recorded (the run is detached; a non-empty stdout means the document was written)\n")
	for _, p := range []struct{ label, path string }{{"output", o.output}, {"log", o.logPath}} {
		if p.path == "" {
			continue
		}
		if st, err := os.Stat(p.path); err == nil {
			fmt.Printf("%-7s : %s  %d bytes  %s\n", p.label, runctl.AbsPath(p.path), st.Size(), st.ModTime().Format("15:04:05"))
		} else {
			fmt.Printf("%-7s : %s  (not created yet)\n", p.label, runctl.AbsPath(p.path))
		}
	}
	if len(tail) > 0 {
		fmt.Printf("--- log tail (%d lines) ---\n", len(tail))
		for _, line := range tail {
			fmt.Println(line)
		}
	}
	if alive {
		fmt.Printf("hint    : re-run `rwrun -wait` to keep waiting (exit %d)\n", waitExpired)
	}
}

// artefactOf describes an output file for the JSON report.
func artefactOf(path string) *waitArtefact {
	if path == "" {
		return nil
	}
	st, err := os.Stat(path)
	if err != nil {
		return &waitArtefact{Path: runctl.AbsPath(path)}
	}
	return &waitArtefact{
		Path:  runctl.AbsPath(path),
		Bytes: st.Size(),
		MTime: st.ModTime().Format(time.RFC3339),
	}
}

// tailLines returns the last n lines of a file without reading all of it: a
// verbose run's stderr log grows to hundreds of megabytes, and -wait must stay
// cheap. At most 1 MiB of the tail is read, which is far more than n lines.
func tailLines(path string, n int) []string {
	if n <= 0 || path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() == 0 {
		return nil
	}
	const (
		chunk = 32 * 1024
		max   = 1 << 20
	)
	size := st.Size()
	read := int64(0)
	var buf []byte
	for read < size && int64(len(buf)) < max {
		want := int64(chunk)
		if size-read < want {
			want = size - read
		}
		read += want
		part := make([]byte, want)
		if _, err := f.ReadAt(part, size-read); err != nil {
			return nil
		}
		buf = append(part, buf...)
		if strings.Count(string(buf), "\n") > n {
			break
		}
	}
	lines := strings.Split(strings.TrimRight(string(buf), "\r\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// purgeArtifacts removes the pidfile and the Windows stop file after a wait.
func purgeArtifacts(pidfile string) {
	_ = os.Remove(pidfile)
	_ = os.Remove(runctl.StopFilePath(pidfile))
	fmt.Printf("removed %s\n", filepath.Clean(pidfile))
}
