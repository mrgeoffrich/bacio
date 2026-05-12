---
title: Quickstart
description: Install bacio, init a repo, open the TUI.
---

# Quickstart, in about a minute

Install bacio, run `init` in a repo, open the TUI, then let your agent in. The four steps below are the whole loop — the sidebar covers everything beyond.

## 1. Install

On macOS, install with Homebrew:

```bash
$ brew tap mrgeoffrich/bacio
$ brew install bacio
```

Verify the install:

```bash
$ bacio --version
bacio 1.0.3
```

## 2. Initialize a repo

Inside any git repo, run `bacio init`. It registers the repo in bacio's database (at `~/.bacio/db.sqlite`) and allocates a 4-letter prefix for issue keys.

```bash
$ cd ~/code/my-project
$ bacio init
Prefix:    MYPR
Name:      my-project
Path:      /Users/you/code/my-project
NextIssue: MYPR-1
Created:   2026-05-12 14:33 AEST
```

`init` is **optional** — any `bacio` command inside a fresh git working tree auto-registers the repo and allocates a prefix from its name. Run `init` explicitly when you want to pick the prefix yourself (`bacio init --prefix AUTH`). See [`bacio init`](/reference/cli/init) for the details.

## 3. Open the TUI

Open the full-screen kanban for the current repo:

```bash
$ bacio tui
```

Four tabs, all keyboard:

- **Board** — kanban view, one column per state.
- **Features** — group issues into shipping units.
- **Docs** — markdown notes that live next to the work.
- **History** — audit log of every mutation, with actor and op.

Switch tabs with `1`–`4`. The footer always shows the keybindings for whatever view you're in — there's no hidden help screen. `q` (or `ctrl-c`) quits; inside an overlay `esc` closes the overlay first. See the [keybindings cheat sheet](/reference/tui/keybindings) for the full surface.

## 4. Let your agent in

bacio ships with a skill — one markdown file that teaches Claude Code the JSON CLI. Install it once per repo:

```bash
$ bacio install-skill
installed bacio skill (12345 bytes) at /Users/you/code/my-project/.claude/skills/bacio/SKILL.md
```

Restart Claude Code in this repo so the new skill loads. From here, your agent can file issues, link features, write doc pages, and read history — all without leaving the repo. Re-run `install-skill` after `brew upgrade bacio` to pick up doc updates.

Looking for higher-level workflows (file-issue, triage, stand-up, plan-feature)? See [`bacio install-sample-skills`](/reference/cli/install-sample-skills).
