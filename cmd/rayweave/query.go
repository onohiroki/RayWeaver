package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// runQuery implements the `query` subcommand: a read-only YAML/JSONL
// selector that turns a shell pipeline into plain-text values (or, with
// --yaml/--json/--csv, structured output). It is designed to replace the
// `python3 -c "import yaml; ..."` snippets that the sample demo scripts
// previously used, so that the demos depend only on the rayweave binary.
//
// Overview of the CLI surface (see docs/query.md for the full manual):
//
//	rayweave query [--yaml|--json|--csv PATH[:col,...]] [--printf FMT]
//	               [--each PATH[:col,...]] [--sum|--product|--count|--len PATH]
//	               [--jsonl [--where EXPR] [--first]] [--set VAR=EXPR]
//	               [--gate EXPR] [--default STR] [--expr EXPR] [-r] [SELECTOR]
//
// SELECTOR is an expression. Paths (e.g. paraxial_result.focal_length,
// chief_rays[0].spot_stats.rms_r, chief_rays[field_angle=0].spot_stats.rms_r)
// are a subset of the expression language, which also supports arithmetic,
// math functions, comparisons, and {..}/[..] literals.
func runQuery(data []byte) {
	fs := flag.NewFlagSet("query", flag.ExitOnError)
	yamlOut := fs.Bool("yaml", false, "output the result as YAML")
	jsonOut := fs.Bool("json", false, "output the result as JSON")
	csvArg := fs.String("csv", "", "CSV output: iterate PATH and emit one row per element (PATH[:col1,col2,...])")
	csvHeader := fs.Bool("csv-header", false, "with --csv, emit a header row with the column names")
	eachArg := fs.String("each", "", "iterate PATH and print one row per element (PATH[:col1,col2,...])")
	countArg := fs.String("count", "", "count non-null values resolved by PATH")
	lenArg := fs.String("len", "", "length of the array/mapping resolved by PATH")
	sumArg := fs.String("sum", "", "sum of the numeric values resolved by PATH")
	productArg := fs.String("product", "", "product of the numeric values resolved by PATH")
	stdevArg := fs.String("stdev", "", "population standard deviation of the numeric values resolved by PATH")
	jsonlMode := fs.Bool("jsonl", false, "read JSON Lines instead of a single YAML document (optimize/escape --log output)")
	whereExpr := fs.String("where", "", "with --jsonl, keep only lines where EXPR is truthy")
	first := fs.Bool("first", false, "with --jsonl, use the first matching line instead of the last")
	gateExpr := fs.String("gate", "", "evaluate EXPR, print a pass record and exit 0/1 by truthiness")
	printfFmt := fs.String("printf", "", "Go fmt format string applied to the output value(s)")
	defaultStr := fs.String("default", "-1", "value printed when a scalar result is missing/null (default -1)")
	exprArg := fs.String("expr", "", "expression to evaluate (same as the positional SELECTOR)")
	var setFlags multiSet
	fs.Var(&setFlags, "set", "bind VAR=EXPR (repeatable, evaluated in order; later bindings may reference earlier ones)")
	raw := fs.Bool("raw", false, "raw text output (the default for scalars; accepted for clarity)")
	rShort := fs.Bool("r", false, "shorthand for --raw")
	fs.Parse(os.Args[2:])

	if *yamlOut && *jsonOut {
		errOut("Error: --yaml and --json are mutually exclusive")
		os.Exit(1)
	}
	if *csvArg != "" && *eachArg != "" {
		errOut("Error: --csv and --each are mutually exclusive")
		os.Exit(1)
	}
	if *exprArg != "" && len(fs.Args()) > 0 {
		errOut("Error: --expr and a positional SELECTOR are mutually exclusive")
		os.Exit(1)
	}
	agg := ""
	aggPath := ""
	for _, a := range []struct{ name, val string }{
		{"count", *countArg}, {"len", *lenArg}, {"sum", *sumArg}, {"product", *productArg}, {"stdev", *stdevArg},
	} {
		if a.val == "" {
			continue
		}
		if agg != "" {
			errOut("Error: --count/--len/--sum/--product/--stdev are mutually exclusive")
			os.Exit(1)
		}
		agg, aggPath = a.name, a.val
	}
	if *whereExpr != "" && !*jsonlMode {
		errOut("Error: --where requires --jsonl")
		os.Exit(1)
	}

	selector := *exprArg
	if selector == "" && len(fs.Args()) > 0 {
		selector = fs.Args()[0]
	}

	rawMode := *raw || *rShort

	// ---- 1. Load the document(s) ----
	var docRoot any  // used by SELECTOR / --set / --gate
	var iterRoot any // used by --each / --csv / aggregates
	if *jsonlMode {
		lines := parseJSONL(data)
		for i := range lines {
			lines[i] = normalizeNumbers(lines[i])
		}
		filtered := lines
		if *whereExpr != "" {
			filtered = filtered[:0:0]
			for _, ln := range lines {
				ok, err := evalExpr(*whereExpr, &evalCtx{root: ln})
				if err != nil {
					errOut("Error in --where expression: %v", err)
					os.Exit(1)
				}
				if truthy(ok) {
					filtered = append(filtered, ln)
				}
			}
		}
		iterRoot = filtered
		if len(filtered) > 0 {
			if *first {
				docRoot = filtered[0]
			} else {
				docRoot = filtered[len(filtered)-1]
			}
		}
	} else {
		var root any
		if err := yaml.Unmarshal(data, &root); err != nil {
			errOut("Error parsing YAML: %v", err)
			os.Exit(1)
		}
		root = normalizeNumbers(root)
		docRoot = root
		iterRoot = root
	}

	// ---- 2. Evaluate bindings in order ----
	bindings := map[string]any{}
	for _, kv := range setFlags {
		eq := strings.Index(kv, "=")
		if eq <= 0 {
			errOut("Error: --set expects VAR=EXPR (got %q)", kv)
			os.Exit(1)
		}
		v, err := evalExpr(kv[eq+1:], &evalCtx{root: docRoot, bindings: bindings})
		if err != nil {
			errOut("Error in --set %s: %v", kv[:eq], err)
			os.Exit(1)
		}
		bindings[kv[:eq]] = v
	}
	ctx := &evalCtx{root: docRoot, bindings: bindings}

	resolve := func(path string) (any, error) {
		if *jsonlMode && (path == "[]" || path == ".") {
			return iterRoot, nil
		}
		return evalExpr(path, ctx)
	}

	// ---- 3. Dispatch ----
	switch {
	case *csvArg != "":
		runQueryCSV(*csvArg, resolve, *csvHeader, *defaultStr, *printfFmt)
	case *eachArg != "":
		runQueryEach(*eachArg, resolve, *defaultStr, *printfFmt, *yamlOut, *jsonOut)
	case agg != "":
		runQueryAgg(agg, aggPath, resolve, *defaultStr, *printfFmt)
	case *gateExpr != "":
		runQueryGate(*gateExpr, ctx, bindings, *yamlOut, *jsonOut, *defaultStr)
	default:
		var result any
		var err error
		if selector != "" {
			result, err = evalExpr(selector, ctx)
		} else if len(bindings) > 0 {
			result = bindings
		} else {
			errOut("Error: nothing to evaluate (give a SELECTOR, --expr, --set, --each, --csv or an aggregate)")
			os.Exit(1)
		}
		if err != nil {
			errOut("Error evaluating expression: %v", err)
			os.Exit(1)
		}
		emitResult(result, *defaultStr, *printfFmt, *yamlOut, *jsonOut, rawMode)
	}
}

