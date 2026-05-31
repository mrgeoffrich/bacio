# Reverse proxy — routing agent Anthropic traffic through bacio

bacio runs a **reverse-proxy forwarding listener** on the same HTTP
server that serves the API and (in `bacio web` mode) the React bundle.
Every agent-mode Claude Code session points `ANTHROPIC_BASE_URL` at this
proxy, so all of its Anthropic API traffic flows through the local bacio
process before reaching `api.anthropic.com`.

This is the **foundation** of the `reverse-proxy-monitor` feature
(📡). BACI-301 ships only the transparent forwarding pipe plus the
launch-env wiring; monitoring, per-FQDN aggregation, the Monitor web
screen, and request/response capture are sibling tickets that hang off
this listener.

---

## Shape

- **Route:** `/anthropic/*` on the existing `bacio api` / `bacio web`
  server, reusing the wtenv-resolved `env.APIAddr` port — no new port,
  no new process. `POST /anthropic/v1/messages` forwards to
  `https://api.anthropic.com/v1/messages` (the `/anthropic` prefix is
  stripped before forwarding).
- **Always mounted:** the route is registered regardless of `MountUI`,
  so the agent gets the pipe whether the user launched `bacio api` or
  `bacio web`.
- **Auth-exempt:** `/anthropic/*` bypasses bacio's bearer-token check
  (the same early-return path as `/healthz`). Agent traffic carries its
  own Anthropic auth (`x-api-key` / `authorization`), not bacio's API
  token, so a configured `--token` must never block it.
- **Real TLS upstream:** the default `http.Transport` (system root CAs,
  TLS verification on) handles the HTTPS connection to Anthropic. The
  proxy rewrites the outbound `Host` header to `api.anthropic.com` so
  the upstream's TLS SNI and vhost routing see the right host, not the
  inbound `127.0.0.1:<port>`.
- **SSE-safe:** `FlushInterval=-1` flushes after every write, so the
  streaming token responses (`text/event-stream`) arrive at the agent
  incrementally rather than buffering to EOF.
- **No server write/read deadline on this route:** the API server sets a
  protective `WriteTimeout: 30s` / `ReadTimeout: 15s` for the JSON handlers,
  but those would be fatal here — the connection's write deadline is set when
  the request is read, so a slow or rate-limited Anthropic turn that crosses
  30s (time-to-first-byte *or* total stream) has its downstream relay fail on
  arrival and the agent gets a truncated SSE stream (no `message_stop`) and
  retries. `clearStreamDeadline` (in [`internal/api/middleware.go`](../internal/api/middleware.go))
  lifts the deadline to a generous bound for the `/anthropic/*` route only,
  reachable through the `requestLog` wrapper because `statusRecorder`
  implements `Unwrap()` for `http.ResponseController`.
- **Clean gateway errors:** an upstream dial/transport failure surfaces
  as a `502` (logged), not a panic.

The construction lives in [`internal/proxy`](../internal/proxy/proxy.go)
(`proxy.New(upstream, logger)`); the server wiring is in
[`internal/api/router.go`](../internal/api/router.go) (the
`mux.Handle("/anthropic/", …)` mount) and
[`internal/api/middleware.go`](../internal/api/middleware.go) (the
auth-exemption). The upstream is configurable via
`api.Options.ProxyUpstream` (empty selects `proxy.DefaultUpstream`); both
`bacio api` and `bacio web` leave it at the default — tests point it at a
fake upstream.

---

## Launch-env injection

The agent launch one-liner — surfaced by `bacio agent-run-command` and
the `bacio install-agent` activation banner — **always** injects the
proxy endpoint:

```bash
BACIO_AGENT_MODE=1 ANTHROPIC_BASE_URL=http://127.0.0.1:5320/anthropic ENABLE_TOOL_SEARCH=true claude --dangerously-skip-permissions --dangerously-load-development-channels server:bacio
```

The endpoint's host:port is the worktree's resolved `env.APIAddr`
(`agentmode.ProxyEndpoint`), so a sibling worktree with its own
allocated port gets a one-liner pointing at *its* server. Both call
sites build the string through `agentmode.LaunchCommand(endpoint)` so
they can never drift.

The one-liner injects **no** bacio correlation header. BACI-305
originally stamped `ANTHROPIC_CUSTOM_HEADERS='X-Bacio-Corr: <slug>'`;
BACI-316 retired that — the capture correlates off Claude Code's own
`X-Claude-Code-Session-Id` / `X-Claude-Code-Agent-Id` request headers
instead (see the Anthropic-capture section below), so nothing extra is
needed at launch.

### Consequence: a running bacio server is a hard dependency

