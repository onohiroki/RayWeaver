// Package runctl launches long rayweave runs detached from the caller's process
// group and stops them with the two-stage SIGTERM sequence the optimisers
// expect.
//
// It exists so the two front-ends that need this behaviour — the `rwrun`
// companion binary and the MCP server — share one implementation of the rules
// that are easy to get wrong: a run must be started in its own session (so a
// shell or agent harness releasing its workspace cannot signal it), a stop is
// always SIGTERM twice (never SIGKILL, which always loses the assembled
// document), and a recycled pid is never signalled.
package runctl

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Resolve locates the rayweave binary. Order: the explicit path, $RAYWEAVE,
// ./rayweave, $PATH, and only then the directory holding the calling executable
// (and its parent).
//
// "Next to the executable" is last on purpose: when the calling binary is built
// somewhere unrelated (a scratch /tmp, say) that directory can hold a
// completely unrelated and badly outdated `rayweave`. The resolved path is
// always reported by the caller, and the explicit path / $RAYWEAVE override the
// search.
func Resolve(explicit string) (string, error) {
	var cands []string
	if explicit != "" {
		cands = append(cands, explicit)
	} else {
		if env := os.Getenv("RAYWEAVE"); env != "" {
			cands = append(cands, env)
		}
		// "./rayweave" explicitly: LookPath on a bare name searches $PATH, which
		// does not normally include the working directory, so a freshly built
		// ./rayweave would be shadowed by an older copy elsewhere on $PATH.
		cands = append(cands, "./rayweave")
		if p, err := exec.LookPath("rayweave"); err == nil {
			cands = append(cands, p)
		}
		if exe, err := os.Executable(); err == nil {
			d := filepath.Dir(exe)
			cands = append(cands, filepath.Join(d, "rayweave"), filepath.Join(d, "..", "rayweave"))
		}
	}
	seen := map[string]bool{}
	for _, c := range cands {
		if seen[c] {
			continue
		}
		seen[c] = true
		if p, err := exec.LookPath(c); err == nil {
			return AbsPath(p), nil
		}
	}
	return "", fmt.Errorf("rayweave not found (tried %s; pass -rayweave PATH or set $RAYWEAVE)", strings.Join(cands, ", "))
}

// LaunchOptions describes one detached run. Empty Stdin means /dev/null; Stdout
// and Stderr are always separate files — the pipeline document and the
// diagnostics are never merged.
type LaunchOptions struct {
	Binary string
	Args   []string
	Dir    string
	Stdin  string
	Stdout string
	Stderr string
}

// Launch starts the command in its own session and returns its pid without
// waiting. The child is reparented to init, so nothing in the caller's process
// group or process tree can signal it as a side effect.
func Launch(o LaunchOptions) (int, error) {
	cmd := exec.Command(o.Binary, o.Args...)
	cmd.Dir = o.Dir
	// Setsid alone: setsid(2) creates a new session with no controlling
	// terminal, which is exactly the isolation needed. Do NOT add
	// SysProcAttr.Noctty here - on darwin it issues ioctl(0, TIOCNOTTY), which
	// fails with ENOTTY whenever fd 0 is not a terminal (a pipe, /dev/null, a
	// script), i.e. exactly how a long job is normally launched. It is also
	// redundant: a session leader has no controlling tty to begin with.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if o.Stdin != "" {
		f, err := os.Open(o.Stdin)
		if err != nil {
			return 0, fmt.Errorf("input: %w", err)
		}
		defer f.Close()
		cmd.Stdin = f
	}
	if o.Stdout != "" {
		f, err := os.Create(o.Stdout)
		if err != nil {
			return 0, fmt.Errorf("output: %w", err)
		}
		defer f.Close()
		cmd.Stdout = f
	}
	f, err := os.Create(o.Stderr)
	if err != nil {
		return 0, fmt.Errorf("log: %w", err)
	}
	defer f.Close()
	cmd.Stderr = f

	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	// Reap the child when it exits. rwrun exits immediately and lets init
	// adopt the run, but an MCP server stays alive: without this Wait the
	// finished run would linger as a zombie and kill(pid, 0) would report it
	// alive forever, so job_status would never leave "running".
	go func() { _ = cmd.Wait() }()
	return pid, nil
}

