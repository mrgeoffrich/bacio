---
title: Document folders
description: Organise a project's pages into a Confluence-style tree — folders are organisational only, and filenames stay unique across the whole project.
---

# Document folders

Documents used to be one flat list per project. Once a project has a few dozen of them — design notes, decision records, session retros — a flat list stops being navigable.

**Document folders** give you a tree: folders containing pages, nested as deep as you like, with the whole thing rendered as a rail down the left of the Documents page. It reads like Confluence, not like a file browser.

## Folders and pages

- A **page** is a document — the same record it always was, with a filename, a type, content, and links to issues and features.
- A **folder** is a container. It has a name, a parent (or none, if it sits at the tree root), and an order among its siblings. It holds pages and other folders.

Nesting is capped at 16 levels, and a folder can't be moved inside its own subtree — both are refused by the store, on every surface.

## The one rule that surprises people

> **Filenames stay unique across the whole project. Folders are organisational only.**

Two pages in different folders **cannot** share a name. `Design/API.md` and `Ops/API.md` are not two different pages — the second one is a collision:

```bash
$ bacio doc add API.md --type architecture --content "…"
bacio: a document with that filename already exists in this repo
```

This is a deliberate trade. A document's filename is its identity everywhere — its URL, its `bacio doc show` argument, its `bacio doc link` target, the folder it occupies in a sync repo. Keeping that identity flat means:

- **Moving a page never breaks anything.** Its links, its URL, and its on-disk sync path are all untouched by a move. Reorganise the tree as often as you like.
- **Renaming a folder is cheap** — one record changes, not every page underneath it.
- **A page always has one name**, so `bacio doc show auth-spec.md` works without you remembering where you filed it.

If you want folder-ish names, put them in the filename: `design-api.md`, `ops-api.md`. bacio's own `--from-path` derivation does exactly that, flattening `docs/planning/not-shipped/foo.md` to `docs-planning-not-shipped-foo.md`.

## Deleting a folder never deletes a page

`bacio doc folder rm` deletes the folder and its descendant folders. Every document anywhere in that subtree is **re-rooted** — moved to the top of the tree, never deleted. A folder is organisational, so losing one must never lose a page.

Rehearse it first and you get both counts:

```bash
$ bacio doc folder rm Quotes --dry-run
[dry-run] no changes were written
Folder:            Quotes
Subfolders:        1 (deleted with it)
Documents:         1 (re-rooted, never deleted)
```

## Driving it from the CLI

Folders are addressed by their **slash display path** — exactly the string `bacio doc folder list` prints. Segments are matched exactly and are **case-sensitive**.

```bash
bacio doc folder add Design                     # a root-level folder
bacio doc folder add API --parent Design        # → Design/API
bacio doc folder list                           # the tree, one path per line

bacio doc mv auth-spec.md --folder Design/API   # file a page
bacio doc mv auth-spec.md --to-root             # un-file it

bacio doc folder rename Design Architecture     # rename in place
bacio doc folder mv Design/API --to-root        # promote a folder + its subtree
bacio doc folder rm Design --dry-run
```

**The empty path is the tree root**, and it's a real destination rather than a missing one. That's why every destination flag has an explicit spelling: `--to-root` on `doc folder mv` and `doc mv`. (On the `--json` path the key must be *present* — omitting `to` / `folder` is an error, so a typo can't silently re-root a subtree.)

`--position` on `bacio doc mv` is a loose sort key within the folder — siblings may share one, and listings tie-break on filename. Omit it to append.

## On the Documents page

The tree is the left rail, and it's the only navigator: it replaced the two panes that used to sit there (the facet rail and the document list), which is where the editor got its extra width from.

- **Click a folder and you get a real page**, not a dead end: a folder page with a live index of its children and a **New page** button. Clicking through the tree always lands you somewhere readable.
- **Search flattens the tree automatically.** Type in the rail's search box, or activate a facet, and the rail flips to a flat ranked list of matches; clear it and the tree comes back. The `Tree | All docs` switch above is the manual override for when the automatic choice is wrong.
- **Drag a page onto a folder** to file it. Drop it on the empty space below the tree to move it to the root.
- **A banner names the space you're in** — the project name plus a `git` or `workspace` pill — and the `Space` facet group below the tree is how you switch.

## Sync

Folders sync. Each one is a small record under `repos/<PREFIX>/folders/` in the sync repo, and it carries the list of pages inside it, in order — so both the tree shape and the order of pages within a folder survive a round trip. Nothing changes about how a *page* is stored: `repos/<PREFIX>/docs/<filename>/` is byte-for-byte what it always was, which is why the tree is invisible (and harmless) to an older bacio on another machine.

See [Sync across machines](/guides/sync-across-machines#workspaces-folders-and-lanes).

## See also

- **[`bacio doc`](/reference/cli/doc)** — every document verb, including `folder` and `mv`.
- **[Data model](/concepts/data-model)** — where documents sit relative to issues and features.
- **[Workspaces](/concepts/workspaces)** — workspaces get the document tree too.
