# RayWeave — agents guide

## Working rules

- On the `develop` branch, do **not** commit (or amend, rebase, push, or otherwise
  rewrite history) until the user explicitly asks you to commit. Keep the working
  tree changes staged/untracked and report what is ready to commit.
- While working on `develop`, do **not** modify the `main` branch and do **not**
  push to GitHub (`origin`). Work stays on `develop` (gitea `private` unless told
  otherwise).
- Re-timestamping commits and force-pushing require explicit user approval.

## Build & run

```sh
go build -o rayweave ./cmd/rayweave/
./rayweave <subcommand> < input.yaml
```

Tests: `go test ./...` (13 test files across all packages, no CI).

## Dependencies

`gopkg.in/yaml.v3` + `golang.org/x/image` (PNG). No Makefile, no opencode.json.

## Subcommands

`chief` | `trace` | `paraxial` | `tmm` | `plot` | `vignette` | `optimize` | `escape` | `import` | `asphere` | `psf`

Standard pipeline: `chief → trace → plot`. Each reads YAML from stdin, writes YAML to stdout. `--config ID` on chief/trace/paraxial/plot for multi-config selection.

`psf` (`rayweaver psf [--ref-surface N] [--psf-grid 64] [--psf-width W] [--num-rays 400] [--fields I,...] [--wavelengths W,...] [--polarization RCP|LCP|X|Y|RCP+LCP] [--max-freq N] [--yaml FILE] [--csv FILE]`) computes the point-spread function on the **fixed flat image plane** via per-field polarized ray tracing, non-uniform wavefront sampling (Delaunay-triangulated reference surface) and a **direct vector Huygens integral** — no FFT, fisheye-safe. Requires a `chief` section (fields). It reuses the chief dynamic-pupil / stop grid to sample each field's entrance pupil, traces the polarized wavefront to the reference surface (default: the last optical surface) with full Jones tracking (`SurfaceResult.Field`), weights the samples by 3D Delaunay triangle areas (`internal/mesh`), and coherently sums secondary wavelets `E(P) = Σ E_j·exp(ik(OPL_j + n·R_j))·K_j·ΔA_j/R_j`. The pipeline YAML carries a lightweight `psf_results[]` summary (Strehl vs a same-samples diffraction-limited reference, FWHM, centroid, encircled-energy 50%, Airy radius, sampling counts, plus the MTF sagittal/tangential threshold and evaluated-frequency summary); full grids (intensity + Ex/Ey/Ez, encircled-energy curve, best-fit-sphere wavefront `rms_opd`/`pv_opd`) and the OTF/MTF curves go to `--yaml` files and gnuplot pm3d maps (blank-line-separated rows, `set datafile separator ","`) to `--csv` files, one index-suffixed file per result. `--max-freq N` caps the reported MTF curves and threshold search at N cycles/mm (default `psf.mtf_config.max_frequency`, else the Nyquist). Default input polarization is RCP; `RCP+LCP` averages intensities incoherently. See `docs/psf.md` and `docs/methods/psf.md`. Strongly aberrated off-axis fields give speckle PSFs whose Strehl is sampling-sensitive — raise `--num-rays` (900..1600) for those.

`vignette` iteratively settles per-field vignetting and `auto_aperture` surface diameters using the dynamic pupil (see below). Output is pipeline-compatible: `vignette → trace → plot`.

`asphere` (`rayweave asphere [--rings N] [--angles N] [--pupil-samples N] [--sensitivity-samples N] [--top-k N] [--sag-scale α] [--validate] [--apply]`) ranks candidate surfaces for asphere introduction and estimates safe initial even-order asphere coefficients (conic + A4..A12) from the per-field OPD residuals. Requires a `chief` section (fields + optional `stop_surface`). It traces a polar pupil grid per (field, wavelength), builds polar footprint cells on each candidate surface (`internal/asphere`), scores each surface for how well a rotationally-symmetric asphere can correct the shared (field-common) OPD while penalising inter-field conflict, manufacturing difficulty and optimisation instability, then fits the top-K surfaces' coefficients via a regularised even-order polynomial (fast OPD→sag approximation `dz ≈ -O/(n2-n1)`, r² defocus separated, radius normalised to the footprint, ridge-regularised). Output is pipeline-compatible (`asphere_candidate_result:` appended). Configure via the `asphere_candidate:` YAML section: `candidate_surfaces`, `max_even_order`, `include_conic`, `preserve_vertex_curvature`, `cell_rings`, `cell_angles`, `pupil_samples_radial`, `sensitivity_samples`, `remove_piston/tilt/defocus`, `top_k`, `min_rays_per_cell`, `score_weights`. Piston is always removed; `remove_tilt`/`remove_defocus` default true/false respectively. The dynamic pupil (per-field chief-ray crossing Z, computed via a cheap `chief` pass) centres each grid; when ill-conditioned (e.g. a heavily degraded start) it falls back to the tightest fixed aperture's Z.

