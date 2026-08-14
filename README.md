# RayWeaver

RayWeaver is a CLI ray tracing engine for optical systems, written in Go.

## Features

- Sequential ray tracing through spherical and aspheric surfaces
- Chief-ray (spot centroid) determination with hexagonal / polar / square pupil grids
- Paraxial (first-order) analysis: EFL, principal points, f/#, entrance/exit pupils
- Thin-film coating analysis via the transfer-matrix method
- SVG cross-section diagram generation
- Support for decentered and tilted elements
- Folded systems (mirrors): positive thicknesses only, fold via `decenter: [{tilt: [0, 180, 0], scope: both}]` + top-level `reflect: true`, fold-aware paraxial/chief/ray tracing
- Glass dispersion via Sellmeier or Cauchy models
- Jones-matrix polarization tracking
- Point-spread function via direct vector Huygens integration (no FFT, fisheye-safe, RCP/LCP/X/Y input)
- DLS (damped least squares) local optimization of lens surfaces
- Escape-function global optimization that finds multiple local minima
- Parallel computation for pupil grids and optimization Jacobians
- YAML-based input/output, pipeable between subcommands

## Build

```sh
go build -o rayweave ./cmd/rayweave/
```

No external dependencies beyond `gopkg.in/yaml.v3`.

Windows usage is covered in [docs/windows.md](docs/windows.md): installing Go
and PowerShell 7.2+ (the recommended shell), the `rayweaver.exe` build, and
running the bash demo scripts under Git Bash / MSYS2 / WSL. Linux and macOS
need no special handling — the build, `docs/`, and `samples/*.bash` all work as
written.

## Quick start

```sh
./rayweave trace < samples/us2645157.yaml
bash samples/run-demo.bash
bash samples/optimize-demo.bash
```

## Subcommands

Subcommands are grouped by their role in the data flow
`system → ray bundle → quantities`, with each stage piping YAML to the next.

