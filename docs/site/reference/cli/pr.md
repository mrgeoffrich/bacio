---
title: bacio pr
description: Attach pull-request URLs to issues so the board sees what's shipping.
---

# `bacio pr`

Track pull requests against issues. bacio doesn't talk to GitHub — it just stores the URL — but `bacio issue brief` surfaces attached PRs so an agent can summarise *"what's shipping for MINI-42"*.

URLs are stored **verbatim, no normalisation**. `https://github.com/owner/repo/pull/374` and `…/pull/374/` (with trailing slash) are distinct attachments; `detach` is an exact match.

## Subcommands

| Subcommand | What it does |
|---|---|
| `bacio pr attach <KEY> <URL>` | Attach a PR URL. Validates `http`/`https` + host. |
| `bacio pr detach <KEY> <URL>` | Remove a PR URL. Exact match. |
| `bacio pr list <KEY>` | List PRs on an issue. One URL per line (text) or JSON array. |

## Worked examples

```bash
bacio pr attach MINI-42 https://github.com/owner/repo/pull/7
bacio pr list MINI-42
bacio pr list MINI-42 -o json

bacio pr detach MINI-42 https://github.com/owner/repo/pull/7
```

## Validation

- Must be `http://` or `https://`.
- Length cap: 2 KiB (`ValidatePRURLStrict`).
- No whitespace, no control characters.
- Trailing slashes, query strings, and fragments are preserved as-is.

## See also

- **[`bacio issue brief`](/reference/cli/issue)** — the bulk-context call includes attached PRs.
- **[TUI Board](/reference/tui/board)** — the card overlay's Attachments pane shows PR URLs.
