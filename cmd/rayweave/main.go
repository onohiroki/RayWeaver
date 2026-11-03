package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"strconv"
	"strings"

	"github.com/hiroki/rayweaver/internal/types"
)

// currentCmd holds the active subcommand so stderr output can be attributed
// to the right pipeline stage (e.g. "rayweave[chief]:").
var currentCmd string

// warnSinksRouted marks that the Warnf sinks of the optimize and glass
// packages currently point at a progress stream rather than at the tagged
// stderr writer (escape/pso with --verbose or --log, see newEscapeProgress).
// loadCatalogs installs the default sink and has to leave a routed one alone —
// it runs after the reporter is created, so overwriting there would both drop
// the warnings raised during the catalog load out of the stream and un-route
// every later warning.
var warnSinksRouted bool

// errOut writes a tagged line to stderr. The tag identifies the subcommand
// that produced the message, which is essential when commands are piped
// (e.g. "rayweave chief | rayweave trace | rayweave plot").
func errOut(format string, args ...any) {
	prefix := "rayweave"
	if currentCmd != "" {
		prefix = "rayweave[" + currentCmd + "]"
	}
	fmt.Fprintf(os.Stderr, prefix+": "+format+"\n", args...)
}

// errExitf reports a fatal error through errOut and exits with status 1. It is
// the single-call form of the `errOut(...); os.Exit(1)` pair that every
// command used to spell out, so a fatal error is one statement and the tag /
// exit policy lives in one place. The message keeps its own "Error:" prefix
// where the docs show one (e.g. `rayweave[zoom]: Error: ...`).
func errExitf(format string, args ...any) {
	errOut(format, args...)
	os.Exit(1)
}

