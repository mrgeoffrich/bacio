package cli

import (
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
