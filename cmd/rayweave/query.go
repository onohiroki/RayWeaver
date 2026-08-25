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

// runQuery implements the `query` subcommand: a YAML/JSONL selector with
// in-memory edits that turns a shell pipeline into plain-text values (or,
// with --yaml/--json/--csv, structured output). It is designed to replace the
// `python3 -c "import yaml; ..."` snippets that the sample demo scripts
// previously used, so that the demos depend only on the rayweave binary.
//
// Overview of the CLI surface (see docs/query.md for the full manual):
//
//	rayweave query [--yaml|--json|--csv PATH[:col,...]] [--printf FMT]
//	               [--each PATH[:col,...]] [--sum|--product|--count|--len PATH]
//	               [--jsonl [--where EXPR] [--first]] [--set VAR=EXPR] [--edit EXPR]
//	               [--gate EXPR] [--default STR] [--expr EXPR] [-r] [SELECTOR]
//
// SELECTOR is an expression. Paths (e.g. paraxial_result.focal_length,
// chief_rays[0].spot_stats.rms_r, chief_rays[field_angle=0].spot_stats.rms_r,
// . for the whole document, .ray for ray) are a subset of the expression
// language, which also supports arithmetic, math functions, comparisons, and
// {..}/[..] literals. --edit mutates a deep copy before the selector runs
// (see docs/query.md §10).
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
	var editFlags multiSet
	fs.Var(&editFlags, "edit", "mutation expression (repeatable, applied in order); supports =, +=, -=, |=, del PATH")
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
	// Parse --edit expressions
	var edits []expr
	for _, editStr := range editFlags {
		// Tokenize each edit expression separately
		toks, err := lexQuery(editStr)
		if err != nil {
			errOut("Error tokenizing --edit %q: %v", editStr, err)
			os.Exit(1)
		}
		p := &parser{toks: toks}
		n, err := p.parseExpr()
		if err != nil {
			errOut("Error parsing --edit %q: %v", editStr, err)
			os.Exit(1)
		}
		edits = append(edits, n)
	}
	// Apply mutations (deep copy and apply in order)
	if len(edits) > 0 {
		var err error
		docRoot, err = applyMutations(docRoot, edits)
		if err != nil {
			errOut("Error applying mutations: %v", err)
			os.Exit(1)
		}
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
			// Special handling for . and .prefix syntax
			if selector == "." {
				result = docRoot
			} else if strings.HasPrefix(selector, ".") {
				// Strip leading . and evaluate the remainder (e.g., .ray → ray)
				selector = strings.TrimPrefix(selector, ".")
				result, err = evalExpr(selector, ctx)
			} else {
				result, err = evalExpr(selector, ctx)
			}
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
type rootNode struct{}
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
type assignNode struct {
	path  expr
	op    string // =, +=, -=, |=
	value expr
}
type deleteNode struct {
	paths []expr
}

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
		// Strip leading . for .prefix syntax (e.g., .ray → ray)
		lookupName := t.name
		if strings.HasPrefix(lookupName, ".") {
			lookupName = lookupName[1:]
		}
		if lookupName == "pi" {
			return math.Pi, nil
		}
		if lookupName == "e" {
			return math.E, nil
		}
		if m, ok := asMap(c.root); ok {
			if v, ok := m[lookupName]; ok {
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
	case *rootNode:
		return c.root, nil
	}
	return nil, fmt.Errorf("unknown expression node %T", n)
}

// ---- mutation evaluator -------------------------------------------------------

// applyMutations applies a list of mutation expressions to a document.
// It deep-copies the root and applies each mutation in order.
// Supported mutations: assignNode (=, +=, -=, |=) and deleteNode (del).
func applyMutations(root any, edits []expr) (any, error) {
	// Deep copy the root
	copied := deepCopy(root)

	for _, edit := range edits {
		switch e := edit.(type) {
		case *assignNode:
			if err := applyAssign(copied, e, root); err != nil {
				return nil, err
			}
		case *deleteNode:
			if err := applyDelete(copied, e); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("unsupported mutation type %T", edit)
		}
	}
	return copied, nil
}

// deepCopy creates a deep copy of a YAML value (maps, arrays, scalars).
func deepCopy(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = deepCopy(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = deepCopy(val)
		}
		return out
	default:
		return v // scalars (float64, string, bool, nil)
	}
}

