# RayWeaver — agents guide

RayWeaver is a Go CLI ray tracing / lens optimization engine. Every pipeline
subcommand reads a YAML document from stdin and writes it back on stdout,
augmented with its own result section, so commands chain
(`chief | trace | plot`).

This file is the **working agreement for coding agents**: how to work in this
repository and where each fact is documented. It is deliberately *not* a feature
reference — product behaviour lives in [`docs/`](docs/README.md) and
[`samples/README.md`](samples/README.md).

## Working rules

- **Never commit unless asked.** No `git commit` / `commit --amend` / `rebase` /
  `push` / history rewrite, and no force-push, until the user explicitly asks.
  Leave the work staged or untracked in the working tree and report what is ready
  to commit (files, what changed, how it was verified).
- **Stay on the working branch.** Do not switch to or modify `main`. `origin` is
  GitHub and is not a push target; the gitea remote `private` is. Re-timestamping
  commits and force-pushing need explicit approval.
- **Long `escape` / `pso` runs: launch detached, then leave them alone.** See
  `cmd/rwrun` below. Print the optimisation conditions (merit, variable count,
  cycle budget, timeout), the PID, the save directory and the log path — then do
  **not** poll, tail the log, watch saved minima or block on it. Report only when
  the user asks.
- **Never merge a `rayweave` process's stdout and stderr.** No `2>&1`, no `|&`.
  stdout is the pipeline document; stderr carries verbose JSONL and progress. If
  both are needed, write them to separate files.
- **Keep the documentation true.** A behaviour change ships with the change to
  the document that owns it (see the map below). If the code and a document
  disagree, the code is the truth — fix the document in the same change, or say
  so in your report. Do not grow this file with feature reference.
- **Repository-root markdown**: only `README.md` is maintained here (plus this
  file). Do not `git add` other root-level markdown.
- **Do not link to gitignored artifacts** (`GLASS/*.agf`, `design/`,
  `samples/*-result.yaml`, run logs, …). Referring to them as inline code
  (`` `GLASS/*.agf` ``) is fine; a markdown link is not.
- **Japanese text**: UTF-8 without BOM, `，` and `．` for punctuation. Commit
  messages and code comments are English; documentation is English (a Japanese
  version may be added beside it when needed).
- Leave unfamiliar files and directories alone (untracked experiment folders,
  stray binaries, archived run outputs) — they are the user's until they say
  otherwise.

## Build, run, test

```sh
go build -o rayweave ./cmd/rayweave/
go build -o rayweave-mcp ./cmd/rayweave-mcp/     # optional MCP server (stdio)
./rayweave <subcommand> < input.yaml > out.yaml   # stdout = pipeline YAML
go test ./...                                    # full suite; there is no CI
```

- `cmd/rayweave` — the CLI. One file per command family; every subcommand's
  usage text lives in `main.go` (`--help` prints it).
- `cmd/rwrun` — companion binary that launches a long run detached.
- `cmd/rayweave-mcp` — companion binary that serves the subcommands to AI agents
  over the Model Context Protocol on stdio (manual: [docs/mcp.md](docs/mcp.md)).
- `cmd/importsweep` — batch sweep of a lens directory through
  `import | trace | paraxial`, CSV status report.
- `internal/<domain>` — `asphere`, `chief`, `coating`, `constraint`, `dls`,
  `escape`, `exporter`, `fft`, `glass`, `importer`, `mcpsrv`, `mesh`, `optimize`,
  `paraxial`, `polarization`, `psf`, `pso`, `pupil`, `ray`, `raymath`, `render`,
  `resource`, `runctl`, `spectral`, `surface`, `types`, `vignette`, `wavefront`.
  `runctl` is the detached-launch/SIGTERM-twice policy shared by `rwrun` and the
  MCP server; `mcpsrv` is only the protocol adapter (it must not reimplement
  YAML semantics).
- `docs/`, `samples/`, `GLASS/` — documentation, demo scripts and inputs, glass
  data.

Dependencies are `gopkg.in/yaml.v3`, `golang.org/x/image` (PNG), and the
official `github.com/modelcontextprotocol/go-sdk` for the MCP companion (its
transitive modules come with it). No Makefile, no lint/CI configuration.

