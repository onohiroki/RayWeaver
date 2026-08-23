# `rayweave query` — read-only YAML/JSONL selector

`rayweave query` turns a YAML (or JSON-Lines) stream into one plain-text value,
one row per list element, or a structured YAML/JSON/CSV document. It replaces
the `python3 -c "import yaml; ..."` snippets the sample demo scripts used to
contain, so the demos depend only on the `rayweave` binary.

`query` is **read-mostly**: in the default mode it is a terminal consumer of a
pipeline and does not modify the input. With `--edit` it applies in-memory
mutations (deep copy) before the selector runs. With `--yaml`/`--json` it can
also extract a subtree for further use.

```
rayweave query [--yaml|--json] [--csv PATH[:col,...]] [--printf FMT]
               [--each PATH[:col,...]] [--sum|--product|--count|--len PATH]
               [--jsonl [--where EXPR] [--first]] [--set VAR=EXPR] [--edit EXPR]
               [--gate EXPR] [--default STR] [--expr EXPR] [-r] [SELECTOR]
```

---

## 1. Basic usage

The most common case: run another subcommand, pipe its YAML into `query` and
read one field.

```sh
rayweave paraxial < lens.yaml | rayweave query -r paraxial_result.focal_length
rayweave chief  < lens.yaml | rayweave query -r chief_rays[0].spot_stats.rms_r
rayweave chief  < lens.yaml | rayweave query -r chief_rays[1].field_angle
```

Output is plain text on stdout, one value per line. Errors go to stderr with
the `rayweave[query]:` tag. Missing/null results print the `--default` value
(`-1` unless changed) and exit 0.

Scalars are printed raw (numbers, strings, `true`/`false`). A single-element
array unwraps to its element; a longer array prints as `[a b c]`. Mappings
require `--yaml`/`--json`.

```sh
$ rayweave query -r chief_rays[1].field_angle < chief.yaml
16
$ rayweave query -r chief_rays[1].spot_stats.rms_r < chief.yaml
0.0345
$ rayweave query -r chief_rays[].field_angle < chief.yaml   # array
[0 16 24]
```

---

## 2. SELECTOR: paths and expressions

The positional SELECTOR (or `--expr`) is a small expression language. Plain
paths are a subset of it.

### Paths

| Path | Meaning |
|---|---|
| `paraxial_result.focal_length` | walk a mapping |
| `chief_rays[0].spot_stats.rms_r` | numeric index into an array |
| `chief_rays[field_angle=0]` | keep array elements where `field_angle == 0` |
| `chief_rays[id="config1"]` | string equality filter |
| `configs[id=config0].surfaces[id=2].thickness` | chained filters + walk |
| `results[].surfaces[interaction=REFLECT].intensity_s` | map an array (see below) |
| `terms:key,value` | array/map iteration **columns** (only with `--each`/`--csv`) |

A `.key` on an array maps the access over the elements (`list[].k` returns the
array of `k` values). `[key=value]` returns the filtered array; a subsequent
`.field` then maps over it, so `chief_rays[field_angle=0].spot_stats.rms_r`
is a one-element array that the default output mode unwraps to a scalar.

### Expressions

Expressions add arithmetic, comparisons, logic, functions, literals, and
bindings (see `--set` below):

```sh
rayweave query --set a=chief_rays[1].field_angle \
               --set ih=chief_rays[1].image_height[1] \
               --expr '100*(ih-efl*tan(radians(a)))/(efl*tan(radians(a)))' \
               --set efl=25.03
```

- Operators: `+ - * / %` , unary `-`, comparisons `== != < <= > >=`,
  logic `&& || !`, and a ternary `cond ? a : b`.
- Math functions: `abs sqrt pow min max sin cos tan asin acos atan atan2
  radians degrees exp log floor ceil round`. Constants `pi`, `e`.
- `len(x)` (array/map/string length), `has("key")` (is the key present? useful
  with `--jsonl`).
- Literals: numbers, `"strings"`, `true/false/null`, arrays `[1, 2, 3]`, and
  structs `{efl: paraxial_result.focal_length, fno: image_space_f_number}`.

Division by zero is an error, so guard it with a ternary:

```sh
rayweave query --set a=chief_rays[0].field_angle --set efl=25.03 \
    --expr 'a < 1e-9 ? 0 : 100*tan(radians(a))*efl/2' < chief.yaml
```