// splitPath converts a mutation-path AST (ident / dot / idx chains) into
// a flat key list: string for map keys, int for array indices.
func splitPath(e expr) ([]any, error) {
	switch p := e.(type) {
	case *identNode:
		return []any{p.name}, nil
	case *dotNode:
		left, err := splitPath(p.x)
		if err != nil {
			return nil, err
		}
		return append(left, p.key), nil
	case *idxNode:
		left, err := splitPath(p.x)
		if err != nil {
			return nil, err
		}
		return append(left, int(p.n)), nil
	default:
		return nil, fmt.Errorf("unsupported path for mutation: %T", e)
	}
}

// getAtPath navigates from root following keys, returning the value at that path.
func getAtPath(root any, keys []any) (any, error) {
	cur := root
	for _, k := range keys {
		switch kk := k.(type) {
		case string:
			m, ok := cur.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("not a map at %q", kk)
			}
			child, ok := m[kk]
			if !ok {
				return nil, fmt.Errorf("key %q not found", kk)
			}
			cur = child
		case int:
			arr, ok := cur.([]any)
			if !ok {
				return nil, fmt.Errorf("not an array")
			}
			idx := kk
			if idx < 0 {
				idx += len(arr)
			}
			if idx < 0 || idx >= len(arr) {
				return nil, fmt.Errorf("index out of bounds: %d (len=%d)", idx, len(arr))
			}
			cur = arr[idx]
		default:
			return nil, fmt.Errorf("invalid key type %T", k)
		}
	}
	return cur, nil
}

// evalExprWithContext evaluates an expression with root as context.
func evalExprWithContext(e expr, root, _ any) (any, error) {
	ctx := &evalCtx{root: root, bindings: nil}
	return ctx.eval(e)
}

// applyAssign applies an assignment mutation (=, +=, -=, |=).
func applyAssign(root any, a *assignNode, origRoot any) error {
	// Special case: arr[] += val  — mapNode path
	if mn, ok := a.path.(*mapNode); ok && a.op == "+=" {
		val, err := evalExprWithContext(a.value, root, origRoot)
		if err != nil {
			return err
		}
		return appendViaMapNode(root, mn, val)
	}

	keys, err := splitPath(a.path)
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		return fmt.Errorf("empty path")
	}

	switch a.op {
	case "=":
		val, err := evalExprWithContext(a.value, root, origRoot)
		if err != nil {
			return err
		}
		return setAtPath(root, keys, val)
	case "|=":
		cur, err := getAtPath(root, keys)
		if err != nil {
			cur = nil
		}
		ctx := &evalCtx{root: cur, bindings: nil}
		newVal, err := ctx.eval(a.value)
		if err != nil {
			return err
		}
		return setAtPath(root, keys, newVal)
	case "+=":
		val, err := evalExprWithContext(a.value, root, origRoot)
		if err != nil {
			return err
		}
		return appendAtPath(root, keys, val)
	case "-=":
		val, err := evalExprWithContext(a.value, root, origRoot)
		if err != nil {
			return err
		}
		return removeAtPath(root, keys, val)
	}
	return fmt.Errorf("unknown assignment operator %q", a.op)
}

// applyDelete applies a delete mutation (del PATH).
func applyDelete(root any, d *deleteNode) error {
	for _, pathExpr := range d.paths {
		deleteWalk(root, pathExpr)
	}
	return nil
}

// deleteWalk navigates pathExpr from container and deletes the target.
// dotNode deletes p.key from all containers found by p.x.
// idxNode removes the element at index p.n from all arrays found by p.x.
// mapNode/filterNode distribute the operation to matching elements.
// Missing paths are silently ignored (no-op).
func deleteWalk(container any, pathExpr expr) {
	switch p := pathExpr.(type) {
	case *identNode:
		if m, ok := container.(map[string]any); ok {
			delete(m, p.name)
		}

	case *dotNode:
		parents := collectContainers(container, p.x)
		for _, parent := range parents {
			if m, ok := parent.(map[string]any); ok {
				delete(m, p.key)
			}
		}

	case *idxNode:
		writeIdxDeletion(container, p.x, int(p.n))

	case *mapNode:
		parents := collectContainers(container, p.x)
		for _, parent := range parents {
			elems := toIterable(parent)
			for i, elem := range elems {
				elems[i] = deleteWalkReturn(elem, p.x)
			}
		}

	case *filterNode:
		parents := collectContainers(container, p.x)
		for _, parent := range parents {
			filtered := filterElements(parent, p.key, p.val)
			for _, elem := range filtered {
				_ = deleteWalkReturn(elem, p.x)
			}
		}
	}
}

