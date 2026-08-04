# RayWeave — agents guide

## Build & run

```sh
go build -o rayweave ./cmd/rayweave/
./rayweave <subcommand> < input.yaml
```

Tests: `go test ./...` (12 test files across all packages, no CI).

## Dependencies

`gopkg.in/yaml.v3` + `golang.org/x/image` (PNG). No Makefile, no opencode.json.

## Subcommands

`chief` | `trace` | `paraxial` | `tmm` | `plot` | `vignette` | `optimize` | `escape` | `import`

Standard pipeline: `chief → trace → plot`. Each reads YAML from stdin, writes YAML to stdout. `--config ID` on chief/trace/paraxial/plot for multi-config selection.

`vignette` iteratively settles per-field vignetting and `auto_aperture` surface diameters using the dynamic pupil (see below). Output is pipeline-compatible: `vignette → trace → plot`.

`escape` (sub-subcommands: `escape` run, `escape extract --index N`) is the Ishiki-Ono style escape-function global optimiser: DLS cycles with merit-function bumps at discovered local minima. Outputs the best solution pipeline-compatible plus `escape_result.minima[]` (full surfaces per minimum, plus `features[].element_powers`: the thin-lens power of each lens element per config as a solution fingerprint).

## Key conventions

- Surfaces use `curvature` (not `radius`) as primary field. `radius` in YAML is converted at parse time.
- Chief field types: `angle` (degrees) + optional `direction [dx, dy]`, `image_height` (mm), or `height` + `object_z` (finite conjugate). Use `--wl` flag for multi-wavelength spot grids.
- `import --format zemax|oslo|codev` produces multi-config YAML with automatic chief+marginal rays.
- Optimize auto-detects multi-config mode when `shared_variables`, `local_variables`, or `configs[].merit` exist.
- Surface 0 = implicit object plane (no intersection/refraction). Ray `path` must start with `0`.
- New surface fields: `auto_aperture` (vignetting), `min_glass_path`/`max_glass_path` (edge-thickness constraints).
- **There is no implicit aperture stop.** The stop is only used when `chief.stop_surface` is set explicitly (it is then output with `yaml:"stop_surface,omitempty"` — omitted when 0/absent). Without a stop, the pupil is **dynamic**: per-field entrance pupil Z = the in-lens crossing of each field's chief ray with field 0's chief ray (the aperture position); the grid is centred on it. `chief` iterates this (≤3 passes) until the pupil settles. `surface.FindStopID` does not exist; `paraxial`/`importer` never infer a stop.
- **Dynamic pupil in `chief`**: the chief ray of each field passes through the surviving-grid centroid at the reference surface; the entrance pupil is the in-lens chief-ray crossing and the exit pupil the image-side outgoing-segment crossing (omitted when ill-conditioned). The grid radius is the paraxial entrance-pupil radius when `chief.stop_surface` is set (the stop's image), else the beam-aware fixed-aperture cap (`fixedApertureAtPupil`: each `auto_aperture: false` surface's aperture projected back to the aperture position along the paraxial marginal ray, so a surface only caps when its clear aperture is smaller than the beam there — image-side surfaces like a field flattener do not shrink the entrance pupil).
- **Dynamic pupil during optimisation**: `optimize`/`escape` seed the grid centring from the dynamic pupil at startup, and the DLS solver calls the optional `dls.PupilUpdater` (`Optimizer.UpdatePupils`) at the top of every iteration, so the aperture position moves with the lens while staying frozen within one iteration (base-point + Jacobian residuals share the same pupil → consistent derivatives). Requires `chief.reference_surface` and per-config `fields` to be present.
- **`vignette`** (`rayweave vignette [--iterations 3] [--min-glass-path 0.5] [--margin-mm 0.2]`): per pass it measures the surviving beam (re-traces every grid ray with the aperture check skipped on `auto_aperture` surfaces, glass-path check on) and sizes `auto_aperture: true` surfaces to `2×max extent + 2×margin-mm`; fixed (`auto_aperture: false`) surfaces are never re-sized. Rays failing the glass-path (edge-thickness) constraint or a fixed-surface aperture are vignetted (narrowed beam); the on-axis field's marginal envelope at each field's entrance pupil plane bounds the off-axis bundles. Outputs `vignetting_result:` (per-field vignetting, per-field entrance pupil Z, envelope, marginals, diameter before/after) plus chief + marginal rays for plotting.
- **`--clear-aperture`** (chief flag) sizes only `auto_aperture: true` surfaces to the beam footprint + margin (always grow+shrink; `--shrink` was removed). One-shot; use `vignette` for the iterative version.
- `optimize --verbose` outputs JSONL on stderr; `--log FILE` writes to file.
- `escape --verbose` also outputs JSONL on stderr and `--log FILE` writes the same stream to a file (events: `params`, `start`, `cycle`, `minimum`, `worker_done`, `timeout`, `interrupt`, `interrupted`, `done`; every event carries `time` (RFC3339) and `elapsed` (seconds since run start)). Both are readable via `query --jsonl --where ...`. The trailing `=== Escape complete ===` human summary on stderr is unchanged.
- `escape --save FILE` writes every discovered local minimum to `FILE1.yaml`, `FILE2.yaml`, ... (discovery order, clean pipeline-compatible Input). When a minimum is improved (better merit, judged the same minimum by `distance_threshold`), the current `FILE N.yaml` is renamed to `FILE N.<version>.yaml` and the better point is written to `FILE N.yaml`. The saver (`escapeFileSaver.record`, wired via `Store.SetOnRecord`) runs under the store lock, uses `applyEscapeX`/`applyEscapeMulti` (catalog read-only, race-free), and writes atomically (temp + fsync + rename) so a SIGKILL never loses already-found minima. `escape_result.minima[].file` records each file; `Result.MinimaIdx` maps the merit-sorted minima back to discovery indices.
- A repeat of a known minimum strengthens its escape bump **and**, when the new point has a better merit, replaces the stored X/Merit (version counter in `Store.Replace`). This is the "keep the better data" behaviour.
- `SIGINT`/`SIGTERM` stop `escape` gracefully (handler in `runEscape`): the first signal emits an `interrupt` event, cancels the shared context, workers stop at the next cycle boundary (the running DLS solve completes), everything is saved, stdout still gets `interrupted: true`, exit code 0; a second signal force-quits with exit 1. No DLS-level interruption is attempted.
- DLS Jacobian parallelisation: `optimization.jacobian_workers` (default GOMAXPROCS; the `escape` command defaults an unset value to 2 instead). The Jacobian loop in `internal/dls/solver.go` (`computeJacobians`/`parallelColumns`) is embarrassingly parallel and deterministic — identical results for any worker count. `applyVariables` must stay pure (no `Optimizer` mutation) for this to hold.
- `optimization.escape.escape_workers` is the top-level parallel escape goroutines (default 4). Total goroutines = `escape_workers × jacobian_workers`; recommend `jacobian_workers: 1` for many escape workers.
- `optimization.escape.max_seconds` is a soft wall-clock budget shared by all escape workers. The deadline is set once in `ParallelEscape` and checked in `Cycle.Run` between DLS solves (zero deadline = unlimited). Running DLS always finishes; `Result.TimedOut`/`EscapeResult.timed_out` flag early stops.
- Escape functions act in the normalised variable space (each variable scaled by Min..Max); fixed vars (Min==Max) are excluded from the escape distance. `escape` disables the DLS internal stall perturbation via `Options.DisableStallEscape`.
- `internal/rayio/` is dead code — never imported. `perllens/` is legacy Perl.
- Z = optical axis (positive right). All units in mm (coating thicknesses in nm, converted internally).

