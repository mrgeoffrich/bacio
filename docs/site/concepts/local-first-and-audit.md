---
title: Local-first and the audit log
description: One SQLite file on your laptop, every mutation recorded with who/when/what, pure-Go and no cloud — until you opt into git-backed sync.
---

# Local-first and the audit log

## Where your data lives

One SQLite file at `~/.bacio/db.sqlite`. That's it. Every repo, every issue, every comment, every link, every document — one file. Move it, back it up, override the location with `--db <path>` per command.

The driver is `modernc.org/sqlite` — pure-Go, no CGO. That means:

- bacio cross-compiles cleanly to any platform Go targets.
- `brew install bacio` works the same on macOS and Linux.
- `go install github.com/mrgeoffrich/bacio/cmd/bacio@latest` works without a C toolchain.

## Schema is idempotent

bacio re-applies its schema (`CREATE TABLE IF NOT EXISTS …`) on every DB open. Column additions go through a `migrate()` path. Practically: upgrading bacio doesn't require a migration step. Open the binary, it sorts itself out.

## The audit log

Every mutation records a row:

- **Actor** — `--user <name>`. Defaults to your OS user. AI agents are expected to pass `--user <agent-name>` so attribution stays clean.
- **Op** — the canonical operation name (`issue.add`, `feature.edit`, `sync.renumber`, …). Mirrors the JSON schema names.
- **Target** — the entity touched (`MINI-42`, the feature slug, the document filename, …).
- **Details** — a free-text blob with context (the title that was set, the cascade counts, the source phrase, …).
- **Timestamps** — created-at on the row itself.

You can read the audit log:

- **From the TUI** — the History tab, last-first.
- **From the CLI** — `bacio history`, with filters like `--since 1d`, `--user claude`, `--op issue.add`, `--target MINI-12`.
- **Inside an agent prompt** — *"what did Claude do yesterday?"* triggers `bacio history -o json --since 1d --user agent-claude`.

## Retention: 60 days, by default

`pruneHistory` runs on every DB open and removes rows older than 60 days. That keeps the local DB lean for read-heavy commands (`bacio history`, the TUI History tab).

If you need long-term records — for compliance, post-mortems, *"when did we decide this?"* — **enable [git-backed sync](/guides/sync-across-machines)**. The audit log is included in the synced YAML repo and survives prune; the sync repo is your long-term archive.

## What gets written

Mutations are recorded in dotted op form (`<entity>.<verb>`):

| Entity | Verbs |
|---|---|
| `repo` | `create` |
| `feature` | `create`, `update`, `delete` |
| `issue` | `create`, `update`, `state`, `assign`, `claim`, `delete` |
| `comment` | `add` |
| `relation` | `create`, `delete` |
| `pr` | `attach`, `detach` |
| `tag` | `add`, `remove` |
| `document` | `create`, `update`, `rename`, `delete`, `link`, `unlink` |
| `sync` | `renumber`, `rename` |

`bacio doc upsert` records `document.create` or `document.update` depending on whether it created the row.

**Reads are not logged.** `*.list`, `*.show`, `*.brief` don't produce audit rows. `--dry-run` doesn't either — it explicitly bypasses the write path.

## The two layers of identity

- **Human address** — keys (`MINI-42`), slugs (`auth-rewrite`), filenames (`design.md`). What you and your agent talk about.
- **Sync identity** — UUIDv7 on every record, assigned at create time. What sync matches on so renumbers and renames don't lose history. You only see it when debugging sync.

## Trust model

bacio assumes you trust your laptop. There's no encryption at rest beyond filesystem permissions, no access control beyond OS user, no auth on the CLI. If you enable `bacio api`, you can require a bearer token, but the default is no auth on localhost.

If your kanban contains sensitive data (security issues, customer data), the relevant lever is **don't sync it to a shared remote** — or sync it to a remote only you have access to. The data isn't doing anything cloud-y; it's all in your file.

## See also

- **[Configuration](/reference/config)** — where files live, env vars, `--db` override.
- **[`bacio history`](/reference/cli/history)** — the CLI reference for the audit log.
- **[Sync across machines](/guides/sync-across-machines)** — long-term retention through a git remote.
- **[How agents drive bacio](/concepts/how-agents-drive-bacio)** — why `--user` matters on agent calls.
