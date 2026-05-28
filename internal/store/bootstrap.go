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

// BootstrapRepoDefaults makes a repo "feature-mandatory ready". If the
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
func (s *Store) BootstrapRepoDefaults(repoID int64) error {
	settings, err := s.GetRepoSettings(repoID)
	if err != nil {
		return err
	}
	if settings.DefaultFeatureID != nil {
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
