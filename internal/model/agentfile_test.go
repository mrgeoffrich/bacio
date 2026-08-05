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
		BuiltinTemplateScope:     "sonnet",
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

// TestRenderAgentFile_FrontmatterEffort checks that a declared
// `effort:` is threaded into the generated agent file. Effort is the
// cheapest per-mode dial: it controls thinking volume, so a mechanical
// mode can be stepped down without touching its brief.
func TestRenderAgentFile_FrontmatterEffort(t *testing.T) {
	for _, lvl := range AgentFileEffortLevels {
		got, err := RenderAgentFile("custom", "Custom", "---\nmodel: opus\neffort: "+lvl+"\n---\nHello.")
		if err != nil {
			t.Fatalf("RenderAgentFile(effort=%s): %v", lvl, err)
		}
		if !strings.Contains(got, "effort: "+lvl+"\n") {
			t.Errorf("expected `effort: %s` in output, got:\n%s", lvl, got)
		}
		if !strings.Contains(got, "model: opus\n") {
			t.Errorf("effort should not displace the model line, got:\n%s", got)
		}
	}
	// An integer is a valid effort too — Claude Code accepts either a
	// named level or a raw token budget.
	got, err := RenderAgentFile("custom", "Custom", "---\neffort: 4096\n---\nHello.")
	if err != nil {
		t.Fatalf("RenderAgentFile(effort=4096): %v", err)
	}
	if !strings.Contains(got, "effort: 4096\n") {
		t.Errorf("expected integer effort in output, got:\n%s", got)
	}
	// effort alone still falls back to the default model.
	if !strings.Contains(got, "model: "+AgentFileModel+"\n") {
		t.Errorf("expected fallback model with effort-only frontmatter, got:\n%s", got)
	}
}

// TestRenderAgentFile_EffortOmittedWhenUnset is the important half of
// the feature: an absent `effort:` must leave the field out of the
// rendered frontmatter entirely, so the mode inherits the session
// default rather than being pinned to a value nobody chose.
func TestRenderAgentFile_EffortOmittedWhenUnset(t *testing.T) {
	for _, body := range []string{
		"Hello.",                       // no frontmatter
		"---\n---\nHello.",             // empty frontmatter
		"---\nmodel: sonnet\n---\nHi.", // model only
	} {
		got, err := RenderAgentFile("custom", "Custom", body)
		if err != nil {
			t.Fatalf("RenderAgentFile(%q): %v", body, err)
		}
		if strings.Contains(got, "effort:") {
			t.Errorf("unset effort must not render an effort: line, got:\n%s", got)
		}
	}
	// Every built-in ships without a pinned effort until a sweep says
	// otherwise — see docs/opus-5-prompting-guidance.md §3.
	for _, slug := range BuiltinTemplateSlugs() {
		if slug == BuiltinTemplatePreamble {
			continue
		}
		body := DefaultPromptBodyForBuiltinSlug(slug)
		if strings.TrimSpace(body) == "" {
			continue
		}
		out, err := RenderAgentFile(slug, BuiltinTemplateLabel(slug), body)
		if err != nil {
			t.Fatalf("RenderAgentFile(%q): %v", slug, err)
		}
		if strings.Contains(out, "effort:") {
			t.Errorf("built-in %q pins an effort; built-ins should inherit the session default:\n%s", slug, out)
		}
	}
}

// TestRenderAgentFile_FrontmatterInvalidEffort checks that a bad effort
// fails at install time rather than at spawn time, where a rejected
// agent file is far harder to diagnose.
func TestRenderAgentFile_FrontmatterInvalidEffort(t *testing.T) {
	for _, bad := range []string{"ultra", "HIGH", "0", "-1", "3.5", ""} {
		_, err := RenderAgentFile("custom", "Custom", "---\neffort: "+bad+"\n---\nHello.")
		if err == nil {
			t.Errorf("RenderAgentFile accepted invalid effort %q; want error", bad)
			continue
		}
		if !strings.Contains(err.Error(), "invalid effort") {
			t.Errorf("error for %q should mention `invalid effort`, got: %v", bad, err)
		}
	}
}

// TestRenderAgentFile_FrontmatterDuplicateEffort mirrors the duplicate
// `model:` guard.
func TestRenderAgentFile_FrontmatterDuplicateEffort(t *testing.T) {
	_, err := RenderAgentFile("custom", "Custom", "---\neffort: low\neffort: high\n---\nHello.")
	if err == nil {
		t.Fatal("RenderAgentFile accepted a duplicate `effort` frontmatter key; want error")
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
		BuiltinTemplateScope, BuiltinTemplatePlan, BuiltinTemplateDesign, BuiltinTemplateImplement,
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