// ---- result emission ------------------------------------------------------

// emitResult prints a single selector / record result. Text mode unwraps
// single-element arrays and prints larger arrays as a bracketed list; maps
// require --yaml/--json.
func emitResult(v any, defaultStr, printfFmt string, yamlOut, jsonOut, raw bool) {
	if v == nil {
		fmt.Println(defaultStr)
		return
	}
	if a, ok := v.([]any); ok {
		if len(a) == 0 {
			fmt.Println(defaultStr)
			return
		}
		if len(a) == 1 {
			v = a[0]
		}
	}
	if yamlOut {
		emitYAML(v)
		return
	}
	if jsonOut {
		emitJSON(v)
		return
	}
	if _, ok := v.(map[string]any); ok {
		errOut("Error: result is a mapping; use --yaml or --json to print it")
		os.Exit(1)
	}
	emitScalar(v, printfFmt)
}

// emitScalar prints one scalar value, optionally through a printf format.
func emitScalar(v any, printfFmt string) {
	if printfFmt != "" {
		fmt.Println(formatPrintf(printfFmt, []any{v}))
		return
	}
	fmt.Println(formatValue(v))
}

func emitYAML(v any) {
	data, err := yaml.Marshal(v)
	if err != nil {
		errOut("Error marshaling YAML: %v", err)
		os.Exit(1)
	}
	os.Stdout.Write(data)
}

func emitJSON(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		errOut("Error marshaling JSON: %v", err)
		os.Exit(1)
	}
	fmt.Println(string(data))
}

// ---- --each ------------------------------------------------------------------

// runQueryEach iterates the array/mapping at PATH (PATH[:col1,col2,...]) and
// prints one row per element. Missing columns print --default in text mode;
// with --yaml/--json the rows are emitted as a structured list.
func runQueryEach(arg string, resolve func(string) (any, error), defaultStr, printfFmt string, yamlOut, jsonOut bool) {
	path, cols := splitEachArg(arg)
	v, err := resolve(path)
	if err != nil {
		errOut("Error evaluating --each path: %v", err)
		os.Exit(1)
	}
	var elems []any
	switch t := v.(type) {
	case nil:
		return
	case []any:
		elems = flattenEach(t)
	case map[string]any:
		elems = mapEntries(t)
	default:
		elems = []any{t}
	}

	var rows []any
	writeRow := func(row any) {
		if yamlOut || jsonOut {
			rows = append(rows, row)
		}
	}

	for _, e := range elems {
		if len(cols) == 0 {
			// No columns: emit the element itself (or the value of a map entry).
			if m, ok := e.(map[string]any); ok {
				if _, isEntry := m["key"]; isEntry {
					if len(m) == 2 {
						e = m["value"]
					}
				}
			}
			if yamlOut || jsonOut {
				writeRow(e)
			} else {
				if _, ok := e.(map[string]any); ok {
					errOut("Error: element is a mapping; give columns (PATH:col1,col2,...) or use --yaml/--json")
					os.Exit(1)
				}
				emitScalar(e, printfFmt)
			}
			continue
		}
		elemCtx := &evalCtx{root: e}
		row := make(map[string]any, len(cols))
		vals := make([]any, len(cols))
		for i, col := range cols {
			cv, err := evalExpr(col, elemCtx)
			if err != nil {
				errOut("Error evaluating column %q: %v", col, err)
				os.Exit(1)
			}
			if cv == nil {
				cv = defaultStr
			}
			row[col] = cv
			vals[i] = cv
		}
		if yamlOut || jsonOut {
			writeRow(row)
			continue
		}
		if printfFmt != "" {
			fmt.Println(formatPrintf(printfFmt, vals))
		} else {
			parts := make([]string, len(vals))
			for i, x := range vals {
				parts[i] = formatValue(x)
			}
			fmt.Println(strings.Join(parts, " "))
		}
	}
	if yamlOut || jsonOut {
		if yamlOut {
			emitYAML(rows)
		} else {
			emitJSON(rows)
		}
	}
}