The ranking's sensitivity term is measured, not analytic: for every candidate surface the scaled asphere coefficients are applied and the pupil grid re-traced, giving the weighted-RMS-OPD merit with and without the asphere (`sensitivity.base_merit`/`asphere_merit`), the relative improvement `1 - asphere/base` used as the score's sensitivity term `H`, and the per-coefficient finite-difference derivatives `∂Merit/∂c_j` (`sensitivity.d_merit_d_coef`, A4..A12) computed with a shared frozen pupil so the Jacobian is consistent. The base merit is traced once per run and shared by all candidates. Top-K selection skips surfaces whose coefficient fit fails (degenerate index difference, no footprint), so the fitted set is genuinely aspherisable. Set `sensitivity_samples: 0` to disable the sensitivity pass and fall back to the analytic index-contrast proxy. The output also carries `opd_profiles[]` (per candidate surface, per field: weight-mean OPD vs footprint-ring radius), the graph data behind the OPD-overlap chart in `asphere-demo.bash`.

`asphere --validate` (`--dls-iter N`, default 20) verifies each fitted top-K asphere with a short DLS: the scaled coefficients are inserted onto the candidate surface (conic left at 0 to avoid a degenerate discriminant on weakly-curved surfaces) and the asphere coefficients `a4..a12` become the only optimisation variables over a **spot-RMS** merit (one term per field × wavelength) — the same geometric spot the `chief` `spot_stats.rms_r` reports, so the validation improvement stays coherent with the spot before/after comparison of the `asphere-demo.bash` script. The dynamic pupil is recomputed against the asphered system before the solve so the initial grid hits the new surface. Each fitted surface gains a `validation:` block (`before_merit`, `after_merit`, `improvement`, `iterations`, `status`, plus `coefficients`: the DLS-solved `a4..a12`). `--apply` (implies `--validate`) inserts the top-ranked validated asphere's DLS-solved coefficients onto its surface in every config (conic 0, `asphere_polynomial`) and outputs the modified system, so the pipeline `asphere --validate --apply | chief | trace | plot` shows the all-spherical vs aspherized lens. The intersection of an `asphere_polynomial`/`asphere_zernike` surface seeds its Newton iterate from the analytic base-sphere intersection (`IntersectAsphere` takes the radius) so off-axis rays whose root lies far along the ray converge; a zero-coefficient asphere traces identically to its sphere.

`escape` (sub-subcommands: `escape` run, `escape extract --index N`) is the Ishiki-Ono style escape-function global optimiser: DLS cycles with merit-function bumps at discovered local minima. Outputs the best solution pipeline-compatible plus `escape_result.minima[]` (full surfaces per minimum, plus `features[].element_powers`: the thin-lens power of each lens element per config as a solution fingerprint).
## Key conventions

### CLI options vs input YAML (three principles)

Every YAML-pipeline subcommand follows three rules:

1. **YAML-specifiable** — every computation-setting CLI flag has a counterpart in
   the input YAML section (`--wl` ↔ `chief.wavelength`, `asphere --rings` ↔
   `asphere_candidate.cell_rings`, `psf --psf-grid` ↔ `psf.grid_size`, `psf --max-freq`
   ↔ `psf.mtf_config.max_frequency`, `--glass-dir`
   ↔ `glass_catalog.directory`, ...). The default flag value is always 0/""/false
   ("unset"); the YAML value (or the built-in default) fills the gap, so an unset
   flag never overrides YAML.
2. **CLI wins** — when both a flag and the YAML value are present, the flag wins
   (resolved with `flagWasSet` so "not given" is distinguishable from a default).
3. **Write-back** — when the run used CLI options, the pipeline output YAML carries
   the effective (flag-won) values in the corresponding section. Scalar settings are
   written back only for flags actually set (never inject built-in defaults into
   every output); `--glass-dir` is written back into `glass_catalog.directory`.

Exemptions (documented, not YAML-specifiable):
- `--config` is a config *selection*, not a setting.
- `plot` is a terminal renderer (SVG/PNG); its render flags never flow into pipeline YAML.
- `import` reads a foreign format (not YAML) — no input-YAML settings to overwrite.
- Action/stream flags record their **effects** in the output instead of a setting:
  chief `--clear-aperture*` / `--marginal-rays` / `--preserve-rays` / `--ray-fan` /
  `--fan-plane` / `--fan-rotation` (effects: diameters, `rays[]`, `ray_fan`),
  `--verbose` / `--log`, `escape --save` (effect: `escape_result.minima[].file`),
  `psf --yaml` / `--csv` (effect: `psf_results[].output_file`).
