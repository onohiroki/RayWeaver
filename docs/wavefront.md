# `rayweave wavefront` — wavefront analysis, Zernike decomposition, best focus

The `wavefront` subcommand analyses the wavefront on a reference surface
(default: the last optical surface) for each field, wavelength and
polarization. It produces three complementary descriptions:

1. **Paraboloid fit** — the least-squares quadratic
   `P(x,y) = a·x² + b·y² + c·xy + d·x + e·y + f` to the sampled OPD, with the
   derived low-order magnitudes (defocus, astigmatism, tilt).
2. **Best-fit sphere** — the reference sphere through the vertex, whose center
   (focus) is found by minimizing the **geometric spot RMS** along the
   image-plane normal.
3. **Stabilized Fringe-Zernike** — the decomposition of the OPD residual after
   the low-order terms (piston, tilt, defocus, astigmatism) are removed, so
   off-axis coefficients stay stable and meaningful.

With `--best-focus`, the per-field focus shifts are combined by a weighted
average (`uniform` or `custom` weights) and applied to the output configs'
image-plane decenter Z shift, so the pipeline can be piped straight into `psf`
(or `trace`/`plot`) for a best-focus PSF.

```
rayweave wavefront [--ref-surface N] [--num-rays 400] [--fields I1,I2,...]
             [--wavelengths W1,...] [--polarization S] [--zernike-order N]
             [--wavefront-workers N] [--map-grid 64]
             [--best-focus] [--focus-weight uniform|custom]
             [--focus-weights W1,...] [--output-shifted-lens FILE]
             [--yaml FILE] [--csv FILE] < pipeline.yaml
```

It reads pipeline YAML (a `chief` section is required for the fields) and writes
pipeline-compatible YAML with a lightweight `wavefront_result` appended
(coefficients only — the maps go to `--yaml`/`--csv` files).

## Pipeline

```
per-field polarized ray tracing → OPD referenced to the best-focus point →
paraboloid fit (always) → best-focus sphere (geometric spot RMS) →
stabilized Fringe-Zernike on the low-order-removed residual → statistics
```

The OPD at each sampled point is `OPL + n·|P − Fbest|` referenced to the
**best-focus point** `Fbest` (the best-fit-sphere center found by minimizing the
geometric spot RMS, so the reference follows the true refocus even without
`--best-focus`). Angle fields are launched from the wavefront plane
perpendicular to the ray direction, so their OPL carries no launch-geometry
tilt; referencing to the best-focus point and removing the low-order terms
yields the standard wavefront-aberration definition (the statistics
`rms`/`pv`/`strehl` agree with `psf --best-focus`'s `rms_opd`/`pv_opd` and
`strehl_ratio`).

## Options

| Flag | Description |
|---|---|
| `--ref-surface N` | reference surface ID for wavefront sampling (default: the last optical surface) |
| `--num-rays N` | entrance-pupil grid rays per field (default 400) |
| `--fields I1,I2,...` | field indices to analyse (default: all chief fields) |
| `--wavelengths W1,...` | wavelengths in mm (default: selected config wavelengths, else reference wavelength) |
| `--polarization S` | input polarization: `RCP` (default) \| `LCP` \| `X` \| `Y` \| `RCP+LCP` |
| `--zernike-order N` | highest Fringe Zernike index to fit (default 15) |
| `--wavefront-workers N` | per-field task parallelism (default: GOMAXPROCS) |
| `--map-grid N` | wavefront-map resolution per side for `--csv` (default 64) |
| `--best-focus` | compute the weighted best image-plane shift and apply it to the output configs' image-plane decenter |
| `--focus-weight T` | best-focus weighting: `uniform` (default) \| `custom` |
| `--focus-weights W1,...` | per-field weights when `--focus-weight custom` |
| `--output-shifted-lens FILE` | write the shifted lens document to FILE |
| `--yaml FILE` | write full structured data (scattered samples + interpolated OPD map) to FILE, one index-suffixed file per result |
| `--csv FILE` | write a gnuplot `x,y,opd` wavefront map to FILE, one index-suffixed file per result |
| `--config ID` | select config by id (multi-config mode) |
| `--glass-dir DIR` | AGF glass catalog directory |

