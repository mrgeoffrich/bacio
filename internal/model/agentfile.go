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
func RenderAgentFile(slug, name, body string) (string, error) {
	body = strings.TrimRight(body, "\r\n")
	if strings.TrimSpace(body) == "" {
		return "", fmt.Errorf("template %q has an empty body — nothing to render into an agent file", slug)
	}
	if strings.Contains(body, "{{") {
		return "", fmt.Errorf("template %q body still contains a {{...}} placeholder — a subagent system prompt is fixed per agent type and cannot interpolate a specific ticket; rewrite the body to refer to \"the ticket named in your dispatch prompt\"", slug)
	}
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
