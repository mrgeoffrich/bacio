package store

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/identity"
	"github.com/mrgeoffrich/bacio/internal/model"
)

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func Slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "feature"
	}
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}

func (s *Store) CreateFeature(repoID int64, slug, title, description, emoji string) (*model.Feature, error) {
	slug, err := ValidateSlug(slug)
	if err != nil {
		return nil, err
	}
	title, err = ValidateTitle(title, "title")
	if err != nil {
		return nil, err
	}
	description, err = ValidateBody(description, "description", false)
	if err != nil {
		return nil, err
	}
	emoji, err = ValidateEmoji(emoji)
	if err != nil {
		return nil, err
	}
	res, err := s.DB.Exec(
		`INSERT INTO features (uuid, repo_id, slug, title, description, emoji) VALUES (?, ?, ?, ?, ?, ?)`,
		identity.New(), repoID, slug, title, description, emoji,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetFeatureByID(id)
}

const featureCols = `id, uuid, repo_id, slug, title, description, emoji, archived_at, created_at, updated_at`

// FeatureFilter (BACI-68) is the filter struct for ListFeaturesFiltered.
// The plain ListFeatures(repoID, includeDescription) signature predated
// archive, so this sibling type is layered in for the read paths that
// need to opt in to archived rows — leaving the simple shape in place
// for the many call sites that don't care.
type FeatureFilter struct {
	RepoID             int64
	AllRepos           bool
	IncludeDescription bool
	// IncludeArchived, when true, includes rows with a non-NULL
	// archived_at. Defaults to false — archived features are hidden
	// from default lists.
	IncludeArchived bool
	// WithHiddenOnBoard (BACI-177), when true, populates each returned
	// Feature.HiddenOnBoard from the per-repo board-hide KV
	// (LoadHiddenFeatures). Off by default so the many call sites
	// that don't care about the toggle don't pay for the extra
	// lookup. AllRepos is supported — the load runs once per repo
	// the scan touches.
	WithHiddenOnBoard bool
}

func (s *Store) GetFeatureByID(id int64) (*model.Feature, error) {
	return scanFeature(s.DB.QueryRow(`SELECT `+featureCols+` FROM features WHERE id = ?`, id))
}

func (s *Store) GetFeatureBySlug(repoID int64, slug string) (*model.Feature, error) {
	return scanFeature(s.DB.QueryRow(`SELECT `+featureCols+` FROM features WHERE repo_id = ? AND slug = ?`, repoID, slug))
}

// GetFeatureByUUID is the sync-side lookup: import maps the canonical
// uuid in feature.yaml back to a DB row, ignoring slug churn.
func (s *Store) GetFeatureByUUID(uuid string) (*model.Feature, error) {
	return scanFeature(s.DB.QueryRow(`SELECT `+featureCols+` FROM features WHERE uuid = ?`, uuid))
}

// ListFeatures returns every feature in the repo, hiding archived rows
// by default. When includeDescription is false the heavy `description`
// field is stripped post-scan — list contexts rarely want full bodies
// inlined, and dropping them keeps the JSON output small enough to fit
// comfortably into an agent's context window.
//
// Use ListFeaturesFiltered when the caller needs to opt in to archived
// rows (the BACI-68 `--include-archived` / `?include_archived=1` path).
func (s *Store) ListFeatures(repoID int64, includeDescription bool) ([]*model.Feature, error) {
	return s.ListFeaturesFiltered(FeatureFilter{
		RepoID:             repoID,
		IncludeDescription: includeDescription,
	})
}

// ListAllFeatures returns every feature across every repo, ordered by
// created_at. Used by the desktop app, which has no notion of a
// "current repo" the way the CLI does. Hides archived rows by default;
// callers wanting them must use ListFeaturesFiltered with AllRepos +
// IncludeArchived.
func (s *Store) ListAllFeatures(includeDescription bool) ([]*model.Feature, error) {
	return s.ListFeaturesFiltered(FeatureFilter{
		AllRepos:           true,
		IncludeDescription: includeDescription,
	})
}

// ListFeaturesFiltered is the BACI-68 filter-aware list. Hides archived
// rows by default; pass IncludeArchived=true to inflate. RepoID is
// ignored when AllRepos is true.
func (s *Store) ListFeaturesFiltered(f FeatureFilter) ([]*model.Feature, error) {
	q := `SELECT ` + featureCols + ` FROM features`
	var (
		where []string
		args  []any
	)
	if !f.AllRepos {
		where = append(where, "repo_id = ?")
		args = append(args, f.RepoID)
	}
	if !f.IncludeArchived {
		where = append(where, "archived_at IS NULL")
	}
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, ` AND `)
	}
	q += ` ORDER BY created_at`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Feature
	for rows.Next() {
		feat, err := scanFeature(rows)
		if err != nil {
			return nil, err
		}
		if !f.IncludeDescription {
			feat.Description = ""
		}
		out = append(out, feat)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// BACI-177: opt-in enrichment from the per-repo board-hide KV.
	// Loaded once per repo we touched and folded onto each row's
	// HiddenOnBoard field — no extra round-trip when the caller
	// didn't ask for it.
	if f.WithHiddenOnBoard && len(out) > 0 {
		hiddenByRepo := make(map[int64]map[string]bool)
		for _, feat := range out {
			if _, ok := hiddenByRepo[feat.RepoID]; ok {
				continue
			}
			hidden, herr := s.LoadHiddenFeatures(feat.RepoID)
			if herr != nil {
				return nil, herr
			}
			hiddenByRepo[feat.RepoID] = hidden
		}
		for _, feat := range out {
			if set := hiddenByRepo[feat.RepoID]; set != nil {
				feat.HiddenOnBoard = set[feat.Slug]
			}
		}
	}
	return out, nil
}

