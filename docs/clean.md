# `rayweave clean` — strip calculation results from a pipeline document

`clean` reads a pipeline YAML document from stdin, removes every *calculation
result* section and writes the remaining configuration back to stdout. It is the
inverse of the analysis commands: `chief | trace | plot` accumulates results,
`clean` turns the result document back into a loadable input.

No rays are traced; the command only edits the document.

## Usage

```sh
rayweave clean < result.yaml > clean.yaml

# show what was dropped
rayweave clean --verbose < result.yaml > clean.yaml 2>removed.jsonl
```

| Flag | Description |
|---|---|
| `--verbose` | print the removed fields as JSONL on **stderr**, one line per field: `{"field":"psf_results","count":3}` (`count` is the number of entries for array fields, `1` for scalar sections) |

`clean` is a YAML-only command: it takes no computation settings, so there is
nothing for the CLI/YAML three-principles rule to apply to.

## What is removed

| Field | Produced by |
|---|---|
| `chief_rays` | `chief` |
| `results` | `trace` / `trace single` |
| `paraxial_result` | `paraxial` |
| `opt_results` | `optimize` (including `spot_flatness[]`, `back_focus[]`, `glass_attraction`, `snap`, `constraints`) |
| `escape_result` | `escape` / `pso` |
| `escape_minimum` | `escape --save` (the index/merit/status provenance block written into each saved minimum file) |
| `vignetting_result` | `vignette` |
| `asphere_candidate_result` | `asphere` |
| `psf_results` | `psf` |
| `focus_comparison` | `focus mtf` / `focus psf` / `focus spot` |
| `wavefront_result` | `wavefront` |

Only the fields above are touched; a field that is absent is simply not
reported (and not listed by `--verbose`).

## What is preserved

- **Configuration**: `metadata`, `notes`, `chief` (fields, pupil model,
  reference surface, …), `configs`, `optimization`, and the analysis *settings*
  sections (`psf`, `wavefront`, `focus`, `vignette`, `asphere_candidate`, …) —
  so a cleaned document can be re-run exactly as before.
- **The top-level `stop` section** (`surface_id` / `physical_z` / `diameter`),
  which `chief` writes but which may equally be hand-entered as input; it is
  configuration, not a calculation result.
- Input data such as `rays` … — note that `results` (the traced output of
  `rays`) *is* removed, while the `rays` definitions stay.

## Metadata

Like every other subcommand, `clean` stamps the output with its own identity:
`metadata.tool.name` / `url` / `schema_version` are reset to RayWeaver's own
values (a foreign `tool.name` would otherwise survive the round trip),
`metadata.tool.generator` / `command` / `version` record this invocation and
`created_at` is refreshed. See [format.md](format.md).

## Pipeline

```sh
# analyse, then go back to a reusable input
rayweave chief < lens.yaml | rayweave clean > lens-chief.yaml

# re-run the cleaned document: configuration only, no stale results
rayweave clean < escape-result.yaml | rayweave chief
```

`clean` is also how a stale result section is dropped before piping a document
into a command that would otherwise mix old and new data (`list`, `escape`,
`optimize`).

See [README.md](README.md) for the full command index.