- `escape extract --index` selects a stored minimum.

`psf` is the reference implementation (every flag ↔ `psf:` field, CLI wins,
`writeBackPSF` echoes the effective values into the output).

- Surfaces use `curvature` (not `radius`) as primary field. `radius` in YAML is converted at parse time.
- Chief field types: `angle` (degrees) + optional `direction [dx, dy]`, `image_height` (mm), or `height` + `object_z` (finite conjugate). Use `--wl` flag for multi-wavelength spot grids.
- `import --format zemax|oslo|codev` produces multi-config YAML with automatic chief+marginal rays.
- Optimize auto-detects multi-config mode when `shared_variables`, `local_variables`, or `configs[].merit` exist.
- Surface 0 = implicit object plane (no intersection/refraction). Ray `path` must start with `0`.
- New surface fields: `auto_aperture` (vignetting), `min_glass_path`/`max_glass_path` (edge-thickness constraints).
- **Document metadata**: every pipeline document carries a top-level `metadata:` block (`metadata.tool: RayWeaver`, `url`, `metadata.schema_version`). Subcommands stamp `metadata.generator` and a `created_at`/`rayweaver_version` on output and round-trip the block through the pipe. The legacy top-level `version` field is deprecated and no longer written; `metadata` is optional on input and documents without it (or with a non-`RayWeaver` `tool`) parse fine (a mismatched `tool` only warns). The `Input.Version` Go field is gone; use `Metadata`.
- **There is no implicit aperture stop.** The stop is only used when `chief.stop_surface` is set explicitly (it is then output with `yaml:"stop_surface,omitempty"` — omitted when 0/absent). Without a stop, the pupil is **dynamic**: per-field entrance pupil Z = the in-lens crossing of each field's chief ray with field 0's chief ray (the aperture position); the grid is centred on it. `chief` iterates this (≤3 passes) until the pupil settles. `surface.FindStopID` does not exist; `paraxial`/`importer` never infer a stop.
- **Dynamic pupil in `chief`**: the chief ray of each field passes through the surviving-grid centroid at the reference surface; the entrance pupil is the in-lens chief-ray crossing and the exit pupil the image-side outgoing-segment crossing (omitted when ill-conditioned). The grid radius is the paraxial entrance-pupil radius when `chief.stop_surface` is set (the stop's image), else the beam-aware fixed-aperture cap (`fixedApertureAtPupil`: each `auto_aperture: false` surface's aperture projected back to the aperture position along the paraxial marginal ray, so a surface only caps when its clear aperture is smaller than the beam there — image-side surfaces like a field flattener do not shrink the entrance pupil).
- **Dynamic pupil during optimisation**: `optimize`/`escape` seed the grid centring from the dynamic pupil at startup, and the DLS solver calls the optional `dls.PupilUpdater` (`Optimizer.UpdatePupils`) at the top of every iteration, so the aperture position moves with the lens while staying frozen within one iteration (base-point + Jacobian residuals share the same pupil → consistent derivatives). Requires `chief.reference_surface` and per-config `fields` to be present.
- **`vignette`** (`rayweave vignette [--iterations 3] [--min-glass-path 0.5] [--margin-mm 0.2]`): per pass it measures the surviving beam (re-traces every grid ray with the aperture check skipped on `auto_aperture` surfaces, glass-path check on) and sizes `auto_aperture: true` surfaces to `2×max extent + 2×margin-mm`; fixed (`auto_aperture: false`) surfaces are never re-sized. Diameters are sized to the union envelope of every field's full beam (measured before the vignetting cut), so a vignetted off-axis bundle never shrinks the lens. Rays failing the glass-path (edge-thickness) constraint or a fixed-surface aperture are vignetted (narrowed beam); the on-axis field's marginal envelope bounds each off-axis bundle in the plane perpendicular to that field's own chief ray (through its entrance pupil), not in a plane perpendicular to the optical axis. Outputs `vignetting_result:` (per-field vignetting, per-field entrance pupil Z, envelope, marginals, diameter before/after) plus chief + marginal rays for plotting.
- **`--clear-aperture`** (chief flag) sizes only `auto_aperture: true` surfaces to the beam footprint + margin (always grow+shrink; `--shrink` was removed). One-shot; use `vignette` for the iterative version.
- `optimize --verbose` outputs JSONL on stderr; `--log FILE` writes to file.
- `escape --verbose` also outputs JSONL on stderr and `--log FILE` writes the same stream to a file (events: `params`, `start`, `cycle`, `minimum`, `worker_done`, `timeout`, `interrupt`, `interrupt_dls`, `interrupted`, `done`; every event carries `time` (RFC3339) and `elapsed` (seconds since run start)). Both are readable via `query --jsonl --where ...`. The trailing `=== Escape complete ===` human summary on stderr is unchanged.
- `escape --save FILE` writes every discovered local minimum to `FILE1.yaml`, `FILE2.yaml`, ... (discovery order, clean pipeline-compatible Input). When a minimum is improved (better merit, judged the same minimum by `distance_threshold`), the current `FILE N.yaml` is renamed to `FILE N.<version>.yaml` and the better point is written to `FILE N.yaml`. The saver (`escapeFileSaver.record`, wired via `Store.SetOnRecord`) runs under the store lock, uses `applyEscapeX`/`applyEscapeMulti` (catalog read-only, race-free), and writes atomically (temp + fsync + rename) so a SIGKILL never loses already-found minima. `escape_result.minima[].file` records each file; `Result.MinimaIdx` maps the merit-sorted minima back to discovery indices.
- A repeat of a known minimum strengthens its escape bump **and**, when the new point has a better merit, replaces the stored X/Merit (version counter in `Store.Replace`). This is the "keep the better data" behaviour.
- `SIGINT`/`SIGTERM` stop `escape` in three escalating stages (handler in `runEscape`), each one still completing normally (`interrupted: true`, exit 0) except the last:
  1. **First signal** — graceful stop: emits an `interrupt` event, cancels the shared context; workers stop at the next cycle boundary once the running DLS solve completes. Everything is saved, stdout YAML written with `interrupted: true`.
  2. **Second signal** — mid-DLS stop: emits an `interrupt_dls` event and closes the `hardStop` channel (`RunOptions.HardStop` → `Cycle.hardStop` → `Wrapper.SetStop` → `dls.Options.Stop`). The running DLS aborts within one iteration (checks at the iteration top, after pupil update/Jacobian, inside the line search, and between Jacobian columns in `parallelColumns`), returns its **best point so far** with `dls.StatusInterrupted`, and the cycle records that point into the store (`Cycle.recordInterrupted`, guarded by `Iterations > 0` and finite merit) so it is saved via the normal `--save` path. The run then completes normally (`interrupted: true`, exit 0).
  3. **Third signal** — force quit: `os.Exit(1)`. Already-discovered minima are on disk (atomic writes) so nothing is lost.
  `Cycle.StoppedByTime()` and `ParallelEscape`'s `TimedOut` exclude both ctx cancellation and a hard stop. The `optimize` command wires its own mid-solve stop channel (`Optimizer.SetStop`) on the first SIGINT/SIGTERM and writes the best-so-far result (exit 0); `escape` uses the shared `hardStop` for the second signal.
