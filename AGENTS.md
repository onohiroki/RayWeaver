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

`chief` | `trace` | `paraxial` | `tmm` | `plot` | `optimize` | `escape` | `import`

Standard pipeline: `chief → trace → plot`. Each reads YAML from stdin, writes YAML to stdout. `--config ID` on chief/trace/paraxial/plot for multi-config selection.

`escape` (sub-subcommands: `escape` run, `escape extract --index N`) is the Ishiki-Ono style escape-function global optimiser: DLS cycles with merit-function bumps at discovered local minima. Outputs the best solution pipeline-compatible plus `escape_result.minima[]` (full surfaces per minimum).

## Key conventions

- Surfaces use `curvature` (not `radius`) as primary field. `radius` in YAML is converted at parse time.
- Chief field types: `angle` (degrees) + optional `direction [dx, dy]`, `image_height` (mm), or `height` + `object_z` (finite conjugate). Use `--wl` flag for multi-wavelength spot grids.
- `import --format zemax|oslo|codev` produces multi-config YAML with automatic chief+marginal rays.
- Optimize auto-detects multi-config mode when `shared_variables`, `local_variables`, or `configs[].merit` exist.
- Surface 0 = implicit object plane (no intersection/refraction). Ray `path` must start with `0`.
- New surface fields: `auto_aperture` (vignetting), `min_glass_path`/`max_glass_path` (edge-thickness constraints).
- `optimize --verbose` outputs JSONL on stderr; `--log FILE` writes to file.
- DLS Jacobian parallelisation: `optimization.jacobian_workers` (default GOMAXPROCS). The Jacobian loop in `internal/dls/solver.go` (`computeJacobians`/`parallelColumns`) is embarrassingly parallel and deterministic — identical results for any worker count. `applyVariables` must stay pure (no `Optimizer` mutation) for this to hold.
- `optimization.escape.escape_workers` is the top-level parallel escape goroutines (default 4). Total goroutines = `escape_workers × jacobian_workers`; recommend `jacobian_workers: 1` for many escape workers.
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

Demo scripts use `set -euo pipefail`, `RAYWEAVE="${RAYWEAVE:-./rayweave}"`, and `--clean` flag. `yq` + `gnuplot` for post-processing (not required by Go build).

## Glass catalog

AGF files + inline entries. Dispersion: Sellmeier (preferred) or Cauchy from nd+vd. `Catalog.RefractiveIndex(name, wavelength)`. AGF files in `GLASS/` (gitignored `*.agf`).

## 日本語の扱い

UTF-8 BOM なし．句読点は「，」「．」．コミットメッセージとソースコードコメントは英語．ドキュメントは英語で，必要なら日本語版も作る．リポジトリ直下の Markdown は `README.md` だけ管理（他は git add しない）．
