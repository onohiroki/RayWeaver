package main

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func evalForTest(t *testing.T, src string, root any, bindings map[string]any) any {
	t.Helper()
	v, err := evalExpr(src, &evalCtx{root: root, bindings: bindings})
	if err != nil {
		t.Fatalf("evalExpr(%q) error: %v", src, err)
	}
	return v
}

func num(v any) float64 {
	f, _ := asNum(v)
	return f
}

func TestEvalExprPaths(t *testing.T) {
	root := map[string]any{
		"a":    float64(1),
		"b":    map[string]any{"c": 2.5},
		"list": []any{map[string]any{"k": float64(0)}, map[string]any{"k": float64(1)}},
		"s":    "hello",
	}
	if v := num(evalForTest(t, "a", root, nil)); v != 1 {
		t.Errorf("a = %v, want 1", v)
	}
	if v := num(evalForTest(t, "b.c", root, nil)); v != 2.5 {
		t.Errorf("b.c = %v, want 2.5", v)
	}
	if v := num(evalForTest(t, "list[1].k", root, nil)); v != 1 {
		t.Errorf("list[1].k = %v, want 1", v)
	}
	if v := evalForTest(t, "missing", root, nil); v != nil {
		t.Errorf("missing = %v, want nil", v)
	}
	if v := evalForTest(t, "s", root, nil); v != "hello" {
		t.Errorf("s = %v, want hello", v)
	}
	if v := evalForTest(t, "pi", root, nil); num(v) != math.Pi {
		t.Errorf("pi = %v, want %v", v, math.Pi)
	}
}

func TestEvalExprFilterAndMap(t *testing.T) {
	root := map[string]any{
		"list": []any{
			map[string]any{"k": float64(0), "id": "a"},
			map[string]any{"k": float64(1), "id": "b"},
		},
		"m": map[string]any{"x": 1.0, "y": 2.0},
	}
	got := evalForTest(t, "list[k=0].k", root, nil)
	if num(got) != 0 {
		t.Errorf("list[k=0].k = %#v, want 0", got)
	}
	got = evalForTest(t, "list[id=\"b\"].k", root, nil)
	if num(got) != 1 {
		t.Errorf(`list[id="b"].k = %#v, want 1`, got)
	}
	got = evalForTest(t, "list[].k", root, nil)
	arr, _ := got.([]any)
	if len(arr) != 2 || num(arr[0]) != 0 || num(arr[1]) != 1 {
		t.Errorf("list[].k = %#v, want [0 1]", got)
	}
	got = evalForTest(t, "m[]", root, nil)
	arr, _ = got.([]any)
	if len(arr) != 2 {
		t.Errorf("m[] = %#v, want 2 values", got)
	}
}

func TestEvalExprFilterUnwrap(t *testing.T) {
	root := map[string]any{
		"configs": []any{
			map[string]any{"id": "config0", "surfaces": []any{
				map[string]any{"id": float64(1), "thickness": float64(20)},
				map[string]any{"id": float64(2), "thickness": float64(5)},
			}},
		},
	}
	// A filter matching exactly one element unwraps, so chained access walks
	// directly instead of mapping over a one-element array.
	if v := num(evalForTest(t, "configs[id=config0].surfaces[id=2].thickness", root, nil)); v != 5 {
		t.Errorf("filtered thickness = %v, want 5", v)
	}
	if v := num(evalForTest(t, "configs[id=config0].surfaces[1].thickness", root, nil)); v != 5 {
		t.Errorf("indexed thickness = %v, want 5", v)
	}
}

func TestEvalExprNegativeIndex(t *testing.T) {
	root := map[string]any{"a": []any{1.0, 2.0, 3.0}}
	if v := num(evalForTest(t, "a[-1]", root, nil)); v != 3 {
		t.Errorf("a[-1] = %v, want 3", v)
	}
	if v := num(evalForTest(t, "a[-2]", root, nil)); v != 2 {
		t.Errorf("a[-2] = %v, want 2", v)
	}
}

