---
title: Install bacio
description: Install bacio on macOS or Linux with Homebrew, build from source with Go, verify the install, and uninstall cleanly.
---

# Install

## Homebrew (macOS and Linux)

The recommended path — pre-built binaries for both platforms:

```bash
brew tap mrgeoffrich/bacio
brew install bacio
```

Upgrade later with:

```bash
brew upgrade bacio
```

After upgrading, re-run `bacio install-skill` in any repo you use bacio in so the bundled agent skill picks up doc updates.

## From source (any platform)

bacio is a pure-Go binary — no CGO, no native SQLite dependency:

```bash
go install github.com/mrgeoffrich/bacio/cmd/bacio@latest
```

Make sure `$(go env GOPATH)/bin` is on your `PATH`.

## Verify

```bash
bacio --version
```

Anything that prints a version is fine. If the command isn't found, your shell's `PATH` doesn't include the install location — `brew` puts the binary at `/opt/homebrew/bin/bacio` (Apple Silicon) or `/usr/local/bin/bacio` (Intel macOS, Linux); `go install` uses `$(go env GOPATH)/bin`.

## What gets installed

Just one binary — `bacio`. Everything else (the SQLite database, per-repo skill files) is created on first use:

- The **database** is created at `~/.bacio/db.sqlite` the first time you run any `bacio` command that needs it. Override with `--db <path>`.
- The **canonical agent skill** is installed per-repo by `bacio install-skill`, which drops `SKILL.md` into `<repo>/.claude/skills/bacio/`.
- The **sync config** is created per-repo by `bacio sync init` at `<repo>/.bacio/config.yaml` — only if and when you opt into sync.

## Uninstall

```bash
brew uninstall bacio                          # if installed via brew
rm "$(go env GOPATH)/bin/bacio"               # if installed via go install
```

To remove your data as well:

```bash
rm -rf ~/.bacio                               # the database
rm -rf <repo>/.claude/skills/bacio            # the agent skill, per repo
rm -rf <repo>/.bacio                          # sync config, per repo
```

::: tip Heads up
`~/.bacio/db.sqlite` is the only place your kanban data lives. **Back it up** the same way you back up anything else on your laptop. If you've enabled [git-backed sync](/guides/sync-across-machines), the sync repo is a second copy.
:::
