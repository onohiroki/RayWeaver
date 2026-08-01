# RayWeave — sample data

This directory contains sample optical system data and demo scripts for the
[RayWeave](https://github.com/hiroki/rayweaver) ray tracing toolkit.

## Source files (input data)

| File | Description |
|---|---|
| `us2645157.yaml` | Triplet-derivative lens from US patent 2,645,157 (MIT license, ©2014 Daniel J. Reiley). Converted from a ZMX file obtained from [lens-designs.com](https://www.lens-designs.com/). The original patent data was validated against [LensForge](https://www.ripplon.com/LensForge/) trace output (`lens-designs.com/US02645157-1-Trace*.txt`) — ray positions at every surface are consistent. |
| `ar-coating.yaml` | Single-layer MgF2 anti-reflection coating on N-SK16 glass, quarter-wave at 550 nm. |
| `dielectric-mirror.yaml` | 9-layer quarter-wave Bragg reflector (SiO2/TiO2) on glass, design wavelength 550 nm. |
| `glass-optimize-demo.yaml` | 3-lens 7-surface system with glass-model variables (nd/vd) intended for DLS optimisation. 3 fields (0°, 10°, 16°), 4 wavelengths (g/F/d/C). |
| `multi-config-zoom.yaml` | 3-config zoom demo. Uses `entrance_pupil_diameter` **equality** constraints (now supported), `edge_thickness` with the `surface2` back-surface field, and vignetting constraints. |
| `simple-zoom.yaml` | 3-config zoom with fuzzy image-height / incident-angle constraints and `ray_paths` (render-only metadata). |
| `asphere-optimize.yaml` | Singlet whose first surface is `asphere_polynomial`; optimizes `conic` and `a4`/`a6` coefficients (asphere variables). |
| `doublegauss-init.yaml` | 6-element symmetric double-Gauss starting point for a 35mm-format 50 mm f/2.8 standard lens. The structure was synthesised by an AI agent (see `design/REPORT_designs.md` appendix) via curvature-scale search to hit EFL ≈ 50 mm, then handed to DLS optimisation. The optimised result reaches on-axis RMS < 0.1 mm (see `doublegauss-demo.bash`). |
| `run-demo.bash` | End-to-end demo script using `us2645157.yaml`. |
| `glass-optimize-demo.bash` | Glass optimisation demo using `glass-optimize-demo.yaml`. |
| `scale-demo.bash` | Demonstrates the `scale` subcommand: resizes the 25 mm triplet to a 50 mm standard (EFL exact, f/# preserved). |
| `doublegauss-demo.bash` | AI-assisted design demonstration: optimises the 6-element double-Gauss with 256 rays / 500 iterations (36 variables including glass nd/vd). Reports spot RMS / EFL / f-number / distortion before and after, draws raytrace diagrams, and checks the on-axis RMS stays below 0.1 mm. |
| `asphere-optimize-demo.bash` | Asphere-optimisation demo using `asphere-optimize.yaml`: optimizes `conic`/`a4`/`a6` and reports the coefficients before/after in the result file. |

## External dependencies

The demo scripts use `python3` (with `PyYAML`) for YAML parsing and comparison;
`yq`, `csvtk` and `bc` are **not** required. `gnuplot` is used only for the
optional PNG renderings (spot diagrams, aberration graphs): if it is not
installed, the scripts print a message and skip those renderings.

## Optimisation-related notes (current syntax)

- Multiple `equality` constraints are supported (satisfiable targets converge;
  an unreachable target is reported with a WARNING instead of freezing).
- `optimization.aperture_margin` is clamped to ≥ 1.0 (smaller values stall DLS).
- `configs[].ray_paths` is render-only metadata; the optimizer ignores it.
- `edge_thickness` constraints specify the back surface explicitly with
  `surface2` (the old `target`-as-back-surface usage is gone).
- Asphere variables: `conic`, `a4`/`a6`/`a8`/`a10`/`a12` (aliases
  `coefficient_0`…`coefficient_4`).
- `chief --clear-aperture --shrink` sizes diameters down to the beam footprint.
- `optimize --verbose` / `--log` also emits a per-term `{"event":"breakdown"}` line.

## Generated files

### Output of `run-demo.bash`

| File | Description |
|---|---|
| `us2645157-chief-result.yaml` | Chief-ray computation result with pupil grid and spot statistics. |
| `us2645157.svg` | SVG cross-section raytrace diagram. |

### Output of `doublegauss-demo.bash`

| File | Description |
|---|---|
| `doublegauss-result.yaml` | Optimised 6-element double-Gauss system YAML. |
| `doublegauss-log.jsonl` | Per-iteration log plus `{"event":"breakdown"}` merit decomposition (256 rays × 500 iters). |
| `doublegauss-demo-result.txt` | Spot RMS, EFL, f-number, and distortion before/after, plus threshold check (on-axis < 0.1 mm). |
| `doublegauss-init.png` | Raytrace diagram before optimisation. |
| `doublegauss-opt.png` | Raytrace diagram after optimisation. |

### Output of `glass-optimize-demo.bash`

| File | Description |
|---|---|
| `glass-optimize-result.yaml` | Optimised system YAML (surface curvatures, glass nd/vd, thicknesses). |
| `glass-optimize-log.jsonl` | Per-iteration optimisation log (merit, status, variable deltas). |
| `glass-optimize-init.svg` | SVG raytrace diagram before optimisation. |
| `glass-optimize-opt.svg` | SVG raytrace diagram after optimisation. |
| `glass-spot-f0.png` | Spot diagram for field 0° (on-axis), before vs after, 4-wavelength overlay. |

### Output of `asphere-optimize-demo.bash`

| File | Description |
|---|---|
| `asphere-optimize-result.yaml` | Optimised system YAML (asphere coefficients, curvatures). |
| `asphere-optimize-log.jsonl` | Per-iteration optimisation log (merit, status) plus a `{"event":"breakdown"}` line with the per-term merit contributions. |
| `asphere-optimize-demo-result.txt` | Two-stage comparison: asphere coefficients (before / spherical-opt / asphere-opt), spot RMS and OPD RMS (before / spherical / asphere), and threshold check. |
| `asphere-optimize-init.png` | Raytrace diagram before optimisation. |
| `asphere-optimize-opt.png` | Raytrace diagram after optimisation. |

### Output of `scale-demo.bash`

| File | Description |
|---|---|
| `us2645157-scaled50.yaml` | The 25 mm triplet uniformly scaled to EFL = 50 mm. |
| `us2645157-scaled50.svg` | Raytrace diagram of the scaled system. |
| `scale-demo-result.txt` | EFL / f-number before and after scaling. |
| `glass-spot-f1.png` | Spot diagram for field 1 (10°), before vs after, 4-wavelength overlay. |
| `glass-spot-f2.png` | Spot diagram for field 2 (16°), before vs after, 4-wavelength overlay. |

## Run the demos

```sh
# Basic raytrace and demo
./rayweave trace < samples/us2645157.yaml
bash samples/run-demo.bash

# Glass optimisation demo (runs DLS, generates SVGs + spot diagrams)
bash samples/glass-optimize-demo.bash
```

## What `run-demo.bash` does

1. **Chief ray computation** — `rayweave chief` finds the chief ray (spot centroid) and a hexagonal pupil grid for each field (0°, 16°, 24°). Result saved as `us2645157-chief-result.yaml`.

2. **Ray-path traces** — `rayweave trace` shows intersection coordinates at every surface in a Markdown table, both on the original YAML and on the chief-result YAML.

3. **Paraxial analysis** — `rayweave paraxial` computes first-order properties (EFL, principal points, f/#, entrance/exit pupils) with and without chief-ray data.

4. **SVG raytrace diagram** — `rayweave chief --clear-aperture --marginal-rays` determines clear apertures from the pupil grid and adds marginal rays; `rayweave trace` traces them; `rayweave plot` draws the SVG.

5. **TMM coating analysis** — `rayweave tmm` computes reflectance/transmittance for the AR coating and the dielectric mirror via the transfer-matrix method.

## What `glass-optimize-demo.bash` does

1. **DLS optimisation** — `rayweave optimize` runs damped least-squares (DLS) on the 3-lens system, varying curvatures (6), glass nd/vd pairs (3 × 2), and air gaps (2) — 14 variables total. Log saved as `glass-optimize-log.jsonl`.

2. **Results summary** — Displays before/after glass values, surface curvatures and diameters, plus diffraction-limit comparison (Airy disk radius vs RMS spot radius).

3. **SVG raytrace diagrams** — Two SVG diagrams (`init` and `opt`) generated via `chief --clear-aperture | chief --marginal-rays | trace | plot`.

4. **Multi-wavelength spot diagrams** — For each of 4 wavelengths (g/F/d/C), `rayweave chief --wl <wl>` traces a hexagonal pupil grid. Grid-point `image_x`/`image_y` coordinates are extracted via yq and rendered by gnuplot as a before-vs-after comparison with colour-coded wavelength overlay.

## Notes

- All units are millimetres (thicknesses, radii, coordinates, wavelengths).
- Surface 0 (object plane) is implicit.
- In `us2645157.yaml` the stop is on surface 5. ZEMAX DIAM values are semi-diameters; the YAML stores full diameters.
- The glass optimisation demo starts with deliberately wrong glass values (model2 and model3 are swapped) to demonstrate DLS convergence.