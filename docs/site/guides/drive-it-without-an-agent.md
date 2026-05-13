---
title: Drive bacio without an agent
description: Use bacio purely from your own keyboard — every workflow, plain CLI, no Claude required.
---

# Drive it without an agent

bacio is built so an AI agent *can* drive it reliably. But you don't have to use one. The CLI and TUI are first-class for humans, and every workflow below works without ever opening Claude Code.

## Filing an issue

```bash
bacio issue add "Login 500s on Safari with '&' in password"
```

For longer descriptions, pass `--description-file path.md` or `--description -` (stdin). Multi-line descriptions can't be typed inline — bacio is explicit about that to keep the contract clean.

```bash
bacio issue add "Auth rewrite design" \
  --description-file /tmp/auth-design.md \
  --feature auth-rewrite \
  --tag P1 --tag design
```

## Reading the board

```bash
bacio status                              # repo + counts by state
bacio issue list                          # all issues
bacio issue list --state in_progress
bacio issue list --tag bug --state todo
bacio issue show MYPR-3                   # detail for one
```

For a richer single-issue view (comments + relations + PRs + linked doc bodies):

```bash
bacio issue brief MYPR-3
```

For the kanban view, open the TUI:

```bash
bacio tui
```

## Moving state

```bash
bacio issue state MYPR-3 in_progress
bacio issue state MYPR-3 in_review
bacio issue state MYPR-3 done
```

State parser accepts `in-progress`, `in progress`, or `in_progress` — all equivalent.

## Features

```bash
bacio feature add "Auth rewrite"             # creates slug auth-rewrite
bacio feature add "Auth rewrite" --slug auth # override the slug

bacio feature list
bacio feature show auth-rewrite

bacio feature edit auth-rewrite --description-file /tmp/rewrite.md
```

To attach an existing issue to a feature, edit it:

```bash
bacio issue edit MYPR-3 --feature auth-rewrite
```

## Relations between issues

```bash
bacio link MYPR-7 blocks MYPR-5
bacio link MYPR-7 relates-to MYPR-12       # 'relates_to' is also accepted on input
bacio unlink MYPR-7 MYPR-5
```

There's no `blocked-by` type to create — relations are one-directional. To say "MYPR-5 is blocked by MYPR-7", create `bacio link MYPR-7 blocks MYPR-5`; the reverse view shows up automatically wherever bacio renders relations.

Relations show up in `bacio issue brief`, in the TUI card overlay, and in JSON output of `bacio issue show -o json`.

## Comments

```bash
bacio comment add MYPR-3 --as Geoff \
  --body "Tried clearing the cookie, didn't help."
bacio comment list MYPR-3
```

`--as <name>` is required — bacio has no auth, so you name yourself.

## PRs and tags

```bash
bacio pr attach MYPR-3 https://github.com/you/repo/pull/42
bacio pr list MYPR-3
bacio pr detach MYPR-3 https://github.com/you/repo/pull/42

bacio tag add MYPR-3 P1 backend
bacio tag rm MYPR-3 backend
```

## Documents

Markdown notes that live in the DB alongside your issues, linkable to one or many issues and features.

```bash
bacio doc add design.md --type architecture --from-path /tmp/design.md
bacio doc list
bacio doc show design.md
bacio doc link design.md auth-rewrite                # link to a feature
bacio doc link design.md MYPR-3                      # link to an issue
bacio doc upsert design.md --from-path /tmp/design.md  # idempotent re-import from disk
bacio doc edit design.md --content-file /tmp/design.md # or update body only on an existing doc
```

`--from-path` (on `doc add` / `doc upsert`) reads the body, derives the filename, and infers the type from the path. `doc edit` doesn't accept `--from-path` — use `--content-file` for the body or `doc upsert` for the full idempotent re-import. `bacio doc export design.md` materialises a doc back to disk.

## History

```bash
bacio history                              # most recent
bacio history --since 1d
bacio history --user-filter Geoff          # filter by actor
bacio history --op issue.create
bacio history --kind issue --since 1w      # filter by entity kind
```

`bacio history` has no `--target` filter — narrow by kind, op, actor, or time range, or pipe `-o json` through `jq` to filter on `target_label`. Note that the actor-filter flag is `--user-filter`; the persistent `--user` records who's running the call but doesn't filter the output.

Or open the TUI and switch to tab `4` History.

## Across all your repos

Two commands accept `--all-repos`:

```bash
bacio issue list --all-repos --state in_progress
bacio history --all-repos --since 1d
```

Useful when you've got multiple projects bound in the same global DB.

## Why use the agent then?

You don't *have* to. But the agent's value is:

- It composes JSON payloads so you don't think about flag combinations.
- It uses `bacio issue brief` for context instead of N separate reads.
- It catches typos through the strict JSON decode and `--dry-run` before they're committed.
- You can ask in English instead of remembering verbs.

The CLI is here when you want it; the agent is here when you want that. See [Work with Claude Code](/guides/work-with-claude-code) for the agent-driven flow.

## See also

- **[CLI reference](/reference/cli/)** — every command, every flag.
- **[Filter and search](/guides/filter-and-search)** — finding things across a busy board.
- **[`bacio issue brief`](/reference/cli/issue)** — bulk-context lookup for a single issue.