func main() {
	args := os.Args[1:]

	// Help handling — no stdin required
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		cmd := ""
		if len(args) >= 2 {
			cmd = args[1]
		}
		printHelp(cmd)
		return
	}
	if args[0] == "help" {
		cmd := ""
		if len(args) >= 2 {
			cmd = args[1]
		}
		printHelp(cmd)
		return
	}

	// Subcommand help (no stdin required) — check before any flag parsing
	for _, a := range args[1:] {
		if a == "--help" || a == "-h" {
			printHelp(args[0])
			return
		}
	}

	// Parse optimize-specific flags before stdin reading
	optVerbose := false
	optLogFile := ""
	optGlassDir := ""
	optExcludeParams := ""
	optPowerSolve := false
	optPowerSolveSurfaces := ""
	optGlassVariables := false
	optOptimizeCPUProfile := ""
	optOptimizeMemProfile := ""
	optOptimizeGorProfile := ""
	optOptimizeGOGC := 0
	subcommand := args[0]
	currentCmd = subcommand
	optSnapMode := false
	if subcommand == "optimize" {
		if len(args) >= 2 && args[1] == "snap" {
			optSnapMode = true
			fs := flag.NewFlagSet("optimize snap", flag.ContinueOnError)
			fs.StringVar(&optGlassDir, "glass-dir", "", "AGF glass catalog directory")
			fs.Parse(args[2:])
			args = append([]string{"optimize"}, fs.Args()...)
		} else {
			fs := flag.NewFlagSet("optimize", flag.ContinueOnError)
			fs.BoolVar(&optVerbose, "verbose", false, "print per-iteration progress to stderr")
			fs.StringVar(&optLogFile, "log", "", "write per-iteration progress to file (JSONL)")
			fs.StringVar(&optGlassDir, "glass-dir", "", "AGF glass catalog directory")
			fs.StringVar(&optExcludeParams, "exclude-param", "", "comma-separated target param names to drop from the optimization variables (e.g. conic,a4,a6)")
			fs.BoolVar(&optPowerSolve, "power-solve", false, "preserve each listed element's thin-lens power (hard solve) while only the glass dispersions are free")
			fs.StringVar(&optPowerSolveSurfaces, "power-solve-surfaces", "", "comma-separated surface IDs whose curvature is recomputed to hold the containing element's thin-lens power at its initial value (with --power-solve)")
			fs.BoolVar(&optGlassVariables, "glass-variables", false, "auto-generate nd/vd optimization variables for every refractive lens element (the merit is left unchanged)")
			fs.StringVar(&optOptimizeCPUProfile, "cpuprofile", "", "write a CPU profile (pprof) to FILE")
			fs.StringVar(&optOptimizeMemProfile, "memprofile", "", "write a heap profile (pprof) to FILE at exit")
			fs.StringVar(&optOptimizeGorProfile, "gorprofile", "", "write a goroutine profile to FILE at exit")
			fs.IntVar(&optOptimizeGOGC, "gogc", 0, "GC target percentage (0 = default for compute commands, 400; overrides $GOGC)")
			fs.Parse(args[1:])
			args = append([]string{"optimize"}, fs.Args()...)
		}
	}

	// Escape has two sub-subcommands: run (default) and extract.
	escapeExtractMode := false
	escapeExtractIndex := 0
	optEscapeGlassDir := ""
	optEscapeVerbose := false
	optEscapeLogFile := ""
	optEscapeSaveFile := ""
	optEscapePowerSolve := false
	optEscapePowerSolveSurfaces := ""
	optEscapeGlassVariables := false
	optEscapeKeepInfeasible := false
	optEscapeDebug := false
	optEscapeCPUProfile := ""
	optEscapeMemProfile := ""
	optEscapeGorProfile := ""
	optEscapeGOGC := 0
	if subcommand == "escape" {
		if len(args) >= 2 && args[1] == "extract" {
			escapeExtractMode = true
			escapeExtractIndex = parseEscapeExtractFlags(args[2:])
		} else {
			fs := flag.NewFlagSet("escape", flag.ContinueOnError)
			fs.BoolVar(&optEscapeVerbose, "verbose", false, "print escape progress (local minima, parameter changes) to stderr as compact JSONL")
			fs.StringVar(&optEscapeGlassDir, "glass-dir", "", "AGF glass catalog directory")
			fs.StringVar(&optEscapeLogFile, "log", "", "write escape progress to file (JSONL)")
			fs.StringVar(&optEscapeSaveFile, "save", "", "save each discovered local minimum to FILE0.yaml, FILE1.yaml, ...")
			fs.BoolVar(&optEscapePowerSolve, "power-solve", false, "insert the power-preserving glass phase between each escape and clean DLS (holds element powers fixed while the glasses are rebalanced)")
			fs.StringVar(&optEscapePowerSolveSurfaces, "power-solve-surfaces", "", "comma-separated surface IDs whose curvature is recomputed to hold the containing element's thin-lens power (with --power-solve; also enables the glass phase)")
			fs.BoolVar(&optEscapeGlassVariables, "glass-variables", false, "auto-generate nd/vd optimization variables for every refractive lens element (the merit is left unchanged)")
			fs.BoolVar(&optEscapeKeepInfeasible, "keep-infeasible", false, "include infeasible basins in stdout YAML (default: discard)")
			fs.BoolVar(&optEscapeDebug, "debug", false, "emit all DLS-internal JSONL events (iter, final, adaptive_damping, mode_change) plus enriched cycle diagnostics")
			fs.StringVar(&optEscapeStream, "stream", "", "stream the pipeline document to stdout as the run progresses: true (default) or false for the one-shot write after the run")
			fs.StringVar(&optEscapeCPUProfile, "cpuprofile", "", "write a CPU profile (pprof) to FILE")
			fs.StringVar(&optEscapeMemProfile, "memprofile", "", "write a heap profile (pprof) to FILE at exit")
			fs.StringVar(&optEscapeGorProfile, "gorprofile", "", "write a goroutine profile to FILE at exit")
			fs.IntVar(&optEscapeGOGC, "gogc", 0, "GC target percentage (0 = default for compute commands, 400; overrides $GOGC)")
			fs.StringVar(&optEscapeMemLimit, "mem-limit", "", "cap the Go heap: 2GiB, 2048MiB, 2048 (MB) or -1 (unlimited); default optimization.mem_limit_mb")
			fs.StringVar(&optEscapePhases.cleanDLS, phaseCleanDLSFlag, "", "run the clean DLS that closes each cycle: true|false (default true)")
			fs.Parse(args[1:])
			optEscapeStreamSet = flagWasSet(fs, "stream")
			optEscapePhases.cleanDLSSet = flagWasSet(fs, phaseCleanDLSFlag)
		}
	}

	// PSO has two sub-subcommands: run (default) and extract.
	psoExtractMode := false
	psoExtractIndex := 0
	optPSOGlassDir := ""
	optPSOVerbose := false
	optPSOLogFile := ""
	optPSOSaveFile := ""
	optPSOPowerSolve := false
	optPSOPowerSolveSurfaces := ""
	optPSOGlassVariables := false
	optPSOKeepInfeasible := false
	optPSOSwarmSize := 0
	optPSOIterations := 0
	optPSOConstraintPenalty := 0.0
	optPSOGbest := ""
	optPSOCPUProfile := ""
	optPSOMemProfile := ""
	optPSOGorProfile := ""
	optPSOGOGC := 0
	if subcommand == "pso" {
		if len(args) >= 2 && args[1] == "extract" {
			psoExtractMode = true
			psoExtractIndex = parseEscapeExtractFlags(args[2:])
		} else {
			fs := flag.NewFlagSet("pso", flag.ContinueOnError)
			fs.BoolVar(&optPSOVerbose, "verbose", false, "print PSO progress (local minima, parameter changes) to stderr as compact JSONL")
			fs.StringVar(&optPSOGlassDir, "glass-dir", "", "AGF glass catalog directory")
			fs.StringVar(&optPSOLogFile, "log", "", "write PSO progress to file (JSONL)")
			fs.StringVar(&optPSOSaveFile, "save", "", "save each discovered local minimum to FILE0.yaml, FILE1.yaml, ...")
			fs.BoolVar(&optPSOPowerSolve, "power-solve", false, "insert the power-preserving glass phase between each PSO and clean DLS")
			fs.StringVar(&optPSOPowerSolveSurfaces, "power-solve-surfaces", "", "comma-separated surface IDs for the power-preserving solve")
			fs.BoolVar(&optPSOGlassVariables, "glass-variables", false, "auto-generate nd/vd optimization variables for every refractive lens element")
			fs.BoolVar(&optPSOKeepInfeasible, "keep-infeasible", false, "include infeasible basins in stdout YAML (default: discard)")
			fs.StringVar(&optPSOStream, "stream", "", "stream the pipeline document to stdout as the run progresses: true (default) or false for the one-shot write after the run")
			fs.IntVar(&optPSOSwarmSize, "swarm-size", 0, "number of particles per worker (overrides optimization.pso.swarm_size)")
			fs.IntVar(&optPSOIterations, "pso-iterations", 0, "PSO iterations per cycle (overrides optimization.pso.pso_iterations)")
			fs.Float64Var(&optPSOConstraintPenalty, "constraint-penalty", 0, "constraint penalty weight (overrides optimization.pso.constraint_penalty)")
			fs.StringVar(&optPSOGbest, "gbest", "", "scale the global best is ranked on: merit|penalised (overrides optimization.pso.gbest)")
			fs.StringVar(&optPSOCPUProfile, "cpuprofile", "", "write a CPU profile (pprof) to FILE")
			fs.StringVar(&optPSOMemProfile, "memprofile", "", "write a heap profile (pprof) to FILE at exit")
			fs.StringVar(&optPSOGorProfile, "gorprofile", "", "write a goroutine profile to FILE at exit")
			fs.IntVar(&optPSOGOGC, "gogc", 0, "GC target percentage (0 = default for compute commands, 400; overrides $GOGC)")
			fs.StringVar(&optPSOMemLimit, "mem-limit", "", "cap the Go heap: 2GiB, 2048MiB, 2048 (MB) or -1 (unlimited); default optimization.mem_limit_mb")
			fs.StringVar(&optPSOPhases.cleanDLS, phaseCleanDLSFlag, "", "run the clean DLS that closes each cycle: true|false (default true)")
			fs.Parse(args[1:])
			optPSOStreamSet = flagWasSet(fs, "stream")
			optPSOPhases.cleanDLSSet = flagWasSet(fs, phaseCleanDLSFlag)
		}
	}

	// Trace has a sub-subcommand: single (CLI one-ray trace).
	traceSingleMode := false
	if subcommand == "trace" {
		if len(args) >= 2 && args[1] == "single" {
			traceSingleMode = true
			args = append([]string{"trace"}, args[2:]...)
		}
	}

	data, err := readStdin()
	if err != nil {
		// `query` can evaluate literal expressions / bindings without any
		// document, so an interactive terminal is not an error for it.
		if subcommand != "query" {
			errExitf("Error reading stdin: %v", err)
		}
		data = nil
	}

	switch subcommand {
	case "chief":
		runChief(data)
	case "trace":
		runTrace(data, traceSingleMode)
	case "paraxial":
		runParaxial(data)
	case "zoom":
		runZoom(data)
	case "tmm":
		runTMM(data)
	case "plot":
		runPlot(data)
	case "vignette":
		runVignette(data)
	case "optimize":
		if optSnapMode {
			runOptimizeSnap(data, optGlassDir)
		} else {
			applyGCPercent(optOptimizeGOGC)
			stop := startCPUProfile(optOptimizeCPUProfile)
			runOptimize(data, optVerbose, optLogFile, optGlassDir, optExcludeParams, optPowerSolve, optPowerSolveSurfaces, optGlassVariables)
			stop()
			writeMemGorProfiles(optOptimizeMemProfile, optOptimizeGorProfile)
		}
	case "escape":
		if escapeExtractMode {
			runEscapeExtract(data, escapeExtractIndex, "escape extract")
		} else {
			applyGCPercent(optEscapeGOGC)
			stop := startCPUProfile(optEscapeCPUProfile)
			runEscape(data, optEscapeGlassDir, optEscapeVerbose, optEscapeLogFile, optEscapeSaveFile, optEscapePowerSolve, optEscapePowerSolveSurfaces, optEscapeGlassVariables, optEscapeKeepInfeasible, optEscapeDebug, nil)
			stop()
			writeMemGorProfiles(optEscapeMemProfile, optEscapeGorProfile)
		}
	case "pso":
		if psoExtractMode {
			runEscapeExtract(data, psoExtractIndex, "pso extract")
		} else {
			applyGCPercent(optPSOGOGC)
			stop := startCPUProfile(optPSOCPUProfile)
			runPSO(data, optPSOGlassDir, optPSOVerbose, optPSOLogFile, optPSOSaveFile, optPSOPowerSolve, optPSOPowerSolveSurfaces, optPSOGlassVariables, optPSOKeepInfeasible, optPSOSwarmSize, optPSOIterations, optPSOConstraintPenalty, optPSOGbest)
			stop()
			writeMemGorProfiles(optPSOMemProfile, optPSOGorProfile)
		}
	case "import":
		runImport(data)
	case "export":
		runExport(data)
	case "scale":
		runScale(data)
	case "cam":
		runCam(data)
	case "asphere":
		runAsphere(data)
	case "psf":
		runPSF(data)
	case "focus":
		runFocus(data)
	case "wavefront":
		runWavefront(data)
	case "query":
		runQuery(data)
	case "clean":
		runClean(data)
	case "list":
		runList(data)
	default:
		errOut("Error: unknown subcommand %q", subcommand)
		errExitf("Run 'rayweave --help' for usage.")
	}
}