## Fold model

- **All thicknesses are positive.** A mirror is folded via
  `decenter: [{tilt: [0, 180, 0], reflect: true}]` (tilt in **degrees**).
  Negative thickness and the top-level surface `reflect` key are parse-time errors.
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

Demo scripts use `set -euo pipefail` and a `--clean` flag. They are location-independent: every path (input data, outputs) resolves against the script's own directory (`SCRIPT_DIR`), and the binary is found as `RAYWEAVE` env → `$SCRIPT_DIR/rayweave` → `$SCRIPT_DIR/../rayweave` (repo root) → `rayweave` on PATH → error. So they run from the repo root (`bash samples/foo.bash`), from `cd samples`, or from a copied directory (data files must be copied alongside the script). `yq` + `gnuplot` for post-processing (not required by Go build).

## Glass catalog

AGF files + inline entries. Dispersion: Sellmeier (preferred) or Cauchy from nd+vd. `Catalog.RefractiveIndex(name, wavelength)`. AGF files in `GLASS/` (gitignored `*.agf`).

## 日本語の扱い

UTF-8 BOM なし．句読点は「，」「．」．コミットメッセージとソースコードコメントは英語．ドキュメントは英語で，必要なら日本語版も作る．リポジトリ直下の Markdown は `README.md` だけ管理（他は git add しない）．
