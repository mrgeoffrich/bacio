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

// TestSyncBackgroundEnabledDefaultsTrue locks in the BACI-89 inverted
// default: a missing sync.background_enabled value reads back as true
// (background sync is opt-OUT), and only the exact string "false"
// disables it.
func TestSyncBackgroundEnabledDefaultsTrue(t *testing.T) {
	s := newTestStore(t)

	// Never set → defaults to true.
	if v, err := s.GetSyncBackgroundEnabled(); err != nil || !v {
		t.Fatalf("GetSyncBackgroundEnabled(unset) = %v, %v; want true, nil", v, err)
	}
	// Explicit false disables.
	if err := s.SetSyncBackgroundEnabled(false); err != nil {
		t.Fatalf("SetSyncBackgroundEnabled(false): %v", err)
	}
	if v, _ := s.GetSyncBackgroundEnabled(); v {
		t.Fatal("after Set(false), GetSyncBackgroundEnabled = true, want false")
	}
	// Back to true.
	if err := s.SetSyncBackgroundEnabled(true); err != nil {
		t.Fatalf("SetSyncBackgroundEnabled(true): %v", err)
	}
	if v, _ := s.GetSyncBackgroundEnabled(); !v {
		t.Fatal("after Set(true), GetSyncBackgroundEnabled = false, want true")
	}
	// An unexpected stored value reads as true (only "false" disables).
	if err := s.SetAppSetting(syncBackgroundEnabledKey, "garbage"); err != nil {
		t.Fatalf("SetAppSetting: %v", err)
	}
	if v, _ := s.GetSyncBackgroundEnabled(); !v {
		t.Fatal("a non-\"false\" stored value should read as true")
	}
}

// TestArchivePreferencesDefaults — BACI-162. A fresh store reads
// archive.auto_enabled as TRUE (auto-archive is opt-OUT, matching the
// historical behaviour) and archive.retention_days as the embedded
// DefaultArchiveRetentionDays (7). A malformed stored retention value
// (non-numeric, out-of-range) reads back as the default — the
// defensive read style every other getter in this file uses.
func TestArchivePreferencesDefaults(t *testing.T) {
	s := newTestStore(t)

	if v, err := s.GetArchiveAutoEnabled(); err != nil || !v {
		t.Fatalf("GetArchiveAutoEnabled(unset) = %v, %v; want true, nil", v, err)
	}
	if v, err := s.GetArchiveRetentionDays(); err != nil || v != DefaultArchiveRetentionDays {
		t.Fatalf("GetArchiveRetentionDays(unset) = %d, %v; want %d, nil", v, err, DefaultArchiveRetentionDays)
	}

	// Garbage retention reads back as default.
	if err := s.SetAppSetting(archiveRetentionDaysKey, "garbage"); err != nil {
		t.Fatalf("set garbage: %v", err)
	}
	if v, _ := s.GetArchiveRetentionDays(); v != DefaultArchiveRetentionDays {
		t.Fatalf("garbage retention reads %d, want default %d", v, DefaultArchiveRetentionDays)
	}
	// Out-of-range reads back as default too.
	if err := s.SetAppSetting(archiveRetentionDaysKey, "99999"); err != nil {
		t.Fatalf("set huge: %v", err)
	}
	if v, _ := s.GetArchiveRetentionDays(); v != DefaultArchiveRetentionDays {
		t.Fatalf("out-of-range retention reads %d, want default %d", v, DefaultArchiveRetentionDays)
	}
}

// TestArchivePreferencesPersist — BACI-162 round-trip on both keys.
func TestArchivePreferencesPersist(t *testing.T) {
	s := newTestStore(t)
	if err := s.SetArchiveAutoEnabled(false); err != nil {
		t.Fatalf("set auto false: %v", err)
	}
	if v, _ := s.GetArchiveAutoEnabled(); v {
		t.Fatal("after Set(false), GetArchiveAutoEnabled = true, want false")
	}
	if err := s.SetArchiveAutoEnabled(true); err != nil {
		t.Fatalf("set auto true: %v", err)
	}
	if v, _ := s.GetArchiveAutoEnabled(); !v {
		t.Fatal("after Set(true), GetArchiveAutoEnabled = false, want true")
	}

	if err := s.SetArchiveRetentionDays(14); err != nil {
		t.Fatalf("set retention 14: %v", err)
	}
	if v, _ := s.GetArchiveRetentionDays(); v != 14 {
		t.Fatalf("after Set(14), GetArchiveRetentionDays = %d, want 14", v)
	}
}

// TestValidateArchiveRetentionDays — BACI-162 store-boundary
// validator. Zero / negatives / huge values are rejected; the valid
// range 1..3650 round-trips.
func TestValidateArchiveRetentionDays(t *testing.T) {
	for _, bad := range []int{0, -1, -100, 3651, 99999} {
		if _, err := ValidateArchiveRetentionDays(bad); err == nil {
			t.Errorf("ValidateArchiveRetentionDays(%d) = nil, want error", bad)
		}
	}
	for _, ok := range []int{1, 7, 30, 365, 3650} {
		got, err := ValidateArchiveRetentionDays(ok)
		if err != nil {
			t.Errorf("ValidateArchiveRetentionDays(%d) errored: %v", ok, err)
		}
		if got != ok {
			t.Errorf("ValidateArchiveRetentionDays(%d) = %d, want %d", ok, got, ok)
		}
	}
}