// ---- --csv -------------------------------------------------------------------

// runQueryCSV iterates the array at PATH and emits CSV rows. Rows where any
// column is missing/null are skipped (matching jq's `select(.x != null)`),
// which is what the spot-diagram demos need. Header is optional.
func runQueryCSV(arg string, resolve func(string) (any, error), header bool, defaultStr, printfFmt string) {
	path, cols := splitEachArg(arg)
	if len(cols) == 0 {
		errOut("Error: --csv requires columns (PATH:col1,col2,...)")
		os.Exit(1)
	}
	v, err := resolve(path)
	if err != nil {
		errOut("Error evaluating --csv path: %v", err)
		os.Exit(1)
	}
	var elems []any
	switch t := v.(type) {
	case nil:
		return
	case []any:
		elems = flattenEach(t)
	case map[string]any:
		elems = mapEntries(t)
	default:
		elems = []any{t}
	}

	if header {
		fmt.Println(strings.Join(quoteCSV(cols), ","))
	}
	for _, e := range elems {
		elemCtx := &evalCtx{root: e}
		vals := make([]any, len(cols))
		skip := false
		for i, col := range cols {
			cv, err := evalExpr(col, elemCtx)
			if err != nil {
				errOut("Error evaluating column %q: %v", col, err)
				os.Exit(1)
			}
			if cv == nil {
				skip = true
				break
			}
			vals[i] = cv
		}
		if skip {
			continue
		}
		cells := make([]string, len(vals))
		for i, x := range vals {
			cells[i] = quoteCSV([]string{formatValue(x)})[0]
		}
		fmt.Println(strings.Join(cells, ","))
	}
}

func quoteCSV(cells []string) []string {
	out := make([]string, len(cells))
	for i, c := range cells {
		if strings.ContainsAny(c, ",\"\n") {
			out[i] = "\"" + strings.ReplaceAll(c, "\"", "\"\"") + "\""
		} else {
			out[i] = c
		}
	}
	return out
}

// ---- aggregates ---------------------------------------------------------------

func runQueryAgg(agg, path string, resolve func(string) (any, error), defaultStr, printfFmt string) {
	v, err := resolve(path)
	if err != nil {
		errOut("Error evaluating --%s path: %v", agg, err)
		os.Exit(1)
	}
	switch agg {
	case "len":
		if v == nil {
			fmt.Println(defaultStr)
			return
		}
		n := 0.0
		switch t := v.(type) {
		case []any:
			n = float64(len(t))
		case map[string]any:
			n = float64(len(t))
		case string:
			n = float64(len([]rune(t)))
		default:
			fmt.Println(defaultStr)
			return
		}
		emitScalar(n, printfFmt)
	case "count":
		if v == nil {
			fmt.Println(defaultStr)
			return
		}
		var n float64
		switch t := v.(type) {
		case []any:
			for _, e := range t {
				if e != nil {
					n++
				}
			}
		case map[string]any:
			n = 1
		default:
			n = 1
		}
		emitScalar(n, printfFmt)
	case "sum", "product":
		if v == nil {
			fmt.Println(defaultStr)
			return
		}
		a, ok := v.([]any)
		if !ok {
			f, isNum := asNum(v)
			if !isNum {
				fmt.Println(defaultStr)
				return
			}
			emitScalar(f, printfFmt)
			return
		}
		acc := 0.0
		if agg == "product" {
			acc = 1.0
		}
		found := false
		for _, e := range a {
			f, isNum := asNum(e)
			if !isNum {
				continue
			}
			found = true
			if agg == "sum" {
				acc += f
			} else {
				acc *= f
			}
		}
		if !found && agg == "sum" {
			acc = 0
		}
		emitScalar(acc, printfFmt)
	case "stdev":
		if v == nil {
			fmt.Println(defaultStr)
			return
		}
		a, ok := v.([]any)
		if !ok {
			fmt.Println(defaultStr)
			return
		}
		var xs []float64
		for _, e := range a {
			if f, isNum := asNum(e); isNum {
				xs = append(xs, f)
			}
		}
		if len(xs) == 0 {
			fmt.Println(defaultStr)
			return
		}
		mean := 0.0
		for _, x := range xs {
			mean += x
		}
		mean /= float64(len(xs))
		ss := 0.0
		for _, x := range xs {
			ss += (x - mean) * (x - mean)
		}
		emitScalar(math.Sqrt(ss/float64(len(xs))), printfFmt)
	}
}

// ---- --gate ------------------------------------------------------------------

func runQueryGate(gate string, ctx *evalCtx, bindings map[string]any, yamlOut, jsonOut bool, defaultStr string) {
	v, err := evalExpr(gate, ctx)
	if err != nil {
		errOut("Error in --gate expression: %v", err)
		os.Exit(1)
	}
	pass := truthy(v)
	if yamlOut || jsonOut {
		rec := make(map[string]any, len(bindings)+1)
		for k, bv := range bindings {
			rec[k] = bv
		}
		rec["pass"] = pass
		if yamlOut {
			emitYAML(rec)
		} else {
			emitJSON(rec)
		}
	} else {
		if v == nil {
			fmt.Println(defaultStr)
		} else {
			emitScalar(v, "")
		}
	}
	if pass {
		os.Exit(0)
	}
	os.Exit(1)
}

