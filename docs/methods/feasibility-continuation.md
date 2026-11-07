# Feasibility continuation

**Status: in progress.** The design is fixed; Layer D is partially implemented
(local-frame extents over partial ray paths). The remaining layers are design
only. This document owns the design of a total, differentiable merit function
and the continuation strategy that carries a lens from "rays do not reach the
image plane" to a feasible, optimisable design. It
is the reference for the `feat/feasibility-continuation` work; command syntax
stays in the individual manuals and the merit catalog stays in
[merit-and-constraints.md](../merit-and-constraints.md).

## 1. The problem: a merit that is only a partial function

The merit function is evaluated on the rays that reach the image plane. A ray
that misses a surface, total-internally reflects, or is clipped by an aperture
simply disappears from the sum. When every ray of every field disappears, the
merit is not "large" — it is **undefined**, and the implementation fills the
hole with a constant:

- `optimization.degenerate` replaces a failed spot/OPD/wavefront evaluation by
  a bounded constant (`spot_value`, `opd_value`, `wavefront_value`), see
  [merit-and-constraints.md §6](../merit-and-constraints.md#6-degenerate-penalties);
- `field_alive` saturates at its threshold and `pupil_fill` is capped at 999, so
  a fully dead field contributes the same value at every design point;
- a `beam_clearance` constraint whose surface is reached by no ray reports
  `diameter/2` (the map lookup returns zero), i.e. **maximum clearance**
  (`internal/constraint/constraint.go`, `evaluateBeamClearance`).

A constant has zero gradient. DLS therefore stalls, an escape bump has no slope
to climb, and a PSO swarm sees identical fitness for every particle (the global
best becomes noise). Population size does not fix this: a zoom that must satisfy
EFL, focus and pupil survival at several positions simultaneously has a feasible
set that is a thin, often disconnected subset of a 20–40-dimensional box, and
the probability that a random point lands within a tolerance ε of it falls like
ε^m for m simultaneous conditions.

The problem is therefore not "the search is too weak" but **"the objective is a
partial function"**. Feasibility continuation removes the hole instead of
searching around it.

## 2. Principles

### 2.1 Total merit

Replace every binary failure predicate by a **signed distance to its boundary**.
The resulting merit is defined wherever the geometry is defined, and is
continuous and differentiable almost everywhere. A failed ray is not dropped;
it is extended, penalised, and kept in the derivative.

"No ray reached this surface" must never read as "this surface is safe". An
absent measurement is a violation, not a large margin.

### 2.2 Three continuations

| Continuation | From | To | Layer |
|---|---|---|---|
| value | survive / die | signed distance | D |
| path | forward march | solved ray path | S |
| model | paraxial / Seidel | real ray trace | R |

They are independent and can be introduced in that order. The last one is the
global strategy; the first two are what make the last one effective.

## 3. Residual vocabulary (Layer D)

A residual is a **pure function of the surface and the ray state** (or ray
segment). It carries no optimizer state and no field bookkeeping, so the forward
tracer, the constraint evaluator and the nested solver can share it.

| Failure mode | Signed residual | Sign |
|---|---|---|
| aperture clip | `r_a = max(0, h − R + m0)` | `h` = radial height in the surface-local frame; `R` = clear-aperture radius; `m0` = required clearance |
| missed surface | `r_i = max(0, −Δ)` (or the closest-approach distance) | `Δ` = intersection discriminant of the ray against the surface |
| total internal reflection | `r_t = max(0, sin θ_c − sin θ)` | `θ_c` = critical angle from the two media; `θ` = incidence angle on the surface normal |
| glass path | `r_g = max(0, floor − edge)` | the existing `edge_thickness` measure, with `floor = min_glass_path` |
| stop clip | `r_s = max(0, h_stop − R_stop)` | constrain at zero, **not** at a positive clearance: the stop defines the pupil and its margin is ~0 by construction |

Two rules make the residuals usable:

1. **Local frame.** The aperture height is `sqrt(x² + y²)` in the surface-local
   frame (`types.Surface.GlobalToLocal`), because that is the frame the tracer's
   check uses (`internal/ray/trace.go`: `h > Diameter/2`). A global
   `max(|X|, |Y|)` is a bounding-box measure that overstates the margin on
   off-axis, tilted or decentered surfaces.
2. **No saturating physical quantity.** Transmitted intensity and surviving-ray
   counts saturate (Fresnel reflectance is exactly 1 beyond the critical angle;
   a survival ratio is a step function), so their derivative is zero exactly in
   the region that needs guidance. Use the distance to the boundary, not the
   probability of crossing it.

The residuals are dimensionally lengths (mm). They join the least-squares system
through the same residual scaling as the other terms
([merit-and-constraints.md §4](../merit-and-constraints.md#4-assembly-weighting-and-normalization)),
so `Σ r² == M` continues to hold.

## 4. Architecture

```
Layer D  residual vocabulary      pure signed distances
Layer P  paraxial initializer     marginal heights/angles per surface
Layer S  nested ray solve         path = solution of surface + Snell equations
Layer R  outer continuation       paraxial merit → real-ray merit
Layer X  simultaneous solve       surface + ray nodes in one system (optional)
```

### 4.1 Layer D — residual layer

A new package (proposed: `internal/feasibility`) owns the residuals. It imports
`types`, `surface`, `ray` and `raymath` only; `dls`, `constraint` and `optimize`
import it. It must not import `optimize` (cycle) and must be deterministic.

The layer also defines the **aggregation** used by the feasibility stage:

```
F(x) = Σ_f Σ_s r_{f,s}(x)²
```

over the reached prefix of each ray, with the "absence is a violation" rule of
§2.1. The bottleneck is `argmax r`, which is the diagnostic the run reports.

### 4.2 Layer P — paraxial initializer

The paraxial trace (`internal/paraxial`) always produces marginal heights and
angles per surface for a non-degenerate geometry. It is the initial node path
for Layer S and the fallback merit when a real-ray evaluation fails. It is also
the natural global surrogate for Layer R: the paraxial marginal height is a
smooth, total approximation of the real beam envelope.

### 4.3 Layer S — nested robust ray solve

For fixed surfaces, one ray's path is the solution of

```
node_i ∈ surface_i                    (surface equation)
direction after i = Snell(direction before i, normal_i)   (refraction)
launch node fixed by the pupil        (boundary condition)
```

solved simultaneously for all nodes by a damped Newton / Levenberg–Marquardt
iteration, instead of the current sequential march. Two consequences:

- There is no "missed surface" event: the node is a variable, and the surface
  equation is a residual. A miss becomes a non-zero residual, with a gradient.
- The least-squares residual norm at the solution is a natural penalty for the
  outer optimizer, so the merit stays total even when the inner solve does not
  converge to a physical path.

Implementation rules:

- **Branch tracking.** Start each node from the previous surface's solution and
  from the paraxial path; never seed a node from an arbitrary point. This keeps
  spheres on the near root and prevents a fold from jumping branches.
- **Warm start.** The DLS Jacobian evaluates `2n + 1` perturbed points. Reuse
  the base point's ray path as the initial guess for every column, so each inner
  solve converges in one or two iterations. This is the difference between
  "affordable" and "2n + 1 full Newton solves".
- **Determinism.** Fixed iteration cap, fixed tolerance, fixed node order (the
  ray `path`, not surface IDs), no shared mutable state. Bit-identical results
  for any worker count, as required by the repository contract.
- **Physical gate.** The inner residual is a penalty, not a replacement for the
  real trace. The reported merit and every acceptance gate (`zoom` vignetting,
  `chief`, `psf`) still evaluate the physical ray trace.

### 4.4 Layer R — outer continuation

```
M_λ(x) = (1 − λ) · M_analytic(x) + λ · M_realtrace(x)
```

where `M_analytic` is the paraxial/Seidel merit (`focal_length`, `seidel_*`,
`distortion_pct`) and λ is ramped from 0 to 1. The ramp is driven by a new
schedule metric, `ray_validity`, derived from the Layer D residuals (e.g. the
fraction of the prescribed pupil whose worst residual is below the clearance).
If a ramp step loses validity, step λ back one stage and restore feasibility.

The existing `optimization.merit_schedule` already ramps named merit modes by
`merit_ratio`, `iteration`, `glass_role`, `spot_diffraction` or `phase`
([optimize.md](../optimize.md#conditional-merit-schedule-optimizationmerit_schedule));
`ray_validity` is the missing metric, not a new scheduling mechanism.

### 4.5 Layer X — simultaneous solve (optional)

The most robust form treats surface parameters and ray nodes as one least-squares
system. It removes the inner/outer split entirely, but the variable count grows
from tens to thousands, so the current finite-difference Jacobian is unusable.
This layer is out of scope until Layer S proves to be the bottleneck, and it
requires an analytic Jacobian and a sparse solver.

## 5. Mapping onto the current code

| Location | Change | Layer |
|---|---|---|
| `internal/dls/grid.go` (`traceGridRays`) | accumulate per-surface extents over **partial** paths (a ray that fails at surface k still contributes 1…k−1); measure the radial height in the local frame — **implemented** (`surfaceExtents`) | D |
| `internal/constraint/constraint.go` (`evaluateBeamClearance`) | use Layer D; a surface with no reaching ray is a violation, never `diameter/2` | D |
| `internal/optimize/optimize_eval.go` (degenerate penalties) | replace the constant with the margin-dependent residual (the same distance, scaled) | D |
| `internal/optimize/optimize_apertures.go` | keep `auto_aperture` sizing as the structural phase: it removes clear-aperture clipping before the search starts | D |
| `internal/paraxial` | expose per-surface marginal height/angle as the Layer P initializer and surrogate | P |
| `internal/dls` (tracer entry) | add the nested solve behind the same interface, fail-over from the forward march; residual norm as penalty | S |
| `internal/optimize`, `internal/escape`, `internal/pso` | expose the residual as a merit kind and a constraint; add the `ray_validity` schedule metric | R |

## 6. Merit or constraint?

Both, deliberately:

- **Constraint** — the DLS augmented Lagrangian enforces the clearance during a
  solve and reports violations; `region_active` selects the binding surface.
- **Merit kind** — the escape function places bumps on the **merit**, not on the
  constraints ([escape.md §Infeasible basins](../escape.md#infeasible-basins)),
  and PSO ranks particles on `merit + penalty × Σc²`. A clearance that exists
  only as a constraint leaves the escape landscape flat. A merit kind makes the
  exploration phases see the slope.

The two must share one implementation (Layer D), not duplicate the formula.

## 7. Determinism and performance

- The residual layer is pure and order-independent; the nested solve fixes its
  iteration order and tolerance, so results are bit-identical for any worker
  count.
- The finite-difference Jacobian cost multiplies the inner solve by `2n + 1`;
  warm starting from the base path is mandatory. If a column's inner solve needs
  more than a few iterations, the penalty already signals an infeasible region.
- The feasibility stage may measure extents with a cheap 8-ray bundle
  (`TraceFieldExtents8Rays`); the imaging stage keeps the full grid. Do not use
  a coarse bundle for the final gate.

## 8. Diagnostics and gates

- The run reports the worst residual, its `(config, field, surface)` identity and
  the reason (`aperture`, `intersection`, `tir`, `glass_path`, `stop`). A moving
  bottleneck is expected and healthy; a bottleneck that does not move is the
  signal to stop the automatic ramp.
- `optimize --verbose`'s `breakdown` event gains the residual terms, so the
  merit can still be reconciled term by term.
- The physical gate is unchanged: `zoom`'s per-field vignetting, `chief`, `psf`
  and the terminal validation of `escape`/`pso` still trace real rays.

## 9. Validation plan

On a zoom document known to produce no feasible minimum:

1. **Gradient.** The clearance residual Jacobian columns are non-zero at the
   initial (dead) point; the merit is not identical across perturbed points.
2. **Progress.** The bottleneck migrates from surface to surface and eventually
   clears; the run reaches a state where `zoom` reports vignetting ≈ 1.
3. **No cheating.** EPD and EFL do not drift (constrain EPD where the pupil is
   dynamic); `auto_aperture` surfaces do not absorb the violation.
4. **Determinism.** Two runs with different `global_workers` / `jacobian_workers`
   produce the same minima and the same document with `--stream false`.
5. **Regression.** `go test ./...`, the Windows build gate, and
   `staticcheck ./...` stay clean; existing analyses are unchanged when the new
   residual is not enabled.

## 10. Migration phases

| Phase | Deliverable | Exit criterion |
|---|---|---|
| D | residual layer + local-frame extents over partial paths | unit tests for every residual; the current `beam_clearance` no longer reports `D/2` for an unreached surface |
| P | paraxial marginal-height accessor | a dead design has a finite paraxial path and merit |
| S | nested ray solve behind the tracer interface | a ray that misses a surface returns a finite residual and a path; warm started Jacobian stays affordable |
| R | `ray_validity` metric + merit-kind exposure | a dead zoom bootstraps to rays-passing under `optimize` |
| X | simultaneous solve | only if S is the measured bottleneck |

Each phase is a separate commit with its own validation; no phase may change the
default behaviour of a successful trace.

## 11. Risks and open questions

- **Surrogate versus truth.** A virtual path can carry a low merit while the
  physical trace still fails. The physical gate must stay mandatory, and the
  penalty must dominate any advantage a dead design could gain.
- **Spurious minima.** The penalty landscape has basins around each surface
  constraint. Global methods still matter; the goal is to remove the *flat*
  regions, not to make the problem convex.
- **Branch switching.** Non-convex surfaces (spheres, aspheres) have multiple
  roots; the near-root continuation is a heuristic and needs a test on a
  high-NA sphere.
- **Folds, tilts and decenters.** The local-frame residual must be tested on the
  folded Schmidt sample and on tilted surfaces, where global coordinates are
  misleading.
- **What is reported as the merit?** The physical merit stays the reported
  number; the residual is a penalty and a diagnostic. A run that ends with a
  non-zero residual reports it explicitly rather than hiding it in a bounded
  constant.
- **Scope.** Feasibility continuation does not replace the first-order bootstrap
  (`cam`, `scale`, `power` variables). A topology that cannot form the field at
  all is a first-order problem, not a ray-gradient problem.

## 12. References

- [dls-optimization.md](dls-optimization.md) — damped least squares, normalized
  variables, finite-difference Jacobian, augmented Lagrangian constraints.
- [escape-function.md](escape-function.md) — the escape cycle and why a flat
  merit defeats its bumps.
- [pso.md](pso.md) — the swarm's constraint penalty and global-best scale.
- [paraxial.md](paraxial.md) — first-order trace and marginal-ray quantities.
- [merit-and-constraints.md](../merit-and-constraints.md) — merit kinds,
  constraints, degenerate penalties.
- Nocedal & Wright, *Numerical Optimization*, 2nd ed., Springer, 2006 —
  augmented Lagrangians, continuation and trust-region methods.
- Blake & Zisserman, *Visual Reconstruction*, MIT Press, 1987 — graduated
  non-convexity, the prototype of a binary decision replaced by a continuous
  continuation parameter.