func TestEvalExprArithmeticAndFunctions(t *testing.T) {
	root := map[string]any{"a": float64(1), "b": float64(2.5)}
	if v := num(evalForTest(t, "a + b", root, nil)); v != 3.5 {
		t.Errorf("a+b = %v, want 3.5", v)
	}
	if v := num(evalForTest(t, "sqrt(4)", root, nil)); v != 2 {
		t.Errorf("sqrt(4) = %v, want 2", v)
	}
	if v := num(evalForTest(t, "pow(2, 10)", root, nil)); v != 1024 {
		t.Errorf("pow(2,10) = %v, want 1024", v)
	}
	if v := num(evalForTest(t, "abs(-3.5)", root, nil)); v != 3.5 {
		t.Errorf("abs(-3.5) = %v, want 3.5", v)
	}
	if v := num(evalForTest(t, "min(3, 1, 2)", root, nil)); v != 1 {
		t.Errorf("min(3,1,2) = %v, want 1", v)
	}
	if v := num(evalForTest(t, "radians(180)", root, nil)); v != math.Pi {
		t.Errorf("radians(180) = %v, want pi", v)
	}
	if v := num(evalForTest(t, "tan(radians(45))", root, nil)); math.Abs(v-1) > 1e-12 {
		t.Errorf("tan(45deg) = %v, want ~1", v)
	}
}

func TestEvalExprComparisonsAndLogic(t *testing.T) {
	root := map[string]any{"a": float64(1), "b": float64(2)}
	if v := evalForTest(t, "a < b", root, nil); v != true {
		t.Errorf("a<b = %v, want true", v)
	}
	if v := evalForTest(t, "a == 1", root, nil); v != true {
		t.Errorf("a==1 = %v, want true", v)
	}
	if v := evalForTest(t, "a == 2", root, nil); v != false {
		t.Errorf("a==2 = %v, want false", v)
	}
	if v := evalForTest(t, "a > 0 && b > 1", root, nil); v != true {
		t.Errorf("a>0 && b>1 = %v, want true", v)
	}
	if v := evalForTest(t, "a > 5 || b > 1", root, nil); v != true {
		t.Errorf("a>5 || b>1 = %v, want true", v)
	}
	if v := evalForTest(t, "! (a == 1)", root, nil); v != false {
		t.Errorf("!(a==1) = %v, want false", v)
	}
	if v := evalForTest(t, "has(\"a\")", root, nil); v != true {
		t.Errorf("has(a) = %v, want true", v)
	}
	if v := evalForTest(t, "has(\"zzz\")", root, nil); v != false {
		t.Errorf("has(zzz) = %v, want false", v)
	}
}

func TestEvalExprStructArrayTernaryBindings(t *testing.T) {
	root := map[string]any{"a": float64(1), "b": float64(2.5)}
	got := evalForTest(t, "{x: a, y: b}", root, nil)
	want := map[string]any{"x": 1.0, "y": 2.5}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("{x:a,y:b} = %#v, want %#v", got, want)
	}
	got = evalForTest(t, "[a, b]", root, nil)
	if arr := got.([]any); len(arr) != 2 || num(arr[0]) != 1 || num(arr[1]) != 2.5 {
		t.Errorf("[a,b] = %#v, want [1 2.5]", got)
	}
	if v := num(evalForTest(t, "a == 1 ? 10 : 20", root, nil)); v != 10 {
		t.Errorf("ternary true = %v, want 10", v)
	}
	if v := num(evalForTest(t, "a == 2 ? 10 : 20", root, nil)); v != 20 {
		t.Errorf("ternary false = %v, want 20", v)
	}
	// Distortion-style guard: on-axis (angle 0) must not divide by zero.
	expr := `a < 1e-9 ? 0 : 100*(ih-efl*tan(radians(a)))/(efl*tan(radians(a)))`
	bind := map[string]any{"a": 0.0, "ih": 0.0, "efl": 25.03}
	if v := num(evalForTest(t, expr, root, bind)); v != 0 {
		t.Errorf("distortion on-axis = %v, want 0", v)
	}
	bind["a"] = 16.0
	bind["ih"] = 7.0
	v := num(evalForTest(t, expr, root, bind))
	if math.Abs(v) > 200 {
		t.Errorf("distortion off-axis = %v, sanity check failed", v)
	}
}