// deleteWalkReturn is like deleteWalk but returns the (potentially modified)
// container. Used by mapNode to propagate array-element replacements.
func deleteWalkReturn(container any, pathExpr expr) any {
	switch p := pathExpr.(type) {
	case *identNode:
		if m, ok := container.(map[string]any); ok {
			delete(m, p.name)
		}
		return container

	case *dotNode:
		parents := collectContainers(container, p.x)
		for _, parent := range parents {
			if m, ok := parent.(map[string]any); ok {
				delete(m, p.key)
			}
		}
		return container

	case *idxNode:
		return applyIdxDeletion(container, p.x, int(p.n))

	case *mapNode:
		parents := collectContainers(container, p.x)
		for _, parent := range parents {
			elems := toIterable(parent)
			for i, elem := range elems {
				elems[i] = deleteWalkReturn(elem, p.x)
			}
		}
		return container

	case *filterNode:
		parents := collectContainers(container, p.x)
		for _, parent := range parents {
			filtered := filterElements(parent, p.key, p.val)
			for _, elem := range filtered {
				_ = deleteWalkReturn(elem, p.x)
			}
		}
		return container
	}
	return container
}

// collectContainers resolves path from container and returns all values
// the path points to. Used to find "parent" containers for deletion.
func collectContainers(container any, path expr) []any {
	switch p := path.(type) {
	case *rootNode:
		return []any{container}

	case *identNode:
		child := getField(container, p.name)
		if child == nil {
			return nil
		}
		return []any{child}

	case *dotNode:
		parents := collectContainers(container, p.x)
		var result []any
		for _, parent := range parents {
			child := getField(parent, p.key)
			if child != nil {
				result = append(result, child)
			}
		}
		return result

	case *idxNode:
		parents := collectContainers(container, p.x)
		var result []any
		for _, parent := range parents {
			child := getIndex(parent, int(p.n))
			if child != nil {
				result = append(result, child)
			}
		}
		return result

	case *mapNode:
		parents := collectContainers(container, p.x)
		var result []any
		for _, parent := range parents {
			result = append(result, toIterable(parent)...)
		}
		return result

	case *filterNode:
		parents := collectContainers(container, p.x)
		var result []any
		for _, parent := range parents {
			result = append(result, filterElements(parent, p.key, p.val)...)
		}
		return result
	}
	return nil
}

// writeIdxDeletion removes the element at idx from all arrays found by
// pathToArrays, writing the shortened array back to its parent container.
func writeIdxDeletion(container any, pathToArrays expr, idx int) {
	applyIdxDeletion(container, pathToArrays, idx)
}

// applyIdxDeletion is the core array-index removal. It returns the
// (potentially modified) container so callers can propagate changes.
func applyIdxDeletion(container any, pathToArrays expr, idx int) any {
	switch p := pathToArrays.(type) {
	case *identNode:
		arr := getField(container, p.name)
		if a, ok := arr.([]any); ok {
			newA := removeIndex(a, idx)
			if m, ok := container.(map[string]any); ok {
				m[p.name] = newA
			}
			return newA
		}

	case *dotNode:
		children := collectContainers(container, p.x)
		for _, child := range children {
			arr := getField(child, p.key)
			if a, ok := arr.([]any); ok {
				newA := removeIndex(a, idx)
				if m, ok := child.(map[string]any); ok {
					m[p.key] = newA
				}
			}
		}

	case *idxNode:
		containers := collectContainers(container, p.x)
		for _, c := range containers {
			child := getIndex(c, int(p.n))
			if child == nil {
				continue
			}
			if a, ok := child.([]any); ok {
				newA := removeIndex(a, idx)
				if arr, ok := c.([]any); ok {
					i := int(p.n)
					if i < 0 {
						i += len(arr)
					}
					if i >= 0 && i < len(arr) {
						arr[i] = newA
					}
				}
			}
		}

	case *mapNode:
		parents := collectContainers(container, p.x)
		for _, parent := range parents {
			if a, ok := parent.([]any); ok {
				for i, elem := range a {
					if subArr, ok := elem.([]any); ok {
						a[i] = removeIndex(subArr, idx)
					}
				}
			}
		}
	}
	return container
}

func removeIndex(a []any, idx int) []any {
	if idx < 0 {
		idx += len(a)
	}
	if idx < 0 || idx >= len(a) {
		return a
	}
	newArr := make([]any, 0, len(a)-1)
	newArr = append(newArr, a[:idx]...)
	newArr = append(newArr, a[idx+1:]...)
	return newArr
}

