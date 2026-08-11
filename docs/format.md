# Pipeline document format

RayWeaver pipeline subcommands (`chief`, `trace`, `paraxial`, `vignette`,
`optimize`, `escape`, `asphere`, `psf`, `scale`, `plot`) read a single YAML
document from stdin and write a single YAML document to stdout. Every document
carries a top-level `metadata:` block that identifies it as RayWeaver-managed
and records its generation provenance.

## `metadata`

| Field                | YAML key              | Presence        | Meaning |
|---|---|---|---|
| `Tool`               | `tool`                 | required (output) | Document owner. Always `RayWeaver` on output. A non-`RayWeaver` value on input is reported as a warning. |
| `URL`                | `url`                  | required (output) | Project repository: `https://github.com/onohiroki/RayWeaver`. |
| `SchemaVersion`      | `schema_version`       | required        | Pipeline-YAML schema version (currently `1`). Replaces the legacy top-level `version` field, which is no longer written. |
| `Generator`          | `generator,omitempty`  | output-time     | The subcommand that produced this document (`chief`, `trace`, …). |
| `Command`            | `command,omitempty`    | output-time     | Subcommand arguments (excluding the binary name), for reproducibility. |
| `RayweaverVer`       | `rayweaver_version,omitempty` | output-time | `rayweave` build version (set via `-ldflags '-X main.Version=...'`; `dev` for ad-hoc builds). |
| `CreatedAt`          | `created_at,omitempty` | output-time     | Generation time, RFC3339 UTC. |
| `Notes`              | `notes,omitempty`      | input/round-trip | Free-form human annotation. |

Example of a document produced by `rayweaver chief`:

```yaml
glass_catalog:
    entries: []
metadata:
    tool: RayWeaver
    url: https://github.com/onohiroki/RayWeaver
    schema_version: 1
    generator: chief
    command: ["--field", "0", "--wl", "0.588"]
    rayweaver_version: "0.5.0"
    created_at: "2026-08-11T09:14:14Z"
configs:
    - id: config1
      surfaces: []
```

### Hand-written input

Input files written by hand or by `rayweaver import` carry only the identity
trio so they are recognisable as RayWeaver documents:

```yaml
metadata:
    tool: RayWeaver
    url: https://github.com/onohiroki/RayWeaver
    schema_version: 1
notes: "US2645157 triplet, f/4"
...
```

`metadata` is optional on input: documents without it are accepted (and produce
no warning) for backward compatibility. When present with `tool` set to something
other than `RayWeaver`, the parser emits

```
rayweave[<cmd>]: input metadata.tool = "..." is not RayWeaver; continuing
```

on stderr and continues.

## Pipeline document shape

Top-level sections (all except `metadata` may be `omitempty`):

| Section             | Tag                          | Meaning |
|---|---|---|
| `glass_catalog`     | `glass_catalog,omitempty`    | Glass / dispersion definitions. |
| `coating_catalog`   | `coating_catalog,omitempty`  | Thin-film coating material definitions. |
| `metadata`          | `metadata,omitempty`         | Document identity & provenance (above). |
| `optimization`      | `optimization,omitempty`     | Multi-config optimizer definition. |
| `configs`           | `configs,omitempty`          | Lens surfaces, fields, wavelengths, ray paths (one or more). |
| `chief`             | `chief,omitempty`            | Chief-ray / pupil-grid parameters. |
| `rays`              | `rays,omitempty`            | Explicit ray list to trace. |
| `paraxial`          | `paraxial,omitempty`        | Paraxial-analysis parameters. |
| `asphere_candidate` | `asphere_candidate,omitempty`| Asphere candidate selection settings. |
| `psf`               | `psf,omitempty`              | Point-spread-function settings. |
| `vignette`          | `vignette,omitempty`         | Vignetting solver settings. |
| `scale`             | `scale,omitempty`            | EFL-scaling settings. |

Output documents inline the full input (preserving every section and `metadata`)
and append the computed result sections on top (e.g. `chief_rays`, `results`,
`paraxial_result`, `escape_result`, etc.), so a pipeline such as
`chief → trace → plot` stays pipe-compatible at every stage.
