# RayWeaver

RayWeaver is a CLI ray tracing engine for optical systems, written in Go.

## Features

- Sequential ray tracing through spherical and aspheric surfaces
- Chief-ray (spot centroid) determination with hexagonal / polar / square pupil grids
- Paraxial (first-order) analysis: EFL, principal points, f/#, entrance/exit pupils
- Thin-film coating analysis via the transfer-matrix method
- SVG cross-section diagram generation
- Support for decentered and tilted elements
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
```

## Subcommands

| Subcommand | Description |
|---|---|
| `chief` | Determine chief ray and pupil grid for each field. Flags: `--clear-aperture` (set diameters from grid extents), `--marginal-rays` (add marginal rays to output). |
| `trace` | Trace ray(s) through the system and output intersection data per surface. |
| `paraxial` | First-order properties: EFL, BFL, FFL, principal points, pupil positions, f/#. |
| `tmm` | Thin-film coating analysis: reflectance, transmittance, phase. |
| `plot` | Generate SVG cross-section diagram. Flags: `-o` (output file), `--lens-width`, `--ray-width`, `--scale`, `--right-margin`. |

## Pipeline examples

```sh
# Ray-path trace
./rayweave trace < samples/us2645157.yaml

# Chief ray with spot diagrams + paraxial analysis
./rayweave chief < samples/us2645157.yaml | tee chief-result.yaml \
  | ./rayweave paraxial

# SVG raytrace diagram
cat samples/us2645157.yaml \
  | ./rayweave chief --clear-aperture \
  | ./rayweave chief --marginal-rays \
  | ./rayweave trace \
  | ./rayweave plot -o diagram.svg

# TMM coating analysis
./rayweave tmm < samples/ar-coating.yaml
```

## Sample data

The [`samples/`](samples/) directory contains:

- `us2645157.yaml` — triplet-derivative lens from US patent 2,645,157 (MIT license, ©2014 Daniel J. Reiley). Converted from a ZMX file obtained from [lens-designs.com](https://www.lens-designs.com/), validated against [LensForge](https://www.ripplon.com/LensForge/) trace output.
- `ar-coating.yaml` — single-layer MgF2 AR coating on N-SK16.
- `dielectric-mirror.yaml` — 9-layer quarter-wave Bragg reflector (SiO2/TiO2).
- `run-demo.bash` — end-to-end demo script producing spot diagrams, SVG, and TMM results.
- `README.md` — detailed documentation of all sample files and workflow.

The [`samples/`](samples/) directory also includes generated artifacts (spot-diagram data, SVG diagrams, chief-ray results) produced by the demo pipeline.

## Units

All units are millimetres (wavelengths, thicknesses, radii, coordinates). Coating layer thicknesses are in nanometres (converted internally). The Z axis is the optical axis (positive right). Surface 0 is the implicit object plane.

## Dependencies

| Library | License |
|---|---|
| [gopkg.in/yaml.v3](https://github.com/go-yaml/yaml) | MIT |

## License

This project is MIT licensed. The sample lens data in `samples/us2645157.yaml` is derived from US patent 2,645,157 and carries the original MIT license (© 2014 Daniel J. Reiley).
