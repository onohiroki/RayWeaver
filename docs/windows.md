# Windows usage

RayWeaver is a pure-Go implementation with no cgo dependencies, so Windows is a
first-class platform: there are no Windows-specific code paths, and the only
differences from Linux/macOS are the build output name, the shell that drives
the stdin/stdout pipe, and the `samples/*.bash` demo scripts. There are no
prebuilt binaries; build from source with the Go toolchain.

## Requirements

- **Go 1.26.4 or newer** (see [Installing Go](#installing-go-on-windows)).
  Slightly older Go (1.21+) also works: when `go build` runs, the toolchain
  mechanism downloads 1.26.4 automatically (network required the first time).
- No other build tools; `gopkg.in/yaml.v3` and `golang.org/x/image` are fetched
  by `go build`.
- Git Bash / MSYS2 / WSL are **only** needed to run the `samples/*.bash` demo
  scripts; the core CLI runs fine from any shell.

## Installing Go on Windows

Pick one:

1. **MSI installer (recommended)** — download `go1.x.windows-amd64.msi` from
   <https://go.dev/dl/> and run it. It installs to `C:\Program Files\Go` and
   updates `PATH`.
2. **winget** — `winget install GoLang.Go`
3. **Chocolatey** — `choco install golang`

After installing, open a **new** terminal (so `PATH` is refreshed) and check:

```powershell
go version
```

## Installing PowerShell 7

Windows PowerShell 5.1 ships with Windows, but this guide recommends
**PowerShell 7.2 or newer** (`pwsh`), a separate cross-platform .NET
install:

1. **winget (recommended)** — `winget install Microsoft.PowerShell`
2. **MSI** — `PowerShell-7.x.x-win-x64.msi` from
   <https://github.com/PowerShell/PowerShell/releases>
3. **Chocolatey** — `choco install pwsh`
4. **Microsoft Store** — search for "PowerShell"

Launch it with `pwsh` (from cmd.exe, run `pwsh`). The built-in `powershell`
(5.1) stays available, but the differences below make 7.2+ the preferred shell
for RayWeaver pipelines.

### Differences from PowerShell 5.1

| Aspect | Windows PowerShell 5.1 (`powershell`) | PowerShell 7.2+ (`pwsh`) |
|---|---|---|
| Launch command | `powershell` | `pwsh` |
| `<` input redirection | Not supported ("The '<' operator is reserved for future use") | Supported since 7.2 |
| Encoding of text piped to a native process | `$OutputEncoding` defaults to **ASCII**; non-ASCII UTF-8 is corrupted | Defaults to **UTF-8** (no BOM) |
| `>` file redirection | Writes **UTF-16 LE** (breaks the UTF-8 YAML convention) | Writes **UTF-8** |
| `cat` | Alias for `Get-Content` | Same alias, same behavior |
| Release | Built into Windows (.NET Framework based) | Separate install, cross-platform (.NET 8+) |

`cat` is an alias for `Get-Content` in both shells. It works, but it streams the
file line by line and re-emits CRLF endings; `Get-Content -Raw` reads the file
as one string and preserves line endings, so prefer it for pipe input.

## Build

```sh
go build -o rayweaver.exe ./cmd/rayweaver/
```

The result is a native PE executable. For the bash demo scripts the binary is
looked up as `rayweaver` (no extension) — either build without the extension
(`go build -o rayweaver ./cmd/rayweaver/`; Windows runs an extensionless PE
fine) or point `RAYWEAVER` at the `.exe` explicitly.

## MCP server and `rwrun`

Both companion binaries build on Windows:

```sh
go build -o rayweave-mcp.exe ./cmd/rayweave-mcp/
go build -o rwrun.exe ./cmd/rwrun/
```

Point the client at the `.exe` (the server finds the engine as
`rayweave.exe` next to itself, or through `RAYWEAVE`):

```jsonc
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "servers": {
      "rayweave": {
        "type": "local",
        "command": ["C:\\tools\\rayweave-mcp.exe"],
        "cwd": "C:\\work\\rayweaver",
        "environment": {
          "RAYWEAVE": "C:\\tools\\rayweave.exe",
          "RAYWEAVE_WORKSPACE": "C:\\work\\rayweaver\\.rw-mcp",
          "RAYWEAVE_ROOT": "C:\\work\\rayweaver"
        }
      }
    }
  }
}
```

(or `opencode mcp add rayweave --env RAYWEAVE=... -- C:\tools\rayweave-mcp.exe`).
The Claude Code, VS Code/Copilot, Claude Desktop and Cursor examples in
[mcp.md](mcp.md#client-configuration) apply unchanged: use absolute Windows
paths ending in `.exe` and, where the client supports `cwd`, the checkout
directory.

The pipeline tools, summaries, documents and resources are the same as on Unix.
The differences are in process control and long runs:

- **No `setsid`.** A detached run is created with `DETACHED_PROCESS` and its own
  process group, so it survives the MCP server restarting and is out of reach of
  a Ctrl+C sent to the launcher's console. It is **not** fully equivalent to a
  Unix session: if an agent harness puts the server in a Job Object whose
  close kills its members, the run dies with it, and a tree killer that walks
  parent pids can still find it (Windows does not reparent to init).
- **No SIGTERM.** `job_stop` and `rwrun -stop` write the stages to a control
  file (`RAYWEAVE_STOP_FILE`) instead of sending a signal: `1` lets the running
  DLS finish, `2` aborts it within one iteration. Both stages still write the
  document in `escape`/`pso`; the rule "never force-kill a long run" is
  unchanged. The file is `<pidfile>.stop` for `rwrun` and `jobs/<id>/stop` for
  an MCP job (see [mcp.md](mcp.md) and [rwrun.md](rwrun.md)).
- **Only the commands that watch the file can be stopped gracefully.**
  `escape`, `pso` and `optimize` do; a synchronous `psf`/`focus` cancellation has
  no document to save and is terminated.
- **No `ps`.** `rwrun -status` reports the elapsed time from the process creation
  time (`GetProcessTimes`) rather than `ps`'s `etime` format; the command name
  comes from `QueryFullProcessImageName`.
- **A stale pid is guarded by the image name**, case-insensitively. A process
  the current user cannot open is treated as alive (the Unix `EPERM` case).

The CI-less validation for the Windows side is a cross-build:

```sh
GOOS=windows go build ./...
GOOS=windows go vet ./...
```

The end-to-end tests run on the host OS; the stop-file path itself is
platform-neutral and is exercised on Unix by
`internal/mcpsrv/TestStopFileStopsEscapeGracefully`.

## Running a pipeline

From a **PowerShell 7.2+** prompt, the standard redirect and pipe work:

```powershell
.\rayweaver.exe trace < samples\us2645157.yaml

.\rayweaver.exe chief < samples\us2645157.yaml | .\rayweaver.exe paraxial
```

On **PowerShell 5.1** there is no `<`, and the default output encoding is ASCII,
so use the pipeline form and set the encoding when the YAML contains non-ASCII
text (glass names, `notes`, ...):

```powershell
$OutputEncoding = [System.Text.Encoding]::UTF8
Get-Content -Raw samples\us2645157.yaml | .\rayweaver.exe trace
```

Capturing stdout to a file works with plain `>` on 7.2+. On 5.1, `>` writes
UTF-16 LE, which the YAML parser rejects when the file is later re-used as
input; use `Set-Content -Encoding utf8` instead (or `Out-File -Encoding utf8`).

**cmd.exe** remains a fallback: `<` is a byte-exact redirect, so UTF-8 passes
through untouched and output files keep the UTF-8 convention:

```
rayweaver.exe trace < samples\us2645157.yaml
```

## Sample demos are bash scripts

The `samples/*.bash` demo scripts are bash-dependent (`set -euo pipefail`,
`${BASH_SOURCE[0]}`, arrays, `[[ ]]`) and cannot run directly from cmd.exe or
PowerShell. Three ways to run them:

1. **Git Bash** (ships with Git for Windows) or **MSYS2**:
   `bash samples/run-demo.bash`. The scripts resolve their own directory and
   locate the binary as `rayweaver` (no extension); build extensionless or set
   `RAYWEAVER`:
   ```bash
   RAYWEAVER="C:\path\to\rayweaver.exe" bash samples/run-demo.bash
   ```
2. **WSL** — the scripts are Linux-native. Build the Linux binary and keep the
   input data inside the WSL filesystem:
   `GOOS=linux go build -o rayweaver ./cmd/rayweaver/`, then
   `bash samples/run-demo.bash`.
3. **Manual commands** — run the underlying `rayweaver` commands by hand from
   the root [README](../README.md#pipeline-examples) ("Pipeline examples"),
   which is exactly what the scripts do internally.

`gnuplot` is only needed for the optional PNG renderings (the demos skip them
with a message when it is absent); `yq` is used only by `asphere-demo.bash --epd`.

## Encoding

Input YAML must be UTF-8 (repo convention: no BOM). In practice the parser
accepts a UTF-8 BOM and CRLF line endings, so files produced by Windows
editors work as-is. The importers additionally strip UTF-16 LE/BE BOMs, which
Windows editors sometimes save ZMX / SEQ / LEN input files in.

## Paths

Flags that take a file path (`--glass-dir`, `--log`, `--yaml`, `--csv`,
`--save`) accept both forward and back slashes on Windows.

## Interrupts and parallelism

- Ctrl+C maps to `os.Interrupt` on Windows. `optimize`'s two-stage and
  `escape`'s three-stage graceful stops behave exactly as on Unix: the first
  (and for `escape`, the second) Ctrl+C waits for a cycle boundary or
  interrupts the running solve and still writes results with
  `interrupted: true`; a final Ctrl+C force-quits. `SIGTERM` is not a console
  concept on Windows, so the staged behavior is driven entirely by repeated
  Ctrl+C.
- A **detached** run has no console to receive Ctrl+C. `rwrun` and the MCP
  server therefore use the stop file described above
  (`RAYWEAVE_STOP_FILE`); it drives the same stages. The file can also be driven
  by hand, e.g. `Add-Content -Value 1 C:\runs\v42.pid.stop`.
- Parallel computation (pupil grids, DLS Jacobians, escape workers via
  `GOMAXPROCS`) works unchanged.

## Linux and macOS

No special handling is needed on Linux or macOS: the build command, the
`docs/` manuals, and the `samples/*.bash` demos all work as documented. This
page exists only because the Windows-specific concerns (shell, encoding, bash
demos) need explaining.