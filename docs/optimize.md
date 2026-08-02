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
| `opd_rms` | RMS optical path difference across the pupil |
| `distortion_pct` | percent distortion (chief-ray vs paraxial height) |
| `lateral_color` | lateral colour (chief-ray height difference between two wavelengths) |
| `longitudinal_color` | longitudinal colour (EFL difference between two wavelengths) |
| `seidel_spherical` / `seidel_coma` / `seidel_astigmatism` / `seidel_distortion` | the corresponding third-order Seidel coefficient |

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

## Method

The damped least-squares algorithm, Jacobian construction, constraint handling
and merit assembly are described in
[methods/dls-optimization.md](methods/dls-optimization.md) and
[methods/merit-functions.md](methods/merit-functions.md).
