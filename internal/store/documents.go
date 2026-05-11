package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/identity"
	"github.com/mrgeoffrich/bacio/internal/model"
)

var ErrDocumentExists = errors.New("a document with that filename already exists in this repo")

func (s *Store) CreateDocument(repoID int64, filename string, t model.DocumentType, content, sourcePath string) (*model.Document, error) {
	filename, err := ValidateDocFilenameStrict(filename)
	if err != nil {
		return nil, err
	}
	content, err = ValidateBody(content, "content", false)
	if err != nil {
		return nil, err
	}
	res, err := s.DB.Exec(
		`INSERT INTO documents (uuid, repo_id, filename, type, content, size_bytes, source_path) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		identity.New(), repoID, filename, string(t), content, len(content), sourcePath,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return nil, ErrDocumentExists
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetDocumentByID(id, true)
}

func (s *Store) GetDocumentByID(id int64, withContent bool) (*model.Document, error) {
	cols := docCols(withContent)
	row := s.DB.QueryRow(`SELECT `+cols+` FROM documents WHERE id = ?`, id)
	return scanDocument(row, withContent)
}

func (s *Store) GetDocumentByFilename(repoID int64, filename string, withContent bool) (*model.Document, error) {
	cols := docCols(withContent)
	row := s.DB.QueryRow(`SELECT `+cols+` FROM documents WHERE repo_id = ? AND filename = ?`, repoID, filename)
	return scanDocument(row, withContent)
}

// GetDocumentByUUID is the sync-side lookup: doc folder → uuid in
// doc.yaml → DB row. Always returns the body so callers handling sync
// content comparisons see the full record.
func (s *Store) GetDocumentByUUID(uuid string, withContent bool) (*model.Document, error) {
	cols := docCols(withContent)
	row := s.DB.QueryRow(`SELECT `+cols+` FROM documents WHERE uuid = ?`, uuid)
	return scanDocument(row, withContent)
}

type DocumentFilter struct {
	RepoID int64
	Type   *model.DocumentType
}

func (s *Store) ListDocuments(f DocumentFilter) ([]*model.Document, error) {
	cols := docCols(false)
	q := `SELECT ` + cols + ` FROM documents WHERE repo_id = ?`
	args := []any{f.RepoID}
	if f.Type != nil {
		q += ` AND type = ?`
		args = append(args, string(*f.Type))
	}
	q += ` ORDER BY filename`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Document
	for rows.Next() {
		d, err := scanDocument(rows, false)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// UpdateDocument patches the type, content, and/or source_path. Pass nil for
// fields you don't want to change. Bumps updated_at when something actually
// changed.
func (s *Store) UpdateDocument(id int64, newType *model.DocumentType, newContent, newSourcePath *string) error {
	sets := []string{}
	args := []any{}
	if newType != nil {
		sets = append(sets, "type = ?")
		args = append(args, string(*newType))
	}
	if newContent != nil {
		clean, err := ValidateBody(*newContent, "content", false)
		if err != nil {
			return err
		}
		sets = append(sets, "content = ?", "size_bytes = ?")
		args = append(args, clean, len(clean))
	}
	if newSourcePath != nil {
		sets = append(sets, "source_path = ?")
		args = append(args, *newSourcePath)
	}
	if len(sets) == 0 {
		return nil
	}
	sets = append(sets, "updated_at = CURRENT_TIMESTAMP")
	args = append(args, id)
	_, err := s.DB.Exec(fmt.Sprintf(`UPDATE documents SET %s WHERE id = ?`, strings.Join(sets, ", ")), args...)
	return err
}

// RenameDocument changes a document's filename in place — the row id is
// preserved, so document_links rows stay intact. Optionally updates the
// type at the same time. Returns ErrDocumentExists on filename collision.
func (s *Store) RenameDocument(id int64, newFilename string, newType *model.DocumentType) error {
	newFilename, err := ValidateDocFilenameStrict(newFilename)
	if err != nil {
		return err
	}
	sets := []string{"filename = ?"}
	args := []any{newFilename}
	if newType != nil {
		sets = append(sets, "type = ?")
		args = append(args, string(*newType))
	}
	sets = append(sets, "updated_at = CURRENT_TIMESTAMP")
	args = append(args, id)
	_, err = s.DB.Exec(
		fmt.Sprintf(`UPDATE documents SET %s WHERE id = ?`, strings.Join(sets, ", ")),
		args...,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return ErrDocumentExists
		}
		return err
	}
	return nil
}

func (s *Store) DeleteDocument(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM documents WHERE id = ?`, id)
	return err
}

