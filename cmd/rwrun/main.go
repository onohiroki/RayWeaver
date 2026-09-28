// Command rwrun starts a long-running rayweave subcommand detached from the
// caller's process group, so an environment-level teardown cannot SIGTERM it
// mid-run.
//
// Why it exists: long optimisations (escape, pso, a heavy focus psf) routinely
// outlast the shell that launched them. When that shell's process group is torn
// down - an editor or agent server releasing the workspace and deleting the task
// it registered - every process in the group gets SIGTERM and the optimiser
// dies mid-cycle, losing the assembled stdout document (the --save minima
// survive, being written atomically). rwrun starts the child in its own session
// (setsid) and returns at once, so the child is reparented to init: neither in
// the caller's process group nor in its process tree.
//
//	go run ./cmd/rwrun -n v42 -i v42-escape-input.yaml -o v42-result.yaml \
//	    -e v42-stderr.jsonl -- escape --verbose --keep-infeasible \
//	    --log v42-progress.jsonl --save v42-min
//	go run ./cmd/rwrun -status -n v42
//	go run ./cmd/rwrun -stop -n v42        # SIGTERM twice; never kill -9
//
// Everything after `--` is handed to rayweave unchanged. When the first argument
// is an existing executable file it is taken as the command as-is
// (`-- ./rayweave escape ...`); otherwise the rayweave binary is prepended, so
// `-- escape ...` works too.
//
// stdout and stderr are always kept in separate files: stdout carries the
// pipeline YAML document (written once, at the end), stderr the diagnostics and
// the JSONL progress stream. The two are never merged.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "rwrun: %v\n", err)
		os.Exit(1)
	}
}

// run parses the flags and dispatches to the requested mode. It returns an error
// rather than exiting so the tests can drive the same code path.
func run(argv []string) error {
	fs := flag.NewFlagSet("rwrun", flag.ContinueOnError)
	fs.Usage = func() { usage(fs) }

	var (
		input   = fs.String("input", "", "YAML document piped to the command's stdin (default: /dev/null)")
		output  = fs.String("output", "", "where the command's stdout goes (default: <name>.out, detached) / stdout (foreground)")
		logFile = fs.String("log", "", "where the command's stderr goes (default: <name>.stderr.log)")
		name    = fs.String("name", "", "artefact base name for the defaults (default: the subcommand)")
		dir     = fs.String("dir", "", "working directory for the command (default: the input's directory)")
		binPath = fs.String("rayweave", "", "rayweave binary (default: $RAYWEAVE, ./rayweave, next to rwrun, then $PATH)")
		pidfile = fs.String("pidfile", "", "where the detached pid is recorded (default: <name>.pid)")
		grace   = fs.Int("stop-grace", 5, "seconds between the first and the second SIGTERM")
		wait    = fs.Int("stop-wait", 60, "seconds to wait for the process to exit after the second SIGTERM")
		fg      = fs.Bool("fg", false, "run in the foreground (no detach)")
		status  = fs.Bool("status", false, "report whether the run is alive")
		stop    = fs.Bool("stop", false, "graceful stop: SIGTERM twice, never SIGKILL")
		purge   = fs.Bool("purge", false, "remove the pidfile")
	)
	if err := fs.Parse(expandAliases(argv)); err != nil {
		return err
	}
	modes := 0
	for _, m := range []bool{*fg, *status, *stop, *purge} {
		if m {
			modes++
		}
	}
	if modes > 1 {
		return fmt.Errorf("-fg, -status, -stop and -purge are mutually exclusive")
	}

	args := fs.Args()

	// The pidfile default only needs the name, so resolve that first.
	if *name == "" {
		*name = deriveName(args, *binPath)
	}
	if *name == "" {
		return fmt.Errorf("nothing to run: pass `-- SUBCOMMAND [ARGS...]` (see -h)")
	}
	if *pidfile == "" {
		*pidfile = *name + ".pid"
	}
	absPid := absPath(*pidfile)

	switch {
	case *status:
		return reportStatus(*pidfile, *output, *logFile)
	case *stop:
		return doStop(*pidfile, *grace, *wait)
	case *purge:
		if err := os.Remove(*pidfile); err != nil && !os.IsNotExist(err) {
			return err
		}
		fmt.Printf("removed %s\n", absPid)
		return nil
	}

	// Start mode: resolve the command.
	rayweave, err := resolveRayweave(*binPath)
	if err != nil {
		return err
	}
	bin, argv0 := commandFor(args, rayweave)

	work := *dir
	if work == "" {
		if *input != "" {
			work = filepath.Dir(absPath(*input))
		} else if wd, err := os.Getwd(); err == nil {
			work = wd
		}
	}

	outPath := *output
	if outPath == "" && !*fg {
		outPath = *name + ".out"
	}
	errPath := *logFile
	if errPath == "" {
		errPath = *name + ".stderr.log"
	}

	if *fg {
		return runForeground(bin, argv0, work, *input, outPath, errPath)
	}
	return runDetached(bin, argv0, work, *input, outPath, errPath, *pidfile, *name)
}

