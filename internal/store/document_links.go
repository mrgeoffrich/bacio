package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

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
// description if the (document, target) pair already exists. The
// bump_document_updated_on_link_insert /
// bump_document_updated_on_link_description_update schema triggers
// advance documents.updated_at on either path so the sync importer's
// last-writer-wins gate (which keys on documents.updated_at) preserves
// the new/edited link through the next round-trip — see BACI-144 (and
// BACI-142, which fixed the same drift at the call site before the
// invariant was promoted into the schema).
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
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.getLink(documentID, t)
}

// UnlinkDocument deletes the link row. The
// bump_document_updated_on_link_delete schema trigger advances
// documents.updated_at when a row went away so the LWW gate preserves
// the deletion through the next sync. See BACI-144.
func (s *Store) UnlinkDocument(documentID int64, t LinkTarget) (int64, error) {
	if err := t.check(); err != nil {
		return 0, err
	}
	var (
		res sql.Result
		err error
	)
	switch {
	case t.IssueID != nil:
		res, err = s.DB.Exec(`DELETE FROM document_links WHERE document_id = ? AND issue_id = ?`, documentID, *t.IssueID)
	case t.FeatureID != nil:
		res, err = s.DB.Exec(`DELETE FROM document_links WHERE document_id = ? AND feature_id = ?`, documentID, *t.FeatureID)
	}
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
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

// CountTranscriptDocsByIssue (BACI-141) returns a map keyed by issue id
// of the number of `.jsonl` transcript documents linked to each issue.
// Backs the per-card "N transcript(s)" chip surfaced on the kanban
// board. Matches both rows of `type = 'transcript'` (the typed-doc
// path BACI-115 introduced) AND legacy rows whose filename matches
// model.IsBacioTranscriptFilename (pre-BACI-115 transcripts that still
// carry `project_complete` as their type). The legacy filter is
// applied Go-side; the per-issue candidate set is tiny (one or two
// docs per issue typically) so the post-filter is cheap.
//
// An issue with zero matching transcripts is absent from the result
// map (not present-with-zero), mirroring CountEvalCommentsByIssue.
func (s *Store) CountTranscriptDocsByIssue(issueIDs []int64) (map[int64]int, error) {
	out := make(map[int64]int, len(issueIDs))
	if len(issueIDs) == 0 {
		return out, nil
	}
	placeholders := make([]string, len(issueIDs))
	args := make([]any, len(issueIDs))
	for i, id := range issueIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	q := `SELECT dl.issue_id, d.type, d.filename
	       FROM document_links dl
	       JOIN documents d ON d.id = dl.document_id
	       WHERE dl.issue_id IN (` + strings.Join(placeholders, ", ") + `)`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			issueID  int64
			docType  string
			filename string
		)
		if err := rows.Scan(&issueID, &docType, &filename); err != nil {
			return nil, err
		}
		if docType == string(model.DocTypeTranscript) || model.IsBacioTranscriptFilename(filename) {
			out[issueID]++
		}
	}
	return out, rows.Err()
}

// LatestPlanForIssue (BACI-216) returns the newest `plan`-typed
// document linked directly to the given issue, or nil when no plan
// is linked. "Newest" sorts on the document's own (updated_at,
// created_at, id) DESC — link-row created_at is not the rule, so a
// plan rewritten yesterday wins over one linked yesterday but
// updated last week. See model.LatestPlan for the contract.
//
// Returns (nil, nil) when no matching plan exists — every caller
// surfaces "no plan" as the omitted/null JSON field, so the
// distinction between "no plan" and ErrNotFound isn't useful.
func (s *Store) LatestPlanForIssue(issueID int64) (*model.LatestPlan, error) {
	row := s.DB.QueryRow(`
		SELECT d.id, d.uuid, d.filename, d.updated_at
		  FROM documents d
		  JOIN document_links dl ON dl.document_id = d.id
		 WHERE dl.issue_id = ? AND d.type = ?
		 ORDER BY d.updated_at DESC, d.created_at DESC, d.id DESC
		 LIMIT 1
	`, issueID, string(model.DocTypePlan))
	var out model.LatestPlan
	if err := row.Scan(&out.DocumentID, &out.UUID, &out.Filename, &out.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan latest plan: %w", err)
	}
	return &out, nil
}

// LatestPlanByIssue (BACI-216) is the bulk sibling of
// LatestPlanForIssue — returns a map keyed by issue id of each
// issue's newest linked plan. Issues with no plan are absent from
// the map (mirrors CountTranscriptDocsByIssue / CountEvalCommentsByIssue).
// Used by boardcards.Assemble to fan the plan affordance onto every
// kanban card in one trip.
//
// Per-issue "latest" follows the same (updated_at, created_at, id) DESC
// rule as the single-id helper — done client-side over a
// `WHERE dl.issue_id IN (...)` scan filtered to plan-typed rows. The
// candidate set per issue is tiny (most issues only ever have one
// plan doc) so the post-filter cost is negligible.
func (s *Store) LatestPlanByIssue(issueIDs []int64) (map[int64]*model.LatestPlan, error) {
	out := make(map[int64]*model.LatestPlan, len(issueIDs))
	if len(issueIDs) == 0 {
		return out, nil
	}
	placeholders := make([]string, len(issueIDs))
	args := make([]any, 0, len(issueIDs)+1)
	for i, id := range issueIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}
	args = append(args, string(model.DocTypePlan))
	q := `SELECT dl.issue_id, d.id, d.uuid, d.filename, d.updated_at, d.created_at
	       FROM document_links dl
	       JOIN documents d ON d.id = dl.document_id
	       WHERE dl.issue_id IN (` + strings.Join(placeholders, ", ") + `)
	         AND d.type = ?`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	// Track each issue's "best so far" candidate by document recency.
	// SQLite's ORDER BY can't pre-pick the per-group winner without a
	// correlated subquery; the Go-side argmax keeps the SQL simple
	// and the per-issue plan candidate count is tiny.
	type candidate struct {
		updated, created time.Time
		id               int64
		latest           *model.LatestPlan
	}
	best := make(map[int64]candidate, len(issueIDs))
	for rows.Next() {
		var (
			issueID    int64
			docID      int64
			uuid       string
			filename   string
			updatedAt  time.Time
			createdAt  time.Time
		)
		if err := rows.Scan(&issueID, &docID, &uuid, &filename, &updatedAt, &createdAt); err != nil {
			return nil, err
		}
		cur := candidate{
			updated: updatedAt,
			created: createdAt,
			id:      docID,
			latest: &model.LatestPlan{
				DocumentID: docID,
				UUID:       uuid,
				Filename:   filename,
				UpdatedAt:  updatedAt,
			},
		}
		prev, ok := best[issueID]
		if !ok || candidateLess(prev, cur) {
			best[issueID] = cur
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for issueID, c := range best {
		out[issueID] = c.latest
	}
	return out, nil
}

// candidateLess reports whether `a` is strictly less recent than `b`
// under the LatestPlan ordering — newer updated_at wins, then newer
// created_at, then larger id as the deterministic tiebreaker. The
// strict-less semantics mean "first seen at the same key wins" when
// two rows are exactly equal, which is fine: we just need
// determinism, and the candidate set per issue is tiny.
func candidateLess(a, b struct {
	updated, created time.Time
	id               int64
	latest           *model.LatestPlan
}) bool {
	if !a.updated.Equal(b.updated) {
		return a.updated.Before(b.updated)
	}
	if !a.created.Equal(b.created) {
		return a.created.Before(b.created)
	}
	return a.id < b.id
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
