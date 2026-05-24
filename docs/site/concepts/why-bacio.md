---
title: Why bacio
description: The design bet — local-first, single-user, agent-driven kanban. What bacio's for, what it's not, and how it compares to the alternatives.
---

# Why bacio

## The one-line version

bacio is a kanban board for a single developer (or a tiny team) who already has Claude Code or Codex in their loop. **The agent does the typing; you do the reading.**

## The bets

1. **You already have an agent in the loop.** If you talk to Claude Code or Codex while you code, asking it to *"file an issue for this bug"* is faster than opening a browser tab. bacio is built so an agent can drive the CLI reliably — every mutation accepts JSON, every payload schema is published at runtime, every destructive call has `--dry-run`. The agent has a skill (one markdown file) that teaches it the contract; you almost never type a `bacio` command yourself.
2. **Your laptop is the source of truth.** Your kanban is a single SQLite file at `~/.bacio/db.sqlite`. No cloud, no signup, no API key. If you want a second machine to see it, [opt into git-backed sync](/guides/sync-across-machines); the sync repo is plain YAML + markdown you can read in your editor.
3. **Read in the terminal.** A full-screen TUI with six tabs (Board, Features, Documents, Agents, History, Settings) means you never leave the place you already are. Cards are kanban-style, columns are issue states, the audit log shows who did what.

## What it's a good fit for

- **Solo work** where you want a real tracker but don't want the overhead of Linear / Jira / GitHub Issues.
- **Pre-public projects** — local-first means nothing leaks until you say so.
- **Agent-heavy workflows** where the CLI design (JSON in, JSON out, schemas at runtime) makes the LLM loop reliable.
- **Multi-machine solo** — a laptop + a desktop with `bacio sync` mirroring through a private git repo.

## What it's not a good fit for

- **Multi-person teams that need real-time collaboration.** sync is git-based, last-writer-wins per record. Two people editing the same issue at the same minute will fight git, not bacio.
- **Public bug trackers** where end-users need a web UI to file issues. `bacio api` gives you a REST surface you could build a UI on top of, but bacio isn't shipping one.
- **People who don't use an LLM coding agent.** You *can* drive bacio purely from the CLI (see [Drive it without an agent](/guides/drive-it-without-an-agent)) — the design just optimises for the case where you don't have to.

## Compared to…

| Tool | bacio's bet |
|---|---|
| **Linear / Jira** | Cloud-first, multi-user, opinionated workflows. bacio swaps that for local-first, single-user, agent-first. |
| **GitHub Issues** | Public-by-default, repo-bound, no offline. bacio is private-by-default, multi-repo, and entirely offline until you opt into sync. |
| **Trello** | Web-first, visual, no API contract for agents. bacio's TUI is the read surface; the JSON CLI is the write surface. |
| **A markdown TODO file** | Lovable, but no audit log, no graph, no states, no agent-friendly schema. bacio adds those without leaving the terminal. |

## The agent angle

The reason bacio exists, in one paragraph: an LLM agent can already file an issue in any tracker if you give it API credentials and a long-enough prompt. But the trackers weren't designed for agents — schemas are undocumented or buried in OpenAPI specs, error messages are vague, and there's no `--dry-run`. bacio's CLI was rebuilt around six rules so the agent flow is *reliable* — discover the shape with `bacio schema show <name>`, compose JSON, rehearse with `--dry-run`, run for real (audit attribution is automatic via `.bacio/agents.json`), then query lean. See [How agents drive bacio](/concepts/how-agents-drive-bacio) for the rules.

## See also

- **[Quickstart](/)** — install, init, open the TUI in about a minute.
- **[Your first board](/getting-started/first-board)** — the ten-minute walk-through.
- **[Data model](/concepts/data-model)** — how issues, features, comments, links, PRs, tags, and documents fit together.
- **[Local-first and audit log](/concepts/local-first-and-audit)** — where the data lives, what the audit log records.
