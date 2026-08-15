# `rayweave asphere` — asphere candidate selection and initial sag estimation

The `asphere` subcommand ranks the candidate surfaces of a system for
introduction of a rotationally-symmetric even-order asphere, fits safe
**initial** asphere coefficients (conic + A4..A12), optionally verifies each
fitted asphere with a short DLS solve, and optionally inserts the top-ranked
validated asphere into the system. The numerical method behind the ranking and
the fit is described in
[asphere-candidates.md](methods/asphere-candidates.md).

```
rayweave asphere [--rings N] [--angles N] [--pupil-samples N] [--sensitivity-samples N]
                 [--top-k N] [--sag-scale α] [--validate] [--apply] < system.yaml
```

It reads standard system YAML, writes pipeline-compatible YAML with an
`asphere_candidate_result:` section appended.

## Options

| Flag | Description |
|---|---|
| `--rings N` | polar cell radial rings (default 8) |
| `--angles N` | polar cell angular sectors (default 16) |
| `--pupil-samples N` | pupil grid radial samples per (field, wavelength) (default 21) |
| `--sensitivity-samples N` | pupil grid radial samples for the measured sensitivity pass (default 9; `0` disables the measured pass and falls back to the analytic index-contrast proxy) |
| `--top-k N` | number of top-ranked surfaces to fit (default 3) |
| `--sag-scale α` | scalar applied to the fitted coefficients for safe insertion (default 0.2; try 0.05..0.5) |
| `--calibrate-scale` | derive each candidate's embedded asphere scale from the measured ray-trace response instead of the fixed `sag_scale` (on by default; disable with `--calibrate-scale=false` / `calibrate_scale: false`) |
| `--scale-probes L` | comma-separated scales to verify instead of the quadratic estimate (e.g. `0.1,0.25,0.5,1.0`) |
| `--validate` | run a short DLS per fitted surface to verify the asphere improves the merit |
| `--apply` | insert the top-ranked DLS-validated asphere onto its surface and output the modified system (implies `--validate`) |
| `--dls-iter N` | DLS iterations per validated surface (default 20, with `--validate`) |
| `--num-rays N` | pupil grid rays for the validation DLS (default 64) |
| `--config ID` | select config by id (multi-config mode) |
| `--glass-dir DIR` | AGF glass catalog directory |

Flags override the corresponding `asphere_candidate:` YAML values; the
effective (flag-won) values are written back into the output's
`asphere_candidate:` section (CLI/YAML rule).

## Input

The system must carry a `chief` section so the fields (and optionally the stop
surface) are known. Fields may come from `chief.fields` / `chief.field_angles`,
from the selected config's `fields` (which can carry per-field weights), or from
`configs[0].fields`. Wavelengths come from `chief.wavelengths`, else the selected
config's `wavelengths`, else the default 587.56 nm.

```yaml
chief:
  fields:
    - {angle: 0}
    - {angle: 14, weight: 1.0}   # optional weight; default 1
  stop_surface: 7               # optional; without a stop the grid is centred
                                # on the dynamic pupil (per-field chief-ray
                                # crossing), falling back to the tightest fixed
                                # aperture when ill-conditioned
```

Optional configuration is supplied in an `asphere_candidate:` section. All
fields are optional; unset values keep the defaults.

```yaml
asphere_candidate:
  candidate_surfaces: [2, 4, 6, 8]   # default: every non-mirror surface
  max_even_order: 10                 # 8 → A4..A8, 10 → A4..A10, 12 → A4..A12
  include_conic: true                # fit a conic on the polynomial residual
  preserve_vertex_curvature: true    # true: r² term reported as a warning;
                                     # false: reported as a curvature change
  sag_scale: 0.2                     # probe scale (scalar on embedded coefficients)
  calibrate_scale: true              # per-surface embedded scale from the measured
                                     # ray-trace response (default; see below)
  scale_probes: []                   # explicit scales to verify ([] = quadratic estimate)
  cell_rings: 8                      # polar cell rings
  cell_angles: 16                    # polar cell angular sectors
  pupil_samples_radial: 21           # pupil grid radial samples
  sensitivity_samples: 9             # 0 = analytic proxy only
  remove_tilt: true                  # remove best-fit tilt plane from per-field OPD
  remove_defocus: false              # additionally remove best-fit defocus paraboloid
  top_k: 3                           # surfaces to fit coefficients for
  min_rays_per_cell: 3               # minimum ray hits for a cell to count
  score_weights:                     # composite score weights
    common: 0.35
    unique: 0.15
    fit: 0.20
    sensitivity: 0.15
    conflict: 0.10
    manufacturing: 0.05
  validate: false                    # enable the short-DLS validation (--validate)
  apply: false                       # insert the top validated asphere (--apply)
  validation_dls_iter: 20            # validation DLS iterations (--dls-iter)
  validation_num_rays: 64            # validation pupil-grid rays (--num-rays)
```

