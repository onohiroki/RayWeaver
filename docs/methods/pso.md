# PSO global optimization

## Overview

PSO (Particle Swarm Optimization) is one of RayWeaver's two global methods; the
other is the [escape-function](escape-function.md) search. Each cycle runs
three phases:

1. **PSO exploration** — a swarm minimizes the **plain** merit (plus the
   constraint penalty) by a gradient-free population-based search.
2. **Glass optimization** (optional) — locks non-glass variables and rebalances
   dispersions while holding element powers fixed (identical to the escape
   command's glass phase).
3. **Clean DLS** — converges to the true local minimum with the full merit
   budget (identical to the escape command's clean phase).

The cycle, the `Store`, the validation and the output infrastructure are the
same ones `escape` uses — only the exploration engine differs, and there PSO is
a *plain* multi-start search: **the escape function is never applied**.

## 1. No merit deformation

An `escape` run adds a Gaussian bump on every recorded minimum
(`E(x) = Σ_p H_p · exp(−d²(x, p) / W_p²)`, see
[escape-function.md](escape-function.md#1-the-escape-bump)) so that the next
DLS run is pushed out of the valley it just found.

A PSO run installs **no** bumps: the cycle asks for the escape set and gets an
empty one whenever a swarm is doing the exploration
(`Cycle.escapesFor()`, `internal/escape/cycle.go`), so the swarm — and the
clean DLS after it — always see the undeformed merit. The `Store` is still
fully used for *classification*: distance/fingerprint deduplication,
new/repeat/improved bookkeeping and "keep the better data". The `H`/`W` fields
of a stored point are therefore maintained but never read.

This is why `optimization.pso` has no `h_initial`, `w_initial`, `h_mult`,
`w_mult`, `escape_iter_frac` or `w_span`, and why a PSO run's
`global_result.params` and JSONL `params` event never mention them.

## 2. The three-phase cycle

Each worker repeats, up to `max_cycles` times:

1. **PSO exploration** — a swarm of `swarm_size` particles searches for a
   point where the merit is low. The swarm is initialized around the current
   start point and evolves for `pso_iterations` iterations. The best particle
   position becomes the exploration result of the cycle.
2. **Power-preserving glass phase** (optional) — locks every variable except
   glass dispersions and rebalances glasses while holding element powers fixed.
   Identical to the escape command's glass phase.
3. **Clean DLS** — runs the full DLS budget on the undeformed merit,
   converging to the true local minimum. This minimum is then validated,
   recorded in the store, and used as the start point for the next cycle.

The cycle structure, validation, store management, and output are identical
to the escape function — only step 1 differs, and there the escape function is
simply never applied: the swarm minimises the plain merit, so no bump is ever
placed on a minimum a PSO run has found. Step 3 stays switchable exactly as it
is for `escape`: `clean_dls: false` records the swarm's best position (or the
glass phase's output) as-is instead of DLS-refining it, and skips the
post-glass retry and the hull-rescue re-solve with it. See
[escape-function.md §2](escape-function.md#2-the-two-step-cycle).

### Post-cycle logic

After the clean DLS converges, the cycle applies the same logic as escape:

- **New minimum** (normalized distance > distance_threshold from every known
  minimum): record as a new Point in the store.
- **Repeat**: mark the nearest stored point (its H/W are still strengthened for
  the record, though no bump is drawn from them) and grow the restart offset.
- **Improved repeat**: replace the stored X/Merit with the better data.
- **Infeasible basin**: recorded separately, not listed as a solution.

## 3. PSO algorithm (global-best)

The PSO operates in **normalized variable space** [0, 1]^n (each variable
scaled by its min..max range). Fixed variables (min == max) are excluded.

### Velocity and position update

For particle i at position x_i with velocity v_i and personal best p_i:

```
v_i = w · v_i + c1 · r1 · (p_i − x_i) + c2 · r2 · (g − x_i)
x_i = x_i + v_i
```

where:

- `w` is the inertia weight (linearly decays from the configured starting value `inertia`, default 0.9, down to 0.4 over iterations)
- `c1` is the cognitive coefficient (personal-best attraction, default 1.494)
- `c2` is the social coefficient (global-best attraction, default 1.494)
- `r1, r2` are independent uniform random numbers in [0, 1]
- `g` is the global best position across the entire swarm

### Velocity clamping

Each velocity component is clamped:

```
|v_j| ≤ velocity_clamp × range_j
```

where `range_j = Max_j − Min_j`. This prevents particles from overshooting
the variable bounds.

### Swarm initialization

Each worker initializes its swarm around the cycle's start point (x0):

- Particle 0: x0 (the current start point)
- Particles 1..N-1: x0 + U(−init_spread, +init_spread) × range, clamped to bounds

Velocities are initialized to zero. The start point is itself a perturbation
of the last converged minimum (matching the escape function's restartPerturb).

### Constraint handling

PSO uses a **static penalty** method:

```
f(x) = merit + μ · Σ c_j²
```

where `c_j` are the constraint residuals from `ComputeConstraints(x)` (the same
residuals DLS uses in its augmented system) and `μ` is the penalty weight
(default 1000). This is symmetric: constraints penalized equally in both
violation directions (matching DLS's `sumConstraintTerms`).

`f` is the swarm's **single** fitness value: personal best and global best are
both selected on it, so an infeasible particle never becomes gbest merely
because its raw merit is lower. (Earlier releases selected gbest on the raw
merit while pbest used the penalized one; a PSO run is therefore not
numerically comparable with one of those older runs whenever constraints are
active.)

### Dynamic pupil

At the start of each PSO iteration, the dynamic pupil is recomputed at the
current global-best position (`UpdatePupils(gbest)`). This matches the DLS
convention of one pupil update per iteration, keeping the merit landscape
consistent as the lens changes.

### Stall detection

The PSO terminates early if the global best has not improved by at least
`stall_rel_tol` (default 1e-4, relative) over a window of
`stall_window_frac × pso_iterations` iterations. This prevents wasted effort
when the swarm has converged.

### Early termination

The PSO respects the same stop mechanisms as the DLS escape phase:

- **Context cancellation** (first SIGINT/SIGTERM): the PSO stops at the next
  cycle boundary, returning the best particle found so far.
- **Hard stop** (second SIGINT/SIGTERM): the PSO returns the best particle
  found so far with status "interrupted".
- **Time budget** (max_seconds): checked at cycle boundaries, not within the
  PSO.

## 4. Comparison with the escape method

| Aspect | `escape` (DLS + bumps) | `pso` (swarm) |
|---|---|---|
| Gradient | Finite-difference Jacobian | None (function-only) |
| Per-cycle cost | Low (50–200 DLS iterations) | Higher (30 particles × 40 iterations) |
| Exploration | Local push via escape gradient | Global swarm search |
| Constraint handling | Augmented system (active-set) | Static penalty |
| Parallelism | Per-worker (Jacobians parallel) | Per-worker (particles sequential) |

PSO is more expensive per cycle but searches globally instead of following the
escape gradient, so it can find minima the escape method misses when that
gradient is weak or the landscape is highly multimodal — and it does so without
ever deforming the merit it reports against.

## 5. Parameters

| Parameter | Default | Meaning |
|---|---|---|
| `swarm_size` | 30 | particles per worker |
| `pso_iterations` | 40 | PSO iterations per cycle |
| `inertia` | 0 (0.9) | starting velocity inertia (linear decay `inertia`→0.4) |
| `cognitive` | 1.494 | personal-best coefficient (c1) |
| `social` | 1.494 | global-best coefficient (c2) |
| `velocity_clamp` | 0.2 | max velocity as fraction of range |
| `init_spread` | 0.1 | initial swarm spread as fraction of range |
| `constraint_penalty` | 1000 | constraint penalty weight |
| `stall_window_frac` | 0.2 | stall detection window fraction |
| `stall_rel_tol` | 1e-4 | stall relative improvement threshold |

All shared cycle parameters (max_cycles, global_workers, max_seconds,
distance_threshold, variable_weights, initial_perturb,
fingerprint_distance_threshold, min_throughput_ratio, stall_*,
hull_rescue*, clean_dls) are identical to the escape command. The escape bump
geometry (h_initial, w_initial, h_mult, w_mult, escape_iter_frac, w_span)
belongs to `optimization.escape` alone and does not exist for PSO.

## Relationship to DLS

- DLS provides the local-search engine for the clean phase; PSO provides the
  global exploration.
- The distance / fingerprint deduplication is defined in normalized space and
  interacts with the same variable bounds; fixed variables are excluded from
  both the distance computation and the PSO optimization.
- PSO adds no residuals to the DLS model — it only uses `EvaluateMerit` and
  `ComputeConstraints`.
- The clean DLS phase receives the PSO result as its start point, exactly as
  it would from an escape phase.