func TestEvalExprDivisionByZero(t *testing.T) {
	root := map[string]any{"a": float64(1)}
	if _, err := evalExpr("a / 0", &evalCtx{root: root}); err == nil {
		t.Error("a / 0 should error")
	}
}

func TestNormalizeNumbers(t *testing.T) {
	in := map[string]any{
		"i":   int(3),
		"i64": int64(4),
		"f":   float64(1.5),
		"arr": []any{int8(1), uint64(2)},
	}
	out := normalizeNumbers(in)
	m := out.(map[string]any)
	if m["i"] != float64(3) {
		t.Errorf("i = %#v, want 3.0", m["i"])
	}
	if m["i64"] != float64(4) {
		t.Errorf("i64 = %#v, want 4.0", m["i64"])
	}
	if m["f"] != float64(1.5) {
		t.Errorf("f = %#v, want 1.5", m["f"])
	}
	arr := m["arr"].([]any)
	if arr[0] != float64(1) || arr[1] != float64(2) {
		t.Errorf("arr = %#v, want [1 2]", arr)
	}
}

func TestFormatPrintf(t *testing.T) {
	cases := []struct {
		format string
		vals   []any
		want   string
	}{
		{"%.4f", []any{1.5}, "1.5000"},
		{"%d", []any{3.0}, "3"},
		{"%s", []any{3.0}, "3"},
		{"%s", []any{"hello"}, "hello"},
		{"%d %s %.2f", []any{0.0, "abc", 1.2345}, "0 abc 1.23"},
		{"%.6e", []any{0.000181243}, "1.812430e-04"},
		{"100%%", nil, "100%"},
		{"f%d", []any{0.0}, "f0"},
	}
	for _, c := range cases {
		if got := formatPrintf(c.format, c.vals, false); got != c.want {
			t.Errorf("formatPrintf(%q, %v) = %q, want %q", c.format, c.vals, got, c.want)
		}
	}
}

func TestFormatPrintfNanEmpty(t *testing.T) {
	cases := []struct {
		format string
		vals   []any
		want   string
	}{
		// NaN with width → spaces
		{"%5.5f", []any{math.NaN()}, "     "},
		{"%10.5f", []any{math.NaN()}, "          "},
		{"%d", []any{math.NaN()}, " "},
		{"%5d", []any{math.NaN()}, "     "},
		// Non-NaN values pass through normally
		{"%5.5f", []any{1.23}, "1.23000"},
		{"%d", []any{3.0}, "3"},
		// Mixed: NaN and non-NaN
		{"%d,%5.5f,%5.5f", []any{1.0, math.NaN(), 3.45}, "1,     ,3.45000"},
		// Multiple NaN
		{"%5.5f %5.5f", []any{math.NaN(), math.NaN()}, "           "},
	}
	for _, c := range cases {
		if got := formatPrintf(c.format, c.vals, true); got != c.want {
			t.Errorf("formatPrintf(%q, %v, true) = %q, want %q", c.format, c.vals, got, c.want)
		}
	}
	// Without nanEmpty, NaN is passed to Sprintf normally
	got := formatPrintf("%5.5f", []any{math.NaN()}, false)
	if !strings.Contains(got, "NaN") {
		t.Errorf("formatPrintf without nanEmpty should contain NaN, got %q", got)
	}
}

func TestSplitEachArg(t *testing.T) {
	path, cols := splitEachArg("a.b[]:x,y")
	if path != "a.b[]" || !reflect.DeepEqual(cols, []string{"x", "y"}) {
		t.Errorf("splitEachArg = (%q, %v)", path, cols)
	}
	path, cols = splitEachArg("a.b")
	if path != "a.b" || cols != nil {
		t.Errorf("splitEachArg = (%q, %v)", path, cols)
	}
	path, cols = splitEachArg("terms:key,value")
	if path != "terms" || !reflect.DeepEqual(cols, []string{"key", "value"}) {
		t.Errorf("splitEachArg = (%q, %v)", path, cols)
	}
}

