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
  server, reusing the wtenv-resolved `env.APIAddr` port. `POST
  /anthropic/v1/messages` forwards to
  `https://api.anthropic.com/v1/messages` (the `/anthropic` prefix is
  stripped before forwarding). BACI-344 additionally serves this route on
  a **stable proxy port** (the API port − 1) — via a standalone
  `bacio proxy serve` and/or the all-in-one's second listener — so the UI
  / binary can be upgraded without dropping the pipe; see "Standalone
  proxy listener" below.
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
BACIO_AGENT_MODE=1 ANTHROPIC_BASE_URL=http://127.0.0.1:5319/anthropic ENABLE_TOOL_SEARCH=true claude --dangerously-skip-permissions --dangerously-load-development-channels server:bacio
```

The endpoint's host:port is the worktree's resolved **proxy** addr —
`env.ProxyAddr` (BACI-344), the API port minus one — via
`agentmode.ProxyEndpoint`, so a sibling worktree with its own allocated
port gets a one-liner pointing at *its* proxy. (Pre-BACI-344 this was
`env.APIAddr`/`5320`; the split moved the launch endpoint onto the stable
proxy port — see "Standalone proxy listener" below.) Both call sites build
the string through `agentmode.LaunchCommand(endpoint)` so they can never
drift.

The one-liner injects **no** bacio correlation header. BACI-305
originally stamped `ANTHROPIC_CUSTOM_HEADERS='X-Bacio-Corr: <slug>'`;
BACI-316 retired that — the capture correlates off Claude Code's own
`X-Claude-Code-Session-Id` / `X-Claude-Code-Agent-Id` request headers
instead (see the Anthropic-capture section below), so nothing extra is
needed at launch.

### Consequence: a running bacio server is a hard dependency

Because `ANTHROPIC_BASE_URL` points at the local proxy, **something must
be listening on the proxy port** for an agent session to reach the
Anthropic API — either a standalone `bacio proxy serve` or the second
`/anthropic`-only listener a default `bacio web` / `bacio api` stands up
there (see below). Without it the worker gets connection-refused on its
first request. This is intended per the feature's scoping decisions; the
activation banner calls it out.

---

## Standalone proxy listener (BACI-344)

`bacio web` does double duty: it serves the disposable UI + REST API **and**
hosts the liveness-critical reverse proxy. Restarting it to ship a new binary
drops the proxy listener — in-flight SSE turns truncate (no `message_stop` →
the agent retries) and the next request gets connection-refused. BACI-344
**splits the proxy onto its own stable listener** so the UI / binary / schema
can be upgraded without interrupting agents (Option A from the ticket).

### Port model

The historical port (`5320`, or a worktree's allocated port) stays the
**UI/API** port — no re-bookmarking, no `BACIO_REMOTE` churn. The standalone
proxy listens on a **derived proxy port = API port − 1** (`5319` in the
default; `<allocated> − 1` under a worktree manifest). The derivation lives in
[`internal/wtenv`](../internal/wtenv/wtenv.go) (`proxyAddrFor`, surfaced as
`Resolved.ProxyAddr`); an unparseable addr or a port ≤ 1 falls back to
`DefaultProxyAddr` (`127.0.0.1:5319`). `bacio status` reports both the API and
Proxy addrs.

Because the proxy port is derived, not separately allocated, the worktree
port allocator reserves both halves of the pair: `AllocatePort`
([`internal/wtenv/registry.go`](../internal/wtenv/registry.go)) treats each
worktree as occupying `{api_port − 1, api_port}` and probes both, so a fresh
`init` never lands its API port on a sibling's proxy port (or vice versa).
That's why auto-allocated API ports step by 2 and the lowest is `5322` — `5321`
would put its proxy on the reserved default `5320`.

`agentmode.ProxyEndpoint` keys off `env.ProxyAddr`, so the launch one-liner
(and the install-agent banner) always point new agents at the proxy port.

### `bacio proxy serve`

A deliberately tiny, long-lived process that hosts **only** `/anthropic/*`
(+ the unauthenticated `/healthz` + `/version` reads). No UI, no REST API, no
controller, no leader election. It reuses the **identical** proxy handler the
full router builds — `buildProxyHandler` in
[`internal/api/proxy_server.go`](../internal/api/proxy_server.go) is the shared
seam, so the standalone surface (`newProxyOnlyHandler` → `api.NewProxyServer`)
and `newRouter` can't drift. It binds `env.ProxyAddr` by default
(`--addr` / `--port` override). This is the process agents pin; it changes
rarely, so you essentially never restart it — meaning `bacio web` becomes
freely restartable.

### Second listener (the all-in-one default)

A user may not have started `bacio proxy serve`, so the all-in-one `bacio web`
/ `bacio api` **also** opens a second `/anthropic`-only listener on the proxy
port (`startSecondaryProxy` in [`internal/cli/web.go`](../internal/cli/web.go)),
so a fresh agent works out of the box. The bind is **non-fatal**: if it clashes
(a dedicated `bacio proxy serve` already owns the port) the process logs one
info line and carries on. `bacio web --no-proxy` / `bacio api --no-proxy`
skips the second listener — use it once you've adopted the dedicated proxy so
the two don't clash. The main listener keeps mounting `/anthropic` on the
API port too (unchanged), so the all-in-one mode stays the default.

> **Why the second listener isn't optional.** The launch endpoint now points
> new agents at the proxy port. A user mid-migration who relaunches an agent
> against an old all-in-one `bacio web` (serving `/anthropic` only on the API
> port, not the proxy port) would get connection-refused — *unless* the second
> listener is up. That's why a default `bacio web` answers on both.

### Why a version-behind proxy is safe

Capture is best-effort and decoupled (the recorder swallows write failures and
the leader-gated sweep backfills missed captures from the on-disk `.http`
files — see "Backfill sweep" below). So a `bacio proxy serve` running a version
behind across a schema migration degrades to "some captures missed for that
window, backfilled later" — **forwarding never breaks**. The proxy only needs
restarting when its own store touchpoints (`proxy_requests` / `proxy_messages`)
change; `bacio status` flags when a running proxy reports a different
`/version` than the current binary.

### Keep-alive

`bacio proxy install-service` writes + loads a per-user OS-supervisor unit that
runs `bacio proxy serve`: a launchd agent
(`~/Library/LaunchAgents/io.bacio.proxy.plist`) on macOS, a systemd user unit
(`~/.config/systemd/user/bacio-proxy.service`) on Linux. `--dry-run` prints the
rendered unit + target path without writing. Windows errors clearly
(`bacio proxy serve` runs there fine — only the install automation is
macOS/Linux). The spawn-detached-child alternative (ticket option 1b) was
rejected for its version-skew + reaping complexity.

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

## Per-job message detail (BACI-306)

BACI-306 is the **parsing layer** on top of the BACI-305 substrate. It
turns the captured `/v1/messages` SSE turns into a structured, durable
per-job message transcript that BACI-308 (Monitor inspection drill-down)
and the re-platformed review skill (BACI-309) consume — the proxy
capture, not a `.jsonl` doc, is now the canonical transcript. BACI-307
removed the old `.jsonl`-attachment path (`attach_transcript` + the
per-issue transcript docs) on the strength of this source.

### The pure parser — `internal/anthropic`

[`internal/anthropic`](../internal/anthropic/parse.go) is a pure package
(imports only `internal/model` + the stdlib) so the recorder (write), the
store read API, the CLI, and the tests share one parser:

- `ParseCapture(raw)` is the inverse of `renderRawCapture` — it splits the
  `==== REQUEST ==== / ==== RESPONSE ====` layout, reads the request JSON
  (model, system, `messages[]`, whether `output_config` carries a
  structured-output `format` — **never** `metadata.user_id`, which carries
  `device_id`/`account_uuid` PII), and
  decodes the response SSE into one assistant turn: ordered text /
  thinking / tool_use blocks (the `input_json_delta` fragments stitched),
  merged usage (input/cache off `message_start`, output + thinking off
  `message_delta`), and the stop reason. The `data:` JSON carries trailing
  whitespace inside the object, so each event is decoded leniently — a
  malformed event is skipped, never sinks the turn.
  - The request/response boundary is matched **line-anchored** — on the
    full line `\n==== RESPONSE ====\n`, not as a bare substring (BACI-323).
    The request body carries the whole conversation-so-far, which in this
    repo frequently *contains* the literal `==== RESPONSE ====` text (agents
    read the proxy capture / `parse.go` / docs into context). A bare cut
    fires on that in-body occurrence and truncates the request JSON
    mid-object, so `json.Unmarshal` returns `unexpected end of JSON input`.
    The recorder always writes the real boundary on its own CRLF line
    (→ `\n==== RESPONSE ====\n` once LF-normalised), so the anchor matches
    exactly the recorder's boundary while an in-body occurrence — which sits
    inside a single-line JSON body, never preceded by a newline — can't. A
    bare-substring cut is kept as a fallback for marker-less inputs.
  - A request message's `content` is accepted in **both** forms the
    `/v1/messages` API allows (BACI-324): the block-list form
    (`"content":[{...}]`) and the string shorthand (`"content":"text"`, which
    the Claude client emits for plain user turns). `AnthropicMessage.Unmarshal-
    JSON` normalises a string to a single text block, so every consumer
    (Classify, the persisted `delta_json`, the React viewer) sees the uniform
    block-list shape. Without this the parser failed `cannot unmarshal string
    into … content` on the bulk of real captures — co-occurring with the
    marker-collision split bug above, so both fixes are needed to recover the
    corpus.
- `Classify(pc, prev)` decides whether a capture extends the job's
  **primary thread** (same `(model, system-fingerprint)`, growing message
  count, no structured-output probe) or is **auxiliary** (a title-gen /
  structured-output probe, a different model). The probe test keys off a
  structured-output `format` (json_schema) child of `output_config`, **not**
  the bare presence of `output_config` (BACI-325): the Opus 4.x API overloads
  `output_config` to also carry a reasoning-`effort` hint
  (`{"effort":"xhigh"}`) on essentially every normal turn, so the old
  bare-presence heuristic filed every real turn auxiliary → zero primary turns,
  a blank model, and an empty transcript for all current-model traffic. The
  `format`-keyed test (`hasStructuredFormat` in `parse.go`) treats an
  `effort`-only config as a normal turn. The system-fingerprint half of the
  primary test (`systemFingerprint` in `assemble.go`) strips the volatile
  leading `x-anthropic-billing-header` system block — whose `cch` cache token
  drifts on essentially every request — before hashing, so consecutive turns of
  one conversation hash identically (BACI-326); without it every turn after the
  first drifted to a fresh fingerprint → auxiliary → `turn_count` capped at 1.
  It returns the
  request-message **delta** — the messages appended since the prior primary
  capture, with the echoed prior-assistant turn dropped (it's already
  represented by that capture's reconstructed turn). Storing the delta, not
  the whole growing request body, keeps per-job storage O(n).
- `AssembleTranscript` concatenates the ordered primary deltas + turns into
  one `model.AnthropicTranscript`, sums usage across the job, and keeps the
  auxiliary turns separate.

The decoder doc (`proxy-capture-decoder.md`, validated against 220 real
SSE turns) is the executable spec for the parse rules.

### Where it hooks in — the recorder, off the request path

The recorder's async `persist` worker (the same one that writes the raw
file + index row) parses a capture when it's `is_anthropic && is_stream &&
!ResponseTruncated` — the selection predicate. A **truncated** response
can't be parsed (every SSE response is gzipped, so a truncated body is an
incomplete, un-inflatable gzip stream written verbatim with a marker), and
the non-stream `count_tokens` / error JSON shapes aren't message
transcripts. The parse reuses the same rendered (inflated, redacted)
`.http` bytes written to disk, classifies against the job's last thread
state (`LatestThreadState`), and inserts a `proxy_messages` row. Best-effort
like the rest of capture: a parse failure or store hiccup is logged and
swallowed, never breaks the agent's traffic.

### Durable storage — `proxy_messages`

One row per parseable capture (`internal/store/proxy_messages.go`),
cross-cutting like `proxy_requests`: `dispatch_id` is the per-job grouping
key (nullable, no FK — a row survives a deleted dispatch); `is_primary` is
the re-derivable classification flag; `delta_json` / `turn_json` hold the
parsed shape (capped at `model.MaxProxyMessageBody` per direction, replaced
with a marker past the cap — the raw `.http` stays ground truth). The
`usage_*` columns let a job-level sum run without re-parsing. Growth is
bounded by `pruneProxyMessages` on the same 60-day window as the index.

### Read surfaces

- `bacio proxy capture <id>` / `GET /proxy/captures/{id}` — the parsed
  detail of one captured turn, keyed on the `proxy_requests` id.
- `bacio proxy job <dispatch_id>` / `GET /proxy/jobs/{dispatch_id}/transcript`
  — a dispatch's assembled ordered transcript. Both sit behind the
  bearer-token auth like `/proxy/stats` (UI/CLI reads, not agent
  passthrough), and the `model` types are snake_case-aligned to the React
  viewer's `transcript/types.ts` so BACI-307/308 can re-point
  `TranscriptView` at this source with a thin seam.
- `bacio proxy captures --host <h> [--dispatch <id>] [--anthropic] [--since <d>]`
  / `GET /proxy/captures?host=&dispatch_id=&is_anthropic=&since=&limit=`
  (BACI-308) — the filtered, newest-first, capped capture **list** the
  Monitor drill-down walks an FQDN stat row down into. Backed by
  `Store.ListProxyCapturesEnriched`, each row is best-effort enriched with
  its dispatch's issue key + mode (one cached `GetDispatch` per dispatch).
  Default cap 200, ceilinged at the 500 `defaultProxyRequestLimit`.
- `bacio proxy raw <id>` / `GET /proxy/captures/{id}/raw` (BACI-308) — the
  inflated, auth-redacted `.http` bytes the recorder wrote to disk, served
  as `text/plain`. The escape hatch for a capture that isn't a parseable
  Anthropic turn. 404 (not 500) when the row has no raw file or it's been
  pruned, so the UI treats "raw unavailable" as a clean miss.
- `bacio proxy grep <text>` (alias `search`) / `GET /proxy/search?q=&role=&block=&dispatch_id=&session=&agent=&since=&from=&limit=`
  (BACI-320) — the *content* filter complementing the BACI-308 *index* filter:
  a case-insensitive substring search over the parsed message bodies
  (`Store.SearchProxyMessages`). The SQL `LIKE` over `delta_json` / `turn_json`
  finds candidate rows (with an `ESCAPE '\'` clause so a literal `%` in the
  needle matches literally), then each matched row's JSON is re-parsed in Go and
  its blocks walked, so the result is a clean per-block **snippet** (real block
  text, never a raw JSON field name) rather than a raw-bytes hit. One match line
  per matching block, each carrying the `proxy_requests` id so a reader can drill
  into `proxy capture <id>` / `proxy raw <id>` — closing the search→drill-in
  loop. `--role assistant|user` narrows which column is scanned, `--block` to one
  block type, and the dispatch/session/agent/since filters mirror `captures`;
  `--limit` caps the match lines (default 200, ceilinged at 500). `LIKE` over a
  60-day single-dev table is sufficient — FTS5 is out of scope. A needle
  containing characters JSON escapes (a literal quote, a newline) won't match the
  JSON-escaped stored bytes; this is a plain-word forensic search, and the raw
  `.http` stays ground truth via `proxy raw <id>`.
- `bacio proxy jobs [--repo <PREFIX>] [--issue <KEY>] [--mode <m>] [--since <d>]`
  / `GET /proxy/jobs?repo=&issue=&mode=&since=&from=&limit=`
  (BACI-322) — the **transcript browser** list: one summary row per distinct
  dispatch that has parsed captures, backing the Monitor screen's Transcript
  page. Backed by `Store.ListJobTranscripts`, a `GROUP BY dispatch_id`
  aggregation over `proxy_messages` (turn count = the primary captures, summed
  token usage, the primary thread's model — picked from an `is_primary` row, not
  a blind `MAX(model)`, so an auxiliary title-gen capture's model never wins —
  and the newest capture's `started_at`). Each distinct dispatch is best-effort
  enriched via a cached `GetDispatch` (the `ListProxyCapturesEnriched` idiom)
  for its issue key / mode / agent / repo prefix. `proxy_messages` has no
  `repo_id`, so the `--repo` / `--issue` / `--mode` scoping is applied in Go
  against the enrichment after the grouped, `LIMIT`-capped scan. The deep-link
  key is the `dispatch_id` (the same id `proxy job <dispatch_id>` takes), so the
  Transcript page's per-transcript URL never rots. Note the bare `/proxy/jobs`
  path is distinct from `/proxy/jobs/{dispatch_id}/transcript` — Go's `ServeMux`
  ranks the longer, wildcard-bearing pattern as the more specific match, so the
  two coexist.

The live recorder parses going forward; the captures it **missed** are
backfilled by BACI-321 (see the backfill section below) — so pre-306
history and any dropped/failed live parse eventually land in
`proxy_messages` too.

---

## Backfill sweep (BACI-321)

The live parse is best-effort and silently drops work in known cases: a
full recorder hand-off queue drops the observation
([`proxy_recorder.go`](../internal/api/proxy_recorder.go) `Record`); a
transient parse/store error skips the row (logged + swallowed in
`parseMessage`); any window where capture ran but parsing was off. A
capture that misses the live parse keeps its `proxy_requests` row but gets
no `proxy_messages` row, so its turn never appears in `Store.JobTranscript`.

BACI-321 adds a periodic, leader-gated, in-process **backfill sweep** that
reparses dispatch-correlated Anthropic captures the live path missed. The
raw `.http` file already on disk is the parseable substrate — nothing new
is captured.

### Home + cadence — `internal/controller`

`controller.ReparseProxyMessagesIfLeader(s, el, log)` mirrors
`ArchiveSweepIfLeader`: nil-tolerant, gated on `el.CurrentState().AmLeader`,
errors logged-and-swallowed. A ticker goroutine in `Controller.Start` fires
it on `store.ProxyReparseInterval` (1 minute), plus a sweep-on-startup call
so a short-lived process catches up. The overlap guard is free — the helper
runs inline in its goroutine's `for/select` and `time.Ticker` drops ticks
when the receiver is busy, so a long sweep just skips the next tick(s).

**Leader-gating is mandatory.** `proxy_requests` / `proxy_messages` live in
the shared `~/.bacio/db.sqlite` and every `bacio web`/`api`/worktree server
runs a recorder; an ungated sweep would have every process racing the same
global unparsed set, double-inserting rows. The `…IfLeader` guard makes
exactly one process sweep.

### Core seam — a store method

`internal/store` (which already imports `internal/anthropic`) holds the
core so the sweep and the CLI share one implementation:
`Store.ReparseUnparsedDispatches(opts)` finds every eligible dispatch and
calls `Store.ReparseDispatch(id)` for each. The four-step parse glue is
folded into `anthropic.ParseAndClassify` (`ParseCapture` + `Classify`),
which the live recorder's `parseMessage` now calls too, so the live and
backfill paths can't drift.

### Ordering + the v1 scope constraint

`Classify` computes each capture's delta against `LatestThreadState` (the
most-recent primary row by `id`), and `JobTranscript` reassembles
`ORDER BY id`. So a dispatch's captures must be parsed in chronological
order. The **non-destructive backfill handles fully-unparsed dispatches only**
(a dispatch with no `proxy_messages` rows at all): replay all captures
`started_at ASC` so ids stay monotonic and the delta chain is correct, with no
delete. A dispatch that already has rows — whether a partial gap or a full set
of *misclassified* rows (the BACI-325 case where every Opus 4.x turn was filed
auxiliary, so the dispatch has rows but zero primaries) — is invisible to that
backfill and needs a destructive rebuild: `Store.RebuildDispatch(id)` deletes
the dispatch's `proxy_messages` rows, clears its `parse_failed_at` markers, and
replays every capture `started_at ASC` through the corrected parser. It ships
behind `bacio proxy reparse --rebuild --dispatch <id>` (BACI-325). A **global**
`--rebuild` (no `--dispatch`) stays **out of the automated loop** and refused —
an unbounded destructive write over the shared store is out of scope. The
rebuild is non-transactional (the `s.DB`-bound `AddProxyMessage` /
`LatestThreadState` have no tx variant), so a concurrent `JobTranscript` read in
the brief delete→re-insert window may 404 transiently; acceptable for a manual,
off-hot-path recovery verb.

### Terminal-failure marker + quiet window

`proxy_requests.parse_failed_at` is stamped when a reparse attempt yields
no row (a truncated/malformed capture that can never parse), so eligibility
excludes it (`… AND parse_failed_at IS NULL`) and the sweep doesn't re-read
its file every minute. The live recorder stamps the same marker on its own
parse miss, for consistency. The sweep also skips any dispatch whose newest
capture is younger than `store.ProxyReparseQuietWindow` (2 minutes), so it
never reparses a job the live recorder is actively streaming into.

The marker is durable — "already given up on stays given up on" — so a
capture that failed for a *fixable* reason (e.g. the BACI-323 marker-collision
parser bug, fixed above) stays stamped even after the parser is corrected.
`bacio proxy reparse --retry-failed` (BACI-323) clears `parse_failed_at` on
the still-unparsed captures in scope (`Store.ClearProxyParseFailed` — the
inverse of the marker stamp, gated on no `proxy_messages` row so it never
resurrects a classified capture) *before* the reparse, so those dispatches
become eligible again and backfill in one pass — no `--rebuild` needed,
because a cleared failed capture is still fully unparsed.

### CLI — `bacio proxy reparse`

The first **mutating** proxy verb, following the six agent-CLI rules:
`--json` in, a `proxy.reparse` schema entry, `--dry-run` (project the
dispatch/capture counts, touch nothing), store-boundary validation. No args
= sweep all eligible; `--dispatch <id>` scopes to one job; `--retry-failed`
clears `parse_failed_at` on the still-unparsed captures in scope first (so
previously-failed captures backfill once the parser bug is fixed — BACI-323);
`--rebuild --dispatch <id>` destructively rebuilds one dispatch (delete its
`proxy_messages` rows + replay all captures through the current parser — the
BACI-325 recovery for a dispatch misclassified by a since-fixed parser bug); a
global `--rebuild` (no `--dispatch`) is refused. Remote-capable
(`POST /proxy/reparse`) for parity with the rest of the proxy group. A non-empty
wet run records a `proxy.reparse` audit row (`bacio-controller` for the sweep
path, the caller's actor for the CLI path).

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

## Monitor capture drill-down (BACI-308)

BACI-308 extends the Monitor screen with a right-docked **capture sheet**
(`MonitorCaptureSheet.jsx`) beside the FQDN table — Option A (master-detail
side sheet) from the design exploration. Clicking an FQDN row shrinks the
table left and opens the sheet, which walks three body states for that host:

- **Capture list** — the host's captures newest-first
  (`api.listProxyCaptures(host)` → `GET /proxy/captures`), each row carrying
  its dispatch's issue-key + mode chip (borrowed from the design's Option D)
  so the job context is one glance in. Simple "showing latest N" cap — no
  Prev/Next paging.
- **Capture detail** — one capture's parsed summary (model / stop reason /
  token usage from `api.anthropicCapture`) plus the raw `.http` body
  (`api.getProxyCaptureRaw`). A capture that isn't a parseable Anthropic turn
  (a `count_tokens` probe, an error, a non-Anthropic host — the parsed read
  404s by design) shows the raw body alone behind a banner, not an error
  toast.
- **Job transcript** — the whole dispatch's assembled transcript
  (`api.jobTranscript`), adapted into the **reused** `<TranscriptView>`.

The one piece of genuinely new render code is the adapter
[`anthropicAdapter.ts`](../desktop/frontend/src/lib/transcript/anthropicAdapter.ts):
`anthropicTranscriptToParseResult` walks the captured `AnthropicTranscript`
(model JSON tags already snake_case-aligned to the viewer's `types.ts`) into
the viewer's `TranscriptEvent[]` — user-prompt / user-tool-result / assistant
turns, with the auxiliary turns appended as trailing assistant events. The
minimap, filter chips, per-event cards, and token totals are reused wholesale;
`TranscriptView` grew one optional `parsed?: ParseResult` prop (an alternative
to its `source: string`) so the captured transcript skips the `.jsonl` parse.

The new reads are wired end-to-end on both transports — `MonitorService`
(`ListCaptures` returning the camelCase `ProxyCaptureRowDTO`, `CaptureRaw`,
`Capture`, `JobTranscript`) for Wails, the matching `GET` reads + a
`callText` text-fetch helper for the web build — with the cross-transport
`ProxyCaptureRow` / `ProxyMessage` / `AnthropicTranscript` aliases (BACI-108
pattern) living in
[`desktop/frontend/src/lib/proxyCaptures.ts`](../desktop/frontend/src/lib/proxyCaptures.ts).

## Monitor Network + Transcript tabs (BACI-322)

BACI-322 splits the single Monitor screen into two URL-synced sub-tabs under
the one `Monitor` Topbar entry — the BACI-308 drill-down conflated raw-traffic
watching with transcript reading, two different jobs:

- **Network** (`/:prefix/monitor`) — today's screen lifted verbatim into
  [`NetworkPanel.jsx`](../desktop/frontend/src/components/NetworkPanel.jsx): the
  per-FQDN traffic table + the right-docked capture sheet. Near-zero regression
  risk (it's the same code).
- **Transcript** (`/:prefix/monitor/transcripts`) — a first-class browser over
  the per-dispatch transcripts
  ([`TranscriptListPanel.jsx`](../desktop/frontend/src/components/TranscriptListPanel.jsx)),
  backed by the new `GET /proxy/jobs` list (active-repo scoped, with a
  client-side issue-substring filter + a server-side job-mode `<select>`). Each
  row links to the **deep-linkable full-transcript route**
  `/:prefix/monitor/transcript/:dispatchId`
  ([`TranscriptRoute.jsx`](../desktop/frontend/src/components/TranscriptRoute.jsx)),
  which renders the same assembled transcript the BACI-308 sheet shows — the
  reused `<TranscriptView>` via the extracted
  [`JobTranscriptBody.jsx`](../desktop/frontend/src/lib/transcript/JobTranscriptBody.jsx)
  (one copy shared by the sheet and the route). The deep-link is keyed on
  `dispatch_id` (the transcript key everywhere), so the URL is shareable from
  other surfaces / comments without rotting; the SPA fallback already serves the
  nested route on both transports, so a cold refresh resolves it.

The one genuinely-new backend is `Store.ListJobTranscripts` + `GET /proxy/jobs`
+ the `bacio proxy jobs` CLI twin (see the read-surface list above) — no
"list transcripts" query existed before (every capture list was host-anchored).
The new read rides the same cross-transport chain — `MonitorService.ListJobTranscripts`
returning the camelCase `JobTranscriptRowDTO` for Wails, the matching `GET`
reshape for the web build — with the cross-transport `JobTranscriptRow` alias in
`proxyCaptures.ts`.

---

## What's deliberately out of scope

- **Forward proxy / `HTTPS_PROXY` all-FQDN MITM** — the other side of
  the reverse-vs-forward fork. Reserved for the aggregate-by-FQDN work
  (BACI-303/304) once multi-upstream routing earns its keep.
- **Per-FQDN aggregation / stats read surfaces** — shipped in BACI-303
  (`GET /proxy/stats` + `bacio proxy stats`); see the read-surface note
  above.
- **Anthropic request/response body parsing** (model, token usage,
  turn/tool counts) — shipped in BACI-305 (classification) + BACI-306
  (per-job message detail); see the message-detail section above.
- ~~**The Monitor web screen** (and the React `api.ts` seam method that
  consumes `GET /proxy/stats`)~~ — **shipped in BACI-304** (see the Monitor
  screen section above).
- ~~**The per-job drill-down** on the BACI-306 transcript (a per-request raw
  view + the parsed per-job message transcript, with REST + CLI reads to
  fetch a single capture and a job's reconstructed transcript)~~ — **shipped
  in BACI-308** (see the capture drill-down section above).
- **Job-first grouping (the design's Option D)** — host → jobs → summed
  per-job tokens is a richer lens deferred past BACI-308; the shipped drill
  is the flat host → captures list with an issue-key/mode chip.
- **Per-capture eval-note composing on the proxy transcript** —
  `TranscriptView` supports `onPostEval`, but anchoring eval notes to
  `proxy_messages` rows (no issue/comment binding like the `.jsonl`
  transcripts had) is its own work.
- ~~**`bacio proxy reparse` backfill** of raw `.http` the live path missed
  into `proxy_messages`~~ — **shipped in BACI-321** (see the backfill section
  above). The destructive **per-dispatch** rebuild
  (`--rebuild --dispatch <id>`) shipped in BACI-325 (recovers a dispatch
  misclassified by a since-fixed parser bug). A **global** `--rebuild` (no
  `--dispatch`) and late re-correlation of `dispatch_id IS NULL` captures stay
  out of scope.
- **Raw-log-file retention / cleanup** — the index prune is BACI-302; the
  on-disk raw files have no auto-prune yet.
- **Retiring the `.jsonl` transcript attachments** — shipped in BACI-307:
  the `attach_transcript` MCP tool, its dispatch-preamble step, and the
  per-issue transcript-doc plumbing are gone; `proxy_messages` is the
  canonical source. Existing legacy transcript docs are left in place
  (no destructive migration) and still open as plain source. The review
  skill is stubbed pending its BACI-309 re-platform.
