---
title: Configuration
description: Where bacio stores its database, what lives in .bacio/config.yaml, and the environment variables it honours.
---

# Configuration

## The database

Bacio's SQLite database lives at `~/.bacio/db.sqlite` by default — **global, not per-repo**. Every repo bacio knows about is a row in that DB, keyed by its 4-letter prefix.

Override per-command with `--db <path>`. Useful for tests and isolated experiments.

## Per-project `.bacio/config.yaml`

`bacio sync init` and `bacio sync clone` write `.bacio/config.yaml` inside your project repo so steady-state `bacio sync` knows which sync remote to use.

**This file is machine-local — do not commit it.** Add `.bacio/` to your repo's `.gitignore` (`bacio init` does this for you automatically). The `.bacio/` directory also holds the per-machine agent identity (`.bacio/agent`); none of it is meant to be shared via git. Each machine writes its own `config.yaml` when you run `bacio sync init` or `bacio sync clone --remote <url>`.

Minimal contents:

```yaml
sync:
  remote: git@github.com:you/your-project-bacio-sync.git
```

The file is only created if you've enabled [git-backed sync](/guides/sync-across-machines). Without it, bacio's behaviour is unchanged from a fresh install.

## Environment variables

| Variable | Effect |
|---|---|
| `BACIO_REPO` | Project prefix to operate on; same as `--repo`. Short-circuits the walk-up-to-`.git` detection entirely. The only way to reach a [workspace](/concepts/workspaces), which has no working tree to detect. Case-insensitive; a lookup, never a create. |
| `BACIO_REMOTE` | URL of a `bacio api` server; same as `--remote`. When set, `bacio` calls go through HTTP instead of the local DB. |
| `BACIO_API_TOKEN` | Bearer token for the remote API; same as `--token`. |

## Defaults that matter

- **History retention:** 60 days. Pruned on every DB open.
- **Auto-archive (BACI-162):** the hourly archive sweep hides done / cancelled issues whose `terminal_at` is older than the configured retention window. Default `7` days. Configurable via `bacio settings archive` (CLI) or the desktop / web Settings panel (toggle + numeric input). When the boolean `archive.auto_enabled` is set to `false` the issue pass is skipped entirely; the feature + linked-doc cascade passes still run, so a manually archived issue still cascades.
- **Output format:** `text`. Override per call with `-o json` for agent / script use.
- **Actor:** resolved automatically. Agent-driven calls (with `bacio install-agent` set up) attribute to the agent's identity via `.bacio/agents.json`; all other calls stamp the literal `"user"` placeholder until real auth lands.