func getField(v any, key string) any {
	if m, ok := v.(map[string]any); ok {
		return m[key]
	}
	return nil
}

func getIndex(v any, idx int) any {
	a, ok := v.([]any)
	if !ok {
		return nil
	}
	if idx < 0 {
		idx += len(a)
	}
	if idx >= 0 && idx < len(a) {
		return a[idx]
	}
	return nil
}

func toIterable(v any) []any {
	switch t := v.(type) {
	case []any:
		return t
	case map[string]any:
		out := make([]any, 0, len(t))
		for _, val := range t {
			out = append(out, val)
		}
		return out
	}
	return nil
}

func filterElements(container any, key string, valExpr expr) []any {
	arr, ok := container.([]any)
	if !ok {
		return nil
	}
	ctx := &evalCtx{root: container}
	fv, _ := ctx.eval(valExpr)
	var out []any
	for _, e := range arr {
		if m, ok := e.(map[string]any); ok {
			if equalVal(m[key], fv) {
				out = append(out, e)
			}
		}
	}
	return out
}

// setAtPath sets keys (last element) in the container reached by keys[:len-1].
// Intermediate maps that are missing are auto-created (yq semantics).
func setAtPath(root any, keys []any, val any) error {
	if len(keys) == 1 {
		return setDirect(root, keys[0], val)
	}
	parentKeys := keys[:len(keys)-1]
	parent, err := getOrCreateParent(root, parentKeys)
	if err != nil {
		return err
	}
	return setDirect(parent, keys[len(keys)-1], val)
}

// appendAtPath appends val to the array at keys.
func appendAtPath(root any, keys []any, val any) error {
	// keys points to the array itself
	target, err := getAtPath(root, keys)
	if err != nil {
		return err
	}
	arr, ok := target.([]any)
	if !ok {
		return fmt.Errorf("+= requires array")
	}
	newArr := append(arr, val)
	if len(keys) == 1 {
		if m, ok := root.(map[string]any); ok {
			if s, ok := keys[0].(string); ok {
				m[s] = newArr
				return nil
			}
		}
		return fmt.Errorf("cannot append to top-level array directly")
	}
	holderKeys := keys[:len(keys)-1]
	holder, err := getAtPath(root, holderKeys)
	if err != nil {
		return err
	}
	return setDirect(holder, keys[len(keys)-1], newArr)
}

// appendViaMapNode handles arr[] += val where arr[] is a mapNode.
func appendViaMapNode(root any, mn *mapNode, val any) error {
	keys, err := splitPath(mn.x)
	if err != nil {
		return err
	}
	target, err := getAtPath(root, keys)
	if err != nil {
		return err
	}
	arr, ok := target.([]any)
	if !ok {
		return fmt.Errorf("+= requires array")
	}
	newArr := append(arr, val)
	if len(keys) == 1 {
		if m, ok := root.(map[string]any); ok {
			if s, ok := keys[0].(string); ok {
				m[s] = newArr
				return nil
			}
		}
		return fmt.Errorf("cannot append to top-level array directly")
	}
	// Same write-back logic as appendAtPath but target is at keys
	holderKeys := keys[:len(keys)-1]
	holder, err := getAtPath(root, holderKeys)
	if err != nil {
		return err
	}
	return setDirect(holder, keys[len(keys)-1], newArr)
}

// removeAtPath handles  arr -= value  and  map -= "key"  semantics.
// For arrays the RHS value is the index to remove; for maps it is ignored
// and the key in the path is removed — but we unify: if target is array,
// val is numeric index; if target's parent is map, path's last key is removed.
func removeAtPath(root any, keys []any, val any) error {
	// Try array-index removal: target at keys should be an array, val is index.
	target, err := getAtPath(root, keys)
	if err == nil {
		if arr, ok := target.([]any); ok {
			f, ok := asNum(val)
			if !ok {
				return fmt.Errorf("-= on array requires numeric index")
			}
			idx := int(f)
			if idx < 0 {
				idx += len(arr)
			}
			if idx < 0 || idx >= len(arr) {
				return fmt.Errorf("index out of bounds: %d (len=%d)", idx, len(arr))
			}
			newArr := append(arr[:idx], arr[idx+1:]...)
			if len(keys) == 1 {
				if m, ok := root.(map[string]any); ok {
					if s, ok := keys[0].(string); ok {
						m[s] = newArr
						return nil
					}
				}
				return fmt.Errorf("cannot delete from top-level array directly")
			}
			holderKeys := keys[:len(keys)-1]
			holder, err := getAtPath(root, holderKeys)
			if err != nil {
				return err
			}
			return setDirect(holder, keys[len(keys)-1], newArr)
		}
	}
	// Fallback: treat as map-key removal where last key is deleted from parent.
	return deleteAtPath(root, keys)
}

