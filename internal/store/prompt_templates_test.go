package store

import (
	"strings"
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
	}
}

// TestGetDispatchPreambleReturnsBody locks the BACI-52 helper that the
// dispatch composer uses to fetch the preamble body. A fresh DB has
// the row seeded from the embedded default — the body must contain
// the load-bearing delegation phrase. Deleting the row degrades the
// helper to "" so ComposeDispatchPayload skips the preamble (the
// pre-BACI-52 shape).
func TestGetDispatchPreambleReturnsBody(t *testing.T) {
	s := newTestStore(t)
	body, err := s.GetDispatchPreamble()
	if err != nil {
		t.Fatalf("GetDispatchPreamble: %v", err)
	}
	if body == "" {
		t.Fatal("GetDispatchPreamble returned empty body on a fresh DB")
	}
	// BACI-76: the delegation contract tells the supervisor to spawn
	// the per-mode custom subagent named by the stub. The stub spells
	// each field as an XML-style tag — Task( front-and-centre, the
	// `<subagent_type>` stub tag, and the `subagent_type` Task arg name.
	for _, phrase := range []string{"Task(", "<subagent_type>", "subagent_type"} {
		if !strings.Contains(body, phrase) {
			t.Errorf("preamble body missing %q\nfull body:\n%s", phrase, body)
		}
	}
	// BACI-76 retired the `general-purpose` spawn — the preamble must no
	// longer name it.
	if strings.Contains(body, "general-purpose") {
		t.Errorf("preamble body still names the retired general-purpose subagent:\n%s", body)
	}
	if _, err := s.DeletePromptTemplate(model.BuiltinTemplatePreamble); err != nil {
		t.Fatalf("delete preamble: %v", err)
	}
	body, err = s.GetDispatchPreamble()
	if err != nil {
		t.Fatalf("GetDispatchPreamble after delete: %v", err)
	}
	if body != "" {
		t.Fatalf("GetDispatchPreamble after delete = %q, want \"\"", body)
	}
}

