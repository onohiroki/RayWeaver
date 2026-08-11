# `rayweave trace` — trace individual rays

Traces one or more rays through the system and reports per-surface intersection
data. This is the lowest-level propagation command; `chief`, `paraxial` and the
optimizer all build on the same engine.

```
rayweave trace [--config ID] [--glass-dir DIR] [--verbose] < system.yaml
```

## Options

| Flag | Description |
|---|---|
| `--config ID` | select a config by id (multi-config mode); defaults to `configs[0]` |
| `--glass-dir DIR` | AGF glass catalog directory (for resolving material names) |
| `--lenient` | trace leniently: skip aperture/glass-path checks and continue past missed surfaces and TIR (equivalent to `rays.lenient: true`; written back into the output) |
| `--verbose` | print per-ray trace info to stderr (JSONL) |

## Input YAML — `rays` section

```yaml
rays:
  polarization: [1, 0, 0, 1]      # Jones vector [ReEx, ImEx, ReEy, ImEy]
  lenient: false                  # lenient tracing (--lenient overrides)
  rays:
    - id: "my_ray"
      wavelength: 0.00058756      # mm
      initial:
        origin: [0, 0, -100]      # [x, y, z] start point (mm)
        direction: [0, 0.1, 1]    # [dx, dy, dz] direction vector (normalised)
      path: [0, 1, 2, ..., N]     # surface IDs to trace (0 = object plane)
```

`--glass-dir` is written back into the output's `glass_catalog.directory`
(CLI/YAML rule).

Alternative ray definitions:

```yaml
aim: [x, y, z]                  # set the direction toward a target point
pass_through:
  surface: 5                    # find origin (or direction) so the ray
  coordinate: [0, 8.9, 0]       #   passes through (x, y, z) on surface N
  variable: "origin"            #   "origin" (default) or "direction"
```

The `path` must start with surface `0` (the implicit object plane). Per-surface
errors (`missed_surface`, `aperture_stop`, `total_internal_reflection`,
`glass_path_too_short`, `glass_path_too_long`) are reported on stderr with the
ray id; a `--verbose` run emits a JSONL error object per failed ray.

## Output

YAML with a `results[]` array, one entry per input ray. Each entry contains
per-surface data: `position`, `direction`, `normal` (via geometry), Fresnel
coefficients, `interaction` (`TRANSMIT`/`REFLECT`), `thickness`, `opl`
(optical path length, cumulative), `intensity_s`/`intensity_p`, and the Jones
vector. The final surface's intensity and OPL are also summarised on the
result.

## Piping

`chief` produces a `rays` section that `trace` consumes directly:

```sh
rayweave chief < lens.yaml | rayweave trace | rayweave plot -o lens.svg
```

## Method

The numerical details (surface intersection, Snell's law, Fresnel coefficients,
fold handling, ghost rays) are described in
[methods/ray-tracing.md](methods/ray-tracing.md).
