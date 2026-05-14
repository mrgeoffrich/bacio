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
| `BACIO_REMOTE` | URL of a `bacio api` server; same as `--remote`. When set, `bacio` calls go through HTTP instead of the local DB. |
| `BACIO_API_TOKEN` | Bearer token for the remote API; same as `--token`. |

## Defaults that matter

- **History retention:** 60 days. Pruned on every DB open.
- **Output format:** `text`. Override per call with `-o json` for agent / script use.
- **Actor (`--user`):** OS user. Agents must pass this explicitly so audits attribute work correctly.