// ---- shared helpers -----------------------------------------------------------

// splitEachArg splits "PATH:a,b,c" into its path and column list. Without a
// colon the column list is empty.
func splitEachArg(arg string) (string, []string) {
	i := strings.Index(arg, ":")
	if i < 0 {
		return arg, nil
	}
	var cols []string
	for _, c := range strings.Split(arg[i+1:], ",") {
		c = strings.TrimSpace(c)
		if c != "" {
			cols = append(cols, c)
		}
	}
	return arg[:i], cols
}

func parseJSONL(data []byte) []any {
	var out []any
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var v any
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			errOut("Warning: skipping invalid JSON line: %v", err)
			continue
		}
		out = append(out, v)
	}
	return out
}

// flattenEach flattens one level of nesting, so `results[].surfaces[]`
// iterates every surface of every result.
func flattenEach(elems []any) []any {
	out := elems
	if len(elems) > 0 {
		if _, ok := elems[0].([]any); ok {
			out = []any{}
			for _, e := range elems {
				if a, ok := e.([]any); ok {
					out = append(out, a...)
				}
			}
		}
	}
	return out
}

func mapEntries(m map[string]any) []any {
	keys := sortedKeys(m)
	out := make([]any, len(keys))
	for i, k := range keys {
		out[i] = map[string]any{"key": k, "value": m[k]}
	}
	return out
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func asNum(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case uint64:
		return float64(t), true
	case float32:
		return float64(t), true
	}
	return 0, false
}

// normalizeNumbers converts every integer decoded from YAML/JSON into a
// float64 so arithmetic, comparisons and printf all see a uniform number
// type (yaml.v3 decodes YAML integers as int, unlike JSON's float64).
func normalizeNumbers(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			t[k] = normalizeNumbers(val)
		}
		return t
	case []any:
		for i, val := range t {
			t[i] = normalizeNumbers(val)
		}
		return t
	case int:
		return float64(t)
	case int8:
		return float64(t)
	case int16:
		return float64(t)
	case int32:
		return float64(t)
	case int64:
		return float64(t)
	case uint:
		return float64(t)
	case uint8:
		return float64(t)
	case uint16:
		return float64(t)
	case uint32:
		return float64(t)
	case uint64:
		return float64(t)
	case float32:
		return float64(t)
	default:
		return v
	}
}

func asMap(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func asArr(v any) ([]any, bool) {
	a, ok := v.([]any)
	return a, ok
}

func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case float64:
		return t != 0
	case string:
		return t != ""
	case []any:
		return len(t) > 0
	case map[string]any:
		return len(t) > 0
	}
	return false
}

func equalVal(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	af, aok := asNum(a)
	bf, bok := asNum(b)
	if aok && bok {
		return af == bf
	}
	if aok != bok {
		return false
	}
	as, aok := a.(string)
	bs, bok := b.(string)
	if aok && bok {
		return as == bs
	}
	ab, aok := a.(bool)
	bb, bok := b.(bool)
	if aok && bok {
		return ab == bb
	}
	return false
}

func formatValue(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64)
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case []any:
		if len(t) == 1 {
			return formatValue(t[0])
		}
		parts := make([]string, len(t))
		for i, e := range t {
			parts[i] = formatValue(e)
		}
		return "[" + strings.Join(parts, " ") + "]"
	default:
		return fmt.Sprint(v)
	}
}

// formatPrintf applies a Go fmt format string to vals, converting integral
// float64 values to int64 for integer verbs (%d/%x/...) while leaving them
// as float64 for floating-point verbs (%f/%e/%g/...).
func formatPrintf(format string, vals []any) string {
	verbs := scanVerbs(format)
	args := make([]any, len(vals))
	ai := 0
	for _, vk := range verbs {
		if vk == verbNone || ai >= len(vals) {
			continue
		}
		args[ai] = convArg(vals[ai], vk)
		ai++
	}
	return fmt.Sprintf(format, args...)
}

type verbKind int

const (
	verbNone verbKind = iota
	verbInt
	verbFloat
	verbAny
)

// scanVerbs walks a fmt format string and reports the kind of each value
// verb in order (%% consumes no argument).
func scanVerbs(format string) []verbKind {
	var out []verbKind
	i, n := 0, len(format)
	for i < n {
		if format[i] != '%' {
			i++
			continue
		}
		i++
		if i < n && format[i] == '%' {
			i++
			out = append(out, verbNone)
			continue
		}
		for i < n && strings.ContainsRune("-+ #0", rune(format[i])) {
			i++
		}
		for i < n && (format[i] >= '0' && format[i] <= '9') {
			i++
		}
		if i < n && format[i] == '*' {
			i++
			out = append(out, verbInt)
		}
		if i < n && format[i] == '.' {
			i++
			if i < n && format[i] == '*' {
				i++
				out = append(out, verbInt)
			}
			for i < n && (format[i] >= '0' && format[i] <= '9') {
				i++
			}
		}
		if i >= n {
			out = append(out, verbAny)
			break
		}
		c := format[i]
		i++
		switch c {
		case 'd', 'o', 'O', 'x', 'X', 'b', 'c', 'U':
			out = append(out, verbInt)
		case 'f', 'F', 'e', 'E', 'g', 'G':
			out = append(out, verbFloat)
		default:
			out = append(out, verbAny)
		}
	}
	return out
}