// startCPUProfile begins a pprof CPU profile writing to path and returns a stop
// function that flushes and closes it. A no-op is returned when path is empty.
// Used to wrap the long-running subcommands (escape/optimize/pso).
func startCPUProfile(path string) func() {
	if path == "" {
		return func() {}
	}
	f, err := os.Create(path)
	if err != nil {
		errExitf("Error: --cpuprofile: %v", err)
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		errExitf("Error: --cpuprofile: %v", err)
	}
	return func() {
		pprof.StopCPUProfile()
		f.Close()
	}
}

// applyGCPercent sets the GC target for the compute-heavy commands
// (escape/optimize/pso). An explicit --gogc wins; otherwise a higher target
// (400) trades a little memory for a large reduction in GC, scavenger (madvise)
// and allocator overhead. The GOGC environment variable, when set, is respected.
func applyGCPercent(gogc int) {
	if gogc > 0 {
		debug.SetGCPercent(gogc)
		return
	}
	if os.Getenv("GOGC") == "" {
		debug.SetGCPercent(400)
	}
}

// Memory-limit CLI options (--mem-limit). They are read by the escape and PSO
// commands after the input has been parsed, so they live at package scope; an
// empty value falls back to optimization.mem_limit_mb.

var (
	optEscapeMemLimit string
	optPSOMemLimit    string
)

