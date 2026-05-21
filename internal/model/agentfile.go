package model

import (
	"fmt"
	"strings"
)

// AgentFileModel is the model pinned in every generated subagent file's
// frontmatter. Uniform for v1 — per-mode right-sizing (Sonnet for ship
// / fix_review, Opus for design / plan) is explicitly out of scope; the
// field is present so the follow-up is a one-line edit per file.
const AgentFileModel = "opus"

// AgentFileIsolation is the worktree-isolation mode written into every
// generated subagent's frontmatter. "worktree" makes Claude Code run
// each dispatched worker in its own throwaway git worktree — created on
// spawn, removed automatically on a clean finish — so concurrent
// dispatches never edit each other's files and the worker never has to
// hand-roll `git worktree add` / `remove`. This is complementary to,
// not a replacement for, `bacio worktree init`: Claude Code isolates
// the filesystem, while `bacio worktree init` (still run inside the
// worktree by every brief) isolates bacio's SQLite DB + API port. The
// briefs were rewritten to lean on this — see prompttemplates/*.txt.
const AgentFileIsolation = "worktree"

// WorktreeGuardPreamble is the safety guard prepended to every
// generated subagent file's body (BACI-91). The agent-file frontmatter
// sets `isolation: worktree`, so Claude Code is *supposed* to spawn
// each dispatched worker in its own throwaway git worktree — but
// nothing verifies it. If worktree isolation silently fails, or a
// brief is run in a non-worktree context, a worker would otherwise
// happily claim the ticket and start editing/committing on the primary
// checkout's main branch.
//
// Centralising the guard here (rather than copying it into each of the
// six prompttemplates/*.txt bodies) means it cannot drift and is
// automatically inherited by any future or user-created template — the
// six built-in bodies stay untouched. The guard runs *before* the
// brief's own `## Setup` section, so a worker that fails the check
// aborts before claiming the ticket or mutating any state.
const WorktreeGuardPreamble = `## Worktree safety guard — run this FIRST, before anything else

Before you use the bacio skill, claim the ticket, change any issue
state, or read/edit/commit a single file, verify you are running in an
**isolated git worktree** and **not** on the repo's main branch:

` + "```bash" + `
git rev-parse --show-toplevel
git rev-parse --git-common-dir   # ends in "/.git" only in a linked worktree
git rev-parse --abbrev-ref HEAD
` + "```" + `

**Trust ONLY the ` + "`git rev-parse`" + ` output you run yourself — NOT the
` + "`gitStatus`" + ` block in your system prompt.** That injected ` + "`gitStatus`" + `
block (and any ` + "`Current branch:`" + ` line in it) is a stale snapshot of
the *supervisor* session, captured when the supervisor started — it does
**not** reflect this worktree. It will often say ` + "`Current branch: main`" + `
even though this worktree is on its own branch. Ignore it completely;
the commands above are the only source of truth for where you are.

You are in an isolated worktree when ` + "`git rev-parse --git-common-dir`" + ` is
**different** from ` + "`git rev-parse --git-dir`" + ` (in the primary checkout they
are identical; in a linked worktree the common dir points back at the
primary ` + "`.git`" + ` while the git dir is a per-worktree path).

**Abort immediately — do NOT proceed — if either is true:**

- The current branch is the repo's main branch (` + "`main`" + ` or ` + "`master`" + `).
- You are not in a linked worktree (` + "`--git-dir`" + ` and ` + "`--git-common-dir`" + `
  resolve to the same path, i.e. you are in the primary checkout).

On abort, make **no mutations whatsoever**: do not use the bacio skill,
do not claim the ticket, do not change its state, do not edit or commit
anything. Return a single clear message stating that you aborted
because you were on the main branch / not in an isolated worktree, and
that the dispatch must be re-run with proper worktree isolation. Then
stop.

Only if both checks pass — you are in a linked worktree on a
non-main branch — continue with the rest of this brief.

---

`

// RenderAgentFile produces the contents of a per-mode custom subagent
// file (`.claude/agents/<SubagentTypeForTemplate(slug)>.md`) for a
// dispatch template (BACI-76). The frontmatter carries the agent name
// (== the file basename, == the subagent_type the supervisor spawns), a
// generated description, the model, and the worktree-isolation mode;
// the body is the template body verbatim — that body is the subagent's
// durable system prompt.
//
// Deliberately no `tools:` line: omitting the field makes Claude Code
// give the subagent the parent session's full tool set. The earlier
// BACI-76 allowlist was removed — narrowing the surface was costing
// dispatched workers tools they legitimately need, and the failure
// mode of a missing tool is silent.
//
// body is written verbatim, NOT {{token}}-rendered: a system prompt is
// fixed per agent type and cannot embed a specific issue id. The six
// built-in briefs were rewritten (BACI-76) to refer to "the ticket
// named in your dispatch prompt" instead. A body that still contains a
// `{{` sequence is a packaging bug — RenderAgentFile rejects it so a
// leftover placeholder never ships into an agent file.
//
// Every rendered file's body is prefixed with WorktreeGuardPreamble
// (BACI-91): a centralised worktree+branch safety guard that runs
// before the template's own brief, so a worker spawned outside an
// isolated worktree (or on the main branch) aborts before mutating
// anything. The placeholder check above runs against the *template*
// body only — the guard preamble is bacio-authored and carries no
// `{{...}}` tokens.
func RenderAgentFile(slug, name, body string) (string, error) {
	body = strings.TrimRight(body, "\r\n")
	if strings.TrimSpace(body) == "" {
		return "", fmt.Errorf("template %q has an empty body — nothing to render into an agent file", slug)
	}
	if strings.Contains(body, "{{") {
		return "", fmt.Errorf("template %q body still contains a {{...}} placeholder — a subagent system prompt is fixed per agent type and cannot interpolate a specific ticket; rewrite the body to refer to \"the ticket named in your dispatch prompt\"", slug)
	}
	body = WorktreeGuardPreamble + body
	agentName := SubagentTypeForTemplate(slug)
	label := name
	if strings.TrimSpace(label) == "" {
		label = slug
	}
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "name: %s\n", agentName)
	fmt.Fprintf(&b, "description: bacio dispatched-work subagent for the %q stage. Spawned by the supervisor session on a %s dispatch.\n", label, slug)
	fmt.Fprintf(&b, "model: %s\n", AgentFileModel)
	fmt.Fprintf(&b, "isolation: %s\n", AgentFileIsolation)
	b.WriteString("---\n\n")
	b.WriteString(body)
	b.WriteString("\n")
	return b.String(), nil
}