| Category | Subcommand | Role | Input → Output |
|---|---|---|---|
| Data | `import` | Convert an external lens file into the internal YAML format. | ZMX / SEQ / LEN → YAML system |
| Data | `export` | Write the internal YAML format back out as a native lens file. `--format zemax\|codev\|oslo`; every config by default (ZEMAX multi-config / CODE V zoom positions) or `--config` for one. | YAML system → ZMX / SEQ / LEN |
| Propagation | `trace` | Trace individual rays and report per-surface intersection data (low-level). | rays → per-surface results |
| Propagation | `chief` | Sample the beam for each field: chief ray, dynamic pupil (per-field entrance/exit pupil from the chief-ray crossings when no `stop_surface` is set), pupil grid, marginal rays, spot statistics and OPL. Flags: `--clear-aperture` (size `auto_aperture: true` surfaces to the beam footprint + margin; `--clear-aperture-margin-mm`, `--clear-aperture-rays`), `--preserve-rays` (keep the existing rays section during aperture adjustment), `--marginal-rays`, `--pass-through N`, `--config ID`, `--wl`. | system + fields → chief_rays / grid |
| Analysis | `paraxial` | First-order / cardinal properties: EFL, BFL, FFL, principal points, pupil positions, f/#. | system → paraxial_result |
| Analysis | `tmm` | Thin-film coating analysis: reflectance, transmittance, phase. | system + coating → R/T/phase |
| Transform | `scale` | Uniformly scale a system so its EFL equals `--efl TARGET` (exact; preserves f/#). Useful for building a starting point before optimizing. | system → scaled system |
| Synthesis | `vignette` | Iteratively settle per-field vignetting and `auto_aperture` surface diameters using the dynamic pupil; glass-path (edge-thickness) rejection and fixed (`auto_aperture: false`) surfaces narrow the beam. Flags: `--iterations`, `--min-glass-path`, `--margin-mm`, `--config`. See `docs/vignette.md`. | system → sized system + `vignetting_result` |
| Synthesis | `optimize` | DLS optimization of lens surfaces. Reads `optimization` and `configs` sections from YAML. `--verbose` also emits a per-term merit breakdown. `--exclude-param` drops target params (e.g. asphere coefficients) from the variable set. | system + merit → optimized system |
| Synthesis | `escape` | Escape-function global optimization (Ishiki-Ono style): repeatedly run DLS, adding a smooth merit-function bump at each discovered local minimum so the next run escapes the valley and finds other minima. Sub-command `escape extract --index N` pulls one minimum out as a clean lens. Flags: `--glass-dir`, `--verbose`, `--log FILE`, `--save FILE` (versioned per-minimum files), `--index N` (extract). See `docs/escape.md`. | system + merit → best solution + `escape_result` |
| Synthesis | `asphere` | Rank candidate surfaces for aspheric introduction and fit safe initial even-order asphere coefficients (conic + A4..A12) from the per-field OPD residuals. `--validate` runs a short DLS against the spot RMS per fitted surface (`validation:` block with the DLS-solved coefficients); `--apply` inserts the top-ranked validated asphere (implies `--validate`) so `asphere --validate --apply \| chief \| trace \| plot` shows all-spherical vs aspherized. See `docs/asphere.md` and `docs/methods/asphere-candidates.md`. | system + `chief` fields → `asphere_candidate_result` (+ modified system with `--apply`) |
| Analysis | `psf` | Point-spread function on the flat image plane via per-field polarized ray tracing, non-uniform wavefront sampling (Delaunay-triangulated reference surface) and a direct vector Huygens integral (no FFT; fisheye-safe). Flags: `--ref-surface`, `--psf-grid`, `--psf-width`, `--num-rays`, `--fields`, `--wavelengths`, `--polarization RCP\|LCP\|X\|Y\|RCP+LCP`, `--yaml FILE` (full structured data), `--csv FILE` (gnuplot map). See `docs/psf.md` and `docs/methods/psf.md`. | system + `chief` fields → `psf_results` summary (+ `--yaml`/`--csv` files) |
| Presentation | `plot` | Render an SVG or PNG cross-section diagram. Flags: `-o file.svg|.png`, `--lens-width`, `--ray-width`, `--scale`, `--right-margin`, `--config`. | system + rays → diagram |
| Tooling | `query` | Read-only YAML/JSONL selector: extract values, iterate arrays, aggregate, evaluate expressions and pass-gates from a shell pipeline. Replaces `python3 + PyYAML` / `yq` in the sample demos. See `docs/query.md`. | YAML/JSONL → plain text / YAML / JSON / CSV |

## Documentation

Per-subcommand usage manuals live in [`docs/`](docs/):

| Document | Covers |
|---|---|
| [docs/import.md](docs/import.md) | ZEMAX / OSLO / CODE V import |
| [docs/export.md](docs/export.md) | ZEMAX ZMX / CODE V SEQ / OSLO LEN export |
| [docs/trace.md](docs/trace.md) | low-level ray tracing |
| [docs/chief.md](docs/chief.md) | chief rays, pupil grids, spot stats, ray fans, clear aperture |
| [docs/paraxial.md](docs/paraxial.md) | first-order / cardinal analysis |
| [docs/tmm.md](docs/tmm.md) | thin-film coating analysis |
| [docs/vignette.md](docs/vignette.md) | vignetting and `auto_aperture` diameter settlement |
| [docs/plot.md](docs/plot.md) | SVG / PNG diagrams |
| [docs/scale.md](docs/scale.md) | EFL scaling |
| [docs/optimize.md](docs/optimize.md) | DLS optimization |
| [docs/escape.md](docs/escape.md) | escape-function global optimization |
| [docs/asphere.md](docs/asphere.md) | asphere candidate ranking and initial coefficient estimation |
| [docs/psf.md](docs/psf.md) | point-spread function via direct vector Huygens integration |
| [docs/query.md](docs/query.md) | YAML/JSONL selector |
| [docs/windows.md](docs/windows.md) | Windows usage: Go/PowerShell 7.2+ install, `rayweaver.exe` build, bash demos |

The numerical methods behind the analyses and optimizations are described
separately in [`docs/methods/`](docs/methods/README.md) (ray tracing, chief-ray
and spot computation, paraxial optics, merit functions, DLS, escape functions,
glass dispersion, thin-film TMM, asphere candidate selection, point-spread
function via vector Huygens integration, and EFL scaling).

## Pipeline examples

```sh
# Ray-path trace
./rayweave trace < samples/us2645157.yaml

# Chief ray with spot diagrams + paraxial analysis
./rayweave chief < samples/us2645157.yaml | tee chief-result.yaml \
  | ./rayweave paraxial

# SVG raytrace diagram (centroid-based chief rays)
cat samples/us2645157.yaml \
  | ./rayweave chief --clear-aperture \
  | ./rayweave chief --marginal-rays \
  | ./rayweave trace \
  | ./rayweave plot -o diagram.svg

# PNG raytrace diagram (same pipeline, just change extension)
cat samples/us2645157.yaml \
  | ./rayweave chief --clear-aperture \
  | ./rayweave chief --marginal-rays \
  | ./rayweave trace \
  | ./rayweave plot -o diagram.png

# SVG raytrace diagram (stop-centre chief rays via --pass-through)
cat samples/us2645157.yaml \
  | ./rayweave chief --pass-through 5 --clear-aperture \
  | ./rayweave chief --marginal-rays \
  | ./rayweave trace \
  | ./rayweave plot -o diagram-stop.svg

# DLS optimization
./rayweave optimize < samples/optimize-demo.yaml > optimized.yaml

# DLS optimization with verbose progress (JSONL on stderr)
./rayweave optimize --verbose < samples/optimize-demo.yaml > optimized.yaml

# DLS optimization with progress logged to a file (JSONL)
./rayweave optimize --log /tmp/opt-progress.jsonl < samples/optimize-demo.yaml > optimized.yaml

# Escape-function global optimization (finds multiple local minima)
./rayweave escape < samples/escape-demo.yaml | tee escape-result.yaml
# or on the double-Gauss (slow): bash samples/escape-demo.bash --lens doublegauss

# Extract local minimum N as a clean lens YAML
./rayweave escape extract --index 1 < escape-result.yaml > min1.yaml
./rayweave escape extract --index 1 < escape-result.yaml | rayweave trace | rayweave plot -o min1.svg

# Scale a reference design to a target focal length, then optimize it
cat reference25mm.yaml | ./rayweave scale --efl 50 | ./rayweave optimize > optimized.yaml

# TMM coating analysis
./rayweave tmm < samples/ar-coating.yaml
```

## Optimization variables

`optimize` accepts `curvature`, `thickness`, `diameter`, `nd`/`vd` (glass), and —
on aspheric surfaces (`asphere_polynomial`) — `conic` and the even polynomial
coefficients `a4`/`a6`/`a8`/`a10`/`a12` (also addressable as
`coefficient_0`…`coefficient_4`). For example:

```yaml
optimization:
  variables:
    - {name: s1_conic, target: {type: surface, id: 1, param: conic}, min: -1, max: 1, active: true}
    - {name: s1_a4,    target: {type: surface, id: 1, param: a4},    min: -1e-3, max: 1e-3, active: true}
```

Notes:

- Constraint kinds: `equality`, `inequality_upper`, `inequality_lower`, `band`,
  `fuzzy`. Multiple `equality` constraints are supported (satisfiable targets
  converge; an unreachable target is reported with a warning).
- `optimization.aperture_margin` is clamped to ≥ 1.0 (smaller values make the
  pupil grid smaller than the aperture and stall DLS).
- `optimization.jacobian_workers` sets the number of goroutines used for the
  DLS finite-difference Jacobian (default `GOMAXPROCS`; the `escape` command
  defaults an unset value to 2 instead). The Jacobian is deterministic: the
  result is identical for any worker count.
- `optimization.escape.escape_workers` sets the top-level parallel escape
  goroutines (default 4). Each escape worker runs a DLS solve that itself
  parallelises its Jacobian across `jacobian_workers`, so the total goroutine
  count is `escape_workers × jacobian_workers`; set `jacobian_workers: 1` when
  running many escape workers to avoid oversubscription.
- `optimization.escape` execution tuning extends the defaults: `escape_iter_frac`
  caps escape-phase DLS iterations (default 1/3), `stall_early_stop` (default
  true) ends an escape-phase solve whose best merit stalls over
  `stall_window_frac` (default 0.2) with a relative change below `stall_rel_tol`
  (default `1e-4`) — the clean phase always runs the full budget; `w_span`
  (default 2.0) widens per-worker escape-bump widths and `initial_perturb`
  (default 0.05) spreads worker start points in the normalised variable space.
- `optimization.escape.max_seconds` adds a soft wall-clock budget (seconds,
  shared by all workers) to the escape search: expiry is checked between DLS
  runs, so a running solve always finishes. The output marks `timed_out: true`
  when the search was cut short.
- `configs[].ray_paths` is render-only metadata; the optimizer ignores it.
- `edge_thickness` constraints take the back surface explicitly via `surface2`.
- `optimization.escape` enables the escape-function global optimizer (see
  `docs/escape.md` and `docs/methods/escape-function.md`). It wraps the same
  `variables`/`merit` definitions as `optimize`; the best solution is written
  to the top-level configs (pipeline-compatible) and every discovered local
  minimum is listed in the `escape_result` section, with `features` (per-config
  thin-lens `element_powers` of each lens element) as a compact fingerprint for
  comparing the minima. A repeat of a known minimum with a better merit replaces
  the stored point ("keep the better data"). `escape --save FILE` writes each
  minimum to `FILE0.yaml`, `FILE1.yaml`, … (improvements are kept as
  `FILE N.<version>.yaml`), and a `SIGINT`/`SIGTERM` stops the search gracefully
  (`interrupted: true`, exit 0): the first signal waits for the cycle boundary,
  the second interrupts the running DLS within one iteration (preserving its
  best point so far), and the third force-quits.
- `optimize` also stops gracefully on `SIGINT`/`SIGTERM` (`interrupted: true`,
  exit 0): the first signal interrupts the running DLS within one iteration and
  writes the best point found so far to stdout; the second force-quits.


## Sample data

The [`samples/`](samples/) directory contains:

- `us2645157.yaml` — triplet-derivative lens from US patent 2,645,157 (MIT license, ©2014 Daniel J. Reiley). Converted from a ZMX file obtained from [lens-designs.com](https://www.lens-designs.com/), validated against [LensForge](https://www.ripplon.com/LensForge/) trace output.
- `us2645157-degraded.yaml` — same triplet with perturbed curvatures (pin-blur starting state) and `optimization` + `configs` sections for the `optimize` subcommand.
- `optimize-demo.bash` — draws SVG cross-sections of the initial (degraded) and optimized lens systems, demonstrating before/after comparison.
- `ar-coating.yaml` — single-layer MgF2 AR coating on N-SK16.
- `dielectric-mirror.yaml` — 9-layer quarter-wave Bragg reflector (SiO2/TiO2).
- `run-demo.bash` — end-to-end demo script producing spot diagrams, SVG, and TMM results.
- `psf-mtf-demo.yaml` + `psf-mtf-demo.bash` — PSF/OTF/MTF demo on the escape-optimised US2645157 triplet: a single `rayweave psf` run (RCP+LCP) computes the PSF and FFT-derived OTF/MTF, then prints a Strehl/FWHM/EE50/Airy/MTF50-30-10 table (MTF reported up to 200 c/mm via `--max-freq`) and draws per-field pm3d maps, a radial-profile overlay, and an MTF overlay (sagittal/tangential per field). The 0/16/24° fields show a monotone 0° > 16° > 24° Strehl profile (~0.99/0.62/0.59); `--lens doublegauss` (or any YAML path) switches to another lens with the MTF cap kept by the CLI flag.
- `optimize-demo.bash` — draws before/after SVG cross-sections of the US2645157 triplet (degraded → optimized).
- `README.md` — detailed documentation of all sample files and workflow.

The [`samples/`](samples/) directory also includes generated artifacts (spot-diagram data, SVG diagrams, chief-ray results) produced by the demo pipeline.

## Units

All units are millimetres (wavelengths, thicknesses, radii, coordinates). Coating layer thicknesses are in nanometres (converted internally). The Z axis is the optical axis (positive right). Surface 0 is the implicit object plane.

## Dependencies

| Library | License |
|:---:|:---:|
| [gopkg.in/yaml.v3](https://github.com/go-yaml/yaml) | MIT |
| [golang.org/x/image](https://pkg.go.dev/golang.org/x/image) | BSD |

## Credits

The CODE V / ZEMAX importers draw on [ray-optics](https://github.com/rayoptics/rayoptics)
for command semantics (e.g. TLA dispatch, decenter/return transforms). Thanks to
the ray-optics authors for open-sourcing it.

## License

This project is MIT licensed. The sample lens data in `samples/us2645157.yaml` is derived from US patent 2,645,157 and carries the original MIT license (© 2014 Daniel J. Reiley).
