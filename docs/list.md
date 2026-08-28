# `rayweave list` — read-only system data listing

`rayweave list` displays a read-only summary of an optical system's definition
data. Unlike the pipeline subcommands, it never traces rays and prints
formatted tables by default, not pipeline YAML.

```
rayweave list [--format table|yaml|json|csv] [--config ID]
              [--glass-dir DIR] [--curvature] [--all] [--roles] [--summary]
              [TARGET...] < input.yaml
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
| `paraxial` | First-order optical properties (EFL, F/#, NA, EPD, BFL, …). With `--roles`, appends the per-element glass-role table. |
| `rays` | Ray trace results from the `results[]` section (requires `trace` / `trace single` output). |

Default (no target arguments): `surfaces glasses`.

```sh
rayweave list paraxial < lens.yaml            # first-order properties
rayweave list paraxial --roles < lens.yaml    # + element glass roles
rayweave chief | rayweave trace | rayweave list rays
```

---

## 3. Options

| Flag | Description |
|---|---|
| `--format table\|yaml\|json\|csv` | Output format (default: `table`). |
| `--config ID` | Select one config by id. Without it, all configs are considered. |
| `--glass-dir DIR` | Load an AGF glass catalog directory (resolves glass names). |
| `--curvature` | Show curvature [1/mm] instead of radius [mm]. |
| `--all` | For `glasses`: also list the `glass_catalog` entries not used by any surface. |
| `--roles` | For `paraxial`: append the per-element glass-role table. |
| `--summary` | For `rays`: show only the summary table (no per-surface detail). |

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

## 6. Rays section — ray trace results

The `rays` target renders the results of a previous `trace` / `trace single`
run (the `results[]` section). It never re-traces: the data comes straight from
the pipeline document.

```sh
rayweave trace single --origin 0,2,-100 --direction 0,0,1 < lens.yaml | rayweave list rays
rayweave chief | rayweave trace | rayweave list rays --summary
```

With no `results[]` present, `list rays` aborts with a hint:

```
rayweave[list]: Error: no ray results found (results[] is empty; run 'trace' or 'trace single' first)
```

### Summary table

One row per ray:

```
Ray Summary:
ID            λ[mm]          OPL[mm]        Is        Ip    Tcum s    Tcum p  Surf  Tx  Miss  Error
trace_single  5.8756e-04  132.050200  1.000000  1.000000  0.681460  0.700724     9   9     0       
chief_0deg    5.8756e-04  132.049964  1.000000  1.000000  0.694194  0.694194     9   9     0       
```

| Column | Meaning |
|---|---|
| `ID` | ray id |
| `λ[mm]` | ray wavelength |
| `OPL[mm]` | cumulative optical path length at the last surface |
| `Is`, `Ip` | per-surface intensity of the last surface (as reported by the engine) |
| `Tcum s`, `Tcum p` | **final cumulative transmittance** from the entrance (incident intensity 1 at the object plane; product of every physical refractive surface's single-surface intensity transmittance; the object plane and ideal fold mirrors contribute 1) |
| `Surf` | number of surface records |
| `Tx` | surfaces transmitted (`TRANSMIT` interaction) |
| `Miss` | surfaces missed (e.g. `MISSED` on a stopping surface) |
| `Error` | the ray's trace error message, when it stopped |

A ray whose trace failed keeps its error in the summary (and its stopping
surface carries the `error_code` in the detail table).

### Per-surface detail

Unless `--summary` is given, `list rays` prints a `Detail — <id>:` block per
ray whenever the results carry surface data:

```
Detail — trace_single:
Surf  x  y          z           dx  dy        dz  Interact  OPL[mm]  Is  Ip  Jones  Tcum s  Tcum p  ...
```

Base columns: `Surf`, `x`, `y`, `z` (intersection position), `dx`, `dy`, `dz`
(propagated direction), `Interact` (`TRANSMIT`/`REFLECT`/`MISSED`), `OPL[mm]`,
per-surface intensity `Is`/`Ip`, the Jones vector, and the cumulative
transmittance `Tcum s`/`Tcum p` from the entrance.

When the results carry detail data (a `--details` trace), the columns `Irs`,
`Irp` (power reflection), `θ[°]` (angle of incidence), `n1`, `n2` and the
Fresnel coefficients `Rs`, `Rp`, `Ts`, `Tp` are appended. An `Err` column is
appended when any surface carries an error code:

```
Surf  x  y          z  dx  dy        dz  Interact  OPL[mm]  Is  Ip  Jones  Tcum s  Tcum p  Irs  Irp  θ[°]  n1  n2  Rs  Rp  Ts  Tp  Err
   0  0  5.000000  -100  0  0.087156  0.996195  TRANSMIT        0  1  1  1+0i 0+1i  1.000  1.000   -    -    -    -   -   -   -   -
   1  0  5.000000  -100  0  0.087156  0.996195  MISSED          0  0  0  0+0i 0+0i   1.000  1.000   -    -    -    -   -   -   -   -  missed_surface
```

Missing values render as `-`; empty `Jones` cells indicate the trace stopped
before that surface was reached.

### Structured output

YAML/JSON wrap the tables in a self-describing shape:

```yaml
summary:
    - id: trace_single
      wavelength: 0.00058756
      opl_total: 132.0502003574585
      intensity_s: 1
      intensity_p: 1
      tcum_s: 0.68146029210437
      tcum_p: 0.7007242275675368
      surfaces: 9
      transmitted: 9
      missed: 0
details:
    - ray_id: trace_single
      surface_id: 0
      position: [0, 2, -100]
      direction: [0, 0, 1]
      interaction: TRANSMIT
      thickness: 0
      opl: 0
      intensity_s: 1
      intensity_p: 1
      jones: [1, 0, 0, 1]          # [ReEx, ImEx, ReEy, ImEy]
      tcum_s: 1
      tcum_p: 1
      angle_of_incidence: 11.21    # only with --details data
      n1: 1
      n2: 1.63854
      rs: 0
      ...
```

CSV emits a `Ray Summary:` block followed by a `Ray Detail:` block with
one header row each.

---

## 7. Output formats

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

## 8. Examples

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

# Trace results: summary + per-surface detail
rayweave trace single --origin 0,2,-100 --direction 0,0,1 < lens.yaml | rayweave list rays

# Trace results: summary only
rayweave chief | rayweave trace | rayweave list rays --summary

# First-order properties with element glass roles
rayweave list paraxial --roles < lens.yaml

# Pipe into query for programmatic access
rayweave list --format yaml < lens.yaml | rayweave query -r glasses[0].nd
```
