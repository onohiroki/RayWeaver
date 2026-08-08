# RayWeaver documentation

RayWeaver is a CLI ray tracing engine for optical systems, written in Go. It
reads YAML from stdin, writes YAML to stdout, and pipes between subcommands:

```
system → ray bundle → quantities
```

## Subcommand manuals

Each subcommand has a usage manual describing its flags, input YAML structure,
output, and worked examples.

| Manual | Subcommand | Purpose |
|---|---|---|
| [import.md](import.md) | `rayweave import` | convert ZEMAX / OSLO / CODE V files to RayWeaver YAML |
| [trace.md](trace.md) | `rayweave trace` | trace individual rays and report per-surface data |
| [chief.md](chief.md) | `rayweave chief` | chief rays, pupil grids, spot statistics, ray fans, clear aperture |
| [paraxial.md](paraxial.md) | `rayweave paraxial` | first-order (paraxial) analysis: EFL, pupils, cardinal points |
| [tmm.md](tmm.md) | `rayweave tmm` | thin-film coating analysis (transfer-matrix method) |
| [plot.md](plot.md) | `rayweave plot` | SVG / PNG cross-section diagrams |
| [scale.md](scale.md) | `rayweave scale` | scale a system so its EFL matches a target |
| [optimize.md](optimize.md) | `rayweave optimize` | DLS (damped least squares) local optimization |
| [escape.md](escape.md) | `rayweave escape` | escape-function global optimization |
| [query.md](query.md) | `rayweave query` | read-only YAML/JSONL selector for pipelines |

## Calculation methods

The numerical methods behind the analyses and optimizations are described
separately from the command usage in [`methods/`](methods/README.md). These
documents explain *how* each quantity is computed, not *how to invoke* it.

- [methods/README.md](methods/README.md) — index and glossary
- [methods/ray-tracing.md](methods/ray-tracing.md) — sequential surface-by-surface tracing, Snell's law, aspheres, Fresnel, folds, TIR
- [methods/chief-rays-and-spot.md](methods/chief-rays-and-spot.md) — pupil grids, centroid chief ray, spot statistics, ray fans, clear aperture
- [methods/paraxial.md](methods/paraxial.md) — first-order ray trace, cardinal points, entrance/exit pupils, f/#
- [methods/merit-functions.md](methods/merit-functions.md) — merit terms (spot RMS, distortion, colour, Seidel, OPD RMS) and weighting
- [methods/dls-optimization.md](methods/dls-optimization.md) — damped least squares, Jacobian, augmented-Lagrangian constraints, damping control
- [methods/escape-function.md](methods/escape-function.md) — escape-function global optimization
- [methods/glass-dispersion.md](methods/glass-dispersion.md) — Sellmeier / Schott / Cauchy / nd-vd models, glass hull
- [methods/thin-film-tmm.md](methods/thin-film-tmm.md) — transfer-matrix method for coatings
- [methods/efl-scaling.md](methods/efl-scaling.md) — uniform EFL scaling

## Conventions

- All lengths, radii, thicknesses and coordinates are in millimetres; wavelengths
  are in mm for surfaces and ray data, and in nanometres for coating layer
  thicknesses (converted internally).
- The Z axis is the optical axis, positive to the right. Surface 0 is the
  implicit object plane (no intersection or refraction).
- Surfaces use `curvature` as the primary field; `radius` in YAML is converted
  to curvature at parse time.
- Folded (mirror) systems use **positive thicknesses only**. A mirror is folded
  via `decenter: [{tilt: [0, 180, 0], scope: both}]` plus a top-level
  `reflect: true` (tilt in degrees). Negative thicknesses are parse-time errors.
- All output documents preserve the input document and add a section; pipelines
  such as `chief → trace → plot` and `chief → paraxial` therefore work.
