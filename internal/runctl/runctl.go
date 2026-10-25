// Package runctl launches long rayweave runs detached from the caller's process
// group and stops them with the staged stop the optimisers expect.
//
// The mechanism is platform-specific: Unix uses setsid(2) and SIGTERM twice,
// Windows has neither, so it starts the child in its own detached process group
// and the child watches a small stop file. The two stages (graceful, then abort
// the running DLS) are the same on both. See docs/rwrun.md, docs/mcp.md and
// docs/escape.md.
package runctl

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// StopFileEnv is the environment variable a detached launch sets to the path of
// the stop file. Only the Windows launcher creates the file; on Unix the
// optimisers keep using signals. A caller may set it explicitly on Unix to use
// the same cooperative path (tests, or a launcher that cannot signal).
const StopFileEnv = "RAYWEAVE_STOP_FILE"

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
		// On Windows LookPath appends PATHEXT, so this also finds rayweave.exe.
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
	// StopFile is the control file the Windows child watches. It is created
	// (truncated) before the process starts, so an immediate stop cannot race
	// the watcher. Unix launchers ignore it and keep using signals.
	StopFile string
}

// Launch starts the command detached and returns its pid without waiting. On
// Unix the child is reparented to init, so nothing in the caller's process
// group or process tree can signal it as a side effect. On Windows the child
// runs in its own detached process group; see the limitations in
// docs/windows.md (a Job Object teardown can still reach it).
func Launch(o LaunchOptions) (int, error) {
	cmd := exec.Command(o.Binary, o.Args...)
	cmd.Dir = o.Dir
	cmd.SysProcAttr = sysProcAttr()

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

	if useStopFile && o.StopFile != "" {
		if err := createStopFile(o.StopFile); err != nil {
			return 0, fmt.Errorf("stop file: %w", err)
		}
		cmd.Env = EnvWithStopFile(AbsPath(o.StopFile))
	}

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
// stages and a 60s wait for the exit, the rwrun defaults.
type StopOptions struct {
	PidFile string
	// StopFile is where Windows writes the stage numbers. Empty means
	// <PidFile>.stop. Unix ignores it.
	StopFile string
	Grace    time.Duration
	Wait     time.Duration
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
	AfterFirst bool // the run was gone after the first stage
	Signalled  bool // at least one stop request was delivered
}

// Stop implements the two-stage graceful stop documented in docs/escape.md
// ("Interrupting the search") and docs/rwrun.md: the first request cancels the
// context and lets the running DLS finish (minutes on a large system, and the
// stdout document is only written after ParallelEscape returns); the second
// aborts that DLS within one iteration so the run still writes the document and
// exits 0.
//
// A third signal force-quits, so it is never sent, and SIGKILL is never used:
// it always loses the assembled document (the --save minima survive). On
// Windows the stages travel through the stop file instead of signals.
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
	if _, comm, perr := ProcInfo(pid); perr == nil && comm != "" && !guardMatches(comm, guard) {
		return res, fmt.Errorf("pid %d is now %q, not a rayweave run - the pidfile is stale; remove %s if you are sure",
			pid, comm, AbsPath(o.PidFile))
	}

	stopFile := o.StopFile
	if useStopFile {
		if stopFile == "" {
			stopFile = StopFilePath(o.PidFile)
		}
		stopFile = AbsPath(stopFile)
		if _, err := os.Stat(stopFile); err != nil {
			return res, fmt.Errorf("no stop file %s: the run was launched without %s (an older launcher?), so it cannot be asked to stop gracefully",
				AbsPath(stopFile), StopFileEnv)
		}
	}

	report("%s #1 -> graceful: the workers finish the running DLS (this can take minutes)", stopName)
	if err := requestStop(pid, 1, stopFile); err != nil {
		return res, err
	}
	res.Signalled = true
	time.Sleep(grace)
	if PidAlive(pid) {
		report("%s #2 -> that DLS aborts within one iteration; the run then writes its document and exits 0", stopName)
		if err := requestStop(pid, 2, stopFile); err != nil {
			return res, err
		}
	} else {
		res.AfterFirst = true
		report("already exited after the first stop request")
	}

	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if !PidAlive(pid) {
			res.Exited = true
			report("exited.")
			report("%s", doneAdvice)
			return res, nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return res, fmt.Errorf("pid %d is still alive after %s; %s", pid, wait, killAdvice)
}

// RequestStop delivers one stop stage. On Unix it sends SIGTERM (the optimisers
// count signals); on Windows it appends the stage to stopFile, which the child
// polls.
func RequestStop(pid, stage int, stopFile string) error {
	return requestStop(pid, stage, stopFile)
}

// StopFilePath returns the default stop-file path for a pidfile. The launcher
// and the stopper derive the same path, so neither has to parse the other's
// files.
func StopFilePath(pidFile string) string {
	return pidFile + ".stop"
}

// StopHint describes the platform's stop mechanism for help text and summaries.
func StopHint() string {
	return stopHint
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

// PidAlive reports whether pid names a live process. A process that exists but
// cannot be inspected (EPERM on Unix, ACCESS_DENIED on Windows) still counts as
// alive.
func PidAlive(pid int) bool {
	return pidAlive(pid)
}

// ProcInfo returns the elapsed time and the command name of pid when the
// platform can provide them (ps on Unix, GetProcessTimes +
// QueryFullProcessImageName on Windows).
func ProcInfo(pid int) (string, string, error) {
	return procInfo(pid)
}

// AbsPath makes p absolute, returning p unchanged when that fails.
func AbsPath(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return a
}

// EnvWithStopFile returns the current environment with RAYWEAVE_STOP_FILE set
// to path, replacing any inherited value so the launcher's path always wins.
func EnvWithStopFile(path string) []string {
	env := os.Environ()
	out := env[:0]
	for _, e := range env {
		if strings.HasPrefix(e, StopFileEnv+"=") {
			continue
		}
		out = append(out, e)
	}
	return append(out, StopFileEnv+"="+path)
}

// createStopFile creates (or truncates) the control file before the child
// starts, removing the race between a fast stop request and the watcher.
func createStopFile(path string) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}