Subcommands (each has a manual in [`docs/`](docs/README.md)): `chief`, `trace`,
`paraxial`, `tmm`, `plot`, `vignette`, `optimize`, `escape`, `pso`, `import`,
`export`, `scale`, `asphere`, `psf`, `focus`, `wavefront`, `query`, `clean`,
`list`, `zoom`, `cam`. `--config ID` selects a configuration on the commands
that support multi-config documents.

### Long runs: `cmd/rwrun`

A long subcommand (`escape`, `pso`, a heavy `focus psf`) routinely outlasts the
shell that launched it, and an agent harness periodically releases the workspace,
deletes the background task it registered and signals the process group — so
**every process in that group gets SIGTERM and the run dies mid-cycle**. This is
routine, not a crash (the OpenCode server logged 157 workspace "location services
evicted" events between 27 minutes and 8.5 hours, including while the agent was
working, and two escapes were killed that way). No fixed time budget survives it.
`rwrun` starts the child with `SysProcAttr{Setsid: true}` and returns at once, so
it is reparented to init — in neither the caller's process group nor its process
tree:

```sh
go run ./cmd/rwrun -n v42 -i v42-escape-input.yaml -o v42-result.yaml \
    -e v42-stderr.jsonl -- escape --verbose --keep-infeasible \
    --log v42-progress.jsonl --save v42-min
go run ./cmd/rwrun -status -n v42     # pid, elapsed, artefact sizes
go run ./cmd/rwrun -stop   -n v42     # SIGTERM twice (see below)
go run ./cmd/rwrun -purge  -n v42     # drop a stale pidfile
```

Everything after `--` goes to `rayweave` unchanged (`-- escape …` prepends the
binary; `-- ./rayweave escape …` is used as-is); one command per launch, no
chaining. The binary is resolved from `-rayweave`, then `$RAYWEAVE`, then
`./rayweave`, then `$PATH`, and the resolved path is printed in the launch
summary — **check it**, since an older copy elsewhere on `$PATH` is otherwise easy
to pick up silently. `-stop` refuses to signal a pid whose command is not a
rayweave run, so a recycled pid can never be hit.

**Stopping a run on purpose: send SIGTERM twice, never SIGKILL.** The first
signal is the slow stage — the workers finish the running DLS solve, which can
take minutes, and the completion block (`best_index` / `best_merit` / `configs`)
is appended to the document only after they return, so killing the process during
that wait loses it. The second signal aborts the running DLS within one iteration
(the best point so far is still recorded and saved) and the run still writes its
document and exits 0. A third signal is a force quit and is never sent; `kill -9`
always loses the document. With streaming on (the default) the stdout document is
appended to as the run progresses, so a kill leaves every minimum found so far
and only drops the completion block; with `--stream false` a kill leaves stdout
empty, because the document is written once at the end. The `--save` minima
survive either way (atomic writes), and `rayweave list global < run.jsonl`
rebuilds the aggregate from the log. Full manual:
[docs/rwrun.md](docs/rwrun.md); the signal stages are also documented in
[docs/escape.md](docs/escape.md).

## Documentation map

`docs/README.md` is the full index (conventions, platform notes), and
[`docs/methods/README.md`](docs/methods/README.md) indexes the numerical-method
documents. Pick the owning document from this table; when a fact is missing
everywhere, add it to the document that owns the topic, not to this file.

