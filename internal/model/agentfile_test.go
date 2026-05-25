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
		"skills: [bacio]\n",
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

// TestRenderAgentFileModelPerMode is the BACI-155 guard: every built-in
// dispatchable brief declares its `model:` via leading
// `---\nmodel: <name>\n---\n` frontmatter on the source file, and
// `RenderAgentFile` must thread that value into the generated agent
// file. plan / plan_large / design / implement / fix_review declare
// `opus`; review / ship declare `sonnet`.
func TestRenderAgentFileModelPerMode(t *testing.T) {
	cases := map[string]string{
		BuiltinTemplatePlan:      "opus",
		BuiltinTemplatePlanLarge: "opus",
		BuiltinTemplateDesign:    "opus",
		BuiltinTemplateImplement: "opus",
		BuiltinTemplateReview:    "sonnet",
		BuiltinTemplateShip:      "sonnet",
		BuiltinTemplateFixReview: "opus",
	}
	for slug, wantModel := range cases {
		body := DefaultPromptBodyForBuiltinSlug(slug)
		out, err := RenderAgentFile(slug, BuiltinTemplateLabel(slug), body)
		if err != nil {
			t.Fatalf("RenderAgentFile(%q): %v", slug, err)
		}
		wantLine := "model: " + wantModel + "\n"
		if !strings.Contains(out, wantLine) {
			t.Errorf("built-in %q agent file missing %q\n--- got ---\n%s", slug, wantLine, out)
		}
	}
}

// TestRenderAgentFile_FrontmatterModel checks that a body whose head
// is a `---\nmodel: <name>\n---\n` block has that model threaded into
// the generated agent file's frontmatter — and the frontmatter block
// itself is consumed (not retained in the body).
func TestRenderAgentFile_FrontmatterModel(t *testing.T) {
	got, err := RenderAgentFile("custom", "Custom", "---\nmodel: haiku\n---\nHello.")
	if err != nil {
		t.Fatalf("RenderAgentFile: %v", err)
	}
	if !strings.Contains(got, "model: haiku\n") {
		t.Errorf("expected `model: haiku` in output, got:\n%s", got)
	}
	// The frontmatter block must NOT leak into the rendered body.
	bodyStart := strings.Index(got, "\n---\n\n")
	if bodyStart < 0 {
		t.Fatalf("output is missing the frontmatter terminator:\n%s", got)
	}
	body := got[bodyStart+len("\n---\n\n"):]
	if strings.Contains(body, "---") {
		t.Errorf("rendered body should not contain a stray --- fence:\n%s", body)
	}
	if !strings.HasPrefix(body, "Hello.") {
		t.Errorf("rendered body should start with the post-frontmatter content, got:\n%s", body)
	}
}

// TestRenderAgentFile_NoFrontmatter checks that a body without a
// leading frontmatter block falls back to AgentFileModel.
func TestRenderAgentFile_NoFrontmatter(t *testing.T) {
	got, err := RenderAgentFile("custom", "Custom", "Hello.")
	if err != nil {
		t.Fatalf("RenderAgentFile: %v", err)
	}
	if !strings.Contains(got, "model: "+AgentFileModel+"\n") {
		t.Errorf("expected fallback model %q in output, got:\n%s", AgentFileModel, got)
	}
}

// TestRenderAgentFile_FrontmatterEmpty checks that a frontmatter with
// no `model:` key falls back to AgentFileModel.
func TestRenderAgentFile_FrontmatterEmpty(t *testing.T) {
	got, err := RenderAgentFile("custom", "Custom", "---\n---\nHello.")
	if err != nil {
		t.Fatalf("RenderAgentFile: %v", err)
	}
	if !strings.Contains(got, "model: "+AgentFileModel+"\n") {
		t.Errorf("expected fallback model %q in output, got:\n%s", AgentFileModel, got)
	}
}

// TestRenderAgentFile_FrontmatterUnknownKey checks that a frontmatter
// with any key other than `model:` is rejected loud — a typo should
// not silently fall through to the default.
func TestRenderAgentFile_FrontmatterUnknownKey(t *testing.T) {
	_, err := RenderAgentFile("custom", "Custom", "---\nother: x\n---\nHello.")
	if err == nil {
		t.Fatal("RenderAgentFile accepted a body with an unknown frontmatter key; want error")
	}
	if !strings.Contains(err.Error(), "unknown key") {
		t.Errorf("error should mention `unknown key`, got: %v", err)
	}
}

// TestRenderAgentFile_FrontmatterUnclosed checks that a body opening
// with `---\n` but missing its closing fence is rejected.
func TestRenderAgentFile_FrontmatterUnclosed(t *testing.T) {
	_, err := RenderAgentFile("custom", "Custom", "---\nmodel: opus\nHello world\n")
	if err == nil {
		t.Fatal("RenderAgentFile accepted a body with an unclosed frontmatter fence; want error")
	}
	if !strings.Contains(err.Error(), "closing `---` fence") {
		t.Errorf("error should mention the missing closing fence, got: %v", err)
	}
}

// TestRenderAgentFile_FrontmatterDuplicateModel checks that a
// frontmatter declaring `model:` twice is rejected.
func TestRenderAgentFile_FrontmatterDuplicateModel(t *testing.T) {
	_, err := RenderAgentFile("custom", "Custom", "---\nmodel: opus\nmodel: sonnet\n---\nHello.")
	if err == nil {
		t.Fatal("RenderAgentFile accepted a body with a duplicate `model` frontmatter key; want error")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("error should mention the duplicate key, got: %v", err)
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
