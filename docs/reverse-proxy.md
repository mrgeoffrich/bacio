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
sites build the string through `agentmode.LaunchCommand(endpoint)` so
they can never drift.

### Consequence: a running bacio server is a hard dependency

Because `ANTHROPIC_BASE_URL` points at the local proxy, a `bacio web`
(or `bacio api`) **must be running on that port** for an agent session
to reach the Anthropic API. Without it the worker gets
connection-refused on its first request. This is intended per the
feature's scoping decisions; the activation banner calls it out.

---

## What's deliberately out of scope (BACI-301)

- **Forward proxy / `HTTPS_PROXY` all-FQDN MITM** — the other side of
  the reverse-vs-forward fork. Reserved for the aggregate-by-FQDN work
  (BACI-303/304) once multi-upstream routing earns its keep.
- **Traffic monitoring / per-FQDN stats** — BACI-302, BACI-303.
- **Anthropic request/response capture to disk + a SQLite index** —
  BACI-305, BACI-306.
- **The Monitor web screen** — BACI-304.
- **Retiring the `.jsonl` transcript attachments** — BACI-307.