| Topic | Document |
|---|---|
| Document shape, `metadata`, field naming | [docs/format.md](docs/format.md) |
| Glasses: material forms, AGF + inline catalogue, precedence, manufacturer priority | [docs/glass-catalog.md](docs/glass-catalog.md) |
| Foreign formats in / out | [docs/import.md](docs/import.md), [docs/export.md](docs/export.md) |
| Chief rays, pupil grids, spot statistics, clear aperture | [docs/chief.md](docs/chief.md), [methods/chief-rays-and-spot.md](docs/methods/chief-rays-and-spot.md) |
| Ray tracing and results | [docs/trace.md](docs/trace.md), [methods/ray-tracing.md](docs/methods/ray-tracing.md) |
| First-order analysis, cardinal points, element glass roles | [docs/paraxial.md](docs/paraxial.md), [methods/paraxial.md](docs/methods/paraxial.md) |
| Multi-config (zoom) per-position summary | [docs/zoom.md](docs/zoom.md) |
| First-order cam solve (zoom group airspaces, fixed image plane) | [docs/cam.md](docs/cam.md), [methods/cam.md](docs/methods/cam.md) |
| Vignetting and `auto_aperture` sizing | [docs/vignette.md](docs/vignette.md) |
| Diagrams | [docs/plot.md](docs/plot.md) |
| EFL scaling | [docs/scale.md](docs/scale.md), [methods/efl-scaling.md](docs/methods/efl-scaling.md) |
| Coatings (transfer matrix) | [docs/tmm.md](docs/tmm.md), [methods/thin-film-tmm.md](docs/methods/thin-film-tmm.md) |
| DLS optimization, variables, hard solves, apertures, glass hull/attraction, snap | [docs/optimize.md](docs/optimize.md) |
| Merit terms and constraints (full catalog), contrast/MTF kinds | [docs/merit-and-constraints.md](docs/merit-and-constraints.md), [docs/contrast.md](docs/contrast.md), [methods/merit-functions.md](docs/methods/merit-functions.md) |
| DLS internals, determinism, adaptive damping, active-set region method | [methods/dls-optimization.md](docs/methods/dls-optimization.md), [methods/region-active.md](docs/methods/region-active.md) |
| Escape-function global optimization, save/stream/signals | [docs/escape.md](docs/escape.md), [methods/escape-function.md](docs/methods/escape-function.md) |
| Particle-swarm optimization | [docs/pso.md](docs/pso.md), [methods/pso.md](docs/methods/pso.md) |
| Glass dispersion and the real-glass hull | [methods/glass-dispersion.md](docs/methods/glass-dispersion.md) |
| Asphere candidates, coefficients, validation | [docs/asphere.md](docs/asphere.md), [methods/asphere-candidates.md](docs/methods/asphere-candidates.md) |
| PSF / Huygens integral / MTF, image-plane comparison, wavefront analysis | [docs/psf.md](docs/psf.md), [docs/focus.md](docs/focus.md), [docs/wavefront.md](docs/wavefront.md), [methods/psf.md](docs/methods/psf.md) |
| Pupil-grid generation and grid tracing | [methods/pupil-grids.md](docs/methods/pupil-grids.md) |
| Read-only listing, YAML/JSONL selection | [docs/list.md](docs/list.md), [docs/query.md](docs/query.md) |
| Stripping results from a document | [docs/clean.md](docs/clean.md) |
| Detached long runs | [docs/rwrun.md](docs/rwrun.md) |
| MCP server, tools, jobs, resources, client setup | [docs/mcp.md](docs/mcp.md) |
| Windows / PowerShell notes | [docs/windows.md](docs/windows.md) |
| Demo scripts and their inputs, generated outputs | [samples/README.md](samples/README.md) |

## CLI options vs input YAML (three principles)

Every YAML-pipeline subcommand follows:

1. **YAML-specifiable** — every computation-setting CLI flag has a counterpart in
   the input YAML section (`--wl` ↔ `chief.wavelength`, `--rings` ↔
   `asphere_candidate.cell_rings`, `--psf-grid` ↔ `psf.grid_size`, `--max-freq` ↔
   `psf.mtf_config.max_frequency`, `--glass-dir` ↔ `glass_catalog.directory`, …).
   A flag's default is always the *unset* value (0 / "" / false), so an unset
   flag never overrides YAML.
2. **CLI wins** — when both are present the flag wins, resolved with `flagWasSet`
   so "not given" stays distinguishable from a default.
3. **Write-back** — a run that used CLI options writes the effective (flag-won)
   values into the corresponding section of the output document. Scalars are
   written back only for flags actually set; built-in defaults are never injected
   into every output. `psf` is the reference implementation (`writeBackPSF`).

Exemptions (deliberate, documented in the owning manual):

