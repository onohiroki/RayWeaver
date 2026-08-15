# RayWeave — sample data

This directory contains sample optical system data and demo scripts for the
[RayWeave](https://github.com/hiroki/rayweaver) ray tracing toolkit.

## Source files (input data)

| File | Description |
|---|---|
| `us2645157.yaml` | Triplet-derivative lens from US patent 2,645,157 (MIT license, ©2014 Daniel J. Reiley). Converted from a ZMX file obtained from [lens-designs.com](https://www.lens-designs.com/). The original patent data was validated against [LensForge](https://www.ripplon.com/LensForge/) trace output (`lens-designs.com/US02645157-1-Trace*.txt`) — ray positions at every surface are consistent. |
| `us2645157-degraded.yaml` | Degraded US2645157 triplet (curvatures distorted so every field is badly out of focus) with the **off-axis spot merit kinds** (`spot_rms_t`/`spot_rms_s`/`spot_rms_worst` on the 16° field, `spot_rms_worst`/`spot_rms_weighted`/`spot_ee_radius` on the 24° field). Input of `optimize-demo.bash`. |
| `us2645157-degraded-spotrms.yaml` | The same degraded triplet with the plain `spot_rms`-only merit — the "old" baseline that `optimize-demo.bash` compares the off-axis merit against. |
| `escape-demo.yaml` | Degraded US2645157 triplet (auto-aperture surfaces) with the escape-function merit: `spot_rms` + `spot_rms_worst` per field (weights 2.0/1.0/0.5 on `spot_rms` and 0.5/0.3 on `spot_rms_worst` for 16°/24°) — a pure **spot** merit (no wavefront term). The full off-axis set (`spot_rms_t`/`_s`/`spot_rms_weighted`/`spot_ee_radius`) collapses the escape landscape to a single basin, so it is kept out of the escape merit to preserve multiple local minima (5 found). The escape then robustly finds the well-corrected landscape (0/16/24° Strehl ~0.99/~0.11/~0.63: the 24° outer field is already ≥ 0.5). `escape-demo.bash` DLS-refines that best with a `wavefront_astigmatism` merit (w16=14000, w24=6500, 24° spot terms w=0.11, 512-ray grid) so every field reaches best-focus Strehl ≥ 0.5 (~0.97/~0.51/~0.63). |
| `ar-coating.yaml` | Single-layer MgF2 anti-reflection coating on N-SK16 glass, quarter-wave at 550 nm. |
| `dielectric-mirror.yaml` | 9-layer quarter-wave Bragg reflector (SiO2/TiO2) on glass, design wavelength 550 nm. |
| `glass-optimize-demo.yaml` | 3-lens 7-surface system with glass-model variables (nd/vd) intended for DLS optimisation. 3 fields (0°, 10°, 16°), 4 wavelengths (g/F/d/C). |
| `multi-config-zoom.yaml` | 3-config zoom demo. Uses `entrance_pupil_diameter` **equality** constraints (now supported), `edge_thickness` with the `surface2` back-surface field, and vignetting constraints. |
| `simple-zoom.yaml` | 3-config zoom with fuzzy image-height / incident-angle constraints and `ray_paths` (render-only metadata). |
| `asphere-optimize.yaml` | Singlet whose first surface is `asphere_polynomial`; optimizes `conic` and `a4`/`a6` coefficients (asphere variables). |
| `doublegauss-init.yaml` | 6-element symmetric double-Gauss starting point for a 35mm-format 50 mm f/2.8 standard lens. The structure was synthesised by an AI agent (see `design/REPORT_designs.md` appendix) via curvature-scale search to hit EFL ≈ 50 mm, then handed to DLS optimisation. The optimised result reaches on-axis RMS < 0.1 mm (see `doublegauss-demo.bash`). It also carries an `optimization.escape` section so the same file drives the escape-function demo (`escape-demo.bash --lens doublegauss`); the normal `doublegauss-demo.bash` ignores it. The escape merit is the plain spot merit (`spot_rms` per field, weights 2.0/1.0/1.0/0.5) plus `lateral_color` and `opd_rms` — the off-axis spot kinds (`spot_rms_t`/`_s`/`spot_rms_worst`/`spot_rms_weighted`/`spot_ee_radius`) collapse the escape landscape to a single basin, so they are kept out of the escape merit to preserve multiple local minima. |
| `doublegauss-ghost.yaml` | Ghost-ray trace sample on the optimised double-Gauss. Uses the surface-sequence encoding of Ono et al. (Optical Review 32:402-411): each ray carries an ordered surface-ID list; a direction reversal in the list means reflection. One ghost path `[0,1,2,3,4,3,2,3,4,...,14]` (reflect at surface 4, reversed refraction through surface 3, reflect at surface 2) plus a normal reference ray, and a `chief` section for re-adjusting the lens effective diameters. See `ghost-demo.bash`. |
| `run-demo.bash` | End-to-end demo script using `us2645157.yaml`. |
| `optimize-demo.bash` | DLS optimisation of the degraded US2645157 triplet, comparing the `spot_rms`-only merit against the off-axis spot merit kinds: per-field spot RMS before/old/new (chief), the new-merit final values from the `--log` breakdown, and PNG diagrams. |
| `psf-mtf-demo.yaml` | PSF/OTF/MTF demo input: the US2645157 triplet after the escape-function global optimiser **plus a DLS re-optimisation with the 16° `wavefront_astigmatism` merit (w16=14000) and the 24° spot terms at w24=0.05** (the same recipe `escape-demo.bash` runs, whose current refinement uses w24=0.11). Cleaned copy — only the sections `psf` consumes (glass_catalog / configs / chief) are kept. Three fields (0/16/24 deg), reference surface 8; best-focus evaluation is the default so the aberrated off-axis fields are measured at their own focus. Reports MTF up to `psf.mtf_config.max_frequency` (200 c/mm). The 0° field is nearly diffraction-limited (Strehl ≈ 0.99, MTF50 ≈ 104 c/mm); the 16° field reaches Strehl ≈ 0.62 (MTF50 ≈ 70 c/mm); the 24° field stays at Strehl ≈ 0.59 (MTF50 ≈ 51 c/mm) — a monotone 0° > 16° > 24° profile meeting 0.9/0.6/0.5. |
| `psf-mtf-demo.bash` | PSF + OTF + MTF demo. Default lens `psf-mtf-demo.yaml` (0/16/24 deg): a single `rayweave psf` run (RCP+LCP, `--max-freq 200` cap, best-focus default) computes the image-plane PSF and the FFT-derived OTF/MTF per field, then prints a Strehl / FWHM / EE50 / Airy / MTF50-30-10 table (`<stem>-result.txt`) and draws per-field pm3d maps, a radial-profile overlay, and a sagittal/tangential MTF-overlay PNG. `--lens doublegauss` (or any YAML path) switches the lens — the MTF cap keeps working via `--max-freq`, which overrides `psf.mtf_config.max_frequency` even when the chosen YAML has no `psf:` section. |
| `glass-optimize-demo.bash` | Glass optimisation demo using `glass-optimize-demo.yaml`. |
| `scale-demo.bash` | Demonstrates the `scale` subcommand: resizes the 25 mm triplet to a 50 mm standard (EFL exact, f/# preserved). |
| `doublegauss-demo.bash` | AI-assisted design demonstration: optimises the 6-element double-Gauss with 256 rays / 500 iterations (36 variables including glass nd/vd). Reports spot RMS / EFL / f-number / distortion before and after, draws raytrace diagrams, and checks the on-axis RMS stays below 0.1 mm. |
| `asphere-optimize-demo.bash` | Asphere-optimisation demo using `asphere-optimize.yaml`: optimizes `conic`/`a4`/`a6` and reports the coefficients before/after in the result file. |
| `schmidt-flattener.yaml` | Folded Schmidt camera: corrector plate + spherical primary + 2-element field flattener. Demonstrates the **fold model** — positive thicknesses only, the fold is carried by the primary's `decenter: [{tilt: [0, 180, 0], scope: both}]` plus a top-level `reflect: true`. Physical Z: corrector/stop at 0, primary at 800, flat sensor at 400 (EFL≈386, F/1.93, D=200). |
| `schmidt-lensless.yaml` | Folded spherical primary alone (no corrector plate), showing the fold model geometry; the uncorrected mirror leaves large spherical-aberration spots. |
| `schmidt-optimize.yaml` | DLS optimisation of the folded Schmidt: corrector asphere (a4/a6) + field-flattener curvatures against spot RMS (4 fields). |
| `schmidt-demo.bash` | Folded-Schmidt demo: chief rays, per-field spot RMS, paraxial analysis (EFL / f/# / pupil / track), and SVG/PNG raytrace diagrams. |
| `ghost-demo.bash` | Ghost-ray demo on the optimised double-Gauss (`doublegauss-ghost.yaml`): re-adjusts the lens effective diameters with `chief --clear-aperture --shrink`, traces the ghost path and a normal reference ray through the re-sized system, prints the per-surface interaction / Fresnel-intensity table with the accumulated ghost intensity, and draws an SVG diagram. |
| `escape-demo.bash` | Escape-function global-optimisation demo. Default lens: degraded US2645157 triplet (`escape-demo.yaml`); `--lens doublegauss` runs the escape optimiser on `doublegauss-init.yaml` (much slower — 36 variables). Lists every discovered minimum with its `features[].element_powers` fingerprint, extracts one with `escape extract`, then **DLS-refines the escape best** with a `wavefront_astigmatism` merit (w16=14000, w24=6500, 24° spot terms w=0.11) so every field reaches a best-focus Strehl ≥ 0.5, prints a per-field **PSF verification table** (RCP+LCP, d line, best-focus) gated on all ≥ 0.5, and draws the initial/refined diagrams. |

## External dependencies

The demo scripts depend only on the `rayweave` binary (built with `go build
-o rayweave ./cmd/rayweave` from the repository root). YAML/JSONL parsing and
numeric comparison use the built-in `rayweave query` subcommand (see
`docs/query.md`). `gnuplot` is used only for the optional PNG renderings
(spot diagrams, aberration graphs): if it is not installed, the scripts print
a message and skip those renderings.

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
- The `optimize` output YAML gains an `opt_results.constraints` block: the final
  measured `value` and `residual` of every active constraint (e.g. the
  `vignetting_factor` the DLS constraint enforced). The multi-config and glass
  demo gates check the optimizer-reported vignetting factor; the `chief`-based
  vignetting printed in the result files is a reference measurement with a
  different pupil-grid sampling.

## Generated files

### Output of `run-demo.bash`

| File | Description |
|---|---|
| `us2645157-chief-result.yaml` | Chief-ray computation result with pupil grid and spot statistics. |
| `us2645157.svg` | SVG cross-section raytrace diagram. |

### Output of `optimize-demo.bash`

| File | Description |
|---|---|
| `optimize-demo-old.yaml` | Optimised system with the `spot_rms`-only merit (baseline). |
| `optimize-demo-new.yaml` | Optimised system with the off-axis spot merit kinds. |
| `optimize-demo-old.log` / `optimize-demo-new.log` | DLS logs; the new one ends with the per-term `{"event":"breakdown"}` for the new merit. |
| `optimize-demo-result.txt` | Per-field spot RMS before / old / new (chief measurement, same sampling), the new-merit final values (RMS_T/S/worst, weighted RMS, EE80), and the on-axis threshold check. |
| `optimize-demo-init.png` / `-old.png` / `-new.png` | Raytrace diagrams of the degraded start and the two optima. |

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

### Output of `schmidt-demo.bash`

| File | Description |
|---|---|
| `schmidt-chief-result.yaml` | Chief-ray computation with pupil grid and per-field spot statistics. |
| `schmidt-trace-result.yaml` | Traced rays with per-field spot RMS. |
| `schmidt-demo-result.txt` | Per-field spot RMS and paraxial summary. |
| `schmidt.svg` / `schmidt.png` | Folded-layout raytrace diagram (beam to primary at 800, back to sensor at 400). |

### Output of `ghost-demo.bash`

| File | Description |
|---|---|
| `doublegauss-ghost-trace-result.yaml` | System with re-adjusted effective diameters plus the traced rays: the ghost path and the normal reference path, with per-surface interaction, direction, OPL and Fresnel intensity. |
| `ghost-demo-result.txt` | Per-surface trace table for both rays, ghost relative intensity (product of the two Fresnel reflectances), and cumulative intensity. |
| `doublegauss-ghost.svg` | Raytrace diagram of the re-sized lens showing the ghost ray doubling back through surface 3. |

## Run the demos

The demo scripts are location-independent: run them from the repo root, from
`cd samples`, or from a copied directory (the input data files must live next
to the script). Output files are always written next to the script.

The `rayweave` binary is located in this order: the `RAYWEAVE` environment
variable → `rayweave` next to the script → `../rayweave` (repo root) →
`rayweave` on `PATH`.

```sh
# Basic raytrace and demo (from the repo root)
./rayweave trace < samples/us2645157.yaml
bash samples/run-demo.bash

# Also works from inside samples/ (uses ../rayweave) ...
cd samples && bash run-demo.bash

# ... or with the binary on PATH, e.g. after `go install ./cmd/rayweave`
# or `PATH=$PWD:$PATH bash run-demo.bash`.

# Point at a specific binary explicitly
RAYWEAVE=/path/to/rayweave bash samples/run-demo.bash

# Glass optimisation demo (runs DLS, generates SVGs + spot diagrams)
bash samples/glass-optimize-demo.bash

# Optimise demo: old (spot_rms-only) vs new (off-axis spot merit kinds)
bash samples/optimize-demo.bash

# Folded Schmidt demo (chief + trace + paraxial + diagrams)
bash samples/schmidt-demo.bash

# Ghost-ray demo on the optimised double-Gauss (trace + intensity table + SVG)
bash samples/ghost-demo.bash
```

## What `run-demo.bash` does

1. **Chief ray computation** — `rayweave chief` finds the chief ray (spot centroid) and a hexagonal pupil grid for each field (0°, 16°, 24°). Result saved as `us2645157-chief-result.yaml`.

2. **Ray-path traces** — `rayweave trace` shows intersection coordinates at every surface in a Markdown table, both on the original YAML and on the chief-result YAML.

3. **Paraxial analysis** — `rayweave paraxial` computes first-order properties (EFL, principal points, f/#, entrance/exit pupils) with and without chief-ray data.

4. **SVG raytrace diagram** — `rayweave chief --clear-aperture --marginal-rays` determines clear apertures from the pupil grid and adds marginal rays; `rayweave trace` traces them; `rayweave plot` draws the SVG.

5. **TMM coating analysis** — `rayweave tmm` computes reflectance/transmittance for the AR coating and the dielectric mirror via the transfer-matrix method.

## What `optimize-demo.bash` does

1. **Two DLS optimisations** — `rayweave optimize` runs on the degraded triplet
   twice: once with the `spot_rms`-only merit (`us2645157-degraded-spotrms.yaml`,
   the "old" baseline) and once with the off-axis spot merit kinds
   (`us2645157-degraded.yaml`, the "new" merit). Logs are saved as
   `optimize-demo-old.log` / `optimize-demo-new.log`.

2. **Per-field spot RMS comparison** — `rayweave chief` re-measures the
   geometric RMS spot radius per field (0/16/24°) on the degraded start, the old
   optimum and the new optimum (same pupil-grid sampling), and writes
   `optimize-demo-result.txt` with the before/old/new table and the old→new
   improvement.

3. **New-merit breakdown** — the new run's `breakdown` event is read via
   `rayweave query --jsonl`, giving the final values of `spot_rms_t`/`spot_rms_s`/
   `spot_rms_worst` (16°), `spot_rms_worst`/`spot_rms_weighted`/`spot_ee_radius`
   (24°) at the optimum.

4. **PNG diagrams** — `chief --clear-aperture --ray-fan | chief --marginal-rays
   | trace | plot` draws the degraded start, the old optimum and the new optimum.

## What `glass-optimize-demo.bash` does1. **DLS optimisation** — `rayweave optimize` runs damped least-squares (DLS) on the 3-lens system, varying curvatures (6), glass nd/vd pairs (3 × 2), and air gaps (2) — 14 variables total. Log saved as `glass-optimize-log.jsonl`.

2. **Results summary** — Displays before/after glass values, surface curvatures and diameters, plus diffraction-limit comparison (Airy disk radius vs RMS spot radius).

3. **SVG raytrace diagrams** — Two SVG diagrams (`init` and `opt`) generated via `chief --clear-aperture | chief --marginal-rays | trace | plot`.

4. **Multi-wavelength spot diagrams** — For each of 4 wavelengths (g/F/d/C), `rayweave chief --wl <wl>` traces a hexagonal pupil grid. Grid-point `image_x`/`image_y` coordinates are extracted with `rayweave query --csv` and rendered by gnuplot as a before-vs-after comparison with colour-coded wavelength overlay.

## Notes

- All units are millimetres (thicknesses, radii, coordinates, wavelengths).
- Surface 0 (object plane) is implicit.
- In `us2645157.yaml` the stop is on surface 5. ZEMAX DIAM values are semi-diameters; the YAML stores full diameters.
- The glass optimisation demo starts with deliberately wrong glass values (model2 and model3 are swapped) to demonstrate DLS convergence.
- Ghost paths are expressed as explicit surface-ID sequences in `rays[].path` (and `chief.fields[].path`): a direction reversal in the sequence means reflection at that surface, a continuous ascent/descent means refraction. Backward (reversed-direction) refraction through a surface uses the physically correct incident/emergent media; path-encoded reflections at lens surfaces report the Fresnel reflectance (ideal `intensity = 1.0` is reserved for fold-mirror surfaces).