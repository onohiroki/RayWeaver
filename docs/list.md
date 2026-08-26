# `rayweave list` — read-only system data listing

`rayweave list` displays a read-only summary of an optical system's definition
data. Unlike the pipeline subcommands, it never traces rays and prints
formatted tables by default, not pipeline YAML.

```
rayweave list [--format table|yaml|json|csv] [--config ID]
              [--glass-dir DIR] [--curvature] [TARGET...] < input.yaml
```

---

## 1. Basic usage

Without arguments, `list` shows both surfaces and glasses of the first (or
only) config:

```sh
rayweave list < lens.yaml
```

```
Surfaces:
ID  Type     Radius[mm]  Thickness[mm]  Material  Diameter[mm]
 1  sphere    10.287149       1.524000  SK18         10.000000
 2  sphere  -239.396795       2.336800  AIR          10.000000
 3  sphere   -12.826987       0.508000  SF12          6.000000
 4  sphere    10.591718       1.498600  AIR           6.000000
 5  sphere          inf       1.016000  AIR           3.782530
 6  sphere    61.845629       1.524000  SK18          6.000000
 7  sphere   -10.007486      21.366952  AIR           6.000000
 8  sphere          inf              0  AIR                  0

Glasses:
Name  Type         nd         vd  587.56nm
SK18  model  1.638540  55.420000  1.638540
SF12  model  1.648310  33.840000  1.648310
```

Targets can be listed explicitly:

```sh
rayweave list surfaces < lens.yaml       # surfaces only
rayweave list glasses < lens.yaml        # glasses only
rayweave list surfaces glasses < lens.yaml  # both (same as default)
```

---

## 2. Targets

| Target | Description |
|---|---|
| `surfaces` | Surface table of the selected config. Includes asphere coefficients and thickness differences when applicable. |
| `glasses` | Refractive-index table of the glasses used by the selected config. |

Default (no target arguments): `surfaces glasses`.

---

## 3. Options

| Flag | Description |
|---|---|
| `--format table\|yaml\|json\|csv` | Output format (default: `table`). |
| `--config ID` | Select one config by id. Without it, all configs are considered. |
| `--glass-dir DIR` | Load an AGF glass catalog directory (resolves glass names). |
| `--curvature` | Show curvature [1/mm] instead of radius [mm]. |

Flags may appear before or after the target arguments.

---

## 4. Surfaces section

Columns: `ID`, `Type`, `Radius[mm]` (or `Curvature[1/mm]` with `--curvature`),
`Thickness[mm]`, `Material`, `Diameter[mm]`. Object plane 0 is excluded.

A flat surface has no finite radius: it is shown as `inf` in tables and
omitted in yaml/json/csv.

### Material display

| Source | Display |
|---|---|
| Air (no material) | `AIR` |
| Catalogue glass | Resolved name (+ manufacturer when known) |
| Inline model glass | `nd:vd` (e.g. `1.6385:55.42`) |
| Unresolved key | Raw key string |

### Asphere Coefficients

When the config contains aspheric surfaces (`asphere_polynomial` /
`asphere_zernike`), an **Asphere Coefficients:** section follows the
Surfaces section:

```
Asphere Coefficients:
ID  Type                Conic          A4          A6
 1  asphere_polynomial      0  0.0000e+00  0.0000e+00
```

Columns extend to the highest even order present (A4..A12). With no
aspheres the section is omitted entirely. Structured output carries it
under `asphere_coefficients` only when non-empty.

### Thickness Differences

Without `--config` and with multiple configs whose common surface IDs
carry different thickness across configs, a **Thickness Differences:**
section compares them:

```
Thickness Differences:
Config  Name  Surface 3  Surface 6
     0  Wide  20.000000  50.000000
     1  Mid   40.000000  30.000000
     2  Tele  60.000000  10.000000
```

Rows are configs; columns are the differing surfaces. The section is
skipped when `--config` is given or no differences exist. Structured
output carries it under `thickness_differences` only when non-empty.

---

## 5. Glasses section

Columns: `Name`, `Mfr` (only when a glass carries a manufacturer),
`Type`, `nd`, `vd`, and one `n` column per collected wavelength
(longest wavelength first).

### Row ordering

1. Catalogue glasses used by the selected config's surfaces (first-use
   order).
2. Unresolved keys (surface `material` references not found in the
   catalog).
3. Remaining `glass_catalog` entries in YAML declaration order.

### nd and vd

| Situation | nd | vd |
|---|---|---|
| Catalogue glass with dispersion formula | Computed at d-line | Computed from n(d), n(F), n(C) |
| Inline model glass (`nd`/`vd` set) | Stored value | Stored value |
| Constant-index glass | Stored value | `-` |
| Unresolved key | `-` | `-` |

The d-line index and Abbe number are obtained via `glass.NDVD`: stored
values are returned when present; otherwise they are computed from the
dispersion data.

### Wavelength columns

Wavelengths are the union of `chief.reference_wavelength` and every
config's `wavelengths[].value`, deduplicated within 1e-12 mm. When the
input carries no wavelength, the d line (587.56 nm) is used as a
fallback.

### Type labels

| Glass kind | Type |
|---|---|
| Catalogue glass | Dispersion formula name (e.g. `sellmeier_1`) |
| Constant index | `constant` |
| nd/vd model glass | `model` |
| Tabulated index table | `tabulated` |
| Unresolved key | `-` |

Unresolved keys produce a stderr warning instead of aborting.

---

## 6. Output formats

### Table (default)

Human-readable, right-aligned numeric columns:

```
rayweave list < lens.yaml
rayweave list --config wide --format table < lens.yaml
```

### CSV

```
rayweave list --format csv < lens.yaml
```

```
surfaces:
id,type,radius,thickness,material,diameter
1,sphere,10.2871491742,1.524,SK18,10
2,sphere,-239.39679547519998,2.3368,AIR,10
...

glasses:
name,mfr,type,nd,vd,587.56nm
SK18,,model,1.63854,55.42,1.63854
SF12,,model,1.64831,33.84,1.64831
```

### YAML

```
rayweave list --format yaml < lens.yaml
```

```yaml
surfaces:
    - id: 1
      type: sphere
      radius: 10.2871491742
      thickness: 1.524
      material: SK18
      diameter: 10
    ...

glasses:
    - name: SK18
      type: model
      nd: 1.63854
      vd: 55.42
      indices:
        - wavelength_nm: 587.56
          "n": 1.63854
```

### JSON

```
rayweave list --format json < lens.yaml
```

```json
{
  "surfaces": [
    {"id": 1, "type": "sphere", "radius": 10.287, "thickness": 1.524,
     "material": "SK18", "diameter": 10}
  ],
  "glasses": [
    {"name": "SK18", "type": "model", "nd": 1.63854, "vd": 55.42,
     "indices": [{"wavelength_nm": 587.56, "n": 1.63854}]}
  ]
}
```

---

## 7. Examples

```sh
# Full listing (surfaces + glasses)
rayweave list < lens.yaml

# Surfaces only with curvature
rayweave list surfaces --curvature < lens.yaml

# Multi-config: thickness differences shown automatically
rayweave list < zoom.yaml

# Single config: thickness differences suppressed
rayweave list --config tele < zoom.yaml

# CSV for spreadsheet import
rayweave list --format csv < lens.yaml > lenses.csv

# Pipe into query for programmatic access
rayweave list --format yaml < lens.yaml | rayweave query -r glasses[0].nd
```
