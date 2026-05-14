package store

import (
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestAppSettingRoundTrip locks in the generic global-KV behaviour:
// a missing key reads back empty (not an error), and a re-set upserts.
func TestAppSettingRoundTrip(t *testing.T) {
	s := newTestStore(t)

	if v, err := s.GetAppSetting("nope"); err != nil || v != "" {
		t.Fatalf("GetAppSetting(missing) = %q, %v; want \"\", nil", v, err)
	}
	if err := s.SetAppSetting("k", "v1"); err != nil {
		t.Fatalf("SetAppSetting: %v", err)
	}
	if v, _ := s.GetAppSetting("k"); v != "v1" {
		t.Fatalf("GetAppSetting after set = %q, want v1", v)
	}
	if err := s.SetAppSetting("k", "v2"); err != nil {
		t.Fatalf("SetAppSetting (update): %v", err)
	}
	if v, _ := s.GetAppSetting("k"); v != "v2" {
		t.Fatalf("GetAppSetting after update = %q, want v2", v)
	}
}

// TestPromptTemplateDefaultFallback checks that an unset stage resolves
// to its built-in default, a custom body overrides it, and clearing the
// body (empty string) reverts to the default.
func TestPromptTemplateDefaultFallback(t *testing.T) {
	s := newTestStore(t)

	// Unset → built-in default.
	got, err := s.GetPromptTemplate(model.DispatchModePlan)
	if err != nil {
		t.Fatalf("GetPromptTemplate: %v", err)
	}
	if got != model.DefaultPromptTemplate(model.DispatchModePlan) {
		t.Fatalf("unset plan template = %q, want the built-in default", got)
	}

	// Custom override wins.
	const custom = "Plan {{issue_id}} carefully."
	if err := s.SetPromptTemplate(model.DispatchModePlan, custom); err != nil {
		t.Fatalf("SetPromptTemplate: %v", err)
	}
	if got, _ := s.GetPromptTemplate(model.DispatchModePlan); got != custom {
		t.Fatalf("custom plan template = %q, want %q", got, custom)
	}

	// Empty body clears the override → back to the default.
	if err := s.SetPromptTemplate(model.DispatchModePlan, ""); err != nil {
		t.Fatalf("SetPromptTemplate (clear): %v", err)
	}
	if got, _ := s.GetPromptTemplate(model.DispatchModePlan); got != model.DefaultPromptTemplate(model.DispatchModePlan) {
		t.Fatalf("cleared plan template = %q, want the default back", got)
	}

	// Untyped mode has no template.
	if got, _ := s.GetPromptTemplate(""); got != "" {
		t.Fatalf("GetPromptTemplate(\"\") = %q, want empty", got)
	}

	// AllPromptTemplates resolves every stage to a non-empty body.
	all, err := s.AllPromptTemplates()
	if err != nil {
		t.Fatalf("AllPromptTemplates: %v", err)
	}
	for _, m := range model.AllDispatchModes() {
		if all[m] == "" {
			t.Errorf("AllPromptTemplates missing/empty for %q", m)
		}
	}
}

// TestSetPromptTemplateRejectsBadInput locks in store-boundary
// validation: an untyped mode and a control-char body are both refused.
func TestSetPromptTemplateRejectsBadInput(t *testing.T) {
	s := newTestStore(t)

	if err := s.SetPromptTemplate("", "anything"); err == nil {
		t.Fatal("SetPromptTemplate(\"\", ...) = nil, want error")
	}
	if err := s.SetPromptTemplate("refactor", "anything"); err == nil {
		t.Fatal("SetPromptTemplate(unknown mode) = nil, want error")
	}
	if err := s.SetPromptTemplate(model.DispatchModePlan, "bad\x00body"); err == nil {
		t.Fatal("SetPromptTemplate(control char) = nil, want error")
	}
}

// TestPromptStatesDefaultFallback checks the state-gate analogue of the
// template fallback: an unset stage resolves to its built-in default, a
// custom set overrides it, and clearing it (empty slice) reverts.
func TestPromptStatesDefaultFallback(t *testing.T) {
	s := newTestStore(t)

	// Unset → built-in default.
	got, err := s.GetPromptStates(model.DispatchModePlan)
	if err != nil {
		t.Fatalf("GetPromptStates: %v", err)
	}
	if len(got) != 1 || got[0] != model.StateTodo {
		t.Fatalf("unset plan state-gate = %v, want [todo]", got)
	}

	// Custom override wins.
	custom := []model.State{model.StateTodo, model.StateInProgress}
	if err := s.SetPromptStates(model.DispatchModePlan, custom); err != nil {
		t.Fatalf("SetPromptStates: %v", err)
	}
	got, _ = s.GetPromptStates(model.DispatchModePlan)
	if len(got) != 2 || got[0] != model.StateTodo || got[1] != model.StateInProgress {
		t.Fatalf("custom plan state-gate = %v, want [todo in_progress]", got)
	}

	// Empty slice clears the override → back to the default.
	if err := s.SetPromptStates(model.DispatchModePlan, nil); err != nil {
		t.Fatalf("SetPromptStates (clear): %v", err)
	}
	got, _ = s.GetPromptStates(model.DispatchModePlan)
	if len(got) != 1 || got[0] != model.StateTodo {
		t.Fatalf("cleared plan state-gate = %v, want [todo] back", got)
	}

	// Untyped mode has no gate.
	if got, _ := s.GetPromptStates(""); got != nil {
		t.Fatalf("GetPromptStates(\"\") = %v, want nil", got)
	}

	// AllPromptStates resolves every stage to a non-empty set.
	all, err := s.AllPromptStates()
	if err != nil {
		t.Fatalf("AllPromptStates: %v", err)
	}
	for _, m := range model.AllDispatchModes() {
		if len(all[m]) == 0 {
			t.Errorf("AllPromptStates missing/empty for %q", m)
		}
	}
}

// TestSetPromptStatesRejectsBadInput locks in store-boundary validation
// for the state-gate: an untyped mode, an unknown state, and a duplicate
// state are all refused.
func TestSetPromptStatesRejectsBadInput(t *testing.T) {
	s := newTestStore(t)

	if err := s.SetPromptStates("", []model.State{model.StateTodo}); err == nil {
		t.Fatal("SetPromptStates(\"\", ...) = nil, want error")
	}
	if err := s.SetPromptStates(model.DispatchModePlan, []model.State{"bogus"}); err == nil {
		t.Fatal("SetPromptStates(unknown state) = nil, want error")
	}
	if err := s.SetPromptStates(model.DispatchModePlan, []model.State{model.StateTodo, model.StateTodo}); err == nil {
		t.Fatal("SetPromptStates(duplicate state) = nil, want error")
	}
}

// TestMigrateAgentDispatchesModeCheck simulates a plan/implement-era DB
// that still carries CHECK (mode IN ('','plan','implement')) on
// agent_dispatches, and asserts the migration rebuilds the table so the
// new review/ship/fix_review stages insert cleanly.
func TestMigrateAgentDispatchesModeCheck(t *testing.T) {
	s, repo, iss, ag, _ := seedDispatchFixture(t)

	// A fresh DB has no mode CHECK — the migration is a no-op.
	if present, err := agentDispatchesModeCheckPresent(s.DB); err != nil || present {
		t.Fatalf("fresh DB: modeCheckPresent = %v, %v; want false, nil", present, err)
	}

	// Recreate agent_dispatches with the old strict CHECK to mimic an
	// early-generation DB. The table is empty at this point in the
	// fixture, so a drop+recreate loses nothing.
	if _, err := s.DB.Exec(`DROP TABLE agent_dispatches`); err != nil {
		t.Fatalf("drop agent_dispatches: %v", err)
	}
	if _, err := s.DB.Exec(`
		CREATE TABLE agent_dispatches (
			id                INTEGER PRIMARY KEY AUTOINCREMENT,
			repo_id           INTEGER NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
			target_agent_id   INTEGER REFERENCES agents(id) ON DELETE CASCADE,
			target_session_id TEXT    NOT NULL DEFAULT '',
			issue_id          INTEGER REFERENCES issues(id) ON DELETE SET NULL,
			mode              TEXT    NOT NULL DEFAULT ''
			                    CHECK (mode IN ('','plan','implement')),
			payload           TEXT    NOT NULL DEFAULT '',
			status            TEXT    NOT NULL DEFAULT 'pending'
			                    CHECK (status IN ('pending','delivered','acked','cancelled')),
			created_by        TEXT    NOT NULL,
			created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			delivered_at      DATETIME,
			acked_at          DATETIME,
			ack_note          TEXT    NOT NULL DEFAULT '',
			CHECK (target_agent_id IS NOT NULL OR target_session_id != '')
		)`); err != nil {
		t.Fatalf("recreate old agent_dispatches: %v", err)
	}

	if present, err := agentDispatchesModeCheckPresent(s.DB); err != nil || !present {
		t.Fatalf("old-shape DB: modeCheckPresent = %v, %v; want true, nil", present, err)
	}

	// Before the migration, a 'review' dispatch is rejected by the CHECK.
	if _, err := s.DB.Exec(
		`INSERT INTO agent_dispatches (repo_id, target_agent_id, mode, created_by) VALUES (?, ?, 'review', 'tester')`,
		repo.ID, ag.ID,
	); err == nil {
		t.Fatal("insert mode='review' on old-shape DB succeeded, want CHECK violation")
	}

	if err := migrateAgentDispatchesModeCheck(s.DB); err != nil {
		t.Fatalf("migrateAgentDispatchesModeCheck: %v", err)
	}
	if present, err := agentDispatchesModeCheckPresent(s.DB); err != nil || present {
		t.Fatalf("after migration: modeCheckPresent = %v, %v; want false, nil", present, err)
	}

	// The new stages now insert cleanly through the normal store path.
	issueID := iss.ID
	d, err := s.AddDispatch(AddDispatchIn{
		RepoID:        repo.ID,
		TargetAgentID: &ag.ID,
		IssueID:       &issueID,
		Mode:          model.DispatchModeReview,
		Payload:       "review it",
		CreatedBy:     "tester",
	})
	if err != nil {
		t.Fatalf("AddDispatch(review) after migration: %v", err)
	}
	if d.Mode != model.DispatchModeReview {
		t.Fatalf("dispatch mode = %q, want review", d.Mode)
	}

	// Migration is idempotent — a second pass is a clean no-op.
	if err := migrateAgentDispatchesModeCheck(s.DB); err != nil {
		t.Fatalf("migrateAgentDispatchesModeCheck (second pass): %v", err)
	}
}