Piston is **always** removed (per-field OPD is referenced to the field's mean
OPL); `remove_piston` is accepted for compatibility but has no effect. The
fields `max_sag`, `max_slope_deg` and `max_curvature_variation` are accepted
for forward-compatibility but are **not** used by the current analysis — see
[asphere-candidates.md](methods/asphere-candidates.md) for what actually happens.

## Output

The output preserves the input and appends `asphere_candidate_result:`. The
`rankings` array is sorted by descending score:

```yaml
asphere_candidate_result:
  rankings:
    - surface_id: 8
      score: 0.49082103748858696
      coverage: 0.8326042802440168
      common_energy: 0.8326042802440168
      conflict: 0.3355717403069528
      unique_energy: 0
      fit_quality: 0.7373492238882969
      manufacturing_penalty: 0.35553455625429586
      sensitivity_penalty: 0.6884906431262114
      coefficients:
        A4: -1.1110577872376941e-05
        A6: -1.9934022308602345e-07
        A8: -2.8788375617480384e-09
        A10: -4.12459203574251e-11
      scaled_coefficients:
        A4: -2.222115574475388e-06
        # ...
      calibrated_coefficients:
        A4: -2.168328e-06
        # ...
      sensitivity:
        base_merit: 0.03531941389044646
        asphere_merit: 0.011002327906172131
        improvement: 0.6884906431262114
        d_merit_d_coef: [-1133.28, -48128.41, -2.55e+06, 1.67e+08, 4.09e+08]
        calibrated_scale: 0.184
        calibrated_merit: 0.010874
        calibrated_improvement: 0.692
      warnings:
        - "removed defocus r² term (2·a2=0.00138725); not part of the asphere coefficients"
      # only with --validate:
      validation:
        surface_id: 8
        before_merit: 4.2e-3
        after_merit: 1.8e-3
        improvement: 0.57
        iterations: 20
        status: converged
        coefficients:
          A4: -1.2e-05
          # ...
  opd_profiles:
    - surface_id: 1
      max_r: 14.96131269998368
      fields:
        - field_id: 0
          ring_radius: [0.83, 2.71, 4.58, 6.46, 8.13]
          opd: [-0.0085, -0.0109, -0.0112, 0.0023, 0.0406]
  warnings: []
```

Meaning of the ranking fields (see the method document for the exact formulas):

| Field | Meaning |
|---|---|
| `score` | composite score `w_com·E^common + w_uni·E^unique + w_fit·F + w_sens·H − w_conf·C − w_mfg·M − w_unstable·U` |
| `coverage` | fraction of the surface's cell OPD energy that an asphere could address |
| `common_energy` / `unique_energy` | shared (multi-field) vs single-field OPD energy fractions |
| `conflict` | weighted inter-field variance in shared cells (0..1) |
| `fit_quality` | R² of fitting the shared-cell common OPD to a radial asphere basis |
| `manufacturing_penalty` | base curvature magnitude and beam radius penalty (0..1) |
| `sensitivity_penalty` | the sensitivity term H (measured improvement, or the analytic proxy) |
| `coefficients` | fitted initial coefficients (conic + A4..A12) |
| `scaled_coefficients` | coefficients × `sag_scale` for safe embedding (the probe) |
| `calibrated_coefficients` | the embedded set recommended by the measured-response calibration (see below); falls back to `scaled_coefficients` when calibration is disabled |
| `sensitivity` | measured merit data: `base_merit`, `asphere_merit`, relative `improvement`, and `d_merit_d_coef` (per-coefficient ∂Merit/∂c_j, A4..A12); with calibration also `calibrated_scale`, `calibrated_merit`, `calibrated_improvement` |
| `warnings` | analysis notes, e.g. a removed r² defocus term, bounded coefficient, or a skipped fit |

