# `focus` — image-plane comparison of MTF and PSF

`focus` evaluates a system at up to three **image-plane conventions** and
reports the same metric for each, so it is clear which plane the design actually
delivers. It is the plane-comparison layer on top of the `psf` engine: the
`psf` command remains the single-plane imaging measurement (its PSF/MTF output
is unchanged), while `focus` answers "how much does the deliverable depend on
where the image plane is?"

```
rayweave focus mtf [flags] < pipeline.yaml
rayweave focus psf [flags] < pipeline.yaml
```

Both sub-subcommands read YAML on stdin and write YAML to stdout (a clean
pipeline document). They do not print a table to stderr; pipe the output into
`list focus` for a human-readable comparison. Neither has `--yaml`/`--csv`.

## Planes

| Selector | Output key | Image plane |
|---|---|---|
| `file` | `file` | The plane as written in the input (no solve). |
| `all` | `focus_plane_all` | The single all-field best focus: a uniform-weighted **wavefront** back-focus solve, the same plane a saved `escape`/`optimize` minimum carries. The applied shift is reported as `focus_plane_all_shift_mm`, and adjusts the back air gap after the last powered element (a genuine image-plane refocus). |
| `best` | `best_focus` | Each field's own best focus (coherent-peak-maximizing shift, as in `psf --best-focus`), reported per row as `best_focus_shift_mm`. |

`--planes file,all,best` selects which conventions to evaluate (default: all
three; the evaluated list is written back). A plane that is not requested is
omitted from its row.

## `focus mtf`

At each requested spatial frequency (cycles/mm), report the sagittal and
tangential MTF — with the matching Strehl — per (field, wavelength) × plane.
(FWHM is a PSF-domain metric and is reported by `focus psf`, not here.)

```
rayweave focus mtf --frequencies 30,50,80 --wavelengths 0.0005876 \
                   --num-rays 400 --fields 0,1,2 < lens.yaml
```

Options shared with `focus psf`:

| Flag | Meaning |
|---|---|
| `--wavelengths W,...` | wavelengths in mm (default: config wavelengths, else the chief reference) |
| `--fields I,...` | field indices to compare (default: all) |
| `--planes LIST` | `file,all,best` (default: all three) |
| `--num-rays N` | entrance-pupil grid rays (default 400) |
| `--psf-grid N` | image-plane pixels per side (default 64) |
| `--ref-surface N` | wavefront sampling surface (default: the last optical surface) |
| `--polarization S` | `RCP` (default) \| `LCP` \| `X` \| `Y` \| `RCP+LCP` |
| `--converge-check BOOL` | re-evaluate at 1.5× rays to label convergence (default: **off** for `focus`) |
| `--config ID` | select config by id (multi-config mode) |
| `--glass-dir DIR` | AGF glass catalog directory |

`focus mtf` only:

| Flag | Meaning |
|---|---|
| `--frequencies F,...` | spatial frequencies in cycles/mm (default `focus.mtf.frequencies`, else `50`) |

The reported MTF arrays are aligned to the effective frequency list.

## `focus psf`

The same plane comparison with PSF metrics instead of MTF: Strehl, FWHM X/Y,
encircled-energy 50%, and the PSF centroid per plane.

## CLI / YAML

Every flag mirrors the `focus:` section (CLI wins; the effective values are
written back). The section is split to match the sub-subcommands:

```yaml
focus:
  mtf:
    wavelengths: [0.0004861, 0.0005876, 0.0006563]
    frequencies: [30, 50, 80]
    fields: [0, 1, 2, 3, 4]
    planes: [file, all, best]
    num_rays: 400
    grid_size: 64
    polarization: RCP
    reference_surface: 0
    converge_check: false
  psf:
    wavelengths: [0.0005876]
    planes: [all, best]
```

## Output

`focus mtf` adds a `focus_comparison` section:

```yaml
focus_comparison:
  mtf:
    planes: [file, focus_plane_all, best_focus]
    frequencies: [30, 50, 80]
    focus_plane_all_shift_mm: -0.098242
    polarization: RCP
    rows:
      - field_index: 0
        field_angle: 0
        wavelength: 0.0005876
        file:
          strehl: 0.3769
          mtf_sagittal:   [0.233, 0.180, 0.120]
          mtf_tangential: [0.231, 0.184, 0.121]
        focus_plane_all: { ... }
        best_focus:
          strehl: 0.9880
          best_focus_shift_mm: 0.1021
          mtf_sagittal:   [0.304, 0.307, 0.240]
          mtf_tangential: [0.298, 0.315, 0.246]
```

`focus psf` fills `focus_comparison.psf` with the same row shape and the PSF
metric set (`strehl`, `fwhm_x`, `fwhm_y`, `encircled_energy_50`, `centroid_x`,
`centroid_y`, and `best_focus_shift_mm` on the `best_focus` block).

The `clean` command strips `focus_comparison`.

## Viewing the result — `list focus`

`focus` writes no table to stderr. Pipe its output into the `list focus`
target to render the comparison (`table` default, plus `yaml`/`json`/`csv` via
`--format`):

```sh
rayweave focus psf --planes all,best < lens.yaml | rayweave list focus
rayweave focus mtf --frequencies 30,50,80 < lens.yaml | rayweave list focus --format csv
```

`list focus` auto-detects the `mtf` or `psf` sub-section (normally only one is
present) and renders the table the `focus` command used to print to stderr. The
`yaml`/`json` forms echo the `focus_comparison` section under its own key; the
`csv` form flattens one row per (field, wavelength) × plane (for `focus mtf`
with one sagittal/tangential column pair per frequency). See `docs/list.md`.

## Relationship to `psf`

- `psf` is the single-plane measurement: it computes the PSF grid and derives
  the full OTF/MTF (curves, thresholds, evaluated frequencies) for one plane per
  run (`--best-focus`, `--focus-plane all`, or the file plane). Its output and
  its `psf.mtf_config` section are unchanged.
- `focus` runs the same engine over several planes and tabulates a compact
  comparison. Use `psf` to characterise one plane in depth; use `focus` to see
  how the choice of plane affects the deliverable.

## Notes

- The `all` plane needs a field list for the uniform wavefront solve. It uses
  the config's `configs[].fields` when present and otherwise falls back to
  `chief.fields`, so a hand-written single-config document (fields only in
  `chief`) is solved rather than silently left at the file plane (the same
  fallback as `psf --focus-plane all`). It is sampled at the command's
  `--num-rays` (default 400), matching the plane evaluation — the solve's
  coherent-peak objective is sampling-sensitive for strongly aberrated fields.
- Best-focus planes are per **field** (not per wavelength): the shift listed on
  the `best_focus` block is the coherent-peak-maximizing shift applied for that
  field, so a row's `best_focus` metrics share one plane across wavelengths.
- The MTF is the FFT-derived MTF of the PSF grid, so the same sampling guidance
  as `psf` applies: raise `--num-rays` for strongly aberrated fields where the
  peak-ratio Strehl is sampling-sensitive.
