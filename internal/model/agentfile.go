package model

import (
	"fmt"
	"strings"
)

// AgentFileModel is the default model pinned in a generated subagent
// file's frontmatter when the template body has no
// `---\nmodel: ...\n---\n` frontmatter declaring otherwise (BACI-155).
const AgentFileModel = "opus"

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

// splitAgentFrontmatter peels a leading `---\n...\n---\n` block off a
// template body and returns the parsed `model:` value (empty when the
// frontmatter has no model key or the body has no frontmatter at all),
// the remaining body after the closing fence, and any parse error.
//
// The grammar is deliberately minimal: only `model:` is recognised, and
// every other key is rejected so a typo surfaces loud instead of being
// silently ignored. CRLF line endings on the fences are tolerated so a
// hand-edited body via `bacio settings template set` on Windows still
// parses; the source files on disk are LF.
func splitAgentFrontmatter(body string) (string, string, error) {
	// No opening fence — body has no frontmatter; the caller falls
	// through to AgentFileModel.
	if !strings.HasPrefix(body, "---\n") && !strings.HasPrefix(body, "---\r\n") {
		return "", body, nil
	}
	// Step past the opening fence line.
	rest := body
	if strings.HasPrefix(rest, "---\r\n") {
		rest = rest[len("---\r\n"):]
	} else {
		rest = rest[len("---\n"):]
	}
	var model string
	var modelSet bool
	for {
		nl := strings.IndexByte(rest, '\n')
		if nl < 0 {
			return "", "", fmt.Errorf("frontmatter is missing its closing `---` fence")
		}
		line := rest[:nl]
		rest = rest[nl+1:]
		// Tolerate CRLF.
		line = strings.TrimRight(line, "\r")
		if line == "---" {
			// Closing fence consumed; rest is whatever follows.
			return model, rest, nil
		}
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			return "", "", fmt.Errorf("frontmatter line %q is not a `key: value` pair", line)
		}
		key := strings.TrimSpace(line[:colon])
		val := strings.TrimSpace(line[colon+1:])
		if key != "model" {
			return "", "", fmt.Errorf("frontmatter has unknown key %q (only `model` is recognised)", key)
		}
		if modelSet {
			return "", "", fmt.Errorf("frontmatter has a duplicate `model` key")
		}
		model = val
		modelSet = true
	}
}

// RenderAgentFile produces the contents of a per-mode custom subagent
// file (`.claude/agents/<SubagentTypeForTemplate(slug)>.md`) for a
// dispatch template (BACI-76). The frontmatter carries the agent name
// (== the file basename, == the subagent_type the supervisor spawns), a
// generated description, the model (from the body's own
// `---\nmodel: ...\n---\n` frontmatter when present, else
// AgentFileModel — BACI-155), the preloaded `skills:` list (BACI-97 —
// Claude Code injects each named skill's full content at startup), and
// the worktree-isolation mode; the body is the template body verbatim
// (minus the frontmatter the renderer peeled off) — that body is the
// subagent's durable system prompt.
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
	parsedModel, rest, err := splitAgentFrontmatter(body)
	if err != nil {
		return "", fmt.Errorf("template %q frontmatter: %w", slug, err)
	}
	body = strings.TrimRight(rest, "\r\n")
	if strings.TrimSpace(body) == "" {
		return "", fmt.Errorf("template %q has an empty body after frontmatter — nothing to render into an agent file", slug)
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
	modelLine := parsedModel
	if modelLine == "" {
		modelLine = AgentFileModel
	}
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "name: %s\n", agentName)
	fmt.Fprintf(&b, "description: bacio dispatched-work subagent for the %q stage. Spawned by the supervisor session on a %s dispatch.\n", label, slug)
	fmt.Fprintf(&b, "model: %s\n", modelLine)
	if len(AgentFileSkills) > 0 {
		fmt.Fprintf(&b, "skills: [%s]\n", strings.Join(AgentFileSkills, ", "))
	}
	fmt.Fprintf(&b, "isolation: %s\n", AgentFileIsolation)
	b.WriteString("---\n\n")
	b.WriteString(body)
	b.WriteString("\n")
	return b.String(), nil
}
