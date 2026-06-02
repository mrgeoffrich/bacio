package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/identity"
	"github.com/mrgeoffrich/bacio/internal/model"
)

// Feature-comment kinds (BACI-333). 'note' is a hand-typed comment;
// 'handoff' is a worker close-out note that the store backstop drops when
// the parent feature has collect_handoffs off.
const (
	FeatureCommentKindNote    = "note"
	FeatureCommentKindHandoff = "handoff"
)

// FeatureCommentResult (BACI-333) is the return shape of
// CreateFeatureComment. It wraps the inserted row rather than duplicating
// its fields. When the store backstop drops a kind='handoff' write to a
// feature with collect_handoffs off, Comment is nil and Skipped is true —
// a worker treats a dropped handoff as success, not a retry, so this is
// not surfaced as an error.
type FeatureCommentResult struct {
	Comment *model.FeatureComment `json:"comment,omitempty"`
	Skipped bool                  `json:"skipped"`
	Reason  string                `json:"reason,omitempty"`
}

// normalizeFeatureCommentKind defaults an empty kind to 'note' and rejects
// anything outside the known set, so a typo fails loud at the store
// boundary rather than silently writing an undroppable handoff.
func normalizeFeatureCommentKind(kind string) (string, error) {
	switch kind {
	case "":
		return FeatureCommentKindNote, nil
	case FeatureCommentKindNote, FeatureCommentKindHandoff:
		return kind, nil
	default:
		return "", fmt.Errorf("invalid feature comment kind %q: want %q or %q", kind, FeatureCommentKindNote, FeatureCommentKindHandoff)
	}
}

// CreateFeatureComment inserts a new feature-scoped chronological-handoff
// comment (BACI-124). Mirrors CreateComment; the differences are the
// parent table, the FeatureID column, and the BACI-333 kind + backstop:
// an empty kind defaults to 'note', and a kind='handoff' write to a
// feature whose collect_handoffs flag is off is dropped (no row inserted)
// and returned as {Skipped:true} rather than erroring — a worker treats a
// dropped handoff as success. A 'note' (or a handoff on an enabled
// feature) always inserts.
func (s *Store) CreateFeatureComment(featureID int64, author, body, kind string) (*FeatureCommentResult, error) {
	kind, err := normalizeFeatureCommentKind(kind)
	if err != nil {
		return nil, err
	}
	author, err = ValidateName(author, "author")
	if err != nil {
		return nil, err
	}
	body, err = ValidateBody(body, "body", true)
	if err != nil {
		return nil, err
	}
	// Backstop: drop a handoff write to a feature that has opted out of
	// collecting handoffs. Keyed on kind so a hand-typed note is never
	// gated. One SELECT on the parent feature.
	if kind == FeatureCommentKindHandoff {
		var collect int64
		if err := s.DB.QueryRow(`SELECT collect_handoffs FROM features WHERE id = ?`, featureID).Scan(&collect); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, ErrNotFound
			}
			return nil, err
		}
		if collect == 0 {
			return &FeatureCommentResult{Skipped: true, Reason: "feature handoffs disabled"}, nil
		}
	}
	res, err := s.DB.Exec(
		`INSERT INTO feature_comments (uuid, feature_id, author, body, kind) VALUES (?, ?, ?, ?, ?)`,
		identity.New(), featureID, author, body, kind,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	cm, err := s.GetFeatureCommentByID(id)
	if err != nil {
		return nil, err
	}
	return &FeatureCommentResult{Comment: cm}, nil
}

const featureCommentCols = `id, uuid, feature_id, author, body, kind, created_at`

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
// kind (BACI-333) round-trips the note/handoff discriminator; an empty
// kind defaults to 'note'. The collect_handoffs backstop is deliberately
// NOT applied here — sync replays whatever the remote authoritatively
// recorded, so a handoff that was kept on the origin must round-trip
// faithfully even onto a replica whose feature later flipped off.
func (s *Store) CreateFeatureCommentFromSync(featureID int64, uuid, author, body, kind string, createdAt sql.NullTime) (*model.FeatureComment, error) {
	if uuid == "" {
		return nil, fmt.Errorf("CreateFeatureCommentFromSync: uuid is required")
	}
	kind, err := normalizeFeatureCommentKind(kind)
	if err != nil {
		return nil, err
	}
	author, err = ValidateName(author, "author")
	if err != nil {
		return nil, err
	}
	body, err = ValidateBody(body, "body", true)
	if err != nil {
		return nil, err
	}
	q := `INSERT INTO feature_comments (uuid, feature_id, author, body, kind`
	vals := `?, ?, ?, ?, ?`
	args := []any{uuid, featureID, author, body, kind}
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
	err := row.Scan(&c.ID, &c.UUID, &c.FeatureID, &c.Author, &c.Body, &c.Kind, &c.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan feature comment: %w", err)
	}
	return &c, nil
}
