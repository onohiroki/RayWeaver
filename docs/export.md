# `rayweave export` — export to external lens files

Exports a RayWeaver system back out as a native lens file for another optical
design tool — the inverse of `rayweave import`. The output is plain text
written to stdout.

```
rayweave export --format zemax < system.yaml > out.zmx
rayweave export --format codev < system.yaml > out.seq
rayweave export --format oslo  < system.yaml > out.len
```

Foreign-format output is exempt from the CLI/YAML precedence rules (like
`import`): the flags are pure output settings and nothing is written back into
the text output.

## Options

| Flag | Description |
|---|---|
| `--format zemax\|codev\|oslo` | output format (required) |
| `-o, --output FILE` | write the foreign format to FILE and pass the input YAML through to stdout unchanged (like `plot -o`); without it the foreign format goes to stdout |
| `--config ID` | export a single config; otherwise ZEMAX/CODE V export every config, OSLO exports config 0 |
| `--nd-vd` | CODE V: write every glass as its inline `nd:vd` model form instead of the catalog name |
| `--glass-dir dir` | load an AGF glass catalog from a directory (resolves glass names / OSLO model-glass indices) |

With `-o FILE` the pipeline keeps flowing:

```
rayweave export --format zemax -o out.zmx < system.yaml | rayweave plot -o lens.svg
```

## Config mapping

| Format | No `--config` | With `--config` |
|---|---|---|
| ZEMAX | every config as ZEMAX multi-config: base geometry (config 1) + `MNUM`/`CONFIG` + `THIC`/`SDIA` overrides | the selected config only |
| CODE V | every config as zoom positions: `ZOOM n` header + `ZOO <code> S<n> <v1> ... <vn>` rows | the selected config only |
| OSLO | config 0 | the selected config only |

The base (first) config always maps to ZEMAX config 1 / CODE V zoom position 1.
Per-config curvature/conic/asphere differences cannot be expressed in ZEMAX
`THIC`/`SDIA` (thickness and diameter only) and are reported on stderr.

## Supported surface features

| Feature | ZEMAX | CODE V | OSLO |
|---|---|---|---|
| Sphere / conic | `STANDARD` + `CONI` | `S` + `CON`/`K` | `RD`/`TH` (sphere only) |
| Even polynomial asphere | `EVENASPH` + `PARM` | `ASP` + `A..J` | — (warned) |
| Per-surface decenter | `COORDBRK` | `DAR` + `YDE`/`XDE`/`ZDE`/`ADE`/`BDE`/`CDE` | — (warned) |
| Glass (catalog / model) | `GLAS <key>` / inline `nd,vd` | inline `nd:vd` | `GLA <key>` / `<nd> <nF> <nC>` |
| Fields | `FTYP` + `XFLD`/`YFLD`/`FWGT` | `YAN`/`XAN` or `YIM` + `WTF` | `F` (angle only) |
| Field weights / vignetting | `FWGT` + `VDXN..VANN` | `WTF` + `VUX`/`VLX`/`VUY`/`VLY` | weights only |
| Wavelengths | `WAVL`/`WWGT`/`PWAV` | `WL`/`WTW`/`REF` | `WV`/`WW` |
| Aperture stop | `STOP` | `STO` | `AST` |
| Diameters | `DIAM` (semi) | `CIR` (semi) | `AP` (semi) |
| Multi-config | `MNUM`/`THIC`/`SDIA` | `ZOOM`/`ZOO` | single config |

Diameters export as semi-diameters (`DIAM`, `CIR`, `AP`), matching the import
convention. The ZEMAX `SDIA` override row carries the full diameter to match
the importer's reading.

## CODE V glass names

CODE V references glasses without the separators used in AGF names and with the
manufacturer appended after an underscore (`N-BK7` from Schott →
`NBK7_SCHOTT`). The CODE V exporter follows that convention:

- the glass name is uppercased with hyphens and underscores removed
  (`N-BK7` → `NBK7`, `H-ZF72` → `HZF72`);
- when the glass catalog (`glass_catalog` entries or `--glass-dir`) knows the
  manufacturer, it is appended: `NBK7_SCHOTT`; a glass of unknown manufacturer
  is written as just the normalized name (`NBK7`);
- `--nd-vd` instead resolves every glass to its inline `nd:vd` model form
  (`1.5168:64.17`); an unresolvable key falls back to the CODE V name with a
  warning.

On re-import, the importer resolves these names back to the catalog glass
(`NBK7_SCHOTT` → N-BK7), so the round trip keeps the dispersion.

## CODE V vignetting

RayWeaver vignetting (`fields[].vignetting`, ZEMAX convention: decenter /
compression as fractions of the entrance-pupil radius) maps to CODE V's four
per-field marginal-ray factors:

```
VUY = compressionY - decenterY     VUX = compressionX - decenterX
VLY = compressionY + decenterY     VLX = compressionX + decenterX
```

Non-zoom CODE V writes the slot-aligned vector form (`VUY <f0> <f1> ...`);
zoom writes `ZOO VUY F<n> <v1> <v2> ...` per field across the positions. The
vignetting rotation term (`tangent`) is not representable in CODE V and is
warned.

## Limitations (reported on stderr)

- Folded mirrors (`reflect: true`) are exported as transmit surfaces — the fold
  model is not unfolded back into negative spacings.
- Zernike aspheres (`asphere_zernike`) export as sphere + conic.
- OSLO cannot represent conics, aspheres or decenters (exported as spheres) and
  carries a single config.
- Object-height (finite-conjugate) fields export only to ZEMAX (`FTYP 1`); CODE V
  and OSLO skip them with a warning.
- Multiple decenter steps on one surface collapse to the first (CODE V `DAR`
  carries one block per surface; consecutive ZEMAX `COORDBRK` surfaces would
  otherwise be dropped on re-import).
- Per-config fields/wavelengths differences use the first config's values.

## Examples

```sh
rayweave export --format zemax < system.yaml > out.zmx
rayweave export --format codev < system.yaml > out.seq
rayweave export --format oslo --config config1 < system.yaml > out.len
rayweave export --format zemax -o out.zmx < system.yaml | rayweave plot -o lens.svg
```

Round-trip: `rayweave export --format codev < system.yaml | rayweave import --format codev`

See also [`docs/import.md`](import.md) for the reverse direction.
