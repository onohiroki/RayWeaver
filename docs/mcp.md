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
  session-detached launch (`setsid` on Unix, a detached process group on
  Windows) and the two-stage stop (SIGTERM on Unix, a control file on Windows).
  See [rwrun.md](rwrun.md) and [escape.md](escape.md).

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

Every client launches the same local stdio server; only the configuration file
and its syntax differ. Build both binaries first:

```sh
go build -o rayweave ./cmd/rayweave/
go build -o rayweave-mcp ./cmd/rayweave-mcp/
```

The examples use `/path/to/rayweaver` for the checkout. **Use absolute paths**:
a client may start the server from a different working directory, and the
engine, the workspace and the manuals must be unambiguous.

| Setting | Value | Why |
|---|---|---|
| `RAYWEAVE` | `/path/to/rayweaver/rayweave` | the engine, not `rayweave-mcp`; optional when it sits next to the server |
| `RAYWEAVE_WORKSPACE` | `/path/to/rayweaver/.rw-mcp` | documents, jobs and plots; must be writable |
| `RAYWEAVE_ROOT` | `/path/to/rayweaver` | makes `rayweave://docs/...` and `rayweave://samples/...` resolve from any cwd |
| `RAYWEAVE_MCP_TOOLS` | `core` (default) or `all` | 15 tools, or all 27 |
| `RAYWEAVE_GLASS_DIR` | AGF directory | optional default glass catalogue |

The workspace defaults to `./.rw-mcp` when the variable is unset, which is only
safe when the client starts the server in the checkout. `.rw-mcp/` is ignored by
this repository's `.gitignore`. On Windows the binaries are `rayweave-mcp.exe`
and `rayweave.exe`; the rest of every entry is unchanged (see
[windows.md](windows.md)).

This repository ignores root-level `*.json`, so a local `opencode.json` or
`.mcp.json` stays untracked. The shared VS Code entry lives at
[`.vscode/mcp.json`](../.vscode/mcp.json) and uses `${workspaceFolder}`; add
`!/.mcp.json` to `.gitignore` only if you want to commit a portable
Claude Code/Copilot entry, whose absolute paths are machine-specific.

### OpenCode V2

The CLI writes a project-local entry (add `--global` to make it available in
every project):

```sh
opencode mcp add rayweave \
  --env RAYWEAVE=/path/to/rayweaver/rayweave \
  --env RAYWEAVE_WORKSPACE=/path/to/rayweaver/.rw-mcp \
  --env RAYWEAVE_ROOT=/path/to/rayweaver \
  -- /path/to/rayweaver/rayweave-mcp
opencode mcp list          # ✓ rayweave  connected
```

The equivalent `opencode.json(c)` entry:

```jsonc
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "servers": {
      "rayweave": {
        "type": "local",
        "command": ["/path/to/rayweaver/rayweave-mcp"],
        "cwd": "/path/to/rayweaver",
        "environment": {
          "RAYWEAVE": "/path/to/rayweaver/rayweave",
          "RAYWEAVE_WORKSPACE": "/path/to/rayweaver/.rw-mcp",
          "RAYWEAVE_ROOT": "/path/to/rayweaver"
        }
      }
    }
  }
}
```

`cwd` is optional (it defaults to the workspace, i.e. the project directory).
Code Mode is on by default, so the tools are grouped as
`tools.rayweave.<tool>`; `codemode: false` exposes them under their native
`rayweave_<tool>` names, and `disabled: true` keeps the entry without
connecting. OpenCode V2's default tool-execution timeout is 12 hours, so a
synchronous `optimize` or `psf` needs no special `timeout`; set
`mcp.timeout.execution` (milliseconds) only for a shorter bound.

### Claude Code

Project scope uses `.mcp.json` at the repository root (Claude Code asks for
approval the first time it loads it):

```json
{
  "mcpServers": {
    "rayweave": {
      "type": "stdio",
      "command": "/path/to/rayweaver/rayweave-mcp",
      "args": [],
      "env": {
        "RAYWEAVE": "/path/to/rayweaver/rayweave",
        "RAYWEAVE_WORKSPACE": "/path/to/rayweaver/.rw-mcp",
        "RAYWEAVE_ROOT": "/path/to/rayweaver"
      }
    }
  }
}
```

The CLI writes the same entry:

