# `rayweave optimize` — DLS optimization

DLS (Damped Least Squares) optimization of lens surfaces. Reads an
`optimization` section and (optionally) a `configs` section from YAML, runs the
damped least-squares solver, and writes an optimized YAML document with the
updated surface parameters.

```
rayweave optimize [--verbose] [--log FILE] [--glass-dir DIR] [--exclude-param LIST] < input.yaml
rayweave optimize snap [--glass-dir DIR] < optimized.yaml
```

`optimize snap` is a sub-subcommand (see [Snap to catalog](#snap-to-catalog-optimize-snap))
that does not run DLS.

## Options

| Flag | Description |
|---|---|
| `--verbose` | print per-iteration progress to stderr (JSONL) |
| `--log FILE` | write per-iteration progress to FILE (JSONL) |
| `--glass-dir DIR` | AGF glass catalog directory |
| `--exclude-param LIST` | comma-separated target param names to drop from the optimization variables (e.g. `conic,a4,a6`) |
| `--central-diff` | use central-difference Jacobian (2nd-order accurate, 2× residual evaluations) |
| `--bfgs` | enable BFGS-augmented damping (replaces μI with μ·B⁻¹) |
| `--auto-scale` | enable Jacobian-based variable scaling (equalises sensitivity across variables) |

`--glass-dir` is written back into the output's `glass_catalog.directory`
(CLI/YAML rule); `--exclude-param` removes the named targets from the echoed
`optimization.variables`; `--verbose` / `--log` are run-stream flags.
`--central-diff`, `--bfgs`, `--auto-scale` are written back into the
output's `optimization` section. `optimize snap` accepts only `--glass-dir`
(the snap settings are read from the input's `optimization`/`glass_catalog`).

## Input — single-config mode

```yaml
optimization:
  method: dls
  jacobian_workers: 8       # parallel Jacobian goroutines (default GOMAXPROCS)
  max_iter: 100
  central_diff: true        # central-difference Jacobian (2nd-order, 2× cost)
  bfgs: true                # BFGS-augmented damping (μ·B⁻¹ instead of μI)
  adaptive_damping:         # per-variable adaptive damping (sensitivity-based)
    sensitivity_ema: 0.70
    classes:
      curvature:
        sensitivity_power: 1.20
        multiplier: 1.50
      thickness:
        sensitivity_power: 0.75
        multiplier: 0.50
  variables:
    - name: s2_curvature
      target:
        type: surface
        id: 2
        param: curvature
      min: -0.2
      max: 0.2
      active: true
```

The `configs[0].surfaces` hold the system, and the merit is defined either by
`optimization.merit` or `configs[0].merit`:

```yaml
configs:
  - id: config1
    surfaces: [...]
    merit:
      type: spot_rms
      terms:
        - kind: spot_rms
          field: 0
          wavelength: 0.0005876
          weight: 1.0
        - kind: distortion_pct
          field: 2
          weight: 0.5
```

### Variable targets

`optimize` accepts the following surface parameters:

| Param | Meaning |
|---|---|
| `curvature` | surface curvature (1/mm) |
| `thickness` | axial thickness (mm) |
| `diameter` | surface diameter (mm) |
| `nd` / `vd` | glass refractive index / Abbe number (model glasses) |
| `conic` | conic constant (on `asphere_polynomial` surfaces) |
| `a4` / `a6` / `a8` / `a10` / `a12` | even asphere polynomial coefficients (also addressable as `coefficient_0`…`coefficient_4`) |
| `power` | thin-lens power φ (1/mm) of the element containing the target surface (see below) |

Asphere coefficients can be excluded from the variable set wholesale with
`--exclude-param`.

#### The `power` variable

A `power` variable targets the element's **dependent solve surface** (the back
surface of a lens element):

```yaml
  variables:
    - name: s2_power
      target: {type: surface, id: 2, param: power}
      min: -0.04
      max: 0.04
```

The element's thin-lens power φ becomes the optimisation variable;
`applyVariables` re-solves the targeted surface's curvature via
`paraxial.SolveElementPower` after every curvature and glass variable is
applied, so the element power is exact — not finite-differenced geometry — and
matches the φ that `rayweave paraxial`'s `element_roles` reports. The initial
state reads φ from the surfaces (`ElementPowerCurvature`), so a flattened
zero-power start starts at 0 and the optimisation can **build** element power.
Declare the dependent back surfaces as `power` variables instead of
`curvature` variables (the power solve overwrites the curvature); the front
surfaces stay ordinary curvature variables.

The `power` variable composes with the escape glass phase: the non-glass lock
freezes the power variables (`Min == Max`), which activates the
power-preserving solve for exactly the glass phase — without any
`optimization.power_solve` section (whose initial-state snapshot would pin a
zero-power start's zeros forever). A surface driven by a power variable is
skipped by `power_solve`'s snapshot when both are declared, so the variable
wins. Adaptive damping treats `power` like curvature (sensitivity power 1.20,
multiplier 1.50).

## Input — multi-config mode

Multi-config mode is auto-detected when `shared_variables`, `local_variables`
or `configs[].merit` exist.

```yaml
optimization:
  method: dls
  shared_variables:
    - name: group2_shift
      min: -5.0
      max: 5.0
      active: true
      bindings:
        - config: wide
          id: 3
          param: thickness
          scale: 1.0
          offset: 0.0
  local_variables:
    - name: wide_extra_space
      config: wide
      target:
        type: surface
        id: 4
        param: thickness
      min: 0.1
      max: 50.0
      active: true

configs:
  - id: wide
    name: Wide
    weight: 1.0
    active: true
    fields: [...]
    wavelengths: [...]
    surfaces: [...]
    merit:
      type: spot_rms
      terms: [...]
  - id: tele
    name: Tele
    weight: 1.0
    active: true
    fields: [...]
    wavelengths: [...]
    surfaces: [...]
    merit:
      type: spot_rms
      terms: [...]
```

Merit terms are evaluated per config and summed weighted by the config `weight`.
A `CONF` operand selects which config's merit terms are active for each rule.

### Merit kinds

| Kind | Quantity minimized |
|---|---|
| `spot_rms` | RMS spot radius on the reference surface |
| `spot_rms_t` / `spot_rms_s` | flux-weighted tangential / sagittal RMS spot (off-axis decomposition) |
| `spot_rms_worst` | `max`(tangential RMS, sagittal RMS) |
| `spot_rms_weighted` | flux-weighted (pupil-cell-area × intensity) RMS spot |
| `spot_ee_radius` | encircled-energy radius (`fraction` on the term, default 0.8) |
| `opd_rms` | RMS optical path difference across the pupil |
| `distortion_pct` | percent distortion (chief-ray vs paraxial height) |
| `lateral_color` | lateral colour (chief-ray height difference between two wavelengths) |
| `longitudinal_color` | longitudinal colour (EFL difference between two wavelengths) |
| `glass_role` | vd/nd vs the element's chromatic-role target (`surface_set[0]`, see below) |
| `seidel_spherical` / `seidel_coma` / `seidel_astigmatism` / `seidel_distortion` | the corresponding third-order Seidel coefficient |
| `wavefront_defocus` | paraboloid defocus `(a+b)/2` of the fitted wavefront OPD |
| `wavefront_astigmatism` | paraboloid astigmatism `√(((a-b)/2)² + (c/2)²)` |
| `wavefront_tilt` | paraboloid tilt `√(d²+e²)` |
| `wavefront_rms_residual` | RMS of OPD minus the paraboloid (high-order residual) |
| `wavefront_sphere_rms` | **reference-sphere residual RMS** — piston + tilt + defocus removed, **astigmatism retained**; the exact quantity `psf --best-focus` reports as `rms_opd` and the direct Strehl determinant |
| `wavefront_sphere_pv` | reference-sphere residual PV (same model as `wavefront_sphere_rms`) |
| `wavefront_x2` / `wavefront_y2` / `wavefront_xy` / `wavefront_x` / `wavefront_y` / `wavefront_constant` | the raw paraboloid fit coefficients `a…f` |

The wavefront kinds fit the least-squares quadratic
`P(x,y) = a·x² + b·y² + c·xy + d·x + e·y + f` to the field's OPD sampled on the
reference surface (default: the last optical surface; override via
`chief.reference_surface`). The OPD is referenced to the best-focus point
(geometric spot-RMS minimization), exactly like the `wavefront` command, so the
coefficient values match `wavefront_result.fields[].paraboloid`. The
`wavefront_sphere_rms`/`_pv` kinds instead evaluate the best-fit reference
sphere `S(x,y) = a + b·x + c·y + d·(x²+y²)` (piston/tilt/defocus only), so
astigmatism stays in the residual — matching `wavefront_result.fields[].
statistics.rms`/`pv` and `psf_results[].rms_opd`/`pv_opd`. This is the
quantity the Strehl is computed from, so a `wavefront_sphere_rms` term (with
`target: 0`) drives the psf-reported Strehl directly, whereas the paraboloid
kinds drive the low-order coefficients separately. The pupil grid
follows `optimization.num_rays` and `optimization.aperture_margin`, and — like
every grid term — is centred on the config's per-iteration frozen pupil, so the
DLS Jacobian stays consistent. A degenerate fit returns merit `1e6`.

The off-axis spot kinds (`spot_rms_t`/`_s`/`_worst`/`spot_rms_weighted`/
`spot_ee_radius`) address the blind spot of the rotationally symmetric,
uniformly-weighted `spot_rms`: it cannot separate coma (a tangential flare) from
astigmatism, is dominated by a sparse comatic tail, and ignores vignetting and
Fresnel reflection losses. Each grid ray is weighted by its pupil-cell area ×
mean transmitted intensity (see `docs/methods/merit-functions.md`), the
deviation is decomposed into the field's tangential axis (the image-plane
azimuth of `fields[].direction`, default Y) and the perpendicular sagittal axis,
and the five kinds target the tangential/sagittal/worst RMS, the flux-weighted
RMS, and the encircled-energy radius respectively. All five contribute
`(value − target)²`; they reuse the same frozen-pupil grid, so the DLS Jacobian
remains consistent. These kinds are the reference implementation of the
area-weighted spot statistics also reported by `chief`/`vignette`.

`glass_role` steers an element's glass toward the vd/nd its chromatic role
needs, judged by its y²-weighted power `w = φ·y²` against the opposite-sign
neighbours (`internal/paraxial`, the same element grouping as `asphere`): the
element with the larger `|w|` is the couple's crown (`vd* = 60`), the smaller
the flint (`vd* = 60·|w|/W_opp`, clamped 20…60), with a neutral 45 for
near-stop / partner-less elements. The role is sign-free, so it also expresses
the positive flint and the negative crown; `nd*` follows the normal glass line
plus a +0.04 boost for positive-power elements. The term contributes
`(vd_actual − vd*)² + K²·(nd_actual − nd*)²` as one signed residual (K = 60),
and the role classification is frozen each DLS iteration in `UpdatePupils` (see
`docs/methods/merit-functions.md`, §2 — including the worked Cooke-triplet
example showing the stop-suppressed middle flint). It is the directed gradient
that recovers a swapped flint/crown arrangement even when the imagery is not yet
converged.

### Conditional merit schedule (`optimization.merit_schedule`)

A fixed weighted-sum merit can be replaced by a **blend of named merit modes**
whose weights follow the evaluation state — e.g. run a colour-only merit while
the imaging merit is still unconverged, then ramp the imaging terms in. The mode
term lists are declared per config:

```yaml
configs:
  - id: config1
    merit_modes:
      - name: color_first
        terms:
          - kind: longitudinal_color
            wavelength: 0.0004358
            comparison_wavelength: 0.0006563
            weight: 1.0
          - kind: lateral_color
            field: 1
            wavelength: 0.0004358
            comparison_wavelength: 0.0006563
            weight: 0.5
          - kind: glass_role
            surface_set: [3, 5]
            weight: 0.01
      - name: full
        terms:
          - kind: spot_rms
            field: 0
            wavelength: 0.0005876
            weight: 1.0
          - kind: longitudinal_color
            wavelength: 0.0004358
            comparison_wavelength: 0.0006563
            weight: 1.0
```

and the weights are scheduled globally:

```yaml
optimization:
  merit_schedule:
    metric: merit_ratio        # merit_ratio | iteration | glass_role | spot_diffraction
    curve: linear              # linear | sigmoid | step
    anchor_from: 1.0
    anchor_to: 0.05
    glass_surfaces: [3, 5]     # required when metric is glass_role
    modes:
      - name: color_first
        weight_from: 1.0
        weight_to: 0.0
      - name: full
        weight_from: 0.0
        weight_to: 1.0
```

`configs[].merit_modes` replaces that config's `merit`; configs without
`merit_modes` keep their fixed `merit` at full weight. **`merit_modes` without
a `merit_schedule` evaluates the config's fixed `merit`** (empty when
undefined) — the mode terms are ignored and the objective stays 0, so a
schedule is required for the modes to run. The schedule's weights
are continuous functions of the state metric (`merit_ratio`, `iteration`, or
the `glass_role` residual aggregated over `glass_surfaces`), are recomputed once
per DLS iteration and frozen for it, and `Σ residual² == merit` is preserved via
per-term `√weight` scaling (see `docs/methods/merit-functions.md`, §5). The
active mode is reported in the output (`opt_results.active_mode`) and the
per-iteration weights as JSONL `weights` events.

A mode may also carry a `back_focus_type` (`paraxial` / `wavefront`) so the
conditional schedule drives the
[back-focus solve](#back-focus-solve-optimizationback_focus_solve) type — see
that section for the dynamic switching.

### Back-focus solve (`optimization.back_focus_solve`)

A **hard solve** that keeps the image plane at the desired focus during
optimization: after every variable application (and after `power_solve`) the
target surface's thickness is adjusted so the paraxial or wavefront best focus
coincides with the image plane. It is the back-focus analogue of the `power`
variable / `power_solve` family, and runs per config, so a multi-config zoom
pins each config's own back focus.

```yaml
optimization:
  back_focus_solve:
    enabled: true
    type: paraxial             # paraxial (default) | wavefront
    surface: 8                 # 0 = auto-detect (last lens surface before image)
    reference_surface: 0       # wavefront only; 0 = last optical surface
    num_rays: 200              # wavefront only
    wavelength: 0.0005876      # wavefront only; default = config reference wavelength
    weight_type: on_axis_only  # on_axis_only (default) | uniform | custom
    custom_weights: [1.0, 0.5] # weight_type: custom
```

`surface: 0` (the default) auto-detects the target: the last lens surface before
the image plane, skipping air-gap / filter surfaces. An explicit `surface` ID
must exist; an invalid ID disables the solve.

- `type: paraxial` shifts the image plane to the paraxial second principal focus
  (`paraxial.Compute`, `SecondPrincipalFocus`) — fast.
- `type: wavefront` references the wavefront to the **best-focus point**
  (`wavefront.Compute` with `BestFocus`, sphere center from the spot-RMS
  minimisation) — accurate but far more expensive.

For the wavefront type, `reference_surface: 0` uses the last optical surface,
`num_rays` defaults to 200, `wavelength` defaults to the config reference
wavelength, and `weight_type` selects the fields combined into the focus
determination (`on_axis_only` default, `uniform`, or `custom` with
`custom_weights`). The setting is YAML-only (no CLI flag).

#### Dynamic type switching (`back_focus_solve.schedule`)

When a merit schedule is active, the back-focus type can switch between
`paraxial` and `wavefront` as the optimization progresses — e.g. track the focus
cheaply with the paraxial solve while aberrations dominate, then switch to the
wavefront solve near the diffraction limit:

```yaml
optimization:
  merit_schedule:
    metric: spot_diffraction
    curve: step
    anchor_from: 3.0
    anchor_to: 1.0
    modes:
      - {name: spot,      weight_from: 1.0, weight_to: 0.0, back_focus_type: paraxial}
      - {name: wavefront, weight_from: 0.0, weight_to: 1.0, back_focus_type: wavefront}
  back_focus_solve:
    enabled: true
    type: paraxial             # fallback when no dominant mode sets a type
    schedule:
      dominant_threshold: 0.5
```

Each mode may carry a `back_focus_type` (`paraxial` / `wavefront` / unset). At
the top of every DLS iteration the optimizer finds the dominant mode; if its
weight reaches `dominant_threshold` (default 0.5) and it declares a
`back_focus_type`, that type is used, otherwise the static `type` is the
fallback. The switch is a hard change (no blending) and, like the mode weights,
is frozen for the iteration. Modes without `back_focus_type` fall back to the
fixed `type`. A `schedule` section without a merit schedule is inert.

### Constraints

Constraints are defined via `optimization.constraints` (or per config) and
follow the `ConstraintOperand` format. Kinds: `equality`, `inequality_upper`,
`inequality_lower`, `band`, `fuzzy`. Multiple `equality` constraints are
supported (satisfiable targets converge; an unreachable target is reported with
a warning). `edge_thickness` constraints take the back surface via
`back_surface`; when it is omitted the next surface in system order is used.
Constraints are enforced with an augmented-Lagrangian penalty inside
the DLS solve.

## Output

Optimized YAML with updated surface parameters (and materialized model-glass
entries), plus the optimizer `result` section:

```yaml
result:
  before_merit: ...
  after_merit: ...
  iterations: ...
  status: ...              # converged | converged_gradient | max_iterations
  variables: [...]         # per-variable before/after values
```

## Logs and `query`

`--log FILE` (or `--verbose`) writes one JSON object per line: `merit`,
`improvement`, `step_norm`, `variables`, and `event: "breakdown"` lines carrying
a `terms` map for the per-term merit breakdown. These can be inspected with
`rayweave query --jsonl`:

```sh
rayweave optimize --log opt.jsonl < lens.yaml > out.yaml
rayweave query --jsonl --where 'has("merit")' -r merit < opt.jsonl
rayweave query --jsonl --where 'event=="breakdown"' \
  --each 'terms:key,value' --printf '  %s: %.6e' < opt.jsonl
```

## Notes

- `optimization.aperture_margin` is clamped to ≥ 1.0; smaller values make the
  pupil grid smaller than the aperture and stall DLS.
- `optimization.jacobian_workers` sets the goroutines used for the finite
  difference Jacobian (default `GOMAXPROCS`). The Jacobian is deterministic:
  the result is identical for any worker count.
- `optimization.central_diff` switches to central-difference Jacobian
  (2nd-order accurate). Doubles the residual evaluations per iteration but
  improves gradient accuracy for tightly-coupled variables. **Strongly
  recommended when using `bfgs: true`** — forward-difference gradient errors
  make the BFGS inverse Hessian approximation unreliable.
- `optimization.bfgs` enables BFGS-augmented damping: the normal equations
  use `μ·B⁻¹` instead of `μI`, where `B` is the damped-BFGS inverse Hessian
  approximation. Gives superlinear convergence in well-conditioned valleys.
  **Always pair with `central_diff: true`** — BFGS alone may stall because
  noisy forward-difference gradients corrupt the Hessian update.
- `optimization.adaptive_damping` enables per-variable adaptive damping: the
  solver replaces the fixed μI damping with μD where D is a diagonal matrix
  derived from Jacobian sensitivity, variable class (curvature, thickness,
  asphere, etc.), and accept/reject history. This gives high-sensitivity
  variables stronger damping while letting low-sensitivity variables move more
  freely.
- Grid traces for different (field, wavelength) pairs within a single merit
  evaluation are now parallelised across CPU cores.
- `configs[].ray_paths` is render-only metadata; the optimizer ignores it.
- Glass variables (nd/vd) are constrained to stay inside the glass hull when
  `optimization.glass_hull.enabled: true`.
- A `SIGINT`/`SIGTERM` stops the solve gracefully (`interrupted: true`, exit 0):
  the first signal interrupts the running DLS within one iteration and writes
  the best point found so far to stdout; the second force-quits (exit 1).

### Degenerate merit terms (`optimization.degenerate`)

A merit term that cannot be evaluated — a pupil grid with no valid rays
(a fully clipped off-axis beam), or a wavefront fit that fails even after the
dynamic-pupil fallback — returns a **bounded penalty** instead of the legacy
1e6 sentinel (which fed `weight·1e12` into the merit and stalled the DLS line
search). The penalties are configured per metric category:

```yaml
optimization:
  degenerate:
    spot_value: 0.1          # mm; spot_rms / spot_rms_t/s/worst / weighted / ee_radius
    opd_value: 1.0e-2        # mm; opd_rms
    wavefront_value: 1.0e-3  # mm; wavefront_* paraboloid kinds
```

All values default when unset (0.1 / 0.01 / 0.001 mm). Non-positive values keep
the built-in default. The contribution is `weight·value²` (e.g. a
`wavefront_astigmatism` term at weight 14000 contributes at most
`14000·(1e-3)² = 1.4e-2`), so a degenerate off-axis field pushes the solver
towards a region where the term can be evaluated without exploding the merit.
Successful terms are unaffected, so existing merit values are unchanged.

### Glass attraction (`optimization.glass_attraction`)

A soft-min potential field that pulls nd/vd glass variables toward the nearest
real catalog glass during optimisation. The attraction supplements the glass
hull: the hull prevents unphysical glass, the attraction actively pulls toward
real glass.

```yaml
optimization:
  glass_attraction:
    enabled: true
    kernel: distance            # distance (default) | gaussian
    sigma_nd: 0.03              # gaussian kernel width (normalised)
    sigma_vd: 0.03
    weight_from: 0.0            # weight at anchor_from
    weight_to: 1.0              # weight at anchor_to
    metric: merit_ratio         # iteration | run_iteration | merit_ratio
    anchor_from: 1.0            # metric value at t=0
    anchor_to: 0.2              # metric value at t=1
    curve: linear               # linear | sigmoid | step
    sensitivity_weighted: true  # per-glass vd sensitivity weighting (default false)
    sensitivity_metric: elasticity  # elasticity (only mode currently implemented)
    sensitivity_ema: 0.6        # EMA smoothing across iterations
    sensitivity_power: 1.0      # exponent for scale = (s/s_ref)^p
    sensitivity_ref: mean       # mean | max
    sensitivity_scale_min: 0.1
    sensitivity_scale_max: 4.0
```

**How it works:**

1. At startup, `BuildCatalogField` collects every glass in the catalog with
   valid nd/vd and normalises them to the default glass range
   (nd ∈ [1.413, 2.154], vd ∈ [16.48, 101.0]).

2. Each DLS iteration, the attraction weight `w(t)` is interpolated along the
   curve between `weight_from` (at `anchor_from`) and `weight_to` (at
   `anchor_to`). The metric is normalised to [0,1] by the anchor range.

3. For each glass variable pair (nd_i, vd_i), the soft-min potential
   `F = min_j φ_j(r_j)` is computed where `r_j` is the normalised distance
   to catalog glass j. The `distance` kernel uses `φ = r²`; the `gaussian`
   kernel uses `φ = 1 − exp(−r²/2σ²)`.

4. The contribution to the merit is `w(t) · scale_i · F` where `scale_i` is
   the per-glass sensitivity scale (default 1.0 when sensitivity is disabled).

5. **Sensitivity weighting** (when `sensitivity_weighted: true`): the vd
   elasticity `|∂M_optical/∂vd_i| · vd_i / (M_optical + ε)` is computed via
   forward finite differences of the optical merit (excluding attraction and
   hull). EMA-smoothed across iterations, normalised to the mean (or max),
   and raised to the power `sensitivity_power`. Glasses with higher merit
   sensitivity to their Abbe number get stronger attraction.

**Escape integration:** glass attraction is active during escape sub-solves.
The `merit_ratio` metric (default for escape) uses the optical-only merit
to avoid self-reference. The weight ramp persists per worker across escape
cycles via `run_iteration` or `merit_ratio`.

**Diagnostics:** `opt_results.glass_attraction` (optimize only) carries
`weight` (final attraction weight) and per-pair `name`, `nd`, `vd`,
`nearest_key`, `nearest_nd`, `nearest_vd`, `distance`, `scale`.
Displayed by `list optimization` when present.

## Snap to catalog (`optimize snap`)

```
rayweave optimize snap [--glass-dir DIR] < optimized.yaml
```

A sub-subcommand that performs a **discrete** glass selection without running
DLS. It reads a document (typically the output of `optimize` or of
`escape extract`), replaces every **declared nd/vd glass variable** with the
nearest real catalog glass in normalised (nd, vd) space, and reports the
resulting change in the **optical merit** (the glass-attraction and glass-hull
penalties are excluded, so the number is the raw cost of discretising the
glass). It is a pure transform: the DLS solver is never invoked, other
variables are left at their input values, and the substitution is
deterministic.

Snap targets are exactly the surfaces carrying a declared `nd`/`vd` variable
(the same pairs the glass attraction uses) — the command does not touch
un-declared glass surfaces or non-glass variables. Each snapped surface is
written back as an inline model glass holding the catalog glass's nd/vd (the
chosen catalog glass is named in the report).

Output (in addition to the updated surfaces):

```yaml
opt_results:
  status: snapped
  snap:
    before_merit: 2.088717e-03
    after_merit: 1.386312e-01
    cost: 1.365425e-01        # after - before
    cost_pct: 6537.15          # 100*(after-before)/before
    pairs:
      - surface_id: 3
        name: LF5              # chosen catalog glass
        from_nd: 1.56049
        from_vd: 34.630
        to_nd: 1.58144
        to_vd: 40.850
        distance: 0.0788       # normalised distance before snapping
```

A large `cost_pct` is a warning that the continuous optimum was far from any
real glass and that snapping would materially degrade the design; a `cost_pct`
near zero means the glasses were already effectively on the catalog. The
command exits non-zero when the input declares no nd/vd glass variables or the
catalog cannot be resolved.

## Method

The damped least-squares algorithm, Jacobian construction, constraint handling
and merit assembly are described in
[methods/dls-optimization.md](methods/dls-optimization.md) and
[methods/merit-functions.md](methods/merit-functions.md).