// deleteAtPath deletes the value at keys.
func deleteAtPath(root any, keys []any) error {
	if len(keys) == 1 {
		return deleteDirect(root, keys[0])
	}
	parentKeys := keys[:len(keys)-1]
	parent, err := getAtPath(root, parentKeys)
	if err != nil {
		return err
	}
	finalKey := keys[len(keys)-1]
	if idx, isInt := finalKey.(int); isInt {
		arr, ok := parent.([]any)
		if !ok {
			return fmt.Errorf("parent is not array")
		}
		if idx < 0 {
			idx += len(arr)
		}
		if idx < 0 || idx >= len(arr) {
			return fmt.Errorf("index out of bounds: %d (len=%d)", idx, len(arr))
		}
		newArr := append(arr[:idx], arr[idx+1:]...)
		if len(parentKeys) == 1 {
			holderKey := parentKeys[0]
			if s, ok := holderKey.(string); ok {
				if m, ok := root.(map[string]any); ok {
					m[s] = newArr
					return nil
				}
			}
			return fmt.Errorf("cannot write back array")
		}
		holderKeys := parentKeys[:len(parentKeys)-1]
		holder, err := getAtPath(root, holderKeys)
		if err != nil {
			return err
		}
		return setDirect(holder, parentKeys[len(parentKeys)-1], newArr)
	}
	return deleteDirect(parent, finalKey)
}

// getOrCreateParent navigates to parentKeys, auto-creating missing intermediate maps.
func getOrCreateParent(root any, parentKeys []any) (any, error) {
	cur := root
	for i, k := range parentKeys {
		switch kk := k.(type) {
		case string:
			m, ok := cur.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("not a map at %q", kk)
			}
			child, exists := m[kk]
			if !exists {
				// Auto-create intermediate map if next step is a map key (string).
				// If the final target is an array index, we still need a map here —
				// but if the missing key itself should be an array, caller must create it explicitly.
				newMap := make(map[string]any)
				m[kk] = newMap
				child = newMap
			} else if child == nil {
				// Treat nil as missing map for auto-create when next key is string.
				nextIsString := false
				if i+1 < len(parentKeys) {
					_, nextIsString = parentKeys[i+1].(string)
				} else {
					// final key type determines — peek via closure is messy; assume map
					nextIsString = true
				}
				if nextIsString {
					newMap := make(map[string]any)
					m[kk] = newMap
					child = newMap
				}
			}
			cur = child
		case int:
			arr, ok := cur.([]any)
			if !ok {
				return nil, fmt.Errorf("not an array")
			}
			idx := kk
			if idx < 0 {
				idx += len(arr)
			}
			if idx < 0 || idx >= len(arr) {
				return nil, fmt.Errorf("index out of bounds: %d (len=%d)", idx, len(arr))
			}
			cur = arr[idx]
		default:
			return nil, fmt.Errorf("invalid key type %T", k)
		}
	}
	return cur, nil
}

// Helper functions for container manipulation

// setDirect sets a value directly on a container (map or array).
func setDirect(container any, key any, val any) error {
	switch c := container.(type) {
	case map[string]any:
		k, ok := key.(string)
		if !ok {
			return fmt.Errorf("map key must be string")
		}
		c[k] = val
		return nil
	case []any:
		i, ok := key.(int)
		if !ok {
			return fmt.Errorf("array index must be int")
		}
		if i < 0 || i >= len(c) {
			return fmt.Errorf("index out of bounds: %d (len=%d)", i, len(c))
		}
		c[i] = val
		return nil
	default:
		return fmt.Errorf("cannot set value on non-container")
	}
}