- `--config` selects a config; it is not a setting.
- `plot` is a terminal renderer (SVG/PNG); its render flags never enter the
  pipeline YAML.
- `import` / `export` read and write a foreign format, so there is no input-YAML
  setting to overwrite. `export -o FILE` writes the foreign format to FILE and
  passes the input YAML through unchanged (like `plot -o`); the format is inferred
  from the extension when `--format` is omitted.
- Action/stream flags record their **effect**, not a setting: chief
  `--clear-aperture*` / `--marginal-rays` / `--preserve-rays` / `--ray-fan` /
  `--fan-plane` / `--fan-rotation` (effect: diameters, `rays[]`, `ray_fan`),
  `--verbose` / `--log`, `escape --save` (effect: `global_result.minima[].file`),
  `psf --yaml` / `--csv` (effect: `psf_results[].output_file`).
- `psf --focus-plane all` is action-style: the shift is reported on stderr and
  nothing is written back; `focus` is the pipeline form of the same comparison.
- `escape extract --index` selects a stored minimum.

## Code conventions

- `curvature` is the primary surface field; `radius` in YAML is converted at
  parse time. All lengths are millimetres, coating thicknesses nanometres
  (converted internally); Z is the optical axis, positive to the right.
- **No "primary + 2" ordinal field pairs.** A second-of-pair field takes a
  semantic name stating its role or order (`weight_from`/`weight_to`,
  `anchor_from`/`anchor_to`, `back_surface`, `comparison_wavelength`), never a
  numeric suffix — `surface2`/`wavelength2` were migrated to
  `back_surface`/`comparison_wavelength`. `edge_thickness` takes `surface` /
  `back_surface` and defaults the back surface to the next one in system order.
- Surface 0 is the implicit object plane (no intersection, no refraction), and a
  ray `path` must start with `0`.
- **Thicknesses are always positive.** A mirror is folded with
  `decenter: [{tilt: [0, 180, 0], scope: both}]` plus a top-level `reflect: true`
  (tilt in degrees); a negative thickness is a parse-time error. After an odd
  number of reflections the beam-frame radius is the negation of the physical
  radius, and both the paraxial trace and the ray trace consume that beam-frame
  value. `role: "stop"` is inert metadata — the stop is `chief.stop_surface`, used
  only when it is set.
- **There is no implicit aperture stop.** Without an explicit stop the pupil is
  *dynamic* (the in-lens crossing of each field's chief ray), and
  `chief.pupil_model` adds a third, *virtual entrance pupil* mode that pins the
  plane position and diameter. `paraxial` and the importers never infer a stop.
- Every subcommand stamps the output document's `metadata.tool` (name, url,
  schema version, generator, command, build version) and `created_at`. The input
  document is preserved and results are appended, so chained pipelines keep
  working; a document without `metadata` (or with a foreign `tool.name`) still
  parses, with a warning.
- **Determinism is a contract.** `applyVariables` must stay pure (no `Optimizer`
  mutation) so the finite-difference Jacobian columns stay independent; the
  Delaunay cavity rebuild sorts its boundary edges and the wavefront-sample sort
  uses an index tiebreak, so results are bit-identical for any worker count.
  Read [methods/dls-optimization.md §2a](docs/methods/dls-optimization.md) (and the
  `TestTriangulateDeterministic` regression test) before touching either.
- Code comments and commit messages are in English and explain *why*, not *what*.

## Sample scripts

- External dependencies are `gnuplot` (optional renderings) and `yq` (only for
  options that edit YAML inputs). **No Python** and no other external tools; POSIX
  shell utilities are fine.
- Every script uses `set -euo pipefail`, accepts `--clean`, and on `--clean`
  deletes exactly the files it generated before exiting.
- Scripts are location-independent: every path resolves against the script's own
  directory (`SCRIPT_DIR`), and the binary is found as `$RAYWEAVE` →
  `$SCRIPT_DIR/rayweave` → `$SCRIPT_DIR/../rayweave` → `rayweave` on `$PATH`.
- A new script is added to the index in [samples/README.md](samples/README.md)
  with its input files, purpose, options and generated outputs, and its `--clean`
  path is exercised.