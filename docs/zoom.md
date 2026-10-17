# `rayweave zoom` — per-config summary

Reads every active configuration of a multi-config (zoom) document and reports
one row per position: that position's first order, the image height each field
reaches, its spot RMS and the surviving fraction of its pupil grid. Read-only —
it sizes no diameter and evaluates no merit — so a zoom can be read in one step
instead of looping `paraxial` and `chief` per position by hand.

```
rayweave zoom [--wl MM] [--num-rays N] [--config ID] [--glass-dir DIR] < system.yaml
```

## Options

| Flag | Description |
|---|---|
| `--wl MM` | reference wavelength in mm for the first-order trace and the grids (default: `chief.reference_wavelength`, else `0.00058756`); written back into `chief.reference_wavelength` |
| `--num-rays N` | pupil-grid rays per field (default: `chief.num_rays`, else 64); written back into `chief.num_rays` when given |
| `--config ID` | summarise only this config (a selector, not a setting — nothing is written back) |
| `--glass-dir DIR` | AGF glass catalog directory |

## Input

Standard system YAML (`glass_catalog` + `configs[].surfaces`). Surfaces live
under `configs[]`, so at least one config is required. Optional:

```yaml
chief:
  reference_surface: 16     # the image plane the spot and image height use
  stop_surface: 1           # the aperture stop (omit for a dynamic pupil)
  reference_wavelength: 0.00058756
  num_rays: 64
  grid_type: polar          # polar | hex | square
```

Only **active** configs are summarised; each config's own `fields` and
`wavelengths` drive its row, so a position's image-height fields are resolved
against that position's surfaces (not another position's).

## Output

Augmented YAML with a `zoom_summary:` section:

```yaml
zoom_summary:
  reference_wavelength: 0.00058756
  num_rays: 64
  configs:
    - id: config0
      active: true
      focal_length: 28.004556        # paraxial EFL of that config
      entrance_pupil_diameter: 7.0
      image_space_f_number: 4.0007   # working f/#
      inf_conj_image_space_f_number: 4.0007
      total_track: 110.067           # first surface → image plane (mm)
      image_plane_z: 110.067         # reference-surface vertex Z, document frame
      fields:
        - field_index: 0
          angle_deg: 0.0
          image_height: 0.0
          spot_rms: 2.5573           # mm at the reference surface
          vignetting: 1.0            # surviving / launched grid rays
          grid_total: 64
          grid_surviving: 64
```

| Field | Meaning |
|---|---|
| `focal_length`, `entrance_pupil_diameter`, `image_space_f_number`, `inf_conj_image_space_f_number`, `total_track` | the values `paraxial_result` reports for that config's surfaces |
| `image_plane_z` | the `reference_surface` vertex Z in the document frame — the same position `psf` / `focus` treat as the image plane |
| `fields[].angle_deg`, `image_height` | the field's angle and the chief ray's image-plane height (mm) |
| `fields[].spot_rms` | RMS spot radius at the reference surface (mm), over the grid rays that reached it — the same statistic as `chief_rays[].spot_stats.rms_r` |
| `fields[].vignetting`, `grid_surviving` / `grid_total` | the fraction of the prescribed pupil grid that survives an aperture clip or the glass-path (edge-thickness) check |

`vignetting` is measured on the config's surfaces as they are in the document:
nothing is sized. Compare it with `vignette`, which also settles
`auto_aperture` diameters, and with `optimize`'s end-of-solve warnings, which
report the two ways this fraction is lost (a collapsed field, a clipped clear
aperture).

A field declared by `image_height` is solved to a field angle per config before
the row is built. When the config's surfaces cannot form that image height the
summary would otherwise describe a different field than the document declares,
so `zoom` exits 1 and names the field, the height asked for and the closest
reachable one (see [docs/chief.md](chief.md), *Where the fields come from*):

```text
rayweave[zoom]: Error: config "config0": field 1 declares image_height 4.868 mm,
which this system cannot form (the closest reachable field delivers 2.741 mm at
7.5 deg) — the traced field is not the one the document declares
```

## Examples

```sh
rayweave zoom < zoom.yaml

# One line per zoom position
rayweave zoom < zoom.yaml \
  | rayweave query --each 'zoom_summary.configs[]:id,focal_length,image_plane_z'

# Every field of the long position: angle, image height, spot, vignetting
rayweave zoom --config config2 < zoom.yaml \
  | rayweave query --each 'zoom_summary.configs[0].fields[]:angle_deg,image_height,spot_rms,vignetting'

# Quick longitudinal colour check per position (F vs C line)
rayweave zoom --wl 0.00048613 < zoom.yaml > f.yaml
rayweave zoom --wl 0.00065627 < zoom.yaml > c.yaml
```

## Method

The first order comes from the same paraxial trace as
[paraxial.md](paraxial.md) ([methods/paraxial.md](methods/paraxial.md)); the
per-field columns come from the same chief-ray grid as [chief.md](chief.md)
([methods/chief-rays-and-spot.md](methods/chief-rays-and-spot.md)). Passing the
config's own chief pass into the paraxial trace is what lets a stop-free
(dynamic-pupil) position report an entrance-pupil diameter instead of an empty
first order.
