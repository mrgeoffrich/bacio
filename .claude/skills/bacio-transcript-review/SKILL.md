---
name: bacio-transcript-review
description: Retrospective quality review of bacio worker subagent transcripts. Downloads the `.jsonl` transcripts attached to a bacio issue, distils each into a structured digest, compares the agent's behaviour against the per-mode prompt at `prompts/agents/<mode>.md`, and attaches a consolidated review markdown doc to the issue (one doc per issue, sectioned per transcript). Use whenever the user asks to "review the transcripts on BACI-X", "see how the worker handled BACI-X", "audit what the implement/review/plan/ship/design/fix-review subagent did", "find behaviour bugs in this dispatch", or wants a retrospective eval of an already-completed agent run. Looks specifically for: missing claim, wrong final state at release, missing close-out tag, edits outside the worktree root, hook denies, direct `bacio issue state` calls mid-run, raw `sqlite3` / direct DB writes that bypass the audit log, `bacio` calls run without `--env` from outside the worktree, silent ticket pollution (unprompted `bacio issue add` / `bacio feature add`), and retry loops / mechanical inefficiency. Use this proactively after a dispatched run finishes and the user wants to know whether the worker followed the brief.
---

# bacio transcript review

A retrospective audit of one bacio worker subagent's run. The transcripts
are JSONL files attached to the issue (one per `Task`-spawned worker —
see `docs/agent-dispatch.md` §`attach_transcript`). For each transcript
you:

1. **download** the raw `.jsonl`,
2. **distil** it into a structured JSON digest with the bundled script,
3. **compare** the digest against the matching prompt at
   `prompts/agents/<mode>.md` and the per-mode contract in
   `references/mode_expectations.md`,
4. **attach one consolidated review markdown doc** to the issue,
   sectioned per transcript and grouped by severity.

You are not the worker — you are reviewing past work. Read-only is the
default; only writes are the eval comments at close-out.

---

## Inputs

- A bacio issue key (e.g. `BACI-131`). The user usually names it; if not,
  ask which issue. If they ask for "the latest dispatched run" or similar,
  list recent issues with attached transcripts via
  `bacio doc list --type transcript`.

You **must** be running from inside the bacio repo (`bacio` resolves the
DB from the cwd / worktree). If you're not, `bacio doc list` will fail —
`cd` into the repo first.

## Workflow

### 1. Locate the transcripts on the issue

```bash
bacio issue brief <ISSUE-KEY> -o json > /tmp/brief.json
```

