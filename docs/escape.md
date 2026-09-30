# `rayweave escape` — escape-function global optimization

Finds multiple local minima of the merit function using the Ishiki-Ono style
escape-function method. DLS repeatedly converges to a local minimum; after each
convergence a smooth "escape bump" is added to the merit function around that
minimum, pushing the next DLS run out of the valley to discover other local
minima.

```
rayweave escape [--verbose] [--log FILE] [--save FILE] [--stream BOOL] [--glass-dir DIR] < input.yaml
rayweave escape extract --index N < escape-output.yaml
```

## Options

| Flag | Description |
|---|---|
| `--glass-dir DIR` | AGF glass catalog directory |
| `--stream BOOL` | stream the pipeline document to stdout as the run progresses: `true` (default) or `false` for the one-shot write performed after the run (see [Streaming output](#streaming-output)) |
| `--verbose` | print escape progress to stderr as **compact** JSONL (keys follow the fixed order `cycle`, `e`, `t`, `event`, `merit`, `worker`, `index`, `kind`, `dls_status`, `phase`, `distance_threshold`, `h`, `h_mult`, `w`, `w_mult`, `max_cycles`, `max_seconds`, `workers`, `escaped`, `recorded`, `best_merit`, `cycles`, `escapes`, `minima`; floats are 6-significant-figure exponent notation, `e` is elapsed since run start as `HH:MM`, `t` is wall-clock `HH:MM:SS`; `status`, `signal`, `timed_out` and `interrupted` are omitted — they are conveyed by the `cycle`/`timeout`/`interrupt`/`interrupted` events themselves) |
| `--log FILE` | write the **full** JSONL progress stream to `FILE` (same fields as before — full-precision floats, RFC3339 `time`, `elapsed` seconds, `status`/`signal`/`timed_out`/`interrupted` included — with keys in the same fixed order followed by the remaining keys alphabetically) |
| `--save FILE` | save every discovered local minimum to `FILE0.yaml`, `FILE1.yaml`, … (see [Saving minima](#saving-minima)) |
| `--power-solve` | insert the power-preserving glass phase between each escape and clean DLS: locks every variable except the glass dispersions, emphasises the config's chromatic merit terms (scaled by `power_solve.color_scale`), keeps a cheap geometric guardrail, and holds the element powers fixed |
| `--power-solve-surfaces A,B,…` | surface IDs whose curvature is recomputed to hold the containing element's thin-lens power (with `--power-solve`) |
| `--glass-variables` | auto-generate nd/vd variables for every refractive element (the merit is left unchanged) |
| `--index N` | (with `escape extract`) local minimum index to extract |
| `--keep-infeasible` | include `escape_result.infeasible_basins[]` in stdout YAML (default: discard; `--save` never includes infeasible basins) |

`--glass-dir` is written back into the output's `glass_catalog.directory`
(CLI/YAML rule); `--save` records the per-minimum files in
`escape_result.minima[].file`; `--verbose` / `--log` are run-stream flags;
`--power-solve` / `--power-solve-surfaces` echo the effective
`power_solve` config into the output; `--stream` is written back as
`optimization.escape.stream` **only when the flag is given**, so an unset flag
never overrides the input YAML.

DLS-internal events are never emitted during escape: the per-iteration `iter`,
per-solve `final`, `adaptive_damping` and `mode_change` records that
`optimize --verbose` produces do not appear in the escape streams (regardless
of the options). The progress stream is the single output channel — each
`cycle` event already carries the DLS outcome as `dls_status` and `merit`.

Sub-commands:

- `escape` (default) — run the global optimization loop
- `escape extract --index N` — extract local minimum N as a clean lens YAML.
  `N` is a position in the document's `minima` list, and the improved entry for
  that position (`minima_improvements`, when present) is applied, so the
  extracted lens is that minimum's final version

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
    stream: true            # append the pipeline document to stdout as the run progresses
    hull_rescue: true       # project a hull-violating point onto the glass hull and re-solve
    hull_rescue_iter_frac: 0.25 # rescue re-solve MaxIter as a fraction of the full budget
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

`stream` (default `true`) selects how the pipeline document reaches stdout:
incrementally while the run progresses, or in one write after it (see
[Streaming output](#streaming-output)). `--stream true|false` overrides it.

### Power-preserving glass phase

Between the escape DLS (layout exploration) and the clean DLS (full refinement)
each cycle can run a dedicated **power-preserving glass phase**, enabled by
listing the solve surfaces in `optimization.power_solve` (or `--power-solve` +
`--power-solve-surfaces`):

```yaml
optimization:
  power_solve:
    enabled: true
    surfaces: [2, 4, 6, 9, 11, 13]  # dependent back surface of each element
```

During the `glass_dls` phase the Optimizer (via its optional `glassPhaseable`
capability, kept decoupled from the escape package) locks **every variable
except the glass dispersions (nd/vd)** to its current value — DLS keeps a
`Min == Max` variable fixed — and enables the **power-preserving solve** so each
element's thin-lens power (and therefore the nominal focal length) stays
constant while only the glasses move. The phase objective is built from the
config's **own** terms, never auto-generated: its chromatic terms
(`longitudinal_color` / `lateral_color`) are scaled by
`optimization.power_solve.color_scale` (default 100) so colour leads the phase,
while the config's cheap analytic geometric terms (`seidel_*`, `focal_length`,
`distortion_pct`, `glass_role`) are retained at their configured weights as a
**guardrail**. Expensive grid-trace terms (`spot_rms*`, `geometric_mtf_*`,
`wavefront_*`, `field_alive`, `pupil_fill`) are excluded so the phase stays
cheap. This
decouples the glass gradient from the layout: a glass change no longer drifts
focus, so the axial and lateral chromatic aberration are improved by the glass
balance itself, while the guardrail keeps the colour solve from wandering onto a
geometrically destructive colour-null. Add the nd/vd variables with
`--glass-variables` (or declare them) if the input does not already list them.

```yaml
optimization:
  power_solve:
    enabled: true
    surfaces: [2, 4, 6, 9, 11, 13]  # dependent back surface of each element
    color_scale: 100                # chromatic-term emphasis during the phase
```

Because the variable-space **dimension is unchanged** — the non-glass variables
are locked, not removed — the escape `store`, distance calculation and next-cycle
start point continue to operate in the full variable space; only the *active*
set shrinks during the phase. `ExitGlassPhase` restores every bound before the
clean DLS, which starts from the glass-phase result and re-uses it as its own
start. Each cycle emits a `glass_dls` cycle event (accepted/rejected) with the
colour merit. Combine with the real-glass convex hull (on by default) so the
glasses stay physical while they are rebalanced.

**Power variables instead of `power_solve`**: the glass phase preserves element
powers without `optimization.power_solve` when the dependent back surfaces are
declared as `power` variables (see `docs/optimize.md`). The non-glass lock
freezes the power variables (`Min == Max`), so `SolveElementPower` keeps
reproducing the same dependent curvature while only the glasses move — the
power-preserving solve becomes active exactly during the phase. This is the
preferred setup for designs that must **build** element power (e.g. a flattened
zero-power start): `power_solve`'s initial-state snapshot would pin those zeros
forever, while a `power` variable starts at 0 and is driven by the merit
elsewhere. A surface driven by a `power` variable is skipped by `power_solve`'s
snapshot when both are declared, so the two can coexist (some elements pinned,
some variable-driven).

### Back-focus solve

The per-config back-focus hard solve (`optimization.back_focus_solve`) is also
honored by `escape`, which builds its worker Optimizers with the same options as
`optimize`. When enabled, every merit evaluation (escape, glass and clean phases)
first re-focuses the image plane, so the search is compared at a common best
focus instead of drifting with the layout. Its dynamic type switching follows the
escape's `optimization.merit_schedule` the same way (the dominant mode's
`back_focus_type` wins). See
[optimize.md](optimize.md#back-focus-solve-optimizationback_focus_solve).

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
   the next cycle boundary, every discovered minimum is saved, the completion is
   appended to the streaming stdout document with `interrupted: true` (or the
   one-shot YAML is written when streaming is off), and the process exits 0.
2. **Second signal** — mid-DLS stop. A JSONL `interrupt_dls` event is emitted
   and the running DLS solve is aborted within one iteration (at the iteration
   top, after the pupil update, inside the line search, and between Jacobian
   column sweeps). The solve's **best point so far** is preserved as a minimum
   and saved to the `--save` files. The run still exits 0 with `interrupted: true`.
3. **Third signal** — force quit with exit 1.

Because every minimum is written atomically as it is found, even a hard kill
never loses already-discovered minima. With streaming on (the default) the
stdout document itself is also written as the run progresses, so a hard kill
leaves a truncated — but still readable — document holding every minimum found
so far (see [Streaming output](#streaming-output)); the completion block
(`best_index`, `best_merit`, `configs`, …) is the part a force-quit run loses.

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

### Infeasible basins

Not every DLS-converged point is a valid optical design. A point whose
throughput drops below a threshold — any field blocked or severely vigneted —
represents an **infeasible basin**: a region of variable space where the
geometry is broken (insufficient clear aperture, rays lost to the stop, etc.).
The escape-function method classifies each converged point and handles the two
categories differently:

1. **Feasible local minimum** (`status: feasible_local_minimum`) — the point
   passes validation; it is recorded and reported as a solution.
2. **Infeasible basin** (`status: infeasible_basin`) — the point fails
   validation; it is **not** listed as a solution, but an escape bump is added
   at its location so the search does not re-enter the same broken basin.
3. **Evaluation failure** (`status: evaluation_failure`) — the merit is NaN or
   Inf; the point is not recorded at all (no bump, no solution).

The default infeasibility rule: **any** field with fewer than 30 % of its pupil
rays surviving the validation trace causes the point to be classified as
infeasible. The threshold is `optimization.escape.min_throughput_ratio` when
set, else the same 0.3 default used by the `field_alive` merit term, so the two
are consistent. The merit-side counterpart is the bounded `field_alive` plus the
unbounded `pupil_fill` kind: a partially clipped pupil shrinks the survivor-only
spot/OPD residuals, so `pupil_fill` (a non-zero weight) keeps the solver from
buying a smaller residual by killing a field's periphery.

Validation happens **post-DLS only** — after the clean DLS converges, the
converged point is traced through `chief.DetermineChiefRaysGrid` with the same
pupil model (dynamic or virtual) the DLS used. If validation fails, the DLS
result is still used for the escape bump, so the search is pushed away from the
broken basin.

The invalid reason is reported per basin:

| Reason | Meaning |
|---|---|
| `insufficient_field_throughput` | fewer than `min_throughput_ratio` (default 30 %) of pupil rays reach the image for at least one field |
| `field_unreachable` | zero rays reach the image for at least one field |
| `severe_vignetting` | intermediate vignetting caused catastrophic beam loss |
| `geometry_violation` | negative thickness or other surface-geometry error |
| `numerical_failure` | merit is NaN or Inf |
| `glass_hull_violation` | a converged nd/vd pair lies outside the real-glass convex hull (see below) |

By default infeasible basins are discarded from the stdout YAML. Pass
`--keep-infeasible` to include them as `escape_result.infeasible_basins[]`
(useful for diagnosing broken regions of variable space). The `--save` flag
**never** includes infeasible basins.

### Real-glass hull: guard band, escape weight, rescue

Glass variables are constrained to the real-glass convex hull by a smooth
penalty (`optimization.glass_hull`, on by default; see `docs/optimize.md`).
The penalty's smooth band is centred on the hull boundary, so an outward
optical gradient can park the DLS balance point slightly **outside** the hull,
where the point then fails validation with `invalid_reason:
glass_hull_violation` — real runs recorded several such minima (e.g. the
v40 campaign's `list escape` output was dominated by
`glass_hull_violation` entries). Three mechanisms keep escape solutions on
real glass without turning the hull into a hard wall:

1. **Guard band** (`optimization.glass_hull.guard_band`, default `0.01`,
   `0` disables): the penalty landscape (smooth band and outside ramp) is
   evaluated against a hull shrunk by `guard_band × hull radius` — capped at
   half the centroid's clearance to the boundary, so the guarded zone stays a
   shell around the edge and the deep interior landscape is untouched — which
   moves the ramp onset, and with it the DLS balance point, **inside** the
   true hull. The feasibility check (`Contains`, used by the escape
   validator) stays on the true hull, and exact catalogue points/vertices
   remain at zero penalty.
2. **Escape-phase weight** (`optimization.glass_hull.escape_weight_factor`,
   default `0.1`): during the escape exploration phase the whole hull-penalty
   term is scaled by this factor (the `glass_dls` and clean phases, and a
   plain `optimize` run, keep full strength), so an escape bump can still
   carry the search across the soft hull wall into a different basin while
   the full-weight phases keep the settle-down on real glass. Residuals scale
   by `sqrt(factor)` so `Σ residual² == merit` still holds.
3. **Hull rescue** (`escape.hull_rescue`, default `true`): a converged point
   that fails validation with `glass_hull_violation` is projected back onto
   the hull (the optimizer's `RescueGlassHull` hull **projection**, not a
   catalogue snap) and re-solved with a short clean DLS
   (`hull_rescue_iter_frac` of the full budget, default `0.25`, floor 50
   iterations) and re-validated; a point that passes is recorded as a
   **feasible** minimum instead of an infeasible basin. The cycle event
   stream carries it as `"phase": "hull_rescue"` with
   `status: accepted|rejected`. The rescue is skipped when the run is
   stopping or out of time; a re-solve that fails validation keeps the
   original classification and its escape bump.

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
    max_seconds: ...
    escape_iter_frac: ...
    w_span: ...
    stall_window_frac: ...
    stall_rel_tol: ...
    stall_early_stop: ...
    initial_perturb: ...
  timed_out: false              # true if the search was cut short by max_seconds
  interrupted: false            # true if a SIGINT/SIGTERM stopped the search
  minima:
    - index: 0
      merit: ...
      status: feasible_local_minimum   # always feasible_local_minimum in minima[]
      file: result0.yaml        # --save output file for this minimum (if any)
      features:                  # compact fingerprint of the minimum (per config)
        - id: config1
          element_powers: [0.0075, -0.0041, 0.0022]
      surfaces: [...]
      variables: [...]
  infeasible_basins:            # only present with --keep-infeasible (default: absent)
    - index: 0
      merit: ...
      status: infeasible_basin
      invalid_reason: insufficient_field_throughput
      surfaces: [...]
      variables: [...]
  minima_improvements:          # only present when a minimum was improved (default: absent)
    - index: 0
      merit: ...                # the better merit a repeat visit achieved
      file: result0.yaml
      features: [...]
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

`minima` holds one entry per discovered solution and each `index` is the
entry's position in that list. The list order depends on how the document was
written — a streamed document keeps **discovery order**, a one-shot document is
sorted **by merit** (see [Streaming output](#streaming-output)) — and
`best_index` is always a position in the document's own list, so
`minima[best_index]` — after applying the improvement for that index, if any —
carries `best_merit`. That is exactly what
`escape extract --index $(query -r escape_result.best_index)` relies on.

`minima_improvements` carries the final version of every minimum whose point was
replaced during the run (a repeat visit that reached a better merit,
`Store.Replace`): each entry's `index` is the position in `minima` it supersedes,
and the whole entry (`merit`, `surfaces`, `variables`, `features`, `file`) is the
replacement, not a delta — take `minima[i]` unless an improvement names `i`.
`list escape` applies these substitutions for you (and then ranks the rows by
effective merit); a raw `query` on `escape_result.minima[]` does not, so it shows
the first-discovery values of a streamed document. Its structured output
(`list escape --format yaml`) is the ranking a script should read instead of
the raw section: `minima[]` rows with `index`, `merit`, `status`, `file`,
`element_powers` and `variables`, plus `best_index` / `best_merit` — the way
`samples/escape-demo.bash` renders its minima summary, chart and glass gate.

A concise summary is printed to stderr (never stdout, so the YAML pipeline
stays intact).

## Streaming output

The pipeline document is written to stdout **as the run progresses** instead of
being assembled at the end, so a run killed mid-flight — the third signal,
`kill -9`, an OOM kill, a lost shell — leaves a file holding every minimum found
so far rather than an empty one. Streaming is on by default; `--stream false` or
`optimization.escape.stream: false` restores the previous one-shot write.

The result is still **one YAML document** with no `---` separator, and every
write is a whole block, so the file parses after any write boundary:

```sh
rayweave escape < lens.yaml > out.yaml           # written incrementally
rayweave escape --stream false < lens.yaml > out.yaml   # one-shot, as before
```

### Write sequence

| # | When | Content |
|---|---|---|
| 1 | run start | header: every input key **except** those the run still computes — `configs` always (the best solution lands there), `chief` when a `pupil_model_*` variable rewrites `chief.pupil_model` — plus the literal `escape_result:` with its `params` block (resolved before the search starts) and the `minima:` list opener |
| 2 | each discovered minimum | one `minima` entry, appended under the store lock (infeasible points are never streamed, so they appear only with the completion) |
| 3 | run end | the `escape_result` completion: `best_index`, `best_merit`, `timed_out` / `interrupted` when true, `infeasible_basins` with `--keep-infeasible`, and `minima_improvements` |
| 4 | run end | the deferred top-level keys: `configs` (best solution applied, back-focus solved, apertures sized) and `chief`, when it was deferred |

### Key order

The values are the same as the one-shot document's; only two orders change,
because a YAML key may be defined once and a stream can only be appended to:

- **top level** — the deferred keys come last: `metadata, glass_catalog,
  optimization, chief, escape_result, configs` instead of `metadata,
  glass_catalog, configs, optimization, chief, escape_result`.
- **`escape_result`** — written in write order: `params`, `minima`, then
  `best_index`, `best_merit`, the outcome flags, `infeasible_basins` and
  `minima_improvements`.

Compare two documents by value, not by layout (`yq -o=json … | jq -S` does it),
and never by line numbers.

### `minima` order and `best_index`

A streamed document's `minima` is **discovery-ordered** (the list is
append-only), so each entry's `index` — and `best_index` — is a position in that
list. The one-shot document writes `minima` **sorted by merit**, which makes its
`best_index` the first entry whenever any minimum exists. In both cases
`best_index` addresses the document's own list, so
`escape extract --index <best_index>` yields the best solution either way.

Because the append-only list keeps the first-discovery values, a minimum that a
repeat visit improved appears there with its old merit; the final value lives in
`minima_improvements` at the position it supersedes. `list escape` merges the
two before ranking; a raw `query` on `escape_result.minima[]` does not.

### Reading a document mid-run

`list escape`, `query` and `escape extract` work on a half-finished document —
it already carries `metadata`, `optimization`, `chief`, `escape_result` and
`minima`. Targets that need `configs` (`list surfaces`, `list paraxial`, the
merit tables) and the tracing commands (`trace`, `plot`, `psf`, …) do not, until
the completion writes it. A force-quit run also loses the completion block, so
`best_index` / `best_merit` / `configs` may be absent from a killed document.

`--stream` is echoed back as `optimization.escape.stream` only when the flag is
actually given, so an unset flag never overrides the input YAML.

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

# List the same results from the JSONL run log (auto-detected), which also
# reports when each worker finished and why
rayweave escape --log run.jsonl < samples/escape-demo.yaml > escape-result.yaml
rayweave list escape < run.jsonl
```

## Reading a run log

`--log FILE` and the `--verbose` stream are JSON Lines, and `rayweave list escape`
reads them directly: piping the log back in reproduces the same listing as the
pipeline document (escape parameters, run aggregate, local minima) and adds a
per-worker completion table (state, cycles, escapes, recordings, best merit,
elapsed time, retirement reason). The format detection is automatic, so no flag
is needed:

```sh
rayweave escape --verbose --log run.jsonl < lens.yaml > out.yaml
rayweave list escape < run.jsonl            # table
rayweave list escape --format csv < run.jsonl
rayweave query --jsonl --where 'event=="minimum"' -r merit < run.jsonl   # raw events
```

A run log records what happened, not the design, so the per-minimum **element
powers** are not recoverable from it — use the pipeline document (`out.yaml`) or
the saved `FILE<n>.yaml` for those. Everything else is: the minima's `--save`
file names come from the `minimum_saved` events (no filesystem lookup), the
infeasible basins are always present in the log even when the document hides
them, and the run log also lists the signals, guard actions and errors. The
compact `--verbose` stream drops every field outside the fixed key order
(`min_status`, `invalid_reason`, `retired`, `timed_out`, `interrupted`,
`reason`, `file`, and the `Log Events` details such as `signal` and `message`),
so a full `--log` file is needed to classify the minima, the basins and the
events. See
[list.md §11](list.md#11-escape-section--escapepso-global-search-results).

The `Log Events` section of `list escape` is the quickest way to see why a run
ended — every signal the process received and which stage it stopped at:

```sh
rayweave escape --log run.jsonl < lens.yaml > out.yaml &
sleep 300; kill -TERM %1
rayweave list escape < run.jsonl        # Log Events: interrupt | terminated
```

The sample script `samples/escape-demo.bash` runs the same pipeline end to end:
the default lens is the degraded US2645157 triplet, and `--lens 6elements`
switches to `samples/escape-6elements-init.yaml` (which carries its own
`optimization.escape` section). The 6-element run is slower (37 variables).

## Method

The escape-function mathematics and the two-step (escape-then-clean) cycle are
described in [methods/escape-function.md](methods/escape-function.md).
