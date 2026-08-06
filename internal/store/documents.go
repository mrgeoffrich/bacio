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
	// IncludeArchived (BACI-68), when true, includes rows with a
	// non-NULL archived_at. Defaults to false — archived docs are
	// hidden from default lists.
	IncludeArchived bool
	// Folder constrains the doc-folder placement. The zero value means
	// "no constraint" — see DocFolderScope for why this isn't a bare
	// *int64.
	Folder DocFolderScope
}

// DocFolderScope is DocumentFilter's folder constraint. It exists
// because the constraint is THREE-WAY and a bare *int64 can only express
// two of the three states:
//
//	unconstrained  — every document, in any folder or none
//	root only      — folder_id IS NULL (the pre-pivot placement every
//	                 existing document keeps, with no backfill)
//	one folder     — folder_id = <id>
//
// The fields are unexported and the only way to build a non-zero value
// is through AnyFolder / RootFolder / InFolder, which makes the obvious
// bug — setting the folder id but forgetting the "is it set" flag, and
// silently listing every document in the repo — unrepresentable. The
// zero value is AnyFolder(), so every pre-pivot
// `DocumentFilter{RepoID: x}` call site keeps its exact behaviour.
type DocFolderScope struct {
	constrained bool
	id          *int64
}

// AnyFolder applies no folder constraint. Same as the zero value; spell
// it out at a call site where the intent matters.
func AnyFolder() DocFolderScope { return DocFolderScope{} }

// RootFolder restricts to documents sitting at the tree root
// (folder_id IS NULL) — the case a bare *int64 cannot express.
func RootFolder() DocFolderScope { return DocFolderScope{constrained: true} }

// InFolder restricts to the documents directly inside one folder. It is
// NOT recursive: a document in a child folder is not "in" the parent.
func InFolder(id int64) DocFolderScope { return DocFolderScope{constrained: true, id: &id} }

// Constrained reports whether this scope narrows the query at all.
func (f DocFolderScope) Constrained() bool { return f.constrained }

// FolderID returns the folder this scope selects: nil means the tree
// root when Constrained() is true, and is meaningless when it is false.
func (f DocFolderScope) FolderID() *int64 { return f.id }

// clause renders the scope as a SQL fragment plus its args. Empty
// fragment when unconstrained.
func (f DocFolderScope) clause() (string, []any) {
	switch {
	case !f.constrained:
		return "", nil
	case f.id == nil:
		return " AND folder_id IS NULL", nil
	default:
		return " AND folder_id = ?", []any{*f.id}
	}
}

