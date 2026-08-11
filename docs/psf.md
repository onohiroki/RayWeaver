# `rayweave psf` — point-spread function via direct vector Huygens integration

The `psf` subcommand computes the point-spread function (PSF) on a **fixed flat
image plane** for each field and wavelength. It performs per-field polarized ray
tracing, samples the resulting wavefront on a reference surface (triangulated
for non-uniform area weights), and integrates it with a **direct vector Huygens
integral** — no FFT. The numerical method is described in
[methods/psf.md](methods/psf.md).

```
rayweave psf [--ref-surface N] [--psf-grid 64] [--psf-width W]
             [--num-rays 400] [--fields I1,I2,...] [--wavelengths W1,...]
             [--polarization S] [--yaml FILE] [--csv FILE] < system.yaml
```

It reads standard system YAML (a `chief` section is required for the fields),
and writes pipeline-compatible YAML with a lightweight `psf_results[]` summary
appended. The full intensity grids are written to `--yaml` / `--csv` files so
the pipeline stream stays small.

## Pipeline

```
per-field polarized ray tracing → non-uniform wavefront samples (Delaunay-
triangulated reference surface) → vector Huygens integral → PSF(x,y)
```

No FFT and no flat-pupil assumption: each field's pupil is generated with the
same dynamic-pupil / explicit-stop logic as `chief`, the wavefront is sampled on
the actual (curved, possibly vignetted) reference surface, and every image-plane
point is evaluated by directly coherently summing the secondary wavelets. This
remains robust for fisheye and other strongly non-paraxial systems where a
single flat exit pupil would break down.

## Options

| Flag | Description |
|---|---|
| `--ref-surface N` | reference surface ID for wavefront sampling (default: the last optical surface, i.e. the surface before the image plane) |
| `--psf-grid N` | image-plane pixels per side (default 64) |
| `--psf-width W` | evaluation half-width in mm (default: auto from the Airy disk and the geometric spot) |
| `--num-rays N` | pupil grid rays (default 400 ≈ 20×20 polar) |
| `--fields I1,I2,...` | field indices to compute (default: all fields in the `chief` section) |
| `--wavelengths W1,...` | wavelengths in mm (default: `chief.wavelengths`, else 587.56 nm) |
| `--polarization S` | input polarization: `RCP` (default) \| `LCP` \| `X` \| `Y` \| `RCP+LCP` (unpolarised average) |
| `--psf-workers N` | parallel workers for the Huygens integral and wavefront tracing (default: GOMAXPROCS) |
| `--yaml FILE` | write full structured data to FILE, one index-suffixed file per result (`FILE_0.yaml`, `FILE_1.yaml`, …) |
| `--csv FILE` | write a gnuplot `x,y,intensity` pm3d map to FILE, one index-suffixed file per result |
| `--config ID` | select config by id (multi-config mode) |
| `--glass-dir DIR` | AGF glass catalog directory |

Flags override the corresponding `psf:` YAML values, and the effective
(flag-won) values are written back into the output's `psf:` section;
`--glass-dir` is written back into `glass_catalog.directory` (CLI/YAML rule,
`psf` is the reference implementation).

## Polarization

The ray tracer accepts an arbitrary Jones vector. The default input is
**right-handed circular** (RCP, `(1, i)/√2`), which avoids biasing toward a
specific linear direction and is the natural reference case for
rotationally-symmetric systems.

| Value | Meaning |
|---|---|
| `RCP` | right circular `(1, i)/√2` — the default |
| `LCP` | left circular `(1, −i)/√2` |
| `X` | x-linear `(1, 0)` |
| `Y` | y-linear `(0, 1)` |
| `RCP+LCP` | compute both and average the intensities incoherently — the polarization-independent (unpolarised) PSF `½(h_RCP + h_LCP)` |

`RCP+LCP` averages **intensities**, never complex amplitudes, so no spurious
interference is introduced. The propagation at every surface applies the Fresnel
amplitudes to the s/p components of the field (with coating transmission), so
oblique-incidence and large-field polarization effects are included.

## Input

The system must carry a `chief` section so the fields (and optionally the stop
surface) are known. Without an explicit stop, each field's pupil is centred on
the **dynamic pupil** (the per-field chief-ray crossing), the same convention as
`chief`.

```yaml
chief:
  fields:
    - {angle: 0}
    - {angle: 24, direction: [0, 1]}
  wavelengths: [0.00058756, 0.00048613]   # optional
```

Optional configuration is supplied in a `psf:` section. All fields are optional;
unset values keep the defaults, and flags override them.