func TestParseDefaultNum(t *testing.T) {
	cases := []struct {
		input   string
		wantNaN bool
		wantInf int // 0=finite, +1=+Inf, -1=-Inf
		wantVal float64
	}{
		{"NaN", true, 0, 0},
		{"nan", true, 0, 0},
		{"Nan", true, 0, 0},
		{"Inf", false, 1, 0},
		{"+Inf", false, 1, 0},
		{"inf", false, 1, 0},
		{"-Inf", false, -1, 0},
		{"-inf", false, -1, 0},
		{"0", false, 0, 0},
		{"3.14", false, 0, 3.14},
		{"-1", false, 0, -1},
	}
	for _, c := range cases {
		f, err := parseDefaultNum(c.input)
		if err != nil {
			t.Errorf("parseDefaultNum(%q) error: %v", c.input, err)
			continue
		}
		if c.wantNaN {
			if !math.IsNaN(f) {
				t.Errorf("parseDefaultNum(%q) = %v, want NaN", c.input, f)
			}
		} else if c.wantInf != 0 {
			if !math.IsInf(f, c.wantInf) {
				t.Errorf("parseDefaultNum(%q) = %v, want Inf with sign %d", c.input, f, c.wantInf)
			}
		} else if f != c.wantVal {
			t.Errorf("parseDefaultNum(%q) = %v, want %v", c.input, f, c.wantVal)
		}
	}
}

func TestFormatValue(t *testing.T) {
	if formatValue(25.033) != "25.033" {
		t.Errorf("formatValue float = %q", formatValue(25.033))
	}
	if formatValue(0.0) != "0" {
		t.Errorf("formatValue zero = %q", formatValue(0.0))
	}
	if formatValue(true) != "true" {
		t.Errorf("formatValue bool = %q", formatValue(true))
	}
	if formatValue([]any{0.0, 16.0}) != "[0 16]" {
		t.Errorf("formatValue array = %q", formatValue([]any{0.0, 16.0}))
	}
	if formatValue([]any{0.0}) != "0" {
		t.Errorf("formatValue single-array = %q", formatValue([]any{0.0}))
	}
}

func TestTruthyAndEqualVal(t *testing.T) {
	if truthy(0.0) {
		t.Error("0 should be falsy")
	}
	if !truthy(1.0) {
		t.Error("1 should be truthy")
	}
	if truthy(nil) {
		t.Error("nil should be falsy")
	}
	if truthy("") {
		t.Error("empty string should be falsy")
	}
	if !truthy("x") {
		t.Error("non-empty string should be truthy")
	}
	if !equalVal(0.0, 0.0) {
		t.Error("0 == 0")
	}
	if equalVal(0.0, 1.0) {
		t.Error("0 != 1")
	}
	if !equalVal("REFLECT", "REFLECT") {
		t.Error("string equality")
	}
	if equalVal("REFLECT", 0.0) {
		t.Error("string != number")
	}
}

// ---- CLI integration via subprocess ----------------------------------------

func TestQueryCLIHelper(t *testing.T) {
	if os.Getenv("GO_QUERY_HELPER") != "1" {
		return
	}
	var args []string
	_ = json.Unmarshal([]byte(os.Getenv("GO_QUERY_ARGS")), &args)
	os.Args = append([]string{"rayweave", "query"}, args...)
	main()
	os.Exit(0)
}

func runQueryCLI(t *testing.T, stdin string, args ...string) (string, int) {
	t.Helper()
	rawArgs, _ := json.Marshal(args)
	cmd := exec.Command(os.Args[0], "-test.run=TestQueryCLIHelper")
	cmd.Env = append(os.Environ(), "GO_QUERY_HELPER=1", "GO_QUERY_ARGS="+string(rawArgs))
	cmd.Stdin = strings.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("query subprocess error: %v", err)
	}
	return out.String(), code
}

const paraxialYAML = "paraxial_result:\n  focal_length: 25.033\n  image_space_f_number: 5.419\n"

const chiefYAML = `chief_rays:
  - field_angle: 0
    spot_stats: {rms_r: 0.0011}
    image_height: [0, 0, 0]
    grid_points:
      - {image_x: 0.1, image_y: 0.2}
      - {image_x: null, image_y: null}
      - {image_x: 0.3, image_y: 0.4}
  - field_angle: 16
    spot_stats: {rms_r: 0.0345}
    image_height: [0, 7.1, 0]
    grid_points:
      - {image_x: 1.1, image_y: 2.2}
`

