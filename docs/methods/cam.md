# First-order cam solve

How `rayweave cam` turns "every position must reach EFL *f* with the focus on one
detector" into airspace values. See [../cam.md](../cam.md) for the command and
its YAML.

## 1. The residuals

One zoom position has a surface list, a set of cam surfaces `k = 1..n`, and a
target EFL. A candidate layout is the input with the cam airspaces replaced by
`t ∈ ℝⁿ`. `paraxial.Compute` gives the first order, and the position contributes
the residuals

```
r₀ = |EFL(t)| − EFL_target      EFL error (mm)
r₁ = BFL(t) − d(t)              defocus (mm)
r₂ = Z_plane(t) − Z_hold        image-plane position error (mm; only when held)
```

where `BFL` is the paraxial back focal distance from the last refracting surface
and `d` is the path from that surface to the image plane. `BFL` is measured
*from* the last surface, and `d` is the airspace it sits in, so `r₁ = 0` exactly
when the axial focus lands on the image plane.

`Z_plane` is the image-plane surface's `PhysicalZ` — the folded document-frame
vertex — so holding it means holding the **detector**, not a path length. For an
unfolded system `Z_plane` is the total track, so `hold_image_plane: true` is the
usual "constant overall length" zoom requirement.

`|EFL|` is deliberate: after an odd number of reflections the beam-frame radius
changes sign and `EFL` comes out negative, so the comparison uses the magnitude —
the same convention as the `focal_length` [constraint](../merit-and-constraints.md).
A positive `efl` target therefore accepts either branch; check the printed `efl`
sign if a design must not fold.

The vector `r(t)` is the residual for the position. The whole solve is one such
vector per config, solved independently (the positions share the cam *surface
set* — the same variables — but not their values, which is exactly what a cam
does).

## 2. Regularised Gauss-Newton

At each iteration the solver freezes `J = ∂r/∂t` by central finite differences
(`h = 1e-6` mm; near the thickness floor it drops to a one-sided difference) and
computes the step `d` solving

```
minimise  ‖J·d + r‖² + λ²‖t + d − t₀‖²
```

`t₀` are the input airspaces and `λ` is `motion_weight`. Because the problem is
usually under-determined (four group airspaces against three conditions), the
normal matrix `JᵀJ` is singular without the second term. With `λ = 0` the step is
the **minimum-norm** solution of the linearised system — the smallest step that
reduces the residuals — which for a linear problem is exactly the closest point
to `t₀` in the solution set. That is the minimum-motion cam, and it is the
default.

A Levenberg-Marquardt factor `mu` multiplies the diagonal (`A ← diag(JᵀJ) · (1 +
mu)` after the `λ²` addition) to steer between the Gauss-Newton step and a short
steepest-descent step; `mu` starts at `1e-6`, is multiplied by ten on a rejected
step and divided by three on an accepted one. The `n × n` system is solved by
Gaussian elimination with partial pivoting.

## 3. Two ways the geometry fights back

**Thickness floor.** Airspaces are positive, so a step may not push one below
`min_airspace` (0 in practice). The naive fix — shorten the whole step until
nothing crosses the floor — is a trap: a tiny back-focus airspace then caps
*every* variable and the solve crawls. Instead the solver runs an **active set**:
any airspace already on the floor whose step would push it further down is
frozen, and the step is re-solved for the variables that can still move. The
freeze/unfreeze decision is remade from scratch each iteration, so a variable the
direction later releases is released.

**Trust region.** The Newton direction is built on a linearisation of a curved
`r`, so a single unclipped step can teleport the layout. Each variable is capped
at `max(0.5·|t|, 1.0)` mm, and the step is then halved (line search) until it
lowers the objective. The cap is per variable; the applied `α` is the smallest
ratios of cap to step, so a long direction still moves every variable in
proportion.

## 4. Convergence and stopping

A position is converged when `max|rᵢ| ≤ tolerance` (default `1e-9` mm). The loop
stops on convergence, on `max_iterations` (default 50), or when no step lowers
the objective — a local minimum of the regularised objective, which is the honest
answer when the targets are out of reach from this layout.

The two floor/convergence outcomes are reported distinctly: `HitFloor` (an
airspace ended at the floor) means the *geometry* blocked the solve, while a
plain non-convergence with a large EFL residual means the *targets* are outside
what this power/track distribution can reach. The command turns both into a
stderr warning and still writes back the best layout found, so the residuals — not
a silent success — are what the pipeline sees.

## 5. Determinism

The solve is a pure function of (surfaces, cam surface list, targets, options).
Every iterate materialises a fresh copy of the surfaces, precomputes it anew, and
evaluates the same residuals in the same order; no map iteration or floating
point reduction touches the arithmetic. Two runs are bit-identical, which is what
makes a cam result reproducible and a diff meaningful. (The doc's per-config
index map is used only to look up surfaces by ID before the iteration starts.)

## 6. Cost

One paraxial first-order trace per residual evaluation, `2n + 1` evaluations per
Jacobian, and a handful of Newton iterations (the tests converge in 4–5). A
four-variable, three-position zoom solves in well under a millisecond, which is
why `cam` can sit in front of every heavy pipeline.
