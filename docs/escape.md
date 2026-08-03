# `rayweave escape` — escape-function global optimization

Finds multiple local minima of the merit function using the Ishiki-Ono style
escape-function method. DLS repeatedly converges to a local minimum; after each
convergence a smooth "escape bump" is added to the merit function around that
minimum, pushing the next DLS run out of the valley to discover other local
minima.

```
rayweave escape [--verbose] [--log FILE] [--save FILE] [--glass-dir DIR] < input.yaml
rayweave escape extract --index N < escape-output.yaml
```

## Options

| Flag | Description |
|---|---|
| `--glass-dir DIR` | AGF glass catalog directory |
| `--verbose` | print escape progress to stderr as JSONL (local minima, escape-parameter changes, per-cycle DLS status); every event carries a wall-clock `time` and `elapsed` seconds |
| `--log FILE` | write the same JSONL progress stream to `FILE` |
| `--save FILE` | save every discovered local minimum to `FILE1.yaml`, `FILE2.yaml`, … (see [Saving minima](#saving-minima)) |
| `--index N` | (with `escape extract`) local minimum index to extract |

Sub-commands:

- `escape` (default) — run the global optimization loop
- `escape extract --index N` — extract local minimum N as a clean lens YAML

## Input YAML — `optimization.escape`

```yaml
optimization:
  method: dls
  jacobian_workers: 8
  max_iter: 200
  variables: [...]          # same variable definitions as 'optimize'
  escape:
    max_cycles: 10          # DLS cycles per worker
    escape_workers: 4       # top-level parallel goroutines (default 4)
    max_seconds: 0          # soft wall-clock budget (seconds, shared; 0 = unlimited)
    distance_threshold: 0.1 # normalised distance to call a point "new"
    h_initial: 0.1          # escape bump height
    w_initial: 0.5          # escape bump width
    h_mult: 2.0             # strengthen factor when a minimum repeats
    w_mult: 1.3             # widen factor when a minimum repeats
    variable_weights:       # optional per-param weight (default 1.0)
      curvature: 1000
      thickness: 1
      nd: 10
      vd: 1
```

### Escape parameters

Escape parameters act in the **normalised variable space**: each variable is
scaled by its `min..max` range. Variables with `min == max` are excluded from
the escape distance. `distance_threshold` is a fraction of the normalised range
(default 0.1): a converged point is recorded as a "new" minimum only if its
normalised distance from every known minimum exceeds the threshold; otherwise
the nearest minimum's bump is strengthened (`h ×= h_mult`) and widened
(`w ×= w_mult`). When such a repeat arrives with a **better** merit, the stored
X and merit of that minimum are replaced (the better data is kept; the escape
strength from the strengthen step is retained).

The DLS solve inside each worker parallelises its Jacobian across
`jacobian_workers` (default `GOMAXPROCS`). With `escape_workers > 1` the total
goroutines are `escape_workers × jacobian_workers`; set `jacobian_workers: 1`
to avoid oversubscription.

### Time budget

`max_seconds` (default 0 = unlimited) is a **soft** wall-clock budget shared by
all workers: expiry is checked between DLS runs (at the start of each cycle), so
a running solve always finishes — the overshoot is bounded by one DLS run. The
search stops early when the budget is exhausted, discovered minima are still
reported, and the output marks `timed_out: true`.

### Interrupting the search

`rayweave escape` is designed for long runs. A `SIGINT`/`SIGTERM` stops it
gracefully: the signal is reported (a human line on stderr plus a JSONL
`interrupt` event in the `--verbose`/`--log` stream), the workers finish the
current DLS run and stop at the next cycle boundary, every discovered minimum is
saved, the stdout YAML is still written with `interrupted: true`, and the
process exits 0. A second signal force-quits immediately (exit 1). Because every
minimum is written atomically as it is found, even a hard kill never loses
already-discovered minima.

### Saving minima

`--save FILE` writes each discovered local minimum to a clean,
pipeline-compatible lens YAML (the full `Input` with that minimum's surfaces
applied, ready for `chief`/`trace`/`plot` or a re-optimisation):

- `FILE1.yaml`, `FILE2.yaml`, … in **discovery order** (a trailing `.yaml`/
  `.yml` on `FILE` is treated as the extension).
- When a minimum is improved, the current `FILE N.yaml` is renamed to
  `FILE N.<version>.yaml` (the old, worse version is kept) and the better point
  is written to `FILE N.yaml`.
- Writes are atomic (temp file + fsync + rename), so a killed process never
  leaves a partial file. The per-minimum file name is also recorded in
  `escape_result.minima[].file`.

## Output

The best solution is written to `configs[].surfaces` (pipeline-compatible with
`rayweave trace` / `rayweave plot`), and an `escape_result` section lists every
discovered local minimum with its full surfaces and variable values:

```yaml
escape_result:
  best_index: 0
  best_merit: ...
  timed_out: false              # true if the search was cut short by max_seconds
  interrupted: false            # true if a SIGINT/SIGTERM stopped the search
  params:
    h_initial: ...
    w_initial: ...
    h_mult: ...
    w_mult: ...
    distance_threshold: ...
    max_cycles: ...
    escape_workers: ...
    max_seconds: ...
  minima:
    - index: 0
      merit: ...
      file: result1.yaml        # --save output file for this minimum (if any)
      features:                  # compact fingerprint of the minimum (per config)
        - id: config1
          element_powers: [0.0075, -0.0041, 0.0022]
      surfaces: [...]
      variables: [...]
```

`features` is a compact fingerprint of each minimum for comparing minima
against each other — one entry per config (`id`), holding `element_powers`: the
thin-lens power of every lens element at the d-line, in system order (the sum
of the surface powers bounding each element, `(n-1)(c1-c2)` for a refractive
element in air; mirrors are single-surface elements with power `-2n/R`). A
single-config run has exactly one entry. `merit` stays at the minimum level as
the objective scalar; other feature values can be added per config later.

A concise summary is printed to stderr (never stdout, so the YAML pipeline
stays intact).

## Examples

```sh
# Run the global optimization, keep the result, and draw the best solution
rayweave escape < samples/escape-demo.yaml | tee escape-result.yaml \
  | rayweave trace | rayweave plot -o best.svg

# Save every discovered minimum to result1.yaml, result2.yaml, ...
rayweave escape --save result < samples/escape-demo.yaml > escape-result.yaml

# Extract a specific local minimum as a clean lens
rayweave escape extract --index 1 < escape-result.yaml > min1.yaml
rayweave escape extract --index 1 < escape-result.yaml \
  | rayweave trace | rayweave plot -o min1.svg

# List the discovered minima
rayweave query --each 'escape_result.minima[]:index,merit' \
  --printf '  [%d] merit=%.6e' < escape-result.yaml
```

The sample script `samples/escape-demo.bash` runs the same pipeline end to end:
the default lens is the degraded US2645157 triplet, and `--lens doublegauss`
switches to `samples/doublegauss-init.yaml` (which carries its own
`optimization.escape` section). The double-Gauss run is much slower.

## Method

The escape-function mathematics and the two-step (escape-then-clean) cycle are
described in [methods/escape-function.md](methods/escape-function.md).
