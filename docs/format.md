# Pipeline document format

RayWeaver pipeline subcommands (`chief`, `trace`, `paraxial`, `vignette`,
`optimize`, `escape`, `asphere`, `psf`, `scale`, `plot`) read a single YAML
document from stdin and write a single YAML document to stdout. Every document
carries a top-level `metadata:` block that identifies it as RayWeaver-managed
and records its generation provenance.

## `metadata`

| Field                | YAML key              | Presence        | Meaning |
|---|---|---|---|
| `Tool.Name`          | `tool.name`            | required (output) | Document owner. Always `RayWeaver` on output. A non-`RayWeaver` value on input is reported as a warning. |
| `Tool.URL`           | `tool.url`             | required (output) | Project repository: `https://github.com/onohiroki/RayWeaver`. |
| `Tool.Version`       | `tool.version,omitempty` | output-time | `rayweave` build version (set via `-ldflags '-X main.Version=...'`; `dev` for ad-hoc builds). |
| `Tool.SchemaVersion` | `tool.schema_version`  | required | Pipeline-YAML schema version (currently `1`). Replaces the legacy top-level `version` field, which is no longer written. |
| `Tool.Generator`     | `tool.generator,omitempty` | output-time | The subcommand that produced this document (`chief`, `trace`, …). |
| `Tool.Command`       | `tool.command,omitempty` | output-time | Subcommand arguments (excluding the binary name), for reproducibility. |
| `CreatedAt`          | `created_at,omitempty` | output-time     | Generation time, RFC3339 UTC. |
| `Notes`              | `notes,omitempty`      | input/round-trip | Free-form human annotation. |

Example of a document produced by `rayweaver chief`:

```yaml
glass_catalog:
    entries: []
metadata:
    tool:
        name: RayWeaver
        url: https://github.com/onohiroki/RayWeaver
        version: "0.5.0"
        schema_version: 1
        generator: chief
        command: ["--field", "0", "--wl", "0.588"]
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
    tool:
        name: RayWeaver
        url: https://github.com/onohiroki/RayWeaver
        schema_version: 1
notes: "US2645157 triplet, f/4"
...
```

`metadata` is optional on input: documents without it are accepted (and produce
no warning) for backward compatibility. When present with `tool.name` set to
something other than `RayWeaver`, the parser emits

```
rayweave[<cmd>]: input metadata.tool.name = "..." is not RayWeaver; continuing
```

on stderr and continues.

## Pipeline document shape

Top-level sections (all except `metadata` may be `omitempty`):

| Section             | Tag                          | Meaning |
|---|---|---|
| `glass_catalog`     | `glass_catalog,omitempty`    | Glass / dispersion definitions. |
| `coating_catalog`   | `coating_catalog,omitempty`  | Thin-film coating material definitions. |
| `spectrum`          | `spectrum,omitempty`         | Continuous source spectrum (`curve`, `range` nm, `step`/`optimize_step` nm, `entries`) used by the `spectral` wavelength set. |
| `metadata`          | `metadata,omitempty`         | Document identity & provenance (above). |
| `optimization`      | `optimization,omitempty`     | Multi-config optimizer definition. |
| `configs`           | `configs,omitempty`          | Lens surfaces, fields, wavelengths, ray paths (one or more), plus the per-config ray pipeline (`chief_rays`, `rays`, `results`) written by `chief --all-configs` and `trace`. |
| `chief`             | `chief,omitempty`            | Chief-ray / pupil-grid parameters. |
| `rays`              | `rays,omitempty`            | Explicit ray list to trace. |
| `paraxial`          | `paraxial,omitempty`        | Paraxial-analysis parameters. |
| `asphere_candidate` | `asphere_candidate,omitempty`| Asphere candidate selection settings. |
| `psf`               | `psf,omitempty`              | Point-spread-function settings. |
| `vignette`          | `vignette,omitempty`         | Vignetting solver settings. |
| `scale`             | `scale,omitempty`            | EFL-scaling settings. |
| `stop_insert`       | `stop_insert,omitempty`      | `rayweave stop` settings: multi-config placement anchor (`rear` / `front`) and the surface ID to insert. |
| `cam`               | `cam,omitempty`              | First-order cam-solve settings (cam surfaces, per-position EFL targets, fixed image plane). |

`configs[].wavelengths[]` is the design wavelength table: `value` (mm),
optional `label` (d/F/C/g), and `weight` (a non-positive weight excludes the
line). A merit term or an evaluation command selects it with
`wavelength_set: design`; the other modes are `spectral` (the document
`spectrum`) and `custom` (an explicit `wavelengths: [{value, label, weight}]`
list). The definitions and weighting rules are in
[methods/polychromatic.md](methods/polychromatic.md).

Output documents inline the full input (preserving every section and `metadata`)
and append the computed result sections on top (e.g. `chief_rays`, `results`,
`paraxial_result`, `zoom_summary`, `cam_result`, `global_result`,
`stop_insert_result`, etc.), so a pipeline such as
`chief → trace → plot` stays pipe-compatible at every stage.

The one result written *into* an input section rather than appended beside it is
`optimization.field_resolution`: a solve records there the field angles it froze
for the fields the input declares by image height. It is what the run actually
measured, so it belongs with the settings rather than in a result block (and
`clean` removes it — see [clean.md](clean.md), [optimize.md](optimize.md)).

A second, explicit exception is the **per-config ray pipeline**:
`chief --all-configs` writes `configs[].chief_rays` and `configs[].rays`, and
`trace` writes `configs[].results` for every config that carries per-config
rays. A zoom position is a complete
system state (its own surfaces, fields and wavelengths), so its rays belong to
that position, not to the document as a whole; the top-level `chief_rays[]` /
`results[]` keep their single-config meaning (the first/selected config). The
per-config `chief_rays` and `results` are computed output and are removed by
`clean`; `configs[].rays` is a ray definition and is preserved, like the
top-level `rays` section.
