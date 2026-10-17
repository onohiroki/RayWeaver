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
| `--chief-ray MODE` | chief-ray definition `entrance_pupil_centre` \| `centroid` \| `vignetting_centre` (empty = `chief.chief_ray_definition`); written back into the output when given. A definition the system cannot honour (a prescribed pupil without `stop_surface` or a virtual `pupil_model`), or an unknown name, exits 1 |
| `--marginal-rays` | extract marginal (max/min) rays from the grid points and append them for piping into trace/plot |
| `--clear-aperture` | trace the chief pupil grid through every surface and **set** each `auto_aperture: true` surface's `diameter = 2 × max radial extent + 2 × margin`. Because it is an assignment, diameters both **grow and shrink** to the beam footprint. The footprint comes from `chief.BeamEnvelope`, traced with the auto-aperture checks skipped, so a stale/undersized input diameter never truncates the measurement. The **reference surface** (`chief.reference_surface`), the stop surface (when `chief.stop_surface` is set) and every `auto_aperture: false` surface are skipped — a document whose reference surface is the image plane therefore cannot be re-sized this way (use the optimizer's own sizing pass for that) |
| `--clear-aperture-margin-mm` | with `--clear-aperture`, extra clearance added to each side of the beam footprint (mm, default 0.2) |
| `--clear-aperture-rays` | ray count for `--clear-aperture` beam tracing (0 = use `chief.num_rays`; use a dense grid for accurate footprints) |
| `--preserve-rays` | with `--clear-aperture`, keep the existing `rays` section instead of replacing it with chief rays, and omit `chief_rays` from the output (aperture adjustment only) |
| `--ray-fan` | compute ray fan (transverse aberration) for each field (YZ + XZ planes) |
| `--fan-plane yz\|xz` | compute only the YZ (meridional) or XZ (sagittal) fan (implies `--ray-fan`) |
| `--fan-rotation DEG` | compute fans in planes rotated by DEG around Z (0 = XZ, 90 = YZ; implies `--ray-fan`; repeatable or space-separated: `--fan-rotation 0 45 90`) |
| `--epz Z` | shorthand for a virtual entrance pupil at axial position Z (mm); activates `virtual_entrance_pupil` mode |
| `--epd D` | shorthand for `--pupil-model-diameter` (mm): virtual entrance pupil diameter |
| `--fnum N` | F-number; sets the virtual entrance pupil diameter to the paraxial EFL / N (mutually exclusive with `--epd`) |
| `--pupil-model-axial-position Z` | virtual entrance pupil Z (mm; activates virtual pupil mode) |
| `--pupil-model-diameter D` | virtual entrance pupil diameter (mm) |
| `--pupil-model-mode MODE` | pupil model mode (default `virtual_entrance_pupil`) |
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
  chief_ray_definition: vignetting_centre  # optional: chief-ray definition
                                           #   entrance_pupil_centre | centroid | vignetting_centre
  grid_type: polar             # pupil grid: polar | square | hex
  dump_map: false              # output per-ray spot data (grid_points)
  wavelength: 0.00058756       # reference wavelength (mm); overridden by --wl
  pass_through:                # optional: constrain chief ray to pass
    surface: 3                 #   through a specific surface coordinate
    coordinate: [0, 0, 0]      #   (default [0, 0, 0] = surface centre)
  pupil_model:                 # optional: virtual entrance pupil
    mode: virtual_entrance_pupil
    axial_position: 12.5       # pupil Z (mm); chief ray of every field passes
                               #   through (0,0,axial_position)
    diameter: 12.5             # entrance pupil diameter (mm)
