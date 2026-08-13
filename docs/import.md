# `rayweave import` — import external lens files

Converts a lens file from another optical design tool into RayWeaver's YAML
format (multi-config format). If the lens data includes field angles, chief
rays and marginal rays are computed automatically so the output can be piped
directly into `rayweave trace` and `rayweave plot`.

```
rayweave import --format zemax < lens.zmx > system.yaml
rayweave import --format oslo  < lens.len > system.yaml
rayweave import --format codev < lens.seq > system.yaml
```

## Options

| Flag | Description |
|---|---|
| `--format zemax\|oslo\|codev` | input format (required) |
| `--config-id name` | config id in the output YAML (default `config1`) |
| `--config-name name` | config display name (default `Config1`) |
| `--no-chief` | skip automatic chief ray computation |
| `--glass-dir dir` | load an AGF glass catalog from a directory (for resolving glass names) |

## Supported surface types

| Format | Input keywords | RayWeaver surface type |
|---|---|---|
| ZEMAX | `STANDARD`, `EVENASPH` | `sphere`, `asphere_polynomial` |
| OSLO | `SRF` (`RD`/`TH`/`GL`/`AP`/`CV`), `NXT` | `sphere` |
| CODE V | `RDY`/`THI`/`GLA`/`CCY`/`DIA`/`STO`; `ASP`/`AD`/`AE`/`AF` | `sphere`, `asphere_polynomial` |

Conic constants (`CCY`, `CONIC`) map to the `conic` field. Even asphere
polynomial coefficients (`AD`/`AE`/`AF`, `A4`/`A6`/`A8`/`A10`/`A12`) map to the
`coefficients` array of `asphere_polynomial`.

## ZEMAX fields and vignetting

ZEMAX field data is read from both the legacy slot rows (`XFLN`/`YFLN`,
`FWGN` weights) and the modern 2016+ rows (`XFLD`/`YFLD`, `FWGT` weights). The
values are interpreted by the system field type `FTYP[0]`:

| FTYP | ZEMAX meaning | RayWeaver field |
| --: | --- | --- |
| `0` | Angle (deg) | `angle_deg` (+ `direction` when `XFLD` ≠ 0) |
| `1` | Object height | `height` + `object_z` (object distance = surface-0 thickness) |
| `2` | Paraxial image height | `image_height` (chief ray resolved to that height) |
| `3` | Real image height | `image_height` |

Only the first `FTYP` value is used: the trailing values are internal
compatibility flags, not per-field field types. Field weights (`FWGN`/`FWGT`)
map to `fields[].weight`.

ZEMAX vignetting factors (`VDXN`/`VDYN`/`VCXN`/`VCYN`/`VANN`) are imported per
field as `fields[].vignetting` (`decenter_x`, `decenter_y`, `compression_x`,
`compression_y`, `tangent`) — an entrance-pupil ellipse clip in the ZEMAX
fraction-of-pupil convention. The `chief` (and `wavefront`) commands apply it as
a per-field entrance-pupil grid mask, so spot diagrams, PSF and wavefront
sampling honor the vignetted pupil. The legacy 24-slot `WAVM` wavelength table
is truncated at its trailing fill run (the unused-slot placeholder value is not
imported as real wavelengths); modern files use `WAVL`/`WWGT` directly. ZEMAX
`FNUM` and `ENPD`/`ENVD` set the F-number / entrance-pupil diameter used for
stop-aperture sizing when the file carries no per-surface diameters.

## Output

Writes a YAML document with a `configs[0]` section (id/name from the flags)
containing the converted `surfaces`. Unless `--no-chief` is given and the input
carries field data, a `chief_rays[]` section (per-field chief rays, pupils, spot
stats) plus a `rays` section carrying the marginal rays is added so the result
can be piped into `rayweave trace`.

Glass names found in the lens file are resolved against `--glass-dir` (AGF
catalog) or carried through as catalog entries. When `--glass-dir` is given,
the written YAML's `glass_catalog.directory` records it so the downstream
pipeline (`chief` / `trace` / `optimize` / …) resolves the same AGF catalog
without re-passing the flag.

## Examples

```sh
rayweave import --format zemax < lens.zmx > system.yaml
rayweave import --format oslo  < lens.len | rayweave trace
rayweave import --format zemax < lens.zmx | rayweave plot -o lens.svg
```

See `samples/` for imported designs and the `import` demo flows.
