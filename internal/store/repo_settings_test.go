package store

import (
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestRepoSurfaces_KindDefaults pins the resolution rule for the two
// per-space nav-surface gates: a column that was never written falls
// back to a default that depends on repos.kind, not to the Go zero
// value.
func TestRepoSurfaces_KindDefaults(t *testing.T) {
	s := newTestStore(t)
	gitRepo, err := s.CreateRepo("GITR", "gitr", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create git repo: %v", err)
	}
	wksp, err := s.CreateWorkspace("WKSP", "wksp")
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	want := func(t *testing.T, label string, got model.RepoSurfaces, agent, kanban bool) {
		t.Helper()
		if got.ShowAgentSurfaces != agent || got.ShowKanban != kanban {
			t.Fatalf("%s: got {agent=%v kanban=%v}, want {agent=%v kanban=%v}",
				label, got.ShowAgentSurfaces, got.ShowKanban, agent, kanban)
		}
	}

	// A git repo has a working tree, so agents can work in it: agent
	// surfaces on, Kanban off. A workspace is the mirror image.
	got, err := s.GetRepoSurfaces(gitRepo.ID)
	if err != nil {
		t.Fatalf("get (fresh git): %v", err)
	}
	want(t, "fresh git repo", got, true, false)

	got, err = s.GetRepoSurfaces(wksp.ID)
	if err != nil {
		t.Fatalf("get (fresh workspace): %v", err)
	}
	want(t, "fresh workspace", got, false, true)

	// THE TRAP. repo_settings rows are created lazily, so writing an
	// unrelated setting materialises the row with both gate columns
	// NULL. A reader that treated "row exists" as "columns are
	// authoritative" — or that relied on a column DEFAULT — would now
	// report {false,false} and blank the nav. The kind defaults must
	// survive the row springing into existence.
	feat, err := s.CreateFeature(gitRepo.ID, "catchall", "Catch-all", "", "", "")
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}
	if err := s.SetDefaultFeatureID(gitRepo.ID, &feat.ID); err != nil {
		t.Fatalf("set default feature: %v", err)
	}
	got, err = s.GetRepoSurfaces(gitRepo.ID)
	if err != nil {
		t.Fatalf("get (after unrelated write): %v", err)
	}
	want(t, "git repo after an unrelated repo_settings write", got, true, false)

	// Explicit values win over the kind default, in both directions —
	// including a workspace opting into the agent surfaces.
	if err := s.SetRepoShowAgentSurfaces(wksp.ID, true); err != nil {
		t.Fatalf("set show_agent_surfaces: %v", err)
	}
	if err := s.SetRepoShowKanban(gitRepo.ID, true); err != nil {
		t.Fatalf("set show_kanban: %v", err)
	}
	got, _ = s.GetRepoSurfaces(wksp.ID)
	want(t, "workspace with Agent Mode on", got, true, true)
	got, _ = s.GetRepoSurfaces(gitRepo.ID)
	want(t, "git repo with Kanban on", got, true, true)

	// Each setter names only its own column in the DO UPDATE, so a
	// sibling write must not clobber it — including the pre-existing
	// auto_ship and default_feature_id columns.
	if err := s.SetRepoAutoShip(gitRepo.ID, true); err != nil {
		t.Fatalf("set auto_ship: %v", err)
	}
	if err := s.SetRepoShowAgentSurfaces(gitRepo.ID, false); err != nil {
		t.Fatalf("set show_agent_surfaces on git repo: %v", err)
	}
	got, _ = s.GetRepoSurfaces(gitRepo.ID)
	want(t, "git repo after sibling writes", got, false, true)
	settings, err := s.GetRepoSettings(gitRepo.ID)
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	if !settings.AutoShip {
		t.Fatal("auto_ship was clobbered by a surface write")
	}
	if settings.DefaultFeatureID == nil || *settings.DefaultFeatureID != feat.ID {
		t.Fatalf("default_feature_id was clobbered by a surface write: %v", settings.DefaultFeatureID)
	}
	// The raw columns are exposed as *bool so a caller can tell "set to
	// false" from "never set" — the distinction the resolver turns on.
	if settings.ShowAgentSurfacesSet == nil || *settings.ShowAgentSurfacesSet {
		t.Fatalf("ShowAgentSurfacesSet = %v, want a pointer to false", settings.ShowAgentSurfacesSet)
	}

	// ListRepoSurfaces covers every repo, including ones with no
	// repo_settings row at all.
	if _, err := s.CreateRepo("BARE", "bare", t.TempDir(), ""); err != nil {
		t.Fatalf("create bare repo: %v", err)
	}
	all, err := s.ListRepoSurfaces()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("ListRepoSurfaces returned %d entries, want 3", len(all))
	}
	want(t, "BARE (no repo_settings row)", all["BARE"], true, false)
	want(t, "WKSP via list", all["WKSP"], true, true)
	want(t, "GITR via list", all["GITR"], false, true)
}

// TestRepoSurfaces_LegacyEmptyKind pins that a repos row whose kind was
// never populated reads as git, matching the column's own DEFAULT and
// Repo.IsPhantom's reasoning. Without this a legacy row would resolve
// as a workspace and lose its agent tabs.
func TestRepoSurfaces_LegacyEmptyKind(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("LEGA", "lega", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	if _, err := s.DB.Exec(`UPDATE repos SET kind = '' WHERE id = ?`, repo.ID); err != nil {
		t.Fatalf("blank the kind: %v", err)
	}
	got, err := s.GetRepoSurfaces(repo.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.ShowAgentSurfaces || got.ShowKanban {
		t.Fatalf("legacy empty kind: got {agent=%v kanban=%v}, want {true false} (reads as git)",
			got.ShowAgentSurfaces, got.ShowKanban)
	}
}

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