// StopOptions configures Stop. Zero durations fall back to 5s between the
// signals and a 60s wait for the exit, the rwrun defaults.
type StopOptions struct {
	PidFile string
	Grace   time.Duration
	Wait    time.Duration
	// Guard is the executable name a signalled pid must look like, protecting
	// against a recycled pid in a stale pidfile. Empty means "rayweave", the
	// rwrun default; the MCP server passes the basename of its own binary,
	// which may be a renamed copy.
	Guard string
	// Report receives the progress lines of the stop sequence. Nil is fine.
	Report func(format string, args ...any)
}

// StopResult reports what the sequence did.
type StopResult struct {
	Pid        int
	Exited     bool
	AfterFirst bool // the run was gone after the first SIGTERM
	Signalled  bool // at least one SIGTERM was delivered
}

// Stop implements the two-stage graceful stop documented in docs/escape.md
// ("Interrupting the search") and docs/rwrun.md: the first signal cancels the
// context and lets the running DLS finish (minutes on a large system, and the
// stdout document is only written after ParallelEscape returns); the second
// aborts that DLS within one iteration so the run still writes the document and
// exits 0.
//
// A third signal force-quits, so it is never sent, and SIGKILL is never used:
// it always loses the assembled document (the --save minima survive).
func Stop(o StopOptions) (StopResult, error) {
	grace, wait := o.Grace, o.Wait
	if grace <= 0 {
		grace = 5 * time.Second
	}
	if wait <= 0 {
		wait = 60 * time.Second
	}
	report := o.Report
	if report == nil {
		report = func(string, ...any) {}
	}

	res := StopResult{}
	pid, err := ReadPid(o.PidFile)
	if err != nil {
		if os.IsNotExist(err) {
			return res, fmt.Errorf("no pidfile %s (never launched, or already purged)", AbsPath(o.PidFile))
		}
		return res, err
	}
	res.Pid = pid
	if !PidAlive(pid) {
		report("not running (stale pidfile %s, pid %d)", AbsPath(o.PidFile), pid)
		return res, nil
	}
	// Refuse to signal a recycled pid: only runctl writes these files, so a
	// pid whose command is not our engine means the pidfile is stale.
	guard := o.Guard
	if guard == "" {
		guard = "rayweave"
	}
	if _, comm, perr := ProcInfo(pid); perr == nil && comm != "" && !strings.Contains(comm, guard) {
		return res, fmt.Errorf("pid %d is now %q, not a rayweave run - the pidfile is stale; remove %s if you are sure",
			pid, comm, AbsPath(o.PidFile))
	}

	report("SIGTERM #1 -> graceful: the workers finish the running DLS (this can take minutes)")
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return res, err
	}
	res.Signalled = true
	time.Sleep(grace)
	if PidAlive(pid) {
		report("SIGTERM #2 -> that DLS aborts within one iteration; the run then writes its document and exits 0")
		if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
			return res, err
		}
	} else {
		res.AfterFirst = true
		report("already exited after the first signal")
	}

	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if !PidAlive(pid) {
			res.Exited = true
			report("exited.")
			report("never use kill -9: it always loses the assembled document (the --save minima survive).")
			return res, nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return res, fmt.Errorf("pid %d is still alive after %s; do NOT send kill -9 (it always loses the assembled document) - re-run the stop, or accept the --save minima", pid, wait)
}

// WritePid records pid in path.
func WritePid(path string, pid int) error {
	return os.WriteFile(path, []byte(strconv.Itoa(pid)+"\n"), 0o644)
}

// ReadPid reads a pid recorded by WritePid.
func ReadPid(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(b)))
}

// PidAlive reports whether a signal can be delivered to pid. EPERM means it
// exists but belongs to someone else, which still counts as alive.
func PidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// ProcInfo returns the elapsed time and the command name of pid, via ps.
func ProcInfo(pid int) (string, string, error) {
	out, err := exec.Command("ps", "-o", "etime=,comm=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "", "", err
	}
	f := strings.Fields(string(out))
	if len(f) == 0 {
		return "", "", fmt.Errorf("no ps output for pid %d", pid)
	}
	comm := f[len(f)-1]
	elapsed := strings.Join(f[:len(f)-1], " ")
	return elapsed, comm, nil
}

// AbsPath makes p absolute, returning p unchanged when that fails.
func AbsPath(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return a
}
