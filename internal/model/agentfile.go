package model

import (
	"fmt"
	"strings"
)

// AgentFileToolAllowlist is the explicit `tools:` allowlist BACI-76
// writes into every generated per-mode subagent file. It is the
// general-purpose code-work core (Read/Edit/Write/Bash/Grep/Glob) plus
// the session task-list family (TaskCreate/TaskUpdate/TaskList/TaskGet —
// no `Task`/`Agent`, since subagents can't spawn subagents, and no
// `TodoWrite`, disabled upstream since v2.1.142 in favour of the Task*
// tools) plus WebFetch/WebSearch (plan and design briefs do real
// research) plus `Skill` (every brief opens with "use the bacio skill"
// and smoke-tests via the playwright-cli skill — an allowlist that
// omits `Skill` blocks all skill invocation) plus only the three bacio
// channel MCP tools a dispatched worker needs. Every other MCP surface
// the general-purpose subagent inherits today (Gmail / Calendar /
// Drive / Linear / Slack / plugin-dev) is deliberately dropped — that
// is the surface-narrowing win the ticket calls out, and it also trims
// the tool-definition block in the subagent's prefill.
//
// v1 keeps the list uniform across all six modes; per-mode narrowing
// is a follow-up. The list is generated, so tightening it later is a
// generator change, not a per-file edit.
var AgentFileToolAllowlist = []string{
	"Read", "Edit", "Write", "Bash", "Grep", "Glob",
	"TaskCreate", "TaskUpdate", "TaskList", "TaskGet",
	"Skill", "WebFetch", "WebSearch",
	"mcp__bacio__register", "mcp__bacio__reply", "mcp__bacio__ask_user_question",
}

// AgentFileModel is the model pinned in every generated subagent file's
// frontmatter. Uniform for v1 — per-mode right-sizing (Sonnet for ship
// / fix_review, Opus for design / plan) is explicitly out of scope; the
// field is present so the follow-up is a one-line edit per file.
const AgentFileModel = "opus"

// RenderAgentFile produces the contents of a per-mode custom subagent
// file (`.claude/agents/<SubagentTypeForTemplate(slug)>.md`) for a
// dispatch template (BACI-76). The frontmatter carries the agent name
// (== the file basename, == the subagent_type the supervisor spawns), a
// generated description, the tool allowlist, and the model; the body is
// the template body verbatim — that body is the subagent's durable
// system prompt.
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
	fmt.Fprintf(&b, "tools: %s\n", strings.Join(AgentFileToolAllowlist, ", "))
	fmt.Fprintf(&b, "model: %s\n", AgentFileModel)
	b.WriteString("---\n\n")
	b.WriteString(body)
	b.WriteString("\n")
	return b.String(), nil
}