// DeleteDocumentByUUID is the sync-side delete: the importer
// propagates a remote deletion by uuid.
func (s *Store) DeleteDocumentByUUID(uuid string) error {
	res, err := s.DB.Exec(`DELETE FROM documents WHERE uuid = ?`, uuid)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// RenameDocumentFilename changes the filename on the document
// identified by uuid. Used by the sync importer's collision-resolution
// phase: when an incoming doc.yaml carries the same filename as a
// local-only DB row with a different uuid, the local row gives up
// the filename.
//
// Validates the new filename, rejects collisions, and bumps
// updated_at. Distinct from RenameDocument(id, ...) which keys by
// integer id and is used by the explicit `bacio doc rename` command.
func (s *Store) RenameDocumentFilename(uuid, newFilename string) error {
	if uuid == "" {
		return fmt.Errorf("RenameDocumentFilename: uuid is required")
	}
	clean, err := ValidateDocFilenameStrict(newFilename)
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
	if err := tx.QueryRow(`SELECT id, repo_id FROM documents WHERE uuid = ?`, uuid).Scan(&id, &repoID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	var collide int64
	err = tx.QueryRow(`SELECT id FROM documents WHERE repo_id = ? AND filename = ? AND id <> ?`, repoID, clean, id).Scan(&collide)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if collide != 0 {
		return ErrDocumentExists
	}
	if _, err := tx.Exec(
		`UPDATE documents SET filename = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		clean, id,
	); err != nil {
		return err
	}
	return tx.Commit()
}

// DocumentPatch carries the import-side fields that flow from
// doc.yaml into a DB row identified by uuid.
type DocumentPatch struct {
	Type       *model.DocumentType
	Content    *string
	SourcePath *string
}

// UpdateDocumentByUUID applies a DocumentPatch to the document
// identified by uuid.
func (s *Store) UpdateDocumentByUUID(uuid string, p DocumentPatch) error {
	if uuid == "" {
		return fmt.Errorf("UpdateDocumentByUUID: uuid is required")
	}
	sets := []string{}
	args := []any{}
	if p.Type != nil {
		sets = append(sets, "type = ?")
		args = append(args, string(*p.Type))
	}
	if p.Content != nil {
		clean, err := ValidateBody(*p.Content, "content", false)
		if err != nil {
			return err
		}
		sets = append(sets, "content = ?", "size_bytes = ?")
		args = append(args, clean, len(clean))
	}
	if p.SourcePath != nil {
		sets = append(sets, "source_path = ?")
		args = append(args, *p.SourcePath)
	}
	if len(sets) == 0 {
		return nil
	}
	sets = append(sets, "updated_at = CURRENT_TIMESTAMP")
	args = append(args, uuid)
	res, err := s.DB.Exec(
		fmt.Sprintf(`UPDATE documents SET %s WHERE uuid = ?`, strings.Join(sets, ", ")),
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

// CreateDocumentFromSync inserts a document with a caller-supplied
// uuid, for the sync import path.
func (s *Store) CreateDocumentFromSync(repoID int64, uuid, filename string, t model.DocumentType, content, sourcePath string, createdAt, updatedAt sql.NullTime) (*model.Document, error) {
	if uuid == "" {
		return nil, fmt.Errorf("CreateDocumentFromSync: uuid is required")
	}
	filename, err := ValidateDocFilenameStrict(filename)
	if err != nil {
		return nil, err
	}
	content, err = ValidateBody(content, "content", false)
	if err != nil {
		return nil, err
	}
	q := `INSERT INTO documents (uuid, repo_id, filename, type, content, size_bytes, source_path`
	vals := `?, ?, ?, ?, ?, ?, ?`
	args := []any{uuid, repoID, filename, string(t), content, len(content), sourcePath}
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
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return nil, ErrDocumentExists
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetDocumentByID(id, true)
}

func docCols(withContent bool) string {
	c := "id, uuid, repo_id, filename, type, size_bytes, source_path, created_at, updated_at"
	if withContent {
		c += ", content"
	}
	return c
}

func scanDocument(row rowScanner, withContent bool) (*model.Document, error) {
	var (
		d   model.Document
		typ string
		err error
	)
	if withContent {
		err = row.Scan(&d.ID, &d.UUID, &d.RepoID, &d.Filename, &typ, &d.SizeBytes, &d.SourcePath, &d.CreatedAt, &d.UpdatedAt, &d.Content)
	} else {
		err = row.Scan(&d.ID, &d.UUID, &d.RepoID, &d.Filename, &typ, &d.SizeBytes, &d.SourcePath, &d.CreatedAt, &d.UpdatedAt)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan document: %w", err)
	}
	d.Type = model.DocumentType(typ)
	return &d, nil
}