```yaml
psf:
  reference_surface: 7        # wavefront sampling surface (default: last optical surface)
  grid_size: 128              # image-plane pixels per side
  half_width: 0.02            # evaluation half-width mm (0 = auto)
  num_rays: 900               # pupil grid rays
  huygens_workers: 8          # parallel workers (0 = GOMAXPROCS)
  fields: [0, 1]              # field indices (default: all)
  wavelengths: [0.00058756]   # mm (default: chief wavelengths)
  polarization: "RCP+LCP"     # RCP | LCP | X | Y | RCP+LCP
```

## Output

The pipeline YAML appends a lightweight `psf_results[]` array, one entry per
(field, wavelength, polarization):

```yaml
psf_results:
  - field_index: 0
    field_angle: 0
    wavelength: 0.00058756
    polarization: RCP
    strehl_ratio: 0.958
    fwhm_x: 0.00408
    fwhm_y: 0.00408
    centroid_x: 2.5e-07
    centroid_y: -1.5e-06
    peak_value: 0.0175
    peak_x: -0.00030
    peak_y: 0.00030
    encircled_energy_50: 0.00214
    airy_radius: 0.00485
    grid_size: 64
    resolution: 0.000606
    total_rays: 400
    valid_rays: 400
    vignetted: 0
    output_file: psf_0.yaml     # only when --yaml was given
```

| Field | Meaning |
|---|---|
| `strehl_ratio` | peak intensity of the actual PSF divided by the peak of the diffraction-limited PSF computed from the **same** samples (a converging sphere to the window centre). A perfect system gives 1.0. |
| `fwhm_x` / `fwhm_y` | full width at half maximum through the peak, sub-pixel interpolated |
| `centroid_x` / `centroid_y` | intensity-weighted centroid of the sampled PSF |
| `peak_value` / `peak_x` / `peak_y` | peak of the intensity-normalised grid (Σ I·Δx·Δy = 1) |
| `encircled_energy_50` | radius enclosing 50 % of the energy, from the centroid |
| `airy_radius` | `0.61·λ/NA` with the image-space NA from the reference-surface footprint as seen from the focus |
| `total_rays` / `valid_rays` / `vignetted` | pupil grid rays, those reaching the reference surface, and those clipped by apertures |
| `output_file` | path of the full `--yaml` data file for this result |

### `--yaml` full structured data

One file per result, referenced by `output_file`. Contains the grid geometry
(`nx, ny, x0, y0, dx, dy`), the row-major `intensity` array, the complex field
components `ex_real/imag, ey_real/imag, ez_real/imag`, an `encircled_energy`
radius/fraction curve, the `wavefront` best-fit-sphere `rms_opd` / `pv_opd`, and
`samples` counts.

### `--csv` gnuplot map

One file per result with a `x,y,intensity` header and **a blank line between
rows** so gnuplot's pm3d recognises each row as a scan line:

```sh
gnuplot -e "set datafile separator ','; set pm3d map; \
  splot 'psf_0.csv' u 1:2:3 w pm3d"
```

## Pipelines

```sh
# PSF for every field at the default wavelength, RCP
rayweave psf < lens.yaml

# All fields, unpolarised average, full data to files
rayweave psf --polarization RCP+LCP --yaml psf.yaml --csv psf.csv < lens.yaml

# A single field, finer sampling for a reliable off-axis Strehl
rayweave psf --fields 2 --num-rays 1600 --psf-grid 128 < lens.yaml

# Pipe from chief (the chief_rays output is ignored; the grid is re-derived)
rayweave chief < lens.yaml | rayweave psf > psf-summary.yaml
```

## Notes

- A `chief` section with fields is required; without one the command exits with
  an error.
- The default reference surface is the **last optical surface**. Sampling the
  wavefront there and propagating to the fixed image plane means field curvature
  and defocus appear naturally in the PSF (they are not refocused away).
- The evaluation window is centred on the geometric spot centroid and sized to
  cover both the diffraction core and the geometric spot. Set `--psf-width` to
  override.
- For strongly aberrated fields the PSF is a coherent speckle pattern whose peak
  (and therefore Strehl) is sensitive to the pupil sampling; increase
  `--num-rays` for stable off-axis metrics. Near-diffraction-limited systems are
  stable at the default 400 rays.
- The wavefront `rms_opd`/`pv_opd` are referenced to the best-fit sphere
  (piston + tilt + defocus removed), the standard wavefront-aberration
  definition.
- Absolute intensities are not calibrated (no photometric scale); the PSF is
  energy-normalised over the sampled window.