// TestMarkSyncFailedAndCompleted checks the BACI-89 last_sync_error
// bookkeeping: MarkSyncFailed records the message, MarkSyncCompleted
// clears it and stamps last_sync_at.
func TestMarkSyncFailedAndCompleted(t *testing.T) {
	s := newTestStore(t)

	const remote = "git@github.com:user/sync.git"
	if err := s.UpsertSyncRemote(remote, "/local/sync"); err != nil {
		t.Fatalf("UpsertSyncRemote: %v", err)
	}

	// Fresh row: no error, no last_sync_at.
	rec, err := s.GetSyncRemote(remote)
	if err != nil {
		t.Fatalf("GetSyncRemote: %v", err)
	}
	if rec.LastSyncError != nil || rec.LastSyncAt != nil {
		t.Fatalf("fresh sync_remote: error=%v lastSyncAt=%v; want both nil",
			rec.LastSyncError, rec.LastSyncAt)
	}

	// MarkSyncFailed records the message.
	if err := s.MarkSyncFailed(remote, "git push: non-fast-forward"); err != nil {
		t.Fatalf("MarkSyncFailed: %v", err)
	}
	rec, _ = s.GetSyncRemote(remote)
	if rec.LastSyncError == nil || *rec.LastSyncError != "git push: non-fast-forward" {
		t.Fatalf("after MarkSyncFailed, error = %v, want the message", rec.LastSyncError)
	}
	if rec.LastSyncAt != nil {
		t.Fatal("MarkSyncFailed must not touch last_sync_at")
	}

	// MarkSyncCompleted clears the error and stamps last_sync_at.
	if err := s.MarkSyncCompleted(remote); err != nil {
		t.Fatalf("MarkSyncCompleted: %v", err)
	}
	rec, _ = s.GetSyncRemote(remote)
	if rec.LastSyncError != nil {
		t.Fatalf("MarkSyncCompleted must clear last_sync_error, got %v", *rec.LastSyncError)
	}
	if rec.LastSyncAt == nil {
		t.Fatal("MarkSyncCompleted must stamp last_sync_at")
	}
}

// TestUIShippedSfxRoundTrip — BACI-240. Defaults to false; the boolean
// round-trips; an unexpected stored value reads back as false (the
// defensive read shape matching GetDisplayShowArchived).
func TestUIShippedSfxRoundTrip(t *testing.T) {
	s := newTestStore(t)

	// Never set → defaults to false.
	if v, err := s.GetUIShippedSfx(); err != nil || v {
		t.Fatalf("GetUIShippedSfx(unset) = %v, %v; want false, nil", v, err)
	}
	// Explicit true.
	if err := s.SetUIShippedSfx(true); err != nil {
		t.Fatalf("SetUIShippedSfx(true): %v", err)
	}
	if v, _ := s.GetUIShippedSfx(); !v {
		t.Fatal("after Set(true), GetUIShippedSfx = false, want true")
	}
	// Back to false.
	if err := s.SetUIShippedSfx(false); err != nil {
		t.Fatalf("SetUIShippedSfx(false): %v", err)
	}
	if v, _ := s.GetUIShippedSfx(); v {
		t.Fatal("after Set(false), GetUIShippedSfx = true, want false")
	}
	// A non-"true" stored value reads as false.
	if err := s.SetAppSetting(uiShippedSfxKey, "garbage"); err != nil {
		t.Fatalf("SetAppSetting: %v", err)
	}
	if v, _ := s.GetUIShippedSfx(); v {
		t.Fatal("a non-\"true\" stored value should read as false")
	}
}

// TestPromptTemplateSeedAndOverride checks that the migration seeds the
// built-in templates on first run, that the legacy SetPromptTemplate /
// GetPromptTemplate shims edit the prompt_templates rows in place, and
// that an empty body reverts a built-in to its embedded default.
func TestPromptTemplateSeedAndOverride(t *testing.T) {
	s := newTestStore(t)

	// Seed → built-in default body for every built-in slug.
	for _, slug := range model.BuiltinTemplateSlugs() {
		got, err := s.GetPromptTemplate(model.DispatchMode(slug))
		if err != nil {
			t.Fatalf("GetPromptTemplate(%s): %v", slug, err)
		}
		if got != model.DefaultPromptBodyForBuiltinSlug(slug) {
			t.Fatalf("seeded %s template = %q, want the built-in default", slug, got)
		}
	}

	// Custom override wins.
	const custom = "Plan {{issue_id}} carefully."
	if err := s.SetPromptTemplate(model.DispatchModePlan, custom); err != nil {
		t.Fatalf("SetPromptTemplate: %v", err)
	}
	if got, _ := s.GetPromptTemplate(model.DispatchModePlan); got != custom {
		t.Fatalf("custom plan template = %q, want %q", got, custom)
	}

	// Empty body resets the built-in → back to the default.
	if err := s.SetPromptTemplate(model.DispatchModePlan, ""); err != nil {
		t.Fatalf("SetPromptTemplate (clear): %v", err)
	}
	if got, _ := s.GetPromptTemplate(model.DispatchModePlan); got != model.DefaultPromptBodyForBuiltinSlug(model.BuiltinTemplatePlan) {
		t.Fatalf("cleared plan template = %q, want the default back", got)
	}

	// Untyped mode has no template.
	if got, _ := s.GetPromptTemplate(""); got != "" {
		t.Fatalf("GetPromptTemplate(\"\") = %q, want empty", got)
	}

	// AllPromptTemplates surfaces every registered template.
	all, err := s.AllPromptTemplates()
	if err != nil {
		t.Fatalf("AllPromptTemplates: %v", err)
	}
	for _, slug := range model.BuiltinTemplateSlugs() {
		if all[model.DispatchMode(slug)] == "" {
			t.Errorf("AllPromptTemplates missing/empty for %q", slug)
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
	if err := s.SetPromptTemplate(model.DispatchModePlan, "bad\x00body"); err == nil {
		t.Fatal("SetPromptTemplate(control char) = nil, want error")
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
