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
// briefs were rewritten to lean on this — see prompts/agents/*.md.
const AgentFileIsolation = "worktree"

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
// body composition: the source files at prompts/agents/<slug>.md
// carry a `{{> _preamble}}` directive (after the per-mode role intro)
// so every built-in body inlines the shared operating-protocol +
// worktree-safety guard block. The default loader expands that
// directive at package init, so a freshly seeded body already carries
// the shared block verbatim. RenderAgentFile also expands directives
// on the passed-in body, so a user-customised template that keeps (or
// moves) the directive still resolves it — and a custom template that
// omits it deliberately gets no preamble. After expansion, the body
// must contain no remaining `{{...}}` placeholder: a subagent system
// prompt is fixed per agent type and cannot embed a specific issue id.
func RenderAgentFile(slug, name, body string) (string, error) {
	body = strings.TrimRight(body, "\r\n")
	if strings.TrimSpace(body) == "" {
		return "", fmt.Errorf("template %q has an empty body — nothing to render into an agent file", slug)
	}
	expanded, err := ExpandPromptIncludes(body)
	if err != nil {
		return "", fmt.Errorf("template %q: %w", slug, err)
	}
	if strings.Contains(expanded, "{{") {
		return "", fmt.Errorf("template %q body still contains a {{...}} placeholder after include expansion — a subagent system prompt is fixed per agent type and cannot interpolate a specific ticket; rewrite the body to refer to \"the ticket named in your dispatch prompt\"", slug)
	}
	body = expanded
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
