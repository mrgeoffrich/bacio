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
| `bacio doc list` | List documents. Metadata only — never inlines `content`. Filters: `--type <type>`. Inside a sync repo, `--repo <PREFIX>` is required (or `--all-repos`). |
| `bacio doc show <filename> [--raw]` | Print metadata + content + links. `--raw` prints content only. `--metadata` skips the body. |
| `bacio doc edit <filename>` | Patch type / content. |
| `bacio doc rename <old> <new>` | Rename in place. Links are preserved. Optional `--type <new-type>`. |
| `bacio doc export <filename>` | Materialise back to disk. `--to-path` (reuses the path the doc was last imported from) or `--to <path>`. |
| `bacio doc download <filename>` | Stream the content to stdout, or pass `--to <path>` to write straight to a file. Remote-mode-friendly counterpart to `export`. |
| `bacio doc rm <filename>` | Delete a document and its links. Use `--dry-run`. |
| `bacio doc link <filename> <ISSUE-KEY\|feature-slug>` | Upsert a link with optional `--why <text>`. |
| `bacio doc unlink <filename> <ISSUE-KEY\|feature-slug>` | Remove a link. |
| `bacio doc mv <filename>` | File a page into a folder (`--folder <PATH>`) or back to the tree root (`--to-root`). |
| `bacio doc folder …` | Manage the folder tree — see [below](#the-folder-tree). |

`<ISSUE-KEY|feature-slug>` auto-detects: anything matching `PREFIX-N` is an issue key, otherwise it's a feature slug in the current repo.

### `bacio doc list` flags

| Flag | What it does |
|---|---|
| `--type <type>` | Filter by document type. |
| `--repo <PREFIX>` | The global project selector — operate on this prefix instead of resolving from the current working tree. **Required for a [workspace](/concepts/workspaces)** (it has no working tree) and inside a [sync repo](/guides/sync-across-machines), where `--all-repos` is the alternative. Falls back to `$BACIO_REPO`. |
| `--all-repos` | List across every tracked repo. Inside a sync repo, walks every prefix recorded in `index.yaml`. |

Inside a sync repo the command reads `repos/<PREFIX>/docs/*/doc.yaml` off disk; `--type` filters as in project-repo mode. Metadata only — `content` is never inlined.

## The folder tree

Documents can be organised into a tree of folders. Folders are **purely organisational**: a filename stays flat and unique across the whole project, so filing a page never changes its identity, its links, its URL, or where it lands in a sync repo. See [Document folders](/concepts/document-folders) for the model.

| Subcommand | What it does |
|---|---|
| `bacio doc folder list` | The whole tree, in tree order, one slash path per line. |
| `bacio doc folder add <NAME>` | Create a folder. `--parent <PATH>` to nest it; omit for the tree root. |
| `bacio doc folder rename <PATH> <NEW-NAME>` | Rename in place. The parent is untouched. `<NEW-NAME>` is a single segment. |
| `bacio doc folder mv <PATH>` | Re-parent a folder and its whole subtree. `--to <PARENT-PATH>` or `--to-root`. |
| `bacio doc folder rm <PATH>` | Delete a folder. Subfolders go with it; **every page inside is re-rooted, never deleted.** |
| `bacio doc mv <filename>` | File a page. `--folder <PATH>` or `--to-root`, plus optional `--position N`. |

Folders are addressed by their **slash display path** — exactly the string `bacio doc folder list` prints. Segments are matched exactly and are **case-sensitive**. Nesting is capped at 16 levels, and moving a folder inside its own subtree is refused.

```bash
bacio doc folder add Design                      # a root-level folder
bacio doc folder add API --parent Design         # → Design/API
bacio doc mv auth-spec.md --folder Design/API    # file a page
bacio doc mv auth-spec.md --to-root              # un-file it
bacio doc folder mv Design/API --to-root         # promote a folder + subtree
bacio doc folder rm Design --dry-run             # counts before you commit
```

::: tip `""` is a destination, not a blank
The empty path means **the tree root**. Because that's a real value, the destination is required: pass `--to-root` (or `--to ""` / `--folder ""`) explicitly. On the `--json` path the `to` / `folder` key must be *present* — omitting it is an error, never an implicit re-root.
:::

`--position` on `bacio doc mv` is a loose sort key within the folder; siblings may share one and listings tie-break on filename. Omit it to append.

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

- **[Document folders](/concepts/document-folders)** — the tree, and why filenames stay flat.
- **[`bacio issue brief`](/reference/cli/issue)** — the bulk-context call that inlines linked-doc content.
- **[TUI Docs tab](/reference/tui/docs)** — read documents in the terminal.
