# RayWeaver

RayWeaver is a CLI ray tracing engine for optical systems, written in Go.

## Features

- Sequential ray tracing through spherical and aspheric surfaces
- Chief-ray (spot centroid) determination with hexagonal / polar / square pupil grids
- Paraxial (first-order) analysis: EFL, principal points, f/#, entrance/exit pupils
- Thin-film coating analysis via the transfer-matrix method
- SVG cross-section diagram generation
- Support for decentered and tilted elements
- Folded systems (mirrors): positive thicknesses only, fold via `decenter: [{tilt: [0, 180, 0], reflect: true}]`, fold-aware paraxial/chief/ray tracing
- Glass dispersion via Sellmeier or Cauchy models
- Jones-matrix polarization tracking
- Parallel computation for pupil grids
- YAML-based input/output, pipeable between subcommands

## Build

```sh
go build -o rayweave ./cmd/rayweave/
```

No external dependencies beyond `gopkg.in/yaml.v3`.

## Quick start

```sh
./rayweave trace < samples/us2645157.yaml
bash samples/run-demo.bash
bash samples/optimize-demo.bash
```

## Subcommands

Subcommands are grouped by their role in the data flow
`system → ray bundle → quantities`, with each stage piping YAML to the next.

| Category | Subcommand | Role | Input → Output |
|---|---|---|---|
| Data | `import` | Convert an external lens file into the internal YAML format. | ZMX / SEQ / LEN → YAML system |
| Propagation | `trace` | Trace individual rays and report per-surface intersection data (low-level). | rays → per-surface results |
| Propagation | `chief` | Sample the beam for each field: chief ray, pupil grid, marginal rays, spot statistics and OPL. Flags: `--clear-aperture` (compute the beam footprint; `--shrink` to also reduce diameters to it, `--clear-aperture-margin-mm`, `--clear-aperture-rays`), `--marginal-rays`, `--pass-through N`, `--config ID`, `--wl`. | system + fields → chief_rays / grid |
| Analysis | `paraxial` | First-order / cardinal properties: EFL, BFL, FFL, principal points, pupil positions, f/#. | system → paraxial_result |
| Analysis | `tmm` | Thin-film coating analysis: reflectance, transmittance, phase. | system + coating → R/T/phase |
| Transform | `scale` | Uniformly scale a system so its EFL equals `--efl TARGET` (exact; preserves f/#). Useful for building a starting point before optimizing. | system → scaled system |
| Synthesis | `optimize` | DLS optimization of lens surfaces. Reads `optimization` and `configs` sections from YAML. `--verbose` also emits a per-term merit breakdown. | system + merit → optimized system |
| Presentation | `plot` | Render an SVG or PNG cross-section diagram. Flags: `-o file.svg|.png`, `--lens-width`, `--ray-width`, `--scale`, `--right-margin`, `--config`. | system + rays → diagram |

## Pipeline examples

```sh
# Ray-path trace
./rayweave trace < samples/us2645157.yaml

# Chief ray with spot diagrams + paraxial analysis
./rayweave chief < samples/us2645157.yaml | tee chief-result.yaml \
  | ./rayweave paraxial

# SVG raytrace diagram (centroid-based chief rays)
cat samples/us2645157.yaml \
  | ./rayweave chief --clear-aperture \
  | ./rayweave chief --marginal-rays \
  | ./rayweave trace \
  | ./rayweave plot -o diagram.svg

# PNG raytrace diagram (same pipeline, just change extension)
cat samples/us2645157.yaml \
  | ./rayweave chief --clear-aperture \
  | ./rayweave chief --marginal-rays \
  | ./rayweave trace \
  | ./rayweave plot -o diagram.png

# SVG raytrace diagram (stop-centre chief rays via --pass-through)
cat samples/us2645157.yaml \
  | ./rayweave chief --pass-through 5 --clear-aperture \
  | ./rayweave chief --marginal-rays \
  | ./rayweave trace \
  | ./rayweave plot -o diagram-stop.svg

# DLS optimization
./rayweave optimize < samples/optimize-demo.yaml > optimized.yaml

# DLS optimization with verbose progress (JSONL on stderr)
./rayweave optimize --verbose < samples/optimize-demo.yaml > optimized.yaml

# DLS optimization with progress logged to a file (JSONL)
./rayweave optimize --log /tmp/opt-progress.jsonl < samples/optimize-demo.yaml > optimized.yaml

# Scale a reference design to a target focal length, then optimize it
cat reference25mm.yaml | ./rayweave scale --efl 50 | ./rayweave optimize > optimized.yaml

# TMM coating analysis
./rayweave tmm < samples/ar-coating.yaml
```

## Optimization variables

`optimize` accepts `curvature`, `thickness`, `diameter`, `nd`/`vd` (glass), and —
on aspheric surfaces (`asphere_polynomial`) — `conic` and the even polynomial
coefficients `a4`/`a6`/`a8`/`a10`/`a12` (also addressable as
`coefficient_0`…`coefficient_4`). For example:

```yaml
optimization:
  variables:
    - {name: s1_conic, target: {type: surface, id: 1, param: conic}, min: -1, max: 1, active: true}
    - {name: s1_a4,    target: {type: surface, id: 1, param: a4},    min: -1e-3, max: 1e-3, active: true}
```

Notes:

- Constraint kinds: `equality`, `inequality_upper`, `inequality_lower`, `band`,
  `fuzzy`. Multiple `equality` constraints are supported (satisfiable targets
  converge; an unreachable target is reported with a warning).
- `optimization.aperture_margin` is clamped to ≥ 1.0 (smaller values make the
  pupil grid smaller than the aperture and stall DLS).
- `configs[].ray_paths` is render-only metadata; the optimizer ignores it.
- `edge_thickness` constraints take the back surface explicitly via `surface2`.


## Sample data

The [`samples/`](samples/) directory contains:

- `us2645157.yaml` — triplet-derivative lens from US patent 2,645,157 (MIT license, ©2014 Daniel J. Reiley). Converted from a ZMX file obtained from [lens-designs.com](https://www.lens-designs.com/), validated against [LensForge](https://www.ripplon.com/LensForge/) trace output.
- `us2645157-degraded.yaml` — same triplet with perturbed curvatures (pin-blur starting state) and `optimization` + `configs` sections for the `optimize` subcommand.
- `optimize-demo.bash` — draws SVG cross-sections of the initial (degraded) and optimized lens systems, demonstrating before/after comparison.
- `ar-coating.yaml` — single-layer MgF2 AR coating on N-SK16.
- `dielectric-mirror.yaml` — 9-layer quarter-wave Bragg reflector (SiO2/TiO2).
- `run-demo.bash` — end-to-end demo script producing spot diagrams, SVG, and TMM results.
- `optimize-demo.bash` — draws before/after SVG cross-sections of the US2645157 triplet (degraded → optimized).
- `README.md` — detailed documentation of all sample files and workflow.

The [`samples/`](samples/) directory also includes generated artifacts (spot-diagram data, SVG diagrams, chief-ray results) produced by the demo pipeline.

## Units

All units are millimetres (wavelengths, thicknesses, radii, coordinates). Coating layer thicknesses are in nanometres (converted internally). The Z axis is the optical axis (positive right). Surface 0 is the implicit object plane.

## Dependencies

| Library | License |
|:---:|:---:|
| [gopkg.in/yaml.v3](https://github.com/go-yaml/yaml) | MIT |
| [golang.org/x/image](https://pkg.go.dev/golang.org/x/image) | BSD |

## License

This project is MIT licensed. The sample lens data in `samples/us2645157.yaml` is derived from US patent 2,645,157 and carries the original MIT license (© 2014 Daniel J. Reiley).
