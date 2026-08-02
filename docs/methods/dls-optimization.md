# DLS (Damped Least Squares) optimization

This document describes the optimization algorithm used by `rayweave optimize`
(and reused by the escape-function global optimizer). It implements a damped
least-squares (Levenberg–Marquardt-type) solver over a normalized variable
space, with an augmented-Lagrangian treatment of constraints.

## 1. Variable normalization

Each optimization variable has a `min` and `max`. The solver works in a
**normalized space** `x ∈ [0,1]^n`:

```
x_norm = (x_phys − min) / (max − min)
```

Normalization gives curvature, thickness, diameter and glass parameters
comparable magnitudes, which stabilizes the linear algebra and is also the
space in which escape bumps are defined (see
[escape-function.md](escape-function.md)). Variables with `min == max` are
fixed.

## 2. Residuals and Jacobian

The model provides a residual vector `r(x)` (typically one entry per merit
term) and an objective `merit(x) = ½ ‖r‖²`-like sum. The solver computes the
Jacobian `J` of `r` with respect to the normalized variables by central
finite differences:

```
J[i][j] ≈ (r_i(x + ε·scale_j) − r_i(x)) / ε
```

with `ε = optimization.epsilon` (default 1e-6) and `scale_j = max − min`.

The Jacobian columns are embarrassingly parallel: each column requires an
independent perturbed evaluation. `optimization.jacobian_workers` sets the
goroutine count (default `GOMAXPROCS`), and because each column is written to
its own slice, the result is **bit-identical for any worker count**.

## 3. Gauss–Newton step with damping

At each iteration the solver forms the normal equations

```
(JᵀJ + μI) δ = −Jᵀ r
```

and solves for the step `δ` (dense linear solve, `raymath.SolveLinear`).
The damping parameter `μ` (default 1e-2) is the "damped" part of DLS: large `μ`
behaves like gradient descent (robust, slow), small `μ` like Gauss–Newton
(fast, fragile).

- The step length is capped (‖δ‖ ≤ 0.5 in normalized space) to avoid wild
  jumps.
- Steps are clamped to the variable box `[0,1]`.
- If the trial point does not reduce the merit, a **backtracking line search**
  halves the step up to 8 times (Armijo-like condition).
- The damping is adapted after each step using the classic Levenberg ratio

```
ρ = actual_reduction / predicted_reduction
```

with `μ` decreased when `ρ > 0.25` (good fit) and doubled otherwise.

Convergence is declared when the gradient norm is below `optimization.tol`
(`converged_gradient`), or when the step is small and the merit has not
improved for a window of iterations while every non-relaxed constraint is
satisfied (`converged`).

## 4. Stall escape

If the merit stalls for many iterations (30), the solver resets to the best
known state and applies a **deterministic pseudo-random perturbation** of ±1%
of the normalized range, then resets the damping. This is disabled when the
escape-function wrapper runs (`optimization.escape`), because the escape cycle
provides its own restart mechanism.

## 5. Constraints (augmented Lagrangian)

Constraints are handled by appending residuals to the system. Each constraint
`c_j(x)` contributes the penalty

```
p_j = λ_j·c_j + ½·μ_j·c_j²
```

to the merit (clamped to be non-negative), and an equivalent augmented residual

```
r_aug = √(μ_j/2) · (c_j + λ_j/μ_j)
```

whose square reproduces the penalty gradient. On accepted steps the multiplier
is updated `λ_j ← λ_j + μ_j·c_j`, and the per-constraint penalty weight `μ_j`
grows ×10 when a violation persists, up to `mu_con_max`. Each constraint is
therefore enforced tightly when satisfiable, but an infeasible constraint can be
relaxed rather than freezing the solve.

## 6. Options summary

| Option | Default | Meaning |
|---|---|---|
| `optimization.max_iter` | 100 | max outer iterations |
| `optimization.mu` | 1e-2 | initial damping |
| `optimization.tol` | 1e-6 | gradient / step tolerance |
| `optimization.epsilon` | 1e-6 | finite-difference step |
| `optimization.num_rays` | 64 | pupil grid rays per merit evaluation |
| `optimization.aperture_margin` | 2.0 | pupil grid radius multiplier (clamped ≥ 1) |
| `optimization.mu_con_max` | 100 | max constraint penalty weight |
| `optimization.jacobian_workers` | GOMAXPROCS | Jacobian parallelism |

## 7. Shared / local variables (multi-config)

In multi-config mode a **shared variable** binds one normalized component to
several surface parameters across configs via `scale`/`offset` bindings
(`param = scale·x + offset`), letting e.g. a group shift move the same group in
every zoom position. A **local variable** targets one surface parameter of one
config. All configs' merits are summed (weighted by config `weight`) into a
single objective, and the Jacobian is computed with respect to the combined
shared+local variable vector.
