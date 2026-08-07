package store

import (
	"errors"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// CatchAllFeatureSlugs are the two features a repo is bootstrapped with
// when the Pipeline makes features mandatory: maintenance (the repo
// default — the home for general non-pipeline issues) and bugs. Both run
// with auto-close off so the BACI-199 completion sweep never archives a
// catch-all out from under newly-filed issues.
const (
	CatchAllFeatureMaintenance = "maintenance"
	CatchAllFeatureBugs        = "bugs"
)

// BootstrapRepoDefaults makes a GIT repo "feature-mandatory ready". If the
// repo has no default feature yet, it creates the two catch-all features
// (maintenance + bugs, auto-close off), points the repo default at
// maintenance, and backfills any feature-less issue onto that default so
// every issue belongs to a feature (the Features page is the home for
// non-pipeline issues).
//
// Idempotent and cheap on the hot path: once a default feature is set the
// function returns after a single read, so it is safe to call on every
// repo resolve. A repo that already carries a deliberate default (set by
// the user via `bacio settings default-feature`) is left untouched — we
// never force the catch-alls onto a repo that has opted into its own
// feature workflow.
//
// A manual workspace gets the Kanban seed and nothing else. "Features are
// mandatory" is a Pipeline rule, and the Pipeline is a git-repo surface
// (ResolveRepoSurfaces hides the agent tabs on a workspace by default), so
// seeding a pathless workspace with Maintenance + Bugs only lands two epics
// nobody asked for on its Epics tab. A featureless issue is legal
// everywhere — ResolveCreateIssueFeatureID returns a nil feature id when the
// repo carries no default — so leaving the default NULL is a supported
// state, not a hole. Epics can still be created on demand
// (`bacio feature add`, the TUI Features tab).
//
// Workspaces created BEFORE this rule keep their catch-alls: they already
// carry a default feature and take the early-return above, so nothing
// retroactively deletes them.
//
// The Kanban seed runs BEFORE both early-returns on purpose. The axes are
// independent: a repo that already carries a deliberate default feature has
// never been given a Kanban board, and gating the seed behind that return
// would leave every pre-existing repo boardless forever. A workspace needs
// the board most of all — it is the workspace's home surface.
// BootstrapKanbanColumns carries its own count-guard, so the unconditional
// call stays a single cheap read once the board exists.
func (s *Store) BootstrapRepoDefaults(repoID int64) error {
	if err := s.BootstrapKanbanColumns(repoID); err != nil {
		return err
	}
	settings, err := s.GetRepoSettings(repoID)
	if err != nil {
		return err
	}
	if settings.DefaultFeatureID != nil {
		return nil
	}
	// The kind read sits after the default-feature early-return so the
	// already-bootstrapped case (every repeat call) still costs one query.
	repo, err := s.GetRepoByID(repoID)
	if err != nil {
		return err
	}
	if repo.IsWorkspace() {
		return nil
	}
	maint, err := s.ensureCatchAllFeature(repoID, CatchAllFeatureMaintenance, "Maintenance")
	if err != nil {
		return err
	}
	if _, err := s.ensureCatchAllFeature(repoID, CatchAllFeatureBugs, "Bugs"); err != nil {
		return err
	}
	if err := s.SetDefaultFeatureID(repoID, &maint.ID); err != nil {
		return err
	}
	return s.BackfillFeaturelessIssues(repoID, maint.ID)
}

// ensureCatchAllFeature returns the repo's feature with the given slug,
// creating it (auto-close off) when absent. Idempotent.
func (s *Store) ensureCatchAllFeature(repoID int64, slug, title string) (*model.Feature, error) {
	feat, err := s.GetFeatureBySlug(repoID, slug)
	if err == nil {
		return feat, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	feat, err = s.CreateFeature(repoID, slug, title, "", "", "")
	if err != nil {
		return nil, err
	}
	// Auto-close OFF (state_manual = 1): a long-lived catch-all must not
	// be promoted to done by the completion sweep just because its
	// children all happen to be terminal at some point.
	if err := s.SetFeatureAutoClose(feat.ID, false); err != nil {
		return nil, err
	}
	return s.GetFeatureByID(feat.ID)
}

// BackfillFeaturelessIssues assigns every feature-less issue in the repo
// to featureID. Bumps updated_at because feature membership is real,
// syncable content (unlike the runtime-metadata writers that
// deliberately skip the bump). Idempotent: a second call matches no rows.
func (s *Store) BackfillFeaturelessIssues(repoID, featureID int64) error {
	_, err := s.DB.Exec(
		`UPDATE issues SET feature_id = ?, updated_at = CURRENT_TIMESTAMP WHERE repo_id = ? AND feature_id IS NULL`,
		featureID, repoID,
	)
	return err
}
