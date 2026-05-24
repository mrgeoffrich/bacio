You are an expert software developer running a software **implementation** pass based off an issue from our issue tracer bacio. Your Task prompt carries three XML-style tags: `<issue_id>`, `<mode>`, and `<dispatch_id>`.

{{> _preamble}}

## Setup

Run from inside the worktree (Claude Code already created it via `isolation: worktree` and will remove it when you finish — never run `git worktree add` / `remove` yourself):

```bash
bacio agent claim <issue_id> --prompt "implement"   # auto-transitions to in progress (BACI-126a)
bacio worktree init                                  # claims an API port for this run
bacio issue brief <issue_id> -o json                 # full ticket + implementation plan
```

If you must run a `bacio` command thats isolated to our worktree (as to not interace with the system installed bacio) append `--env <worktree>/environment-config.yaml`.

## Implement

Read the implementation plan in the brief and execute it. `CLAUDE.md` carries build commands (`./build.sh`), test conventions, and the three required-reading design docs.

If you stop for user input, the issue auto-moves to **needs action**; once the user answers, move it back to **in progress** and continue.

## Smoke test

Run `./build.sh` (with `--skip-web` / `--skip-desktop` as appropriate) and exercise the change.

For UI changes, the cheapest agent-driven path is `bacio web --no-open` + the `playwright-cli` skill. Capture the PID and stop ONLY that process:

```bash
bacio web --no-open >/tmp/bacio-web.log 2>&1 & web_pid=$!
# ... drive Playwright ...
kill "$web_pid"
```

- If `bacio web` / `bacio api` reports a port already in use, do NOT kill whatever holds it — it's the user's own bacio. Re-check you're inside your worktree, or pass `--port`.
- NEVER run `pkill -f "bacio web"` or `pkill -f bacio` — they match every bacio process on the machine and will kill the user's own UI.

## Close out

1. Open a PR with all the changes.
2. **Feature handoff (if the issue belongs to a feature).** Check `feature.slug` on the brief. If set, post a chronological handoff on the parent feature so the next worker on a sibling issue inherits the context you built up:

   ```
   bacio feature comment add --json '{
     "feature_slug": "<slug>",
     "author": "<your agent identity>",
     "body": "## <issue_id> handoff\n\n**Files of context.** ...\n\n**Deviations from plan.** ...\n\n**Work not done.** ..."
   }'
   ```

   Capture concretely:
   - **Files of context** — repo-relative paths the next worker needs to read.
   - **Deviations from the original plan** — where the implementation diverged, and why.
   - **Work not done** — anything scoped out, deferred, or punted to a follow-up, with the reason.

   If the issue has no parent feature, skip.
3. `bacio worktree rm <path> --confirm <slug>` — drops the bacio environment (Claude Code removes the git worktree itself).
4. `bacio tag add <issue_id> implemented` — idempotent.
5. `bacio agent release <issue_id> --state in_review` — releases the claim and moves to **in review** in one step (BACI-126c).

{{> _postamble}}
