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
| `run-demo.bash` | End-to-end demo script using `us2645157.yaml`. |
| `glass-optimize-demo.bash` | Glass optimisation demo using `glass-optimize-demo.yaml`. |

## Generated files

### Output of `run-demo.bash`

| File | Description |
|---|---|
| `us2645157-chief-result.yaml` | Chief-ray computation result with pupil grid and spot statistics. |
| `us2645157.svg` | SVG cross-section raytrace diagram. |

### Output of `glass-optimize-demo.bash`

| File | Description |
|---|---|
| `glass-optimize-result.yaml` | Optimised system YAML (surface curvatures, glass nd/vd, thicknesses). |
| `glass-optimize-log.jsonl` | Per-iteration optimisation log (merit, status, variable deltas). |
| `glass-optimize-init.svg` | SVG raytrace diagram before optimisation. |
| `glass-optimize-opt.svg` | SVG raytrace diagram after optimisation. |
| `glass-spot-f0.png` | Spot diagram for field 0° (on-axis), before vs after, 4-wavelength overlay. |
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