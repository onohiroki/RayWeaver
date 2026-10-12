# `rayweave pso` — particle-swarm global optimization

Particle Swarm Optimization as a plain multi-start global search: each cycle a
swarm explores the merit landscape from a perturbed start point, then a
power-preserving glass phase and a clean DLS refine the result — the same
three-phase cycle as [`rayweave escape`](escape.md), with the exploration phase
run by a swarm instead of DLS. **No escape-function bumps are ever placed**: the
swarm's merit is the pipeline's own merit, so `pso` never deforms the
landscape around minima it has already found. Constraint violations are
handled by the fitness penalty described in
[§Constraint handling](#constraint-handling), and the scale the swarm's global
best is ranked on is selected by `gbest`.

```
rayweave pso [--verbose] [--log FILE] [--save FILE] [--glass-dir DIR]
             [--clean-dls BOOL] < input.yaml
rayweave pso extract --index N < pso-output.yaml
```

## Options

| Flag | Description |
|---|---|
| `--glass-dir DIR` | AGF glass catalog directory |
| `--verbose` | print PSO progress to stderr as compact JSONL |
| `--log FILE` | write the full JSONL progress stream to FILE |
| `--save FILE` | save every discovered local minimum to FILE0.yaml, FILE1.yaml, ... |
| `--power-solve` | insert the power-preserving glass phase between each PSO and clean DLS |
| `--power-solve-surfaces A,B,...` | surface IDs for the power-preserving solve |
| `--glass-variables` | auto-generate nd/vd variables for every refractive element |
| `--index N` | (with `pso extract`) local minimum index to extract |
| `--keep-infeasible` | include infeasible basins in stdout YAML (default: discard) |
| `--stream BOOL` | stream the pipeline document to stdout as the run progresses: `true` (default) or `false` for the one-shot write performed after the run |
| `--clean-dls BOOL` | run the clean DLS that closes each cycle: `true` (default) or `false` (see [escape.md §Switching the clean DLS off](escape.md#switching-the-clean-dls-off)) |
| `--swarm-size N` | number of particles per worker (default 30; overrides `optimization.pso.swarm_size`) |
| `--pso-iterations N` | PSO iterations per cycle (default 40; overrides `optimization.pso.pso_iterations`) |
| `--constraint-penalty W` | constraint penalty weight (default 1e3; overrides `optimization.pso.constraint_penalty`) |
| `--gbest SCALE` | scale the global best is ranked on: `merit` (default) or `penalised` (overrides `optimization.pso.gbest`) |

`--glass-dir` is written back into the output's `glass_catalog.directory`.
`--save` records the per-minimum files in `global_result.minima[].file`.
`--swarm-size` / `--pso-iterations` / `--constraint-penalty` / `--gbest` are
PSO-specific flags that override their YAML counterparts. `--stream` is echoed back as
`optimization.pso.stream` only when the flag is actually given, and
`--clean-dls` follows the same rule (see
[escape.md §Switching the clean DLS off](escape.md#switching-the-clean-dls-off))
— each lands in `optimization.pso` only when that flag was given. A `pso` run
keeps its settings in `optimization.pso` throughout; it never writes to
`optimization.escape`, so a document that carries both sections echoes each one
unchanged.

The JSONL progress stream is readable with `rayweave list global`, which detects
it automatically and adds the per-worker completion table:

```sh
rayweave pso --log run.jsonl < lens.yaml > out.yaml
rayweave list global < run.jsonl
```

See [escape.md](escape.md#reading-a-run-log) for what a run log can and cannot
show, and [list.md §11](list.md#11-global-section--escapepso-global-search-results)
for the full output shape.

## Input YAML — `optimization.pso`

```yaml
optimization:
  method: dls
  jacobian_workers: 8
  max_iter: 200
  variables: [...]          # same variable definitions as 'optimize'
  pso:
    # Shared cycle parameters (same as optimization.escape)
    max_cycles: 10
    global_workers: 4       # top-level parallel goroutines (default 4)
    max_seconds: 0          # soft wall-clock budget (seconds, shared; 0 = unlimited)
    distance_threshold: 0.1 # normalised distance to call a point "new"
    fingerprint_distance_threshold: 0  # design-fingerprint distance for "new" (0 = off)
    variable_weights:       # optional per-param weight (default 1.0)
      curvature: 1000
      thickness: 1
      nd: 10
      vd: 1
    initial_perturb: 0.05   # normalised amplitude spreading parallel workers' start points
    min_throughput_ratio: 0.3 # minimum fraction of the prescribed per-field pupil the design must deliver (0.3 default; ~0.9 matches the +-0.03 vignetting checks)
    stall_window_frac: 0.2  # stall-early-stop window (fraction of the phase budget)
    stall_rel_tol: 1e-4     # stalled-early-stop relative merit threshold
    stream: true            # append the pipeline document to stdout as the run progresses
    clean_dls: true         # clean DLS closing each cycle (false = record the PSO result as-is)

    # PSO-specific parameters
    swarm_size: 30            # particles per worker
    pso_iterations: 40        # PSO iterations per cycle
    inertia: 0.9              # starting inertia weight (linear decay: inertia -> 0.4; 0 = 0.9)
    cognitive: 1.494          # personal-best attraction coefficient (c1)
    social: 1.494             # global-best attraction coefficient (c2)
    velocity_clamp: 0.2       # max velocity as fraction of variable range
    init_spread: 0.1          # initial swarm spread as fraction of range
    constraint_penalty: 1000  # penalty weight for constraint violations
    gbest: merit              # scale for the global best: merit | penalised
```

### Shared cycle parameters

Every parameter of `optimization.escape` that is *not* escape-specific is
embedded inline in `optimization.pso`, so `max_cycles`, `global_workers`,
`max_seconds`, `distance_threshold`, `fingerprint_distance_threshold`,
`variable_weights`, `initial_perturb`, `min_throughput_ratio`,
`stall_window_frac`, `stall_rel_tol`, `stall_early_stop`, `resource_guard`,
`stream`, `hull_rescue`, `hull_rescue_iter_frac` and `clean_dls` behave
identically under either section. They act in the **normalized variable space**
(each variable scaled by its min..max range). See
[escape.md](escape.md#input-yaml--optimizationescape) for their full
description.

The escape bump geometry (`h_initial`, `w_initial`, `h_mult`, `w_mult`,
`escape_iter_frac`, `w_span`) belongs to `optimization.escape` only: it is
absent from `optimization.pso`, from `global_result.params` and from the JSONL
`params` event of a PSO run.

### PSO-specific parameters

| Parameter | Default | Meaning |
|---|---|---|
| `swarm_size` | 30 | number of particles per worker |
| `pso_iterations` | 40 | PSO iterations per cycle (the escape-phase `escape_iter_frac` budget does not apply) |
| `inertia` | 0 (0.9) | starting velocity inertia weight; linearly decays from `inertia` (or 0.9 when unset) to 0.4 |
| `cognitive` | 1.494 | personal-best attraction coefficient (c1) |
| `social` | 1.494 | global-best attraction coefficient (c2) |
| `velocity_clamp` | 0.2 | max velocity as fraction of variable range (0 = unclamped) |
| `init_spread` | 0.1 | initial swarm spread as fraction of variable range |
| `constraint_penalty` | 1000 | penalty weight for constraint violations in fitness |
| `gbest` | `merit` | scale the swarm's global best is ranked on: `merit` (plain merit) or `penalised` (`merit + constraint_penalty × Σc²`) |

### Swarm initialisation

Each worker initializes its swarm around the cycle's start point (x0):

- Particle 0: exactly x0
- Particles 1..N-1: x0 + uniform(-spread, spread) × range, clamped to bounds

Velocities are initialized to zero. The swarm is confined to the normalized
variable space [0, 1]^n (each variable scaled by its min..max range).

### Constraint handling

PSO uses a **static penalty** method: the fitness at particle x is

```
f(x) = EvaluateMerit(x) + constraint_penalty × Σ c_j²
```

where `c_j` are the constraint residuals from `ComputeConstraints(x)` (the same
residuals DLS uses in its augmented system). This guides the swarm toward
feasible regions without the complexity of a death penalty or constraint
preservation. The penalty weight is configurable via `constraint_penalty`
(default 1000).

Two scales are therefore available for every particle: the plain `merit` and
the penalised `f`. Personal best is always ranked on `f`, global best on the
scale `gbest` selects:

- `gbest: merit` (**default**) — gbest is ranked on the plain merit, so the
  swarm explores the merit landscape the pipeline reports and the cycle's DLS
  repairs the constraints. A particle is never pushed out of gbest merely
  because its constraint residual is large.
- `gbest: penalised` — gbest is ranked on `f`, so an infeasible particle can
  never become gbest on a lower raw merit. Because `f` squares the residuals
  and scales them by `constraint_penalty` and the constraint weights, a
  strongly infeasible-but-low-merit point and a feasible-but-high-merit point
  can be far apart on this scale; whichever the swarm converges on decides the
  basin the cycle's clean DLS starts from.

The two runs are **not numerically comparable**: record which `gbest` a run
used (`optimization.pso.gbest` is echoed in the output document, and the
`pso_init` JSONL event reports it as `gbest_criterion`).

### PSO algorithm (global-best)

Each cycle's PSO phase runs a standard global-best PSO in normalized space:

1. Initialize particles and velocities.
2. For each iteration:
   a. Update the dynamic pupil at the current global best (`UpdatePupils`).
   b. Evaluate each particle's merit and fitness `f`.
   c. Update personal bests on `f`, and the global best on the `gbest` scale.
   d. Update velocities: `v = w·v + c1·r1·(pbest−x) + c2·r2·(gbest−x)`
   e. Update positions: `x = x + v`, clamped to [0, 1].
3. Return the global best as the exploration result of the cycle. The phase
   result carries the **plain** merit of the start point (`before_merit`) and
   of the returned particle (`after_merit`), so a cycle event, a recorded
   minimum and a DLS phase result are all on one scale; the penalised fitness
   the swarm ranked by is reported in the `pso_iter` events as `gbest_merit`
   next to the plain `gbest_true`.

The inertia weight `w` linearly decays from 0.9 to 0.4 over the iteration
budget. The velocity is clamped to `|v_j| ≤ velocity_clamp × range_j`.
The algorithm terminates early if the global best has not improved by
`stall_rel_tol` over a `stall_window_frac` window (status "converged"),
or when `pso_iterations` is exhausted (status "max_iterations").

### Dynamic pupil

The dynamic pupil grid (entrance-pupil Z) is recomputed at the global-best
position at the start of each PSO iteration, matching the DLS convention
of one pupil update per iteration. This keeps the merit landscape consistent
with the moving lens.

## Output

The output reuses `global_result` for full pipeline compatibility. The best
solution is written to `configs[].surfaces` and all discovered minima are
listed in `global_result.minima[]`. See [escape.md#output](escape.md#output)
for the full schema.

As with `escape`, the document is appended to stdout while the run progresses
(`--stream false` restores the one-shot write), so a killed run still leaves
every minimum discovered so far; the write sequence, the key order it implies
and `global_result.minima_improvements` are described in
[escape.md#streaming-output](escape.md#streaming-output).

`pso extract --index N` works identically to `escape extract --index N`.

## Examples

```sh
# Run PSO global optimization and draw the best solution
rayweave pso < samples/escape-demo.yaml | tee pso-result.yaml \
  | rayweave trace | rayweave plot -o best.svg

# Save every discovered minimum
rayweave pso --save result < samples/escape-demo.yaml > pso-result.yaml

# Extract a specific local minimum
rayweave pso extract --index 1 < pso-result.yaml > min1.yaml

# PSO with custom parameters
rayweave pso --swarm-size 50 --pso-iterations 60 < input.yaml > output.yaml
```

## Method

The PSO algorithm is described in [methods/pso.md](methods/pso.md). The
three-phase cycle (PSO exploration, glass optimization, clean DLS) is the same
structure used by [escape](escape.md) and
[methods/escape-function.md](methods/escape-function.md), with the DLS
exploration phase replaced by PSO.