`opd_profiles` holds the graph data behind the OPD-overlap comparison: per
candidate surface, per field, the weight-mean OPD vs footprint ring radius. A
closely overlapping set of field profiles means the fields share a wavefront
error the asphere can correct.

## Scale calibration

The fitted `coefficients` are an OPD-to-sag estimate whose **absolute
magnitude** is approximate (`dz ≈ −O/(n2−n1)`, normal incidence). A fixed
`sag_scale` damps it for safe embedding, but the "right" scale differs per
surface, and an oversized probe can overshoot (the measured OPD merit then
worsens). By default (`calibrate_scale: true`, requires `sensitivity_samples > 0`)
the embedded scale is derived **per surface from the measured ray-trace
response** instead of the fixed α:

1. The sensitivity pass already traces the merit `M(0)=base` (no asphere),
   `M(α)=asphere` at the probe scale α = `sag_scale`, and the per-coefficient
   derivatives `∂M/∂c_j`. The directional derivative along the fitted
   coefficients `D = Σ_j ∂M/∂c_j·c_j` is the local slope of the merit w.r.t.
   the scale.
2. `M(β)` is modelled by a quadratic through those three local data points and
   the minimizer `β*` proposed, clamped to `[α/4, 2α] ∩ [0.05, 1.0]`.
3. The proposal is **verified** by one extra merit trace; the scale with the
   lowest traced merit wins, with the probe α as the fallback (pick-min
   property: calibration never does worse than the fixed-α behaviour).
4. The chosen scale becomes `calibrated_scale`; `calibrated_coefficients` =
   `coefficients × calibrated_scale` is what `--validate` seeds and what the
   ranking's sensitivity term uses (`calibrated_improvement`, floored at 0 so
   an overshooting probe can never demote a genuinely aspherizable surface).

`--scale-probes` replaces the quadratic estimate with an explicit list of
scales to trace and verify (each is still checked against the probe). See
[asphere-candidates.md](methods/asphere-candidates.md) §7.6 for the algorithm
and a worked example. The OPD profiles (`opd_profiles`) are measured ray-trace
data and are unaffected by calibration.

## Pipelines

```sh
# Rank candidates and estimate initial coefficients (default: no validation)
rayweave asphere < lens.yaml | rayweave optimize > optimized.yaml

# Verify each fitted asphere against spot RMS (short DLS)
rayweave asphere --validate < lens.yaml | rayweave optimize > optimized.yaml

# Insert the top-ranked validated asphere, then evaluate and plot it
rayweave asphere --validate --apply < lens.yaml \
  | rayweave chief | rayweave trace | rayweave plot -o aspherized.svg
```

The `--apply` output replaces the chosen surface's `type` with
`asphere_polynomial`, sets `conic: 0`, and fills `coefficients` with the
DLS-solved values in every config, so the real spot before/after can be
compared directly. See `samples/asphere-demo.bash` for an end-to-end demo that
ranks, validates, applies and plots, using `samples/asphere-demo-init.yaml` as
its starting system.

## Notes

- A `chief` section with fields is required; without one the command exits with
  an error.
- Without an explicit stop, the grid centring uses the **dynamic pupil** (the
  in-lens crossing of each field's chief ray with field 0's chief ray), the same
  convention as `chief`. A heavily degraded starting system falls back to the
  position of the tightest fixed aperture.
- The fit is an approximation (`dz ≈ −O/(n2−n1)`, radius normalised to the
  footprint, ridge-regularised); it estimates a *start*, not a final design. Use
  the returned coefficients as `optimize` variables and re-optimise.
- Surfaces whose fit fails (degenerate index difference, no footprint) are
  skipped from the top-K selection, so the fitted set is genuinely aspherisable.
- The rank's sensitivity term is *measured*: the scaled asphere is inserted and
  the pupil grid re-traced, comparing weighted-RMS-OPD with and without it. Set
  `--sensitivity-samples 0` to skip this and use the analytic index-contrast
  proxy (faster, less informative).