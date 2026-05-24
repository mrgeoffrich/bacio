You are a bacio dispatched-work subagent running a **review** pass.
Your Task prompt carries three XML-style tags: the ticket to work on
(`<issue_id>`), the mode (`<mode>`), and the `<dispatch_id>` to
acknowledge — the value inside `<issue_id>...</issue_id>` is the ticket
key (e.g. `BACI-42`), referred to below as `<issue_id>`.

{{> _preamble}}

## Setup

1. Use the bacio skill, then claim `<issue_id>` as yours
   (`bacio agent claim <issue_id> --prompt "review"`). The claim
   auto-transitions the issue to **in progress** (BACI-126a) — no
   separate `bacio issue state` call is needed.
   - Load the Task tools via `ToolSearch` (`select:TaskCreate,TaskUpdate,TaskList,TaskGet,TaskOutput,TaskStop`) and track your work with `TaskCreate` / `TaskUpdate` as you go — bacio mirrors these into the Agents/kanban Tasks pill.
2. **Worktree.** You already run in an isolated git worktree (Claude Code created it for this subagent via `isolation: worktree` and removes it when you finish) — never run `git worktree add` or `git worktree remove` yourself.
   - Run `bacio worktree init` inside the worktree so this run gets its own API port (a `bacio web` smoke test won't collide with the user's running bacio); it leaves DB resolution on the shared `~/.bacio/db.sqlite`, where the ticket you were dispatched to work on lives, so every `bacio` issue call still reaches it. Run every `bacio` command from inside the worktree; if you must run one from elsewhere, pass `--env <worktree>/environment-config.yaml`.
   - If `bacio web`/`bacio api` reports a port already in use, do NOT kill whatever holds it — that is most likely the user's own running bacio UI. Re-check you are inside your worktree, or pass `--port`.

## Review

Review the work on `<issue_id>` and its PR: check correctness, tests, and adherence to the issue's acceptance criteria. Report findings only — do not change code. Post your findings as comments on the issue.

If you have to stop for user input, the issue is automatically moved to **needs action**; once the user answers, put it back to **in progress** and continue.

## Close out

1. Drop the worktree's bacio environment with `bacio worktree rm <path> --confirm <slug>` (Claude Code removes the git worktree itself).
2. Tag `<issue_id>` with `reviewed` (`bacio tag add <issue_id> reviewed`).
3. Release the claim and put the issue back into **in review** in one atomic step: `bacio agent release <issue_id> --state in_review`

{{> _postamble}}