Because `ANTHROPIC_BASE_URL` points at the local proxy, a `bacio web`
(or `bacio api`) **must be running on that port** for an agent session
to reach the Anthropic API. Without it the worker gets
connection-refused on its first request. This is intended per the
feature's scoping decisions; the activation banner calls it out.

---

---

## Traffic capture (BACI-302)

BACI-302 adds an **observation layer** over the forwarding pipe: every
request that flows through `/anthropic/*` is recorded — transport-level
fields only, no Anthropic body parsing (that is BACI-305/306). Two
artefacts land per request:

- a **raw req/resp file** under the per-worktree log dir
  (`<LogDir>/proxy/<YYYY-MM-DD>/<unix-nanos>.http`), holding the request
  header block + body, then the response header block + body;
- a **lightweight index row** in the `proxy_requests` SQLite table:
  method, host, path, status, bytes in/out, round-trip duration, the
  timestamps, and the raw-file path.

### Where the capture hooks in

The capture lives in a custom `http.RoundTripper`
([`captureTransport`](../internal/proxy/proxy.go)) installed on the
`httputil.ReverseProxy`. It tees the outbound request body and wraps the
response body in a counting + teeing `io.ReadCloser` whose `Close`
finalises the observation. This is the **load-bearing correctness
constraint**: capture must NEVER buffer or delay the streamed bytes — the
tee forwards every byte downstream immediately and accumulates the raw
copy alongside, so SSE token streams still arrive incrementally
(`FlushInterval=-1` is untouched). An upstream round-trip failure records
a status-0 observation so a failed request still shows up in the index;
the `ErrorHandler` surfaces the 502 to the client as before.

### Recorder seam — proxy stays store-free

The `proxy` package must not import `store` or know the log dir, so a
narrow [`Recorder`](../internal/proxy/recorder.go) interface inverts the
dependency: `proxy.New(upstream, logger, rec)` calls `rec.Record(obs)`
once per round-trip, and the `api` package supplies the concrete
[`captureRecorder`](../internal/api/proxy_recorder.go). A nil recorder is
replaced with an inert `nopRecorder`.

### Off the request path

`captureRecorder` is **fully asynchronous**: `Record` enqueues onto a
buffered channel (non-blocking — a full queue drops the observation with a
single warning rather than stalling the proxy) and a single worker
goroutine does the disk write + DB insert. So the proxy's streaming hot
path is never gated on a flush or a write. Failures are swallowed (logged
once, never panicked); a failed file write still inserts the index row
with an empty `raw_log_path`. Auth-bearing headers (`authorization`,
`x-api-key`, …) are redacted before the header block is written, so the
raw capture never persists an Anthropic API key. Raw bodies are buffered
up to `proxy.MaxCapturedBody` (8 MiB) per direction — past that the
streamed bytes are uncapped but the raw copy is truncated and the index
row's truncation is implicit in the `.http` file's marker.

### Retention

