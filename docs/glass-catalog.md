# Glass catalog: built-in entries and fallback resolution

This document explains how RayWeave resolves glass (optical material) data: the
built-in table, its contents, and the precedence/fallback behaviour between it
and external (AGF) catalogs.

## 1. Two data sources

Each surface in a lens file carries a glass name via `GLAS <name>`, which needs
its refractive index n and Abbe number νd (or a dispersion formula). RayWeave
has two sources:

| Source | Location | Contents | Precision |
|---|---|---|---|
| Built-in `commonGlass` | `internal/importer/importer.go` (map in Go code) | Only the **n and νd pair** for each glass | Approximate (Cauchy dispersion derived from n, νd) |
| External AGF catalog | `GLASS/` directory (via `--glass-dir`) | Full data: Sellmeier coefficients, wavelength range, manufacturer name, etc. | High precision |

The built-in table is a fallback dictionary for getting "known name, unknown
values" glasses minimally working with their known n and νd. When a complete AGF
catalog is available, it takes precedence in principle.

## 2. Resolution order (at parse time)

Each surface's `GLAS <name>` is resolved by the importer via
`addGlassEntryNDV` (`internal/importer/helpers.go`) in the following order:

1. **Inline n, νd** — a ZEMAX model-glass line `GLAS <name> <disp-flag> 0 <nd> <vd> ...`
   (disp-flag=1) carries nd/vd directly. Example: `___BLANK` → n=1.76499, νd=15.0.
2. **`"n:νd"` label convention** — a label of the form `1.517:64` is split.
3. **Built-in `commonGlass`** (`LookupGlass`) — the label matches the built-in table.
4. Otherwise → **unresolved model glass** (n=0). Reported as `unresolved` in sweeps.

## 3. AGF "upgrade" (after parsing)

In the later stages of `import` / `importsweep`,
`EnhanceGlassEntriesFromAGF` (`internal/importer/agf_lookup.go`) overwrites
entries with full AGF data, but only for labels that match.

- Matching uses `glass.Catalog.Lookup` (normalized key: hyphens/underscores stripped).
- On a match, `Type=Catalog`, Sellmeier coefficients, `ND`/`VD`, manufacturer
  name, etc. are copied over.
- This runs **only when `--glass-dir` is given** (`cmd/rayweave/import.go`).

## 4. Which one takes precedence

- **AGF wins as long as the label matches** (the built-in n/νd are overwritten by AGF values).
- Only when the label does not match is the built-in `commonGlass` n/νd used.
- In other words, the built-in table is a last-resort safety net reserved for
  names absent from the AGF catalog.

## 5. If the same glass later appears in the AGF catalog

Matching uses a dynamic normalized key. If a future AGF catalog lists the same
glass as `H-LAF2` (normalized `HLAF2`), it automatically matches the built-in
`H_LAF2` (normalized `HLAF2`) and switches to AGF data with no changes needed.

Caveats:

- **Aliases from different manufacturers do not match** (e.g., if a future AGF
  lists `LAF2` as a Nikon spec, normalized `LAF2` ≠ `HLAF2`). That case requires
  adding an alias entry or name normalization.
- Currently only label matching is implemented; **n/νd-based fallback is not**
  (a different-brand glass with the exact same n/νd will not switch automatically).

## 6. Built-in `commonGlass` listing