```sh
claude mcp add --scope project \
  --env RAYWEAVE=/path/to/rayweaver/rayweave \
  --env RAYWEAVE_WORKSPACE=/path/to/rayweaver/.rw-mcp \
  --env RAYWEAVE_ROOT=/path/to/rayweaver \
  --transport stdio rayweave -- /path/to/rayweaver/rayweave-mcp
```

`--scope project` shares the entry through `.mcp.json`; `--scope user` makes it
available in every project. The variadic `--env` list must be terminated by
another option (`--transport stdio` above) before the server name, otherwise
Claude Code reads the name as one more `KEY=value` pair. Check it with
`claude mcp list` or `/mcp`. Claude Code names the tools
`mcp__rayweave__<tool>`, for example
`mcp__rayweave__paraxial`; a per-server `timeout` field (milliseconds) is only
needed if you want a bound shorter than the default.

### GitHub Copilot

**VS Code** uses `.vscode/mcp.json` (the VS Code format, top-level `servers`),
which supports `cwd` and the `${workspaceFolder}` variable. This repository
ships one at [`.vscode/mcp.json`](../.vscode/mcp.json):

```json
{
  "servers": {
    "rayweave": {
      "type": "stdio",
      "command": "${workspaceFolder}/rayweave-mcp",
      "cwd": "${workspaceFolder}",
      "env": {
        "RAYWEAVE": "${workspaceFolder}/rayweave",
        "RAYWEAVE_WORKSPACE": "${workspaceFolder}/.rw-mcp",
        "RAYWEAVE_ROOT": "${workspaceFolder}"
      }
    }
  }
}
```

**GitHub Copilot CLI** and Copilot harness sessions use the *portable* format
(top-level `mcpServers`) from the workspace-root `.mcp.json`,
`.github/mcp.json`, or `$COPILOT_HOME/mcp-config.json`
(`~/.copilot/mcp-config.json` by default). A portable entry has no `cwd`, so
keep the absolute paths:

```json
{
  "mcpServers": {
    "rayweave": {
      "command": "/path/to/rayweaver/rayweave-mcp",
      "env": {
        "RAYWEAVE": "/path/to/rayweaver/rayweave",
        "RAYWEAVE_WORKSPACE": "/path/to/rayweaver/.rw-mcp",
        "RAYWEAVE_ROOT": "/path/to/rayweaver"
      }
    }
  }
}
```

The Copilot CLI can write its user entry:

```sh
copilot mcp add rayweave \
  --env RAYWEAVE=/path/to/rayweaver/rayweave \
  --env RAYWEAVE_WORKSPACE=/path/to/rayweaver/.rw-mcp \
  --env RAYWEAVE_ROOT=/path/to/rayweaver \
  -- /path/to/rayweaver/rayweave-mcp
```

VS Code does not hand `.vscode/mcp.json` to the Copilot harness directly; it
forwards eligible stdio servers, and this server needs no interactive input, so
it is forwarded. Verify with **MCP: List Servers** in VS Code or
`copilot mcp list` in the CLI.

### Claude Desktop

