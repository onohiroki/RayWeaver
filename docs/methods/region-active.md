# Region Active Method (Okudaira)

This document describes the **Okudaira Region Active Method** — a
Lagrange-multiplier-based dynamic active-set strategy for inequality constraints
in lens-design optimization.  It extends the augmented-Lagrangian constraint
treatment of the DLS solver (see [dls-optimization.md](dls-optimization.md))
with per-constraint hysteresis switching and selective multiplier updates.

## 1. Background: constrained optimization in lens design

A lens-design optimizer minimizes a merit function Φ(x) (wavefront error,
spot size, etc.) subject to physical constraints:

- **Equality constraints** h_j(x) = 0 — e.g. effective focal length, entrance
  pupil diameter.
- **Inequality constraints** g_k(x) ≤ 0 — e.g. minimum centre thickness,
  maximum total track length, back-focus limits, edge-thickness clearance.

The augmented-Lagrangian method appends each constraint to the merit as a
penalty term:

```
p_j = λ_j · c_j + ½ · μ_j · c_j²
```

where c_j is the violation (signed for equality, non-negative for inequality),
λ_j is the Lagrange multiplier, and μ_j is the per-constraint penalty weight.
This is the approach described in [dls-optimization.md](dls-optimization.md),
section 5.

A limitation of the standard approach is that **all** constraints contribute to
the penalty at every iteration.  When many inequality constraints are present
but only a few are actually binding (g_k ≈ 0), the inactive constraints add
unnecessary curvature to the normal equations, slowing convergence or causing
the solver to stall.

## 2. The Okudaira Region Active Method

The Region Active Method (Okudaira, 2001) addresses this by **dynamically
partitioning inequality constraints into active and inactive subsets** at each
iteration, so that only the binding constraints participate in the augmented
system.  The name "region" refers to the feasible region defined by each
constraint's boundary: a constraint is "active" when the current design point
lies on (or near) its boundary region.

### 2.1 Complementarity condition

The method is rooted in the KKT complementarity condition.  For each
inequality constraint g_k(x) ≤ 0 with multiplier λ_k ≥ 0:

```
λ_k · g_k(x) = 0
```

This means:

- If g_k(x) < 0 (strictly feasible, constraint has slack) → λ_k = 0
  (constraint is inactive).
- If g_k(x) = 0 (on the boundary) → λ_k ≥ 0 (constraint is active).

The Region Active Method enforces this dichotomy iteratively rather than
solving for it simultaneously.

### 2.2 Active-set update with hysteresis

At the start of each DLS iteration, every inequality constraint is classified:

```
Activate:   NOT active  AND  violation > ε_activate
Deactivate: active      AND  violation < ε_deactivate  AND  |λ_k| small
```

The two thresholds satisfy **ε_activate > ε_deactivate** (hysteresis
invariant).  This gap prevents rapid toggling ("chattering") of the active set
when a constraint boundary oscillates near the design point — a common
pathology in lens optimization where curvature changes flip edge-thickness
signs.

Equality constraints are **always active** regardless of their residual.

### 2.3 Lagrange multiplier update

Only active constraints have their multipliers updated:

```
λ_k ← max(0,  λ_k + α · g_k(x))
```

where α is the step size (default 1.0).  The max(0, ·) clamp enforces
λ_k ≥ 0, consistent with inequality-constraint KKT conditions.  The
multiplier is capped at λ_max (default 10^6) to prevent overflow.

Inactive constraints keep their current λ_k (which is 0 on deactivation, or
the stored value from a previous active period).  This is important: deactivating
a constraint does not destroy its multiplier history, so re-activation can
resume with the learned penalty weight.

## 3. Integration into the DLS solver

### 3.1 Augmented system with active filtering

The DLS solver builds an augmented residual and Jacobian from the objective
and constraint gradients.  With the Region Active Method, only the active
subset A ⊆ {1, …, q} contributes:

```
r_aug = [ r_opt ]
        [ r_con(A) ]

J_aug = [ J_opt        ]
        [ J_con(A)     ]
```

where r_con(A) is the vector of scaled constraint residuals for k ∈ A only.
The scaling is:

```
r_con_k = √(μ_k/2) · (g_k + λ_k / μ_k)
```

so that r_con_k² reproduces the augmented-Lagrangian penalty gradient.

When no region-active updater is present (or disabled), A = {1, …, q} and the
system is identical to the standard augmented-Lagrangian — zero overhead.

### 3.2 Merit evaluation

The merit function sums the objective merit and only the **active** constraint
penalties:

```
merit = Φ(x) + Σ_{k ∈ A} [ λ_k · g_k + ½ · μ_k · g_k² ]
```

Inactive constraints contribute zero to the merit.  This means the line search
and acceptance criterion operate on a merit that reflects only the binding
constraints, preventing inactive constraints from dominating the step
selection.

### 3.3 Multiplier synchronization

The Lagrange multipliers are managed in two places:

1. **Region-active state** (per-config, in the `Optimizer`): stores the
   canonical λ_k values and the active/inactive flag.
2. **Solver-local lambdas**: the DLS solver maintains a local copy that it
   updates on accepted steps.

After each accepted step, the solver writes the updated multipliers back to
the region-active state.  At the start of the next iteration, the solver reads
back the multipliers.  This round-trip ensures the multipliers survive across
DLS restarts (escape cycles, glass phases) and are consistent between the
solver and the optimizer.

### 3.4 Jacobian filtering

