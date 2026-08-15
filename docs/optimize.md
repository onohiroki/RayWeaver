# `rayweave optimize` — DLS optimization

DLS (Damped Least Squares) optimization of lens surfaces. Reads an
`optimization` section and (optionally) a `configs` section from YAML, runs the
damped least-squares solver, and writes an optimized YAML document with the
updated surface parameters.

```
rayweave optimize [--verbose] [--log FILE] [--glass-dir DIR] [--exclude-param LIST] < input.yaml
```

## Options

| Flag | Description |
|---|---|
| `--verbose` | print per-iteration progress to stderr (JSONL) |
| `--log FILE` | write per-iteration progress to FILE (JSONL) |
| `--glass-dir DIR` | AGF glass catalog directory |
| `--exclude-param LIST` | comma-separated target param names to drop from the optimization variables (e.g. `conic,a4,a6`) |

`--glass-dir` is written back into the output's `glass_catalog.directory`
(CLI/YAML rule); `--exclude-param` removes the named targets from the echoed
`optimization.variables`; `--verbose` / `--log` are run-stream flags.

## Input — single-config mode

```yaml
optimization:
  method: dls
  jacobian_workers: 8       # parallel Jacobian goroutines (default GOMAXPROCS)
  max_iter: 100
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

Asphere coefficients can be excluded from the variable set wholesale with
`--exclude-param`.

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
| `glass_role` | Abbe number vs the element-power role target (`surface_set[0]`, see below) |
| `seidel_spherical` / `seidel_coma` / `seidel_astigmatism` / `seidel_distortion` | the corresponding third-order Seidel coefficient |
| `wavefront_defocus` | paraboloid defocus `(a+b)/2` of the fitted wavefront OPD |
| `wavefront_astigmatism` | paraboloid astigmatism `√(((a-b)/2)² + (c/2)²)` |
| `wavefront_tilt` | paraboloid tilt `√(d²+e²)` |
| `wavefront_rms_residual` | RMS of OPD minus the paraboloid (high-order residual) |
| `wavefront_x2` / `wavefront_y2` / `wavefront_xy` / `wavefront_x` / `wavefront_y` / `wavefront_constant` | the raw paraboloid fit coefficients `a…f` |

The wavefront kinds fit the least-squares quadratic
`P(x,y) = a·x² + b·y² + c·xy + d·x + e·y + f` to the field's OPD sampled on the
reference surface (default: the last optical surface; override via
`chief.reference_surface`). The OPD is referenced to the best-focus point
(geometric spot-RMS minimization), exactly like the `wavefront` command, so the
coefficient values match `wavefront_result.fields[].paraboloid`. The pupil grid
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

`glass_role` steers an element's Abbe number toward the role its power requires
(negative power → flint, positive power → crown) via
`vd_target = 45 + 16·tanh(φ)` and contributes `(vd_actual − vd_target)²`. The
element is the one whose bounding surfaces include `surface_set[0]` (see
`docs/methods/merit-functions.md`, §2). It is the directed gradient that
recovers a swapped flint/crown arrangement even when the imagery is not yet
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
            wavelength2: 0.0006563
            weight: 1.0
          - kind: lateral_color
            field: 1
            wavelength: 0.0004358
            wavelength2: 0.0006563
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
            wavelength2: 0.0006563
            weight: 1.0
```

and the weights are scheduled globally:

```yaml
optimization:
  merit_schedule:
    metric: merit_ratio        # merit_ratio | iteration | glass_role
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
`merit_modes` keep their fixed `merit` at full weight. The schedule's weights
are continuous functions of the state metric (`merit_ratio`, `iteration`, or
the `glass_role` residual aggregated over `glass_surfaces`), are recomputed once
per DLS iteration and frozen for it, and `Σ residual² == merit` is preserved via
per-term `√weight` scaling (see `docs/methods/merit-functions.md`, §5). The
active mode is reported in the output (`opt_results.active_mode`) and the
per-iteration weights as JSONL `weights` events.

### Constraints

Constraints are defined via `optimization.constraints` (or per config) and
follow the `ConstraintOperand` format. Kinds: `equality`, `inequality_upper`,
`inequality_lower`, `band`, `fuzzy`. Multiple `equality` constraints are
supported (satisfiable targets converge; an unreachable target is reported with
a warning). `edge_thickness` constraints take the back surface explicitly via
`surface2`. Constraints are enforced with an augmented-Lagrangian penalty inside
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

## Method

The damped least-squares algorithm, Jacobian construction, constraint handling
and merit assembly are described in
[methods/dls-optimization.md](methods/dls-optimization.md) and
[methods/merit-functions.md](methods/merit-functions.md).