---

## 3. Bindings: `--set VAR=EXPR`

`--set VAR=EXPR` evaluates EXPR (a path, a number, or an arithmetic
expression) and stores it as a variable. Bindings are evaluated **in order**;
a later binding can reference an earlier one.

```sh
rayweave query --yaml \
  --set efl=paraxial_result.focal_length \
  --set fno=paraxial_result.image_space_f_number \
  --set airy='1.22*0.0005876*fno' \
  < paraxial.yaml
```
```
airy: 0.003884741700608052
efl: 25.03330446879033
fno: 5.419017203361343
```

With `--yaml`/`--json` and no SELECTOR, the bindings are emitted as a record
(as above). With a SELECTOR, bindings are just available to the expression.

---

## 4. Output modes

### Text (default)

Scalars print raw; arrays print bracketed; mappings require `--yaml`/`--json`.

### `--yaml` / `--json`

Serialize whatever the query produced:

```sh
rayweave query --yaml 'chief_rays[0].spot_stats' < chief.yaml   # subtree dump
rayweave query --json --set efl=paraxial_result.focal_length < paraxial.yaml
```

For `--each`, `--yaml` emits a list of row mappings (see below).

### `--printf FMT`

Apply a Go `fmt` format string to the value (or to each `--each` row).
Whole numbers become `int64` for integer verbs (`%d`), stay `float64` for
floating-point verbs (`%.4f`, `%.6e`), and become their decimal string for
`%s`/`%v`.

```sh
rayweave query --each 'escape_result.minima[]:index,merit' \
               --printf '  [%d] merit=%.6e' < escape.yaml
```

### `--csv PATH:col1,col2,...`

Iterate an array and emit one CSV row per element. Rows with a **missing/null
column are skipped**, which is the jq `select(.x != null)` behaviour the
spot-diagram demos need. Add `--csv-header` for a header row.

```sh
rayweave query --csv 'chief_rays[0].grid_points[]:image_x,image_y,intensity' \
    < chief.yaml > spots.csv
```

### `--gate EXPR`

Evaluate EXPR, print the result (or a `pass:` record with `--yaml`/`--json`)
and exit 0/1 by truthiness. This replaces the `python3 -c "print('1' if ...)"`
pass-gate pattern.

```sh
if rayweave query --gate 'abs(efl-50.0)<=0.01' \
        --set efl=paraxial_result.focal_length < paraxial.yaml; then
  echo "EFL is on target"
fi

rayweave query --yaml --gate 'rms < 0.3' \
        --set rms=chief_rays[0].spot_stats.rms_r < chief.yaml
# → rms: 0.0011
#   pass: true
```

---

## 5. Iteration and aggregates

### `--each 'PATH:col1,col2,...'`

Print one row per element of an array (or per entry of a mapping). Columns are
resolved relative to each element; missing columns print `--default`.

```sh
# all field angles
rayweave query --each 'chief_rays[]:field_angle' --printf 'field %s deg' < chief.yaml

# escape local minima as structured YAML
rayweave query --yaml --each 'escape_result.minima[]:index,merit' < escape.yaml

# optimize-log breakdown terms (a mapping → key/value columns)
rayweave query --jsonl --where 'event=="breakdown"' \
    --each 'terms:key,value' --printf '  %s: %.6e' < opt.jsonl
```

A `[]` segment maps over the array (`results[].surfaces[]` iterates every
surface of every result; `--each` flattens one level of nesting).

### Aggregates

| Flag | Meaning |
|---|---|
| `--count PATH` | number of non-null values in the array resolved by PATH |
| `--len PATH` | length of the array/mapping |
| `--sum PATH` | sum of the numeric values |
| `--product PATH` | product of the numeric values (empty → 1) |
| `--stdev PATH` | population standard deviation of the numeric values |

```sh
# vignetting factor numerator / denominator
n=$(rayweave query --count 'chief_rays[1].grid_points[].image_x' < chief.yaml)
d=$(rayweave query --len 'chief_rays[1].grid_points' < chief.yaml)

# ghost relative intensity (product of the Fresnel reflectances)
rayweave query --product 'results[0].surfaces[interaction=REFLECT].intensity_s' \
    < ghost.yaml

# wavefront OPD RMS = population stddev of the pupil-grid OPL
rayweave query --stdev 'chief_rays[0].grid_points[].opl' < chief.yaml
```

