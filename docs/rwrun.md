# `rwrun` — keep a long run alive when an AI agent launches it

`rwrun` is a small companion binary (`cmd/rwrun/`, no dependencies beyond the Go
standard library) that starts a rayweave subcommand **detached from the caller's
process group** and returns immediately. It exists because a long optimisation
started by an AI coding agent (OpenCode, an editor agent, any harness that owns
the shell) is routinely killed long before it finishes.

```
go run ./cmd/rwrun -n v42 -i v42-escape-input.yaml -o v42-result.yaml \
    -e v42-stderr.jsonl -- escape --verbose --keep-infeasible \
    --log v42-progress.jsonl --save v42-min
```

## The problem it solves

An agent harness runs commands in a shell it owns. When it releases the
workspace — on an idle period, a session recycle, or a manual stop — it deletes
the background task it registered and signals the process group, so **every
process in that group receives SIGTERM**. A rayweave run started there dies
mid-cycle.

This is not hypothetical. From the OpenCode server log (`opencode.log`, UTC):

```
2026-09-28T05:00:15.626Z  message="location services evicted" directory=…/rayweaver-refactor
2026-09-28T05:00:15.641Z  message="watcher stopped" path=…
2026-09-28T05:00:15.811Z  GET /api/shell/sh_…/output -> 404
```

and the run's own log, in the same second:

```
{"time":"2026-09-28T14:00:15…","event":"interrupt","signal":"terminated"}
```

