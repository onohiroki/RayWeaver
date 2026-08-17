# `rayweave plot` — SVG / PNG cross-section diagrams

Renders a cross-section drawing of the lens system with ray paths overlaid, as
SVG (default) or PNG. PNG rasterization is done in-process via
`golang.org/x/image/vector`; no external tools are required.

```
rayweave plot [-o file.svg|.png] [flags] < input.yaml
```

## Options

| Flag | Description |
|---|---|
| `-o, --output file.svg\|.png` | output file (default: stdout, SVG) |
| `--config ID` | select a config by id (multi-config mode) |
| `--lens-width 0.1` | lens-body stroke width |
| `--ray-width 0.1` | ray-path stroke width |
| `--scale 0` | SVG/PNG scale factor (0 = auto) |
| `--right-margin 20` | right-side margin beyond the image plane (% of lens length) |
| `--fan-rays 11` | max fan rays drawn per field in the lens diagram (0 = hide fan rays) |
| `--show-invalid-ray-fan` | boolean toggle, no value argument (default `false` = off): draw fan rays whose path carries an error code in full instead of the default hiding |
| `--clip-invalid-ray-fan` | boolean toggle, no value argument (default `false` = off): draw error-coded fan rays only up to the first surface that errored |
| `--glass-dir DIR` | AGF glass catalog directory (resolves material colours; written back into `glass_catalog.directory`) |

## Input

YAML with `configs[].surfaces` plus optional `results[]` (from `trace`) and
`chief_rays[]`. Pipe after `chief`/`trace` for ray paths:

```sh
cat system.yaml | rayweave chief | rayweave trace | rayweave plot -o lens.svg
cat system.yaml | rayweave chief | rayweave trace | rayweave plot -o lens.png
```

In multi-config mode, use `--config` to choose which config to draw:

```sh
cat result.yaml | rayweave plot --config wide -o wide.svg
cat result.yaml | rayweave plot --config tele -o tele.png
```

## Styling

- Glass types are colour-coded using the nd/vd values from the `glass_catalog`
  section.
- Ray colours follow the field angle (low = blue, high = red).
- Aspheric surfaces are drawn from the sag function (see the asphere rendering
  in `internal/render`).

## Invalid fan rays

A fan ray whose path records an error code on any surface (e.g. `aperture_stop`,
`missed_surface`, `total_internal_reflection`, `glass_path_too_short`,
`glass_path_too_long`) is considered invalid. By default such fan rays are not
drawn at all; pass `--show-invalid-ray-fan` to draw them in full (the previous
behaviour) or `--clip-invalid-ray-fan` to stop the path at the first erroring
surface. Both are boolean toggles (default `false`): enabled by giving them
alone, disabled with `--show-invalid-ray-fan=false` / `--clip-invalid-ray-fan=false`.
The two flags are mutually exclusive. These options only affect fan
rays; traced `results[]`, marginal and chief rays are drawn as always.

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
```