// convArg prepares a value for a fmt verb: whole numbers become int64 for
// integer verbs, stay float64 for floating-point verbs, and become their
// decimal string for generic verbs (%s/%v/%q) so `%s` never sees an int64.
func convArg(v any, k verbKind) any {
	switch k {
	case verbFloat:
		return v
	case verbInt:
		return printfArg(v)
	default:
		if f, ok := v.(float64); ok {
			if !math.IsInf(f, 0) && !math.IsNaN(f) && f == math.Trunc(f) {
				return strconv.FormatFloat(f, 'g', -1, 64)
			}
			return v
		}
		return v
	}
}

// printfArg converts whole float64 values to int64 so integer verbs work,
// while keeping fractional values as float64.
func printfArg(v any) any {
	if f, ok := v.(float64); ok {
		if !math.IsInf(f, 0) && !math.IsNaN(f) && f == math.Trunc(f) && math.Abs(f) < 1e15 {
			return int64(f)
		}
	}
	return v
}

func lenOf(v any) int {
	switch t := v.(type) {
	case nil:
		return 0
	case []any:
		return len(t)
	case map[string]any:
		return len(t)
	case string:
		return len([]rune(t))
	}
	return 1
}

// ---- expression language ------------------------------------------------------

type evalCtx struct {
	root     any
	bindings map[string]any
}

type expr interface{}

type numNode struct{ v float64 }
type strNode struct{ v string }
type boolNode struct{ v bool }
type nilNode struct{}
type identNode struct{ name string }
type callNode struct {
	name string
	args []expr
}
type dotNode struct {
	x   expr
	key string
}
type idxNode struct {
	x expr
	n float64
}
type filterNode struct {
	x   expr
	key string
	val expr
}
type mapNode struct{ x expr }
type binNode struct {
	op   string
	l, r expr
}
type unNode struct {
	op string
	x  expr
}
type condNode struct{ cond, a, b expr }
type structNode struct {
	keys []string
	vals []expr
}
type arrNode struct{ vals []expr }