func TestQueryCLIExtract(t *testing.T) {
	out, code := runQueryCLI(t, paraxialYAML, "-r", "paraxial_result.focal_length")
	if code != 0 || strings.TrimSpace(out) != "25.033" {
		t.Errorf("extract = (%q, %d), want 25.033, 0", out, code)
	}
}

func TestQueryCLIDefault(t *testing.T) {
	out, code := runQueryCLI(t, paraxialYAML, "-r", "paraxial_result.missing_key")
	if code != 0 || strings.TrimSpace(out) != "-1" {
		t.Errorf("default = (%q, %d), want -1, 0", out, code)
	}
}

func TestQueryCLIFilter(t *testing.T) {
	out, _ := runQueryCLI(t, chiefYAML, "-r", "chief_rays[field_angle=16].spot_stats.rms_r")
	if strings.TrimSpace(out) != "0.0345" {
		t.Errorf("filter = %q, want 0.0345", out)
	}
}

func TestQueryCLICountLen(t *testing.T) {
	out, _ := runQueryCLI(t, chiefYAML, "--count", "chief_rays[0].grid_points[].image_x")
	if strings.TrimSpace(out) != "2" {
		t.Errorf("count = %q, want 2", out)
	}
	out, _ = runQueryCLI(t, chiefYAML, "--len", "chief_rays[0].grid_points")
	if strings.TrimSpace(out) != "3" {
		t.Errorf("len = %q, want 3", out)
	}
}

func TestQueryCLIStdev(t *testing.T) {
	yaml := "g:\n  - {y: 0}\n  - {y: 2}\n  - {y: 4}\n"
	out, code := runQueryCLI(t, yaml, "--stdev", "g[].y", "--printf", "%.4f")
	if code != 0 || strings.TrimSpace(out) != "1.6330" {
		t.Errorf("stdev = (%q, %d), want 1.6330, 0", out, code)
	}
}

func TestQueryCLINegativeIndex(t *testing.T) {
	out, _ := runQueryCLI(t, chiefYAML, "-r", "chief_rays[-1].field_angle")
	if strings.TrimSpace(out) != "16" {
		t.Errorf("negative index = %q, want 16", out)
	}
}

