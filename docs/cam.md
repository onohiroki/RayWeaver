# `rayweave cam` — first-order cam solve

Solves the **cam** of a multi-config (zoom) system: the airspaces between the
moving groups, one set per position, so that every position reaches its target
EFL while the paraxial focus lands on one image plane pinned to a fixed Z. It is
a **first-order** solve — paraxial only, no ray tracing, no merit — so it is
fast and bit-deterministic, and it belongs *before* `optimize` / `escape`: `cam`
lays the zoom out, then the optimizer gives it the aberrations.

```
rayweave cam [--config ID] [--wl MM] [--max-iter N] [--tolerance MM]
             [--verbose] [--glass-dir DIR] < system.yaml
```

## Options

| Flag | Description |
|---|---|
| `--config ID` | solve only this position (a selector, not a setting — nothing is written back) |
| `--wl MM` | reference wavelength in mm for the paraxial solve (default: `chief.reference_wavelength`, else `0.00058756`, the same default `paraxial` uses); written back into `chief.reference_wavelength` |
| `--max-iter N` | solver iteration cap (default: `cam.max_iterations`, else 50); written back into `cam.max_iterations` |
| `--tolerance MM` | convergence tolerance on every residual, in mm (default: `cam.tolerance`, else `1e-9`); written back into `cam.tolerance` |
| `--verbose` | print one line per accepted iteration to stderr |
| `--glass-dir DIR` | AGF glass catalog directory |

## Input

Standard multi-config system YAML plus a `cam:` section:

```yaml
configs:
  - id: wide
    active: true
    fields:   [ ... ]          # not used by the solve
    wavelengths: [ ... ]       # not used by the solve
    surfaces: [ ... ]          # the layout this position starts from
  - id: tele
    active: true
    surfaces: [ ... ]

cam:
  surfaces: [5, 8, 11, 16]     # each lists the surface whose FOLLOWING airspace is solved
  targets:                     # one target per solved position
    - {config: wide, efl: 28.0}
    - {config: tele, efl: 70.0}
  image_plane: 17              # optional, resolved (see below)
  image_plane_z: 110.067       # optional, resolved (see below)
  hold_image_plane: true       # optional, default true
  max_iterations: 50           # optional
  tolerance: 0.000000001       # optional
  motion_weight: 0             # optional, default 0
```

| Field | Meaning |
|---|---|
| `surfaces` | the cam surfaces. For each ID, the airspace that **follows** it — that surface's `thickness` — is a variable, in every solved config. These are the group separations (and the back focus) the cam barrels carry. Required. |
| `targets` | one `{config, efl}` per position that is solved. `efl` is a **magnitude** (positive), compared against the absolute value of the paraxial EFL. Required. |
| `image_plane` | image-plane surface ID. It must be the **last** surface of every solved config, because the paraxial trace reads the last surface as the image plane. Resolved as `cam.image_plane` → `chief.reference_surface` → the last surface of the first solved config. |
| `image_plane_z` | the Z that plane is **held at**. Resolved as `cam.image_plane_z` → the current image-plane vertex Z of the **first solved config** (with several positions solved, the first is the reference the others are moved to — one detector). |
| `hold_image_plane` | `true` (default) also drives the image-plane position to that Z. `false` leaves the plane free, so each position settles wherever its EFL and focus put it — useful to learn where the planes *want* to be. |
| `max_iterations`, `tolerance` | solver controls, mirrored by `--max-iter` and `--tolerance`. |
| `motion_weight` | YAML-only solver control (no flag), see below. |

Only **active** configs are solved, and `--config` narrows that further. Every
solved position must have a target; a `cam.targets` entry naming a config that is
not solved is ignored.

## Output

The solved airspaces are written back into `configs[].surfaces[].thickness` (the
same contract as `scale`), and a `cam_result:` section reports the solve:

```yaml
cam_result:
  image_plane_z: 43.0
  wavelength: 0.00058756
  converged: true                  # true only when EVERY position converged
  configs:
    - config: wide
      efl_target: 22.8             # the magnitude that was asked for
      efl: 22.800000000012837      # signed paraxial EFL (negative after an odd reflection count)
      image_plane_z: 42.999999999994685
      focus_z: 42.99999999998204   # where the axial focus landed
      residual_efl_mm: 1.28e-11    # |EFL| − target
      residual_focus_mm: -1.26e-11 # focus − plane; positive = focus falls beyond the plane
      residual_image_plane_mm: -5.3e-12
      iterations: 4
      converged: true
      airspaces:
        - {surface: 2, from: 12.0,  to: 14.948537, delta: 2.948537}
        - {surface: 4, from: 10.0,  to:  4.915710, delta: -5.084290}
        - {surface: 6, from:  4.0,  to:  6.135753, delta: 2.135753}
  strokes:                         # present when 2+ positions are solved
    - surface: 2
      values:
        - {config: wide, thickness: 14.948537}
        - {config: mid,  thickness: 13.465778}
        - {config: tele, thickness: 12.690697}
```

