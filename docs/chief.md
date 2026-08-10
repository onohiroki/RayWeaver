# `rayweave chief` — chief rays, spot statistics and pupil grids

Samples the beam for each field, determines the chief ray (spot centroid or
stop-centre), and computes spot statistics, marginal rays, ray fans and the
beam footprint. The output's `rays` section can be piped into `rayweave trace`
and `rayweave plot`.

```
rayweave chief [flags] < system.yaml
```

## Options

| Flag | Description |
|---|---|
| `--config ID` | select a config by id (multi-config mode) |
| `--wl W` | reference wavelength in mm (default `0.00058756`) |
| `--pass-through N` | constrain the chief ray to pass through `(0,0,0)` (centre) of surface N; overrides the YAML `pass_through.surface` |
| `--marginal-rays` | extract marginal (max/min) rays from the grid points and append them for piping into trace/plot |
| `--clear-aperture` | trace grid rays through every surface and set `surfaces[].diameter = 2 × max radial extent`, using an entrance-pupil-based beam diameter |
| `--shrink` | with `--clear-aperture`, also shrink diameters down to the beam footprint (default: only grow); the aperture stop keeps its diameter |
| `--clear-aperture-margin-mm` | with `--clear-aperture --shrink`, extra clearance added to each side of the footprint (mm, default 0.2) |
| `--clear-aperture-rays` | ray count for `--clear-aperture` beam tracing (0 = use `chief.num_rays`; use a dense grid for accurate footprints) |
| `--preserve-rays` | with `--clear-aperture`, keep the existing `rays` section instead of replacing it with chief rays, and omit `chief_rays` from the output (aperture adjustment only) |
| `--ray-fan` | compute ray fan (transverse aberration) for each field (YZ + XZ planes) |
| `--fan-plane yz\|xz` | compute only the YZ (meridional) or XZ (sagittal) fan (implies `--ray-fan`) |
| `--fan-rotation DEG` | compute fans in planes rotated by DEG around Z (0 = XZ, 90 = YZ; implies `--ray-fan`; repeatable or space-separated: `--fan-rotation 0 45 90`) |
| `--glass-dir DIR` | AGF glass catalog directory |

## Input YAML — `chief` section

```yaml
chief:
  fields:
    - angle: 10.0              # field angle (degrees)
      direction: [0, 1]        # [dx, dy] field vector (default [0, 1])
    - image_height: 17.666     # or specify by image height (mm)
    - height: 10.0             # or object height (mm, finite conjugate)
      object_z: -500           #   object plane Z (default -1000)
  reference_surface: 8         # surface ID for centroid/image height
  num_rays: 512                # pupil samples (≈ √n × √n)
  grid_type: polar             # pupil grid: polar | square | hex
  dump_map: false              # output per-ray spot data (grid_points)
  pass_through:                # optional: constrain chief ray to pass
    surface: 3                 #   through a specific surface coordinate
    coordinate: [0, 0, 0]      #   (default [0, 0, 0] = surface centre)
```

`fields` may also be given as `field_angles: [0, 16, 24]`. Field direction is
a unit vector in the pupil plane; the chief ray of each field lies in the plane
spanned by the field vector and the optical axis.

### Chief ray definitions

- **Without `pass_through`** — the chief ray is the ray that passes through the
  spot centroid on the reference surface. This definition is robust during
  optimization where the stop may be ill-defined.
- **With `pass_through`** — the chief ray is the ray from the field that passes
  through the given coordinate on the given surface (the traditional
  "stop-centre" definition).

## Output

Augmented YAML with a `chief_rays[]` section, one entry per field:

- `field_angle`, `image_height` — the image point of the chief ray
- `chief_ray` — the chief ray itself (single source of chief-ray geometry;
  `rayweave trace` reads it from here)
- `entrance_pupil` — radius, and centre when ≥ 2 fields allow stop inference
- `spot_stats` — `centroid`, `rms_x`/`rms_y`/`rms_r`, min/max extent,
  `traced_rays`/`missed_rays`
- `grid_points` (only with `dump_map: true`) — per-ray pupil/image position,
  intensity, OPL, and the origin/direction for re-tracing
- `ray_fan` (with `--ray-fan`) — meridional / sagittal / rotated fans
- `wavelengths` (when the config defines wavelengths) — per-wavelength stats

The top-level `rays` section carries only the extras needed for tracing
(marginal rays with `--marginal-rays`) plus the polarization, so the chief-ray
geometry is not duplicated; `trace` gathers the ray list from `chief_rays[]`
and the `rays` section. With `--preserve-rays` the existing `rays` section is
kept and `chief_rays` is omitted (aperture adjustment only).

## Examples

```sh
# Spot diagrams + paraxial analysis
rayweave chief < lens.yaml | tee chief.yaml | rayweave paraxial

# SVG raytrace diagram with marginal rays
rayweave chief --marginal-rays < lens.yaml | rayweave trace | rayweave plot -o diagram.svg

# Auto-size the lens clear apertures, then draw
rayweave chief --clear-aperture --clear-aperture-rays 4000 < lens.yaml \
  | rayweave trace | rayweave plot -o diagram.svg

# Stop-centre chief rays
rayweave chief --pass-through 5 < lens.yaml | rayweave trace

# Ray fans in three planes
rayweave chief --fan-rotation 0 45 90 < lens.yaml
```

## Method

The numerical details (pupil grids, centroid weighting, spot statistics, ray
fans, beam footprint) are described in
[methods/chief-rays-and-spot.md](methods/chief-rays-and-spot.md).
