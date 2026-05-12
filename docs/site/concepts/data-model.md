---
title: Data model
description: How bacio thinks — repos, prefixes, issues, features, comments, links, PRs, tags, documents, history, and the UUID identity layer underneath.
---

# Data model

## The hierarchy in one paragraph

**repo → (optional) feature → issue.** A repo binds the current git working tree to a 4-letter prefix (`MINI`, `AUTH`). Inside that repo, you create **issues** (`MINI-42`) and optionally group them into **features** (slug-addressed, e.g. `auth-rewrite`). Issues can exist without a feature. Everything attaches to an issue: **comments**, **tags**, typed **links** to other issues, attached **PR URLs**. **Documents** are per-repo markdown blobs that link to one or many issues / features.

## At a glance

```
                ┌─────────────┐
                │    repo     │    prefix (MINI), name, path, remote
                │   (MINI)    │
                └──────┬──────┘
         ┌────────────┴────────────┬──────────────────┐
         │                         │                  │
   ┌─────▼─────┐             ┌─────▼─────┐      ┌─────▼──────┐
   │  feature  │             │   issue   │      │  document  │
   │ (slug)    │◄────────────│ (MINI-42) │      │ (file.md)  │
   └─────┬─────┘  feature_   └─────┬─────┘      └─────┬──────┘
         │        slug             │                  │
         │                         ├─ comment[*]      │
         │                         ├─ tag[*]          │
         │                         ├─ pr_url[*]       │
         │                         └─ relation[*]     │
         │                            ↓               │
         │                       ┌────┴─────┐         │
         │                       │  issue   │         │
         │                       └──────────┘         │
         │                                            │
         └─ link ─────────────────────────────────────┤
                                                      │
                          link ────────────── issue ──┘

   history (audit log) records every mutation across all of the above.
```

Direction notes:
- `issue.feature_slug` — many issues per feature, one feature per issue (optional).
- Documents link to one or many issues / features through a join table with an optional `--why` reason.
- Relations are typed edges between issues: `blocks`, `relates_to`, `duplicate_of` (canonical underscored form; the CLI also accepts the dashed spelling on input).

## Entities

### Repo

The unit of project scope. Auto-detected from the current working directory by walking up to find a `.git` toplevel. Identity is the absolute git toplevel path — moving the repo on disk creates a new bacio row.

- Has a **prefix** (4 alphanumeric chars), unique across the global DB.
- Auto-registers on first `bacio` command if not bound — `bacio init` is optional.

### Feature

An optional grouping of issues — think *project* or *epic*. Addressed by slug (kebab-case, auto-derived from the title; override with `--slug`).

- Has a title, description, created/updated timestamps.
- Issues link to a feature via `feature_slug`. Multiple issues per feature, one feature per issue.

### Issue

The unit of work. Addressed by canonical key `PREFIX-N` (e.g. `MINI-42`). On the CLI flag path, humans can also use the bare number; JSON payloads must use the canonical form.

- **State**: `backlog | todo | in_progress | in_review | done | cancelled | duplicate`. State parser tolerates dashes or spaces (`in-progress`, `in progress`).
- **Fields**: title, description, state, optional `feature_slug`, optional `assignee`, tags, timestamps.
- **Attached entities**: comments, typed relations, PR URLs, linked documents.

### Comment

A note on an issue. Has an `author` (passed via `--as <name>` on the CLI — there's no auth), a body (long text), and a created-at timestamp. Comments appear in the TUI card overlay, in `bacio comment list`, and inside `bacio issue brief`.

### Link (relation)

A typed edge between two issues. `bacio link <FROM> <type> <TO>`. Relation types come in three flavours: **`blocks`**, **`relates_to`**, **`duplicate_of`**. The CLI accepts both the dashed (`relates-to`) and underscored (`relates_to`) spelling on input; the **canonical stored form is underscored** (it's what the schema CHECK constraint enforces and what JSON output emits). Relations are stored one-directionally — `blocked-by` is the implicit inverse view of `blocks`, not a creatable type. The TUI surfaces relations in the card overlay; `bacio issue brief` includes them in its bulk-context payload; `bacio feature plan` uses `blocks` to compute execution order.

### PR (pull request)

An attached URL on an issue. bacio doesn't talk to GitHub — it just stores the URL. `bacio pr attach`, `bacio pr detach`, `bacio pr list`. URLs go through `ValidatePRURLStrict` (no whitespace, no control chars, length-capped at 2 KiB).

### Tag

A free-form label on an issue. Single-line, length-capped at 80, no control chars. `bacio tag add [KEY] [tag...]` / `bacio tag rm`. Used for filtering (`bacio issue list --tag bug`) and visual grouping.

### Document

A per-repo named text blob (markdown, etc.) with a typed category (`architecture`, `designs`, `project-in-planning`, …). Addressed by filename. Can be linked to many issues and features at once with an optional reason.

- **`bacio doc add` / `upsert` / `edit` / `rename` / `rm`** — CRUD.
- **`bacio doc link [filename] [ISSUE-KEY|feature-slug]`** — wire up the link. Auto-detects issue keys by the `PREFIX-N` shape; treats anything else as a feature slug.
- Linked-doc bodies are inlined into `bacio issue brief` (unless `--no-doc-content`).

### History (audit log)

Every mutation records a row: actor (`--user`), op (`issue.create`, `feature.update`, `sync.renumber`, …), target, and a details blob. Pruned to 60 days on every DB open. Survives [git-backed sync](/guides/sync-across-machines).

## Identity: the UUIDv7 layer

Every record carries an immutable **UUIDv7** assigned at create time. You almost never see it — `key`, `slug`, and `filename` are how humans (and JSON payloads) address things. But:

- **Sync matches by `uuid`, never by label.** That's how renumbers and renames survive — `MINI-7` getting renumbered to `MINI-12` doesn't lose comments, relations, or history.
- **Every JSON record includes a `uuid` field.** Use it when you're debugging the sync layer; ignore it the rest of the time.

## Timestamps

- **Created-at** on every entity.
- **Updated-at** on features, issues, and documents — bumped automatically on edits, state changes, and tag mutations.
- JSON output uses UTC RFC 3339 (`2026-05-03T07:27:14Z`) — that's the parsing contract.
- Text output renders in your local timezone (`2026-05-03 17:27 AEST`).

## Validation contract

Every mutation runs through validators in the store layer, so malformed input fails fast with a clear error rather than being silently normalised:

- **No control characters anywhere.** Single-line fields reject all C0 controls and DEL. Multi-line fields allow `\t \n \r`.
- **No silent trimming on identifiers.** Whitespace in a `filename`, `slug`, or URL is a hard error.
- **Length caps**: title 200, name/assignee 80, slug 60, filename 200, tag 80, PR URL 2 KiB, body fields 1 MiB.

See [JSON payloads](/reference/json-payloads) for how this surfaces over the agent contract.

## See also

- **[Local-first and audit log](/concepts/local-first-and-audit)** — where this data lives.
- **[How agents drive bacio](/concepts/how-agents-drive-bacio)** — how the contract uses this model.
- **[CLI reference](/reference/cli/)** — every command that creates, reads, or mutates these entities.
