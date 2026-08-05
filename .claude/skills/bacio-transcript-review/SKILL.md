---
name: bacio-transcript-review
description: Retrospective quality review of bacio worker subagent runs, sourced from the reverse-proxy capture of each dispatch's Anthropic traffic (`bacio proxy job` / `bacio proxy captures` / `bacio proxy raw`). For one issue it resolves the dispatched worker runs, distils each into a behavioural digest, grades the digest against the worker's per-mode brief at `prompts/agents/<mode>.md`, and writes one severity-bucketed markdown finding per run plus a rollup. Triggers on "review the transcripts on BACI-X", "audit what the implement/review/plan/ship/design/fix-review subagent did on BACI-X", "review the captured runs on BACI-X", or "find behaviour bugs in this dispatch".
---

# bacio worker review — captured-traffic edition

Grade what a dispatched `bacio-<mode>-worker` actually did against the brief it
was given, using the reverse-proxy capture of its Anthropic traffic as the
source of truth. This replaces the retired `.jsonl`-attachment path (BACI-307):
the worker's model turns are now captured by bacio's local proxy
(BACI-305/306/308) and read back per dispatch with `bacio proxy`.

## When to use / preconditions

- **Read-only.** The skill reads captures and writes draft markdown to the cwd;
  it posts nothing unless the user explicitly asks (see the last step).
- **Runs DB-direct, no server needed.** `bacio proxy …` resolves the store the
  same way `bacio history` does — a running `bacio web`/`api` is not required.
- **Needs a BACI-306+ `bacio` on PATH** for `proxy job`/`captures`/`raw`. The
  system binary is fine once it's a 306+ build; during this feature's rollout,
  the workspace binary (`.bin/bacio-agent-<slug>` from `./build.sh`) carries the
  verbs. Check with `bacio proxy --help` — if it lists only `stats`, the binary
  predates the capture verbs.
- **Scope.** One issue at a time. The skill reviews only the worker's own model
  turns (`api.anthropic.com` `/v1/messages` captures); other proxied FQDN traffic
  is the Monitor screen's concern, not a behavioural review.

The bundled pieces:

- `scripts/analyze_transcript.py` — distils one dispatch's captured traffic into
  a compact JSON digest. Two input modes (`--job`, `--raw-ids`); see Step 3.
- `references/mode_expectations.md` — the per-mode close-out contract + the
  findings / not-findings rubric. Read it alongside `prompts/agents/<mode>.md`.

## Step 1 — resolve the issue's worker runs

List the issue's correlated Anthropic captures and group them into dispatches:

```bash
bacio proxy captures --host api.anthropic.com --anthropic --limit 500 -o json
```

Each row is enriched with `dispatch_id`, `issue_key`, and `mode` (the capture's
correlation, lifted from Claude Code's session / agent-id headers). Then:

1. Keep rows whose `issue_key == <KEY>`.
2. Group the kept rows by `dispatch_id`. Each distinct `dispatch_id` is **one
   worker run to review**; its `mode` (plan / implement / review / ship / …)
   comes straight off the row — no payload regex.
3. Within each dispatch, sort the rows by `started_at` (then `id`) ascending —
   that oldest-first capture-id order is what the raw fallback needs in Step 3.

Notes:

- `--limit 500` caps the scan; bump it, or add `--since 2d` (a lookback window),
  for a very active issue whose captures fall outside the default window.
- A capture only carries `issue_key`/`mode` when its `dispatch_id` resolved.
  Early-session or non-subagent requests have empty correlation and are
  correctly excluded from a per-issue review.
- **No correlated rows for `<KEY>`** → report "no captured runs found for
  `<KEY>`" and stop. That is a clean, expected outcome (the issue may predate the
  proxy capture, or its workers ran on a machine without the proxy), not a
  failure to paper over.

## Step 2 — fetch each run's transcript

For each dispatch, prefer the parsed per-job transcript:

```bash
bacio proxy job <dispatch_id> -o json
```

This returns a `model.AnthropicTranscript`: the ordered primary-thread messages
(user / tool_result turns interleaved with the assistant turns), summed token
usage across the job, and the auxiliary turns (title-gen / sidechain probes).