func TestQueryCLIEach(t *testing.T) {
	out, code := runQueryCLI(t, chiefYAML, "--each", "chief_rays[]:field_angle", "--printf", "field %s deg")
	if code != 0 {
		t.Fatalf("each exit %d", code)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 || lines[0] != "field 0 deg" || lines[1] != "field 16 deg" {
		t.Errorf("each = %q", out)
	}
}

func TestQueryCLIDefaultNumNaN(t *testing.T) {
	// YAML with some missing fields to trigger nil → NaN
	yaml := `items:
  - {a: 1, b: 2.5}
  - {a: 3}
  - {b: 4.0}
`
	// --default-num NaN --printf-nan-empty: nil → NaN → spaces
	out, code := runQueryCLI(t, yaml,
		"--each", "items[]:a,b",
		"--default-num", "NaN",
		"--printf-nan-empty",
		"--printf", "%5.5f,%5.5f")
	if code != 0 {
		t.Fatalf("default-num NaN exit %d", code)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %q", len(lines), out)
	}
	// Line 1: both present
	if lines[0] != "1.00000,2.50000" {
		t.Errorf("line 0 = %q, want 1.00000,2.50000", lines[0])
	}
	// Line 2: b is nil → 5 spaces
	if lines[1] != "3.00000,     " {
		t.Errorf("line 1 = %q, want '3.00000,     '", lines[1])
	}
	// Line 3: a is nil → 5 spaces
	if lines[2] != "     ,4.00000" {
		t.Errorf("line 2 = %q, want '     ,4.00000'", lines[2])
	}
}

func TestQueryCLIDefaultNumZero(t *testing.T) {
	yaml := `items:
  - {a: 1}
  - {}
`
	// --default-num 0: nil → 0 (no NaN involved)
	out, code := runQueryCLI(t, yaml,
		"--each", "items[]:a",
		"--default-num", "0",
		"--printf", "%d")
	if code != 0 {
		t.Fatalf("default-num 0 exit %d", code)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 || lines[0] != "1" || lines[1] != "0" {
		t.Errorf("default-num 0 = %q, want '1\\n0'", out)
	}
}

func TestQueryCLIYAMLRecord(t *testing.T) {
	out, code := runQueryCLI(t, paraxialYAML, "--yaml",
		"--set", "efl=paraxial_result.focal_length",
		"--set", "fno=paraxial_result.image_space_f_number",
		"--set", "airy=1.22*0.0005876*fno")
	if code != 0 {
		t.Fatalf("yaml record exit %d", code)
	}
	if !strings.Contains(out, "efl: 25.033") || !strings.Contains(out, "fno: 5.419") {
		t.Errorf("yaml record = %q", out)
	}
}

func TestQueryCLICSV(t *testing.T) {
	out, code := runQueryCLI(t, chiefYAML, "--csv", "chief_rays[0].grid_points[]:image_x,image_y")
	if code != 0 {
		t.Fatalf("csv exit %d", code)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 { // the image_x:null row must be skipped
		t.Errorf("csv rows = %d, want 2: %q", len(lines), out)
	}
}

func TestQueryCLICSVKeepAll(t *testing.T) {
	out, code := runQueryCLI(t, chiefYAML, "--csv-keep-all", "--csv", "chief_rays[0].grid_points[]:image_x,image_y")
	if code != 0 {
		t.Fatalf("csv keep-all exit %d", code)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 { // null row is now kept
		t.Errorf("csv rows = %d, want 3: %q", len(lines), out)
	}
	if !strings.Contains(lines[1], ",") {
		t.Errorf("null row must still have a comma separator: %q", lines[1])
	}
}

func TestQueryCLIGate(t *testing.T) {
	out, code := runQueryCLI(t, paraxialYAML, "--gate", "abs(efl-25.033)<0.01", "--set", "efl=paraxial_result.focal_length")
	if code != 0 || !strings.Contains(out, "true") {
		t.Errorf("gate pass = (%q, %d), want true, 0", out, code)
	}
	out, code = runQueryCLI(t, paraxialYAML, "--gate", "abs(efl-50.0)<=0.01", "--set", "efl=paraxial_result.focal_length")
	if code != 1 || !strings.Contains(out, "false") {
		t.Errorf("gate fail = (%q, %d), want false, 1", out, code)
	}
}

func TestQueryCLIExprDistortion(t *testing.T) {
	expr := `a < 1e-9 ? 0 : 100*(ih-efl*tan(radians(a)))/(efl*tan(radians(a)))`
	out, code := runQueryCLI(t, chiefYAML,
		"--set", "a=chief_rays[0].field_angle",
		"--set", "ih=chief_rays[0].image_height[1]",
		"--set", "efl=25.033",
		"--expr", expr)
	if code != 0 || strings.TrimSpace(out) != "0" {
		t.Errorf("distortion on-axis = (%q, %d), want 0, 0", out, code)
	}
}

const jsonlLog = `{"iter":0,"merit":0.5,"status":"running"}
{"iter":1,"merit":0.1}
{"iter":2,"merit":0.02,"status":"converged"}
{"event":"breakdown","terms":{"spot_rms":0.01,"opd_rms":0.01}}
`

func TestQueryCLIJSONL(t *testing.T) {
	out, _ := runQueryCLI(t, jsonlLog, "--jsonl", "--where", "has(\"status\")", "-r", "status")
	if strings.TrimSpace(out) != "converged" {
		t.Errorf("jsonl status = %q, want converged", out)
	}
	out, _ = runQueryCLI(t, jsonlLog, "--jsonl", "--where", "has(\"merit\")", "--count", "[]")
	if strings.TrimSpace(out) != "3" {
		t.Errorf("jsonl merit count = %q, want 3", out)
	}
	out, _ = runQueryCLI(t, jsonlLog, "--jsonl", "--where", "has(\"merit\")", "-r", "merit")
	if strings.TrimSpace(out) != "0.02" {
		t.Errorf("jsonl last merit = %q, want 0.02", out)
	}
	out, _ = runQueryCLI(t, jsonlLog, "--jsonl", "--where", "event==\"breakdown\"",
		"--each", "terms:key,value", "--printf", "%s=%.4f")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 || lines[0] != "opd_rms=0.0100" || lines[1] != "spot_rms=0.0100" {
		t.Errorf("jsonl breakdown = %q", out)
	}
}


// ---- del() tests ----------------------------------------------------------

func applyDelForTest(t *testing.T, editStr string, root any) any {
	t.Helper()
	toks, err := lexQuery(editStr)
	if err != nil {
		t.Fatalf("lexQuery(%q) error: %v", editStr, err)
	}
	p := &parser{toks: toks}
	n, err := p.parseExpr()
	if err != nil {
		t.Fatalf("parseExpr(%q) error: %v", editStr, err)
	}
	result, err := applyMutations(root, []expr{n})
	if err != nil {
		t.Fatalf("applyMutations(%q) error: %v", editStr, err)
	}
	return result
}

func TestDelBasic(t *testing.T) {
	root := map[string]any{"a": float64(1), "b": float64(2)}
	got := applyDelForTest(t, "del(a)", root)
	want := map[string]any{"b": float64(2)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("del(a) = %#v, want %#v", got, want)
	}
}

func TestDelDotPath(t *testing.T) {
	root := map[string]any{
		"a": map[string]any{"b": float64(1), "c": float64(2)},
	}
	got := applyDelForTest(t, "del(a.b)", root)
	want := map[string]any{
		"a": map[string]any{"c": float64(2)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("del(a.b) = %#v, want %#v", got, want)
	}
}

func TestDelArrayIndex(t *testing.T) {
	root := map[string]any{
		"a": []any{float64(1), float64(2), float64(3)},
	}
	got := applyDelForTest(t, "del(a[1])", root)
	want := map[string]any{
		"a": []any{float64(1), float64(3)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("del(a[1]) = %#v, want %#v", got, want)
	}
}

func TestDelWildcard(t *testing.T) {
	root := map[string]any{
		"items": []any{
			map[string]any{"x": float64(1), "y": float64(2)},
			map[string]any{"x": float64(3), "y": float64(4)},
		},
	}
	got := applyDelForTest(t, "del(items[].y)", root)
	want := map[string]any{
		"items": []any{
			map[string]any{"x": float64(1)},
			map[string]any{"x": float64(3)},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("del(items[].y) = %#v, want %#v", got, want)
	}
}

func TestDelNestedWildcard(t *testing.T) {
	root := map[string]any{
		"rays": []any{
			map[string]any{
				"wl": []any{
					map[string]any{"s": float64(1)},
					map[string]any{"s": float64(2)},
				},
			},
			map[string]any{
				"wl": []any{
					map[string]any{"s": float64(3)},
				},
			},
		},
	}
	got := applyDelForTest(t, "del(rays[].wl[].s)", root)
	want := map[string]any{
		"rays": []any{
			map[string]any{
				"wl": []any{map[string]any{}, map[string]any{}},
			},
			map[string]any{
				"wl": []any{map[string]any{}},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("del(rays[].wl[].s) = %#v, want %#v", got, want)
	}
}

func TestDelFilter(t *testing.T) {
	root := map[string]any{
		"rays": []any{
			map[string]any{"id": "a", "x": float64(1)},
			map[string]any{"id": "b", "x": float64(2)},
		},
	}
	got := applyDelForTest(t, `del(rays[id="a"].x)`, root)
	want := map[string]any{
		"rays": []any{
			map[string]any{"id": "a"},
			map[string]any{"id": "b", "x": float64(2)},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf(`del(rays[id="a"].x) = %#v, want %#v`, got, want)
	}
}

func TestDelMultiplePaths(t *testing.T) {
	root := map[string]any{"a": float64(1), "b": float64(2), "c": float64(3)}
	got := applyDelForTest(t, "del(a, c)", root)
	want := map[string]any{"b": float64(2)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("del(a, c) = %#v, want %#v", got, want)
	}
}

func TestDelMissingPathNoOp(t *testing.T) {
	root := map[string]any{"a": float64(1)}
	got := applyDelForTest(t, "del(nonexistent)", root)
	want := map[string]any{"a": float64(1)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("del(nonexistent) = %#v, want %#v", got, want)
	}
}

func TestDelEmptyArrayNoOp(t *testing.T) {
	root := map[string]any{"a": []any{}}
	got := applyDelForTest(t, "del(a[].x)", root)
	want := map[string]any{"a": []any{}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("del(a[].x) = %#v, want %#v", got, want)
	}
}

func TestDelPreservesOriginal(t *testing.T) {
	root := map[string]any{"a": float64(1), "b": float64(2)}
	_ = applyDelForTest(t, "del(a)", root)
	// Original must not be mutated
	if root["a"] == nil {
		t.Error("original was mutated by del")
	}
}

func TestDelCLI(t *testing.T) {
	yaml := "a:\n  b: 1\n  c: 2\nd: 3\n"
	out, code := runQueryCLI(t, yaml, "--yaml", "--edit", "del(a.b)", ".")
	if code != 0 {
		t.Fatalf("del cli exit %d", code)
	}
	if !strings.Contains(out, "c: 2") || !strings.Contains(out, "d: 3") || strings.Contains(out, "b:") {
		t.Errorf("del cli output = %q", out)
	}
}

func TestDelCLIWildcard(t *testing.T) {
	yaml := "items:\n  - {x: 1, y: 2}\n  - {x: 3, y: 4}\n"
	out, code := runQueryCLI(t, yaml, "--yaml", "--edit", "del(items[].y)", ".")
	if code != 0 {
		t.Fatalf("del wildcard exit %d", code)
	}
	if strings.Contains(out, "y:") {
		t.Errorf("del wildcard should remove y, got %q", out)
	}
	if !strings.Contains(out, "x: 1") || !strings.Contains(out, "x: 3") {
		t.Errorf("del wildcard should preserve x, got %q", out)
	}
}

func TestDelCLIFilter(t *testing.T) {
	yaml := "rays:\n  - {id: a, x: 1}\n  - {id: b, x: 2}\n"
	out, code := runQueryCLI(t, yaml, "--yaml", "--edit", `del(rays[id="a"].x)`, ".")
	if code != 0 {
		t.Fatalf("del filter exit %d", code)
	}
	if strings.Contains(out, "x: 1") {
		t.Errorf("del filter should remove x from id=a, got %q", out)
	}
	if !strings.Contains(out, "x: 2") {
		t.Errorf("del filter should preserve x from id=b, got %q", out)
	}
}

func TestQueryCLIRootDot(t *testing.T) {
	// YAML with top-level keys for testing .prefix syntax
	topLevelYAML := "focal_length: 25.033\nimage_space_f_number: 5.419\n"

	// . → entire YAML document (with --yaml outputs the root)
	out, code := runQueryCLI(t, topLevelYAML, "--yaml", ".")
	if code != 0 {
		t.Errorf("yaml '.' exit %d", code)
	}
	// Should contain the root keys
	if !strings.Contains(out, "focal_length") || !strings.Contains(out, "image_space_f_number") {
		t.Errorf("yaml '.' output = %q, want focal_length and image_space_f_number", out)
	}

	// .focal_length → same as focal_length
	out, code = runQueryCLI(t, topLevelYAML, "-r", ".focal_length")
	if code != 0 || strings.TrimSpace(out) != "25.033" {
		t.Errorf(".focal_length = (%q, %d), want 25.033, 0", out, code)
	}

	// .image_space_f_number → same as image_space_f_number
	out, code = runQueryCLI(t, topLevelYAML, "-r", ".image_space_f_number")
	if code != 0 || strings.TrimSpace(out) != "5.419" {
		t.Errorf(".image_space_f_number = (%q, %d), want 5.419, 0", out, code)
	}

	// .missing → default value
	out, code = runQueryCLI(t, topLevelYAML, "-r", ".missing_key")
	if code != 0 || strings.TrimSpace(out) != "-1" {
		t.Errorf(".missing_key = (%q, %d), want -1, 0", out, code)
	}
}
