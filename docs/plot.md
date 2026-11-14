# `rayweave plot` — SVG / PNG cross-section diagrams

Renders a cross-section drawing of the lens system with ray paths overlaid, as
SVG (default) or PNG. A multi-config (zoom) document can be drawn as several
stacked panels at one shared scale, aligned on their image planes (see
[Multi-config layouts](#multi-config-layouts)). PNG rasterization is done
in-process via `golang.org/x/image/vector`; no external tools are required.

```
rayweave plot [-o file.svg|.png] [flags] < input.yaml
```

## Options

| Flag | Description |
|---|---|
| `-o, --output file.svg\|.png` | output file (default: stdout, SVG) |
| `--config ID` | select a config by id (single-config mode) |
| `--multi-config` | draw several configs stacked vertically: the first 5 **active** configs in document order (every config when none is active) unless `--configs` is given |
| `--configs "id1,id2,..."` | configs to draw in multi-config mode, by id or 0-based index (comma-separated; implies `--multi-config`; not capped) |
| `--lens-width 0.1` | lens-body stroke width |
| `--ray-width 0.1` | ray-path stroke width |
| `--scale 0` | SVG/PNG scale factor (0 = auto; one shared value in multi-config mode) |
| `--right-margin 20` | right-side margin beyond the image plane (% of lens length) |
| `--fan-rays 11` | max fan rays drawn per field in the lens diagram (0 = hide fan rays) |
| `--show-invalid-rays` | boolean toggle, no value argument (default `false` = off): draw rays whose path carries an error code in full instead of the default hiding; `--show-invalid-ray-fan` is an alias |
| `--clip-invalid-rays` | boolean toggle, no value argument (default `false` = off): draw error-coded rays only up to the first surface that errored; `--clip-invalid-ray-fan` is an alias |
| `--glass-dir DIR` | AGF glass catalog directory (resolves material colours; written back into `glass_catalog.directory`) |
| `--element-color "1=#ff0000,3=#00ff00"` | element fill colors by 1-based index (repeatable; hex, rgb(), or CSS named colors) |
| `--asphere-color "#ff0000"` / `--asphere-color "3=#ff0000,7=#0000ff"` | aspheric **surface edge** colors (the sag curve line, not the element fill): single color for all aspheres, or per-surface-ID map (repeatable) |

## Input

YAML with `configs[].surfaces` plus optional `results[]` (from `trace`) and
`chief_rays[]`. Pipe after `chief`/`trace` for ray paths:

```sh
cat system.yaml | rayweave chief | rayweave trace | rayweave plot -o lens.svg
cat system.yaml | rayweave chief | rayweave trace | rayweave plot -o lens.png
```

In single-config mode, use `--config` to choose which config to draw:

```sh
cat result.yaml | rayweave plot --config wide -o wide.svg
cat result.yaml | rayweave plot --config tele -o tele.png
```

## Multi-config layouts

Zoom and other multi-config documents can be drawn in one picture. Every config
becomes a **panel**:

```sh
# the first 5 active configs, in document order
cat zoom.yaml | rayweave plot --multi-config -o zoom.svg

# an explicit selection (ids or 0-based indexes)
cat zoom.yaml | rayweave plot --configs wide,mid,tele -o zoom.png
cat zoom.yaml | rayweave plot --configs 0,2 -o zoom.png
```

`--configs` implies `--multi-config` and selects in the order given (duplicates
are dropped). `--config` and the multi-config flags are mutually exclusive.

Selection rules for `--multi-config` without `--configs`:

- the **active** configs in document order, at most 5; when the document does
  not mark any config active, every config is drawn in document order;
- configs without surfaces are skipped with a warning;
- if more than 5 configs would be drawn, the first 5 are drawn and a warning
  says so (`--configs` selects a different set, without a cap).

Layout rules:

- **one shared scale** for every panel (`--scale` overrides it globally);
- the **image planes are aligned** on one vertical line. The image plane is the
  `chief.reference_surface` when the document declares one, else the last
  surface of the config;
- panels are **stacked vertically** with a fixed gap, so two configs never
  overlap. Each band is as tall as that config's own beam envelope at the shared
  scale, the stack is centred in the one-diagram canvas while it fits, and the
  canvas grows taller as panels are added (a one-panel plot is the ordinary
  diagram);
- panel order is the selection order, and no config labels are drawn.

Rays are not drawn in multi-config mode: `results[]` and `chief_rays[]` hold
the rays of **one** config only (the last `trace --config`), so overlaying them
on other panels would be wrong. When they are present a warning says so; use
`--config ID` for a ray-overlaid single plot.

```sh
# zoom positions, aligned and to scale
cat samples/simple-zoom.yaml | rayweave plot --multi-config -o zoom.png

# one config with rays (the usual raytrace diagram)
cat result.yaml | rayweave plot --config config0 -o wide.png
```

## Styling

- Glass types are colour-coded using the nd/vd values from the `glass_catalog`
  section.
- Ray colours follow the field angle (low = blue, high = red).
- Aspheric surfaces are drawn from the sag function (see the asphere rendering
  in `internal/render`).
- **Element colors**: `--element-color "0=#ff0000,2=blue"` overrides the glass-based
  fill for specific elements (1-based index from `findElements` order).
- **Asphere colors**: `--asphere-color "#ff0000"` colors the aspheric surface
  **edge line** itself (the sag curve stroked on top of the lens body; mirrors
  included) — the element fill keeps its glass colour. `--asphere-color "3=#ff0000,7=blue"`
  sets per-surface-ID colors. Both flags are repeatable.

## Warnings (stderr, rendering continues)

Three warning cases may appear when `--element-color` or `--asphere-color`
are used:

| Situation | Warning (stderr) |
|---|---|
| Element index out of range | `Warning: --element-color %d=... ignored: no element %d (elements are numbered 1..%d)` |
| Non-aspheric surface ID | `Warning: --asphere-color %d=... ignored: surface %d is not an aspheric surface in this config` |
| All-color but no aspheres | `Warning: --asphere-color %s ignored: this config has no aspheric surfaces` |

All warnings are emitted to stderr, the run continues normally (exit 0), and the
rendering proceeds with the effective (merged) values. The YAML output carries
the merged element/asphere colors that were actually used.

## Invalid rays

A ray whose path records an error code on any surface (e.g. `aperture_stop`,
`missed_surface`, `total_internal_reflection`, `glass_path_too_short`,
`glass_path_too_long`) is considered invalid. By default such rays are not
drawn at all; pass `--show-invalid-rays` to draw them in full (the previous
behaviour) or `--clip-invalid-rays` to stop the path at the first erroring
surface. Both are boolean toggles (default `false`): enabled by giving them
alone, disabled with `--show-invalid-rays=false` / `--clip-invalid-rays=false`.
The two flags are mutually exclusive.

The policy covers every drawn ray: traced `results[]` (including marginal and
chief rays written by `chief --marginal-rays`) and `chief_rays[].ray_fan` fan
rays. A marginal ray that misses the stop is hidden like an invalid fan ray
instead of being drawn across the aperture. The older flag spellings
`--show-invalid-ray-fan` / `--clip-invalid-ray-fan` are kept as aliases.

## Examples

```sh
# SVG raytrace diagram (centroid-based chief rays)
cat samples/us2645157.yaml \
  | rayweave chief --clear-aperture \
  | rayweave chief --marginal-rays \
  | rayweave trace \
  | rayweave plot -o diagram.svg

# PNG version (same pipeline, just change the extension)
cat samples/us2645157.yaml \
  | rayweave chief --clear-aperture \
  | rayweave chief --marginal-rays \
  | rayweave trace \
  | rayweave plot -o diagram.png

# Custom element colors (element 1 = red, element 3 = blue)
cat samples/us2645157.yaml \
  | rayweave chief --clear-aperture \
  | rayweave chief --marginal-rays \
  | rayweave trace \
  | rayweave plot --element-color "1=#ff0000,3=blue" -o colored.svg

# Custom asphere edge color (all aspheric surfaces = green)
cat samples/us2645157.yaml \
  | rayweave chief --clear-aperture \
  | rayweave chief --marginal-rays \
  | rayweave trace \
  | rayweave plot --asphere-color "green" -o asphere.svg
```

## Reference

The SVG rendering approach is based on the method described at
<http://onohiroki.cycling.jp/comp-perllens>.