If `bacio proxy job` prints **`not found`**, the capture index exists but the
BACI-306 parser hasn't populated `proxy_messages` for that dispatch yet. Fall
back to the raw captures: take the dispatch's capture ids from Step 1
(oldest-first) and let the distiller reconstruct the transcript from the raw
`.http` bytes (Step 3 handles both — you don't shell `proxy job` yourself).

## Step 3 — distil one run into a digest

The distiller shells out to `bacio proxy` itself; hand it the dispatch and (for
the fallback) the capture ids:

```bash
# Parsed path (proxy_messages populated):
python3 scripts/analyze_transcript.py --job <dispatch_id> --mode <mode> --bacio "$(command -v bacio)"

# Raw fallback (proxy job returned not-found):
python3 scripts/analyze_transcript.py --job <dispatch_id> --mode <mode> \
    --raw-ids <id,id,...> --bacio "$(command -v bacio)"
```

Pass `--bacio <path>` when the capture verbs live on a workspace binary rather
than the bare `bacio` on PATH. With `--job` set, the script tries the parsed
path first and only uses `--raw-ids` when `proxy job` is empty — so passing both
is safe and makes the call work regardless of which substrate is available.

The digest is compact JSON. Read each field against the brief:

- `source` — `proxy_job` (parsed) or `raw_fallback` (reconstructed from raw).
  `truncated: true` means a response body exceeded the recorder cap and the turn
  is partial — note it, don't treat missing tail content as a finding.
- `dispatch.{mode,dispatch_id,model}` — identity. Set `issue_key` from Step 1.
- `usage` — summed tokens for the run (input / output / cache / thinking). The
  proxy source has no per-call timing, only this per-job total.
- `tool_counts` — tool-use tally. A `ship`/`review` run with `Edit`/`Write` > 0
  is a red flag (both modes are edit-free); a `plan`/`design` run with no
  `doc upsert` is suspicious.
- `worktree.root` + `edits.outside_worktree` — the confinement test. `root` is
  inferred from `Edit`/`Write` `file_path` and `worktree init`/`rm` args (the
  proxy transcript has no per-message `cwd`). Any entry in `outside_worktree` is
  an edit that landed outside the worker's worktree — a confinement finding.
- `claim_calls` / `release_calls` — the claim/release pair that brackets the run.
  A missing claim, or a release with the wrong `--state` for the mode, is a
  finding (check `references/mode_expectations.md`).
- `worktree_init` / `worktree_rm` — expected once each (startup / close-out).
- `issue_state_calls_midrun` — a direct `bacio issue state` mid-run is a finding
  unless it's the single legitimate `needs_action → in_progress` hop after a
  user reply.
- `tag_calls` — the close-out tag, where the mode adds one. (Pipeline-engine
  cards may have the engine add the tag instead of the worker — cross-check the
  mode contract before flagging an absent tag.)
- `comments` / `plan_doc_calls` — the handoff comment / produced plan or design
  doc, per mode.
- `mcp.reply` — `mcp__bacio__reply` is the mandatory final ack. Absent = finding.
- `mcp.ask_user_question` — a clarification pause. Used legitimately before
  speculative work or to file a follow-up; its absence before a `bacio issue
  add` / `feature add` is the "silent ticket pollution" finding.
- `bash.failed_samples` / `bash.repeated_groups` — failed commands and retry
  loops (inefficiency that should have been mechanical).
- `hook_denies` — PreToolUse hook denials (e.g. an edit attempted outside the
  worktree, or a raw-`sqlite3` attempt). A deny that the worker recovered from is
  healthier than never trying, but still worth recording.

Anchor findings on the digest's `msg_index` (and the capture id from Step 1)
rather than `.jsonl` line numbers — the proxy source has no line envelope.

## Step 4 — grade and write one finding per run

For each dispatch: read `prompts/agents/<mode>.md` (the brief the worker was
given) and `references/mode_expectations.md` (the close-out contract + rubric),
then judge the digest against both. Write one file per run to the cwd:

```
comment-<dispatch_id>-<mode>.md
```

with `## Blocker` / `## Non-blocker` / `## Nit` headings (use only the headings
that apply — the same three buckets `references/mode_expectations.md` uses).

Report every real divergence you found and let the buckets carry the severity —
don't pre-filter to "only the ones worth mentioning". Two things are not
findings, though: a divergence you can't ground in a specific `msg_index`, and
padding on a run that genuinely followed the brief. A clean run is reported
clean — "followed the brief, no findings" — and that is a valid outcome (a
clean review, an edit-free ship).

Keep each finding to the claim, the evidence anchor, and one line on why it
matters. These files are read at a glance next to the rollup, not studied.

## Step 5 — rollup

Print one stdout paragraph naming each run by mode and its blocker/major/nit
counts, so the user sees at a glance which runs are interesting without opening a
file. Example: "BACI-309 plan (1672): clean. BACI-309 implement (1676): 1 major
(no handoff comment), 2 nits."

## Optional — posting eval notes

The skill saves drafts by default. Only when the user explicitly asks to post,
write each finding as an eval comment:

```bash
bacio comment add --eval --json '{
  "issue_key": "<KEY>",
  "author": "<your identity>",
  "body": "<the comment-*.md body>"
}'
```

`--eval` pins the in-flight review snapshot onto the comment. There is no
persisted per-event ref on the proxy transcript (the `.jsonl` transcripts had
one), so omit `transcript_event_ref` — the finding anchors on the capture id /
`msg_index` named in its body. Post one comment per run, or a single rollup
comment, as the user prefers.