---

## 6. Input: `--jsonl` and optimize logs

`optimize --log FILE` writes one JSON object per line. `--jsonl` reads that
format instead of a single YAML document.

- By default the SELECTOR / `--set` resolve against the **last** line (add
  `--first` to use the first).
- `--where EXPR` keeps only matching lines; the last (or first) match is then
  used as the document.
- `--each '[]:col'` iterates the (filtered) lines themselves.
- `--count '[]'` counts the (filtered) lines.

```sh
# final merit
rayweave query --jsonl --where 'has("merit")' -r merit < opt.jsonl

# first merit (with --first)
rayweave query --jsonl --where 'has("merit")' --first -r merit < opt.jsonl

# number of merit lines
rayweave query --jsonl --where 'has("merit")' --count '[]' < opt.jsonl

# final status
rayweave query --jsonl --where 'has("status")' -r status < opt.jsonl

# per-term merit breakdown of the final state
rayweave query --jsonl --where 'event=="breakdown"' \
    --each 'terms:key,value' --printf '  %s: %.6e' < opt.jsonl
```

`--where` is only valid with `--jsonl`. Note that `--where 'event=="breakdown"'`
needs the double quotes inside single quotes because the shell eats the outer
layer.

---

## 7. Full flag reference

| Flag | Description |
|---|---|
| `SELECTOR` / `--expr EXPR` | expression to evaluate (paths are a subset) |
| `--yaml` | output the result as YAML |
| `--json` | output the result as JSON |
| `--csv PATH:col,...` | CSV rows over an array; skip rows with missing columns |
| `--csv-header` | with `--csv`, print a header row |
| `--each PATH:col,...` | one row per element; columns resolved per element |
| `--count PATH` | count non-null values |
| `--len PATH` | array/map length |
| `--sum PATH` | sum of numeric values |
| `--product PATH` | product of numeric values |
| `--stdev PATH` | population standard deviation of numeric values |
| `--jsonl` | read JSON Lines (optimize `--log` output) |
| `--where EXPR` | with `--jsonl`, keep only matching lines |
| `--first` | with `--jsonl`, use the first matching line instead of the last |
| `--gate EXPR` | print a pass record and exit 0/1 by truthiness |
| `--set VAR=EXPR` | bind a variable (repeatable, ordered) |
| `--edit EXPR` | mutate the document in-memory (repeatable, see §10) |
| `--printf FMT` | Go fmt format string for the value / each row |
| `--default STR` | value for missing/null results (default `-1`) |
| `-r`, `--raw` | raw text output (the default for scalars) |

Notes:

- `--yaml` and `--json` are mutually exclusive, as are `--each` and `--csv`,
  and the four aggregates.
- Numeric indices are zero-based; a negative index counts from the end.
- The expression language uses double quotes for strings; wrap expressions in
  single quotes in the shell.
- Go's `flag` package consumes the value of `--each`/`--csv`/`--printf`
  **directly after the flag**. Put the path/format right after its flag, then
  other flags: `--each 'a[]:x' --printf '%.2f'`.

---

## 8. Worked example: double-Gauss evaluation

```sh
PARAXIAL=$(rayweave paraxial < result.yaml 2>/dev/null)
CHIEF=$(rayweave chief < result.yaml 2>/dev/null)

efl=$(echo "$PARAXIAL" | rayweave query -r paraxial_result.focal_length)
fno=$(echo "$PARAXIAL" | rayweave query -r paraxial_result.image_space_f_number)

for fi in 0 1 2 3; do
  rms=$(echo "$CHIEF" | rayweave query -r "chief_rays[$fi].spot_stats.rms_r")
  ang=$(echo "$CHIEF" | rayweave query -r "chief_rays[$fi].field_angle")
  dist=$(echo "$CHIEF" | rayweave query \
    --set a="chief_rays[$fi].field_angle" \
    --set ih="chief_rays[$fi].image_height[1]" \
    --expr "a < 1e-9 ? 0 : 100*(ih-$efl*tan(radians(a)))/($efl*tan(radians(a)))")
  printf "  field %s deg  RMS = %.4f mm  distortion = %.2f%%\n" "$ang" "$rms" "$dist"
done

rayweave query --gate "rms < 0.1" --set rms="chief_rays[0].spot_stats.rms_r" \
  <<< "$CHIEF" || { echo "on-axis RMS gate failed"; exit 1; }
```

