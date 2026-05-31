# Per-mode close-out expectations

Quick reference for what each `bacio-<mode>-worker` is supposed to do at
close-out. Use this together with the full prompt at `prompts/agents/<mode>.md`
when judging whether a transcript's behaviour matches its brief.

## Universal expectations (every mode, every transcript)

These come from `prompts/agents/_preamble.md` and `_postamble.md` and apply
regardless of mode:

- **Worktree safety check, FIRST.** Before claiming, before any edit:
  `git rev-parse --show-toplevel` (path must contain `.claude/worktrees`)
  and `git branch --show-current` (must not be `main`/`master`). Run via
  Bash with `git -C <root> …` or after `cd`. Abort on failure.
- **First TaskCreate is "Establish working directory".** The description
  must record the worktree-root prefix verbatim and the rule that every
  `Read`/`Edit`/`Write` `file_path` must start with that prefix.
- **`bacio worktree init` once at startup** (claims an API port for this
  run). Then every `bacio` call from outside that worktree root must pass
  `--env <worktree>/environment-config.yaml`.
- **`bacio worktree rm <path> --confirm <slug>` at close** (drops the env;
  Claude Code removes the git worktree itself).
- **`mcp__bacio__reply` with the dispatch_id from the Task prompt** as the
  final action — acks the dispatch and unblocks the supervisor.
- **No new issues / features / external tickets without first asking via
  `mcp__bacio__ask_user_question`.** This includes follow-ups, adjacent
  bugs, deferred scope. Filing silently is a finding.
- **No direct `bacio issue state <key> <state>` mid-run.** State is owned
  by the claim/release pair. The only legitimate direct call is the
  `needs_action → in_progress` hop after a user reply.

## Per-mode close-out

| Mode         | Claim prompt    | Final tag      | `agent release --state` |
| ------------ | --------------- | -------------- | ----------------------- |
| `plan`       | `"plan"`        | `planned`      | `todo`                  |
| `design`     | `"design"`      | `design`       | `todo`                  |
| `implement`  | `"implement"`   | `implemented`  | `in_review`             |
| `review`     | `"review"`      | `reviewed`     | `in_review`             |
| `fix_review` | `"fix-review"`  | `fixed`        | `in_review`             |
| `ship`       | `"ship"`        | (none)         | `done`                  |

(Source of truth: each `prompts/agents/<mode>.md` — `Close out` section.)

Other modes exist under `prompts/agents/` and may show up in a dispatch
chain — `plan_large` (the bulk-planning variant of `plan`), `research`,
and `scope`. This table doesn't enumerate their close-out contracts; for
any mode the table doesn't list, **read `prompts/agents/<mode>.md`
directly** — the brief is the authority. The digest's `dispatch.mode`
(off the capture row) tells you which brief to open.

Note on pipeline-engine cards: a card driven by the controller job-engine
may have the *engine* add the close-out tag (and advance the issue state)
rather than the worker. So an absent `tag_calls` entry or an unchanged
state in the digest is not automatically a finding for an engine-driven
run — cross-check whether the mode's brief still tells the worker to tag,
or whether that's the engine's job now.

### `plan` — extra contract

- Produces one markdown plan document and links it to the issue as
  `--type plan`. Wrong type defeats `bacio issue brief` inlining.
- **Never links the plan to the parent feature.** Feature links fan the
  doc out onto every sibling issue's brief.
- Plan doc body should keep file references repo-relative (no absolute
  paths, no worktree-specific paths) — the implement worker reads the
  plan from a different worktree.

### `design` — extra contract

- Produces a design doc at `docs/designs/<issue-id>-<slug>.md` containing
  up to **four** distinct options (target four, may collapse to three or
  two with a stated reason) and a committed recommendation. "No strong
  preference" is invalid output.
- Sibling `.html` wireframes for every UI option; backend-only options
  skip them.
- `bacio doc upsert --type designs` then `bacio doc link ... <issue_id>`
  for each artefact. Always link to the issue, never the feature.
- Posts one summary comment on the issue.

### `implement` — extra contract

- One commit per plan step (rough rule). Mirrors the plan's
  `## Implementation steps` in the PR body.
- Smoke test before opening the PR (`./build.sh` + `go vet` + `go test`,
  plus UI smoke via `bacio web --no-open` + playwright-cli if frontend
  changed).
- **Posts a handoff** — feature comment if the issue belongs to a
  feature, otherwise an issue comment. Three buckets: Files of context /
  Deviations from plan / Work not done.
- Opens a PR and links it to the issue.

### `review` — extra contract

- Records findings as **one summary comment** (`bacio comment add`) with
  `## Blocker` / `## Non-blocker` / `## Nit` headings and
  `file_path:line_number` citations.
- **No code changes.** Edits and commits in a review-mode transcript are
  a finding — review records; fix-review fixes.
- Reads brief, walks the diff, runs the code itself. Plan + handoff
  before diff.

### `fix_review` — extra contract

- Pushes fixes to the **same PR branch** the implement worker opened.
  No new PR.
- Addresses every medium/high/critical finding from the review comment.
- Posts a comment summarising what was done.

### `ship` — extra contract

- Merges the PR (usually `gh pr merge --squash --auto`). No `implement`,
  no `review` — it's the merge step.
- Does **not** add a tag at close-out (it's the one mode without one).

## Things that count as findings

When scoring a transcript, raise findings against:

- **Behaviour mismatch with the mode's brief** — wrong final state, missing
  tag, skipped claim, no `mcp__bacio__reply`, no `worktree init`/`rm`,
  direct `issue state` mid-run.
- **Worktree confinement violations** — `Edit`/`Write` with a `file_path`
  that doesn't begin with the worker's worktree root (the digest flags
  these as `edits.outside_worktree`), or hook denies visible in tool
  results (`hook_denies`). Note: a worker that *tried* the wrong path and
  got denied by the PreToolUse hook, then retried inside the worktree, is
  healthier than one that never tried — but the deny is still worth
  recording.
- **Wrong DB / wrong env** — any `--db` override pointing at someone
  else's DB (`db_overrides`). The proxy source has no per-call `cwd`, so
  the old "bacio call from outside the worktree without `--env`" check
  can't be reconstructed; a `--env`/`--db` override is still visible in
  the command string and is the signal to grade on.
- **Inefficiency that should have been mechanical** — repeated identical
  bash commands (retry loop on a stale assumption), long sequences of
  failed bash calls before a fix, exploratory reads when the path was
  named in the brief or CLAUDE.md.
- **Silent ticket pollution** — `bacio issue add` / `bacio feature add` /
  any `mcp__claude_ai_Linear__save_issue` without a preceding
  `mcp__bacio__ask_user_question`.

## Things that are NOT findings

Skip these even if they look suspicious:

- **A `worktree.root` of `null` in the digest.** The proxy transcript has
  no per-message `cwd` envelope, so the worktree root is *inferred* from
  the first `Edit`/`Write` `file_path` or `worktree init`/`rm` arg. A run
  that did no edits and whose `worktree rm` arg didn't match the heuristic
  may show `null` — that is missing signal, not a confinement violation.
  Only an entry in `edits.outside_worktree` is an actual breach.
- **No edits in a `ship` or `review` run.** Both modes are deliberately
  edit-free.
- **No PR in a `plan` or `design` run.** Both produce docs, not code.
- **A clean review.** "No findings, ready to merge" is a valid review
  outcome — the absence of findings isn't itself a finding.
- **A `truncated: true` digest with a missing tail.** A capture whose
  response body exceeded the recorder cap is partial; absent
  late-conversation content reflects the cap, not the worker's behaviour.
