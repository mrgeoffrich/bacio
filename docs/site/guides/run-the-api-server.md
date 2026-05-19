---
title: Run the bacio API server
description: Put the bacio CLI behind HTTP for web UIs, IDE plugins, long-running agents, or sharing a board across machines that can't sync git.
---

# Run the API server

`bacio api` exposes every CLI mutation and read over HTTP, backed by the same SQLite database, JSON shapes, validators, and audit log. The CLI conventions all apply — discover schemas, compose JSON, dry-run, then commit. Only the transport differs.

`bacio api` is API-only. If you want the bundled web kanban (same React tree the desktop app ships, served at `/ui/`), reach for [`bacio web`](/reference/cli/web) — it serves the same API surface, mounts the bundle, and opens the OS default browser as a one-liner.

## Start the server

```bash
bacio api                                  # bind 127.0.0.1:5320, no auth
bacio api --addr 127.0.0.1:7777            # custom port, still no auth
bacio api --token T                        # require Authorization: Bearer T
BACIO_API_TOKEN=T bacio api                # token via env
```

The default `127.0.0.1:5320` binding plus no auth is the trust boundary — only loopback callers can hit it. Bind to a public address only with `--token` set.

## Pointing other `bacio` calls at it

`bacio` itself can drive a remote server instead of the local DB:

```bash
export BACIO_REMOTE=http://127.0.0.1:5320
export BACIO_API_TOKEN=…                   # if the server enforces auth
bacio issue list                           # now hits the API
bacio issue add "New bug" --feature auth   # mutating verbs work too
```

Every read and mutating verb behaves identically over remote — same flags, same JSON output, same `--dry-run`, same `--user`. Audit rows are written by the server.

Verbs that touch the local filesystem or terminal error clearly in remote mode and stay local-direct: `bacio init`, `bacio install-skill`, `bacio doc add --from-path` / `--content-file`, `bacio doc export`, `bacio tui`, `bacio schema *`, `bacio status`.

## Discovery endpoints

The schemas are the same as the CLI's:

```bash
curl http://127.0.0.1:5320/schema/list           # every operation name + summary
curl http://127.0.0.1:5320/schema/issue.add      # full JSON Schema with examples[0]
curl http://127.0.0.1:5320/schema                # everything in one object
```

Routes follow REST under `/repos/{prefix}/...` — list/create on the collection, show/patch/delete on the item, plus sub-resources for state (`/state`, `/assignee`), batch ops (`/tags`, `/pull-requests`), graph edges (`/relations`, `/links`), bulk reads (`/brief`, `/plan`), and claim/peek (`/next`). Use `GET /schema/list` to enumerate every operation.

Issue keys in URLs and bodies must be canonical (`MINI-42`) — the bare-number shortcut isn't accepted over HTTP.

## A curl smoke test

```bash
# Health check (no auth required)
curl http://127.0.0.1:5320/healthz

# List issues in repo MINI
curl http://127.0.0.1:5320/repos/MINI/issues

# Add an issue, attributing the action to "claude-code"
curl -X POST http://127.0.0.1:5320/repos/MINI/issues \
  -H 'Content-Type: application/json' \
  -H 'X-Actor: claude-code' \
  -d '{"title": "Login 500 on Safari"}'

# Dry-run the same call
curl -X POST 'http://127.0.0.1:5320/repos/MINI/issues?dry_run=true' \
  -H 'Content-Type: application/json' \
  -H 'X-Actor: claude-code' \
  -d '{"title": "Login 500 on Safari"}'
```

## Headers and query params

| Header / param | What it does |
|---|---|
| `Authorization: Bearer <token>` | Required when `--token` is set. Constant-time compare. |
| `X-Actor: <agent-name>` | Stamps the audit log. Absent → falls back to literal `"api"` (not OS user). **Required** on `POST /repos/{prefix}/features/{slug}/next` (claiming work needs a real assignee). |
| `?dry_run=true` / `X-Dry-Run: 1` | Validate and project the result without writing. Response carries `X-Dry-Run: applied`. |
| `?with_description=true` | Inflate `description` on `*.list` responses. |
| `?with_content=true` | Inflate `content` on `doc.list` responses. |
| `?no_feature_docs=1`, `?no_comments=1`, `?no_doc_content=1` | Brief opt-outs on `GET /issues/{key}/brief`. |
| `?limit`, `?offset`, `?op`, `?kind`, `?actor`, `?since`, `?from`, `?to`, `?oldest_first` | History filters on `GET /history`. |

## Errors

```json
{ "error": "title is required", "code": "invalid_input", "details": {"field": "title"} }
```

| Status | Code | When |
|---|---|---|
| 400 | `invalid_input` | Malformed JSON, unknown field, validator failure, missing required `X-Actor` on claim. |
| 401 | `unauthorized` | Token configured and bearer is missing or wrong. |
| 404 | `not_found` | Path resolves no such entity. |
| 409 | `conflict` | Duplicate slug / prefix / PR URL / document filename. |
| 413 | `payload_too_large` | Body exceeds 4 MiB. |
| 500 | `internal` | Server-side panic (caught by recovery middleware). |

## API-only quirks

- **`POST /repos`** is the equivalent of `bacio init`, but the server can't see the caller's CWD — pass `{"name":"...", "path":"..."}` (plus optional `prefix`) explicitly.
- **`GET /repos/{prefix}/documents/{filename}/download`** is the only non-JSON endpoint. Streams the body as `text/markdown` with `Content-Disposition: attachment`. No audit row, no dry-run.
- **No CLI equivalent → not exposed:** `bacio install-skill` (writes a file in the caller's repo), `bacio tui` (terminal-bound), `bacio doc add --from-path` (read the file yourself and inline the content).

## Logs

`bacio api` writes a per-day log file in addition to its existing
stderr output. The destination follows the same resolver chain as the
per-worktree DB / port (BACI-73): `--log-dir` flag > `$BACIO_LOG_DIR`
> worktree manifest > `~/.bacio/logs/`. The filename is
`bacio-api-YYYY-MM-DD.log`. Each line carries a timestamp, level
(default `info`; override with `--log-level debug` for verbose
output), the request method/path/status/latency/actor, and any
structured fields the emitter passed.

```bash
# Tail the resolved log file.
tail -F "$(bacio status -o json | jq -r .log_dir)/bacio-api-$(date +%F).log"
```

A log-dir creation failure prints one stderr warning and continues
without a file sink — `bacio api` never refuses to start because of a
log problem. Full spec: [`docs/logging.md`](https://github.com/mrgeoffrich/bacio/blob/main/docs/logging.md).

## When to use the API

- **You're building a web UI or IDE plugin** on top of bacio.
- **Long-running agents** that don't want to fork a CLI per call.
- **One machine is the source of truth**, others are clients — set `BACIO_REMOTE` on the clients. (For multi-machine *peer* sync, prefer [git-backed sync](/guides/sync-across-machines).)

## See also

- **[`bacio api`](/reference/cli/api)** — the CLI reference.
- **[JSON payloads](/reference/json-payloads)** — the same contract the HTTP API exposes.
- **[How agents drive bacio](/concepts/how-agents-drive-bacio)** — the rules behind the contract.