`strokes` is the cam curve as a table, not a fit: one row per cam variable, each
listing the solved thickness at every position in config order. Fed to a
machinist, those are the slot positions the barrel must carry (interpolate
between them for intermediate focal lengths).

The image plane is written back too, so a `cam` output document records the
detector it was solved onto: `cam.image_plane`, `cam.image_plane_z` and
`cam.hold_image_plane` always carry the resolved values.

## What it does and does not solve

Per position the solve drives three residuals to zero:

1. the **EFL error** — the paraxial EFL magnitude minus the target,
2. the **defocus** — the back focal distance from the last refracting surface
   minus the airspace from it to the image plane (zero exactly when the axial
   focus sits on the plane),
3. the **image-plane position error** — when the plane is held.

The unknowns are the cam airspaces, so a position with more airspaces than
conditions spends the extra freedom on the **minimum-motion** solution (see
`motion_weight` below). A position with **too few** is reported as
`converged: false` with its residuals, so "this cam surface set cannot hold a
fixed detector" is visible rather than hidden behind a lucky-looking answer — and
the best layout found is still written back, so the pipeline can keep going.

What it does **not** do:

- **No aberrations.** The lens is not ray traced; spot size, vignetting and the
  merit are untouched. Run `optimize` / `escape` afterwards.
- **No f-number control.** The stop diameter is not a cam variable, so the
  working f/# drifts as the EFL changes. A constant-f/# zoom needs the stop
  diameter (or an iris) on the cam as an extra variable and condition — that is
  outside this command.
- **No powers.** The curvatures and glasses are fixed. If a target is out of
  reach, the fix is a different group power or total track, not a bigger cam
  step; the reported residual shows by how much.
- **No focus cure for a raw seed.** A starting layout whose paraxial focus is
  far from its image plane (common in a raw, merit-free seed) cannot be pulled
  onto a fixed plane by moving spacings alone at a fixed total track. Run with
  `hold_image_plane: false` first to see where the focus *wants* to be, then
  rebalance the powers.

## `motion_weight`

`0` (the default) makes every Newton step the **smallest** that reduces the
residuals, so the solved layout stays as close to the input as the conditions
allow — the minimum-motion cam. A positive value adds an explicit Tikhonov pull
toward the input airspaces, which competes with the conditions: the solve then
settles with the residuals above the tolerance (and is reported as not
converged). It is a probe — "how little motion could this take?" — not the normal
mode.

## Examples

```sh
# Solve every active position onto one plane and read the summary
rayweave cam < zoom.yaml > solved.yaml

# Just the cam curve, one line per variable
rayweave cam < zoom.yaml \
  | rayweave query --each 'cam_result.strokes[]:surface,values[].thickness'

# Solve one position only, from YAML to YAML
rayweave cam --config tele < zoom.yaml | rayweave zoom --config tele

# Probe where the planes want to be (no fixed detector)
rayweave cam < zoom.yaml > free.yaml \
  && rayweave query --each 'cam_result.configs[]:config,image_plane_z,residual_focus_mm'
```

A typical verification is `cam` followed by `zoom`: `zoom` re-derives each
position's first order from the written-back surfaces, so
`cam | zoom` shows the EFLs and the one image-plane Z that the solve achieved,
independently of the `cam_result` block.

When every target is reachable the summary reads, for example:

```
$ rayweave cam < zoom.yaml 2>&1 >/dev/null
=== Cam solve: 3 position(s) ===
  image plane Z 43.000000 mm (surface 7), wavelength 0.00058756 mm
  config0      EFL    22.8000 (target    22.8000)  focus -1.26e-11  plane -5.31e-12   4 iters
  config1      EFL    24.2000 (target    24.2000)  focus -1.49e-10  plane -4.60e-11   3 iters
  config2      EFL    25.7000 (target    25.7000)  focus -7.05e-13  plane -5.76e-13   4 iters
```

and when one is not, that position is marked and explained instead:

```
  wide         EFL    39.3015 (target    28.0000)  focus -1.17e+01  plane -6.91e+00  29 iters  NOT CONVERGED
rayweave[cam]: Warning: cam did not converge for wide (EFL off 11.3 mm, focus off -11.7 mm, image plane off -6.91 mm)
rayweave[cam]: Warning: wide: a cam airspace is at its lower bound — the barrels cannot reach this EFL with a fixed image plane here
```

## Method

[methods/cam.md](methods/cam.md) derives the residuals, the regularised
Gauss-Newton iteration, the trust region, the thickness-floor active set and the
determinism contract. The paraxial first order itself is
[paraxial.md](paraxial.md) / [methods/paraxial.md](methods/paraxial.md).
