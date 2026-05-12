---
title: bacio doc
description: Per-repo text documents — design notes, decisions, anything markdown — linked to issues and features.
---

# `bacio doc`

Documents are markdown notes (or any text body) that live inside the bacio DB and link to one or many issues and features. They show up in the TUI Docs tab and — when linked — inside `bacio issue brief` for agents.

## Subcommands

| Subcommand | What it does |
|---|---|
| `bacio doc add <filename>` | Create a document. |
| `bacio doc upsert <filename>` | Create or update by filename. Use this from skills to skip the "show, branch, then add or edit" dance. |
| `bacio doc list [--type <type>]` | List documents. Metadata only — never inlines `content`. |
| `bacio doc show <filename> [--raw]` | Print metadata + content + links. `--raw` prints content only. `--metadata` skips the body. |
| `bacio doc edit <filename>` | Patch type / content. |
| `bacio doc rename <old> <new>` | Rename in place. Links are preserved. Optional `--type <new-type>`. |
| `bacio doc export <filename>` | Materialise back to disk. `--to-path` (reuses the path the doc was last imported from) or `--to <path>`. |
| `bacio doc download <filename>` | Stream the content to stdout (remote-mode-friendly counterpart to `export`). |
| `bacio doc rm <filename>` | Delete a document and its links. Use `--dry-run`. |
| `bacio doc link <filename> <ISSUE-KEY\|feature-slug>` | Upsert a link with optional `--why <text>`. |
| `bacio doc unlink <filename> <ISSUE-KEY\|feature-slug>` | Remove a link. |

`<ISSUE-KEY|feature-slug>` auto-detects: anything matching `PREFIX-N` is an issue key, otherwise it's a feature slug in the current repo.

## Document types

Canonical underscore form; the parser also accepts dashes / spaces:

```
user_docs · project_in_planning · project_in_progress · project_complete
vendor_docs · architecture · designs · testing_plans
```

The list is extensible — additional types may appear over time.

## `--from-path` derivation

Path-on-disk shortcuts mean you can skip flag fiddling for the common case:

- **Filename** replaces `/` with `-`, so `docs/planning/not-shipped/foo-plan.md` → `docs-planning-not-shipped-foo-plan.md`.
- **Type** is auto-derived from path prefixes: `docs/planning/{not-shipped,in-progress,shipped}/` → `project_in_{planning,progress,complete}`. For any other path, pass `--type` explicitly.
- **Content** is read from the path itself if `--content` / `--content-file` isn't given.

Explicit `--type` / `--content-file` always wins over derivation.

## Worked examples

```bash
# One-liner add: filename and type both derived, content read from disk.
bacio doc add --from-path docs/planning/not-shipped/auth-plan.md

# Idempotent maintenance from a skill — no probe-then-branch shell dance.
bacio doc upsert --from-path docs/planning/not-shipped/auth-plan.md

# Plan shipped: rename and bump the type in one step. Links survive.
bacio doc rename \
  docs-planning-not-shipped-auth-plan.md \
  docs-planning-shipped-auth-plan.md \
  --type project_complete

# Materialise the canonical version back onto disk
bacio doc export docs-designs-foo.svg --to-path
bacio doc export auth-spec.md --to docs/auth-spec.md

# Manual filename / type still works
bacio doc add auth-spec.md --type architecture --content-file docs/auth.md
bacio doc link auth-spec.md auth-rewrite --why "Source of truth for the JWT switch"
bacio doc link auth-spec.md MINI-42 --why "Reference for the 500 fix"
bacio doc list --type architecture
bacio doc show auth-spec.md --raw > /tmp/auth.md
```

## How linked docs show up

- **`bacio issue show <KEY>`** and **`bacio feature show <slug>`** both render a "Linked documents:" section: `auth-spec.md (architecture) — Source of truth for the JWT switch`.
- **`bacio issue brief <KEY>`** inlines the full doc bodies for any linked doc. Use `--no-doc-content` to keep metadata but skip bodies; `--no-feature-docs` to skip docs linked to the parent feature only.
- **TUI Docs tab** — the full-screen reader for any document.

## See also

- **[`bacio issue brief`](/reference/cli/issue)** — the bulk-context call that inlines linked-doc content.
- **[TUI Docs tab](/reference/tui/docs)** — read documents in the terminal.