- DLS Jacobian parallelisation: `optimization.jacobian_workers` (default GOMAXPROCS; the `escape` command defaults an unset value to 2 instead). The Jacobian loop in `internal/dls/solver.go` (`computeJacobians`/`parallelColumns`) is embarrassingly parallel and deterministic — identical results for any worker count. `applyVariables` must stay pure (no `Optimizer` mutation) for this to hold.
- `optimization.escape.escape_workers` is the top-level parallel escape goroutines (default 4). Total goroutines = `escape_workers × jacobian_workers`; recommend `jacobian_workers: 1` for many escape workers.
- `optimization.escape.max_seconds` is a soft wall-clock budget shared by all escape workers. The deadline is set once in `ParallelEscape` and checked in `Cycle.Run` between DLS solves (zero deadline = unlimited). Running DLS always finishes; `Result.TimedOut`/`EscapeResult.timed_out` flag early stops.
- Escape functions act in the normalised variable space (each variable scaled by Min..Max); fixed vars (Min==Max) are excluded from the escape distance. `escape` disables the DLS internal stall perturbation via `Options.DisableStallEscape`.
- `internal/rayio/` is dead code — never imported. `perllens/` is legacy Perl.
- Z = optical axis (positive right). All units in mm (coating thicknesses in nm, converted internally).

## Fold model

- **All thicknesses are positive.** A mirror is folded via
  `decenter: [{tilt: [0, 180, 0], scope: both}]` plus a top-level
  `reflect: true` (tilt in **degrees**). Negative thickness is a parse-time
  error.
- The beam-frame radius after an odd number of reflections is the **negation**
  of the physical radius (e.g. a concave mirror with R=-800 in the old unfolded
  model is R=+800 here). Paraxial and ray trace both consume the beam-frame radius.
