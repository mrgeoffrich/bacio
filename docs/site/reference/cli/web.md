---
title: bacio web
description: Run the REST API + embedded web bundle and open the browser.
---

# `bacio web`

Run the local REST API, mount the embedded React web bundle at `/ui/`, and pop the OS default browser to `http://<addr>/ui/`. It's the recommended one-liner for humans who want bacio's kanban board.

```bash
bacio web                                  # API + /ui/ + browser
bacio web --no-open                        # API + /ui/, skip the browser launch
bacio web --addr 127.0.0.1:7777 --token T  # bind elsewhere, require bearer
```

## Flags

| Flag | Default | What it does |
|---|---|---|
| `--addr` | `127.0.0.1:5320` | Bind address as `host:port`. |
| `--port` | — | Shorthand to override only the port (keeps host from `--addr`). |
| `--token` | — | Require `Authorization: Bearer <token>` on every request **except `/healthz`**. Falls back to `BACIO_API_TOKEN`. |
| `--cors-origin` | — (empty allow-list) | Allow cross-origin browser requests from this origin (repeatable; e.g. `http://localhost:5174`). Empty allow-list = same-origin only. |
| `--no-open` | off | Mount the bundle but skip the browser launch. Use for SSH sessions, headless CI, or agent-driven smoke tests that drive the page via Playwright. |

Plus all [global flags](/reference/cli/index#global-flags). One caveat: the persistent `--user` flag is silently ignored under `bacio web` — incoming requests carry their own actor via the `X-Actor` header (default `"api"`).

## What it does

`bacio web` is the API + UI sibling of [`bacio api`](/reference/cli/api). On startup it:

1. Opens the SQLite store the rest of bacio shares.
2. Serves the same JSON API as `bacio api` — every route, every audit row, every dry-run header.
3. Mounts the embedded React bundle at `/ui/`, with a `301` from `/ui` → `/ui/` so the SPA's base path resolves correctly.
4. Polls `/healthz` every 100 ms for up to 5 s and launches the OS default browser (`open` on macOS, `xdg-open` on Linux, `rundll32` on Windows) at `http://<addr>/ui/` when it comes up.

Browser launch is best-effort — a failure logs a hint and the server keeps running. `--no-open` skips the launch entirely. When the embedded web bundle is absent (e.g. the binary was built with `./build.sh --skip-web`), `bacio web` prints a one-line hint and the browser launch is implicitly skipped — there's nothing meaningful to open.

## See also

- **[`bacio api`](/reference/cli/api)** — API-only sibling; no `/ui/` mount and no browser launch.
- **[Web app mode](/concepts/web-app-mode)** — the durable reference for the web bundle, including the build pipeline and deployment shapes.
- **[Run the API server](/guides/run-the-api-server)** — same API surface, same flags, no bundle.