// TestPromptTemplatesAddRenameDelete covers the agent-facing verbs in
// one go: add a user template, rename it, delete a built-in, restore.
func TestPromptTemplatesAddRenameDelete(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.AddPromptTemplate(AddPromptTemplateIn{
		Slug: "spike",
		Name: "Spike",
		Body: "Spike on {{issue_id}}.",
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

// TestPromptTemplatesActionLabelRoundtrip exercises BACI-67: the
// action_label column round-trips through Add, Update, and the
// focused SetPromptTemplateActionLabel mutator, and the built-in
// seed step stamps an imperative override on every dispatchable
// built-in.
func TestPromptTemplatesActionLabelRoundtrip(t *testing.T) {
	s := newTestStore(t)

	// Built-in seed step: every dispatchable built-in has an imperative
	// action_label; the reserved preamble row has none (it never
	// reaches a dropdown).
	for _, slug := range model.BuiltinTemplateSlugs() {
		tmpl, err := s.GetPromptTemplateBySlug(slug)
		if err != nil {
			t.Fatalf("get %s: %v", slug, err)
		}
		want := model.BuiltinTemplateActionLabel(slug)
		if tmpl.ActionLabel != want {
			t.Errorf("%s: action_label = %q, want %q", slug, tmpl.ActionLabel, want)
		}
	}

	// Add with an explicit action_label.
	added, err := s.AddPromptTemplate(AddPromptTemplateIn{
		Slug:        "spike",
		Name:        "Spike",
		Body:        "Spike on {{issue_id}}.",
		ActionLabel: "Spike",
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if added.ActionLabel != "Spike" {
		t.Errorf("after add: action_label = %q, want %q", added.ActionLabel, "Spike")
	}

	// Focused mutator: clear the override (UI then derives from Name).
	cleared, err := s.SetPromptTemplateActionLabel("spike", "")
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if cleared.ActionLabel != "" {
		t.Errorf("after clear: action_label = %q, want \"\"", cleared.ActionLabel)
	}

	// Focused mutator: set a new override.
	set, err := s.SetPromptTemplateActionLabel("spike", "Investigate")
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if set.ActionLabel != "Investigate" {
		t.Errorf("after set: action_label = %q, want %q", set.ActionLabel, "Investigate")
	}

	// UpdatePromptTemplate honours the *string sentinel: nil = leave
	// alone, non-nil empty = clear.
	newLabel := "Spike review"
	updated, err := s.UpdatePromptTemplate("spike", UpdatePromptTemplatePatch{
		ActionLabel: &newLabel,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.ActionLabel != "Spike review" {
		t.Errorf("after update: action_label = %q, want %q", updated.ActionLabel, "Spike review")
	}
	// A nil patch leaves the field alone.
	bodyOnly := "Spike on {{issue_id}} (updated)."
	again, err := s.UpdatePromptTemplate("spike", UpdatePromptTemplatePatch{
		Body: &bodyOnly,
	})
	if err != nil {
		t.Fatalf("update body-only: %v", err)
	}
	if again.ActionLabel != "Spike review" {
		t.Errorf("after body-only update: action_label = %q, want preserved %q", again.ActionLabel, "Spike review")
	}

	// Validator: control characters are rejected.
	if _, err := s.SetPromptTemplateActionLabel("spike", "bad\x01label"); err == nil {
		t.Errorf("expected control-char rejection, got nil")
	}

	// Validator: missing slug surfaces as ErrNotFound.
	if _, err := s.SetPromptTemplateActionLabel("nope-no-such-slug", "x"); err != ErrNotFound {
		t.Errorf("expected ErrNotFound on missing slug, got %v", err)
	}
}

// TestPromptTemplatesMigrationFromAppSettings checks the one-shot fold:
// app_settings prompt_template.<slug> rows from a pre-BACI-31 DB land
// in the new table. BACI-252 retired the per-template state-gate, so
// the migration also discards any legacy `prompt_states.*` KV row it
// finds rather than folding it through.
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
	// BACI-252: a stale prompt_states.* KV row from a pre-BACI-252 DB
	// must be silently dropped on migrate (no fold, no error).
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

	// Both legacy KV rows must be removed after the fold (the
	// state-gate row is dropped rather than folded; BACI-252).
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

// TestMigrateDropAllowedStatesJSON_Idempotent (BACI-252) pins the
// schema drop: running the migration on a pre-drop schema removes the
// column, and a second run on the post-drop schema is a clean no-op.
// Fresh DBs already have the column gone (schema.sql declares the
// new shape), so the migration should still no-op them without an
// error.
func TestMigrateDropAllowedStatesJSON_Idempotent(t *testing.T) {
	s := newTestStore(t)

	// Fresh test DB starts in the post-drop shape — the column is gone.
	gone, err := columnExists(s.DB, "prompt_templates", "allowed_states_json")
	if err != nil {
		t.Fatalf("columnExists (fresh): %v", err)
	}
	if gone {
		t.Fatal("fresh test DB still has allowed_states_json column — schema.sql leaked")
	}

	// Run the migration on the post-drop schema: no-op, no error.
	if err := migrateDropAllowedStatesJSON(s.DB); err != nil {
		t.Fatalf("migrate (post-drop, no-op): %v", err)
	}

	// Now simulate a pre-drop DB by re-adding the legacy column with
	// the old default — running the migration must drop it again.
	if _, err := s.DB.Exec(`ALTER TABLE prompt_templates ADD COLUMN allowed_states_json TEXT NOT NULL DEFAULT '[]'`); err != nil {
		t.Fatalf("simulate pre-drop schema: %v", err)
	}
	hasIt, err := columnExists(s.DB, "prompt_templates", "allowed_states_json")
	if err != nil {
		t.Fatalf("columnExists (pre-drop): %v", err)
	}
	if !hasIt {
		t.Fatal("simulated pre-drop ALTER did not stick")
	}
	if err := migrateDropAllowedStatesJSON(s.DB); err != nil {
		t.Fatalf("migrate (pre-drop): %v", err)
	}
	gone, err = columnExists(s.DB, "prompt_templates", "allowed_states_json")
	if err != nil {
		t.Fatalf("columnExists (post-migrate): %v", err)
	}
	if gone {
		t.Fatal("allowed_states_json still present after migrate")
	}

	// One more re-run is still a clean no-op.
	if err := migrateDropAllowedStatesJSON(s.DB); err != nil {
		t.Fatalf("migrate (idempotent rerun): %v", err)
	}
}
