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
| `--verbose` | print escape progress to stderr as **compact** JSONL (keys follow the fixed order `cycle`, `e_min`, `t`, `event`, `merit`, `worker`, `index`, `kind`, `dls_status`, `phase`, `distance_threshold`, `h`, `h_mult`, `w`, `w_mult`, `max_cycles`, `max_seconds`, `workers`, `escaped`, `recorded`, `best_merit`, `cycles`, `escapes`, `minima`; floats are 6-significant-figure exponent notation, `e_min` is whole minutes since run start, `t` is `HH:MM:SS`; `status`, `signal`, `timed_out` and `interrupted` are omitted — they are conveyed by the `cycle`/`timeout`/`interrupt`/`interrupted` events themselves) |
| `--log FILE` | write the **full** JSONL progress stream to `FILE` (same fields as before — full-precision floats, RFC3339 `time`, `elapsed` seconds, `status`/`signal`/`timed_out`/`interrupted` included — with keys in the same fixed order followed by the remaining keys alphabetically) |
| `--save FILE` | save every discovered local minimum to `FILE0.yaml`, `FILE1.yaml`, … (see [Saving minima](#saving-minima)) |
| `--index N` | (with `escape extract`) local minimum index to extract |

`--glass-dir` is written back into the output's `glass_catalog.directory`
(CLI/YAML rule); `--save` records the per-minimum files in
`escape_result.minima[].file`; `--verbose` / `--log` are run-stream flags.

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
    fingerprint_distance_threshold: 0  # design-fingerprint distance for "new" (0 = off)
    h_initial: 0.1          # escape bump height
    w_initial: 0.5          # escape bump width
    h_mult: 2.0             # strengthen factor when a minimum repeats
    w_mult: 1.3             # widen factor when a minimum repeats
    variable_weights:       # optional per-param weight (default 1.0)
      curvature: 1000
      thickness: 1
      nd: 10
      vd: 1
    # Optional execution tuning (defaults shown; 0 = default):
    escape_iter_frac: 0.333 # escape-phase MaxIter as a fraction of the full budget
    w_span: 2.0             # per-worker W scaling span: W*(1 + i/(N-1)*(w_span-1))
    stall_window_frac: 0.2  # stalled-early-stop window as a fraction of escape MaxIter
    stall_rel_tol: 1e-4     # stalled-early-stop relative merit threshold
    stall_early_stop: true  # stalled-early-stop in the escape phase (clean phase never stalls)
    initial_perturb: 0.05   # normalised amplitude spreading parallel workers' start points
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

### Design fingerprint (`fingerprint_distance_threshold`)

The "new minimum" test can additionally require a **structural** difference, not
just a numerical one. When `fingerprint_distance_threshold > 0`, the command
maps each candidate to a design fingerprint — the thin-lens element powers
(`paraxial.ElementPowers`, the same values reported per minimum as
`features[].element_powers`; multi-config runs concatenate every config) — and
a converged point is a **repeat** only when it is close in variable space
*and* close in fingerprint space:

```
repeat ⇔ distance(x, p) < distance_threshold
         AND fingerprint_distance(x, p) < fingerprint_distance_threshold
```

The fingerprint distance is the per-element RMS power difference
`sqrt(mean(Δp²))` in `1/mm`, so a solution that is numerically close but has a
different element-power distribution (e.g. a different crown/flint split) is
recorded as a **distinct** minimum instead of strengthening the old one. A
mismatch in the number of elements (structural topology change) is always
treated as distinct. The threshold is an absolute power scale and therefore
system-dependent (a 50 mm lens has powers ~0.02, a 100 mm lens ~0.01); start
near the observed inter-minimum power spread and tune per system. `0` (the
default) keeps the original variable-distance-only behaviour.

The DLS solve inside each worker parallelises its Jacobian across
`jacobian_workers`. Under the `escape` command an unset `jacobian_workers`
defaults to **2** instead of `GOMAXPROCS`. With `escape_workers > 1` the total
goroutines are `escape_workers × jacobian_workers`; set `jacobian_workers: 1`
with many escape workers to avoid oversubscription.

### Execution tuning

Escape-phase DLS solves have a capped iteration budget:
`escape_iter_frac` (default 1/3) of the full `max_iter` budget, floored at 50
iterations. Once the best merit has failed to improve by at least
`stall_rel_tol` (default `1e-4`, relative) over a `stall_window_frac`
(default 0.2) window of iterations, that escape-phase solve returns early with
the best point found (`stall_early_stop: true`, the default). The clean
re-optimisation phase — the source of slow, late-stage merit improvements —
**always** runs the full budget and never stalls. Set `stall_early_stop: false`
to disable stalled-early-stop in the escape phase entirely.

`w_span` (default 2.0) widens worker diversity: worker `i` of `N` uses
`W × (1 + i/(N-1) × (w_span-1))`, spreading the escape-bump widths so parallel
workers drift toward different basins. `initial_perturb` (default 0.05, in the
normalised variable space) seeds the paralllel workers at slightly different
start points. With the default `initial_perturb` all workers converge toward the
same basin, so this is usually the first knob to raise.

### Exploration depth vs. breadth

Two search strategies are useful, and the knobs above map directly onto them.

**Broad search** — survey many distinct basins, accepting that each is explored
shallowly. This suits coarse stage-one searches (e.g. when the dome is unknown
and a diverse set of starting points is more valuable than a deep local solve).

- raise `initial_perturb` (e.g. `0.10`) and `w_span` (e.g. `3.0`) so workers
  spread widely
- widen `w_initial` so the escape bump covers a larger neighbourhood, letting
  the next run leave the valley and reach distant regions
- lower `escape_iter_frac` (e.g. `0.25`): each basin is only refined briefly
  before escaping
- the trade-off is a shallower best merit per basin

**Deep search** — concentrate on a narrow region and refine it thoroughly.
This suits refining a known-good solution or a second-pass sweep around a
promising dome.

- lower `initial_perturb` (e.g. `0.02`) and `w_span` (e.g. `1.5`) so workers
  stay close together
- narrow `w_initial` so the escape bump stays local, keeping re-runs in the same
  neighbourhood
- raise `escape_iter_frac` (e.g. `0.5`): each basin is solved to a tight
  convergence before moving on
- the trade-off is fewer basins visited per unit time

In both cases `max_cycles` trades breadth against depth of a setting, and
`escape_workers` scales the wall-clock cost (each worker is independent, so a
higher count broadens the search cheaply). Nothing here changes the two-phase
(escape-then-clean) structure — the clean phase always refines the final best
point with the full budget.

#### Parameter summary

| Parameter | Balanced (default) | Broad | Deep |
|---|---|---|---|
| `initial_perturb` | 0.05 | 0.10 | 0.02 |
| `w_span` | 2.0 | 3.0 | 1.5 |
| `w_initial` | 0.5 | 0.8 | 0.3 |
| `escape_iter_frac` | 1/3 | 0.25 | 0.5 |
| `max_cycles` | 10 | lower | higher |
| `escape_workers` | 4 | higher for cheap breadth | lower |

`w_initial` and `max_cycles` are set alongside the four tuning knobs above. The
remaining performance fields (`h_initial`, `w_mult`, `max_seconds`, …) are
independent of the breadth/depth trade-off.

#### Example: broad search

```yaml
optimization:
  method: dls
  escape:
    escape_workers: 8
    max_cycles: 6
    initial_perturb: 0.10
    w_span: 3.0
    w_initial: 0.8
    escape_iter_frac: 0.25
```

#### Example: deep search

```yaml
optimization:
  method: dls
  escape:
    escape_workers: 2
    max_cycles: 15
    initial_perturb: 0.02
    w_span: 1.5
    w_initial: 0.3
    escape_iter_frac: 0.5
```

### Time budget

`max_seconds` (default 0 = unlimited) is a **soft** wall-clock budget shared by
all workers: expiry is checked between DLS runs (at the start of each cycle), so
a running solve always finishes — the overshoot is bounded by one DLS run. The
search stops early when the budget is exhausted, discovered minima are still
reported, and the output marks `timed_out: true`.

### Interrupting the search

`rayweave escape` is designed for long runs. A `SIGINT`/`SIGTERM` stops it in
three escalating stages, and the first two still complete normally
(`interrupted: true`, exit 0):

1. **First signal** — graceful stop. The signal is reported (a human line on
   stderr plus a JSONL `interrupt` event in the `--verbose`/`--log` stream), the
   shared context is cancelled, workers finish the current DLS run and stop at
   the next cycle boundary, every discovered minimum is saved, the stdout YAML
   is still written with `interrupted: true`, and the process exits 0.
2. **Second signal** — mid-DLS stop. A JSONL `interrupt_dls` event is emitted
   and the running DLS solve is aborted within one iteration (at the iteration
   top, after the pupil update, inside the line search, and between Jacobian
   column sweeps). The solve's **best point so far** is preserved as a minimum
   and saved to the `--save` files. The run still exits 0 with `interrupted: true`.
3. **Third signal** — force quit with exit 1.

Because every minimum is written atomically as it is found, even a hard kill
never loses already-discovered minima.

### Saving minima

`--save FILE` writes each discovered local minimum to a clean,
pipeline-compatible lens YAML (the full `Input` with that minimum's surfaces
applied, ready for `chief`/`trace`/`plot` or a re-optimisation):

- `FILE0.yaml`, `FILE1.yaml`, … in **discovery order**, matching the 0-based
  `index` of the JSONL `minimum` events (a trailing `.yaml`/`.yml` on `FILE` is
  treated as the extension).
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
    escape_iter_frac: ...
    w_span: ...
    stall_window_frac: ...
    stall_rel_tol: ...
    stall_early_stop: ...
    initial_perturb: ...
  minima:
    - index: 0
      merit: ...
      file: result0.yaml        # --save output file for this minimum (if any)
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

# Save every discovered minimum to result0.yaml, result1.yaml, ...
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