// runDetached starts the command in its own session and records its pid. The
// child is never waited for: rwrun returns immediately and the child is
// reparented to init, so nothing in the caller's process group or process tree
// can signal it as a side effect.
func runDetached(bin string, argv []string, work, input, output, errPath, pidfile, name string) error {
	cmd := exec.Command(bin, argv...)
	cmd.Dir = work
	// Setsid alone: setsid(2) creates a new session with no controlling
	// terminal, which is exactly the isolation needed. Do NOT add
	// SysProcAttr.Noctty here - on darwin it issues ioctl(0, TIOCNOTTY), which
	// fails with ENOTTY whenever fd 0 is not a terminal (a pipe, /dev/null, a
	// script), i.e. exactly how a long job is normally launched. It is also
	// redundant: a session leader has no controlling tty to begin with.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if input != "" {
		f, err := os.Open(input)
		if err != nil {
			return fmt.Errorf("input: %w", err)
		}
		defer f.Close()
		cmd.Stdin = f
	}
	if output != "" {
		f, err := os.Create(output)
		if err != nil {
			return fmt.Errorf("output: %w", err)
		}
		defer f.Close()
		cmd.Stdout = f
	}
	f, err := os.Create(errPath)
	if err != nil {
		return fmt.Errorf("log: %w", err)
	}
	defer f.Close()
	cmd.Stderr = f

	if err := cmd.Start(); err != nil {
		return err
	}
	pid := cmd.Process.Pid
	if err := writePid(pidfile, pid); err != nil {
		return err
	}

	fmt.Printf("detached: name=%s  pid=%d  session=%d\n", name, pid, pid)
	fmt.Printf("  cwd     : %s\n", work)
	if input != "" {
		fmt.Printf("  stdin   : %s\n", absPath(input))
	} else {
		fmt.Printf("  stdin   : /dev/null\n")
	}
	if output != "" {
		fmt.Printf("  stdout  : %s   (the pipeline document, written at the very end)\n", absPath(output))
	} else {
		fmt.Printf("  stdout  : inherited\n")
	}
	fmt.Printf("  stderr  : %s   (diagnostics; never merged with stdout)\n", absPath(errPath))
	fmt.Printf("  command : %s %s\n", bin, strings.Join(argv, " "))
	fmt.Printf("  status  : go run ./cmd/rwrun -status -name %s -pidfile %s\n", name, absPath(pidfile))
	fmt.Printf("  stop    : go run ./cmd/rwrun -stop   -name %s -pidfile %s   (SIGTERM twice)\n", name, absPath(pidfile))
	return nil
}

// runForeground runs the command in this process group. Useful for a short run
// or when the caller's lifetime is not a concern; -status/-stop do not apply.
func runForeground(bin string, argv []string, work, input, output, errPath string) error {
	cmd := exec.Command(bin, argv...)
	cmd.Dir = work
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if input != "" {
		f, err := os.Open(input)
		if err != nil {
			return fmt.Errorf("input: %w", err)
		}
		defer f.Close()
		cmd.Stdin = f
	}
	if output != "" {
		f, err := os.Create(output)
		if err != nil {
			return fmt.Errorf("output: %w", err)
		}
		defer f.Close()
		cmd.Stdout = f
	}
	f, err := os.Create(errPath)
	if err != nil {
		return fmt.Errorf("log: %w", err)
	}
	defer f.Close()
	cmd.Stderr = f

	return cmd.Run()
}

