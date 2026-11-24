# `rayweave stop` — materialize the virtual entrance pupil as a real stop

`stop` converts `chief.pupil_model` (mode `virtual_entrance_pupil`) into a
physical plane stop. The virtual pupil is an **entrance-pupil specification** —
a plane in object space and a diameter — not a stop plane. The surfaces in
front of a physical stop image it, so a stop placed at the pupil plane with the
pupil diameter would change the delivered beam. The command solves for the
plane inside an air gap whose paraxial image is the requested entrance pupil and
sets the clear diameter to the pupil diameter divided by the front-group
magnification. The beam, f-number and per-field vignetting definitions are then
unchanged, which the command verifies with a chief-ray pass before and after.

## Usage

```sh
rayweave stop [--config ID] [--anchor rear|front] [--id N] [--wl MM]
              [--glass-dir DIR] [--no-verify] [--dry-run]
              < lens-with-virtual-pupil.yaml > lens-with-stop.yaml
```

Typical workflow — pull a minimum out of a global search, clean it, then give it
a physical stop:

```sh
rayweave escape extract --index 0 < run.yaml \
  | rayweave clean \
  | rayweave stop \
  | rayweave paraxial
```

| Flag | Description |
|---|---|
| `--config ID` | reference config (default: the first config). Its stop plane is solved; the other configs follow it. |
| `--anchor rear` | (default) the other configs keep the distance to the surface **behind** the stop, so the stop moves with the rear group. |
| `--anchor front` | the other configs keep the distance to the surface **in front** of the stop, so it moves with the front group. This is an error when the reference stop becomes the first surface (the virtual pupil is at or in front of the first surface), because there is no front surface then. |
| `--id N` | surface ID for the inserted surface (default: one above the largest existing ID). |
| `--wl MM` | reference wavelength for the paraxial solve (written back to `chief.reference_wavelength`). |
| `--glass-dir DIR` | AGF glass catalog directory. |
| `--no-verify` | skip the chief-ray spot-RMS comparison. |
| `--dry-run` | report the planned stop and its entrance pupil without modifying the document. |

## Input requirements

- `chief.pupil_model.mode: virtual_entrance_pupil` with a positive `diameter`.
- `chief.stop_surface` must be unset: a document that already has a physical
  stop has nothing to materialize.
- The stop plane must fall inside an **air gap** before the first fold (a stop
  cannot live inside glass, on an existing vertex, or in the image space).
  A virtual pupil *position* that is inside glass is fine: the stop itself is
  solved into an air gap whose image is that pupil.
- A folded system is rejected when the stop's entrance pupil would lie beyond
  the first fold, where the unfolded Z is no longer a global coordinate.

## The `stop_insert` section

The placement anchor and the surface ID can be declared in the document; a flag
overrides the YAML value and the effective value is written back.

```yaml
stop_insert:
  anchor: rear   # rear (default) | front
  id: 0          # 0 = one above the largest existing ID
```

## What the command writes

- A new plane surface in every config: `type: sphere`, `curvature: 0`,
  `material: AIR`, `auto_aperture: false`, `role: stop`, and the solved clear
  `diameter`.
- `chief.stop_surface` set to the new ID; `chief.pupil_model` removed (the
  legacy top-level `pupil_model` is removed too when present).
- Optimisation variables that targeted the virtual pupil (params
  `pupil_model_axial_position` / `pupil_model_diameter`, type `pupil_model`) are
  removed, together with variable links sourced from them. A later
  `optimize`/`escape` must not recreate the model.
- Explicit ray paths (`rays[].path`, `configs[].rays[].path`,
  `chief.fields[].path`, `trace_single.path`) are updated to visit the new stop
  when they are forward traversals. A non-monotonic (ghost) path is left alone
  and reported.
- Computed result sections (`chief_rays`, `results`, `paraxial_result`, …) are
  stripped exactly like `clean`: they describe the pre-conversion surface list,
  so keeping them would let a later `trace` skip the new stop. The `stop:`
  configuration block is rewritten with the inserted surface's ID, physical Z
  and diameter.
- `stop_insert_result` (below) and refreshed `metadata`.

## Front-of-lens entrance pupil

When `chief.pupil_model.axial_position <= 0`, the entrance pupil is at or in
front of the first surface. There is no power in front of it, so the stop plane
and diameter are the pupil's own: the stop is inserted as the **new first
surface**, at the pupil plane, with the pupil diameter (magnification 1).