The `commonGlass` map in `internal/importer/importer.go`. Current as of
2026-08. Several of these entries were added from lens data obtained from
[lens-designs.com](https://www.lens-designs.com/).

| Name | nd | νd | Category |
|---|---|---|---|
| BK7 | 1.51680 | 64.17 | Standard crown |
| F2 | 1.62004 | 36.37 | Flint |
| SF12 | 1.64831 | 33.84 | Flint |
| S-LAH66 | 1.77250 | 49.60 | Lanthanum |
| SK18 | 1.63854 | 55.42 | Crown |
| SF5 | 1.67270 | 32.21 | Flint |
| SF11 | 1.78470 | 25.76 | Flint |
| LAKN22 | 1.65113 | 55.89 | Lanthanum |
| K10 | 1.50137 | 56.41 | Crown |
| H_LAF2 | 1.74320 | 49.31 | Hoya family |
| H-LAK52 | 1.72916 | 54.68 | Hoya family |
| H-LAK53 | 1.72151 | 50.79 | Hoya family |
| H-ZF3 | 1.71736 | 29.51 | Hoya family |
| H-F1 | 1.62588 | 35.70 | Hoya family |
| H-ZLAF56 | 1.77377 | 47.25 | Hoya family |
| E48R | 1.53016 | 55.99 | Optical plastic |
| 480R | 1.52500 | 56.00 | Optical plastic |
| COC | 1.53000 | 56.00 | Resin |
| POLYCARB | 1.58547 | 30.16 | Resin |
| POLYSTYR | 1.59030 | 30.90 | Resin |
| PMMA | 1.49180 | 57.40 | Optical plastic |
| ACRYLIC | 1.49180 | 57.40 | Optical plastic (PMMA) |
| OKP4 | 1.52500 | 56.00 | Zeonex resin |
| OKP4HT | 1.52500 | 56.00 | Zeonex resin |
| 330R | 1.50940 | 56.20 | Zeonex resin |
| CAF2 | 1.43380 | 95.30 | Fluoride |
| QUARTZ | 1.45846 | 67.82 | Fused silica |
| SUPRASIL | 1.45846 | 67.82 | Synthetic fused silica |
| PYREX | 1.47340 | 67.50 | Borosilicate |
| SKN18 | 1.63854 | 55.42 | Crown |
| LAKN16 | 1.73400 | 51.49 | Lanthanum |
| H-ZF72 | 1.92286 | 18.90 | Hoya legacy |
| H-ZLAF70 | 1.90366 | 31.32 | Hoya legacy |
| H-LAK51 | 1.69680 | 55.44 | Hoya legacy |
| H-ZF4 | 1.72825 | 28.32 | Hoya legacy |
| H-QK3 | 1.48749 | 70.44 | Hoya legacy |
| H-ZLAF54 | 1.81600 | 46.54 | Hoya legacy |
| H-ZLAF55 | 1.83480 | 42.73 | Hoya legacy |
| H-ZLAF53 | 1.83400 | 37.32 | Hoya legacy |
| H-LAK2 | 1.69099 | 54.75 | Hoya legacy |
| H-ZLAF50B | 1.80400 | 46.56 | Hoya legacy |
| H-ZLAF55F | 1.83480 | 42.73 | Hoya legacy |
| H-LAF6L | 1.75699 | 47.70 | Hoya legacy |
| H-LAF50A | 1.77250 | 49.60 | Hoya legacy |
| H-LAF3 | 1.74400 | 44.89 | Hoya legacy |
| H-FK70 | 1.56907 | 71.30 | Hoya legacy |
| H-ZPK2 | 1.60300 | 65.44 | Hoya legacy |
| H-ZLAF55A | 1.83480 | 42.73 | Hoya legacy |
| H-LAK50 | 1.65160 | 58.39 | Hoya legacy |
| H-ZF75 | 1.94595 | 17.99 | Hoya legacy |
| H-ZLAF68 | 1.88299 | 40.79 | Hoya legacy |
| H-ZLAF80 | 2.00069 | 25.47 | Hoya legacy |

The Hoya legacy entries use the values of the equivalent glasses as supplied by
the manufacturers that keep the H- naming (湖北新华光 NHG, CDGM; Ohara S-PHM53
for H-ZPK2; Sumita K-GFK70 for H-FK70), since these families were renamed and
discontinued from HOYA's own catalog.

Note: the listing is maintained manually. For the authoritative values always
refer to `commonGlass` in `internal/importer/importer.go`.

## 7. Obtaining the listing

- Currently the only way is to read `commonGlass` in the source (there is no
  enumeration API).
- The exported function `LookupGlass(name)` looks up a single name and cannot
  dump the whole table.
- No CLI prints the full list (`importsweep -dump` prints the glass entries and
  unresolved determination for one file, not the entire built-in table).

## 8. When unresolved entries remain

Patent files in the corpus often use non-catalog notations that do not match the
AGF catalog (`H_LAF2`, `E48R`, `AL-6263-(OKP4HT)`, `MIRROR`, etc.). The built-in
`commonGlass` deliberately omits glasses of unclear identity (`MIRROR` = mirror
surface handling, `SF17`/`IRG15` = discontinued special glasses whose data is not
publicly available). As identities are confirmed, they are added to `commonGlass`.

Reference: the wide-angle entrance-pupil analysis is in
`wide-angle-pupil-analysis.md` (repo root).
