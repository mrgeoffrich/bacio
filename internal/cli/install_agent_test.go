package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

func tmpl(slug, name, body string, builtin bool) *store.PromptTemplate {
	return &store.PromptTemplate{Slug: slug, Name: name, Body: body, IsBuiltin: builtin}
}

// TestSelectAgentTemplatesNoArgs: with no args every dispatchable
// template is selected, and the reserved preamble + empty-body rows are
// excluded.
func TestSelectAgentTemplatesNoArgs(t *testing.T) {
	in := []*store.PromptTemplate{
		tmpl(model.BuiltinTemplatePreamble, "Preamble", "spawn the subagent", true),
		tmpl("plan", "Planning", "do the plan", true),
		tmpl("implement", "Implementing", "do the work", true),
		tmpl("blank", "Blank", "   ", false),
	}
	wanted, warnings, err := selectAgentTemplates(in, nil)
	if err != nil {
		t.Fatalf("selectAgentTemplates: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	got := map[string]bool{}
	for _, w := range wanted {
		got[w.Slug] = true
	}
	if !got["plan"] || !got["implement"] {
		t.Fatalf("expected plan + implement, got %v", got)
	}
	if got[model.BuiltinTemplatePreamble] {
		t.Fatal("preamble should never be selected for an agent file")
	}
	if got["blank"] {
		t.Fatal("empty-body template should be excluded")
	}
}

// TestSelectAgentTemplatesExplicitUnknown: an unknown slug is a hard
// error.
func TestSelectAgentTemplatesExplicitUnknown(t *testing.T) {
	in := []*store.PromptTemplate{tmpl("plan", "Planning", "do the plan", true)}
	if _, _, err := selectAgentTemplates(in, []string{"nope"}); err == nil {
		t.Fatal("expected error for unknown slug, got nil")
	}
	// The reserved preamble is not dispatchable even by explicit name.
	in = append(in, tmpl(model.BuiltinTemplatePreamble, "Preamble", "x", true))
	if _, _, err := selectAgentTemplates(in, []string{model.BuiltinTemplatePreamble}); err == nil {
		t.Fatal("expected error selecting the reserved preamble, got nil")
	}
}

// TestSelectAgentTemplatesUserPlaceholderWarns: a user template whose
// body still has a {{...}} placeholder is dropped with a warning, not a
// hard error.
func TestSelectAgentTemplatesUserPlaceholderWarns(t *testing.T) {
	in := []*store.PromptTemplate{
		tmpl("custom", "Custom", "work on {{issue_id}} now", false),
		tmpl("plan", "Planning", "do the plan", true),
	}
	wanted, warnings, err := selectAgentTemplates(in, nil)
	if err != nil {
		t.Fatalf("selectAgentTemplates: %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "custom") {
		t.Fatalf("expected one warning mentioning custom, got %v", warnings)
	}
	for _, w := range wanted {
		if w.Slug == "custom" {
			t.Fatal("placeholder-bearing user template should be dropped")
		}
	}
}

// TestInstallAgentWritesAllThreeArtefacts is the BACI-79 integration
// test: a single `install-agent --yes` invocation must write all three
// artefacts — the per-mode subagent files under .claude/agents/, the
// bacio hook groups merged into .claude/settings.json, and the `bacio`
// entry in .mcp.json.
func TestInstallAgentWritesAllThreeArtefacts(t *testing.T) {
	repo := initGitRepo(t)
	withCwd(t, repo)

	// Point openClient at a fresh on-disk DB so the built-in prompt
	// templates are seeded for planAgentFiles to read.
	dbPath := filepath.Join(t.TempDir(), "db.sqlite")
	prevDB := opts.dbPath
	opts.dbPath = dbPath
	t.Cleanup(func() { opts.dbPath = prevDB })
	// Seed the templates by opening + closing the store once.
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("seed store: %v", err)
	}
	_ = s.Close()

	cmd := newInstallAgentCmd()
	cmd.SetArgs([]string{"--yes"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install-agent --yes: %v", err)
	}

	// 1. Subagent files.
	agentsDir := filepath.Join(repo, agentFilesDir)
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		t.Fatalf("read agents dir: %v", err)
	}
	var agentFiles int
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "bacio-") && strings.HasSuffix(e.Name(), "-worker.md") {
			agentFiles++
		}
	}
	if agentFiles == 0 {
		t.Fatalf("expected at least one bacio-*-worker.md file, got %d", agentFiles)
	}

	// 2. Hooks merged into .claude/settings.json.
	settingsData, err := os.ReadFile(filepath.Join(repo, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	if !strings.Contains(string(settingsData), bacioHookMarker) {
		t.Fatalf("settings.json missing bacio hook marker; got:\n%s", settingsData)
	}

	// 3. bacio entry in .mcp.json.
	mcpData, err := os.ReadFile(filepath.Join(repo, ".mcp.json"))
	if err != nil {
		t.Fatalf("read .mcp.json: %v", err)
	}
	mcp := map[string]json.RawMessage{}
	if err := json.Unmarshal(mcpData, &mcp); err != nil {
		t.Fatalf("parse .mcp.json: %v", err)
	}
	servers := map[string]json.RawMessage{}
	if err := json.Unmarshal(mcp["mcpServers"], &servers); err != nil {
		t.Fatalf("parse mcpServers: %v", err)
	}
	if _, ok := servers[mcpServerName]; !ok {
		t.Fatalf(".mcp.json missing the %q server entry; got:\n%s", mcpServerName, mcpData)
	}
}

// TestRemovedInstallCommandsAreUnregistered guards the BACI-79 removal:
// the four retired verbs (install-agents, install-hooks, install-channel,
// install-sample-skills) must no longer be registered on the root
// command, and exactly the two surviving install verbs must be present.
func TestRemovedInstallCommandsAreUnregistered(t *testing.T) {
	r, _ := NewRoot()
	have := map[string]bool{}
	for _, c := range r.Commands() {
		have[c.Name()] = true
	}
	for _, gone := range []string{"install-agents", "install-hooks", "install-channel", "install-sample-skills"} {
		if have[gone] {
			t.Fatalf("retired command %q is still registered", gone)
		}
	}
	for _, want := range []string{"install-skill", "install-agent"} {
		if !have[want] {
			t.Fatalf("expected command %q to be registered", want)
		}
	}
}