// GetFeatureBySlugWithHidden (BACI-177) wraps GetFeatureBySlug and
// also populates Feature.HiddenOnBoard from the per-repo board-hide KV
// — the single-row sibling to ListFeaturesFiltered's WithHiddenOnBoard
// branch. Used by the API/Wails show-feature handlers so the toggle
// state arrives in lockstep with the rest of the row.
func (s *Store) GetFeatureBySlugWithHidden(repoID int64, slug string) (*model.Feature, error) {
	feat, err := s.GetFeatureBySlug(repoID, slug)
	if err != nil {
		return nil, err
	}
	hidden, err := s.IsFeatureHiddenOnBoard(repoID, slug)
	if err != nil {
		return nil, err
	}
	feat.HiddenOnBoard = hidden
	return feat, nil
}

// SetFeatureArchived stamps or clears the feature's archived_at column
// (BACI-68). Same idempotent semantics as SetIssueArchived. updated_at
// is bumped by the bump_feature_updated_on_archive_change schema
// trigger (BACI-189); see the SetIssueArchived doc-comment for the
// rationale.
func (s *Store) SetFeatureArchived(featureID int64, archived bool) error {
	if archived {
		_, err := s.DB.Exec(`UPDATE features SET archived_at = CURRENT_TIMESTAMP WHERE id = ? AND archived_at IS NULL`, featureID)
		return err
	}
	_, err := s.DB.Exec(`UPDATE features SET archived_at = NULL WHERE id = ?`, featureID)
	return err
}

func (s *Store) UpdateFeature(id int64, title, description, emoji *string) error {
	sets := []string{}
	args := []any{}
	if title != nil {
		clean, err := ValidateTitle(*title, "title")
		if err != nil {
			return err
		}
		sets = append(sets, "title = ?")
		args = append(args, clean)
	}
	if description != nil {
		clean, err := ValidateBody(*description, "description", false)
		if err != nil {
			return err
		}
		sets = append(sets, "description = ?")
		args = append(args, clean)
	}
	if emoji != nil {
		clean, err := ValidateEmoji(*emoji)
		if err != nil {
			return err
		}
		sets = append(sets, "emoji = ?")
		args = append(args, clean)
	}
	if len(sets) == 0 {
		return nil
	}
	sets = append(sets, "updated_at = CURRENT_TIMESTAMP")
	args = append(args, id)
	_, err := s.DB.Exec(fmt.Sprintf(`UPDATE features SET %s WHERE id = ?`, strings.Join(sets, ", ")), args...)
	return err
}