- `role: "stop"` is inert metadata; the stop is `chief.stop_surface` (used by
  chief, paraxial, DLS/optimize). `Surface.Reflects()` / `Surface.PhysicalZ`
  are the runtime signals; fold walk lives in `internal/surface/precompute.go`.
- Per-field trace order is `chief.fields[].path` (object plane `0` is implicit
  and auto-prepended if absent); the default is the surface-ID order.

## Samples

| Script | What it demonstrates |
|---|---|
| `run-demo.bash` | Basic chief → trace → plot pipeline on US2645157 triplet |
| `optimize-demo.bash` | DLS on degraded triplet, before/after PNG diagrams |
| `simple-zoom-demo.bash` | Multi-config zoom (3 configs, shared variables), DLS + RMS check |
| `multi-config-zoom-demo.bash` | Multi-config zoom with per-config comparison table |
| `glass-optimize-demo.bash` | Glass model variables (nd/vd) as optimisation targets, 4-wavelength spot diagrams |
| `escape-demo.bash` | Escape-function global search on the US2645157 triplet; `--lens doublegauss` switches to the double-Gauss system (`doublegauss-init.yaml`, slow) |
| `asphere-demo.bash` | Adds one asphere to an optimised all-spherical double-Gauss (`asphere-demo-init.yaml`): per case ranking → spot-RMS validation → `--apply` → spot RMS before/after → PNG diagrams + per-case gate. Runs the full-aperture case always; ONLY when a stop is requested also runs a stopped-down variant (surface 7's fixed aperture resized via yq) — `--epd X` as a **fraction of the full pupil** for `0 < X < 1` (e.g. `--epd 0.5` = half) or an absolute `--epd N` mm (N ≥ 1), `--fno N` by F-number (EPD = EFL/N). A stop triggers the side-by-side OPD-overlap chart (left = full, right = stopped) **each column on its own y-range** (same-EPD scales align; different EPDs are each read on their own scale), plus a full-vs-stopped spot-RMS table. Without a stop the chart is a plain single-column plot. `--clean` removes tagged `-epd*`/`-fno*` artifacts and init variants |

Demo scripts use `set -euo pipefail` and a `--clean` flag. They are location-independent: every path (input data, outputs) resolves against the script's own directory (`SCRIPT_DIR`), and the binary is found as `RAYWEAVE` env → `$SCRIPT_DIR/rayweave` → `$SCRIPT_DIR/../rayweave` (repo root) → `rayweave` on PATH → error. So they run from the repo root (`bash samples/foo.bash`), from `cd samples`, or from a copied directory (data files must be copied alongside the script). `yq` + `gnuplot` for post-processing (not required by Go build).

## Glass catalog

AGF files + inline entries. Dispersion: Sellmeier (preferred) or Cauchy from nd+vd. AGF files in `GLASS/` (gitignored `*.agf`).

## Surface materials (`types.Material`)

A surface `material` is a structured type, one of:
- **Catalogue reference** — `material: {key: N-BK7}` (flat legacy scalar `material: "N-BK7"` still parses to this). Index comes from `glass_catalog`.
- **Inline model glass** — `material: {nd: 1.76499, vd: 15.0}` (flat legacy `"1.76499:15.00"` still parses to this). Dispersion is computed from nd/vd; no catalogue entry is involved.
- **Air** — `material: AIR` or absent. A surface with both a `key` and `nd`/`vd` is a catalogue reference (key wins).

`Catalog.RefractiveIndex(types.Material, wavelength)` resolves by key first, then inline nd/vd. The importers (ZEMAX inline `GLAS ___BLANK ...`, Oslo inline nd, CODE V) write **inline model glasses** — each surface carries its own nd/vd, so same-named model glasses from different files no longer collide in `glass_catalog`. A model glass may instead be registered in `glass_catalog` under its own key and referenced by `key` (shared across surfaces); the importers do not do this automatically.

**Optimization**: a glass `nd`/`vd` variable targets the surface's material. Inline models are updated in place (key stays empty); keyed references are optimised through an in-flight catalogue override (the base catalog is never mutated) and `MaterializeGlassEntries`/`FinalConfigs` rewrite every surface sharing that key to the optimised **inline** model glass (`{nd, vd}`, key removed). No new glass entry is appended to `glass_catalog`.

## 日本語の扱い

UTF-8 BOM なし．句読点は「，」「．」．コミットメッセージとソースコードコメントは英語．ドキュメントは英語で，必要なら日本語版も作る．リポジトリ直下の Markdown は `README.md` だけ管理（他は git add しない）．
