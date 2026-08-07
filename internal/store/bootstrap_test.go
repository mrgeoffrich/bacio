package store

import "testing"

// TestBootstrapRepoDefaultsSkipsWorkspaceCatchAlls pins the asymmetry in
// BootstrapRepoDefaults: a git repo is seeded with the two catch-all epics
// (maintenance + bugs) and a repo default pointing at maintenance, while a
// manual workspace gets the Kanban board and nothing else.
//
// The catch-alls exist to satisfy the Pipeline's "features are mandatory"
// rule, and the Pipeline is a git-repo surface. Seeding a pathless
// workspace with two epics nobody asked for just clutters its Epics tab.
func TestBootstrapRepoDefaultsSkipsWorkspaceCatchAlls(t *testing.T) {
	s := newTestStore(t)

	git, err := s.CreateRepo("GITR", "git-repo", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create git repo: %v", err)
	}
	// CreateRepo does not bootstrap — the caller does, on registration.
	if err := s.BootstrapRepoDefaults(git.ID); err != nil {
		t.Fatalf("BootstrapRepoDefaults(git): %v", err)
	}
	gitFeats, err := s.ListFeatures(git.ID, false)
	if err != nil {
		t.Fatalf("list git features: %v", err)
	}
	if len(gitFeats) != 2 {
		t.Fatalf("git repo feature count = %d, want 2 (maintenance + bugs)", len(gitFeats))
	}
	gitSettings, err := s.GetRepoSettings(git.ID)
	if err != nil {
		t.Fatalf("git settings: %v", err)
	}
	if gitSettings.DefaultFeatureID == nil {
		t.Fatal("git repo default feature is nil, want maintenance")
	}

	// CreateWorkspace bootstraps inline (nothing re-resolves a pathless row).
	ws, err := s.CreateWorkspace("WSPC", "Workspace")
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	wsFeats, err := s.ListFeatures(ws.ID, false)
	if err != nil {
		t.Fatalf("list workspace features: %v", err)
	}
	if len(wsFeats) != 0 {
		t.Fatalf("workspace feature count = %d, want 0 — no catch-alls on a manual workspace", len(wsFeats))
	}
	wsSettings, err := s.GetRepoSettings(ws.ID)
	if err != nil {
		t.Fatalf("workspace settings: %v", err)
	}
	if wsSettings.DefaultFeatureID != nil {
		t.Fatalf("workspace default feature = %v, want nil", wsSettings.DefaultFeatureID)
	}
	// The Kanban half of the bootstrap still runs — it is the workspace's
	// home surface, so skipping the epics must not skip the board.
	cols, err := s.ListKanbanColumns(ws.ID)
	if err != nil {
		t.Fatalf("list workspace columns: %v", err)
	}
	if len(cols) == 0 {
		t.Fatal("workspace has no Kanban columns — the board seed must still run")
	}

	// A NULL default is a supported state, not a hole: a new issue simply
	// resolves to no epic rather than erroring.
	featureID, feat, err := s.ResolveCreateIssueFeatureID(ws.ID, "")
	if err != nil {
		t.Fatalf("ResolveCreateIssueFeatureID on a workspace: %v", err)
	}
	if featureID != nil || feat != nil {
		t.Fatalf("workspace issue resolved to feature %v/%v, want nil (featureless is legal)", featureID, feat)
	}

	// Re-bootstrapping a workspace that has since grown its own epic and
	// default leaves it alone, exactly as it does for a git repo.
	custom, err := s.CreateFeature(ws.ID, "custom", "Custom", "", "", "")
	if err != nil {
		t.Fatalf("create workspace feature: %v", err)
	}
	if err := s.SetDefaultFeatureID(ws.ID, &custom.ID); err != nil {
		t.Fatalf("set workspace default: %v", err)
	}
	if err := s.BootstrapRepoDefaults(ws.ID); err != nil {
		t.Fatalf("BootstrapRepoDefaults(workspace, second): %v", err)
	}
	after, err := s.ListFeatures(ws.ID, false)
	if err != nil {
		t.Fatalf("list workspace features (second): %v", err)
	}
	if len(after) != 1 {
		t.Fatalf("workspace feature count after re-bootstrap = %d, want 1 (no catch-alls forced)", len(after))
	}
}