func (s *Store) DeleteFeature(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM features WHERE id = ?`, id)
	return err
}

// DeleteFeatureByUUID is the sync-side delete: the importer
// propagates a remote deletion by uuid so it can run inside the
// outer transaction without resolving a stale id.
func (s *Store) DeleteFeatureByUUID(uuid string) error {
	res, err := s.DB.Exec(`DELETE FROM features WHERE uuid = ?`, uuid)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// RenameFeatureSlug updates the slug of the feature identified by
// uuid. Used by the sync importer's collision-resolution phase: when
// an incoming feature.yaml carries the same slug as a local-only DB
// row but a different uuid, the local row gives up the slug.
//
// Validates the new slug, rejects collisions with another feature in
// the same repo, and bumps updated_at.
func (s *Store) RenameFeatureSlug(uuid, newSlug string) error {
	if uuid == "" {
		return fmt.Errorf("RenameFeatureSlug: uuid is required")
	}
	clean, err := ValidateSlug(newSlug)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var (
		id     int64
		repoID int64
	)
	if err := tx.QueryRow(`SELECT id, repo_id FROM features WHERE uuid = ?`, uuid).Scan(&id, &repoID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	var collide int64
	err = tx.QueryRow(`SELECT id FROM features WHERE repo_id = ? AND slug = ? AND id <> ?`, repoID, clean, id).Scan(&collide)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if collide != 0 {
		return fmt.Errorf("RenameFeatureSlug: slug %q already used by another feature in this repo", clean)
	}
	if _, err := tx.Exec(
		`UPDATE features SET slug = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		clean, id,
	); err != nil {
		return err
	}
	return tx.Commit()
}

// FeaturePatch carries the import-side fields that flow from
// feature.yaml into a DB row identified by uuid. Mirrors IssuePatch.
type FeaturePatch struct {
	Title       *string
	Description *string
	// Emoji (BACI-172) round-trips the per-feature glyph through sync;
	// pointer so a nil patch leaves the field unchanged and an
	// explicit-empty clears it.
	Emoji *string
}

// UpdateFeatureByUUID applies a FeaturePatch to the feature
// identified by uuid. Mirrors UpdateFeature for the sync importer.
func (s *Store) UpdateFeatureByUUID(uuid string, p FeaturePatch) error {
	if uuid == "" {
		return fmt.Errorf("UpdateFeatureByUUID: uuid is required")
	}
	sets := []string{}
	args := []any{}
	if p.Title != nil {
		clean, err := ValidateTitle(*p.Title, "title")
		if err != nil {
			return err
		}
		sets = append(sets, "title = ?")
		args = append(args, clean)
	}
	if p.Description != nil {
		clean, err := ValidateBody(*p.Description, "description", false)
		if err != nil {
			return err
		}
		sets = append(sets, "description = ?")
		args = append(args, clean)
	}
	if p.Emoji != nil {
		clean, err := ValidateEmoji(*p.Emoji)
		if err != nil {
			return err
		}
		sets = append(sets, "emoji = ?")
		args = append(args, clean)
	}
	if len(sets) == 0 {
		return nil
	}
	sets = append(sets, "updated_at = CURRENT_TIMESTAMP")
	args = append(args, uuid)
	res, err := s.DB.Exec(
		fmt.Sprintf(`UPDATE features SET %s WHERE uuid = ?`, strings.Join(sets, ", ")),
		args...,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// CreateFeatureFromSync inserts a feature with a caller-supplied uuid,
// for the sync import path. Mirrors CreateFeature but bypasses
// auto-uuid generation and accepts createdAt/updatedAt.
func (s *Store) CreateFeatureFromSync(repoID int64, uuid, slug, title, description, emoji string, createdAt, updatedAt sql.NullTime) (*model.Feature, error) {
	if uuid == "" {
		return nil, fmt.Errorf("CreateFeatureFromSync: uuid is required")
	}
	slug, err := ValidateSlug(slug)
	if err != nil {
		return nil, err
	}
	title, err = ValidateTitle(title, "title")
	if err != nil {
		return nil, err
	}
	description, err = ValidateBody(description, "description", false)
	if err != nil {
		return nil, err
	}
	emoji, err = ValidateEmoji(emoji)
	if err != nil {
		return nil, err
	}
	q := `INSERT INTO features (uuid, repo_id, slug, title, description, emoji`
	vals := `?, ?, ?, ?, ?, ?`
	args := []any{uuid, repoID, slug, title, description, emoji}
	if createdAt.Valid {
		q += `, created_at`
		vals += `, ?`
		args = append(args, createdAt.Time)
	}
	if updatedAt.Valid {
		q += `, updated_at`
		vals += `, ?`
		args = append(args, updatedAt.Time)
	}
	q += `) VALUES (` + vals + `)`
	res, err := s.DB.Exec(q, args...)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetFeatureByID(id)
}

func scanFeature(row rowScanner) (*model.Feature, error) {
	var (
		f          model.Feature
		archivedAt sql.NullTime
	)
	err := row.Scan(&f.ID, &f.UUID, &f.RepoID, &f.Slug, &f.Title, &f.Description, &f.Emoji, &archivedAt, &f.CreatedAt, &f.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan feature: %w", err)
	}
	if archivedAt.Valid {
		t := archivedAt.Time
		f.ArchivedAt = &t
	}
	return &f, nil
}
