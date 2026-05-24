package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/identity"
	"github.com/mrgeoffrich/bacio/internal/model"
)

// CreateFeatureComment inserts a new feature-scoped chronological-handoff
// comment (BACI-124). Mirrors CreateComment 1:1; the only differences are
// the parent table and the FeatureID column.
func (s *Store) CreateFeatureComment(featureID int64, author, body string) (*model.FeatureComment, error) {
	author, err := ValidateName(author, "author")
	if err != nil {
		return nil, err
	}
	body, err = ValidateBody(body, "body", true)
	if err != nil {
		return nil, err
	}
	res, err := s.DB.Exec(
		`INSERT INTO feature_comments (uuid, feature_id, author, body) VALUES (?, ?, ?, ?)`,
		identity.New(), featureID, author, body,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetFeatureCommentByID(id)
}

const featureCommentCols = `id, uuid, feature_id, author, body, created_at`

func (s *Store) GetFeatureCommentByID(id int64) (*model.FeatureComment, error) {
	row := s.DB.QueryRow(`SELECT `+featureCommentCols+` FROM feature_comments WHERE id = ?`, id)
	return scanFeatureComment(row)
}

// GetFeatureCommentByUUID is the sync-side lookup. Feature comments live
// in their own timestamped files under each feature folder, so the
// importer matches incoming comment uuids against the DB.
func (s *Store) GetFeatureCommentByUUID(uuid string) (*model.FeatureComment, error) {
	row := s.DB.QueryRow(`SELECT `+featureCommentCols+` FROM feature_comments WHERE uuid = ?`, uuid)
	return scanFeatureComment(row)
}

// CreateFeatureCommentFromSync inserts a feature comment with a caller-
// supplied uuid for the sync import path. Mirrors CreateCommentFromSync.
func (s *Store) CreateFeatureCommentFromSync(featureID int64, uuid, author, body string, createdAt sql.NullTime) (*model.FeatureComment, error) {
	if uuid == "" {
		return nil, fmt.Errorf("CreateFeatureCommentFromSync: uuid is required")
	}
	author, err := ValidateName(author, "author")
	if err != nil {
		return nil, err
	}
	body, err = ValidateBody(body, "body", true)
	if err != nil {
		return nil, err
	}
	q := `INSERT INTO feature_comments (uuid, feature_id, author, body`
	vals := `?, ?, ?, ?`
	args := []any{uuid, featureID, author, body}
	if createdAt.Valid {
		q += `, created_at`
		vals += `, ?`
		args = append(args, createdAt.Time)
	}
	q += `) VALUES (` + vals + `)`
	res, err := s.DB.Exec(q, args...)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetFeatureCommentByID(id)
}

// DeleteFeatureCommentByUUID is both the sync-side delete (the importer
// drops a row when the on-disk file disappears) and the user-facing
// delete primitive behind `bacio feature comment rm` and the API/UI
// delete affordance. Mirrors DeleteCommentByUUID.
func (s *Store) DeleteFeatureCommentByUUID(uuid string) error {
	res, err := s.DB.Exec(`DELETE FROM feature_comments WHERE uuid = ?`, uuid)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateFeatureCommentByUUID rewrites the body / author of a feature
// comment by uuid. Mirrors UpdateCommentByUUID — provided for sync
// importer symmetry; user-facing edit is deliberately omitted, same as
// for issue comments.
func (s *Store) UpdateFeatureCommentByUUID(uuid string, author, body *string) error {
	if uuid == "" {
		return fmt.Errorf("UpdateFeatureCommentByUUID: uuid is required")
	}
	sets := []string{}
	args := []any{}
	if author != nil {
		clean, err := ValidateName(*author, "author")
		if err != nil {
			return err
		}
		sets = append(sets, "author = ?")
		args = append(args, clean)
	}
	if body != nil {
		clean, err := ValidateBody(*body, "body", true)
		if err != nil {
			return err
		}
		sets = append(sets, "body = ?")
		args = append(args, clean)
	}
	if len(sets) == 0 {
		return nil
	}
	args = append(args, uuid)
	res, err := s.DB.Exec(
		fmt.Sprintf(`UPDATE feature_comments SET %s WHERE uuid = ?`, strings.Join(sets, ", ")),
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

func (s *Store) ListFeatureComments(featureID int64) ([]*model.FeatureComment, error) {
	rows, err := s.DB.Query(`SELECT `+featureCommentCols+` FROM feature_comments WHERE feature_id = ? ORDER BY created_at, id`, featureID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.FeatureComment
	for rows.Next() {
		c, err := scanFeatureComment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CountFeatureComments returns the row count for one feature — used by
// the FeatureDeletePreview cascade so the operator sees how many handoff
// comments would be wiped along with the parent feature.
func (s *Store) CountFeatureComments(featureID int64) (int, error) {
	var n int
	row := s.DB.QueryRow(`SELECT COUNT(*) FROM feature_comments WHERE feature_id = ?`, featureID)
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func scanFeatureComment(row rowScanner) (*model.FeatureComment, error) {
	var c model.FeatureComment
	err := row.Scan(&c.ID, &c.UUID, &c.FeatureID, &c.Author, &c.Body, &c.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan feature comment: %w", err)
	}
	return &c, nil
}