// --stream (escape/pso) takes an explicit true/false value — the flag package's
// built-in bool flags only accept the `--stream=false` spelling, and the
// documented form is `--stream false` — so the raw string travels here with
// its "was it given?" state: an unset flag must not override the input YAML
// (see escapeStreamEnabled). escape and pso parse their own flag sets; only
// one of the pairs is ever set per run.

var (
	optEscapeStream    string
	optEscapeStreamSet bool
	optPSOStream       string
	optPSOStreamSet    bool
)

// applyMemLimit applies the Go soft memory limit (runtime/debug.SetMemoryLimit)
// for a long-running compute command. Precedence: the --mem-limit CLI flag, then
// optimization.mem_limit_mb. An empty flag and an absent/zero YAML value leave
// the limit unset (the GOMEMLIMIT environment variable / Go default applies);
// -1 means unlimited. The effective value is written back into the input.
func applyMemLimit(input *types.Input, flagValue string) {
	mb, set := 0.0, false
	if strings.TrimSpace(flagValue) != "" {
		v, err := parseMemLimitMB(flagValue)
		if err != nil {
			errExitf("Error: --mem-limit %q: %v", flagValue, err)
		}
		mb, set = v, true
	} else if input.Optimization != nil && input.Optimization.MemLimitMB != 0 {
		mb, set = input.Optimization.MemLimitMB, true
	}
	if !set {
		return
	}
	debug.SetMemoryLimit(memLimitBytes(mb))
	if input.Optimization != nil {
		input.Optimization.MemLimitMB = mb
	}
}

