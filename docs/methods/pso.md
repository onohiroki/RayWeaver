# PSO escape-function global optimization

## Overview

The PSO (Particle Swarm Optimization) method replaces the DLS escape phase of
the [escape-function](escape-function.md) global optimization with a
gradient-free population-based search. Each cycle runs three phases:

1. **PSO exploration** — a PSO swarm minimizes the escape-augmented merit,
   pushing the search away from known local minima without requiring gradients.
2. **Glass optimization** (optional) — locks non-glass variables and rebalances
   dispersions while holding element powers fixed (identical to the escape
   command's glass phase).
3. **Clean DLS** — converges to the true local minimum with the full merit
   budget (identical to the escape command's clean phase).

The PSO phase is a drop-in replacement for the DLS escape phase: it uses the
same `escape.Wrapper` with the same Gaussian bumps, and the same `Store`,
validation, and output infrastructure. Only the exploration algorithm differs.

## 1. The escape-function bump

The escape bump is identical to the DLS version (see
[escape-function.md](escape-function.md#1-the-escape-bump)):

```
E(x) = Σ_p  H_p · exp( −d²(x, p) / W_p² )
```

The wrapper's `EvaluateMerit(x)` adds this term to the inner merit, so PSO
naturally minimizes `merit + E(x)` without any special handling.

## 2. The three-phase cycle

Each worker repeats, up to `max_cycles` times:

1. **PSO exploration** — a swarm of `swarm_size` particles searches for a
   point where the escape-augmented merit is low. The swarm is initialized
   around the current start point and evolves for `pso_iterations` iterations.
   The best particle position becomes the escaped point.
2. **Power-preserving glass phase** (optional) — locks every variable except
   glass dispersions and rebalances glasses while holding element powers fixed.
   Identical to the escape command's glass phase.
3. **Clean DLS** — runs the full DLS budget with escapes cleared, converging
   to the true local minimum. This minimum is then validated, recorded in the
   store, and used as the start point for the next cycle.

The cycle structure, validation, store management, and output are identical
to the escape function — only step 1 differs.

### Post-cycle logic

After the clean DLS converges, the cycle applies the same logic as escape:

- **New minimum** (normalized distance > distance_threshold from every known
  minimum): record as a new Point with its own escape bump.
- **Repeat**: strengthen the nearest bump (H ×= h_mult, W ×= w_mult) and
  grow the restart offset.
- **Improved repeat**: replace the stored X/Merit with the better data.
- **Infeasible basin**: recorded separately, receives escape bump, not
  listed as a solution.

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
f(x) = merit + E(x) + μ · Σ c_j²
```

where `c_j` are the constraint residuals from `ComputeConstraints(x)` (the same
residuals DLS uses in its augmented system) and `μ` is the penalty weight
(default 1000). This is symmetric: constraints penalized equally in both
violation directions (matching DLS's `sumConstraintTerms`).

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

## 4. Comparison with DLS escape

| Aspect | DLS escape | PSO escape |
|---|---|---|
| Gradient | Finite-difference Jacobian | None (function-only) |
| Per-cycle cost | Low (50–200 DLS iterations) | Higher (30 particles × 40 iterations) |
| Exploration | Local push via escape gradient | Global swarm search |
| Constraint handling | Augmented system (active-set) | Static penalty |
| Parallelism | Per-worker (Jacobians parallel) | Per-worker (particles sequential) |

PSO is more expensive per cycle but can discover minima that DLS misses when
the escape gradient is weak or the landscape is highly multimodal.

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

All escape-function parameters (max_cycles, escape_workers, max_seconds,
distance_threshold, h_initial, w_initial, h_mult, w_mult, variable_weights,
initial_perturb, fingerprint_distance_threshold, min_throughput_ratio) are
identical to the escape command.

## Relationship to DLS

- DLS provides the local-search engine for the clean phase; PSO provides the
  global exploration for the escape phase.
- The escape bump is defined in normalized space and interacts with the same
  variable bounds; fixed variables are excluded from both distance computations
  and PSO optimization.
- PSO adds no residuals to the DLS model — it only uses `EvaluateMerit` and
  `ComputeConstraints`.
- The clean DLS phase receives the PSO result as its start point, exactly as
  it would from a DLS escape phase.
