# RayWeave — sample data

This directory contains sample optical system data and demo scripts for the
[RayWeave](https://github.com/hiroki/rayweaver) ray tracing toolkit.

## Source files (input data)

| File | Description |
|---|---|
| `us2645157.yaml` | Triplet-derivative lens from US patent 2,645,157 (MIT license, ©2014 Daniel J. Reiley). Converted from a ZMX file obtained from [lens-designs.com](https://www.lens-designs.com/). The original patent data was validated against [LensForge](https://www.ripplon.com/LensForge/) trace output (`lens-designs.com/US02645157-1-Trace*.txt`) — ray positions at every surface are consistent. |
| `us2645157-degraded.yaml` | Degraded US2645157 triplet (curvatures distorted so every field is badly out of focus) with the **off-axis spot merit kinds** (`spot_rms_t`/`spot_rms_s`/`spot_rms_worst` on the 16° field, `spot_rms_worst`/`spot_rms_weighted`/`spot_ee_radius` on the 24° field). Input of `optimize-demo.bash`. |
| `us2645157-degraded-spotrms.yaml` | The same degraded triplet with the plain `spot_rms`-only merit — the "old" baseline that `optimize-demo.bash` compares the off-axis merit against. |
| `optimize-demo.yaml` | SK18/SF12 triplet with a declared curvature-variable `optimization` section and a `weighted_sum` / `spot_rms` merit. Kept as an input sample; no demo script reads it (`optimize-demo.bash` uses `us2645157-degraded*.yaml`). |
| `escape-demo.yaml` | Degraded US2645157 triplet (auto-aperture surfaces) with the escape-function merit: `spot_rms` + `spot_rms_worst` per field (weights 2.0/1.0/0.5 on `spot_rms` and 0.5/0.3 on `spot_rms_worst` for 16°/24°) — a pure **spot** merit (no wavefront term). It carries an `optimization.pso`
section with the same cycle budget, so the same file drives `rayweave pso` as
well (no bump geometry: PSO never deforms the merit). The full off-axis set (`spot_rms_t`/`_s`/`spot_rms_weighted`/`spot_ee_radius`) collapses the escape landscape to a single basin, so it is kept out of the escape merit to preserve multiple local minima (5 found). The escape then robustly finds the well-corrected landscape (0/16/24° Strehl ~0.99/~0.11/~0.63: the 24° outer field is already ≥ 0.5). `escape-demo.bash` DLS-refines that best with a `wavefront_astigmatism` merit (w16=14000, w24=6500, 24° spot terms w=0.11, 512-ray grid) so every field reaches best-focus Strehl ≥ 0.5 (~0.97/~0.51/~0.63). |
| `ar-coating.yaml` | Single-layer MgF2 anti-reflection coating on N-SK16 glass, quarter-wave at 550 nm. |
| `dielectric-mirror.yaml` | 9-layer quarter-wave Bragg reflector (SiO2/TiO2) on glass, design wavelength 550 nm. |
| `glass-optimize-demo.yaml` | 3-lens 7-surface system with glass-model variables (nd/vd) intended for DLS optimisation. 3 fields (0°, 10°, 16°), 4 wavelengths (g/F/d/C). |
| `glass-color-demo.yaml` | The US2645157 Cooke triplet with the flint/crown glasses **swapped** (the negative element carries a crown, the positive one a flint), so the axial + lateral colour is obvious. Declares the colour merit explicitly (`longitudinal_color` + per-field `lateral_color`); the script adds the nd/vd glass variables with `--glass-variables` and pins the element powers with `--power-solve`, so a lower merit is a true glass rebalance. |
| `multi-config-zoom.yaml` | 3-config zoom demo. Uses `entrance_pupil_diameter` **equality** constraints (now supported), `edge_thickness` with the `back_surface` field, and vignetting constraints. |
| `multi-config-zoom-demo.bash` | Multi-config zoom under hard engineering gates: DLS with an EPD equality constraint (20 mm), a per-field vignetting floor (`VF ≥ 0.5`) and the on-axis spot-RMS merit. Prints before/after RMS + vignetting factor per config/field, EFL and EPD per config, and the ray-overlaid layouts; every config must pass all gates. |
| `simple-zoom.yaml` | 3-config zoom with fuzzy image-height / incident-angle constraints and `ray_paths` (render-only metadata). |
| `simple-zoom-demo.bash` | Multi-config (shared-variable) zoom optimisation: one set of shared variables plus per-config local air gaps over wide/mid/tele. Runs `optimize --verbose --log`, then reports before/after EFL and on-axis RMS per config (gate: RMS < 0.3 mm each), draws the per-config layouts and checks the lens air gap never falls below 5 mm. |
| `asphere-optimize.yaml` | Singlet whose first surface is `asphere_polynomial`; optimizes `conic` and `a4`/`a6` coefficients (asphere variables). |
| `asphere-optimize-spherical.yaml` | The same singlet with **only curvature** variables (no `conic`/`a4`/`a6`) and no `pass_through` — a spherical-only optimisation input. It is a standalone variant: `asphere-optimize-demo.bash` runs its spherical stage on `asphere-optimize.yaml` with `--exclude-param conic,a4,a6,...` and writes `asphere-optimize-spherical-result.yaml`. |
| `asphere-demo-init.yaml` | Optimised 6-element double-Gauss (f/2.8 50 mm) whose curvatures carry **no** aspherical correction — the all-spherical baseline for `asphere-demo.bash`. |
| `6elements-init.yaml` | 6-element symmetric air-spaced starting point for a 35mm-format 50 mm f/2.8 standard lens. The structure was synthesised by an AI agent via curvature-scale search to hit EFL ≈ 50 mm, then handed to DLS optimisation. The optimised result reaches on-axis RMS < 0.1 mm (see `6elements-optimization-demo.bash`). It also carries an `optimization.escape` section so the same file drives the escape-function demo (`escape-demo.bash --lens 6elements`); the normal `6elements-optimization-demo.bash` ignores it. The escape merit is the spot merit (`spot_rms` per field, weights 2.0/1.0/1.0/0.5) plus moderate-weight off-axis spot kinds (`spot_rms_t`/`_s`/`spot_rms_worst` on 10°/16° at 0.3/0.3/0.6, `spot_rms_worst`/`spot_rms_weighted`/`spot_ee_radius` on 23° at 0.2/0.15/0.15), `lateral_color` and `opd_rms` — the full-weight off-axis set collapses the escape landscape to a single basin, but these reduced weights keep ~20 local minima while improving the edge-field (23°) spot RMS. |
| `escape-6elements-init.yaml` | v52 spot-merit 6-element demo input (virtual entrance pupil, 37 variables, 256 rays). Same start point as the original v14 init but with the wavefront merit family replaced by `spot_flatness`/`spot_astigmatism`, `back_focus_solve.type: spot` (num_rays 256), `vp_z` range 0..30 mm, and `max_cycles: 4`. Carries one `pupil_fill` term per field (weight 1000) in both merit modes and `min_throughput_ratio: 0.9`: the unbounded pupil-coverage term stops the search buying a smaller spot residual by clipping a field's periphery, and the threshold makes the feasible/infeasible classification match the `+-0.03` delivered-vignetting checks (see [merit-and-constraints.md §3.2](merit-and-constraints.md) and [escape.md §Infeasible basins](escape.md#infeasible-basins)). Used by `escape-demo.bash --lens 6elements`. |
| `doublegauss-ghost.yaml` | Ghost-ray trace sample on the optimised double-Gauss. Uses the surface-sequence encoding of Ono et al. (Optical Review 32:402-411): each ray carries an ordered surface-ID list; a direction reversal in the list means reflection. One ghost path `[0,1,2,3,4,3,2,3,4,...,14]` (reflect at surface 4, reversed refraction through surface 3, reflect at surface 2) plus a normal reference ray, and a `chief` section for re-adjusting the lens effective diameters. See `ghost-demo.bash`. |
| `ghost-demo-dbg.yaml` | Same system as `doublegauss-ghost.yaml` with the diagnostic comments expanded; `ghost-demo.bash` reads this copy for its traces and tables. |
| `vignette-demo.yaml` | Optimised double-Gauss whose every `auto_aperture: true` surface is deliberately inflated to a uniform 36 mm so the beam only fills the centre of each element. `rayweave vignette` shrinks them to the surviving-beam envelope (2× extent + margin) with `min_glass_path: 0.5`, settling per-field vignetting; surfaces 7 (f/2.8 stop, D=17.5) and 14 (image) are fixed and never re-sized. No `chief.stop_surface` — the pupils come from the chief-ray crossings (dynamic pupil). |
| `run-demo.bash` | End-to-end demo script using `us2645157.yaml`. |
| `optimize-demo.bash` | DLS optimisation of the degraded US2645157 triplet, comparing the `spot_rms`-only merit against the off-axis spot merit kinds: per-field spot RMS before/old/new (chief), the new-merit final values from the `--log` breakdown, and PNG diagrams. |
| `psf-mtf-demo.yaml` | PSF/OTF/MTF demo input: the US2645157 triplet after the escape-function global optimiser **plus a DLS re-optimisation with the 16° `wavefront_astigmatism` merit (w16=14000) and the 24° spot terms at w24=0.05** (the same recipe `escape-demo.bash` runs, whose current refinement uses w24=0.11). Cleaned copy — only the sections `psf` consumes (glass_catalog / configs / chief) are kept. Three fields (0/16/24 deg), reference surface 8; best-focus evaluation is the default so the aberrated off-axis fields are measured at their own focus. Reports MTF up to `psf.mtf_config.max_frequency` (200 c/mm). The 0° field is nearly diffraction-limited (Strehl ≈ 0.99, MTF50 ≈ 104 c/mm); the 16° field reaches Strehl ≈ 0.62 (MTF50 ≈ 70 c/mm); the 24° field stays at Strehl ≈ 0.59 (MTF50 ≈ 51 c/mm) — a monotone 0° > 16° > 24° profile meeting 0.9/0.6/0.5. |
| `psf-mtf-demo.bash` | PSF + OTF + MTF demo. Default lens `psf-mtf-demo.yaml` (0/16/24 deg): a single `rayweave psf` run (RCP+LCP, `--max-freq 200` cap, best-focus default) computes the image-plane PSF and the FFT-derived OTF/MTF per field, then prints a Strehl / FWHM / EE50 / Airy / MTF50-30-10 table (`<stem>-result.txt`) and draws per-field pm3d maps, a radial-profile overlay, and a sagittal/tangential MTF-overlay PNG. `--lens 6elements` (or any YAML path) switches the lens — the MTF cap keeps working via `--max-freq`, which overrides `psf.mtf_config.max_frequency` even when the chosen YAML has no `psf:` section. |
| `wavefront-demo.bash` | Wavefront analysis of the US2645157 triplet (0/16/24°, d line) in one `rayweave wavefront` pass: paraboloid fit (defocus / astigmatism / tilt), best-focus sphere, stabilized Fringe-Zernike decomposition and the statistics per field, one interpolated OPD map per field, and the **best-focus correction** (apply `--best-focus`, re-analyse, the defocus vanishes). `--lens 6elements` switches to the 4-field 6-element lens; `--defocus D` (needs `yq`) shifts the image plane by +D mm first so the correction is dramatic. `--clean` removes every artifact it generated. |
| `glass-optimize-demo.bash` | Glass optimisation demo using `glass-optimize-demo.yaml`. |
| `glass-color-demo.bash` | Power-preserving glass chromatic optimisation on the swapped-crown/flint triplet (`glass-color-demo.yaml`). `optimize --glass-variables --power-solve --power-solve-surfaces 2,4,7` auto-creates nd/vd variables for every element (the merit is left unchanged) while each element's thin-lens power is pinned by the hard solve, so a lower merit is a genuine glass rebalance rather than a de-collimation. Prints the element powers from `paraxial element_roles` before/after (they must match), the before/after LCA/TCA and the echoed `power_solve` write-back. `--clean` removes the generated YAML/logs. |
| `glass-attraction-demo.yaml` | Self-contained Cooke triplet with inline 12-glass catalog, S3/S6 nd/vd variables, glass_attraction enabled. 3 fields (0/16/24 deg), g/F/d/C wavelengths. |
| `glass-attraction-demo.bash` | Base vs glass-attraction comparison. Two DLS runs (base + attraction), compares nd/vd, spot RMS, and displays `opt_results.glass_attraction` diagnostics. `--snap` also runs `optimize snap` and reports the discrete-snap optical-merit cost. |
| `scale-demo.bash` | Demonstrates the `scale` subcommand: resizes the 25 mm triplet to a 50 mm standard (EFL exact, f/# preserved). |
| `6elements-optimization-demo.bash` | AI-assisted design demonstration: optimises the 6-element symmetric lens with 256 rays / 500 iterations (36 variables including glass nd/vd). Reports spot RMS / EFL / f-number / distortion before and after, draws raytrace diagrams, and checks the on-axis RMS stays below 0.1 mm. |
| `asphere-optimize-demo.bash` | Asphere-optimisation demo using `asphere-optimize.yaml`: optimizes `conic`/`a4`/`a6` and reports the coefficients before/after in the result file. |
| `asphere-demo.bash` | Adds one asphere to an optimised all-spherical double-Gauss (`asphere-demo-init.yaml`): per-case surface ranking → **before/after OPD-overlap chart** → spot-RMS validation of the fitted coefficients → `--apply` → spot RMS before/after → PNG diagrams and a per-case gate. Runs the full-aperture case always; when a stop is requested it also runs a stopped-down variant (`--epd X` as a fraction of the full pupil for `0 < X < 1`, an absolute mm diameter for `X ≥ 1`, or `--fno N`; surface 7's fixed aperture is resized via `yq`), and `--chart` adds the OPD-overlap / focus-footprint / field-defocus diagnostics. `--clean` removes every tagged `-epd*` / `-fno*` artifact. |
| `schmidt-flattener.yaml` | Folded Schmidt camera: corrector plate + spherical primary + 2-element field flattener. Demonstrates the **fold model** — positive thicknesses only, the fold is carried by the primary's `decenter: [{tilt: [0, 180, 0], scope: both}]` plus a top-level `reflect: true`. Physical Z: corrector/stop at 0, primary at 800, flat sensor at 400 (EFL≈386, F/1.93, D=200). |
| `schmidt-demo.bash` | Folded-Schmidt demo: chief rays, per-field spot RMS, paraxial analysis (EFL / f/# / pupil / track), and SVG/PNG raytrace diagrams. |
| `ghost-demo.bash` | Ghost-ray demo on the optimised double-Gauss (`doublegauss-ghost.yaml`): re-adjusts the lens effective diameters with `chief --clear-aperture --clear-aperture-rays 1024 --preserve-rays`, traces the ghost path and a normal reference ray through the re-sized system, prints the per-surface interaction / Fresnel-intensity table with the accumulated ghost intensity, and draws an SVG diagram. |
| `vignette-demo.bash` | Dynamic pupil + iterative vignetting / diameter sizing on the optimized double-Gauss with oversized diameters (`vignette-demo.yaml`): `chief` (per-field entrance/exit pupil from the chief-ray crossings, no explicit stop) → `trace` + `plot` of the before state → `vignette` (3 passes shrinking the `auto_aperture` surfaces to the surviving-beam envelope and applying `min_glass_path`) → `chief --marginal-rays` + `trace` + `plot` of the settled system, plus the per-field vignetting/pupil/diameter report. |
| `escape-demo.bash` | Escape-function global-optimisation demo. Default lens: degraded US2645157 triplet (`escape-demo.yaml`); `--lens 6elements` runs the escape optimiser on `escape-6elements-init.yaml` (37 variables, virtual pupil, spot merit, 4 cycles). Lists every discovered minimum with its `features[].element_powers` fingerprint, extracts the best with `escape extract`, then **reports** the per-field best-focus PSF Strehl of that minimum (RCP+LCP, d line) as a table with an `OK`/`BELOW 0.5` flag — the demo does **not** refine, so the flag is informational: a 4-cycle spot-merit run typically reports below 0.5 at every field, and reaching the ≥ 0.5 target needs a full run (a 30-cycle escape run with this merit reports 0.76-0.84). It also draws the initial/best diagrams and the element-powers chart, and a gate checks that the glass actually changed between minima. |

## External dependencies

The demo scripts depend only on the `rayweave` binary (built with `go build
-o rayweave ./cmd/rayweave` from the repository root) and POSIX shell
utilities. YAML/JSONL parsing and numeric comparison use the built-in
`rayweave query` subcommand (see `docs/query.md`). `gnuplot` is used only for
the optional PNG renderings (spot diagrams, aberration graphs): if it is not
installed, the scripts print a message and skip those renderings. `yq` is
optional too and only needed by specific options — `asphere-demo.bash --epd`
/ `--fno` (resizing the stop's fixed aperture) and `wavefront-demo.bash
--defocus D`. No Python and no other external tools are used, and every
script accepts `--clean` to delete exactly the files it generated.

## Optimisation-related notes (current syntax)

- Multiple `equality` constraints are supported (satisfiable targets converge;
  an unreachable target is reported with a WARNING instead of freezing).
- `optimization.aperture_margin` is clamped to ≥ 1.0 (smaller values stall DLS).
- `configs[].ray_paths` is render-only metadata; the optimizer ignores it.
- `edge_thickness` constraints take the back surface via `back_surface`; when it
  is omitted the next surface in system order is used (the old
  `target`-as-back-surface usage is gone).
- Asphere variables: `conic`, `a4`/`a6`/`a8`/`a10`/`a12` (aliases
  `coefficient_0`…`coefficient_4`).
- `power` variables (element thin-lens power): the dependent back surface's
  curvature is re-solved every evaluation; the escape glass phase freezes them
  (power preservation) without `power_solve`, whose snapshot would pin a
  zero-power start's zeros forever.
- `merit_modes` require `optimization.merit_schedule`: without a schedule the
  config's (possibly absent) fixed `merit` is evaluated and the objective stays
  0, so the run chases constraints only.
- `chief --clear-aperture` assigns every `auto_aperture: true` surface's
  diameter to the beam footprint + margin (so it both grows and shrinks).
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

### Output of `6elements-optimization-demo.bash`

| File | Description |
|---|---|
| `6elements-result.yaml` | Optimised 6-element symmetric lens system YAML. |
| `6elements-log.jsonl` | Per-iteration log plus `{"event":"breakdown"}` merit decomposition (256 rays × 500 iters). |
| `6elements-optimization-result.txt` | Spot RMS, EFL, f-number, and distortion before/after, plus threshold check (on-axis < 0.1 mm). |
| `6elements-init.png` | Raytrace diagram before optimisation. |
| `6elements-opt.png` | Raytrace diagram after optimisation. |

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