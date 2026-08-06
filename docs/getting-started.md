# Getting started with `bacio`

`bacio` is a kanban for a single developer (or a small team), built so Claude Code can drive it. The standard loop:

1. you ask Claude Code something — *"file an issue for the Safari bug"*, *"what's on my plate?"*, *"plan out the auth rewrite"*.
2. claude reads the relevant state from `bacio`, writes if needed, tells you what it did.
3. you read the result in your editor, in `bacio tui`, or on the CLI.

You **don't have to memorise bacio's commands**. claude has a copy of the canonical skill (installed once per repo) and knows the surface. You can drop into `bacio --help` if you want — but the design assumes you mostly won't.

This guide walks you through:

- Setting up `bacio` in a repo (no sync — quickest path to value).
- A first session driving `bacio` through Claude Code.
- The two boards — the agent's pipeline and your own kanban lanes.
- Keeping notes in a page tree.
- Optional: a **workspace** for work that isn't code.
- Optional: share the board between machines via git-backed sync.

---

## 1. One-time setup

Install the binary. Pre-built packages on macOS, Linux, and Windows; or build from source — bacio is pure-Go, no CGO.

```bash
# macOS and Linux
brew tap mrgeoffrich/bacio
brew install bacio
```

```powershell
# Windows
scoop bucket add bacio https://github.com/mrgeoffrich/scoop-bacio
scoop install bacio
```

```bash
# any platform, from source
go install github.com/mrgeoffrich/bacio/cmd/bacio@latest
```

Pick a project. From inside its working tree:

```bash
cd ~/Repos/your-project
bacio init                # bind this repo to a 4-letter prefix (e.g. YOUR)
bacio install-skill       # write .claude/skills/bacio/SKILL.md
```

Restart Claude Code in this repo so the new skill loads. That's it — you're done.

The SQLite database lives at `~/.bacio/db.sqlite`. Move it, back it up, or override with `--db <path>` per command. There's nothing else to configure.

> **Note.** `bacio init` is optional — running any mutating `bacio` command (e.g. `bacio issue add`) inside a fresh git repo auto-creates the row and allocates a prefix. `bacio status` is strictly read-only and will *not* register the repo; it reports `registered: false` when the working tree isn't bound yet. Calling `init` explicitly just lets you choose the prefix (`--prefix AUTH`) and confirms the binding.

### Not code? Make a workspace

Plenty of what you want to track doesn't produce commits — a renovation, an ops runbook, a reading list. A **workspace** is a bacio project with a 4-letter prefix, issues, documents and a kanban board, and **no folder on disk**:

```bash
bacio workspace add "Home Renovation"        # → prefix HOME
```

It shares one prefix namespace with your git repos, so `HOME-42` is as unambiguous as `YOUR-42`. The catch is that there's no working tree for bacio to detect, so `cd` buys you nothing — every command that targets a workspace needs the global `--repo` selector:

```bash
bacio --repo HOME issue add "Replace the back fence"

export BACIO_REPO=HOME                       # or set it once for the session
bacio issue list
```

`--repo` works on git repos too, and it's a lookup rather than a create: an unknown prefix errors instead of registering a new project. A workspace can't run agent dispatches (no checkout for an agent to work in) and can't be given filesystem paths (`doc add --from-path`, `doc export --to-path`); everything else behaves identically.

---

## 2. Your first session

Open Claude Code in the project and try this conversation.

### File an issue

> **you**: we've got a bug — login 500s on Safari when the password contains a `&`. can you log it?
>
> **claude**: *(calls `bacio issue add` with title, description, tag `bug`)*
> filed as `YOUR-1`.

claude doesn't ask you to confirm a flag dump — it composes a JSON payload, optionally rehearses with `--dry-run`, then commits and tells you the issue key. The audit log records the action against claude's agent identity (resolved automatically from `.bacio/agents.json`, populated by the SessionStart hook that `bacio install-agent` wires up) so you can later see who wrote what.

### Ask what's on your plate

