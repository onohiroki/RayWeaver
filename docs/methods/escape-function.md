# Escape-function global optimization

DLS (see [dls-optimization.md](dls-optimization.md)) converges to a *local*
minimum. The escape-function method (Ishiki–Ono style) turns a sequence of DLS
runs into a *global* search: after each convergence a smooth **bump** is added
to the merit landscape around the found minimum so the next run is pushed out
of that valley and can discover other local minima.

## 1. The escape bump

For each recorded minimum `p` with height `H_p` and width `W_p`, the merit is
augmented with

```
E(x) = Σ_p  H_p · exp( −d²(x, p) / W_p² )
```

where `d(x, p)` is the **normalized mean-squared distance** between `x` and the
minimum `p`. The bump is Gaussian: large near the minimum, negligible far from
it, so it deforms the landscape only locally and leaves distant regions — and
their local minima — essentially unchanged.

### Normalized distance

The distance is measured in the normalized variable space (each variable
scaled by its `min..max` range):

```
d² = (1/N) Σ_active  w_j · ( (x_j − p_j) / scale_j )²
```

- Variables with `min == max` (fixed) are **excluded** from the distance.
- Per-variable weights `w_j` (default 1) come from `variable_weights`, keyed by
  parameter name (`curvature`, `thickness`, `nd`, `vd`, …). They normalize the
  physical scale differences between variable types.

### Residuals

The wrapper exposes the escape contribution as residuals:

```
r_escape = √H_p · exp( −d² / (2 W²) )
```

so that `r² = H_p·exp(−d²/W²)`, i.e. the square of the residual is exactly the
escape term. The DLS finite-difference Jacobian therefore captures the escape
gradient naturally — no special-casing needed in the solver.

## 2. The two-step cycle

Each worker repeats, up to `max_cycles` times:

1. **Escape DLS** — run DLS with all recorded minima's bumps active, starting
   from a nudged state. The bumps push the solution away from known minima.
2. **Clean DLS** — run DLS with the bumps cleared, starting from the escaped
   point, to converge to the *true* local minimum of the unperturbed merit.

The true minimum is then classified:

- **New** (normalized distance to every known minimum > `distance_threshold`):
  record it as a new `Point{X, Merit, H, W}`.
- **Repeat** of a known minimum: **strengthen** the nearest bump (`H ×= h_mult`,
  `W ×= w_mult`) and double the restart offset, so the next escape run starts on
  a steeper part of the (now taller/wider) bump. If the repeat arrives with a
  **better** merit, the stored `X`/`Merit` of that minimum are replaced (keeping
  the strengthened `H`/`W`), so the search always keeps the better data — the
  version counter (`Store.Replace`, exposed to `--save` file versioning)
  records how many times a minimum has been improved.

The starting point for the next cycle is a deterministic perturbation of the
last true minimum (`restartPerturb = 0.1` of the normalized range). Starting
exactly on a recorded minimum would give a zero escape gradient (the bump is
flat at its own centre), so the nudge is necessary.

## 3. Parallel workers

`escape_workers` (default 4) goroutines each run their own cycle, sharing a
single store of recorded minima. Each worker's initial state is perturbed by a
deterministic, seed-derived offset (`initialPerturb = 0.05` default) so the
workers explore distinct neighbourhoods. Each worker builds an **isolated model**
(a fresh optimizer) so concurrent merit evaluations never share mutable state.
The escape-bump width is additionally a per-worker function of the index
(`W × (1 + i/(N-1) × (w_span-1))`, default `w_span = 2.0`), widening the spread
so workers drift toward different basins.

Because each DLS run inside a cycle parallelizes its own Jacobian across
`jacobian_workers`, the total goroutine count is `escape_workers ×
jacobian_workers`; set `jacobian_workers: 1` when running many escape workers.

The escape cycle disables the DLS internal stall perturbation
(`DisableStallEscape`), which would otherwise fight the escape mechanism.

### Stalled-early-stop (phase-aware)

