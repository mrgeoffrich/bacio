---
title: Troubleshooting
description: Common errors and gotchas — what they mean, how to fix them, and the patterns that catch real users out.
---

# Troubleshooting

A short, structured list of things that catch real users out — drawn from bacio's `## Gotchas` in `SKILL.md` and from common stumbles when grounding the docs.

## Errors with obvious fixes

### `bacio: not inside a git repository`

Most `bacio` commands resolve the current repo from your working directory. `cd` into a git working tree first.

If you genuinely want to run something that doesn't need a repo — `bacio repo list`, `bacio --version`, `bacio schema list` — those work anywhere.

### `bacio: unknown field "..." in JSON payload`

You misspelled or invented a field in `--json`. The JSON decoder is **strict** — typos surface as errors, not as silent no-ops. Run:

```bash
bacio schema show <command-name>
```

…to see the exact accepted shape. `<command-name>` is the dotted form: `issue.add`, `feature.edit`, `doc.link`, etc.

### `bacio: title is required`

A required field on a mutating call is missing or empty. Check the schema for which fields are required:

```bash
bacio schema show issue.add | jq .required
```

### `unauthorized` from `bacio api` / `bacio web`

You started the server with `--token T` (or `BACIO_API_TOKEN=T`), but your request didn't include `Authorization: Bearer T`. With the CLI client:

```bash
export BACIO_API_TOKEN=T
bacio --remote http://127.0.0.1:5320 issue list
```

### `payload_too_large`

You posted a body bigger than 4 MiB. Probably a paste accident — bodies are length-capped at 1 MiB inside bacio itself; the HTTP body cap is just defence-in-depth. Trim and retry.

### `bacio: invalid prefix`

Prefixes are exactly 4 alphanumeric characters, no whitespace. `bacio init --prefix A_UTH` is rejected; `bacio init --prefix AUTH` works.

## Behaviour that surprises people

### "My agent's mutations are all attributed to `user`, not its agent name"

The audit log resolves an agent's actor by walking up the process tree to the `claude` parent PID and looking up `.bacio/agents.json`. If the entry is missing — most often because `bacio install-agent` wasn't run in the repo, or the SessionStart hook didn't fire — `actor()` falls back to the literal `"user"`. Re-run `bacio install-agent` in the repo and start a fresh agent session.

### "I deleted MINI-3 and the next issue is MINI-5"

Issue numbers don't repeat. Deleting `MINI-3` does not free up the number — the next issue is still whatever `next_issue_number` was when you created it. Intentional: external references (commit messages, PRs) keep pointing at something real.

### "My agent kept calling `bacio issue show` ten times in a row"

It's missed the [`bacio issue brief`](/reference/cli/issue) convention. `brief` returns the issue + parent feature + relations + PRs + comments + linked-doc bodies in one read. Ask the agent to use it.

### "`bacio init --with-skill` doesn't work"

That flag doesn't exist (the website's old quickstart was wrong). `bacio init` and `bacio install-skill` are two separate commands.

```bash
bacio init                # bind prefix
bacio install-skill       # install the canonical SKILL.md
```

### "Two clones of the same project ended up with different prefixes"

The auto-allocator picks from the repo basename and skips taken prefixes. If two repos with the same basename register on the same machine, the second gets a numeric suffix. Solution: pass `--prefix` explicitly to align them, or rename one of the clones on disk.

### "`bacio sync` won't push"

Non-fast-forward — someone else pushed first. `bacio sync` automatically pulls, re-imports/re-exports, and retries once. If it still fails, run the steps manually:

```bash
cd ~/sync/your-project
git pull --rebase
cd ~/code/your-project
bacio sync                    # re-runs the full pipeline
```

### "I edited the sync repo YAML directly and now bacio is confused"

Don't edit the YAML directly — bacio's the writer. Edits made through your editor bypass validators, don't record audit rows, and may be overwritten on the next `bacio sync`. If you've already done it:

```bash
cd ~/sync/your-project
bacio sync verify           # see what's broken
```

…and revert the manual edit in git.

## State machine confusions

### "`bacio issue state MINI-3 In Progress` failed unexpectedly"

The state parser is permissive: it lower-cases the input and accepts `-`, `_`, or space as separators, so `in_progress`, `in-progress`, `IN PROGRESS`, and `In Progress` all parse to the same state. If something *does* fail, the value isn't a state at all — check `bacio schema show issue.state` for the canonical list (`todo`, `in_progress`, `needs_action`, `in_review`, `done`, `cancelled`).

### "`bacio issue next` returned `{"issue": null}` — is something broken?"

No. That's the success-with-nothing-claimable shape. Exit code is `0`. Callers should poll / retry, not treat it as an error. See [`bacio issue`](/reference/cli/issue#next-peek-the-atomic-claim-pattern).

### "An agent crashed mid-claim and now MINI-3 is stuck in `in_progress` with no progress"

`bacio issue next` is atomic but the workload after isn't. Clear the stale claim manually:

```bash
bacio issue state MINI-3 todo
bacio issue unassign MINI-3
```

## Forgotten flags

### `--as <name>` on `bacio comment add`

Required. bacio has no auth — you name yourself. JSON path uses `"author"` instead.

### `--confirm <PREFIX>` on `bacio repo rm`

Required. The command refuses to run without it, printing the cascade preview instead. Rehearse with `--dry-run` first.

## When in doubt

- Pass `-o json` and read the structured error. The field `code` is stable; `error` is the human message.
- `bacio --help` and `bacio <subcommand> --help` cover every flag.
- `bacio schema show <name>` is authoritative for JSON shapes — never guess from documentation.

## See also

- **[FAQ](/faq)** — common questions about bacio's design and philosophy.
- **[Exit codes](/reference/exit-codes)** — the `0` / `1` model plus the `issue next` edge case.
- **[JSON payloads](/reference/json-payloads)** — the strict-decode contract.
