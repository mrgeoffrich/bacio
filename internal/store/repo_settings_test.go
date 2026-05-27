package store

import (
	"testing"
)

// TestRepoSettings_DefaultFeatureRoundTrip exercises the BACI-235
// default_feature_id round-trip: defaults to nil on a fresh repo,
// flips on, flips back off, and ClearDefaultFeature has the same
// effect as SetDefaultFeatureID(nil).
func TestRepoSettings_DefaultFeatureRoundTrip(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("RS", "rs", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	feat, err := s.CreateFeature(repo.ID, "catchall", "Catch-all", "", "", "")
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}

	// Default: no row, no default.
	got, err := s.GetRepoSettings(repo.ID)
	if err != nil {
		t.Fatalf("get (fresh): %v", err)
	}
	if got.DefaultFeatureID != nil {
		t.Fatalf("fresh repo: DefaultFeatureID = %v, want nil", *got.DefaultFeatureID)
	}
	if got.RepoID != repo.ID {
		t.Fatalf("fresh repo: RepoID = %d, want %d", got.RepoID, repo.ID)
	}

	// Set.
	if err := s.SetDefaultFeatureID(repo.ID, &feat.ID); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err = s.GetRepoSettings(repo.ID)
	if err != nil {
		t.Fatalf("get (after set): %v", err)
	}
	if got.DefaultFeatureID == nil || *got.DefaultFeatureID != feat.ID {
		t.Fatalf("after set: DefaultFeatureID = %v, want %d", got.DefaultFeatureID, feat.ID)
	}

	// Idempotent set (same value).
	if err := s.SetDefaultFeatureID(repo.ID, &feat.ID); err != nil {
		t.Fatalf("set (idempotent): %v", err)
	}
	got, _ = s.GetRepoSettings(repo.ID)
	if got.DefaultFeatureID == nil || *got.DefaultFeatureID != feat.ID {
		t.Fatalf("after idempotent set: lost the value")
	}

	// Clear via the explicit verb.
	if err := s.ClearDefaultFeature(repo.ID); err != nil {
		t.Fatalf("clear: %v", err)
	}
	got, _ = s.GetRepoSettings(repo.ID)
	if got.DefaultFeatureID != nil {
		t.Fatalf("after clear: DefaultFeatureID = %v, want nil", *got.DefaultFeatureID)
	}

	// Set then clear via SetDefaultFeatureID(nil) — same effect as ClearDefaultFeature.
	if err := s.SetDefaultFeatureID(repo.ID, &feat.ID); err != nil {
		t.Fatalf("set (re-write): %v", err)
	}
	if err := s.SetDefaultFeatureID(repo.ID, nil); err != nil {
		t.Fatalf("set(nil): %v", err)
	}
	got, _ = s.GetRepoSettings(repo.ID)
	if got.DefaultFeatureID != nil {
		t.Fatalf("after set(nil): DefaultFeatureID = %v, want nil", *got.DefaultFeatureID)
	}
}

// TestRepoSettings_CascadesOnFeatureDelete verifies that deleting the
// referenced feature clears default_feature_id back to NULL via the
// ON DELETE SET NULL FK — the "stored column never carries a dead
// reference" invariant the BACI-235 plan relies on.
func TestRepoSettings_CascadesOnFeatureDelete(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("RS", "rs", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	feat, err := s.CreateFeature(repo.ID, "catchall", "Catch-all", "", "", "")
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}
	if err := s.SetDefaultFeatureID(repo.ID, &feat.ID); err != nil {
		t.Fatalf("set: %v", err)
	}

	// Confirm the row exists with the FK populated.
	got, _ := s.GetRepoSettings(repo.ID)
	if got.DefaultFeatureID == nil || *got.DefaultFeatureID != feat.ID {
		t.Fatalf("setup: expected feature %d, got %v", feat.ID, got.DefaultFeatureID)
	}

	// Delete the feature. The row in repo_settings stays (it's keyed
	// by repo_id, not the feature), but default_feature_id flips to
	// NULL.
	if err := s.DeleteFeature(feat.ID); err != nil {
		t.Fatalf("delete feature: %v", err)
	}
	got, err = s.GetRepoSettings(repo.ID)
	if err != nil {
		t.Fatalf("get (after feature delete): %v", err)
	}
	if got.DefaultFeatureID != nil {
		t.Fatalf("after feature delete: DefaultFeatureID = %v, want nil (FK cascade)", *got.DefaultFeatureID)
	}
}

// TestRepoSettings_CascadesOnRepoDelete verifies that deleting the
// repo cascades the repo_settings row away. Mirrors the tui_settings
// cascade so the two per-repo settings tables behave consistently.
func TestRepoSettings_CascadesOnRepoDelete(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("RS", "rs", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	feat, err := s.CreateFeature(repo.ID, "catchall", "Catch-all", "", "", "")
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}
	if err := s.SetDefaultFeatureID(repo.ID, &feat.ID); err != nil {
		t.Fatalf("set: %v", err)
	}

	if err := s.DeleteRepo(repo.ID); err != nil {
		t.Fatalf("delete repo: %v", err)
	}

	// Re-read against the (now-deleted) repo id — should return the
	// zero-value row, not error.
	got, err := s.GetRepoSettings(repo.ID)
	if err != nil {
		t.Fatalf("get (after repo delete): %v", err)
	}
	if got.DefaultFeatureID != nil {
		t.Fatalf("after repo delete: DefaultFeatureID = %v, want nil", *got.DefaultFeatureID)
	}

	// Confirm there really is no row left (vs a row with NULL).
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM repo_settings WHERE repo_id = ?`, repo.ID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("after repo delete: %d rows remain, want 0", n)
	}
}