```

`fields` may also be given as `field_angles: [0, 16, 24]`. Field direction is
a unit vector in the pupil plane; the chief ray of each field lies in the plane
spanned by the field vector and the optical axis.

**Where the fields come from.** `--config ID` selects a config *and that
config's fields* — a zoom position carries its own field angles, or the constant
image heights of that position. The resolution order is: the selected config's
`fields`, then `configs[0].fields`, then the top-level `chief.fields` /
`chief.field_angles`. The surfaces come from the same config, so the analysis
always uses the field list that belongs to the system it traced. Fields given as
`image_height` are resolved to a field angle by inversion, per config.
`vignette` resolves its fields the same way.

**An unreachable `image_height` is an error, never a substitution.** The
inversion can fail: the requested height may lie beyond the field's traceable
range (the beam clips at the stop or a surface aperture as the angle grows), and
the traced height is not necessarily monotone or continuous — the chief ray can
switch branch, leaving a step that no angle lands on. A command that measures
the field then exits 1, naming the field, the height asked for and the closest
reachable one:

```text
rayweave[zoom]: Error: config "config0": field 1 declares image_height 4.868 mm,
which this system cannot form (the closest reachable field delivers 2.741 mm at
7.5 deg) — the traced field is not the one the document declares
```

This covers `chief`, `zoom`, `psf`, `focus` and `wavefront`. Commands that
*report* the field set instead of measuring it keep going and warn: `list
fields` still shows the input/computed mismatch (see
[docs/list.md](list.md)), and the paraxial pupil derivation — which only uses
the first field — warns for `paraxial` / `list paraxial`. The search paths
(`optimize`, `escape`, `pso`) cannot stop mid-run either: they warn once on
stderr, keep the closest reachable angle and record every resolution in
`optimization.field_resolution` (see [docs/optimize.md](optimize.md)). Either
way an `image_height` field never silently becomes the on-axis field, which is
exactly what a spot-only merit would otherwise reward.

### Chief ray definitions

`chief.chief_ray_definition` (or `--chief-ray`) selects how each field's
object-space chief ray is constructed; a `pass_through` target takes precedence
over it.

| Definition | The chief ray passes through |
|---|---|
| `centroid` | the intensity-weighted centroid of the surviving pupil grid at the reference surface (root-solved origin; robust during optimisation, where the stop may be ill-defined) |
| `entrance_pupil_centre` | the entrance-pupil centre `(0, 0, z_EP)` — the textbook object-space chief ray of a prescribed pupil (analytic, no search) |
| `vignetting_centre` | the centre of the field's vignetting ellipse (`fields[].vignetting` decenter); without a vignetting specification it coincides with `entrance_pupil_centre` |

The default depends on whether the document **prescribes the pupil**:

- **Stop-free dynamic pupil** (no `stop_surface`, no virtual `pupil_model`) —
  the entrance-pupil Z is only known after tracing, so only `centroid` is
  meaningful: it is the default and the only accepted value. Requesting either
  prescribed-pupil definition exits 1 with an explanation.
- **Explicit `stop_surface` or a virtual `pupil_model`** — the default is
  `vignetting_centre`, so a field without a vignetting specification behaves
  exactly like `entrance_pupil_centre` while a vignetted field's chief ray
  follows the transmitted beam's centre.

The prescribed-pupil definitions aim the chief ray at the chosen pupil-plane
point and trace it, so `image_height` is the image of the object-space chief ray
of the prescribed pupil rather than the centre of a (possibly vignetted) bundle.

- **With `pass_through`** — the chief ray is the ray from the field that passes
  through the given coordinate on the given surface (the traditional
  "stop-centre" definition), whatever the definition says.

```sh
# Object-space chief ray of the entrance pupil (textbook construction)
rayweave chief --chief-ray entrance_pupil_centre < lens.yaml | rayweave trace
```

### Virtual entrance pupil

With `chief.pupil_model.mode: virtual_entrance_pupil`, the pupil is a fixed
virtual plane instead of a physical stop or the dynamic pupil: every field's
chief ray passes through `(0, 0, axial_position)`, the grid radius is
`diameter / 2`, and the dynamic-pupil iteration and low-angle probe are
skipped. Use it to specify a beam by entrance-pupil position and diameter (or
F-number) during initial exploration and escape optimisation.

The CLI shorthands are `--epz Z` (sets `axial_position` and activates virtual
mode), `--epd D` (sets `diameter`), and `--fnum N` (sets `diameter = |EFL|/N`,
with the EFL from a one-shot `paraxial` computation); `--epd` and `--fnum` are
mutually exclusive, and virtual mode requires a positive diameter. The
`paraxial` command honors the virtual pupil, so its entrance-pupil location,
diameter and F-number reflect the fixed plane.

Flags override the corresponding YAML values (`--wl` → `chief.reference_wavelength`,
`--pass-through` → `pass_through.surface`); the effective values are written
back into the output YAML (CLI/YAML rule).

## Output

Augmented YAML with a `chief_rays[]` section, one entry per field:

- `field_angle`, `image_height` — the image point of the chief ray
- `chief_ray` — the chief ray itself (single source of chief-ray geometry;
  `rayweave trace` reads it from here)
- `entrance_pupil` — radius and centre: the paraxial entrance pupil of an
  explicit stop, the per-field dynamic-pupil crossing, or the virtual pupil
  plane
- `spot_stats` — `centroid`, `rms_x`/`rms_y`/`rms_r`, min/max extent,
  `traced_rays`/`missed_rays`
- `effective_vignetting` (only for a heavily vignetted field) — the estimated
  effective pupil as a `VignettingDef` in the same convention as
  `fields[].vignetting`; see
  [methods/chief-rays-and-spot.md](methods/chief-rays-and-spot.md)
- `grid_points` (only with `dump_map: true`) — per-ray pupil/image position,
  intensity, OPL, and the origin/direction for re-tracing
- `ray_fan` (with `--ray-fan`) — meridional / sagittal / rotated fans
- `wavelengths` (when the config defines wavelengths) — per-wavelength stats
- `pupil_probe` / `pupil_probe_z` — for a stop-free (dynamic-pupil) system,
  whether the low-angle probe supplied the aperture position and at which Z

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

# Chief ray through the entrance-pupil centre instead of the spot centroid
rayweave chief --chief-ray entrance_pupil_centre < lens.yaml | rayweave trace

# Virtual entrance pupil at Z=12.5 mm with F/2.5
rayweave chief --epz 12.5 --fnum 2.5 < lens.yaml | rayweave paraxial

# Ray fans in three planes
rayweave chief --fan-rotation 0 45 90 < lens.yaml
```

## Method

The numerical details (pupil grids, centroid weighting, spot statistics, ray
fans, beam footprint) are described in
[methods/chief-rays-and-spot.md](methods/chief-rays-and-spot.md).
