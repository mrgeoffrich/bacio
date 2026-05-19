---
title: CLI reference
description: Every bacio command at a glance — what each verb does, global flags, output formats.
---

# CLI reference

bacio's CLI is the same surface that agents drive — every mutating command accepts JSON via `--json`, every payload schema is reachable at runtime via `bacio schema`, and every mutation supports `--dry-run`. See [agent-CLI principles](/concepts/how-agents-drive-bacio) for the design rules behind that.

## Top-level commands

| Command | What it does |
|---|---|
| [`bacio init`](/reference/cli/init) | Bind the current git repo to a 4-letter prefix. |
| [`bacio repo`](/reference/cli/repo) | List, show, or remove tracked repos. |
| [`bacio feature`](/reference/cli/feature) | Manage features (groups of issues). Includes `plan` for dependency-ordered execution. |
| [`bacio issue`](/reference/cli/issue) | Manage issues — add, list, show, brief, edit, state, assign, unassign, next, peek, rm. |
| [`bacio comment`](/reference/cli/comment) | Add and list issue comments. |
| [`bacio link`](/reference/cli/link) / `bacio unlink` | Create / remove typed issue relations (`blocks`, `relates-to`, `duplicate-of` — stored as `blocks`, `relates_to`, `duplicate_of`). |
| [`bacio pr`](/reference/cli/pr) | Attach, detach, and list pull requests on an issue. |
| [`bacio tag`](/reference/cli/tag) | Add or remove tags on issues. |
| [`bacio doc`](/reference/cli/doc) | Manage per-repo text documents and their links to issues / features. |
| [`bacio agent`](/reference/cli/agent) | Track live AI-agent sessions and their issue claims (local-only registry). |
| [`bacio status`](/reference/cli/status) | One-screen summary of the current repo. |
| [`bacio history`](/reference/cli/history) | Query the audit log of mutations. |
| [`bacio schema`](/reference/cli/schema) | List and show JSON schemas for every `--json` payload. |
| [`bacio sync`](/reference/cli/sync) | Mirror the SQLite DB to a git-backed YAML+markdown repo. |
| [`bacio api`](/reference/cli/api) | Run the REST API server (API only — no `/ui/` mount). |
| [`bacio web`](/reference/cli/web) | Run the REST API + embedded web bundle and open the browser. |
| [`bacio install-skill`](/reference/cli/install-skill) | Install the canonical `SKILL.md` for AI agents into the repo. |
| [`bacio install-sample-skills`](/reference/cli/install-sample-skills) | Install the bundled flow-level skill packs (file-issue, triage, stand-up, plan-feature). |
| [`bacio tui`](/reference/cli/tui) | Open the full-screen kanban TUI. |

## Global flags

All commands inherit these from the root:

| Flag | Default | What it does |
|---|---|---|
| `-o`, `--output` | `text` | Output format — `text` for humans, `json` for agents and scripts. |
| `--db` | `~/.bacio/db.sqlite` | Override the database path. Useful for tests and isolated experiments. |
| `--user` | OS user | Actor name recorded in history. AI agents should pass this explicitly. |
| `--dry-run` | off | Validate the request and emit the projected result without writing. No audit log entry. |
| `--remote` | — | Talk to a `bacio api` server at this URL instead of the local DB. Falls back to `BACIO_REMOTE`. |
| `--token` | — | Bearer token for the remote API. Falls back to `BACIO_API_TOKEN`. |

## Conventions

- **JSON in via `--json`.** Mutating commands accept `--json <inline>`, `--json -` (stdin), or `--json @path/to.json` (file). Mutually exclusive with positional and per-field flags.
- **Lean lists, fat shows.** `*.list` strips heavy bodies; `*.show` and `bacio issue brief` return full records.
- **Strict decode.** Unknown JSON fields are rejected, not silently dropped.
- **Audit log.** Every mutation records who, when, and what changed. Pruned to 60 days on every DB open.
