# RayWeave — sample data

This directory contains sample optical system data and a demo script for the
[RayWeave](https://github.com/hiroki/rayweaver) ray tracing toolkit.

## Source files (input data)

| File | Description |
|---|---|
| `us2645157.yaml` | Triplet-derivative lens from US patent 2,645,157 (MIT license, ©2014 Daniel J. Reiley). Converted from a ZMX file obtained from [lens-designs.com](https://www.lens-designs.com/). The original patent data was validated against [LensForge](https://www.ripplon.com/LensForge/) trace output (`lens-designs.com/US02645157-1-Trace*.txt`) — ray positions at every surface are consistent. |
| `ar-coating.yaml` | Single-layer MgF2 anti-reflection coating on N-SK16 glass, quarter-wave at 550 nm. |
| `dielectric-mirror.yaml` | 9-layer quarter-wave Bragg reflector (SiO2/TiO2) on glass, design wavelength 550 nm. |
| `run-demo.bash` | End-to-end demo script. |

## Generated files (output of `run-demo.bash`)

| File | Description |
|---|---|
| `us2645157-chief-result.yaml` | Chief-ray computation result with pupil grid and spot statistics. |
| `spot-00.txt` / `spot-f16.txt` / `spot-f24.txt` | Grid-point image coordinates `(image_x, image_y, intensity)` for each field. |
| `spot-00.png` / `spot-f16.png` / `spot-f24.png` | Spot diagrams rendered by gnuplot. |
| `us2645157.svg` | SVG cross-section raytrace diagram. |

## Run the demo

```sh
./rayweave trace < samples/us2645157.yaml
bash samples/run-demo.bash
```

## What `run-demo.bash` does

1. **Chief ray computation** — `rayweave chief` finds the chief ray (spot centroid) and a hexagonal pupil grid for each field (0°, 16°, 24°). Result saved as `us2645157-chief-result.yaml`.

2. **Spot diagram extraction** — Grid-point positions at the reference surface are extracted per field into TSV files `(image_x, image_y, intensity)`.

3. **Spot diagram PNG output** — Gnuplot renders each spot diagram as a PNG image.

4. **Ray-path traces** — `rayweave trace` shows intersection coordinates at every surface in a Markdown table, both on the original YAML and on the chief-result YAML.

5. **Paraxial analysis** — `rayweave paraxial` computes first-order properties (EFL, principal points, f/#, entrance/exit pupils) with and without chief-ray data.

6. **SVG raytrace diagram** — `rayweave chief --clear-aperture --marginal-rays` determines clear apertures from the pupil grid and adds marginal rays; `rayweave trace` traces them; `rayweave plot` draws the SVG.

7. **TMM coating analysis** — `rayweave tmm` computes reflectance/transmittance for the AR coating and the dielectric mirror via the transfer-matrix method.

## Notes

- All units are millimetres (thicknesses, radii, coordinates, wavelengths).
- Surface 0 (object plane) is implicit — the system has infinite conjugate (object at infinity).
- The stop is on surface 5. ZEMAX DIAM values are semi-diameters; the YAML stores full diameters.
