# `rayweave escape` — escape-function global optimization

Finds multiple local minima of the merit function using the Ishiki-Ono style
escape-function method. DLS repeatedly converges to a local minimum; after each
convergence a smooth "escape bump" is added to the merit function around that
minimum, pushing the next DLS run out of the valley to discover other local
minima.

```
rayweave escape [--glass-dir DIR] < input.yaml
rayweave escape extract --index N < escape-output.yaml
```

## Options

| Flag | Description |
|---|---|
| `--glass-dir DIR` | AGF glass catalog directory |
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
(`w ×= w_mult`).

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

## Output

The best solution is written to `configs[].surfaces` (pipeline-compatible with
`rayweave trace` / `rayweave plot`), and an `escape_result` section lists every
discovered local minimum with its full surfaces and variable values:

```yaml
escape_result:
  best_index: 0
  best_merit: ...
  timed_out: false              # true if the search was cut short by max_seconds
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
