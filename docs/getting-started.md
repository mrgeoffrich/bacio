# Getting started with `bacio`

`bacio` is a kanban for a single developer (or a small team), built so Claude Code can drive it. The standard loop:

1. you ask Claude Code something — *"file an issue for the Safari bug"*, *"what's on my plate?"*, *"plan out the auth rewrite"*.
2. claude reads the relevant state from `bacio`, writes if needed, tells you what it did.
3. you read the result in your editor, in `bacio tui`, or on the CLI.

You **don't have to memorise bacio's commands**. claude has a copy of the canonical skill (installed once per repo) and knows the surface. You can drop into `bacio --help` if you want — but the design assumes you mostly won't.

This guide walks you through:

- Setting up `bacio` in a repo (no sync — quickest path to value).
- A first session driving `bacio` through Claude Code.
- Optional: install sample skills for common flows.
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

> **Note.** `bacio init` is optional — running any `bacio` command inside a fresh git repo auto-creates the row and allocates a prefix. Calling `init` explicitly just lets you choose the prefix (`--prefix AUTH`) and confirms the binding.

---

## 2. Your first session

Open Claude Code in the project and try this conversation.

### File an issue

> **you**: we've got a bug — login 500s on Safari when the password contains a `&`. can you log it?
>
> **claude**: *(calls `bacio issue add` with title, description, tag `bug`)*
> filed as `YOUR-1`.

claude doesn't ask you to confirm a flag dump — it composes a JSON payload, optionally rehearses with `--dry-run`, then commits and tells you the issue key. The audit log records the action against `--user claude` (or whatever name your agent identifies as) so you can later see who wrote what.

### Ask what's on your plate

> **you**: what's in progress?
>
> **claude**: *(calls `bacio issue list --state in_progress -o json`)*
> two items: `YOUR-3` (auth rewrite) and `YOUR-7` (flaky deploy test). want details on either?

Lists are read-only and return JSON. claude summarises so you don't have to skim raw output.

### Look at the board yourself

When you want to *see* the board, not chat about it:

```bash
bacio tui
```

This is the full-screen kanban — read-first. You can move cards by hand from here, but most users find the muscle memory that sticks is to keep using claude for writes and use the TUI as a window into the state.

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

## 3. Sample skills (optional)

The canonical `bacio` skill teaches claude **how** to call the CLI. The sample skills bundled with `bacio` teach claude **when** and **why** for common flows — they're shortcuts you can drop into your repo when the bare canonical skill isn't picking up your phrasing.

Install all four into the current repo:

```bash
bacio install-sample-skills
```

Or install only the ones you want:

```bash
bacio install-sample-skills triage stand-up
```

Each lands at `<repo-root>/.claude/skills/<name>/SKILL.md` and is overwritten on every run, so re-running picks up updates from a newer build of `bacio`. The file is checked into your repo alongside everything else, so the rest of your team gets the same shortcuts.

Restart Claude Code so the new skills load, then:

| Skill | Trigger phrase | What it does |
|---|---|---|
| `file-issue` | *"file an issue"*, *"log this"*, *"add a ticket"* | Takes a one-line description, writes a clean title, body, and tags; attaches to a feature if obvious. |
| `triage` | *"triage the backlog"*, *"groom the board"*, *"what should I look at"* | Sweeps open issues in `todo`, proposes tags / priorities / feature groupings — asks before writing. |
| `stand-up` | *"stand-up"*, *"daily summary"*, *"what changed yesterday"* | Pure-read summary of `in_progress`, blocked items, and the last 24h of audit history. |
| `plan-feature` | *"plan the auth rewrite"*, *"break this down"* | Creates a feature, child issues, blocks/blocked-by edges, and (optionally) a linked design doc. |

These are templates — open them, tweak the trigger phrases or the procedural steps to match your workflow, and commit the changes. Re-running `bacio install-sample-skills` will overwrite your edits with the bundled version, so once you've customised, leave the install command alone.

---

## 4. The CLI as a fallback

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

## 5. Sync across machines (when you're ready)

Single-machine `bacio` is the fast path. If you want the same board on a laptop and a desktop, or to share a board with a teammate, set up git-backed sync.

The model: `bacio sync` mirrors the SQLite DB to a checked-in folder of YAML + markdown in a separate git repo (the *sync repo*). You push and pull through normal git; conflicts are resolved last-writer-wins per record, with already-in-git winning label collisions.

### First-time setup

From inside your project repo:

```bash
bacio sync init ~/sync/your-project --remote git@github.com:you/your-project-bacio-sync.git
```

This creates the sync repo at `~/sync/your-project`, exports the project's data, commits, and pushes. It also writes `.bacio/config.yaml` in your project (checked in) so other clones know which sync remote to use.

### Joining the sync repo from another machine

After cloning your project on machine 2:

```bash
cd ~/Repos/your-project
bacio sync clone           # uses .bacio/config.yaml to find the remote
```

This clones the sync repo and imports its contents into the local SQLite DB. If the local DB already has issues for this prefix, `bacio sync clone` will refuse unless you pass `--allow-renumber`.

### Steady-state

```bash
bacio sync                 # pull → import → export → commit → push
```

Run it whenever you want to push your local writes upstream and pull anyone else's. Most users find on-demand sufficient — wire it into a cron or git hook only if you want continuous mirroring.

For collisions, conflict semantics, redirect chains, and the verify-and-inspect commands, see the `Git-backed sync` section in `.claude/skills/bacio/SKILL.md`.

---

## What to read next

- **`.claude/skills/bacio/SKILL.md`** — exhaustive reference for AI agents. Also the right place to look if you want to know exactly what `bacio` exposes.
- **`docs/agent-cli-principles.md`** — the design rules `bacio` follows so agents can drive it reliably.
- **`bacio --help`** — every CLI command with one-line summaries.

If something in this guide is wrong or unclear, file an issue — *"hey claude, file an issue against bacio: …"*.
