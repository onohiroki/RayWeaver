# RayWeaver over MCP

`cmd/rayweave-mcp` serves the RayWeaver subcommands to AI agents over the
[Model Context Protocol](https://modelcontextprotocol.io/) (stdio transport).
It is a **protocol adapter**, not a second optics engine: every tool call builds
a `rayweave <subcommand>` command line, feeds a pipeline document to its stdin,
and turns its stdout (the document) and stderr (diagnostics) into a document
handle, a compact summary and a warnings list.

Nothing about the YAML semantics is reimplemented here. The CLI stays the single
source of truth for parsing, defaults, glass resolution, merit terms, the clean
`--flag`-wins-over-YAML rule and the write-back of effective values. An agent
that uses these tools therefore produces exactly the documents the CLI produces.

## Why a companion binary, not `rayweave mcp`

- **stdout is the JSON-RPC stream.** A rayweave subcommand writes its pipeline
  document to stdout, and a long-lived server must keep stdout protocol-only, so
  every computation runs in a child process whose two streams stay separate.
- **Isolation.** The CLI exits from 200+ places and uses package-level state
  (warning sinks, the active subcommand tag). A subprocess call keeps all of that
  out of the server process, and a crashed or cancelled run cannot take the
  server — or another tool call — down with it.
- **The long-run rules already exist.** `cmd/rwrun` and the MCP server share
  [`internal/runctl`](../internal/runctl) for binary resolution, a
  session-detached launch (`setsid`) and the two-stage SIGTERM stop. See
  [rwrun.md](rwrun.md) and [escape.md](escape.md).

A future in-process implementation can implement the same `Runner` surface
without changing any tool schema.

## Build and run

```sh
go build -o rayweave ./cmd/rayweave/
go build -o rayweave-mcp ./cmd/rayweave-mcp/
```

The server finds the engine in this order: `-rayweave PATH`, `$RAYWEAVE`,
`./rayweave`, `rayweave` on `$PATH`, then beside (or above) the
`rayweave-mcp` binary. The resolved path is printed on stderr at startup —
check it, because an older copy elsewhere on `$PATH` is easy to pick up
silently.

| Option | Default | Meaning |
|---|---|---|
| `-rayweave PATH` | `$RAYWEAVE`, `./rayweave`, `$PATH`, beside the server | engine binary |
| `-workspace DIR` | `$RAYWEAVE_WORKSPACE`, else `./.rw-mcp` | documents, jobs, plots |
| `-glass-dir DIR` | `$RAYWEAVE_GLASS_DIR` | default AGF catalog directory (made absolute) |
| `-tools core\|all` | `$RAYWEAVE_MCP_TOOLS`, else `core` | exposed tool set |
| `-max-inline-bytes N` | 2 MiB | larger documents must use `document_id` or `path` |
| `-max-jobs N` | 2 | concurrent `escape`/`pso` jobs |
| `-version` | | print the version and exit |

The repository root is looked up for `docs/` and `samples/` resources: from
`$RAYWEAVE_ROOT`, the working directory (walking up), then beside the server
binary. Set `RAYWEAVE_ROOT` when the binary is installed away from a checkout.

## Client configuration

OpenCode (`opencode.json`), with a generous timeout because `optimize` and
`psf` are synchronous:

```json
{
  "mcp": {
    "rayweave": {
      "type": "local",
      "command": ["/path/to/rayweave-mcp"],
      "environment": {
        "RAYWEAVE": "/path/to/rayweave",
        "RAYWEAVE_WORKSPACE": "./.rw-mcp"
      },
      "timeout": 120000
    }
  }
}
```

`RAYWEAVE` and `RAYWEAVE_WORKSPACE` are the environment forms of `-rayweave`
and `-workspace`. A local stdio server inherits the client's working directory,
which is why the default workspace is relative.

The server never prints a pipeline document on its own stdout, and the client
must not merge stderr into stdout: stderr carries the startup line and, during a
call, nothing from the child is copied there (child diagnostics are summarised
into the tool result).

## Documents and the workspace

A document input is exactly one of:

| Field | Meaning |
|---|---|
| `document` | inline RayWeaver pipeline YAML (up to `-max-inline-bytes`) |
| `document_id` | id returned by an earlier call in the same workspace |
| `path` | a YAML file inside the workspace |

Every document-producing result carries:

| Field | Meaning |
|---|---|
| `document_id` | content-addressed id: the first 12 hex digits of the SHA-256 of the bytes |
| `document_uri` | `rayweave://documents/<id>` |
| `summary` | compact, JSON-safe result view (large arrays folded to counts) |
| `warnings` | the `Warning:` lines of the command |
| `diagnostics` | the last plain stderr lines (progress JSONL is omitted) |
| `elapsed_ms` | wall time of the child |
| `truncated` | the summary was compacted; read the document for the rest |

Content addressing means the same input plus the same settings produce the same
id, and passing the same `document_id` twice never writes the file twice. The
workspace is a cache plus job store:

```
.rw-mcp/
  documents/<id>.yaml        produced documents
  documents/index.jsonl      append-only index (newest last)
  jobs/<job-id>/             one escape/pso run (see below)
  plots/                     PNG/SVG renderings
  <export files>             whatever `export -o` wrote
```

Child processes run with the workspace as their working directory, so a
relative `glass_catalog.directory` or `path` in a document resolves inside the
workspace; the server's own `-glass-dir` is made absolute. `path` arguments are
refused if they escape the workspace. No shell is involved: arguments are passed
as an argv vector.

## Tools

Settings are **CLI flags**, never YAML edits. A tool argument that is unset
(empty string, `false`, zero, `null`) is not passed, so the document keeps its
own value; a set argument wins and the command writes the effective value back
into the result document. `config` is the exception in kind: it is a selector,
not a setting.

### `core` (default)

| Tool | Purpose |
|---|---|
| `validate` | parse the document and load its glasses (`list surfaces`); cheap pre-flight |
| `list` | read-only surfaces/glasses/paraxial/fields/rays/merit/optimization/global/focus listing, JSON by default |
| `query` | read-only selector: one value, a table, `--count`/`--len`/`--sum`, JSONL run logs |
| `paraxial` | first-order analysis (EFL, BFL, pupils, cardinal points, element roles) |
| `chief` | chief rays and pupil grids: per-field spot statistics, optional ray fan and clear apertures |
| `trace` | trace the document's `rays`/`chief_rays` (usually chained after `chief`) |
| `optimize` | DLS solve (or `snap`); progress notifications when the client requests them |
| `plot` | render the lens: PNG returned as an image, SVG written to the workspace |
| `pipeline` | chain up to 8 document-to-document stages in one call |
| `escape` | escape-function global search, **as a job** |
| `pso` | particle-swarm global search, **as a job** |
| `job_status`, `job_stop`, `job_result`, `job_list` | job control |

### `all`

Adds `zoom`, `cam`, `vignette`, `asphere`, `tmm`, `scale`, `psf`, `focus`,
`wavefront`, `clean`, `import` and `export`. The full tool set is 27 tools;
`core` exists to keep a small model's tool list (and context cost) manageable,
not because the extended tools are less stable.

### `pipeline`

```json
{
  "document": "...",
  "stages": [
    {"command": "chief", "args": ["--clear-aperture", "--marginal-rays"]},
    {"command": "trace"},
    {"command": "paraxial"}
  ]
}
```

Stages read the previous stage's document; the final document is stored and
summarised, and every stage id is returned. `plot`, `list`, `query`, `import`,
`export`, `escape` and `pso` are not pipeline stages, and file-writing flags
(`-o`, `--save`, `--log`, `--yaml`, `--csv`, profiles) are rejected — the
dedicated tool owns those outputs.

### Summaries

`paraxial`, `chief`, `trace`, `optimize`, `escape`, `pso` and `focus` reuse the
CLI's own `list ... --format json` view, so the summary means the same thing as
the terminal table. The other commands summarise the result sections directly:
configs become ids and surface counts, `rays`/`grid_points` become counts,
`results[]` keeps its scalars and drops the per-surface path, `chief_rays[]`
keeps per-field `spot_stats`. A summary is compacted (array head + count, then
lower limits) until it fits the budget; `truncated` says so and the full
document stays one resource read away.

## Jobs: `escape` and `pso`

Global searches are never synchronous. The tool writes the input, starts the
engine with `setsid` (its own session, reparented to init) and returns a job id
at once. The same isolation as `rwrun` means an MCP server restart, a client
timeout or an agent harness releasing the workspace does not kill the run.

```
jobs/<job-id>/
  input.yaml       the document as given
  out.yaml         stdout: the document, written (or streamed) as the run goes
  stderr.log       diagnostics
  progress.jsonl   the full --log JSONL stream
  minima0.yaml …   --save minima
  meta.json        command, argv, pid, started_at, paths
  job.pid          pidfile used by job_stop
```

`job_status` is cheap and does not disturb the run. It reads the last progress
event (`cycle`, `best_merit`, `explored`, `timed_out`, …), reports the state as
`running`, `finished` or `failed`, and lists artefact sizes. A run counts as
`finished` when the process is gone and `out.yaml` is non-empty; `failed`
otherwise (read `stderr.log` or the document resource).

`job_result` ingests `out.yaml` into the document store and returns its summary
(best minimum, basins). It refuses while the job is running, because the
document is written at the very end.

`job_stop` implements the policy documented in
[escape.md § Interrupting the search](escape.md) and [rwrun.md](rwrun.md):

1. **SIGTERM #1** — the workers finish the running DLS (this can take minutes),
   then the run appends its completion block and exits 0.
2. **SIGTERM #2** (after `grace_seconds`, default 5) — that DLS aborts within one
   iteration; the best point so far is still recorded and saved, and the run
   still writes its document and exits 0.

A third signal would force-quit, and `kill -9` always loses the assembled
document, so neither is ever sent. `job_stop` waits up to `wait_seconds`
(default 60) and returns the status it reached; if the run is still finishing,
the save files and the streamed document survive either way.

Jobs are capped by `-max-jobs` across the workspace; the limiter counts live
pids, so a stale job never blocks a new one. Job tool results are served from
disk, so the server can be restarted between `escape` and `job_result`.

## Resources

| URI | Contents |
|---|---|
| `rayweave://documents/<id>` | full YAML document |
| `rayweave://documents` | index of this workspace's documents |
| `rayweave://docs` | index of the manuals |
| `rayweave://docs/<path>` | a manual, e.g. `rayweave://docs/optimize.md`, `rayweave://docs/methods/dls-optimization.md` |
| `rayweave://samples` | index of the demo inputs |
| `rayweave://samples/<name>` | a demo document (pass its text as `document`) |
| `rayweave://files/<path>` | a workspace file a tool wrote (plot, export) |
| `rayweave://jobs/<id>/progress` | an `escape`/`pso` JSONL progress stream |

Each manual file is registered as a static resource on startup (so it appears in
`resources/list`), and each directory also has a template for paths not known at
startup. Reading all manuals is expensive; the server instructions point at the
owning manual for a topic, and `validate`/`list` answer most questions without
reading any.

## Errors and cancellation

A non-zero exit is a tool error (`isError`), with the exit code and the last 20
stderr lines so the model can correct the call. Output that is empty, or a
`document_id` from another workspace, is an error with the same shape.

When a client cancels a synchronous call (or its timeout fires), the child is
stopped with SIGTERM, then SIGTERM again after 5 s, and waited for up to 60 s.
Nothing is ever SIGKILLed. If the child is still finishing after that, the tool
returns an explanatory error and the child keeps going: `optimize` writes its
document when the DLS returns, and the `escape`/`pso` job path is unaffected
because jobs are not tied to the request.

## Intentional limits

- `psf --yaml/--csv` side files are not exposed; the document carries
  `psf_results`. `psf --focus-plane all` is exposed as the `focus_plane`
  argument, but the shift it reports is stderr-only; use the `focus` tool for
  the full image-plane comparison.
- `plot` and `export` write only inside the workspace and return a resource URI
  (plus inline PNG for `plot`).
- `escape`/`pso` always run as jobs; there is no synchronous variant.
- No MCP sampling, elicitation or roots; the server is local stdio with no auth.
- One child process per call: a pipeline saves round trips, not CPU. Parallel
  tool calls run in parallel children, and `applyVariables` purity keeps each
  result deterministic.
- Platform: like `rwrun`, the server uses `setsid(2)` and therefore builds on
  macOS and Linux. The engine itself builds on Windows; a Windows MCP transport
  would need a job-control shim and is not implemented.

## Tests

```sh
go test ./internal/mcpsrv ./internal/runctl ./cmd/rwrun
```

The MCP tests build the real CLI once, connect an in-memory client to the server
and cover the tool schemas (embedded inputs flattened), `paraxial`, a
`chief → trace` pipeline, query/list, plot image content, `optimize` summaries,
PSF/wavefront compaction, the escape job lifecycle and the graceful stop. The
`rwrun` tests cover the shared `runctl` stop sequence.
