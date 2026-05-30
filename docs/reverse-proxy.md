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
sites build the string through
`agentmode.LaunchCommand(endpoint, correlationKey)` so they can never
drift.

When the launch happens inside a worktree env, the one-liner also
injects `ANTHROPIC_CUSTOM_HEADERS='X-Bacio-Corr: <slug>'` — the
worktree slug, the BACI-305 correlation key the capture maps each
Anthropic request back to (see the Anthropic-capture section below).
Outside a worktree env the slug is empty and the header is omitted, so
the string stays byte-identical to the BACI-301 form.

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
shipped that screen — see the Monitor screen note below.

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
with a `[gzip body truncated — not decoded]` marker. SSE
(`text/event-stream`) streams aren't gzipped and are captured as before.

### Classification columns

Three columns on `proxy_requests` let BACI-306 select exactly the
parseable Anthropic captures without re-deriving them:

- `content_type` — the response `Content-Type`, base media type only
  (lowercased, params dropped).
- `is_stream` — the response was `text/event-stream` (an SSE turn).
- `is_anthropic` — the post-rewrite upstream host is `api.anthropic.com`
  and the path is under `/v1/` (`isAnthropicCapture`). Host+path only,
  no body inspection.

### Per-dispatch correlation (Mechanism B)

The agent launch one-liner stamps `X-Bacio-Corr: <worktree-slug>` on
every Anthropic request via `ANTHROPIC_CUSTOM_HEADERS`. The capture
transport lifts the header onto the observation (`CorrelationKey`),
**strips it from the upstream request** so it never reaches Anthropic,
and redacts it from the raw header block (`isSensitiveHeader`). The
recorder resolves it back to an attribution:

```
slug → LatestActiveSessionBySlug → ActiveDispatchForSession → session_id + dispatch_id
```

written onto the index row. The slug is the launch-time-stable key
(Claude Code mints the session id later, so it isn't known at launch);
the `session-start` hook stamps `agent_sessions.worktree_slug` from the
resolved wtenv so the lookup has something to match. `dispatch_id` is a
nullable INTEGER with **no FK** — like the audit log, a capture row is
cross-cutting and must survive a deleted dispatch.

**Caveats.** Attribution is *per-worktree*, best-effort: a worktree
interleaving a supervisor + subagent session attributes to the
worktree's currently-active dispatch — the header eliminates
cross-worktree confusion, which is the failure mode the ticket's
correlation caveat names. Per-request (session-id-level) precision is a
future refinement. And Mechanism B assumes Claude Code forwards
`ANTHROPIC_CUSTOM_HEADERS` onto its model-API requests; if it doesn't,
`session_id`/`dispatch_id` stay empty and the decode/classify half still
works (graceful degradation) — the columns are correlation-ready for a
later mechanism.

---

## Monitor web screen (BACI-304)

BACI-304 puts the BACI-303 per-FQDN rollup on a top-level **Monitor**
screen in the shared React tree, so the same surface renders on both
`bacio web` and the Wails desktop. It is the first read surface over the
capture in the UI — read-only stats only; deep per-request / per-message
inspection is a later issue.

- **Route + nav.** `/:prefix/monitor` (a `monitor` entry in the Topbar
  `NAV`, after History). The route lives under the BACI-285 `/:prefix`
  segment like every other page for nav uniformity, but the
  `proxy_requests` table is cross-cutting (no `repo_id`), so the data is
  **global** — the screen ignores the active repo for its fetch.
- **The dual-transport seam.** `listProxyStats(sinceDays)` is added to
  both `api.ts` (Wails: a new `MonitorService.ProxyStats` binding in
  [`desktop/monitorservice.go`](../desktop/monitorservice.go)) and
  `api.http.ts` (web: `GET /proxy/stats?since=Nd` + snake→camel reshape).
  The cross-transport `ProxyFQDNStat` type alias (BACI-108 pattern) lets
  `MonitorView` import one name regardless of build mode; the web-side
  shape + the formatters live in
  [`desktop/frontend/src/lib/proxyStats.ts`](../desktop/frontend/src/lib/proxyStats.ts).
- **The screen.**
  [`MonitorView.jsx`](../desktop/frontend/src/components/MonitorView.jsx)
  is a sortable table (Host / Reqs / In / Out / Err % / P50 / P95 / Last
  Seen), busiest-first by default (the order the store already returns),
  with a window selector and a silent 10s refresh while mounted.
- **Window selector.** Three buckets — **Last 24h** (`?since=1d`), **Last
  7d** (`7d`), **All-time** (the `0` = no-lower-bound sentinel). They map
  onto the endpoint's rolling-duration `since` lookback; a calendar-Today
  (local-midnight) bucket would need the BACI-312 cutoff the proxy
  endpoint doesn't have, so it's deferred.

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
- ~~**The Monitor web screen** (and the React `api.ts` seam method that
  consumes `GET /proxy/stats`)~~ — **shipped in BACI-304** (see the
  Monitor screen section above).
- **Raw-log-file retention / cleanup** — the index prune is BACI-302; the
  on-disk raw files have no auto-prune yet.
- **Retiring the `.jsonl` transcript attachments** — BACI-307.
