package model

import (
	"fmt"
	"strings"
)

// AgentFileModel is the default model pinned in a generated subagent
// file's frontmatter — used for every mode that does not have a
// per-mode override in agentFileModelOverrides.
const AgentFileModel = "opus"

// agentFileModelOverrides records per-mode model right-sizing (BACI-118):
// a dispatch template slug whose generated `.claude/agents/` file should
// carry a `model:` other than AgentFileModel. The review and ship
// workers run on Sonnet — their jobs (reviewing a diff, merging a PR)
// don't need the heavier default. Every other mode (plan, design,
// implement, fix_review) is absent here and inherits AgentFileModel.
//
// Keyed by built-in template slug; a user-created template slug has no
// entry and falls through to the default. Mirrors the
// builtinTemplateActionLabels shape so the per-mode override sits next
// to its peers and a future mode is a one-line addition.
var agentFileModelOverrides = map[string]string{
	BuiltinTemplateReview: "sonnet",
	BuiltinTemplateShip:   "sonnet",
}

// AgentFileModelForSlug returns the model a generated subagent file
// should pin for a template slug — the per-mode override from
// agentFileModelOverrides when one exists, else the default
// AgentFileModel. RenderAgentFile uses this to set the frontmatter
// `model:` line.
func AgentFileModelForSlug(slug string) string {
	if m, ok := agentFileModelOverrides[slug]; ok {
		return m
	}
	return AgentFileModel
}

// AgentFileSkills lists the skills preloaded into every generated
// subagent via the frontmatter `skills:` field (BACI-97). Claude Code
// injects each named skill's full content into the subagent's context
// at startup, so the worker no longer has to take an explicit "Use the
// bacio skill" step — and the preloaded content is prompt-cache-
// eligible across back-to-back same-mode spawns.
//
// "bacio" is the skill directory basename written by `bacio
// install-skill` (`.claude/skills/bacio/SKILL.md`). Only *skills* can
// be preloaded this way; the deferred Task tools (`TaskCreate` etc.)
// cannot — the briefs keep the `ToolSearch` instruction for those.
var AgentFileSkills = []string{"bacio"}

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
` + "```" + `

**Trust ONLY the ` + "`git rev-parse --show-toplevel`" + ` output for the location of our current working folder.

You are in an isolated worktree please check that .claude/worktrees is a part of the working folder, if
not abort immediately.

Also abort if the current branch is on main.

---

`

// WorkerProtocolPreamble is the shared worker brief block prepended to
// every generated subagent file's body (BACI-96), directly after the
// WorktreeGuardPreamble and before the template's own brief.
//
// A dispatched `bacio-<mode>-worker` subagent does NOT inherit the
// Claude Code default system prompt — confirmed by a mitmproxy capture
// (BACI-95, `docs/subagent-default-prompt-gap.md`). So harness
// conventions a normal Claude session takes for granted (what a
// `<system-reminder>` tag means, that hook output is feedback, the
// `file_path:line_number` citation form) and the task-tool usage
// guidance are simply absent from a worker's prompt. This preamble
// folds a curated subset back in.
//
// Two parts:
//
//  1. A worker-adapted trim of the Claude Code default prompt — the
//     "autonomous agent" framing plus the harness/behaviour notes. The
//     terminal-markdown line and the security-policy paragraph are
//     dropped as not applicable to a headless worker.
//  2. A short spec for the task tools (`TaskCreate` / `TaskUpdate` /
//     `TaskList` / `TaskGet`). They are deferred tools with no usage
//     guidance in a worker's system prompt; the spec tells the worker
//     when and how to track multi-step dispatch work.
//
// Centralising it here (rather than copying it into each
// prompttemplates/*.txt body) means it cannot drift and is inherited
// by any future or user-created template — the same rationale as
// WorktreeGuardPreamble.
const WorkerProtocolPreamble = `## Worker protocol

You are an autonomous agent that performs software engineering tasks.

### Harness

- ` + "`<system-reminder>`" + ` tags in messages and tool results are injected by the harness, not the user. Hooks may intercept tool calls; treat hook output as user feedback.
- Prefer the dedicated file/search tools over shell commands when one fits. Independent tool calls can run in parallel in one response.
- Reference code as ` + "`file_path:line_number`" + ` — it's clickable.

Write code that reads like the surrounding code: match its comment density, naming, and idiom.

