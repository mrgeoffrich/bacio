package model

import (
	"strings"
	"testing"
)

func TestRenderAgentFile(t *testing.T) {
	got, err := RenderAgentFile("plan", "Planning", "Do the planning work.")
	if err != nil {
		t.Fatalf("RenderAgentFile: %v", err)
	}
	for _, want := range []string{
		"name: bacio-plan-worker\n",
		"model: opus\n",
		"isolation: worktree\n",
		"Do the planning work.\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("RenderAgentFile output missing %q\n--- got ---\n%s", want, got)
		}
	}
	// Frontmatter is a leading --- ... --- block.
	if !strings.HasPrefix(got, "---\n") {
		t.Errorf("RenderAgentFile output does not start with frontmatter:\n%s", got)
	}
	// No `tools:` line — the field is deliberately omitted so the
	// subagent inherits the parent session's full tool set.
	if strings.Contains(got, "\ntools:") {
		t.Errorf("RenderAgentFile output should not carry a tools: line:\n%s", got)
	}
	// The description must NOT leak the gerund into the agent-name slot.
	if !strings.Contains(got, `subagent for the "Planning" stage`) {
		t.Errorf("RenderAgentFile description missing the stage label:\n%s", got)
	}
}

// TestRenderAgentFileBuiltinsHaveNoPlaceholder is the §8 guard: every
// built-in dispatchable brief must render into an agent file with no
// leftover {{...}} placeholder. A leftover token is a packaging bug —
// it would render literally in the worker's own system prompt.
func TestRenderAgentFileBuiltinsHaveNoPlaceholder(t *testing.T) {
	for _, slug := range []string{
		BuiltinTemplatePlan, BuiltinTemplateDesign, BuiltinTemplateImplement,
		BuiltinTemplateReview, BuiltinTemplateShip, BuiltinTemplateFixReview,
	} {
		body := DefaultPromptBodyForBuiltinSlug(slug)
		if body == "" {
			t.Fatalf("built-in %q has no embedded body", slug)
		}
		out, err := RenderAgentFile(slug, BuiltinTemplateLabel(slug), body)
		if err != nil {
			t.Fatalf("RenderAgentFile(%q): %v", slug, err)
		}
		if strings.Contains(out, "{{") {
			t.Errorf("built-in %q renders an agent file containing a {{...}} placeholder", slug)
		}
		// The agent name in the frontmatter must equal SubagentTypeForTemplate.
		wantName := "name: " + SubagentTypeForTemplate(slug) + "\n"
		if !strings.Contains(out, wantName) {
			t.Errorf("built-in %q agent file missing %q", slug, wantName)
		}
	}
}

func TestRenderAgentFileRejectsPlaceholder(t *testing.T) {
	if _, err := RenderAgentFile("custom", "Custom", "Work on {{issue_id}} now."); err == nil {
		t.Fatal("RenderAgentFile accepted a body with a {{...}} placeholder; want error")
	}
}

func TestRenderAgentFileRejectsEmptyBody(t *testing.T) {
	if _, err := RenderAgentFile("custom", "Custom", "   \n  "); err == nil {
		t.Fatal("RenderAgentFile accepted an empty body; want error")
	}
}