A surface list starts at Z = 0, so the document frame is shifted by
`|axial_position|`: the stop becomes the first surface and the old first
surface moves to `|axial_position|`. Absolute-Z inputs are shifted with it to
keep the same object distances and ray origins:

- `fields[].object_z` (a finite-conjugate field with no explicit `object_z`
  materialises the implicit `-1000` mm plane first),
- `pass_through.coordinate.z`,
- `rays[].initial.origin.z` (top level and per config),
- `trace_single.origin` / `trace_single.aim`,
- `cam.image_plane_z` when set.

`frame_shift_mm` in `stop_insert_result` records the translation.

## Multi-config (zoom) documents

The reference config's stop plane is solved so its entrance pupil is the virtual
model's `(axial_position, diameter)`. The other configs insert the stop in the
same surface gap with the anchor rule:

- `rear`: `Z_stop' = Z_rear' − (Z_rear − Z_stop)`,
- `front`: `Z_stop' = Z_front' + (Z_stop − Z_front)`.

The clear diameter is computed per config from that config's entrance-pupil
diameter: `diameter = EPD_target / m'`, where `m'` is the config's front-group
magnification at the anchored plane. Every position therefore delivers the same
EPD, but a single physical stop cannot make every zoom position's **entrance
pupil position** equal the model's; each config's resulting position is reported
and a material difference is warned about.

`--anchor front` with a front-of-lens entrance pupil (the reference stop becomes
the first surface) is rejected: the other configs would have no surface in front
of the stop to anchor to.

All configs receive the stop: `chief.stop_surface` is one document-wide surface
ID, so a stop present in only the reference config would leave the other
positions pointing at a missing surface. Convert a single-config document when
only one position should be affected. There is no barrel-absolute anchor: each
config's Z origin is its own first surface, so "absolute" would be a third
placement rule with no cross-config frame to anchor to.

## Output: `stop_insert_result`

```yaml
stop_insert_result:
  pupil_model: {mode: virtual_entrance_pupil, axial_position: 15, diameter: 12.5}
  anchor: rear
  reference_config: config0
  frame_shift_mm: 0          # > 0 only for the front-of-lens case
  configs:
    - id: config0
      reference: true
      stop_surface: 14
      stop_z: 15.9072        # plane in the output document frame
      stop_diameter: 10.8488 # clear aperture (not the EPD)
      magnification: 1.15220 # EPD / stop diameter
      entrance_pupil_z: 15.0
      entrance_pupil_diameter: 12.5
      ep_z_error_mm: 0
      epd_error_mm: 0
      previous_surface: 6
      next_surface: 7
      gap_front_mm: 5.2072
      gap_rear_mm: 0.7928
  verification:
    fields:
      - {index: 0, before_spot_rms_mm: 0.443053, after_spot_rms_mm: 0.443053}
    max_spot_rms_delta_mm: 0
    max_spot_rms_delta_rel: 0
  warnings: []
```

`entrance_pupil_z` / `entrance_pupil_diameter` are the paraxial entrance pupil
the inserted stop actually delivers; `ep_z_error_mm` / `epd_error_mm` are the
residuals against the virtual model (zero for the reference config by
construction, and the EPD zero for every config). The verification compares the
reference config's per-field chief-ray spot RMS in the virtual-pupil and
materialized systems; a large delta means the stop no longer selects the same
rays (check the warning). Other configs deliver the same EPD but their entrance
pupil position follows their own front group, as the warnings report.

## Errors

- no `chief` section, no `chief.pupil_model`, or a mode other than
  `virtual_entrance_pupil`,
- a non-positive pupil diameter,
- `chief.stop_surface` already set,
- no air gap yields the requested pupil (the message lists every air gap and the
  entrance-pupil range it can produce),
- the solved plane falls outside a config's gap under the anchor rule,
- the requested surface ID is already used,
- a fold between the stop and its entrance pupil,
- `--anchor front` when the reference stop is the first surface.

## Related

- [chief.md](chief.md) — the virtual entrance pupil (`--epz` / `--epd` /
  `--fnum`, `chief.pupil_model`) and the chief-ray definitions.
- [paraxial.md](paraxial.md) — the entrance-pupil location and diameter the
  converted document reports.
- [clean.md](clean.md) — the result stripping `stop` applies.
- [methods/ray-tracing.md](methods/ray-tracing.md) — surface paths and the
  list-order interaction rule (surface IDs are labels).
