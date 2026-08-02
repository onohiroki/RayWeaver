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

## Output

The best solution is written to `configs[].surfaces` (pipeline-compatible with
`rayweave trace` / `rayweave plot`), and an `escape_result` section lists every
discovered local minimum with its full surfaces and variable values:

```yaml
escape_result:
  best_index: 0
  best_merit: ...
  params:
    h_initial: ...
    w_initial: ...
    h_mult: ...
    w_mult: ...
    distance_threshold: ...
    max_cycles: ...
    escape_workers: ...
  minima:
    - index: 0
      merit: ...
      surfaces: [...]
      variables: [...]
```

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

## Method

The escape-function mathematics and the two-step (escape-then-clean) cycle are
described in [methods/escape-function.md](methods/escape-function.md).