Pick the transcript documents — they're entries in `.documents[]` with
`type == "transcript"` and `filename` matching
`bacio-transcript-<ISSUE-KEY>-agent-<id>.jsonl`. The brief returns
metadata only (transcript bodies aren't inlined) — you'll download each
one in step 2.

If the issue has no transcripts attached, stop and tell the user — there
is nothing to review.

### 2. Download and distil each transcript

For each transcript filename:

```bash
mkdir -p /tmp/bacio-review-<ISSUE-KEY>
bacio doc download <filename> --to /tmp/bacio-review-<ISSUE-KEY>/<filename>
python3 ${CLAUDE_PLUGIN_ROOT:-.claude/skills/bacio-transcript-review}/scripts/analyze_transcript.py \
  /tmp/bacio-review-<ISSUE-KEY>/<filename> \
  --out /tmp/bacio-review-<ISSUE-KEY>/<filename>.digest.json
```

The digest is small (typically ~150 lines of JSON, vs a multi-MB raw
transcript) and covers everything the review needs:

| Digest field | What it tells you |
|---|---|
| `dispatch.{issue_key,mode,dispatch_id,agent_id}` | The four facts from the first dispatch tag |
| `worktree.{root,cwd_first,cwd_distinct}` | Where the worker actually ran |
| `tool_counts` | Volume of activity per tool |
| `claim_calls` / `release_calls` | `bacio agent claim`/`release` invocations with their `--prompt` / `--state` args |
| `issue_state_calls_midrun` | Direct `bacio issue state` calls — should be empty (red flag if not) |
| `tag_calls` | `bacio tag add` invocations |
| `worktree_init` / `worktree_rm` | Whether the env was set up and torn down |
| `bacio_calls_outside_worktree_no_env` | `bacio` calls run from elsewhere without `--env` (db / port leak risk) |
| `db_overrides` / `env_overrides` | `--db` / `--env` flag uses |
| `edits.outside_worktree` | `Edit`/`Write` calls whose `file_path` didn't begin with the worktree root |
| `bash.failed_count` + `failed_samples` | Failed Bash invocations |
| `bash.repeated_groups` | Identical bash commands run ≥3 times (retry-loop signal) |
| `hook_denies` | Tool calls denied by the PreToolUse worktree-confinement hook |
| `mcp.reply` / `attach_transcript` / `ask_user_question` | bacio MCP tool calls |
| `comments.{issue_comment_add,feature_comment_add}` | Comments the worker posted |
| `task_creates` | First few TaskCreate subjects (the first should be "Establish working directory") |

If the script errors (truncated transcript, etc.), record that as a
review finding and proceed with whatever it managed to extract.

### 3. Score each transcript

Open the matching prompt: `prompts/agents/<mode>.md` (the mode is the
`dispatch.mode` field). Read it end-to-end — the contract is the brief,
not your prior expectations of the mode. Then walk
`references/mode_expectations.md` for the per-mode close-out checklist.

For each finding bucket below, gather concrete evidence from the digest
(line numbers, command strings, file paths). Cite them as
`line N: <evidence>` so the user can jump to the source.

#### Blocker (must-fix)

- Worker never claimed the issue (`claim_calls` empty) — every mode's
  Setup section requires it.
- Worker released with the wrong `--state` for its mode (see
  `references/mode_expectations.md` table). The memory note
  `feedback_review-mode-final-state` documents one historical
  occurrence — review-mode runs that end in `in_progress` instead of
  `in_review` are a recurring class.
- Worker never released the claim at all (`release_calls` empty but the
  transcript reached its end).
- `edits.outside_worktree` non-empty — wrote to the primary checkout
  rather than the worker's worktree (the BACI-102 failure mode).
  `hook_denies` reinforces the finding if the worker tried and was
  caught.
- `issue_state_calls_midrun` non-empty for any state other than
  `in_progress` (legitimate as the `needs_action → in_progress` hop
  after a user reply, illegitimate for anything else).
- Silent ticket pollution: `bacio issue add` or `bacio feature add`
  appears in `bacio_calls` without a preceding
  `mcp__bacio__ask_user_question` in the same run.
- No `mcp__bacio__reply` at all — the supervisor never gets ack'd.

#### Major (should-fix)

- Missing close-out tag (e.g. an `implement` run with no
  `bacio tag add <key> implemented`).
- No `bacio worktree init` early in the run or no
  `bacio worktree rm ... --confirm ...` at close.
- `bacio_calls_outside_worktree_no_env` non-empty — running `bacio` from
  outside the worktree root without `--env <root>/environment-config.yaml`
  means the call resolves DB/port from `$BACIO_ENV` or `~/.bacio/db.sqlite`,
  not the worktree's manifest. Likely fine on a shared-DB setup, harmful
  on `--isolate-db`.
- Plan-mode worker linked the plan doc to the parent feature (it should
  link only to the issue — a feature link fans the doc onto every
  sibling).
- Review-mode worker made code edits (`tool_counts` for `Edit`/`Write`
  > 0) — review records findings; fix-review fixes them.
- First `TaskCreate` is NOT the "Establish working directory" task.

#### Inefficiency / nit

- `bash.repeated_groups` shows the same command ≥3 times in a row —
  usually a retry loop where the worker kept poking the same broken
  assumption.
- High `bash.failed_count` for a short run — the worker is groping.
- Long sequence of exploratory `Read`/`Grep` calls when the brief or
  `CLAUDE.md` already named the file path (read those before deciding
  this is a finding — a deep grep through a referenced subsystem is
  legitimate).
- Tool counts wildly skewed for the mode (e.g. a `ship` run with
  hundreds of edits — ship should be all `gh pr merge`).
- Editing the same file 4+ times in a row when one Edit would have
  done — usually a confused refactor mid-run.

When in doubt, prefer fewer high-confidence findings over many uncertain
ones. A clean transcript is a valid review outcome — say so explicitly
("no findings beyond minor nits") rather than manufacturing concerns.

### 4. Attach one consolidated review doc to the issue

Build a single markdown file at `/tmp/bacio-transcript-review-<ISSUE-KEY>.md`
covering every transcript on the issue, then upsert it as a `review`-typed
doc (so `bacio issue brief` inlines its body) and link it to the issue.

#### Doc body template

```markdown
# Transcript review — <ISSUE-KEY>

Reviewed <N> transcript(s) on <ISSUE-KEY> against the per-mode prompts at
`prompts/agents/<mode>.md`.

| Transcript | Mode | Agent | Dispatch | Blocker / Major / Nit |
|---|---|---|---|---|
| `<filename>` | implement | `<agent_id>` | 509 | 0 / 1 / 2 |
| `<filename>` | ship | `<agent_id>` | 525 | 1 / 0 / 1 |

---

## `<filename>` — <mode> (agent `<agent_id>`, dispatch <dispatch_id>)

**Transcript:** `<filename>` (<total_lines> lines)

### Blocker
- line 88: edited `/Users/geoff/Repos/bacio/internal/store/store.go` — outside worktree root `/Users/geoff/Repos/bacio/.claude/worktrees/agent-xyz`

### Major
- line 42: released with `--state in_progress`; review-mode should release with `--state in_review`
- line 51: missing close-out `bacio tag add <key> reviewed`

### Nit
- lines 60, 64, 68, 72: `go test ./...` run 4 times in a row — looks like a debug loop

### Notes
- Worker did claim the issue and call `bacio worktree init` correctly.
- `mcp__bacio__reply` fired at line 95.

---

## `<filename>` — <mode> (agent `<agent_id>`, dispatch <dispatch_id>)

(repeat per transcript; for clean runs use a single line: "No findings —
ran cleanly through the <mode> brief.")
```

The summary table at the top lets a reader skim the per-transcript verdict
without scrolling; the per-transcript sections carry the line-anchored
evidence.

#### Upsert and link

```bash
bacio doc upsert "bacio-transcript-review-<ISSUE-KEY>.md" \
  --content-file /tmp/bacio-transcript-review-<ISSUE-KEY>.md \
  --type session-retro

bacio doc link "bacio-transcript-review-<ISSUE-KEY>.md" <ISSUE-KEY> \
  --why "Retrospective transcript review of dispatched workers"

bacio tag add <ISSUE-KEY> retro
```

`--type session-retro` flags the doc as a transcript retrospective —
distinct from PR-review (`review`) and planning (`plan`) docs.
`session_retro` is in the `DocTypeInlinedInBrief` allow-list (alongside
`plan` and `review`), so `bacio issue brief` inlines the body — a reader
opening the brief sees the full retro without a second round trip. The
deterministic filename `bacio-transcript-review-<ISSUE-KEY>.md` means
re-running the skill on the same issue re-upserts in place rather than
duplicating — safe to invoke after every new dispatch.

The `bacio tag add <ISSUE-KEY> retro` step is idempotent — safe on
re-runs. It mirrors the per-mode worker tagging convention (`planned`,
`implemented`, `reviewed`, etc.) and makes "this issue has a
transcript retro attached" a queryable signal on the kanban surfaces.

If the issue already has a `bacio-transcript-review-*` doc attached, the
upsert overwrites the body. That's the correct behaviour: the review is a
rolling snapshot of every transcript currently on the issue, not an
append-only log.

### 5. Summarise to the user

After posting, print a one-paragraph summary to the chat naming each
transcript and its finding count by severity — so the user knows at a
glance whether to open the issue and read the comments. Example:

> Reviewed 3 transcripts on BACI-131. **plan** (agent abc): 0 blockers /
> 1 major / 2 nits — missing `planned` tag. **implement** (agent def):
> 0 blockers / 0 major / 1 nit. **review** (agent ghi): 1 blocker —
> released with `--state in_progress` (should be `in_review`). Comments
> posted to the issue with the full breakdown.

---

## Conventions & gotchas

- **Run from inside the bacio repo.** Every `bacio` command needs the
  repo's manifest or default DB to resolve.
- **Do not modify any other state on the issue.** No `bacio issue state`,
  no `bacio tag add`, no `bacio agent claim` from this skill — you are
  not a worker. Eval comments are the only write.
- **Trust the digest over hand-counted facts.** If the digest says
  `claim_calls: []`, that means no `bacio agent claim` invocation matched
  the parser. If you doubt it, `grep "bacio agent claim" <transcript>`
  before posting a finding — the parser is regex-based and could miss an
  unusual quoting pattern.
- **The first user line of a transcript is the dispatch.** It carries
  `<issue_id>`, `<mode>`, `<dispatch_id>` tags. That's the source of truth
  for which `prompts/agents/<mode>.md` to compare against — not the
  issue's current state.
- **`gitBranch` in the JSONL metadata is unreliable.** It often reads as
  the parent session's branch (e.g. `main`) even when the worker is in a
  proper `.claude/worktrees/agent-…` linked worktree. Only the worker's
  own `git branch --show-current` Bash output is authoritative — and
  even then, a `main` reading from the parent is metadata noise, not a
  finding.
- **Transcripts can be truncated.** Anything over ~2.5 MB gets a one-line
  footer at the end naming the omitted byte count. The analyzer stops at
  the first un-parseable line and emits what it has. Acknowledge that in
  the review comment if you hit it.
- **Don't fabricate the `agent_session_id` / `dispatch_id` pin.** The
  `--eval` flag relies on the server pinning the in-flight snapshot. For
  retrospective reviews those snapshots are empty — the body header is
  the only carrier of which run is being reviewed.
