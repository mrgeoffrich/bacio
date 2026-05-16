package store

import (
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestPromptTemplatesSeededOnFirstOpen locks in the BACI-31 migration:
// a fresh DB has exactly the bundled built-in templates, each with the
// embedded default body and state-gate.
func TestPromptTemplatesSeededOnFirstOpen(t *testing.T) {
	s := newTestStore(t)
	tmpls, err := s.ListPromptTemplates()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tmpls) != len(model.BuiltinTemplateSlugs()) {
		t.Fatalf("expected %d templates seeded, got %d", len(model.BuiltinTemplateSlugs()), len(tmpls))
	}
	gotSlugs := make([]string, len(tmpls))
	for i, t := range tmpls {
		gotSlugs[i] = t.Slug
	}
	for i, want := range model.BuiltinTemplateSlugs() {
		if gotSlugs[i] != want {
			t.Errorf("seed order: pos %d = %q, want %q", i, gotSlugs[i], want)
		}
	}
	for _, tmpl := range tmpls {
		if !tmpl.IsBuiltin {
			t.Errorf("%s: IsBuiltin = false, want true", tmpl.Slug)
		}
		if tmpl.Body == "" {
			t.Errorf("%s: empty body", tmpl.Slug)
		}
		if len(tmpl.AllowedStates) == 0 {
			t.Errorf("%s: empty allowed_states", tmpl.Slug)
		}
	}
}

// TestPromptTemplatesAddRenameDelete covers the agent-facing verbs in
// one go: add a user template, rename it, delete a built-in, restore.
func TestPromptTemplatesAddRenameDelete(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.AddPromptTemplate(AddPromptTemplateIn{
		Slug:          "spike",
		Name:          "Spike",
		Body:          "Spike on {{issue_id}}.",
		AllowedStates: []model.State{model.StateTodo},
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	// Slug uniqueness is enforced.
	if _, err := s.AddPromptTemplate(AddPromptTemplateIn{
		Slug: "spike", Name: "Other", Body: "x",
	}); err == nil {
		t.Fatal("expected error on duplicate slug, got nil")
	}
	// Case-insensitive name uniqueness is enforced.
	if _, err := s.AddPromptTemplate(AddPromptTemplateIn{
		Slug: "other", Name: "SPIKE", Body: "x",
	}); err == nil {
		t.Fatal("expected error on duplicate name (case-insensitive), got nil")
	}

	// Rename: slug + name. The cascade to agent_dispatches.mode is
	// covered indirectly — we just confirm the prompt_templates row
	// updates and the no-op-rename guard fires when nothing changes.
	r, err := s.RenamePromptTemplate(RenamePromptTemplateIn{
		OldSlug: "spike", NewSlug: "investigation", NewName: "Investigation",
	})
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if r.Slug != "investigation" || r.Name != "Investigation" {
		t.Fatalf("after rename: %+v", r)
	}
	if _, err := s.GetPromptTemplateBySlug("spike"); err != ErrNotFound {
		t.Fatalf("expected old slug to be gone, got %v", err)
	}
	if _, err := s.RenamePromptTemplate(RenamePromptTemplateIn{
		OldSlug: "investigation", NewSlug: "investigation",
	}); err == nil {
		t.Fatal("expected no-op rename to error, got nil")
	}

	// Delete a built-in — should succeed (built-ins are deletable).
	if _, err := s.DeletePromptTemplate("fix_review"); err != nil {
		t.Fatalf("delete built-in: %v", err)
	}
	if _, err := s.GetPromptTemplateBySlug("fix_review"); err != ErrNotFound {
		t.Fatalf("expected fix_review to be gone, got %v", err)
	}

	// Restore-defaults re-seeds only the missing slug.
	created, err := s.RestoreBuiltinPromptTemplates()
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if len(created) != 1 || created[0] != "fix_review" {
		t.Fatalf("restore created %v, want [fix_review]", created)
	}
	created2, err := s.RestoreBuiltinPromptTemplates()
	if err != nil {
		t.Fatalf("restore (idempotent): %v", err)
	}
	if len(created2) != 0 {
		t.Fatalf("second restore created %v, want nothing", created2)
	}
}

// TestPromptTemplatesMigrationFromAppSettings checks the one-shot fold:
// app_settings prompt_template.<slug> / prompt_states.<slug> rows from
// a pre-BACI-31 DB land in the new table.
func TestPromptTemplatesMigrationFromAppSettings(t *testing.T) {
	s := newTestStore(t)

	// Clear the seeded rows so we can simulate "fresh table + legacy KV
	// rows present" — the second branch of the migration gate. Wipe
	// the table directly with SQL to avoid the recordOp-driven helpers.
	if _, err := s.DB.Exec(`DELETE FROM prompt_templates`); err != nil {
		t.Fatalf("wipe templates: %v", err)
	}
	if err := s.SetAppSetting("prompt_template.plan", "Custom plan body for {{issue_id}}."); err != nil {
		t.Fatalf("seed legacy template key: %v", err)
	}
	if err := s.SetAppSetting("prompt_states.plan", "todo,in_progress"); err != nil {
		t.Fatalf("seed legacy states key: %v", err)
	}

	if err := migratePromptTemplates(s.DB); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	got, err := s.GetPromptTemplateBySlug("plan")
	if err != nil {
		t.Fatalf("get after migrate: %v", err)
	}
	if got.Body != "Custom plan body for {{issue_id}}." {
		t.Errorf("body after fold = %q, want the custom body", got.Body)
	}
	if len(got.AllowedStates) != 2 || got.AllowedStates[0] != model.StateTodo || got.AllowedStates[1] != model.StateInProgress {
		t.Errorf("states after fold = %v, want [todo in_progress]", got.AllowedStates)
	}

	// Legacy KV rows must be removed after the fold.
	if v, _ := s.GetAppSetting("prompt_template.plan"); v != "" {
		t.Errorf("legacy template key still present: %q", v)
	}
	if v, _ := s.GetAppSetting("prompt_states.plan"); v != "" {
		t.Errorf("legacy states key still present: %q", v)
	}

	// A subsequent migration call is a no-op (table non-empty).
	if err := migratePromptTemplates(s.DB); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}