---

## 9. Demo integration

| Demo script | query usage |
|---|---|
| `ghost-demo.bash` | `chief --preserve-rays`, `--each` surface table, `--product` Fresnel intensities |
| `doublegauss-demo.bash` | scalar extraction, `--set`+expr distortion, `--jsonl` logs, `--gate` |
| `escape-demo.bash` | `--each --printf` minima rows |
| `simple-zoom-demo.bash` | filters `[field_angle=0]`, `[id=..]`, `--gate` |
| `scale-demo.bash` | scalar extraction, `--gate abs(...)` |
| `schmidt-demo.bash` | scalar extraction, `spot_stats.rms_r` |
| `optimize-demo.bash` | scalar extraction, `--gate` |
| `glass-optimize-demo.bash` | `--set`+expr Airy/RMS, `--csv` spot diagrams, `--jsonl` |
| `multi-config-zoom-demo.bash` | `--each`, `--count/--len`, `--gate` |
| `run-demo.bash` | `--csv` spot/aberration extraction, `--yaml` paraxial display |

## 10. Mutations: `--edit` (yq-style in-memory edits)

`--edit EXPR` mutates a **deep copy** of the input document before the
selector is evaluated. Multiple `--edit` flags are applied in order, later
edits see earlier ones. The original stdin document is never modified.

```
rayweave query --edit 'a.b.c = 99' --edit 'del a.b.d' --yaml '.' < in.yaml
rayweave query --edit 'surfaces[0].thickness = 5.5' -r surfaces[0].thickness < lens.yaml
```

### Supported syntax

| Syntax | Meaning | Example |
|--------|---------|---------|
| `PATH = VALUE` | Set (auto-creates intermediate maps) | `a.b.c = 99` , `surfaces[0].thickness = 5.5` |
| `PATH \|= EXPR` | Update with `.` bound to the current value | `a.b \|= . * 2` , `a.b \|= sqrt(.)` |
| `ARR += VALUE` / `ARR[] += VALUE` | Append to array | `arr += 3` , `arr[] += {x: 1}` |
| `ARR -= INDEX` | Remove array element at INDEX | `arr -= 1` , `arr -= -1` |
| `del PATH` | Delete map key or array element | `del a.b` , `del surfaces[1]` |

PATH is a dot/index chain (`a.b[0].c`, `configs[0].surfaces[2].thickness`);
the same filter/index syntax as SELECTORs but only the `ident / .key / [N]`
subset is valid for mutations. Missing intermediate maps on `=` are
auto-created (yq semantics); setting through a scalar (`a: 1` then
`a.b.c = 2`) is an error.

`VALUE` / `EXPR` is any query expression (literals, arithmetic, function
calls, `{k: v}` / `[1,2]` constructors, `.` in `|=`).

### Examples

```sh
# Deep nesting and array indexing
rayweave query --yaml --edit 'paraxial_result.focal_length = 50.0' < lens.yaml
rayweave query --yaml --edit 'surfaces[1].thickness = 10.0' < lens.yaml
rayweave query --yaml --edit 'configs[0].surfaces[0].thickness = 5.5' < lens.yaml

# Chained edits and edit-then-select
rayweave query --yaml --edit 'a.b.c = 99' --edit 'del a.b.d' '.' < in.yaml
rayweave query --json --edit 'a.b = 99' 'a' < in.yaml   # → {"b":99,"c":2}

# |= with the current value
rayweave query --yaml --edit 'a.b |= . + 1' < in.yaml
rayweave query --yaml --edit 'a.b |= sqrt(.)' < in.yaml

# Arrays
rayweave query --yaml --edit 'arr += 3' < in.yaml        # append
rayweave query --yaml --edit 'arr[] += 3' < in.yaml      # same
rayweave query --yaml --edit 'arr -= 0' < in.yaml        # remove index 0
rayweave query --yaml --edit 'del arr[-1]' < in.yaml     # negative index
```

Notes:

- Input is read from stdin; there is no `--in-place` file rewrite.
- An `arr[]` on the LHS is syntactic sugar for `arr`; both append to `arr`.
- `|=` evaluates its RHS with `.` bound to the **current** value at PATH
  (like `jq`); other identifiers in the RHS resolve against that current
  value, not the document root.
