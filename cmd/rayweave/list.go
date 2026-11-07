package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/hiroki/rayweaver/internal/clispec"
	"github.com/hiroki/rayweaver/internal/types"
)

// runList implements the `list` subcommand: a read-only, human-readable
// listing of the input system's definition data (surfaces, glasses, paraxial
// properties, ray results). It never traces rays and prints formatted tables
// by default, with --yaml/--json/--csv variants for automation. Without
// targets, surfaces, glasses and paraxial are shown.
//
//	rayweave list [--format table|yaml|json|csv] [--config ID]
//	              [--glass-dir DIR] [--curvature] [TARGET...] < input.yaml
func runList(data []byte) {
	args := splitFlagsAndPositional(os.Args[2:])
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	opts := clispec.Bind(clispec.List, fs)
	if err := opts.Parse(args.flags); err != nil {
		errExitf("Error: %v", err)
	}
	configFlag := opts.String("config")

	switch opts.String("format") {
	case "table", "yaml", "json", "csv":
	default:
		errExitf("Error: unknown --format %q (supported: table, yaml, json, csv)", opts.String("format"))
	}

	// An escape/pso JSONL run log is detected before any YAML parsing: it
	// carries no system definition, so only the global target can be served
	// from it (a bare `list < run.jsonl` therefore lists the escape run).
	if looksLikeGlobalJSONL(data) {
		runListGlobalLog(data, args.positional, opts.String("format"))
		return
	}
	// JSON Lines that carries no escape/pso event (an `optimize --log` stream,
	// say) must not fall through to the YAML parser: it would either error with
	// a confusing message or, when the lines happen to parse as YAML scalars,
	// report "no global result" with exit 0.
	if looksLikeJSONLObjects(data) {
		errExitf("Error: input is JSON Lines but carries no escape/pso event; `list` cannot read it (use `query --jsonl` for a generic JSONL stream)")
	}

	// defaultListTargets is the target set of the bare `list` invocation; the
	// explicit "default" keyword expands to it in place.
	defaultListTargets := []string{"surfaces", "glasses", "paraxial", "fields"}
	// allListTargets is the full target set; the "all" keyword expands to it
	// and implies --auto-aperture, --all-glasses, and --roles.
	allListTargets := []string{"surfaces", "glasses", "paraxial", "fields", "wavelengths", "rays", "merit", "optimization", "global", "focus"}

	targets := args.positional
	if len(targets) == 0 {
		targets = append([]string(nil), defaultListTargets...)
	} else {
		// Expand "default" and "all" keywords in place, preserving user
		// order (e.g. "list default merit" shows the default set, then
		// merit; "list all merit" shows all targets, then merit).
		expanded := make([]string, 0, len(targets)+len(allListTargets))
		for _, t := range targets {
			switch t {
			case "default":
				expanded = append(expanded, defaultListTargets...)
			case "all":
				expanded = append(expanded, allListTargets...)
			default:
				expanded = append(expanded, t)
			}
		}
		targets = expanded
	}

	// "all" implies --auto-aperture, --all-glasses, and --roles.
	for _, t := range args.positional {
		if t == "all" {
			opts.SetBool("auto-aperture", true)
			opts.SetBool("all-glasses", true)
			opts.SetBool("roles", true)
			break
		}
	}

	needsOutput := false
	for _, t := range targets {
		if t == "rays" || t == "merit" || t == "optimization" || t == "global" || t == "focus" {
			needsOutput = true
		}
	}

	var input types.Input
	var output types.Output
	if needsOutput {
		output = parseYAML[types.Output](data)
		input = output.Input
	} else {
		input = parseYAML[types.Input](data)
	}

	gc, _ := loadCatalogs(&input, opts.String("glass-dir"))
	surfaces := configSurfaces(input.Configs, &configFlag)

	printed := map[string]bool{}
	first := true
	for _, target := range targets {
		if printed[target] {
			continue
		}
		printed[target] = true
		if !first && (opts.String("format") == "table" || opts.String("format") == "csv") {
			fmt.Println()
		}
		first = false
		switch target {
		case "surfaces":
			listSurfaces(surfaces, gc, opts.Bool("curvature"), opts.Bool("auto-aperture"), opts.String("format"), input.Configs, configFlag != "")
		case "glasses":
			listGlasses(surfaces, input, gc, opts.String("format"), opts.Bool("all-glasses"))
		case "paraxial":
			listParaxial(data, input, gc, opts.String("format"), configFlag, opts.Bool("roles"))
		case "rays":
			listRays(output, opts.Bool("summary"), opts.String("format"))
		case "fields":
			listFields(input, opts.String("format"), opts.String("glass-dir"), configFlag)
		case "wavelengths":
			listWavelengths(input, opts.String("format"), configFlag)
		case "merit":
			listMerit(input, output, opts.String("format"))
		case "optimization":
			listOptimization(input, output, opts.String("format"))
		case "global":
			listGlobal(output, opts.String("format"))
		case "focus":
			listFocus(output, opts.String("format"))
		case "summary":
			listSummary(data, opts.String("format"))
		default:
			errExitf("Error: unknown list target %q (supported: surfaces, glasses, paraxial, fields, wavelengths, rays, merit, optimization, global, focus, summary, \"default\", or \"all\")", target)
		}
	}
}

// splitFlagsAndPositional separates value-taking flags from positional target
// arguments so flags may appear after the targets (the stdlib FlagSet stops
// parsing at the first non-flag argument). Boolean flags consume no value;
// --format/--config/--glass-dir consume the following token. `--flag=value`
// forms are single tokens and need no special casing.
func splitFlagsAndPositional(args []string) flagAndPositional {
	var out flagAndPositional
	valueFlags := map[string]bool{
		"-format": true, "--format": true,
		"-config": true, "--config": true,
		"-glass-dir": true, "--glass-dir": true,
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") && a != "-" {
			out.flags = append(out.flags, a)
			if valueFlags[a] && i+1 < len(args) {
				i++
				out.flags = append(out.flags, args[i])
			}
			continue
		}
		out.positional = append(out.positional, a)
	}
	return out
}

// formatTableFloat formats a value for fixed-width table cells: 6 decimal
// places in the readable range, scientific notation outside it
// (|x| < 0.001 or |x| > 9999.999999).
func formatTableFloat(v float64) string {
	if v == 0 {
		return "0"
	}
	a := math.Abs(v)
	if a < 1e-3 || a > 9999.999999 {
		return fmt.Sprintf("%.4e", v)
	}
	return fmt.Sprintf("%.6f", v)
}

// renderTable lays out columns padded to their widest cell (headers included),
// numeric columns right-aligned, string columns left-aligned, two spaces
// between columns.
func renderTable(cols []tableColumn) string {
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = utf8.RuneCountInString(c.header)
		for _, cell := range c.cells {
			if n := utf8.RuneCountInString(cell); n > widths[i] {
				widths[i] = n
			}
		}
	}
	var sb strings.Builder
	for i, c := range cols {
		if i > 0 {
			sb.WriteString("  ")
		}
		sb.WriteString(padCell(c.header, widths[i], c.right))
	}
	sb.WriteString("\n")
	for r := range cols[0].cells {
		for i, c := range cols {
			if i > 0 {
				sb.WriteString("  ")
			}
			sb.WriteString(padCell(c.cells[r], widths[i], c.right))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// padCell pads s with spaces to width w, aligning right when requested.
func padCell(s string, w int, right bool) string {
	pad := w - utf8.RuneCountInString(s)
	if pad <= 0 {
		return s
	}
	spaces := strings.Repeat(" ", pad)
	if right {
		return spaces + s
	}
	return s + spaces
}
