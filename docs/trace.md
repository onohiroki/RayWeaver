# `rayweave trace` — trace individual rays

Traces one or more rays through the system and reports per-surface intersection
data. This is the lowest-level propagation command; `chief`, `paraxial` and the
optimizer all build on the same engine.

```
rayweave trace [--config ID] [--glass-dir DIR] [--verbose] [--details] < system.yaml
rayweave trace single [FLAGS] < system.yaml
```

Without `single`, `trace` propagates every ray in the input `rays` section plus the
per-field chief rays from `chief_rays`, appending the results to any existing
`results[]`. The `trace single` form traces exactly one ray specified via CLI
flags (or the `trace_single:` YAML section) and **overwrites** `results[]` with a
single entry.

## Options

| Flag | Description |
|---|---|
| `--config ID` | select a config by id (multi-config mode); defaults to `configs[0]` |
| `--glass-dir DIR` | AGF glass catalog directory (for resolving material names) |
| `--lenient BOOL` | trace leniently (`true`/`false`): skip aperture/glass-path checks and continue past missed surfaces and TIR (default: `rays.lenient` from the input YAML, else `false`; written back into the output) |
| `--verbose` | print per-ray trace info to stderr (JSONL) |
| `--details` | populate per-surface detail fields (AOI, n1/n2, Fresnel, coating) in the output YAML (see [Per-surface detail](#per-surface-detail)) |

## Input YAML — `rays` section

```yaml
rays:
  polarization: [1, 0, 0, 1]      # Jones vector [ReEx, ImEx, ReEy, ImEy]
  lenient: false                  # lenient tracing (--lenient true/false overrides)
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

---

## Single-ray tracing — `trace single`

`trace single` traces one ray from a specification given on the command line,
so it needs no `rays`/`chief_rays` section in the input:

```
rayweave trace single [--config ID] [--glass-dir DIR] [FLAGS] < system.yaml
```

```
rayweave trace single --origin 0,5,-100 --direction 0,0,1 < lens.yaml
rayweave trace single --origin 0,5,-100 --angle-yz 5 < lens.yaml | rayweave list rays
rayweave trace single --origin 0,5,-100 --aim 0,2.5,25 --path 0,1,2,3,4 < lens.yaml
```

### Options

| Flag | Description |
|---|---|
| `--origin X,Y,Z` | ray origin (mm); default `0,0,0` |
| `--direction DX,DY,DZ` | ray direction vector (normalised internally); default `0,0,1` |
| `--aim X,Y,Z` | aim target: the direction is auto-computed from the origin toward the point |
| `--angle-yz DEG` | incidence angle in the YZ plane (degrees); sets direction `(0, sin θ, cos θ)` |
| `--pass-through S:Y:X` | solve the direction so the ray passes through `(x, y)` on surface `S` (in the YZ-plane convention, `Y` comes before `X`) |
| `--path 0,1,2,...` | surface path (comma-separated IDs; default: sequential `0,1,…,N`) |
| `--wavelength MM` | wavelength in mm; CLI > `trace_single.wavelength` > `chief.reference_wavelength` > 587.56 nm |
| `--id NAME` | ray ID (default: `trace_single`) |
| `--details` | populate per-surface detail fields in the output YAML |
| `--verbose` | print a per-surface dump to stderr |
| `--lenient BOOL` | skip aperture/glass-path checks (default: `trace_single.lenient`, else `false`) |
| `--config ID` | select a config by id (multi-config mode) |
| `--glass-dir DIR` | AGF glass catalog directory |

### Ray specification precedence

Only one of `--direction`, `--aim`, `--angle-yz`, `--pass-through` takes effect;
`--aim` wins, then `--direction`, then `--angle-yz`, then `--pass-through`
(which itself derives the direction from `--angle-yz`, default 0°). Unset flags
fall back to the `trace_single:` YAML section, then to the built-in defaults.
Without any specification the ray travels along `+Z` from the origin.

### Input YAML — `trace_single` section

Every flag has a YAML counterpart, so the specification can live in the input
document (CLI still wins when both are given, and the effective values are
written back into the output):

```yaml
trace_single:
  origin: [0, 5, -100]      # ray origin (mm)
  direction: [0, 0, 1]      # direction / aim / angle_yz / pass_through are alternatives
  aim: [0, 2.5, 25]
  angle_yz: 5               # degrees
  pass_through: [5, 2.5, 0] # [surface, Y, X]
  path: [0, 1, 2, 3, 4]     # surface IDs (default: sequential)
  wavelength: 0.00058756    # mm
  id: "my_ray"
  lenient: false
  details: false
```

### Output

`trace single` writes pipeline-compatible YAML (so it pipes into `plot` and
`list rays`) with `results[]` replaced by the single traced ray. The default
polarization is right-circular (`rays.polarization` in the input overrides it).

When the trace fails (a missed surface, an aperture stop, TIR, a glass-path
violation), the stopping surface is **retained in the output** with its
`interaction` set to `MISSED`, zero intensity/Jones, and the `error_code`
filled in; the result carries the human-readable `error`. A warning is printed
to stderr:

```
rayweave[trace]: Warning: ray "trace_single" error: ray missed surface
```

### Verbose per-surface dump

`--verbose` prints one line per surface to stderr with the interaction,
position, and (when available) the angle of incidence, refractive indices and
Fresnel/coating coefficients, marking erroring surfaces:

```
  s0    TRANSMIT  y=  2.0000  z=-100.0000
  s1    TRANSMIT  y=  2.0000  z=  0.1963  θ=11.21°  n1=1.000  n2=1.639  Rs=0.0000  Rp=0.0000  Ts=0.7523  Tp=0.7637
  ...
  OPL_total = 132.0502 mm  Is=1.0000  Ip=1.0000
```

A line such as `[ERROR: aperture_stop]` is appended to the offending surface and
a trailing `!!! Trace stopped: <code>: <message>` line explains the failure.

---

## Per-surface detail

The `--details` flag (on both `trace` and `trace single`) enriches each output
surface with `angle_of_incidence` (degrees), the incident/emergent refractive
indices `n1`/`n2`, the Fresnel amplitude coefficients `rs`/`rp`/`ts`/`tp`
(`intensity_rs`/`intensity_rp` are the power reflections), and, for coated
surfaces, the coating power reflection/transmission `coating_rs`/`coating_rp`/
`coating_ts`/`coating_tp`. These are the same fields `list rays` renders in its
detail columns and that `trace single --verbose` prints.

## Method

The numerical details (surface intersection, Snell's law, Fresnel coefficients,
fold handling, ghost rays) are described in
[methods/ray-tracing.md](methods/ray-tracing.md).