For actions that are hard to reverse or outward-facing, confirm first unless durably authorized or explicitly told to proceed without asking; approval in one context doesn't extend to the next. Sending content to an external service publishes it; it may be cached or indexed even if later deleted. Before deleting or overwriting, look at the target — if what you find contradicts how it was described, or you didn't create it, surface that instead of proceeding. Report outcomes faithfully: if tests fail, say so with the output; if a step was skipped, say that; when something is done and verified, state it plainly without hedging.

### Tracking your work with the task tools

The task tools (` + "`TaskCreate`" + ` / ` + "`TaskUpdate`" + ` / ` + "`TaskList`" + ` / ` + "`TaskGet`" + ` — the successor to ` + "`TodoWrite`" + `) let you track multi-step dispatch work. They are deferred tools — load their schemas via ` + "`ToolSearch`" + ` (` + "`select:TaskCreate,TaskUpdate,TaskList,TaskGet,TaskOutput,TaskStop`" + `) before calling them.

- Use ` + "`TaskCreate`" + ` when the dispatch needs 3+ distinct steps; skip it for trivial single-step jobs.
- Fields: ` + "`subject`" + ` (imperative title), ` + "`description`" + `, optional ` + "`activeForm`" + ` (spinner text). Tasks start ` + "`pending`" + `.
- Mark a task ` + "`in_progress`" + ` before starting it; ` + "`completed`" + ` only when fully done — never with failing tests, partial work, or unresolved errors. When blocked, keep it ` + "`in_progress`" + ` and add a new task for the blocker.
- ` + "`TaskGet`" + ` the latest state before ` + "`TaskUpdate`" + ` (staleness). ` + "`addBlocks`" + ` / ` + "`addBlockedBy`" + ` wire dependencies.
- This applies to YOU, the worker doing the real work. The supervisor that dispatched you stays a thin scheduler — it does not grow a per-dispatch task list.

---

`

// RenderAgentFile produces the contents of a per-mode custom subagent
// file (`.claude/agents/<SubagentTypeForTemplate(slug)>.md`) for a
// dispatch template (BACI-76). The frontmatter carries the agent name
// (== the file basename, == the subagent_type the supervisor spawns), a
// generated description, the model (per-mode via AgentFileModelForSlug —
// BACI-118), the preloaded `skills:` list
// (BACI-97 — Claude Code injects each named skill's full content at
// startup), and the worktree-isolation mode; the body is the template
// body verbatim — that body is the subagent's durable system prompt.
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
// Every rendered file's body is prefixed with two bacio-authored
// preamble blocks, in order: WorktreeGuardPreamble (BACI-91) — a
// centralised worktree+branch safety guard that runs before anything
// else so a worker spawned outside an isolated worktree (or on the
// main branch) aborts before mutating anything — then
// WorkerProtocolPreamble (BACI-96) — the curated harness/behaviour
// prose and task-tool usage spec a worker would otherwise lack,
// because dispatched subagents do not inherit the Claude Code default
// system prompt. The template's own brief follows both. The
// placeholder check above runs against the *template* body only — both
// preambles are bacio-authored and carry no `{{...}}` tokens.
func RenderAgentFile(slug, name, body string) (string, error) {
	body = strings.TrimRight(body, "\r\n")
	if strings.TrimSpace(body) == "" {
		return "", fmt.Errorf("template %q has an empty body — nothing to render into an agent file", slug)
	}
	if strings.Contains(body, "{{") {
		return "", fmt.Errorf("template %q body still contains a {{...}} placeholder — a subagent system prompt is fixed per agent type and cannot interpolate a specific ticket; rewrite the body to refer to \"the ticket named in your dispatch prompt\"", slug)
	}
	body = WorktreeGuardPreamble + WorkerProtocolPreamble + body
	agentName := SubagentTypeForTemplate(slug)
	label := name
	if strings.TrimSpace(label) == "" {
		label = slug
	}
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "name: %s\n", agentName)
	fmt.Fprintf(&b, "description: bacio dispatched-work subagent for the %q stage. Spawned by the supervisor session on a %s dispatch.\n", label, slug)
	fmt.Fprintf(&b, "model: %s\n", AgentFileModelForSlug(slug))
	if len(AgentFileSkills) > 0 {
		fmt.Fprintf(&b, "skills: [%s]\n", strings.Join(AgentFileSkills, ", "))
	}
	fmt.Fprintf(&b, "isolation: %s\n", AgentFileIsolation)
	b.WriteString("---\n\n")
	b.WriteString(body)
	b.WriteString("\n")
	return b.String(), nil
}