Escape-phase DLS solves have a capped iteration budget: `escape_iter_frac`
(default 1/3) of the full budget, floored at 50 iterations. With
`stall_early_stop` (default true), a solve whose best merit has not improved by
at least `stall_rel_tol` (default `1e-4`, relative) over a `stall_window_frac`
(default 0.2) window returns the best point found and the cycle moves on. The
clean re-optimisation phase always runs the full budget and never stalls, so a
slow, late-stage merit improvement (a real source of the global best) is never
cut off.

## 4. Parameters

| Parameter | Default | Meaning |
|---|---|---|
| `escape_workers` | 4 | parallel top-level goroutines |
| `max_cycles` | 10 | DLS cycles per worker |
| `max_seconds` | 0 | soft wall-clock budget in seconds, shared by all workers (0 = unlimited) |
| `distance_threshold` | 0.1 | normalized distance to call a point "new" |
| `h_initial` | 0.1 | initial bump height (escape strength) |
| `w_initial` | 0.5 | initial bump width (locality) |
| `h_mult` | 2.0 | strengthen factor on a repeated minimum |
| `w_mult` | 1.3 | widen factor on a repeated minimum |
| `variable_weights` | 1.0 each | per-parameter distance weights |
| `escape_iter_frac` | 1/3 | escape-phase MaxIter as a fraction of the full budget (min 50) |
| `w_span` | 2.0 | per-worker W scaling span |
| `stall_window_frac` | 0.2 | stalled-early-stop window as a fraction of escape MaxIter |
| `stall_rel_tol` | `1e-4` | stalled-early-stop relative merit threshold |
| `stall_early_stop` | true | stalled-early-stop in the escape phase (clean phase never stalls) |
| `initial_perturb` | 0.05 | normalised spread of parallel-worker start points |

## 4b. Termination

The search normally runs `escape_workers` workers for `max_cycles` cycles each.
A worker also stops early after `maxFail = 3` consecutive unacceptable DLS runs
(a solve that neither converges nor produces a finite, sane merit).

`max_seconds` adds a **soft** wall-clock budget shared by all workers: each
worker checks the shared deadline at the start of every cycle and stops as soon
as it is exceeded. The check only happens *between* DLS runs — a running solve
always completes, so the overshoot is bounded by one DLS run. This is a
coarse-grained stop: it never interrupts an in-flight DLS iteration. Minima
recorded before expiry are still reported, and the output sets `timed_out: true`.

A `SIGINT`/`SIGTERM` behaves the same way: the first signal cancels the shared
context, workers stop at the next cycle boundary (the running DLS solve
completes), everything is saved, and the output sets `interrupted: true` with
exit code 0; a second signal force-quits with exit 1. No DLS-level interruption
is attempted.

## 4c. Saving minima

`escape --save FILE` writes each recorded minimum to a versioned YAML file as it
is discovered (see `docs/escape.md`): `FILE1.yaml`, `FILE2.yaml`, … in discovery
order, with `FILE N.<version>.yaml` archives for superseded improvements. The
saver is wired through `Store.SetOnRecord` (invoked under the store lock, so
invocations from parallel workers are serialised), materialises each minimum via
`applyEscapeX`/`applyEscapeMulti` (which only read the glass catalog — race-free
against the worker models), and writes atomically (temp + fsync + rename) so a
hard kill never loses already-found minima.

## 5. Output

The best minimum (lowest true merit) is written to the top-level configs,
pipeline-compatible with `rayweave trace`/`plot`. Every discovered minimum is
listed in `escape_result.minima[]` with its full surfaces and variable values,
so `escape extract --index N` can recover any of them as a clean lens YAML.
Each minimum also carries `features` — a compact fingerprint for comparing
minima against each other — with one entry per config (`id`) holding
`element_powers`: the thin-lens power of every lens element at the d-line, in
system order (the sum of the surface powers bounding each element, which for a
refractive element in air equals `(n-1)(c1-c2)`; mirrors are single-surface
elements with power `-2n/R`). `merit` stays at the minimum level as the
objective scalar.

## Relationship to DLS

- DLS provides the local-search engine; escape provides the global exploration.
- The escape bump is defined in normalized space and interacts with the same
  variable bounds; fixed variables are excluded from distance computations.
- Escape adds one residual per recorded minimum to the DLS model; the solver
  treats it exactly like any other residual.