The same signature killed a 6-element escape 1 h 05 m into its 8 h budget
(`2026-09-27T13:49:11.873Z` versus the run's `interrupt` 19 ms later). The
eviction is a routine server-side event — 157 occurrences in that log, at
intervals from 27 minutes to 8.5 hours, including while the agent was actively
working. **No fixed budget survives it**: an 8-hour run is structurally
impossible from inside a harness-owned shell.

The cost of being killed is not only the wasted time. `escape` assembles its
report document and writes it to stdout **once, after every cycle has finished**.
A process killed during the graceful-stop window leaves stdout empty, so the
merit-ranked minimum list and the `escape_result` summary are gone. The `--save`
minima survive (atomic writes), and `rayweave list escape < run.jsonl`
reconstructs the aggregate from the log, so no computation is lost — but the
convenient artifact is.

## How it works

`rwrun` starts the child with `syscall.SysProcAttr{Setsid: true}` and does not
wait for it. `setsid(2)` creates a new session and a new process group; when
`rwrun` returns, the child is reparented to init. The child is therefore **neither
in the caller's process group nor in its process tree**, so a group teardown —
and a killer that walks descendants — cannot reach it.

```
rwrun ─fork─▶ child: setsid(2) ──▶ exec rayweave …
  │                                     ppid = 1
  └─ writes <name>.pid, prints the summary, exits in ~0.25 s
```

There is no plist, no launchd job, no `setsid(1)` (macOS does not ship it) and no
`fork`/`exec` plumbing: `SysProcAttr.Setsid` is implemented for darwin and linux
in the standard library. Do **not** add `SysProcAttr.Noctty` — on darwin it
issues `ioctl(0, TIOCNOTTY)`, which fails with `ENOTTY` whenever fd 0 is not a
terminal (a pipe, `/dev/null`, a script), i.e. exactly how an agent launches a
job. It is also redundant: a session leader has no controlling terminal anyway.

## Options

```
rwrun [options] -- [rayweave] SUBCOMMAND [ARGS...]

  -i, -input FILE    YAML piped to the command's stdin (default: /dev/null)
  -o, -output FILE   where the command's stdout goes (default: <name>.out)
  -e, -log FILE      where the command's stderr goes (default: <name>.stderr.log)
  -n, -name NAME     artefact base name (default: the subcommand)
  -d, -dir DIR       working directory (default: the input's directory)
      -rayweave PATH rayweave binary (default: $RAYWEAVE, ./rayweave, $PATH, …)
      -pidfile FILE  detached pid record (default: <name>.pid)
      -stop-grace N  seconds between the two SIGTERMs (default 5)
      -stop-wait N   seconds to wait for the exit (default 60)
      -fg           run in the foreground (no detach)
      -status       report whether the run is alive
      -stop         graceful stop: SIGTERM twice, never SIGKILL
      -purge        remove the pidfile
```

Everything after `--` is handed to rayweave unchanged. When the first argument
is an existing executable it is used as the command as-is
(`-- ./rayweave escape …`); otherwise the rayweave binary is prepended, so
`-- escape …` works. The short spellings (`-i`, `-o`, `-e`, `-n`, `-d`) are
rewritten to their long form **only before `--`**, so a rayweave flag that
happens to share a letter (`plot -o FILE`) is never touched.

`-dir` sets the child's working directory and is what decides where rayweave's
own relative outputs land (`--csv a.csv`, `--yaml b.yaml`, `plot -o`). The
`-i/-o/-e` paths are resolved against the *caller's* directory.

stdout and stderr are always kept in separate files — stdout carries the
pipeline document, stderr the diagnostics and the JSONL progress stream. Never
merge them (see AGENTS.md).

### Which rayweave gets run

Resolution order: `-rayweave`, then `$RAYWEAVE`, then `./rayweave`, then `$PATH`,
and only then the directory holding `rwrun`. That last candidate is a last
resort on purpose: when `rwrun` is built somewhere unrelated (a scratch `/tmp`,
say) that directory can hold a completely unrelated and badly outdated
`rayweave`, which is exactly what happened during development. **The resolved
path is always printed in the launch summary — check it**, and pass `-rayweave`
when in doubt.

## Stopping a run

```
go run ./cmd/rwrun -stop -n v42
```

`escape` stops in three escalating stages (see AGENTS.md), and the first stage is
slow: it cancels the context and the workers finish the running DLS solve, which
can take minutes on a large system. The stdout document is written only after
`ParallelEscape` returns. So:

- **signal #1** — graceful. Let it take its time; the document will be written.
- **signal #2** — the running DLS aborts within one iteration, its best point so
  far is recorded, and the run **still writes the document and exits 0**.
- **signal #3** — force quit (`exit 1`). Never send it.

`rwrun -stop` sends #1, waits `-stop-grace` (5 s), then #2 only if the process is
still alive, and reports whether it exited. A `kill -9` (SIGKILL) cannot be
caught and therefore always loses the assembled document.

`rwrun -stop` refuses to signal a pid whose command is not a rayweave run, so a
recycled pid left behind in a stale pidfile can never be hit; `-purge` clears the
file.

## Reporting on a run

```
go run ./cmd/rwrun -status -n v42      # pid, elapsed, command, artefact sizes
rayweave list escape < v42-progress.jsonl   # minima (feasible only), workers, log events
rayweave list escape < v42-result.yaml     # the same, from the assembled document
```

`escape_result` in the final document lists the feasible minima with their `--save`
file names, and the infeasible basins separately (with `--keep-infeasible`).
`list escape` also reads the JSONL run log directly, which is the way to inspect
a run that was interrupted before the document was written.

Because the child is reparented to init, nothing reaps it: a crash leaves no
`launchd` exit code, so liveness is `kill -0` on the pidfile plus the logs. That
is the one capability given up relative to running under `launchd`; in exchange
there are no plists, no job registry and nothing to purge.

## What it is not

- It is **not** a rayweave subcommand. Process lifetime is not a pipeline
  document concern, and putting it inside `escape` would force the tool to
  re-exec rayweave and materialise stdin (every subcommand reads YAML from
  stdin), plus duplicate itself in `pso`, which runs the identical
  `runEscapeCore` path.
- It does **not** chain subcommands. One command per launch; for a pipeline, run
  the stages as separate launches (the second reading the first's output file).
- It does **not** resume an interrupted run. The escape store lives in the
  process; a restart re-explores. The `--save` minima are the durable record.

## Measured behaviour

On the 6-element double-Gauss escape (8 workers), stopped while cycle 1 was still
running:

```
launch   0.25 s to return
stop     6.0 s total:  interrupt(200.3 s) → interrupt_dls(205.3 s) → done(205.8 s)
result   103,371 bytes, 8 minima, interrupted: true, best_merit 124.78
```

The same run launched from a harness-owned shell and evicted at 1 h 11 m produced
a 0-byte result file.