`claude_desktop_config.json` (macOS
`~/Library/Application Support/Claude/`, Windows `%APPDATA%\Claude\`, Linux
`~/.config/Claude/`) also uses the portable `mcpServers` object:

```json
{
  "mcpServers": {
    "rayweave": {
      "command": "/path/to/rayweaver/rayweave-mcp",
      "env": {
        "RAYWEAVE": "/path/to/rayweaver/rayweave",
        "RAYWEAVE_WORKSPACE": "/path/to/rayweaver/.rw-mcp",
        "RAYWEAVE_ROOT": "/path/to/rayweaver"
      }
    }
  }
}
```

Restart Claude Desktop after editing the file.

### Cursor

Cursor reads `~/.cursor/mcp.json` (global) or `.cursor/mcp.json` (project),
with the same `mcpServers` shape:

```json
{
  "mcpServers": {
    "rayweave": {
      "command": "/path/to/rayweaver/rayweave-mcp",
      "env": {
        "RAYWEAVE": "/path/to/rayweaver/rayweave",
        "RAYWEAVE_WORKSPACE": "/path/to/rayweaver/.rw-mcp",
        "RAYWEAVE_ROOT": "/path/to/rayweaver"
      }
    }
  }
}
```

Reload the window (or restart Cursor) after editing.

### Verifying the connection

| Client | Check |
|---|---|
| OpenCode | `opencode mcp list`; `/mcps`; `opencode service restart` if a config change is not picked up |
| Claude Code | `claude mcp list`; `claude mcp get rayweave`; `/mcp` |
| VS Code / Copilot | **MCP: List Servers**; **MCP: Reset Cached Tools**; `copilot mcp list` |
| Claude Desktop / Cursor | the client's MCP status panel; restart or reload after editing |

Then ask the agent to use the server, for example "run `paraxial` on
`samples/us2645157.yaml`". A connected server answers with a compact summary
and a `rayweave://documents/<id>` resource. If no rayweave tools appear:

- check that `RAYWEAVE` points at the engine and not at `rayweave-mcp`;
- make the workspace path absolute and writable;
- set `RAYWEAVE_ROOT` if the docs/samples resources are missing;
- restart the server from the client (OpenCode `/mcps`, VS Code **MCP: List
  Servers**, Claude Code `/mcp`);
- `RAYWEAVE_MCP_TOOLS=all` exposes the extended tools (27 instead of 15).

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
engine detached (its own session on Unix; a detached process group on Windows)
and returns a job id at once. The same isolation as `rwrun` means an MCP server restart, a client
timeout or an agent harness releasing the workspace does not kill the run (on
Windows the isolation is weaker — see the platform note under intentional
limits).

```
jobs/<job-id>/
  input.yaml       the document as given
  out.yaml         stdout: the document, written (or streamed) as the run goes
  stderr.log       diagnostics
  progress.jsonl   the full --log JSONL stream
  minima0.yaml …   --save minima
  meta.json        command, argv, pid, started_at, paths
  job.pid          pidfile used by job_stop
  stop             Windows control file for the staged stop
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

1. **Stage 1** — the workers finish the running DLS (this can take minutes),
   then the run appends its completion block and exits 0.
2. **Stage 2** (after `grace_seconds`, default 5) — that DLS aborts within one
   iteration; the best point so far is still recorded and saved, and the run
   still writes its document and exits 0.

On Unix the stages are SIGTERM twice; Windows has no SIGTERM, so the server
writes the numbers to `jobs/<id>/stop` and the child watches that file (the same
`RAYWEAVE_STOP_FILE` a detached `rwrun` uses). A third stage would force-quit,
and a force-kill always loses the assembled document, so neither is ever sent.
`job_stop` waits up to `wait_seconds` (default 60) and returns the status it
reached; if the run is still finishing, the save files and the streamed
document survive either way.

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
stopped with the platform's two-stage request: SIGTERM twice on Unix, the
control file on Windows (`optimize` watches it; it is passed in the child's
environment for that call). Nothing is ever force-killed on Unix. If the child
is still finishing after `CancelWait`, the tool returns an explanatory error
and the child keeps going: `optimize` writes its document when the DLS returns,
and the `escape`/`pso` job path is unaffected because jobs are not tied to the
request. On Windows a command that does not watch the control file (for
example `psf`) cannot be signalled, so a cancelled call is terminated instead;
there is no assembled document to lose in that case.

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
- Platform: the server runs on Windows as well as macOS/Linux. Unix detaches
  with `setsid(2)` and stops with SIGTERM; Windows detaches with
  `DETACHED_PROCESS` and stops through `RAYWEAVE_STOP_FILE`. Windows isolation
  is weaker (a closing Job Object or a parent-pid tree kill can still reach the
  child) and only `escape`, `pso` and `optimize` watch the stop file. See
  [windows.md](windows.md).

## Tests

```sh
go test ./internal/mcpsrv ./internal/runctl ./cmd/rwrun
GOOS=windows go build ./...     # the Windows compile gate; there is no CI
GOOS=windows go vet ./...
```

The MCP tests build the real CLI once, connect an in-memory client to the server
and cover the tool schemas (embedded inputs flattened), `paraxial`, a
`chief → trace` pipeline, query/list, plot image content, `optimize` summaries,
PSF/wavefront compaction, the escape job lifecycle and the graceful stop. One
test drives `escape` with `RAYWEAVE_STOP_FILE` directly, so the stop-file stage
that Windows uses is exercised on every platform. The `rwrun` tests cover the
shared `runctl` stop sequence (the Unix process-group property is Unix-only).
