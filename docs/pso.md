# `rayweave pso` — PSO escape-function global optimization

Particle Swarm Optimization (PSO) combined with the escape-function method.
Each cycle runs a PSO swarm that minimizes the escape-augmented merit, followed
by a power-preserving glass phase and a clean DLS refinement — identical to
`rayweave escape` except the first phase uses PSO instead of DLS.

```
rayweave pso [--verbose] [--log FILE] [--save FILE] [--glass-dir DIR] < input.yaml
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
| `--swarm-size N` | number of particles per worker (default 30; overrides `optimization.pso.swarm_size`) |
| `--pso-iterations N` | PSO iterations per cycle (default 40; overrides `optimization.pso.pso_iterations`) |
| `--constraint-penalty W` | constraint penalty weight (default 1e3; overrides `optimization.pso.constraint_penalty`) |

`--glass-dir` is written back into the output's `glass_catalog.directory`.
`--save` records the per-minimum files in `escape_result.minima[].file`.
`--swarm-size` / `--pso-iterations` / `--constraint-penalty` are PSO-specific
flags that override their YAML counterparts.

## Input YAML — `optimization.pso`

```yaml
optimization:
  method: dls
  jacobian_workers: 8
  max_iter: 200
  variables: [...]          # same variable definitions as 'optimize'
  pso:
    # Escape-function parameters (same as optimization.escape)
    max_cycles: 10
    escape_workers: 4       # top-level parallel goroutines (default 4)
    max_seconds: 0          # soft wall-clock budget (seconds, shared; 0 = unlimited)
    distance_threshold: 0.1
    fingerprint_distance_threshold: 0
    h_initial: 0.1
    w_initial: 0.5
    h_mult: 2.0
    w_mult: 1.3
    variable_weights:
      curvature: 1000
      thickness: 1
      nd: 10
      vd: 1
    initial_perturb: 0.05
    min_throughput_ratio: 0.3

    # PSO-specific parameters
    swarm_size: 30            # particles per worker
    pso_iterations: 40        # PSO iterations per cycle
    inertia: 0.729             # velocity inertia weight (linear decay: 0.9 -> 0.4)
    cognitive: 1.494           # personal-best attraction coefficient (c1)
    social: 1.494              # global-best attraction coefficient (c2)
    velocity_clamp: 0.2        # max velocity as fraction of variable range
    init_spread: 0.1           # initial swarm spread as fraction of range
    constraint_penalty: 1000   # penalty weight for constraint violations
    stall_window_frac: 0.2     # stall-early-stop window (fraction of max_iter)
    stall_rel_tol: 1e-4        # relative improvement threshold for stall
```

### Escape parameters

All escape-function parameters (max_cycles, distance_threshold, h_initial,
w_initial, h_mult, w_mult, variable_weights, fingerprint_distance_threshold,
initial_perturb, min_throughput_ratio) are shared with `optimization.escape`
and embedded inline in `optimization.pso`. These parameters act in the
**normalized variable space** (each variable scaled by its min..max range).
See [escape.md](escape.md#input-yaml--optimizationescape) for their full
description.

### PSO-specific parameters

| Parameter | Default | Meaning |
|---|---|---|
| `swarm_size` | 30 | number of particles per escape worker |
| `pso_iterations` | 40 | PSO iterations per cycle (replaces DLS escape-phase budget) |
| `inertia` | 0.729 | velocity inertia weight; linearly decays from 0.9 to 0.4 |
| `cognitive` | 1.494 | personal-best attraction coefficient (c1) |
| `social` | 1.494 | global-best attraction coefficient (c2) |
| `velocity_clamp` | 0.2 | max velocity as fraction of variable range (0 = unclamped) |
| `init_spread` | 0.1 | initial swarm spread as fraction of variable range |
| `constraint_penalty` | 1000 | penalty weight for constraint violations in fitness |
| `stall_window_frac` | 0.2 | fraction of pso_iterations for stall detection |
| `stall_rel_tol` | 1e-4 | relative improvement threshold for stall detection |

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

### PSO algorithm (global-best)

Each cycle's PSO phase runs a standard global-best PSO in normalized space:

1. Initialize particles and velocities.
2. For each iteration:
   a. Update the dynamic pupil at the current global best (`UpdatePupils`).
   b. Evaluate each particle's fitness.
   c. Update personal bests and global best.
   d. Update velocities: `v = w·v + c1·r1·(pbest−x) + c2·r2·(gbest−x)`
   e. Update positions: `x = x + v`, clamped to [0, 1].
3. Return the global best as the escaped point.

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

The output reuses `escape_result` for full pipeline compatibility. The best
solution is written to `configs[].surfaces` and all discovered minima are
listed in `escape_result.minima[]`. See [escape.md#output](escape.md#output)
for the full schema.

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

The escape-function mathematics and the PSO algorithm are described in
[methods/pso.md](methods/pso.md). The three-phase cycle (PSO exploration,
glass optimization, clean DLS) is the same structure used by
[escape](escape.md), with the DLS escape phase replaced by PSO.
