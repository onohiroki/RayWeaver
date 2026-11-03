# `importsweep` — batch lens-directory sweep

`importsweep` is a developer companion binary (not a pipeline subcommand): it
drives every foreign lens file under a directory through the same stages as

```
rayweave import | rayweave chief | rayweave trace | rayweave paraxial
```

and writes two CSV reports. It reads files from disk and writes files, so there
is no stdin/stdout pipeline and no YAML document to chain.

```
importsweep [-root DIR] [-glass-dir DIR] [-out FILE] [-summary FILE] [-dump SUBSTRING]
```

## Options

| Flag | Description |
|---|---|
| `-root DIR` | data directory to walk recursively (default `untrack-lens-data`) |
| `-glass-dir DIR` | AGF glass catalog directory; empty means none, like a plain `rayweave import` |
| `-out FILE` | per-file CSV (default `<root>/sweep_results.csv`) |
| `-summary FILE` | aggregate CSV (default `<root>/sweep_summary.csv`) |
| `-dump SUBSTRING` | debug: print per-surface data for paths containing SUBSTRING |

## Inputs

The walk classifies files by extension:

| Extension | Format |
|---|---|
| `.zmx` | ZEMAX |
| `.len` | OSLO |
| `.seq` | CODE V |
| `.zda`, `.zar` | binary ZEMAX archives — reported as skipped, never parsed |

Everything else (including the CSVs the tool writes) is ignored. Files are
swept in sorted path order, and the work is parallelised over `GOMAXPROCS`
workers.

## Output

`sweep_results.csv` has one row per lens with the columns of the per-file
record: path/group/format, parse status and error, surface/field/wavelength/
glass counts, unresolved glass names, stop and diameter state, chief-ray and
marginal-ray counts, grid errors, spot RMS, trace totals and failures,
paraxial results (EFL, EPD, f-number, track) and the aperture-stop/vignetting
diagnostics. `record.row()` must stay aligned with the `header` slice — a test
pins the column count.

`sweep_summary.csv` aggregates the same run per group (directory): totals,
parse/chief/trace/paraxial failures, vignetted and model-failure counts, and a
critical/problem tally.

The tool also prints a one-line summary of swept and skipped files, followed by
the summary rows.

## Relationship to the CLI

The sweep re-implements the CLI stages (`import`, the FNO→stop fill, the chief
pass, marginal-ray extraction, tracing and the paraxial trace) so it can run
without spawning processes. Those stages must mirror `cmd/rayweave`; the tests
in `cmd/importsweep/main_test.go` pin the CSV contract, the directory walk and
a clean end-to-end sweep of a one-lens directory, but not the optical numbers.
When a CLI stage changes, check the sweep's copy too.
