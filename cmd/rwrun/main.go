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
//	go run ./cmd/rwrun -stop -n v42        # two stages; never a force-kill
//
// On Windows there is no setsid and no SIGTERM: the child is created with
// DETACHED_PROCESS and its own process group, and -stop writes the stages to
// <pidfile>.stop, which the run watches. A Job Object teardown or a parent-pid
// tree kill can still reach the child; see docs/windows.md.
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
	"strings"
	"time"

	"github.com/hiroki/rayweaver/internal/runctl"
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
		stop    = fs.Bool("stop", false, "graceful stop: two stages, never a force-kill")
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
	absPid := runctl.AbsPath(*pidfile)

	switch {
	case *status:
		return reportStatus(*pidfile, *output, *logFile)
	case *stop:
		return doStop(*pidfile, *grace, *wait)
	case *purge:
		if err := os.Remove(*pidfile); err != nil && !os.IsNotExist(err) {
			return err
		}
		// Windows leaves the stop file next to the pidfile; Unix never creates it.
		_ = os.Remove(runctl.StopFilePath(*pidfile))
		fmt.Printf("removed %s\n", absPid)
		return nil
	}

	// Start mode: resolve the command.
	rayweave, err := runctl.Resolve(*binPath)
	if err != nil {
		return err
	}
	bin, argv0 := commandFor(args, rayweave)

	work := *dir
	if work == "" {
		if *input != "" {
			work = filepath.Dir(runctl.AbsPath(*input))
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
	pid, err := runctl.Launch(runctl.LaunchOptions{
		Binary:   bin,
		Args:     argv,
		Dir:      work,
		Stdin:    input,
		Stdout:   output,
		Stderr:   errPath,
		StopFile: runctl.StopFilePath(pidfile),
	})
	if err != nil {
		return err
	}
	if err := runctl.WritePid(pidfile, pid); err != nil {
		return err
	}

	fmt.Printf("detached: name=%s  pid=%d  session=%d\n", name, pid, pid)
	fmt.Printf("  cwd     : %s\n", work)
	if input != "" {
		fmt.Printf("  stdin   : %s\n", runctl.AbsPath(input))
	} else {
		fmt.Printf("  stdin   : /dev/null\n")
	}
	if output != "" {
		fmt.Printf("  stdout  : %s   (the pipeline document, written at the very end)\n", runctl.AbsPath(output))
	} else {
		fmt.Printf("  stdout  : inherited\n")
	}
	fmt.Printf("  stderr  : %s   (diagnostics; never merged with stdout)\n", runctl.AbsPath(errPath))
	fmt.Printf("  command : %s %s\n", bin, strings.Join(argv, " "))
	fmt.Printf("  status  : go run ./cmd/rwrun -status -name %s -pidfile %s\n", name, runctl.AbsPath(pidfile))
	fmt.Printf("  stop    : go run ./cmd/rwrun -stop   -name %s -pidfile %s   (%s)\n", name, runctl.AbsPath(pidfile), runctl.StopHint())
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
	pid, err := runctl.ReadPid(pidfile)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("state   : no pidfile (never launched, or already purged)")
			return nil
		}
		return err
	}
	elapsed, comm, perr := runctl.ProcInfo(pid)
	switch {
	case !runctl.PidAlive(pid):
		fmt.Printf("state   : not running (stale pidfile %s, pid %d)\n", runctl.AbsPath(pidfile), pid)
		fmt.Printf("          purge it with: go run ./cmd/rwrun -purge -pidfile %s\n", runctl.AbsPath(pidfile))
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

// doStop runs the two-stage graceful stop (see runctl.Stop) and prints what it
// did. A run that is still alive after the wait is a warning, not a failure:
// the caller can re-run -stop, and the document is not lost.
func doStop(pidfile string, grace, wait int) error {
	res, err := runctl.Stop(runctl.StopOptions{
		PidFile:  pidfile,
		StopFile: runctl.StopFilePath(pidfile),
		Grace:    time.Duration(grace) * time.Second,
		Wait:     time.Duration(wait) * time.Second,
		Report:   func(format string, args ...any) { fmt.Printf(format+"\n", args...) },
	})
	if err != nil {
		if res.Signalled {
			fmt.Printf("WARNING: %v\n", err)
			return nil
		}
		return err
	}
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
      -stop-grace N   seconds between the two stop stages (default 5)
      -stop-wait N    seconds to wait for the exit (default 60)
      -fg            run in the foreground (no detach)
      -status        report whether the run is alive
      -stop          graceful stop: two stages (SIGTERM on Unix, control file on
                     Windows), never a force-kill
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
