---
title: bacio api
description: Run the REST API server (API-only — no /ui/ mount).
---

# `bacio api`

Run a REST server that exposes the same surface as the CLI. Same `inputs.*Input` structs, same `schemaRegistry`, same validators, same audit log — only the transport differs.

`bacio api` is **API-only**: it does not mount the embedded React web bundle and `GET /ui/` returns 404. Reach for [`bacio web`](/reference/cli/web) when you want the bundled kanban; `bacio api` is the right shape for headless agents, scripts, `BACIO_REMOTE` rigs, and CI.

```bash
bacio api                                  # bind 127.0.0.1:5320, no auth
bacio api --addr 127.0.0.1:7777 --token T  # require Authorization: Bearer T
BACIO_API_TOKEN=T bacio api                # token via env
bacio api --cors-origin http://localhost:5174   # opt in to cross-origin browser callers
```

## Flags

| Flag | Default | What it does |
|---|---|---|
| `--addr` | `127.0.0.1:5320` | Bind address as `host:port`. |
| `--port` | — | Shorthand to override only the port (keeps host from `--addr`). |
| `--token` | — | Require `Authorization: Bearer <token>` on every request **except `/healthz`** (so liveness probes never need to know the token). Falls back to `BACIO_API_TOKEN`. |
| `--cors-origin` | — (empty allow-list) | Allow cross-origin browser requests from this origin (repeatable; e.g. `http://localhost:5174`). Empty allow-list = same-origin only. |

Plus all [global flags](/reference/cli/index#global-flags). One caveat: the persistent `--user` flag is silently ignored under `bacio api` — incoming requests carry their own actor via the `X-Actor` header (default `"api"`).

Once running, point any client at the server — or set `BACIO_REMOTE` so other `bacio` calls go through the API instead of the local DB:

```bash
export BACIO_REMOTE=http://127.0.0.1:5320
export BACIO_API_TOKEN=…
bacio issue list                 # now hits the API
```

## See also

- **[`bacio web`](/reference/cli/web)** — same API plus the embedded React bundle and an OS browser launch.
- **[Run the API server](/guides/run-the-api-server)** — end-to-end guide with `curl` examples, endpoint mapping, error codes, and the `--remote` / `--token` CLI-client pairing.
- **[JSON payloads](/reference/json-payloads)** — the same contract the HTTP API exposes.