func evalExpr(src string, ctx *evalCtx) (any, error) {
	toks, err := lexQuery(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	n, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.cur().kind != "eof" {
		return nil, fmt.Errorf("unexpected trailing input %q", p.cur().text)
	}
	return ctx.eval(n)
}

func (c *evalCtx) eval(n expr) (any, error) {
	switch t := n.(type) {
	case *numNode:
		return t.v, nil
	case *strNode:
		return t.v, nil
	case *boolNode:
		return t.v, nil
	case *nilNode:
		return nil, nil
	case *identNode:
		if b, ok := c.bindings[t.name]; ok {
			return b, nil
		}
		if t.name == "pi" {
			return math.Pi, nil
		}
		if t.name == "e" {
			return math.E, nil
		}
		if m, ok := asMap(c.root); ok {
			if v, ok := m[t.name]; ok {
				return v, nil
			}
		}
		return nil, nil
	case *dotNode:
		v, err := c.eval(t.x)
		if err != nil {
			return nil, err
		}
		if v == nil {
			return nil, nil
		}
		if m, ok := asMap(v); ok {
			return m[t.key], nil
		}
		if a, ok := asArr(v); ok {
			out := make([]any, len(a))
			for i, e := range a {
				if em, ok := asMap(e); ok {
					out[i] = em[t.key]
				} else {
					out[i] = nil
				}
			}
			return out, nil
		}
		return nil, nil
	case *idxNode:
		v, err := c.eval(t.x)
		if err != nil {
			return nil, err
		}
		a, ok := asArr(v)
		if !ok {
			return nil, nil
		}
		i := int(t.n)
		if i < 0 {
			i += len(a)
		}
		if i < 0 || i >= len(a) {
			return nil, nil
		}
		return a[i], nil
	case *filterNode:
		v, err := c.eval(t.x)
		if err != nil {
			return nil, err
		}
		a, ok := asArr(v)
		if !ok {
			return nil, nil
		}
		fv, err := c.eval(t.val)
		if err != nil {
			return nil, err
		}
		var out []any
		for _, e := range a {
			if em, ok := asMap(e); ok {
				if equalVal(em[t.key], fv) {
					out = append(out, e)
				}
			}
		}
		// A filter that matches exactly one element unwraps to that element,
		// so `configs[id=config1].surfaces[2].thickness` walks directly
		// instead of mapping over a one-element array.
		if len(out) == 1 {
			return out[0], nil
		}
		return out, nil
	case *mapNode:
		v, err := c.eval(t.x)
		if err != nil {
			return nil, err
		}
		if a, ok := asArr(v); ok {
			return a, nil
		}
		if m, ok := asMap(v); ok {
			keys := sortedKeys(m)
			out := make([]any, len(keys))
			for i, k := range keys {
				out[i] = m[k]
			}
			return out, nil
		}
		return nil, nil
	case *binNode:
		return c.evalBin(t)
	case *unNode:
		return c.evalUn(t)
	case *condNode:
		cv, err := c.eval(t.cond)
		if err != nil {
			return nil, err
		}
		if truthy(cv) {
			return c.eval(t.a)
		}
		return c.eval(t.b)
	case *callNode:
		return c.evalCall(t)
	case *structNode:
		m := make(map[string]any, len(t.keys))
		for i, k := range t.keys {
			v, err := c.eval(t.vals[i])
			if err != nil {
				return nil, err
			}
			m[k] = v
		}
		return m, nil
	case *arrNode:
		out := make([]any, len(t.vals))
		for i, v := range t.vals {
			x, err := c.eval(v)
			if err != nil {
				return nil, err
			}
			out[i] = x
		}
		return out, nil
	}
	return nil, fmt.Errorf("unknown expression node %T", n)
}

func (c *evalCtx) evalBin(t *binNode) (any, error) {
	switch t.op {
	case "&&":
		l, err := c.eval(t.l)
		if err != nil {
			return nil, err
		}
		if !truthy(l) {
			return false, nil
		}
		r, err := c.eval(t.r)
		if err != nil {
			return nil, err
		}
		return truthy(r), nil
	case "||":
		l, err := c.eval(t.l)
		if err != nil {
			return nil, err
		}
		if truthy(l) {
			return true, nil
		}
		r, err := c.eval(t.r)
		if err != nil {
			return nil, err
		}
		return truthy(r), nil
	}
	l, err := c.eval(t.l)
	if err != nil {
		return nil, err
	}
	r, err := c.eval(t.r)
	if err != nil {
		return nil, err
	}
	switch t.op {
	case "==":
		return equalVal(l, r), nil
	case "!=":
		return !equalVal(l, r), nil
	case "<", "<=", ">", ">=":
		lf, lok := asNum(l)
		rf, rok := asNum(r)
		if !lok || !rok {
			return false, nil
		}
		switch t.op {
		case "<":
			return lf < rf, nil
		case "<=":
			return lf <= rf, nil
		case ">":
			return lf > rf, nil
		case ">=":
			return lf >= rf, nil
		}
		return nil, nil
	}
	lf, lok := asNum(l)
	rf, rok := asNum(r)
	if !lok || !rok {
		return nil, nil
	}
	switch t.op {
	case "+":
		return lf + rf, nil
	case "-":
		return lf - rf, nil
	case "*":
		return lf * rf, nil
	case "/":
		if rf == 0 {
			return nil, fmt.Errorf("division by zero")
		}
		return lf / rf, nil
	case "%":
		if rf == 0 {
			return nil, fmt.Errorf("modulo by zero")
		}
		return math.Mod(lf, rf), nil
	}
	return nil, fmt.Errorf("unknown operator %q", t.op)
}

func (c *evalCtx) evalUn(t *unNode) (any, error) {
	x, err := c.eval(t.x)
	if err != nil {
		return nil, err
	}
	switch t.op {
	case "!":
		return !truthy(x), nil
	case "-":
		f, ok := asNum(x)
		if !ok {
			return nil, nil
		}
		return -f, nil
	}
	return nil, fmt.Errorf("unknown unary operator %q", t.op)
}

func (c *evalCtx) evalCall(t *callNode) (any, error) {
	name := t.name
	switch name {
	case "has":
		if len(t.args) != 1 {
			return nil, fmt.Errorf("has expects 1 argument")
		}
		k, err := c.eval(t.args[0])
		if err != nil {
			return nil, err
		}
		key, ok := k.(string)
		if !ok {
			return nil, fmt.Errorf("has expects a string key")
		}
		m, ok := asMap(c.root)
		if !ok {
			return false, nil
		}
		_, exists := m[key]
		return exists, nil
	case "len":
		if len(t.args) != 1 {
			return nil, fmt.Errorf("len expects 1 argument")
		}
		v, err := c.eval(t.args[0])
		if err != nil {
			return nil, err
		}
		return float64(lenOf(v)), nil
	}

	args := make([]float64, 0, len(t.args))
	for _, a := range t.args {
		v, err := c.eval(a)
		if err != nil {
			return nil, err
		}
		f, ok := asNum(v)
		if !ok {
			return nil, fmt.Errorf("%s expects numeric arguments", name)
		}
		args = append(args, f)
	}
	need := func(n int) error {
		if len(args) != n {
			return fmt.Errorf("%s expects %d argument(s), got %d", name, n, len(args))
		}
		return nil
	}
	switch name {
	case "abs":
		if err := need(1); err != nil {
			return nil, err
		}
		return math.Abs(args[0]), nil
	case "sqrt":
		if err := need(1); err != nil {
			return nil, err
		}
		return math.Sqrt(args[0]), nil
	case "sin":
		if err := need(1); err != nil {
			return nil, err
		}
		return math.Sin(args[0]), nil
	case "cos":
		if err := need(1); err != nil {
			return nil, err
		}
		return math.Cos(args[0]), nil
	case "tan":
		if err := need(1); err != nil {
			return nil, err
		}
		return math.Tan(args[0]), nil
	case "asin":
		if err := need(1); err != nil {
			return nil, err
		}
		return math.Asin(args[0]), nil
	case "acos":
		if err := need(1); err != nil {
			return nil, err
		}
		return math.Acos(args[0]), nil
	case "atan":
		if err := need(1); err != nil {
			return nil, err
		}
		return math.Atan(args[0]), nil
	case "atan2":
		if err := need(2); err != nil {
			return nil, err
		}
		return math.Atan2(args[0], args[1]), nil
	case "exp":
		if err := need(1); err != nil {
			return nil, err
		}
		return math.Exp(args[0]), nil
	case "log":
		if err := need(1); err != nil {
			return nil, err
		}
		return math.Log(args[0]), nil
	case "floor":
		if err := need(1); err != nil {
			return nil, err
		}
		return math.Floor(args[0]), nil
	case "ceil":
		if err := need(1); err != nil {
			return nil, err
		}
		return math.Ceil(args[0]), nil
	case "round":
		if err := need(1); err != nil {
			return nil, err
		}
		return math.Round(args[0]), nil
	case "radians":
		if err := need(1); err != nil {
			return nil, err
		}
		return args[0] * math.Pi / 180, nil
	case "degrees":
		if err := need(1); err != nil {
			return nil, err
		}
		return args[0] * 180 / math.Pi, nil
	case "pow":
		if err := need(2); err != nil {
			return nil, err
		}
		return math.Pow(args[0], args[1]), nil
	case "min":
		m := args[0]
		for _, a := range args[1:] {
			if a < m {
				m = a
			}
		}
		return m, nil
	case "max":
		m := args[0]
		for _, a := range args[1:] {
			if a > m {
				m = a
			}
		}
		return m, nil
	}
	return nil, fmt.Errorf("unknown function %q", name)
}

// ---- tokenizer -----------------------------------------------------------------

type token struct {
	kind string // num | str | id | op | eof
	text string
	num  float64
}

func lexQuery(src string) ([]token, error) {
	var toks []token
	i, n := 0, len(src)
	for i < n {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '"':
			j := i + 1
			var sb strings.Builder
			closed := false
			for j < n {
				if src[j] == '\\' && j+1 < n {
					sb.WriteByte(src[j+1])
					j += 2
					continue
				}
				if src[j] == '"' {
					closed = true
					break
				}
				sb.WriteByte(src[j])
				j++
			}
			if !closed {
				return nil, fmt.Errorf("unterminated string literal")
			}
			toks = append(toks, token{kind: "str", text: sb.String()})
			i = j + 1
		case (c >= '0' && c <= '9') || (c == '.' && i+1 < n && src[i+1] >= '0' && src[i+1] <= '9'):
			j := i
			for j < n && ((src[j] >= '0' && src[j] <= '9') || src[j] == '.') {
				j++
			}
			if j < n && (src[j] == 'e' || src[j] == 'E') {
				k := j + 1
				if k < n && (src[k] == '+' || src[k] == '-') {
					k++
				}
				if k < n && src[k] >= '0' && src[k] <= '9' {
					for k < n && src[k] >= '0' && src[k] <= '9' {
						k++
					}
					j = k
				}
			}
			f, err := strconv.ParseFloat(src[i:j], 64)
			if err != nil {
				return nil, fmt.Errorf("invalid number %q", src[i:j])
			}
			toks = append(toks, token{kind: "num", text: src[i:j], num: f})
			i = j
		case isIdentStart(c):
			j := i
			for j < n && isIdentPart(src[j]) {
				j++
			}
			toks = append(toks, token{kind: "id", text: src[i:j]})
			i = j
		default:
			if i+1 < n {
				two := src[i : i+2]
				switch two {
				case "==", "!=", "<=", ">=", "&&", "||":
					toks = append(toks, token{kind: "op", text: two})
					i += 2
					continue
				}
			}
			if strings.ContainsRune("+-*/%<>!=()[]{},:?.", rune(c)) {
				toks = append(toks, token{kind: "op", text: string(c)})
				i++
				continue
			}
			return nil, fmt.Errorf("unexpected character %q", string(c))
		}
	}
	toks = append(toks, token{kind: "eof"})
	return toks, nil
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

// ---- parser ---------------------------------------------------------------------

type parser struct {
	toks []token
	pos  int
}

func (p *parser) cur() token {
	return p.toks[p.pos]
}

func (p *parser) advance() token {
	t := p.toks[p.pos]
	if p.pos < len(p.toks)-1 {
		p.pos++
	}
	return t
}

func (p *parser) isOp(text string) bool {
	t := p.cur()
	return t.kind == "op" && t.text == text
}

func (p *parser) parseExpr() (expr, error) {
	cond, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.isOp("?") {
		p.advance()
		a, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if !p.isOp(":") {
			return nil, fmt.Errorf("expected ':' in conditional expression")
		}
		p.advance()
		b, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		return &condNode{cond: cond, a: a, b: b}, nil
	}
	return cond, nil
}

func (p *parser) parseOr() (expr, error) {
	l, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.isOp("||") {
		p.advance()
		r, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		l = &binNode{op: "||", l: l, r: r}
	}
	return l, nil
}

func (p *parser) parseAnd() (expr, error) {
	l, err := p.parseCmp()
	if err != nil {
		return nil, err
	}
	for p.isOp("&&") {
		p.advance()
		r, err := p.parseCmp()
		if err != nil {
			return nil, err
		}
		l = &binNode{op: "&&", l: l, r: r}
	}
	return l, nil
}

func (p *parser) parseCmp() (expr, error) {
	l, err := p.parseAdd()
	if err != nil {
		return nil, err
	}
	for {
		op := ""
		for _, cand := range []string{"==", "!=", "<=", ">=", "<", ">"} {
			if p.isOp(cand) {
				op = cand
				break
			}
		}
		if op == "" {
			break
		}
		p.advance()
		r, err := p.parseAdd()
		if err != nil {
			return nil, err
		}
		l = &binNode{op: op, l: l, r: r}
	}
	return l, nil
}

func (p *parser) parseAdd() (expr, error) {
	l, err := p.parseMul()
	if err != nil {
		return nil, err
	}
	for p.isOp("+") || p.isOp("-") {
		op := p.advance().text
		r, err := p.parseMul()
		if err != nil {
			return nil, err
		}
		l = &binNode{op: op, l: l, r: r}
	}
	return l, nil
}

func (p *parser) parseMul() (expr, error) {
	l, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.isOp("*") || p.isOp("/") || p.isOp("%") {
		op := p.advance().text
		r, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		l = &binNode{op: op, l: l, r: r}
	}
	return l, nil
}

func (p *parser) parseUnary() (expr, error) {
	if p.isOp("-") || p.isOp("!") {
		op := p.advance().text
		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &unNode{op: op, x: x}, nil
	}
	return p.parsePostfix()
}

func (p *parser) parsePostfix() (expr, error) {
	x, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for {
		switch {
		case p.isOp("."):
			p.advance()
			t := p.cur()
			if t.kind != "id" {
				return nil, fmt.Errorf("expected an identifier after '.'")
			}
			p.advance()
			x = &dotNode{x: x, key: t.text}
		case p.isOp("["):
			p.advance()
			if p.isOp("]") {
				p.advance()
				x = &mapNode{x: x}
				continue
			}
			if p.cur().kind == "num" {
				num := p.advance()
				if !p.isOp("]") {
					return nil, fmt.Errorf("expected ']' after index")
				}
				p.advance()
				x = &idxNode{x: x, n: num.num}
				continue
			}
			if p.isOp("-") && len(p.toks) > p.pos+1 && p.toks[p.pos+1].kind == "num" {
				p.advance() // -
				num := p.advance()
				if !p.isOp("]") {
					return nil, fmt.Errorf("expected ']' after index")
				}
				p.advance()
				x = &idxNode{x: x, n: -num.num}
				continue
			}
			if p.cur().kind == "id" {
				key := p.advance()
				if p.isOp("=") {
					p.advance()
					val, err := p.parseFilterValue()
					if err != nil {
						return nil, err
					}
					if !p.isOp("]") {
						return nil, fmt.Errorf("expected ']' after filter value")
					}
					p.advance()
					x = &filterNode{x: x, key: key.text, val: val}
					continue
				}
			}
			return nil, fmt.Errorf("expected index, filter (key=value) or ']' inside '['")
		default:
			return x, nil
		}
	}
}

func (p *parser) parseFilterValue() (expr, error) {
	t := p.cur()
	switch t.kind {
	case "num":
		p.advance()
		return &numNode{v: t.num}, nil
	case "str":
		p.advance()
		return &strNode{v: t.text}, nil
	case "id":
		p.advance()
		switch t.text {
		case "true":
			return &boolNode{v: true}, nil
		case "false":
			return &boolNode{v: false}, nil
		case "null":
			return &nilNode{}, nil
		default:
			return &strNode{v: t.text}, nil
		}
	}
	return nil, fmt.Errorf("expected a filter value")
}

func (p *parser) parsePrimary() (expr, error) {
	t := p.cur()
	switch t.kind {
	case "num":
		p.advance()
		return &numNode{v: t.num}, nil
	case "str":
		p.advance()
		return &strNode{v: t.text}, nil
	case "id":
		p.advance()
		if p.isOp("(") {
			p.advance()
			var args []expr
			if !p.isOp(")") {
				for {
					a, err := p.parseExpr()
					if err != nil {
						return nil, err
					}
					args = append(args, a)
					if p.isOp(",") {
						p.advance()
						continue
					}
					break
				}
			}
			if !p.isOp(")") {
				return nil, fmt.Errorf("expected ')' after arguments")
			}
			p.advance()
			return &callNode{name: t.text, args: args}, nil
		}
		switch t.text {
		case "true":
			return &boolNode{v: true}, nil
		case "false":
			return &boolNode{v: false}, nil
		case "null":
			return &nilNode{}, nil
		}
		return &identNode{name: t.text}, nil
	case "op":
		switch t.text {
		case "(":
			p.advance()
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			if !p.isOp(")") {
				return nil, fmt.Errorf("expected ')'")
			}
			p.advance()
			return e, nil
		case "{":
			return p.parseStruct()
		case "[":
			return p.parseArray()
		}
	}
	return nil, fmt.Errorf("unexpected token %q", t.text)
}

func (p *parser) parseStruct() (expr, error) {
	p.advance() // {
	var keys []string
	var vals []expr
	if !p.isOp("}") {
		for {
			t := p.cur()
			if t.kind != "id" {
				return nil, fmt.Errorf("expected a key in {..} literal")
			}
			p.advance()
			if !p.isOp(":") {
				return nil, fmt.Errorf("expected ':' in {..} literal")
			}
			p.advance()
			v, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			keys = append(keys, t.text)
			vals = append(vals, v)
			if p.isOp(",") {
				p.advance()
				continue
			}
			break
		}
	}
	if !p.isOp("}") {
		return nil, fmt.Errorf("expected '}' in {..} literal")
	}
	p.advance()
	return &structNode{keys: keys, vals: vals}, nil
}

func (p *parser) parseArray() (expr, error) {
	p.advance() // [
	var vals []expr
	if !p.isOp("]") {
		for {
			v, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			vals = append(vals, v)
			if p.isOp(",") {
				p.advance()
				continue
			}
			break
		}
	}
	if !p.isOp("]") {
		return nil, fmt.Errorf("expected ']' in [..] literal")
	}
	p.advance()
	return &arrNode{vals: vals}, nil
}

// multiSet is a repeatable string flag value.
type multiSet []string

func (m *multiSet) String() string {
	return strings.Join(*m, ",")
}

func (m *multiSet) Set(v string) error {
	*m = append(*m, v)
	return nil
}