The `proxy_requests` index is pruned on every `store.Open` by
`pruneProxyRequests` against a 60-day `ProxyRequestRetention` window —
the same best-effort housekeeping pass that prunes the audit log. **Known
gap:** the raw capture files on disk have **no** auto-prune in BACI-302
(same as today's logs / transcripts); only the SQLite index is bounded.
Raw-log-file cleanup is a deliberate follow-on, not yet ticketed.

The write side is all BACI-302 adds. BACI-303 shipped the first read
surface over the capture: a per-FQDN aggregation (`Store.ProxyStatsByFQDN`)
exposed through both `GET /proxy/stats` (cross-cutting, sibling of
`/history`, behind the bearer-token auth — outside the `/anthropic/`
exemption) and the `bacio proxy stats` CLI verb. The rollup reports, per
upstream host: request count, bytes in/out, error rate (rows with
`status >= 400 || status == 0`), p50/p95 round-trip latency, and
first/last seen, busiest host first. The percentiles are computed in Go
off the windowed rows (no portable SQLite percentile under
`modernc.org/sqlite`); a `--since` lookback plus a row cap keep that pass
cheap. The returned `model.ProxyFQDNStat` DTO is snake_case-tagged so the
Monitor web screen (BACI-304) drops it straight onto the page. BACI-304
still owns the React `api.ts` / `api.http.ts` seam and the screen itself.

---

## Anthropic capture (BACI-305)

BACI-305 extends the BACI-302 capture so the on-disk raw file and the
`proxy_requests` index row are the **parseable substrate** the per-job
message parser (BACI-306) reads. It adds three things on top of the
transport-level observation — no Anthropic body/SSE parsing here, that
stays BACI-306.

### gzip decode on the captured copy

Anthropic non-stream replies (errors, `count_tokens`, non-streaming
`/v1/messages`) come back `Content-Encoding: gzip`: the Claude client
sets its own `Accept-Encoding`, the proxy forwards it verbatim, and
Go's transport only transparently decompresses the gzip *it* requested
— so the proxy tees the **compressed** wire bytes. The recorder
(`renderRawCapture` → `responseCaptureBody` / `gunzipCapture` in
[`internal/api/proxy_recorder.go`](../internal/api/proxy_recorder.go))
inflates the captured copy when `ResponseContentEncoding == "gzip"` and
the body wasn't truncated, so the `.http` file holds readable JSON. The
bytes forwarded downstream to the agent are **never** touched — only the
on-disk copy is decoded. A truncated gzip body (capped at
`proxy.MaxCapturedBody`) can't be inflated, so it's written verbatim
with a `[gzip body truncated — not decoded]` marker. The decode keys off
`Content-Encoding`, not the content type — and in practice the SSE
(`text/event-stream`) responses arrive gzipped too (the Claude client
advertises `Accept-Encoding: gzip`, which the proxy forwards verbatim),
so the same inflate path makes their on-disk capture readable.

### Classification columns

Three columns on `proxy_requests` let BACI-306 select exactly the
parseable Anthropic captures without re-deriving them:

- `content_type` — the response `Content-Type`, base media type only
  (lowercased, params dropped).
- `is_stream` — the response was `text/event-stream` (an SSE turn).
- `is_anthropic` — the post-rewrite upstream host is `api.anthropic.com`
  and the path is under `/v1/` (`isAnthropicCapture`). Host+path only,
  no body inspection.

### Per-dispatch correlation (Claude Code headers, BACI-316)

Claude Code stamps its own ids on every model-API request, so the
capture correlates off them directly — no bacio header, no worktree slug
(BACI-316 retired the BACI-305 `X-Bacio-Corr` mechanism):

- `X-Claude-Code-Session-Id` — the supervisor session id. Stored verbatim
  as `proxy_requests.session_id`; it maps 1:1 to
  `agent_sessions.session_id` (the same id the `session-start` hook keys
  on).
- `X-Claude-Code-Agent-Id` — the per-subagent id, present only on a
  Task-spawned subagent's requests. Stored as
  `proxy_requests.claude_agent_id`.

Both are Claude's own headers already bound for Anthropic, so the
capture transport reads but does **not** strip them (unlike
`X-Bacio-Corr`), and they stay visible in the raw `.http` block.

**Why the agent id matters.** A Task subagent shares the supervisor's
session id — there is exactly one `agent_sessions` row per `claude`
process (see [`agent-dispatch.md`](agent-dispatch.md), "Subagents share
the parent's session id"). So the session id alone can't say *which*
dispatch a request belongs to when one supervisor works several jobs;
the per-subagent agent id is the only discriminator.

**The binding.** `subagent_dispatches` maps
`NormalizeClaudeAgentID(agent_id) → dispatch_id` (one row per subagent;
`dispatch_id` carries **no FK** so a capture row survives a deleted
dispatch, like the audit log). It is written at the **start** of a
dispatched run: the `PreToolUse` hook fires on the worker's `bacio agent
claim <ISSUE>` with the subagent's `agent_id` in its payload — the id
lives **only** in the hook payload and the parent's `Task` result, never
an env var — resolves the dispatch by `(session, issue)`
(`resolveActiveDispatchID`), and records the binding. The recorder then
resolves a capture's `dispatch_id` by `X-Claude-Code-Agent-Id →
subagent_dispatches`, falling back to the session's active dispatch
(`ActiveDispatchForSession`) when there's no binding (an early request,
or a non-subagent call).

The `agent_id` ↔ `X-Claude-Code-Agent-Id` equality is funnelled through
the single `NormalizeClaudeAgentID` seam (lowercase, trim, strip an
`agent-` prefix) so the write and read sides can't drift; the exact
transform is to be pinned once verified on a live dispatched run.

**Graceful degradation.** A missing or unresolved header leaves the
correlation columns empty; the decode + classify half still works.

---

## What's deliberately out of scope

- **Forward proxy / `HTTPS_PROXY` all-FQDN MITM** — the other side of
  the reverse-vs-forward fork. Reserved for the aggregate-by-FQDN work
  (BACI-303/304) once multi-upstream routing earns its keep.
- **Per-FQDN aggregation / stats read surfaces** — shipped in BACI-303
  (`GET /proxy/stats` + `bacio proxy stats`); see the read-surface note
  above.
- **Anthropic request/response body parsing** (model, token usage,
  turn/tool counts) — BACI-305, BACI-306.
- **The Monitor web screen** (and the React `api.ts` seam method that
  consumes `GET /proxy/stats`) — BACI-304.
- **Raw-log-file retention / cleanup** — the index prune is BACI-302; the
  on-disk raw files have no auto-prune yet.
- **Retiring the `.jsonl` transcript attachments** — BACI-307.
