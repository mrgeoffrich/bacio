package store

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// LinkTarget identifies the issue or feature end of a document link.
// Exactly one of IssueID or FeatureID must be set.
type LinkTarget struct {
	IssueID   *int64
	FeatureID *int64
}

func (t LinkTarget) check() error {
	if (t.IssueID == nil) == (t.FeatureID == nil) {
		return errors.New("link target must specify exactly one of issue or feature")
	}
	return nil
}

// LinkDocument upserts: creating the link if absent, or replacing the
// description if the (document, target) pair already exists. Bumps
// documents.updated_at so the sync importer's last-writer-wins gate
// (which keys on documents.updated_at) preserves the new link through
// the next round-trip; without the bump, replaceDocLinksTx silently
// wipes the local row — see BACI-142.
func (s *Store) LinkDocument(documentID int64, t LinkTarget, description string) (*model.DocumentLink, error) {
	if err := t.check(); err != nil {
		return nil, err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// Try update first; if no row matches, insert.
	var (
		res sql.Result
	)
	switch {
	case t.IssueID != nil:
		res, err = tx.Exec(`UPDATE document_links SET description = ? WHERE document_id = ? AND issue_id = ?`,
			description, documentID, *t.IssueID)
	case t.FeatureID != nil:
		res, err = tx.Exec(`UPDATE document_links SET description = ? WHERE document_id = ? AND feature_id = ?`,
			description, documentID, *t.FeatureID)
	}
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		_, err = tx.Exec(
			`INSERT INTO document_links (document_id, issue_id, feature_id, description) VALUES (?, ?, ?, ?)`,
			documentID, nullableInt(t.IssueID), nullableInt(t.FeatureID), description,
		)
		if err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(`UPDATE documents SET updated_at = CURRENT_TIMESTAMP WHERE id = ?`, documentID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.getLink(documentID, t)
}

// UnlinkDocument deletes the link row and, when a row actually went
// away, bumps documents.updated_at so the LWW gate preserves the
// deletion through the next sync. See BACI-142.
func (s *Store) UnlinkDocument(documentID int64, t LinkTarget) (int64, error) {
	if err := t.check(); err != nil {
		return 0, err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var res sql.Result
	switch {
	case t.IssueID != nil:
		res, err = tx.Exec(`DELETE FROM document_links WHERE document_id = ? AND issue_id = ?`, documentID, *t.IssueID)
	case t.FeatureID != nil:
		res, err = tx.Exec(`DELETE FROM document_links WHERE document_id = ? AND feature_id = ?`, documentID, *t.FeatureID)
	}
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		if _, err := tx.Exec(`UPDATE documents SET updated_at = CURRENT_TIMESTAMP WHERE id = ?`, documentID); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return n, nil
}

func (s *Store) getLink(documentID int64, t LinkTarget) (*model.DocumentLink, error) {
	q := docLinkSelect + ` WHERE dl.document_id = ?`
	args := []any{documentID}
	if t.IssueID != nil {
		q += ` AND dl.issue_id = ?`
		args = append(args, *t.IssueID)
	} else {
		q += ` AND dl.feature_id = ?`
		args = append(args, *t.FeatureID)
	}
	return scanDocumentLink(s.DB.QueryRow(q, args...))
}

// ReplaceDocumentLinks clears every link row for documentID and
// re-creates the given set. Used by the sync importer to apply the
// `links:` block from doc.yaml in one shot. Each LinkTarget is
// validated for "exactly one of issue or feature".
func (s *Store) ReplaceDocumentLinks(documentID int64, links []DocumentLinkSpec) error {
	for _, l := range links {
		if err := l.Target.check(); err != nil {
			return err
		}
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM document_links WHERE document_id = ?`, documentID); err != nil {
		return err
	}
	stmt, err := tx.Prepare(
		`INSERT INTO document_links (document_id, issue_id, feature_id, description) VALUES (?, ?, ?, ?)`,
	)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, l := range links {
		if _, err := stmt.Exec(documentID, nullableInt(l.Target.IssueID), nullableInt(l.Target.FeatureID), l.Description); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DocumentLinkSpec is the import-side description of one link: the
// (issue|feature) target plus an optional description.
type DocumentLinkSpec struct {
	Target      LinkTarget
	Description string
}

// ListDocumentLinks returns every link for a single document, both to issues
// and to features.
func (s *Store) ListDocumentLinks(documentID int64) ([]*model.DocumentLink, error) {
	rows, err := s.DB.Query(docLinkSelect+` WHERE dl.document_id = ? ORDER BY dl.created_at, dl.id`, documentID)
	if err != nil {
		return nil, err
	}
	return scanDocumentLinks(rows)
}

// ListDocumentsLinkedToIssue returns links from any document to a given issue.
func (s *Store) ListDocumentsLinkedToIssue(issueID int64) ([]*model.DocumentLink, error) {
	rows, err := s.DB.Query(docLinkSelect+` WHERE dl.issue_id = ? ORDER BY d.filename`, issueID)
	if err != nil {
		return nil, err
	}
	return scanDocumentLinks(rows)
}

// ListDocumentsLinkedToFeature returns links from any document to a given feature.
func (s *Store) ListDocumentsLinkedToFeature(featureID int64) ([]*model.DocumentLink, error) {
	rows, err := s.DB.Query(docLinkSelect+` WHERE dl.feature_id = ? ORDER BY d.filename`, featureID)
	if err != nil {
		return nil, err
	}
	return scanDocumentLinks(rows)
}

const docLinkSelect = `
SELECT dl.id, dl.document_id, d.filename, d.type,
       dl.issue_id, COALESCE(r.prefix || '-' || i.number, '') AS issue_key,
       dl.feature_id, COALESCE(f.slug, '') AS feature_slug,
       dl.description, dl.created_at
FROM document_links dl
JOIN documents d   ON d.id = dl.document_id
LEFT JOIN issues i ON i.id = dl.issue_id
LEFT JOIN repos r  ON r.id = i.repo_id
LEFT JOIN features f ON f.id = dl.feature_id`

func scanDocumentLink(row rowScanner) (*model.DocumentLink, error) {
	var (
		l         model.DocumentLink
		docType   string
		issueID   sql.NullInt64
		featureID sql.NullInt64
		issueKey  string
		featSlug  string
	)
	err := row.Scan(&l.ID, &l.DocumentID, &l.DocumentFilename, &docType,
		&issueID, &issueKey, &featureID, &featSlug,
		&l.Description, &l.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan document link: %w", err)
	}
	l.DocumentType = model.DocumentType(docType)
	if issueID.Valid {
		v := issueID.Int64
		l.IssueID = &v
		l.IssueKey = issueKey
	}
	if featureID.Valid {
		v := featureID.Int64
		l.FeatureID = &v
		l.FeatureSlug = featSlug
	}
	return &l, nil
}

func scanDocumentLinks(rows *sql.Rows) ([]*model.DocumentLink, error) {
	defer rows.Close()
	var out []*model.DocumentLink
	for rows.Next() {
		l, err := scanDocumentLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
