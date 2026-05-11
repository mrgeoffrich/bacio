package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/identity"
	"github.com/mrgeoffrich/bacio/internal/model"
)

func (s *Store) CreateComment(issueID int64, author, body string) (*model.Comment, error) {
	author, err := ValidateName(author, "author")
	if err != nil {
		return nil, err
	}
	body, err = ValidateBody(body, "body", true)
	if err != nil {
		return nil, err
	}
	res, err := s.DB.Exec(
		`INSERT INTO comments (uuid, issue_id, author, body) VALUES (?, ?, ?, ?)`,
		identity.New(), issueID, author, body,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetCommentByID(id)
}

const commentCols = `id, uuid, issue_id, author, body, created_at`

func (s *Store) GetCommentByID(id int64) (*model.Comment, error) {
	row := s.DB.QueryRow(`SELECT `+commentCols+` FROM comments WHERE id = ?`, id)
	return scanComment(row)
}

// GetCommentByUUID is the sync-side lookup. Comments live in their own
// timestamped files under each issue folder, so an importer needs to
// match incoming comment uuids against the DB.
func (s *Store) GetCommentByUUID(uuid string) (*model.Comment, error) {
	row := s.DB.QueryRow(`SELECT `+commentCols+` FROM comments WHERE uuid = ?`, uuid)
	return scanComment(row)
}

// CreateCommentFromSync inserts a comment with a caller-supplied
// uuid, for the sync import path. Mirrors CreateComment but bypasses
// auto-uuid generation and accepts createdAt to preserve the
// timestamp embedded in the on-disk filename.
func (s *Store) CreateCommentFromSync(issueID int64, uuid, author, body string, createdAt sql.NullTime) (*model.Comment, error) {
	if uuid == "" {
		return nil, fmt.Errorf("CreateCommentFromSync: uuid is required")
	}
	author, err := ValidateName(author, "author")
	if err != nil {
		return nil, err
	}
	body, err = ValidateBody(body, "body", true)
	if err != nil {
		return nil, err
	}
	q := `INSERT INTO comments (uuid, issue_id, author, body`
	vals := `?, ?, ?, ?`
	args := []any{uuid, issueID, author, body}
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
	return s.GetCommentByID(id)
}

// DeleteCommentByUUID is the sync-side delete: the importer drops a
// row when the on-disk file disappears.
func (s *Store) DeleteCommentByUUID(uuid string) error {
	res, err := s.DB.Exec(`DELETE FROM comments WHERE uuid = ?`, uuid)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateCommentByUUID rewrites the body / author of a comment by
// uuid. Comments are simpler than other records — there's no
// label-collision phase, no relations — so a single helper covers
// every import-side mutation.
func (s *Store) UpdateCommentByUUID(uuid string, author, body *string) error {
	if uuid == "" {
		return fmt.Errorf("UpdateCommentByUUID: uuid is required")
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
		fmt.Sprintf(`UPDATE comments SET %s WHERE uuid = ?`, strings.Join(sets, ", ")),
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

func (s *Store) ListComments(issueID int64) ([]*model.Comment, error) {
	rows, err := s.DB.Query(`SELECT `+commentCols+` FROM comments WHERE issue_id = ? ORDER BY created_at, id`, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Comment
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func scanComment(row rowScanner) (*model.Comment, error) {
	var c model.Comment
	err := row.Scan(&c.ID, &c.UUID, &c.IssueID, &c.Author, &c.Body, &c.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan comment: %w", err)
	}
	return &c, nil
}