The constraint Jacobian `J_con` is computed for **all** constraints (the cost
of a constraint Jacobian is dominated by the objective Jacobian, which is
already computed).  `buildAugmentedActive` then selects only the rows
corresponding to active constraints, avoiding any structural change to the
finite-difference loop.

## 4. Hysteresis design choices

### 4.1 Threshold defaults

| Parameter | Default | Purpose |
|---|---|---|
| ε_activate | 1e-3 mm | Activate when violation exceeds this |
| ε_deactivate | 1e-4 mm | Deactivate when violation drops below this |
| α (lambda_step) | 1.0 | Multiplier update step size |
| λ_max | 1e6 | Multiplier cap |

### 4.2 Why hysteresis matters

Without hysteresis (ε_activate = ε_deactivate), a constraint at g_k ≈ ε would
toggle active/inactive every iteration.  Each toggle changes the augmented
system dimension, which can cause the Jacobian to become ill-conditioned (rows
appearing/disappearing) and the DLS step to oscillate.  The 10:1 ratio of
ε_activate to ε_deactivate (1e-3 vs 1e-4) provides a dead zone that absorbs
numerical noise and step-to-step fluctuation.

### 4.3 Interaction with penalty weight μ_j

The per-constraint penalty weight μ_j grows ×10 when a violation persists on
an accepted step (up to μ_con_max).  This is independent of the active-set
switching: an active constraint with a growing μ_j is enforced more tightly,
while an inactive constraint's μ_j is irrelevant (it contributes zero to the
merit and augmented system).

## 5. Interaction with the escape-function optimizer

The escape-function global optimizer (see [escape-function.md](escape-function.md))
runs multiple DLS cycles (escape, glass, clean phases) wrapped by a `Wrapper`
model.  The Region Active Method integrates transparently:

- The `Wrapper` forwards `UpdateRegionActiveSet`, `ActiveConstraintIndices`,
  `ConstraintMultipliers`, and `SetConstraintMultipliers` to the inner
  `Optimizer`.
- Each DLS cycle (escape, glass, clean) independently updates the active set
  based on the current design point.
- Escape bumps only affect the objective merit, not the constraint evaluation,
  so the active-set classification is determined solely by constraint
  violations.

This means the active set can change between escape cycles: constraints that
were inactive at one local minimum may become active at another, which is
the desired behaviour for global exploration.

## 6. Configuration

Add a `region_active` section to `optimization` in the YAML input:

```yaml
optimization:
  region_active:
    enabled: true
    eps_activate: 1.0e-3
    eps_deactivate: 1.0e-4
    lambda_step: 1.0
    max_lambda: 1.0e6
```

When `region_active` is absent or `enabled: false`, the solver falls back to
the standard augmented-Lagrangian with all constraints active — identical to
the pre-Region-Active behaviour.

### 6.1 YAML fields

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | false | Enable the region active method |
| `eps_activate` | float | 1e-3 | Activation threshold (violation > this → active) |
| `eps_deactivate` | float | 1e-4 | Deactivation threshold (violation < this → inactive) |
| `lambda_step` | float | 1.0 | Multiplier update step α |
| `max_lambda` | float | 1e6 | Multiplier cap |

### 6.2 CLI override

The region-active configuration is YAML-only; there are no CLI flags.  This
matches the convention that computation settings (not selection flags) live in
YAML.

## 7. Implementation notes

### 7.1 Determinism

The active-set update depends on the constraint violations at the current
design point, which are deterministic for a given x.  The DLS Jacobian is
parallelized (see [dls-optimization.md](dls-optimization.md), section 2), but
the augmented-system construction that follows is sequential and depends on
the frozen active set.  Therefore, DLS results are bit-identical for any
worker count, even with the Region Active Method enabled.

### 7.2 Equality constraints

Equality constraints (e.g. `constraint: {kind: equality, measure: efl,
target: 50}`) are always active.  `BuildRegionActiveStates` initializes
equality constraints with `Active = true`, and `UpdateActiveSet` forces
`Active = true` for them regardless of the violation.  Their multipliers are
updated in the same way as inequality constraints.

### 7.3 Degenerate evaluations

When a constraint cannot be evaluated (all rays clipped, wavefront fit failure,
etc.), the degenerate penalty is applied (see `optimization.degenerate`).  The
Region Active Method does not alter this behaviour: a degenerate evaluation
returns a bounded penalty, and the active-set update uses the measured violation
(zero for unevaluable constraints), so the constraint is neither activated nor
deactivated by a failed evaluation.

### 7.4 Data structures

```
RegionActiveState
├── Operand    ConstraintOperand   (read-only definition)
├── Active     bool                (current active-set membership)
├── Lambda     float64             (Lagrange multiplier)
└── Violation  float64             (most recent violation, for monitoring)
```

Per-config states are stored in `Optimizer.regionActive[]` and flattened to a
1-D index space for the solver.  The solver's `activeIndices` slice maps back
to per-config constraint indices via offset arithmetic.

## 8. References

- Okudaira, "Active control of optimization process in lens design by using
  Lagrange's undetermined multiplier method," *Korean Journal of Optics and
  Photonics*, vol. 12, no. 2, 2001.
- Nocedal & Wright, *Numerical Optimization*, 2nd ed., Springer, 2006.
  (Ch. 17: augmented Lagrangian methods.)
- Fletcher, *Practical Methods of Optimization*, 2nd ed., Wiley, 1987.
  (Ch. 12: penalty and augmented Lagrangian methods.)
