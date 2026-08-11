# `rayweave vignette` — vignetting and aperture-diameter settlement

The `vignette` subcommand iteratively settles per-field vignetting and the
clear diameters of `auto_aperture: true` surfaces using the **dynamic pupil**
(per-field entrance/exit pupils from the chief-ray crossings; no physical stop
required). Fixed (`auto_aperture: false`) surfaces act as hard limiters and are
never re-sized.

```
rayweave vignette [--iterations 3] [--min-glass-path 0.5] [--margin-mm 0.2]
                  [--wl 0.00058756] [--config ID] [--glass-dir DIR] < system.yaml
```

It reads standard system YAML, writes pipeline-compatible YAML with updated
`configs[].surfaces[].diameter`, `chief_rays[]`, `rays[]` (marginal rays) and a
`vignetting_result:` report:

```
rayweave vignette < lens.yaml | rayweave trace | rayweave plot -o out.png
```

## Options

| Flag | Description |
|---|---|
| `--iterations N` | number of diameter/pupil passes (default 3) |
| `--min-glass-path M` | minimum glass path (edge thickness) below which a ray fails, applied to every glass element (mm, default 0.5) |
| `--margin-mm M` | clearance added to each side of the beam footprint when sizing `auto_aperture` surfaces (mm, default 0.2) |
| `--wl λ` | reference wavelength for grid ray tracing (default 587.56 nm) |
| `--config ID` | select config by id (multi-config mode) |
| `--glass-dir DIR` | AGF glass catalog directory |

## Input YAML — `vignette` section (optional)

Every setting can be given under a top-level `vignette:` section (also valid as
a computation setting under the CLI/YAML rules; the flags override, and the
effective flag-won values are written back into the output section):

```yaml
vignette:
  iterations: 3           # number of diameter/pupil passes
  min_glass_path: 0.5     # minimum glass path (edge thickness) per element (mm)
  margin_mm: 0.2          # beam-footprint clearance per side (mm)
  wavelength: 0.00058756  # reference wavelength (mm)
```

## Input

The system must carry a `chief` section providing the fields (`fields` or
`field_angles`) plus the grid parameters (`num_rays`, `grid_type`,
`reference_surface`). `chief.stop_surface` may be present, but **there is no
implicit aperture stop**: even with a stop id set, the per-field pupils come
from the chief-ray crossings.

```yaml
chief:
  fields:
    - {angle: 0}
    - {angle: 14}
  reference_surface: 9

configs:
  - surfaces:
      - {id: 1, type: sphere, curvature: 0.02, thickness: 6.0,
         material: {key: N-BK7}, diameter: 30, auto_aperture: true}
      - {id: 7, type: sphere, curvature: 0.0, thickness: 2.0,
         material: AIR, diameter: 15.8, auto_aperture: false}   # fixed limiter
      # ...
```

- `auto_aperture: true` surfaces are re-sized to the beam envelope each pass.
- `auto_aperture: false` surfaces are fixed limiters; rays exceeding their
  clear aperture are vignetted. `chief.stop_surface` is never an
  `auto_aperture` surface and is also excluded from re-sizing (a physical stop
  aperture is a design input).
- `min_glass_path` is applied to every glass element's entry surface that does
  not already carry a value (a non-AIR surface whose preceding medium is AIR;
  mirrors are handled by the fold walk). Rays whose edge thickness in any glass
  element falls below it are vignetted.

## Behaviour

Each pass re-traces every field's pupil grid against the **current** diameters
and takes two measurements:

1. **Envelope (for sizing)** — the full beam of every field, measured *before*
   the vignetting cut, so a vignetted off-axis bundle never shrinks the lens.
   Each `auto_aperture: true` surface is re-sized to `2 × max extent + 2 ×
   margin-mm` over the union of all fields' envelopes.
2. **Vignetting (for the report)** — the surviving fraction of each field's
   grid. Rays are dropped when they fail the glass-path (edge-thickness) check,
   exceed a fixed-surface aperture, or fall outside the bounding envelope.

The off-axis comparison frame is the plane perpendicular to **each field's own
chief ray** through its entrance pupil, not a plane perpendicular to the optical
axis: field 0's marginal-ray envelope casts the reference circle, and each
off-axis bundle must fit inside it as seen along its chief ray.

The loop runs until the diameters stop changing or `--iterations` passes are
used up (default 3). The final pass re-derives the per-field marginal rays
(`Yplus` / `Yminus`) against the settled diameters.

## Output

```yaml
vignetting_result:
  iterations: 3
  min_glass_path: 0.5
  stop_surface: 7                      # set when the input had one
  diameters:
    - {surface_id: 1, before: 36, after: 22.54226692364057}
    - {surface_id: 2, before: 36, after: 21.37808521496089}
    # ... only auto_aperture: true surfaces
  fields:
    - field_index: 0
      angle_deg: 0
      vignetting: 1.0                  # surviving fraction of the grid
      grid_total: 261
      grid_surviving: 261
      entrance_pupil_z: 11.942597095   # per-field dynamic entrance pupil
      exit_pupil_z: -10.6054675884     # image-side chief-ray crossing (omitted when unreliable)
      bound_lower: -6.79067084459      # field 0's marginal envelope at this field's pupil plane
      bound_upper: 6.79067084459
      marginal_y_lower: 6.79067084459  # this field's +Y/−Y marginal-ray heights there
      marginal_y_upper: -6.79067084459
    - field_index: 1
      angle_deg: 10
      vignetting: 1.0
      # ...
```

Per-field fields:

| Field | Meaning |
|---|---|
| `vignetting` | surviving fraction of the pupil grid (1.0 = no vignetting) |
| `grid_total` / `grid_surviving` | grid points before / after the cuts |
| `entrance_pupil_z` | dynamic entrance pupil Z (absent when ill-conditioned) |
| `exit_pupil_z` | exit pupil Z from the image-side chief-ray crossing (omitted when unreliable) |
| `bound_lower` / `bound_upper` | field 0's marginal-ray envelope at this field's entrance-pupil plane |
| `marginal_y_lower` / `marginal_y_upper` | this field's marginal-ray heights there (within the bounds ⇒ not additionally vignetted) |

`diameters[]` lists each `auto_aperture: true` surface's `before`/`after`
diameter.

In addition the output carries:

- updated `configs[].surfaces[].diameter` (and `min_glass_path` where applied),
- `chief_rays[]` with the settled per-field chief rays, entrance/exit pupils and
  spot statistics,
- `rays[]` with the per-field marginal rays (`marginal_f<N>_Yplus` /
  `marginal_f<N>_Yminus`) plus the polarization, ready for `trace` / `plot`.

## Pipelines

```sh
rayweave vignette --iterations 3 --min-glass-path 0.5 < lens.yaml \
  | rayweave trace | rayweave plot -o lens.png

rayweave vignette < lens.yaml | rayweave paraxial
```

See `samples/vignette-demo.bash` (double-Gauss, dynamic pupil — no stop) and
`phase-3` of `samples/doublegauss-demo.bash` for end-to-end demos.

## Notes

- Use `chief --clear-aperture` for a **one-shot** size-to-footprint (no
  iteration, always grow+shrink); `vignette` is the iterative, glass-path-aware
  settling tool.
- The stop (`role: "stop"`) is inert metadata; the aperture is defined by the
  surfaces' `auto_aperture` flags and the chief-ray-crossing pupils themselves.
- The dynamic-pupil convention and the interplay of the pupil with fixed
  surfaces / glass-path checks are described in
  [chief-rays-and-spot.md](methods/chief-rays-and-spot.md).