// parseMemLimitMB parses a memory-limit size: a bare number is MB, and the
// KiB/MiB/GiB (binary) and KB/MB/GB (decimal) suffixes are accepted. A value
// <= 0 means unlimited.
func parseMemLimitMB(s string) (float64, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, fmt.Errorf("empty value")
	}
	mult := 1.0
	for _, suf := range []struct {
		s string
		m float64
	}{
		{"kib", 1.0 / 1024}, {"mib", 1}, {"gib", 1024},
		{"kb", 1.0 / 1000}, {"mb", 1}, {"gb", 1000},
		{"k", 1.0 / 1024}, {"m", 1}, {"g", 1024},
	} {
		if strings.HasSuffix(s, suf.s) {
			mult = suf.m
			s = strings.TrimSuffix(s, suf.s)
			break
		}
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, fmt.Errorf("invalid number %q", s)
	}
	if v <= 0 {
		return -1, nil // unlimited
	}
	return v * mult, nil
}

// memLimitBytes converts MB to the debug.SetMemoryLimit argument (<= 0 =
// unlimited).
func memLimitBytes(mb float64) int64 {
	if mb <= 0 {
		return -1
	}
	return int64(mb * (1 << 20))
}

// writeMemGorProfiles writes the heap and goroutine profiles to the given paths
// (skipping empty ones). Called after the CPU profile has been stopped.
func writeMemGorProfiles(memPath, gorPath string) {
	if memPath != "" {
		f, err := os.Create(memPath)
		if err != nil {
			errExitf("Error: --memprofile: %v", err)
		}
		runtime.GC()
		pprof.WriteHeapProfile(f)
		f.Close()
	}
	if gorPath != "" {
		f, err := os.Create(gorPath)
		if err != nil {
			errExitf("Error: --gorprofile: %v", err)
		}
		if p := pprof.Lookup("goroutine"); p != nil {
			p.WriteTo(f, 1)
		}
		f.Close()
	}
}

func readStdin() ([]byte, error) {
	stat, err := os.Stdin.Stat()
	if err != nil {
		return nil, err
	}
	if (stat.Mode() & os.ModeCharDevice) != 0 {
		return nil, fmt.Errorf("no input data (pipe YAML into stdin)")
	}
	return io.ReadAll(os.Stdin)
}