// deleteDirect deletes a key/index directly from a container.
func deleteDirect(container any, key any) error {
	switch c := container.(type) {
	case map[string]any:
		k, ok := key.(string)
		if !ok {
			return fmt.Errorf("map key must be string")
		}
		delete(c, k)
		return nil
	case []any:
		i, ok := key.(int)
		if !ok {
			return fmt.Errorf("array index must be int")
		}
		if i < 0 {
			i += len(c)
		}
		if i < 0 || i >= len(c) {
			return fmt.Errorf("index out of bounds: %d (len=%d)", i, len(c))
		}
		// Can't actually delete from slice in place without returning new slice
		// This is a limitation - caller should use grandparent approach
		return fmt.Errorf("direct array delete not supported, use grandparent")
	default:
		return fmt.Errorf("cannot delete from non-container")
	}
}

// getFromContainer gets a value from a container (map or array) by key.
func getFromContainer(container any, key any) (any, error) {
	switch c := container.(type) {
	case map[string]any:
		k, ok := key.(string)
		if !ok {
			return nil, fmt.Errorf("map key must be string")
		}
		return c[k], nil
	case []any:
		i, ok := key.(int)
		if !ok {
			return nil, fmt.Errorf("array index must be int")
		}
		if i < 0 {
			i += len(c)
		}
		if i < 0 || i >= len(c) {
			return nil, fmt.Errorf("index out of bounds: %d (len=%d)", i, len(c))
		}
		return c[i], nil
	default:
		return nil, fmt.Errorf("cannot get from non-container")
	}
}

// putToContainer puts a value back into a container (map or array) by key.
func putToContainer(container any, key any, val any) error {
	switch c := container.(type) {
	case map[string]any:
		k, ok := key.(string)
		if !ok {
			return fmt.Errorf("map key must be string")
		}
		c[k] = val
		return nil
	case []any:
		i, ok := key.(int)
		if !ok {
			return fmt.Errorf("array index must be int")
		}
		if i < 0 {
			i += len(c)
		}
		if i < 0 || i >= len(c) {
			return fmt.Errorf("index out of bounds: %d (len=%d)", i, len(c))
		}
		c[i] = val
		return nil
	default:
		return fmt.Errorf("cannot put to non-container")
	}
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
				case "==", "!=", "<=", ">=", "&&", "||", "+=", "-=", "|=":
					toks = append(toks, token{kind: "op", text: two})
					i += 2
					continue
				}
			}
			if strings.ContainsRune("+-*/%<>!=()[]{},:?.|", rune(c)) {
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
	return p.parseAssign()
}

func (p *parser) parseAssign() (expr, error) {
	l, err := p.parseTernary()
	if err != nil {
		return nil, err
	}
	// Check for assignment operators
	for {
		op := ""
		for _, cand := range []string{"=", "+=", "-=", "|="} {
			if p.isOp(cand) {
				op = cand
				break
			}
		}
		if op == "" {
			break
		}
		p.advance()
		r, err := p.parseAssign() // right-associative
		if err != nil {
			return nil, err
		}
		l = &assignNode{path: l, op: op, value: r}
	}
	return l, nil
}

func (p *parser) parseTernary() (expr, error) {
	cond, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.isOp("?") {
		p.advance()
		a, err := p.parseAssign() // ternary branches can have assignments
		if err != nil {
			return nil, err
		}
		if !p.isOp(":") {
			return nil, fmt.Errorf("expected ':' in conditional expression")
		}
		p.advance()
		b, err := p.parseAssign()
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
	case "root":
		p.advance()
		return &rootNode{}, nil
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
	case "root":
		p.advance()
		return &rootNode{}, nil
	case "str":
		p.advance()
		return &strNode{v: t.text}, nil
	case "id":
		p.advance()
		if t.text == "del" {
			if p.isOp("(") {
				p.advance()
				var paths []expr
				if !p.isOp(")") {
					for {
						path, err := p.parseAssign()
						if err != nil {
							return nil, err
						}
						paths = append(paths, path)
						if p.isOp(",") {
							p.advance()
							continue
						}
						break
					}
				}
				if !p.isOp(")") {
					return nil, fmt.Errorf("expected ')' after del arguments")
				}
				p.advance()
				return &deleteNode{paths: paths}, nil
			}
			path, err := p.parseAssign()
			if err != nil {
				return nil, err
			}
			return &deleteNode{paths: []expr{path}}, nil
		}
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
		return &identNode{name: strings.TrimPrefix(t.text, ".")}, nil
	case "op":
		switch t.text {
		case ".":
			p.advance()
			if p.cur().kind == "id" {
				id := p.advance()
				// ".foo" shorthand → root.foo ; remaining postfix (".bar", "[0]") is handled by parsePostfix
				return &dotNode{x: &rootNode{}, key: id.text}, nil
			}
			return &rootNode{}, nil
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