> **you**: what's in progress?
>
> **claude**: *(calls `bacio issue list --state in_pipeline -o json`)*
> two items: `YOUR-3` (auth rewrite) and `YOUR-7` (flaky deploy test). want details on either?

Lists are read-only and return JSON. claude summarises so you don't have to skim raw output.

### Look at the board yourself

When you want to *see* the board, not chat about it:

```bash
bacio tui
```

This is the full-screen terminal board — read-first. You can move cards by hand from here, but most users find the muscle memory that sticks is to keep using claude for writes and use the TUI as a window into the state.

The desktop app and `bacio web` show two boards side by side in the nav, and it's worth knowing which is which before you go looking — that's the next section. (The TUI's Board tab is a third thing: the original state-keyed columns, `todo` / `in_review` / `done` / `cancelled`.)

### Take a closer look at one issue

> **you**: tell me about YOUR-3.
>
> **claude**: *(calls `bacio issue brief YOUR-3`)*
> here's the gist: …

`bacio issue brief` is the bulk-context call. claude pulls the issue, parent feature, comments, relations, attached PRs, and any linked design documents in one read. Good for *"catch me up"* questions.

### Other things you can ask

The flexibility comes from claude knowing the full surface, not from memorised command names:

- *"Move YOUR-3 to in review."*
- *"Tag YOUR-12 as P1 and attach it to the auth-rewrite feature."*
- *"What's blocked, and by what?"*
- *"Add a comment on YOUR-7 saying I tried clearing the cookie and it didn't help."*
- *"Show me everything claude did yesterday."* (audit log)

If a request maps to something `bacio` exposes, claude will pick it up.

---

## 3. Two boards: the agent's pipeline and your lanes

bacio shows you two boards, and they are not two views of the same thing.

- The **Agentic Pipeline** is where agents run work. Its columns are issue *states*: Backlog → In Pipeline → Shipping. The engine drives cards along it.
- The **Kanban** is your own board. Its columns are *lanes* you own — `Backlog` / `Doing` / `Waiting` / `Done` out of the box, renameable to whatever your week actually looks like.

They're orthogonal. `bacio kanban move` never changes an issue's state; `bacio issue state` never changes its lane. The rule that keeps them apart:

> **A card is on the Kanban if and only if you've put it in a lane.**

In a git repo the Kanban therefore starts **empty** — most cards there are agent work, and the Pipeline already tracks those. You opt one in when you want to hand-track it:

```bash
bacio kanban column list                       # lanes, left to right, with card counts
bacio kanban move YOUR-3 --column Doing        # put a card on the board
bacio kanban move YOUR-3 --off-board           # take it off again
```

In a **workspace** it's the other way round: there's no Pipeline (no checkout for an agent to work in, so the tab is hidden), and every new issue lands on the leftmost lane automatically. The Kanban *is* the board there.

Lanes are yours to reshape — `bacio kanban column add|rename|mv|rm`. Deleting a lane never deletes an issue; its cards just come off the board.

## 4. Notes, and the page tree

`bacio doc` holds per-project markdown pages — design notes, decisions, runbooks. Once you have a few dozen, organise them into folders:

```bash
bacio doc folder add Design
bacio doc folder add API --parent Design       # → Design/API
bacio doc mv auth-spec.md --folder Design/API
bacio doc folder list                          # the tree, one slash path per line
```

Two things to know:

- **Filenames stay unique across the whole project.** Folders are organisational only, so two pages in different folders can't share a name — and moving a page never changes its filename, its links, or its URL. Reorganise as freely as you like.
- **Deleting a folder never deletes a page.** Subfolders go with it; every page inside is re-rooted. `--dry-run` gives you both counts first.

In the desktop app and `bacio web`, the tree is the rail down the left of the Documents page. Clicking a folder opens a real page with an index of its children, and typing in the search box flattens the tree to ranked results until you clear it.

## 5. The CLI as a fallback

You don't need to drive `bacio` directly, but you can. Useful for tab-completion-friendly commands, scripts, and getting a quick read on something without opening Claude Code:

```bash
bacio status                      # repo + issue counts at a glance
bacio issue list --state todo
bacio issue show YOUR-3
bacio feature plan auth-rewrite   # topo-sorted execution plan
bacio history --since 1d          # last day's mutations
```

For the full surface, `bacio --help` and `bacio <subcommand> --help` cover everything. The exhaustive reference is `.claude/skills/bacio/SKILL.md` (the same file claude reads) — open it any time you want to know what's possible.

---

## 6. Sync across machines (when you're ready)

Single-machine `bacio` is the fast path. If you want the same board on a laptop and a desktop, or to share a board with a teammate, set up git-backed sync.

The model: `bacio sync` mirrors the SQLite DB to a checked-in folder of YAML + markdown in a separate git repo (the *sync repo*). You push and pull through normal git; conflicts are resolved last-writer-wins per record, with already-in-git winning label collisions.

### First-time setup

From inside your project repo:

```bash
bacio sync init ~/sync/your-project --remote git@github.com:you/your-project-bacio-sync.git
```

This creates the sync repo at `~/sync/your-project`, exports the project's data, commits, and pushes. It also writes a machine-local `.bacio/config.yaml` in your project so steady-state `bacio sync` knows the remote. That file is **not** committed — `.bacio/` is gitignored (`bacio init` adds the rule).

### Joining the sync repo from another machine

After cloning your project on machine 2, pass the sync repo's git URL explicitly:

```bash
cd ~/Repos/your-project
bacio sync clone --remote git@github.com:you/your-project-bacio-sync.git
```

This clones the sync repo, imports its contents into the local SQLite DB, and writes this machine's local `.bacio/config.yaml`. `--remote` is required — the config file is machine-local, so a fresh project clone has no remote to read. If the local DB already has issues for this prefix, `bacio sync clone` will refuse unless you pass `--allow-renumber`.

### Steady-state

```bash
bacio sync                 # pull → import → export → commit → push
```

Run it whenever you want to push your local writes upstream and pull anyone else's. Most users find on-demand sufficient — wire it into a cron or git hook only if you want continuous mirroring.

### Workspaces come along for free

The export is **whole-database**, not per-project: every `bacio sync` run walks every project in `~/.bacio/db.sqlite`. So the moment any git repo on the machine syncs, your workspaces' issues, documents, folders and lanes ride along into the sync repo with them.

That means a workspace has **nothing to configure** — and can't be configured. It has no working tree, so nowhere to keep a `.bacio/config.yaml`, and it can't drive a sync run of its own. `bacio sync init` / `clone` operate on the repo you're standing in. Set sync up on any one git repo and you're done.

Folders and lanes land as new sibling records under each project (`repos/<PREFIX>/folders/…`, `repos/<PREFIX>/kanban/…`, plus a `workspace.yaml` marker), with membership and order recorded on the container — so the tree shape and the order of cards in a lane survive a round trip.

> **An older bacio on another machine keeps syncing fine.** It can pull, import, export and push the same sync repo without error; it just won't see workspaces, folders or lanes. The files it does read (`repo.yaml`, `issue.yaml`, `doc.yaml`) are byte-for-byte unchanged, and the new sibling directories are invisible to it and left untouched. Upgrade that machine whenever you like and everything appears.

For collisions, conflict semantics, redirect chains, and the verify-and-inspect commands, see the `Git-backed sync` section in `.claude/skills/bacio/SKILL.md`.

---

## What to read next

- **`.claude/skills/bacio/SKILL.md`** — exhaustive reference for AI agents. Also the right place to look if you want to know exactly what `bacio` exposes.
- **<https://bacio.io/docs/>** — the full user docs, including the concept pages for [workspaces](https://bacio.io/docs/concepts/workspaces), [the two boards](https://bacio.io/docs/concepts/kanban-and-pipeline), and [document folders](https://bacio.io/docs/concepts/document-folders).
- **`docs/agent-cli-principles.md`** — the design rules `bacio` follows so agents can drive it reliably.
- **`bacio --help`** — every CLI command with one-line summaries.

If something in this guide is wrong or unclear, file an issue — *"hey claude, file an issue against bacio: …"*.