// reportStatus prints the liveness of the recorded pid plus the artefact sizes.
func reportStatus(pidfile, output, errPath string) error {
	pid, err := readPid(pidfile)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("state   : no pidfile (never launched, or already purged)")
			return nil
		}
		return err
	}
	elapsed, comm, perr := procInfo(pid)
	switch {
	case !pidAlive(pid):
		fmt.Printf("state   : not running (stale pidfile %s, pid %d)\n", absPath(pidfile), pid)
		fmt.Printf("          purge it with: go run ./cmd/rwrun -purge -pidfile %s\n", absPath(pidfile))
		return nil
	case perr != nil:
		fmt.Printf("state   : RUNNING  pid=%d\n", pid)
	default:
		fmt.Printf("state   : RUNNING  pid=%d  elapsed=%s  comm=%s\n", pid, elapsed, comm)
	}
	for _, p := range []string{output, errPath} {
		if p == "" {
			continue
		}
		if st, err := os.Stat(p); err == nil {
			fmt.Printf("  %-28s %9d bytes  %s\n", filepath.Base(p), st.Size(), st.ModTime().Format("15:04:05"))
		} else {
			fmt.Printf("  %-28s %s\n", filepath.Base(p), "(not created yet)")
		}
	}
	return nil
}

// doStop implements the two-stage graceful stop documented in AGENTS.md: the
// first signal cancels the context and lets the running DLS finish (minutes on a
// large system, and the stdout document is only written after ParallelEscape
// returns), the second aborts that DLS within one iteration so the run still
// writes the document and exits 0. A third signal force-quits, so it is never
// sent; SIGKILL always loses the document.
func doStop(pidfile string, grace, wait int) error {
	pid, err := readPid(pidfile)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no pidfile %s (never launched, or already purged)", absPath(pidfile))
		}
		return err
	}
	if !pidAlive(pid) {
		fmt.Printf("not running (stale pidfile %s, pid %d)\n", absPath(pidfile), pid)
		return nil
	}
	// Refuse to signal a recycled pid: only rwrun writes these files, so a
	// non-rayweave command means the pidfile is stale.
	if _, comm, perr := procInfo(pid); perr == nil && comm != "" && !strings.Contains(comm, "rayweave") {
		return fmt.Errorf("pid %d is now %q, not a rayweave run - the pidfile is stale; remove %s if you are sure",
			pid, comm, absPath(pidfile))
	}

	fmt.Printf("SIGTERM #1 -> graceful: the workers finish the running DLS (this can take minutes)\n")
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return err
	}
	time.Sleep(time.Duration(grace) * time.Second)
	if pidAlive(pid) {
		fmt.Printf("SIGTERM #2 -> that DLS aborts within one iteration; the run then writes its document and exits 0\n")
		if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
			return err
		}
	} else {
		fmt.Printf("already exited after the first signal\n")
	}

	deadline := time.Now().Add(time.Duration(wait) * time.Second)
	for time.Now().Before(deadline) {
		if !pidAlive(pid) {
			fmt.Printf("exited.\n")
			fmt.Printf("never use kill -9: it always loses the assembled document (the --save minima survive).\n")
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	fmt.Printf("WARNING: pid %d is still alive after %ds. Do NOT send kill -9 - it always loses\n", pid, wait)
	fmt.Printf("         the assembled document. Re-run -stop, or accept the --save minima.\n")
	return nil
}

// expandAliases rewrites the short flag spellings to their long form, and only
// before `--`, so a rayweave flag that happens to share a letter is untouched.
func expandAliases(argv []string) []string {
	aliases := map[string]string{"-i": "-input", "-o": "-output", "-e": "-log", "-n": "-name", "-d": "-dir"}
	out := make([]string, 0, len(argv))
	for i, a := range argv {
		if a == "--" {
			out = append(out, argv[i:]...)
			return out
		}
		if long, ok := aliases[a]; ok {
			out = append(out, long)
			continue
		}
		if eq := strings.Index(a, "="); eq > 0 {
			if long, ok := aliases[a[:eq]]; ok {
				out = append(out, long+a[eq:])
				continue
			}
		}
		out = append(out, a)
	}
	return out
}

// deriveName returns the artefact base name: the subcommand, so
// `-- escape ...` yields "escape" and the defaults are escape.out /
// escape.stderr.log / escape.pid.
func deriveName(args []string, binPath string) string {
	var sub string
	switch {
	case len(args) == 0:
		return ""
	case looksLikeBinary(args[0]) && len(args) > 1:
		sub = args[1] // an explicit command: skip the binary
	case looksLikeBinary(args[0]):
		return ""
	default:
		sub = args[0] // a bare subcommand
	}
	if binPath != "" && sub == filepath.Base(binPath) {
		return ""
	}
	var b strings.Builder
	for _, r := range sub {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// looksLikeBinary reports whether p should be read as a command rather than a
// bare subcommand. A subcommand never contains a path separator, so that alone
// is decisive; an existing executable file and the name "rayweave" cover the
// obvious cases without depending on the file being present (it may be resolved
// later, and in a test it need not exist).
func looksLikeBinary(p string) bool {
	if p == "" || strings.HasPrefix(p, "-") {
		return false
	}
	return isExecutableFile(p) || filepath.Base(p) == "rayweave" || strings.ContainsRune(p, os.PathSeparator)
}

// resolveRayweave locates the rayweave binary. Order: the explicit -rayweave
// path, $RAYWEAVE, ./rayweave, $PATH, and only then the directory holding rwrun.
// "Next to the executable" is last on purpose: when rwrun is built somewhere
// unrelated (a scratch /tmp, say) that directory can hold a completely unrelated
// and badly outdated `rayweave`, which is exactly what happened during
// development. The resolved path is always printed in the launch summary, and
// -rayweave/$RAYWEAVE override the search.
func resolveRayweave(explicit string) (string, error) {
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
			if abs, err := filepath.Abs(p); err == nil {
				return abs, nil
			}
			return p, nil
		}
	}
	return "", fmt.Errorf("rayweave not found (tried %s; pass -rayweave PATH or set $RAYWEAVE)", strings.Join(cands, ", "))
}

// commandFor returns the executable and its arguments (the binary itself is NOT
// repeated in the argument list - exec.Command takes it separately). An existing
// executable file as the first argument is honoured verbatim; otherwise the
// rayweave binary is prepended so `-- escape ...` reads naturally.
func commandFor(args []string, rayweave string) (string, []string) {
	if len(args) == 0 {
		return "", nil
	}
	if looksLikeBinary(args[0]) {
		return args[0], args[1:]
	}
	return rayweave, args
}

func isExecutableFile(p string) bool {
	if p == "" || strings.HasPrefix(p, "-") {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
}

// pidAlive reports whether a signal can be delivered to pid. EPERM means it
// exists but belongs to someone else, which still counts as alive.
func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// procInfo returns the elapsed time and the command name of pid, via ps.
func procInfo(pid int) (string, string, error) {
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

func writePid(path string, pid int) error {
	return os.WriteFile(path, []byte(strconv.Itoa(pid)+"\n"), 0o644)
}

func readPid(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(b)))
}

func absPath(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return a
}

func usage(fs *flag.FlagSet) {
	fmt.Fprint(fs.Output(), `rwrun - start a long rayweave subcommand detached from the caller's process group

Usage:
  rwrun [options] -- [rayweave] SUBCOMMAND [ARGS...]

  -i, -input FILE    YAML piped to the command's stdin (default: /dev/null)
  -o, -output FILE   where the command's stdout goes (default: <name>.out)
  -e, -log FILE      where the command's stderr goes (default: <name>.stderr.log)
  -n, -name NAME     artefact base name (default: the subcommand)
  -d, -dir DIR       working directory (default: the input's directory)
      -rayweave PATH rayweave binary (default: $RAYWEAVE, ./rayweave, beside rwrun, $PATH)
      -pidfile FILE   detached pid record (default: <name>.pid)
      -stop-grace N   seconds between the two SIGTERMs (default 5)
      -stop-wait N    seconds to wait for the exit (default 60)
      -fg            run in the foreground (no detach)
      -status        report whether the run is alive
      -stop          graceful stop: SIGTERM twice, never SIGKILL
      -purge         remove the pidfile

Examples:
  rwrun -n v42 -i v42-escape-input.yaml -o v42-result.yaml -e v42-stderr.jsonl \
      -- escape --verbose --keep-infeasible --log v42-progress.jsonl --save v42-min
  rwrun -status -n v42
  rwrun -stop   -n v42

Everything after -- goes to rayweave unchanged. When the first argument is an
existing executable it is used as the command as-is (-- ./rayweave escape ...);
otherwise the rayweave binary is prepended (-- escape ...).

stdout and stderr are never merged: stdout carries the pipeline document, which
escape writes only once every cycle has finished, and stderr the diagnostics.
`)
}