## Input YAML — `wavefront` section

All fields are optional; CLI flags override them and the effective values are
written back into the output's `wavefront:` section (the CLI/YAML rule).

```yaml
wavefront:
  reference_surface: 7
  num_rays: 400
  fields: [0, 1, 2]
  wavelengths: [0.00058756]
  polarization: "RCP"
  zernike_max_order: 15
  workers: 8
  map_grid: 64
  best_focus:
    enabled: true
    weight_type: "uniform"     # uniform | custom
    custom_weights: []         # required when weight_type: custom
    output_shifted_lens: "shifted.yaml"
```

## Output

The pipeline YAML carries `wavefront_result:` with per-field entries:

```yaml
wavefront_result:
  fields:
    - field_index: 0
      field_angle: 0
      wavelength: 0.00058756
      polarization: RCP
      paraboloid:
        x2: 2.33e-05      # a
        y2: 2.30e-05      # b
        xy: 4.74e-08      # c
        x: 1.23e-08       # d
        y: 4.38e-11       # e
        constant: 132.05  # f (piston, OPL units)
        defocus: 2.31e-05     # (a+b)/2
        astigmatism: 1.46e-07 # sqrt(((a-b)/2)² + (c/2)²)
        tilt: 1.23e-08        # sqrt(d²+e²)
        rms_residual: 8.90e-06
      best_fit_sphere:
        radius: 21.378     # axial focus distance from the reference surface (mm)
        center: [0, 0, 21.378]
        rms_residual: 8.90e-06
      zernike:
        basis: Fringe
        max_order: 15
        removed_terms: [1, 2, 3, 4, 5, 6]
        terms:
          - {index: 9, name: primary spherical, coefficient: -1.94e-05}
        rms_residual: 4.2e-06
      statistics:
        rms: 8.90e-06
        pv: 3.03e-05
        strehl: 0.991
      samples: {total: 400, valid: 385, missed: 15}
      output_file: wf_0.yaml   # when --yaml given
  best_focus:                  # when --best-focus given
    weight_type: uniform
    per_field:
      - {field_index: 0, shift_wavelengths: 19.5, shift_mm: 0.0114, weight: 1}
      - ...
    weighted_average: {shift_wavelengths: 115.1, shift_mm: 0.0676}
    shifted_lens: {shift_mm: 0.0676, output_file: shifted.yaml}
```

All OPD/RMS/PV values are in mm. `shift_wavelengths = shift_mm / λ_ref`.

### Best-focus image-plane shift

Each field's best-focus shift δ minimizes the intensity-weighted geometric
spot RMS of the beam propagated to the image plane moved by δ. The weighted
average is applied to the **image plane's last decenter Z shift** in every
target config (`--config` selects one config; otherwise all configs get the
shift recomputed from their own image distance). The shifted lens can be
written standalone with `--output-shifted-lens FILE`.

```
rayweave chief < lens.yaml | rayweave wavefront --best-focus | rayweave psf
```

computes the PSF at the best-focus image plane in one pipeline.

## Notes

- The statistics `rms`/`pv`/`strehl` are referenced to the best-fit sphere
  (piston + tilt + defocus removed, **astigmatism retained**), matching `psf
  --best-focus`'s `rms_opd`/`pv_opd` and `strehl_ratio`. `strehl` is the exact
  peak ratio `|<e^{i(2π/λ)W}>|²` — the pupil-area-weighted coherent average of
  the residual wavefront `W` — which stays meaningful for residual RMS beyond
  the Maréchal limit (~0.2 λ) where the `exp(−(2πσ/λ)²)` approximation collapses
  towards zero. The paraboloid's `rms_residual` additionally removes astigmatism
  (higher-order only).
- For angle fields, rays are launched from the wavefront plane perpendicular to
  the ray direction, so the OPL carries no launch-geometry tilt (the linear
  `Δpx·sinθ` ramp previously present in angle-field OPL is gone at the source).
- Strongly aberrated off-axis fields give large coma/astigmatism and low
  Strehl; raise `--num-rays` for stable off-axis coefficients.