func (s *Store) ListDocuments(f DocumentFilter) ([]*model.Document, error) {
	// BACI-204: project a snippet alongside the lean column set so the
	// Documents list rich-row gets its title + ~200-char preview in
	// one round trip. The snippet column is empty for transcript-typed
	// rows (their body is audit JSONL, not browsing material).
	cols := docCols(false) + `, CASE WHEN type = 'transcript' THEN '' ELSE substr(content, 1, ?) END AS snippet`
	q := `SELECT ` + cols + ` FROM documents WHERE repo_id = ?`
	args := []any{snippetByteLimit, f.RepoID}
	if f.Type != nil {
		q += ` AND type = ?`
		args = append(args, string(*f.Type))
	}
	if !f.IncludeArchived {
		q += ` AND archived_at IS NULL`
	}
	if frag, fargs := f.Folder.clause(); frag != "" {
		q += frag
		args = append(args, fargs...)
	}
	// folder_position first, filename as the tie-break: inside one
	// folder the manual drag order wins, and an untouched folder (every
	// position still 0, which is every pre-pivot document) falls back to
	// exactly the alphabetical order this list has always had.
	q += ` ORDER BY folder_position, filename`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Document
	ids := []int64{}
	for rows.Next() {
		d, snippet, err := scanDocumentWithSnippet(rows)
		if err != nil {
			return nil, err
		}
		d.Snippet = trimSnippetAtWordBoundary(snippet)
		out = append(out, d)
		ids = append(ids, d.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// BACI-204: hydrate links for the listed docs in one IN-query so
	// the Documents page's linked-issue / linked-feature chips don't
	// fan out into N per-row round trips. A doc with zero links stays
	// with a nil Links slice — omitempty drops it from the JSON.
	if len(out) > 0 {
		linksByDoc, err := s.linksForDocuments(ids)
		if err != nil {
			return nil, err
		}
		for _, d := range out {
			d.Links = linksByDoc[d.ID]
		}
	}
	return out, nil
}

// snippetByteLimit caps the per-row preview the Documents list
// renders under each title. ~200 chars is enough for a one-line
// preview; trimSnippetAtWordBoundary clips back to whitespace so we
// don't slice mid-word.
const snippetByteLimit = 200

// trimSnippetAtWordBoundary clips a substr-projected snippet back to
// the last whitespace boundary so the rendered preview doesn't end
// mid-word. Bodies shorter than the cap pass through unchanged; rows
// with no whitespace inside the cap (a long URL, say) keep the hard
// byte slice — better than blank.
func trimSnippetAtWordBoundary(s string) string {
	if len(s) < snippetByteLimit {
		return s
	}
	for i := len(s) - 1; i > snippetByteLimit/2; i-- {
		switch s[i] {
		case ' ', '\n', '\t':
			return s[:i]
		}
	}
	return s
}

// linksForDocuments returns a documentID → []*DocumentLink map for
// the supplied set of document ids, fetched in one IN-query (BACI-204).
// A doc with zero links is absent from the map.
func (s *Store) linksForDocuments(ids []int64) (map[int64][]*model.DocumentLink, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	q := docLinkSelect + ` WHERE dl.document_id IN (` + strings.Join(placeholders, ", ") + `) ORDER BY dl.document_id, dl.created_at, dl.id`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	links, err := scanDocumentLinks(rows)
	if err != nil {
		return nil, err
	}
	out := make(map[int64][]*model.DocumentLink, len(ids))
	for _, l := range links {
		out[l.DocumentID] = append(out[l.DocumentID], l)
	}
	return out, nil
}

// SetDocumentArchived stamps or clears the document's archived_at
// column (BACI-68). Same idempotent semantics as SetIssueArchived.
// updated_at is bumped by the bump_document_updated_on_archive_change
// schema trigger (BACI-189); see the SetIssueArchived doc-comment for
// the rationale.
func (s *Store) SetDocumentArchived(documentID int64, archived bool) error {
	if archived {
		_, err := s.DB.Exec(`UPDATE documents SET archived_at = CURRENT_TIMESTAMP WHERE id = ? AND archived_at IS NULL`, documentID)
		return err
	}
	_, err := s.DB.Exec(`UPDATE documents SET archived_at = NULL WHERE id = ?`, documentID)
	return err
}

// SetDocumentFolder places a document in the repo's doc-folder tree.
// folderID nil means the tree root — the placement every pre-pivot
// document already has, and the one a folder deletion re-roots pages to.
//
// position is written literally: it is a sort key, not an index, and
// need be neither dense nor unique. ListDocuments orders by
// (folder_position, filename), so siblings sharing a position still sort
// deterministically. A caller driving a drag-and-drop reorder writes the
// new position for each affected sibling.
//
// The folder must live in the document's own repo (ErrDocFolderOtherRepo
// otherwise) — the tree is strictly per-repo, and a cross-repo move
// would put a page in a tree its repo never renders.
//
// Deliberately does NOT bump documents.updated_at. Folder membership is
// NOT part of doc.yaml — it lives on the container side, in the folder's
// own synced record — so a move changes no byte of the document's synced
// form, and bumping updated_at would both rewrite doc.yaml on the next
// export and let a pure re-filing win the sync LWW race against a real
// remote content edit. The affected folders' updated_at IS bumped: their
// membership list is what changed, and that timestamp is what the
// import-side membership dedupe (last writer by folder updated_at) keys
// on.
func (s *Store) SetDocumentFolder(docID int64, folderID *int64, position int) error {
	if position < 0 {
		return fmt.Errorf("position must be >= 0, got %d", position)
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var (
		repoID      int64
		oldFolderID sql.NullInt64
	)
	if err := tx.QueryRow(`SELECT repo_id, folder_id FROM documents WHERE id = ?`, docID).Scan(&repoID, &oldFolderID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if folderID != nil {
		folder, ferr := getDocFolder(tx, *folderID)
		if ferr != nil {
			if errors.Is(ferr, ErrNotFound) {
				return fmt.Errorf("%w: folder %d", ErrNotFound, *folderID)
			}
			return ferr
		}
		if folder.RepoID != repoID {
			return fmt.Errorf("%w: folder %d is in repo %d, document %d is in repo %d",
				ErrDocFolderOtherRepo, folder.ID, folder.RepoID, docID, repoID)
		}
	}
	if _, err := tx.Exec(
		`UPDATE documents SET folder_id = ?, folder_position = ? WHERE id = ?`,
		folderID, position, docID,
	); err != nil {
		return err
	}
	touch := []int64{}
	if oldFolderID.Valid {
		touch = append(touch, oldFolderID.Int64)
	}
	if folderID != nil && (!oldFolderID.Valid || oldFolderID.Int64 != *folderID) {
		touch = append(touch, *folderID)
	}
	for _, fid := range touch {
		if _, err := tx.Exec(`UPDATE doc_folders SET updated_at = CURRENT_TIMESTAMP WHERE id = ?`, fid); err != nil {
			return err
		}
	}
	return tx.Commit()
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
	c := "id, uuid, repo_id, filename, type, size_bytes, source_path, archived_at, folder_id, folder_position, created_at, updated_at"
	if withContent {
		c += ", content"
	}
	return c
}

// scanDocumentWithSnippet scans the lean column set + the trailing
// snippet column ListDocuments projects (BACI-204). The snippet is
// returned separately so the caller can apply the word-boundary
// trim before stamping it onto the Document.
func scanDocumentWithSnippet(row rowScanner) (*model.Document, string, error) {
	var (
		d          model.Document
		typ        string
		archivedAt sql.NullTime
		folderID   sql.NullInt64
		snippet    string
	)
	err := row.Scan(&d.ID, &d.UUID, &d.RepoID, &d.Filename, &typ, &d.SizeBytes, &d.SourcePath, &archivedAt, &folderID, &d.FolderPosition, &d.CreatedAt, &d.UpdatedAt, &snippet)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", fmt.Errorf("scan document with snippet: %w", err)
	}
	d.Type = model.DocumentType(typ)
	if archivedAt.Valid {
		t := archivedAt.Time
		d.ArchivedAt = &t
	}
	if folderID.Valid {
		f := folderID.Int64
		d.FolderID = &f
	}
	return &d, snippet, nil
}

func scanDocument(row rowScanner, withContent bool) (*model.Document, error) {
	var (
		d          model.Document
		typ        string
		archivedAt sql.NullTime
		folderID   sql.NullInt64
		err        error
	)
	if withContent {
		err = row.Scan(&d.ID, &d.UUID, &d.RepoID, &d.Filename, &typ, &d.SizeBytes, &d.SourcePath, &archivedAt, &folderID, &d.FolderPosition, &d.CreatedAt, &d.UpdatedAt, &d.Content)
	} else {
		err = row.Scan(&d.ID, &d.UUID, &d.RepoID, &d.Filename, &typ, &d.SizeBytes, &d.SourcePath, &archivedAt, &folderID, &d.FolderPosition, &d.CreatedAt, &d.UpdatedAt)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan document: %w", err)
	}
	d.Type = model.DocumentType(typ)
	if archivedAt.Valid {
		t := archivedAt.Time
		d.ArchivedAt = &t
	}
	if folderID.Valid {
		f := folderID.Int64
		d.FolderID = &f
	}
	return &d, nil
}
