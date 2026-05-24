# Profiling

Three hidden persistent root flags capture CPU profiles, heap profiles, and execution traces. They're a dev/debug affordance — deliberately kept out of `bacio --help` — and are useful enough to be worth documenting.

## The flags

| Flag | What it captures | Open with |
|---|---|---|
| `bacio --cpuprofile <path>` | CPU profile covering the whole interactive session. | `go tool pprof <path>` |
| `bacio --memprofile <path>` | Heap profile of what survived the session (`runtime.GC()` then `pprof.WriteHeapProfile`). | `go tool pprof <path>` |
| `bacio --trace <path>` | Go execution trace covering the whole session. | `go tool trace <path>` |

Today the flags are wired for `bacio tui`. Extending them to the other long-running surfaces (`bacio api` / `web` / `channel` / desktop) is a possible follow-up — none are currently instrumented.

## Pick the right tool

- **CPU profile.** Best for "where is the CPU time going" — hot functions, expensive call sites, GC pressure.
- **Heap profile.** Best for "what's still alive after the session" — leaks, retained closures, oversized caches. Sampled allocation profile, not a live-heap snapshot.
- **Execution trace.** Best for **UI freezes**. Unlike the CPU profile, the trace captures off-CPU events (goroutine scheduling, blocking on syscalls, channel sends/receives, mutex contention), so a stall on a slow query or a `git` shell-out is visible in the trace but **invisible to CPU profiling** (the goroutine isn't running, so it consumes no samples). The trace also shows P/M/G activity over time so you can spot a goroutine that's monopolising the bubbletea event loop.

## How it's wired

[`internal/cli/profiling.go`](../internal/cli/profiling.go):

- `startProfiling` runs from `PersistentPreRunE`, before the command's `RunE` — for `bacio tui` that means recording is live before `tea.NewProgram(...).Run()` and covers the whole interactive session.
- `stopProfiling` is returned from `NewRoot()` and called by [`cmd/bacio/main.go`](../cmd/bacio/main.go) **unconditionally** after `Execute()` returns. That's deliberate: cobra skips `PersistentPostRunE` on error, but `main.go`'s deferred call still flushes the profile when a command exits via an error path. Safe to call when profiling was never started.

## Recipes

```bash
# Diagnose a TUI freeze. Execution trace is the right tool.
bacio --trace /tmp/bacio.trace tui
# (reproduce the freeze, then quit normally)
go tool trace /tmp/bacio.trace
# Browse 'Goroutine analysis' for the goroutines that ran the longest
# without yielding, and 'Network blocking profile' / 'Synchronization
# blocking profile' for off-CPU stalls.

# CPU hotspots in a long TUI session.
bacio --cpuprofile /tmp/bacio.cpu tui
go tool pprof -http=:0 /tmp/bacio.cpu

# Memory retained after a session.
bacio --memprofile /tmp/bacio.mem tui
go tool pprof -http=:0 /tmp/bacio.mem
```

## Why `--memprofile` writes at stop time

`stopProfiling` runs `runtime.GC()` then `pprof.WriteHeapProfile` so the heap profile reflects **survivors**, not in-flight allocations. If you want allocation-rate profiling, take a CPU profile and look at allocation samples there — bacio doesn't currently expose `--allocprofile` separately.
