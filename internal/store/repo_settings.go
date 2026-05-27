package store

import (
	"database/sql"
	"errors"
)

// RepoSettings is the per-repo typed-settings view (BACI-235). Today
// the only column is DefaultFeatureID — the per-repo `default_feature`
// setting that auto-applies to issues created without an explicit
// `feature_slug`. nil = no default (the historical behaviour); a non-
// nil value points at a live feature row in the same repo (the FK is
// ON DELETE SET NULL so the column auto-clears when the referenced
// feature is deleted).
type RepoSettings struct {
	RepoID           int64
	DefaultFeatureID *int64
}

// GetRepoSettings reads the per-repo settings row for repoID. Returns
// a zero-valued RepoSettings (with RepoID set) when no row exists —
// the "no per-repo settings have ever been written" case is read as
// "every column is its zero value", not as an error. Matches the
// defensive read pattern of GetDisplayShowArchived / friends.
func (s *Store) GetRepoSettings(repoID int64) (RepoSettings, error) {
	out := RepoSettings{RepoID: repoID}
	var defaultFeatureID sql.NullInt64
	err := s.DB.QueryRow(
		`SELECT default_feature_id FROM repo_settings WHERE repo_id = ?`,
		repoID,
	).Scan(&defaultFeatureID)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if defaultFeatureID.Valid {
		v := defaultFeatureID.Int64
		out.DefaultFeatureID = &v
	}
	return out, nil
}

// SetDefaultFeatureID writes the per-repo default_feature_id column.
// nil clears the column (the "no default" semantic). The caller is
// responsible for verifying that featureID refers to a live feature
// row in the same repo — pass the result of GetFeatureBySlug.ID, not
// a slug. The FK ON DELETE SET NULL handles the post-write cascade if
// the feature is later deleted.
//
// Upsert shape so the first write doesn't need a separate INSERT
// path. updated_at is bumped on every write (including idempotent
// writes of the same value) — a deliberately conservative choice so
// the audit log + UI can show "last touched at" without joining the
// history table.
func (s *Store) SetDefaultFeatureID(repoID int64, featureID *int64) error {
	_, err := s.DB.Exec(
		`INSERT INTO repo_settings (repo_id, default_feature_id, updated_at)
		 VALUES (?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(repo_id) DO UPDATE SET
		   default_feature_id = excluded.default_feature_id,
		   updated_at         = CURRENT_TIMESTAMP`,
		repoID, nullableInt(featureID),
	)
	return err
}

// ClearDefaultFeature is the explicit "unset the default" form. Same
// effect as SetDefaultFeatureID(repoID, nil); kept as its own method
// so the call site reads naturally and the audit-op naming
// (`repo_setting.update` with details `cleared`) doesn't have to
// re-derive intent from the value.
func (s *Store) ClearDefaultFeature(repoID int64) error {
	return s.SetDefaultFeatureID(repoID, nil)
}
