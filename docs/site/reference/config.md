---
title: Configuration
description: Where bacio stores its database, what lives in .bacio/config.yaml, and the environment variables it honours.
---

# Configuration

## The database

Bacio's SQLite database lives at `~/.bacio/db.sqlite` by default — **global, not per-repo**. Every repo bacio knows about is a row in that DB, keyed by its 4-letter prefix.

Override per-command with `--db <path>`. Useful for tests and isolated experiments.

## Per-project `.bacio/config.yaml`

When you run `bacio sync init`, bacio writes `.bacio/config.yaml` inside your project repo. **Check this in** — other clones (and your other machines) use it to find the sync remote.

Minimal contents:

```yaml
sync:
  remote: git@github.com:you/your-project-bacio-sync.git
```

The file is only required if you've enabled [git-backed sync](/guides/sync-across-machines). Without it, bacio's behaviour is unchanged from a fresh install.

## Environment variables

| Variable | Effect |
|---|---|
| `BACIO_REMOTE` | URL of a `bacio api` server; same as `--remote`. When set, `bacio` calls go through HTTP instead of the local DB. |
| `BACIO_API_TOKEN` | Bearer token for the remote API; same as `--token`. |

## Defaults that matter

- **History retention:** 60 days. Pruned on every DB open.
- **Output format:** `text`. Override per call with `-o json` for agent / script use.
- **Actor (`--user`):** OS user. Agents must pass this explicitly so audits attribute work correctly.
