package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/identity"
	"github.com/mrgeoffrich/bacio/internal/model"
)

// MaxDocFolderDepth caps how deep the doc-folder tree may nest, counting
// a root folder as depth 1. Enforced at the store boundary (agent-CLI
// rule #4) by both CreateDocFolder and MoveDocFolder so no transport can
// build a tree the UI can't render or the sync exporter can't walk.
const MaxDocFolderDepth = 16

// maxDocFolderWalk bounds every parent_id walk and every subtree descent
// below. The schema makes a cycle unreachable through this package's
// mutators, but a walk that runs away is a corruption tripwire we'd much
// rather see as a loud error than as a hung process.
const maxDocFolderWalk = 4 * MaxDocFolderDepth

// FolderPathSeparator joins the segments of a DERIVED folder display
// path ("Design/API/Auth"). Display paths are never stored — see the
// doc comment on model.DocFolder and the schema.sql commentary on
// doc_folders for why.
const FolderPathSeparator = "/"

var (
	// ErrDocFolderExists reports a sibling-name collision — the store-side
	// face of both partial unique indexes (uniq_doc_folders_root for
	// parent_id IS NULL, uniq_doc_folders_child for everything else).
	// Always wrapped with the location so the message reads sensibly for
	// a root folder and for a nested one; match it with errors.Is.
	ErrDocFolderExists = errors.New("a folder with that name already exists")

	// ErrDocFolderCycle reports an attempt to move a folder inside
	// itself or one of its own descendants.
	ErrDocFolderCycle = errors.New("a folder cannot be moved inside itself or one of its own descendants")

	// ErrDocFolderTooDeep reports a create/move that would push the tree
	// past MaxDocFolderDepth.
	ErrDocFolderTooDeep = errors.New("folder nesting would exceed the maximum depth")

	// ErrDocFolderOtherRepo reports a parent folder (or a document)
	// living in a different repo from the row being written. The tree is
	// strictly per-repo.
	ErrDocFolderOtherRepo = errors.New("folder belongs to a different repo")
)

const docFolderCols = `id, uuid, repo_id, parent_id, name, position, created_at, updated_at`

// docFolderQuerier abstracts *sql.DB and *sql.Tx so the ancestor walk,
// the subtree descent and the sibling lookups all serve both the plain
// reads and the transactional mutators without duplicating SQL. Mirrors
// the `execer` shim in sync_state.go.
type docFolderQuerier interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// ---------------------------------------------------------------------
// Create
// ---------------------------------------------------------------------

// CreateDocFolder inserts a folder under parentID (nil = a root folder)
// and returns it. The name is validated through ValidateFolderName — the
// single folder-name validator, shared with the Kanban lane rules — so
// '/' can never enter a folder name and forge a fake hierarchy.
//
// position is assigned automatically: the new folder is appended to the
// end of its sibling list. Callers wanting a specific slot follow up
// with ReorderDocFolder.
//
// Returns ErrDocFolderExists (wrapped, with the location) on a sibling
// name collision, ErrDocFolderTooDeep past MaxDocFolderDepth, and
// ErrDocFolderOtherRepo if parentID names a folder in another repo.
func (s *Store) CreateDocFolder(repoID int64, parentID *int64, name string) (*model.DocFolder, error) {
	clean, err := ValidateFolderName(name)
	if err != nil {
		return nil, err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	f, err := createDocFolderTx(tx, repoID, parentID, clean)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return f, nil
}

// createDocFolderTx is CreateDocFolder's body, factored out so
// EnsureFolderPath can create a whole chain of missing segments inside
// one transaction. `name` must already be through ValidateFolderName.
func createDocFolderTx(tx *sql.Tx, repoID int64, parentID *int64, name string) (*model.DocFolder, error) {
	parentDepth, err := checkDocFolderParent(tx, repoID, parentID)
	if err != nil {
		return nil, err
	}
	if depth := parentDepth + 1; depth > MaxDocFolderDepth {
		return nil, fmt.Errorf("%w: %q would sit at depth %d (max %d)", ErrDocFolderTooDeep, name, depth, MaxDocFolderDepth)
	}
	if err := ensureDocFolderNameFree(tx, repoID, parentID, name, 0); err != nil {
		return nil, err
	}
	pos, err := nextDocFolderPosition(tx, repoID, parentID)
	if err != nil {
		return nil, err
	}
	res, err := tx.Exec(
		`INSERT INTO doc_folders (uuid, repo_id, parent_id, name, position) VALUES (?, ?, ?, ?, ?)`,
		identity.New(), repoID, parentID, name, pos,
	)
	if err != nil {
		return nil, mapDocFolderUnique(err, tx, parentID, name)
	}
	id, _ := res.LastInsertId()
	return getDocFolder(tx, id)
}

// CreateDocFolderFromSync inserts a folder with a caller-supplied uuid,
// position and timestamps, for the sync import path. Mirrors
// CreateDocumentFromSync / CreateFeatureFromSync: same validation, no
// auto-uuid, no auto-position.
func (s *Store) CreateDocFolderFromSync(repoID int64, uuid string, parentID *int64, name string, position int, createdAt, updatedAt sql.NullTime) (*model.DocFolder, error) {
	if uuid == "" {
		return nil, fmt.Errorf("CreateDocFolderFromSync: uuid is required")
	}
	clean, err := ValidateFolderName(name)
	if err != nil {
		return nil, err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	parentDepth, err := checkDocFolderParent(tx, repoID, parentID)
	if err != nil {
		return nil, err
	}
	if depth := parentDepth + 1; depth > MaxDocFolderDepth {
		return nil, fmt.Errorf("%w: %q would sit at depth %d (max %d)", ErrDocFolderTooDeep, clean, depth, MaxDocFolderDepth)
	}
	if err := ensureDocFolderNameFree(tx, repoID, parentID, clean, 0); err != nil {
		return nil, err
	}
	q := `INSERT INTO doc_folders (uuid, repo_id, parent_id, name, position`
	vals := `?, ?, ?, ?, ?`
	args := []any{uuid, repoID, parentID, clean, position}
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
	res, err := tx.Exec(q, args...)
	if err != nil {
		return nil, mapDocFolderUnique(err, tx, parentID, clean)
	}
	id, _ := res.LastInsertId()
	f, err := getDocFolder(tx, id)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return f, nil
}

// ---------------------------------------------------------------------
// Read
// ---------------------------------------------------------------------

// GetDocFolderByID returns one folder, or ErrNotFound.
func (s *Store) GetDocFolderByID(id int64) (*model.DocFolder, error) {
	return getDocFolder(s.DB, id)
}

// GetDocFolderByUUID is the sync-side lookup: folder.yaml carries the
// immutable uuid, so import resolves a synced folder back to its DB row
// by uuid rather than by the (mutable) name or parent.
func (s *Store) GetDocFolderByUUID(uuid string) (*model.DocFolder, error) {
	return scanDocFolder(s.DB.QueryRow(`SELECT `+docFolderCols+` FROM doc_folders WHERE uuid = ?`, uuid))
}

// ListDocFolders returns every folder in the repo, flat.
//
// The result is ordered by (position, name, id) — so a caller that
// groups rows by ParentID gets each sibling group already in display
// order. It deliberately does NOT promise parent-before-child across the
// whole slice: after a MoveDocFolder a child row can carry a lower id
// than its parent, so tree builders must group by ParentID rather than
// assume a topological order.
func (s *Store) ListDocFolders(repoID int64) ([]*model.DocFolder, error) {
	rows, err := s.DB.Query(
		`SELECT `+docFolderCols+` FROM doc_folders WHERE repo_id = ? ORDER BY position, name, id`,
		repoID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDocFolders(rows)
}

// ListDocFolderChildren returns the direct children of parentID (nil =
// the root level) in display order. The one-level read behind a lazily
// expanded tree rail.
func (s *Store) ListDocFolderChildren(repoID int64, parentID *int64) ([]*model.DocFolder, error) {
	rows, err := s.DB.Query(
		`SELECT `+docFolderCols+` FROM doc_folders WHERE repo_id = ? AND parent_id IS ? ORDER BY position, name, id`,
		repoID, parentID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDocFolders(rows)
}

// FolderPath is the DERIVED display path of one folder — built by
// walking parent_id on every call, never stored (a stored path would
// make a one-word rename rewrite N rows and N synced files, and would
// tempt loosening ValidateDocFilenameStrict to admit '/', which the flat
// `docs/<filename>/` sync record layout cannot survive).
//
// Every field is plain data so the shape marshals straight onto the wire
// if a transport wants the whole breadcrumb rather than just the string.
type FolderPath struct {
	// ID / UUID identify the folder the path terminates at.
	ID   int64  `json:"id"`
	UUID string `json:"uuid"`
	// Display is Segments joined with FolderPathSeparator —
	// "Design/API/Auth". This is what FolderByPath round-trips.
	Display string `json:"display"`
	// Segments are the folder names, root-first.
	Segments []string `json:"segments"`
	// Chain is the folder rows themselves, root-first and INCLUSIVE of
	// the folder the path terminates at — i.e. Chain[len(Chain)-1].ID ==
	// ID. This is the breadcrumb source: each entry carries the uuid and
	// id a crumb needs to be clickable.
	Chain []*model.DocFolder `json:"chain"`
	// Depth is len(Chain). A root folder has Depth 1, so the invariant
	// the store enforces reads as Depth <= MaxDocFolderDepth.
	Depth int `json:"depth"`
}

// ResolveFolderPath derives a folder's display path by walking parent_id
// up to the root. Returns ErrNotFound if the folder doesn't exist.
//
// There is no "path of the root" — the tree root is not a folder. Code
// holding a *int64 folder id should treat nil as "" (root level) without
// calling this.
func (s *Store) ResolveFolderPath(id int64) (*FolderPath, error) {
	chain, err := folderChain(s.DB, id)
	if err != nil {
		return nil, err
	}
	return newFolderPath(chain), nil
}

// FolderByPath resolves a slash-separated display path to a folder,
// segment by segment, inside one repo.
//
// The EMPTY path (or "/", or a path of only separators) means the tree
// ROOT, which is not a folder: it returns (nil, nil). That is the shape
// callers want — "move this page to the root" and "list the root level"
// are both spelled with a nil folder id everywhere else in the store, so
// FolderByPath("") feeding straight into SetDocumentFolder's folderID
// argument does the right thing.
//
// Any other path returns ErrNotFound (wrapped, naming the segment that
// failed) if a segment is missing. Use EnsureFolderPath to create the
// missing segments instead.
func (s *Store) FolderByPath(repoID int64, path string) (*model.DocFolder, error) {
	segs := SplitFolderPath(path)
	if len(segs) == 0 {
		return nil, nil
	}
	var (
		parent *int64
		cur    *model.DocFolder
	)
	for i, seg := range segs {
		f, err := docFolderByName(s.DB, repoID, parent, seg)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return nil, fmt.Errorf("%w: no folder %q under %q", ErrNotFound, seg, strings.Join(segs[:i], FolderPathSeparator))
			}
			return nil, err
		}
		cur = f
		parent = &f.ID
	}
	return cur, nil
}

// EnsureFolderPath resolves a slash-separated display path, creating any
// missing segments as it goes (mkdir -p), and returns the deepest
// folder. The whole chain lands in one transaction, so a half-created
// path can never be observed.
//
// Like FolderByPath, the empty path means the tree root and returns
// (nil, nil). Every segment goes through ValidateFolderName and the
// depth cap, so this is a safe target for a CLI flag like
// `bacio doc folder add Design/API/Auth`.
func (s *Store) EnsureFolderPath(repoID int64, path string) (*model.DocFolder, error) {
	segs := SplitFolderPath(path)
	if len(segs) == 0 {
		return nil, nil
	}
	if len(segs) > MaxDocFolderDepth {
		return nil, fmt.Errorf("%w: path %q has %d segments (max %d)", ErrDocFolderTooDeep, path, len(segs), MaxDocFolderDepth)
	}
	clean := make([]string, len(segs))
	for i, seg := range segs {
		c, err := ValidateFolderName(seg)
		if err != nil {
			return nil, err
		}
		clean[i] = c
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var (
		parent *int64
		cur    *model.DocFolder
	)
	for _, seg := range clean {
		f, err := docFolderByName(tx, repoID, parent, seg)
		if errors.Is(err, ErrNotFound) {
			f, err = createDocFolderTx(tx, repoID, parent, seg)
		}
		if err != nil {
			return nil, err
		}
		cur = f
		parent = &f.ID
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return cur, nil
}

// SplitFolderPath splits a display path into its segments, dropping
// empty ones so "", "/", "Design/", "/Design//API" all behave sensibly.
// Exported so transports can count segments (and reject an over-deep
// path) before they reach the store.
func SplitFolderPath(path string) []string {
	var out []string
	for _, seg := range strings.Split(path, FolderPathSeparator) {
		if seg == "" {
			continue
		}
		out = append(out, seg)
	}
	return out
}

// ---------------------------------------------------------------------
// Mutate
// ---------------------------------------------------------------------

// RenameDocFolder renames a folder in place. The row id and uuid are
// preserved, so documents and child folders keep pointing at it and the
// derived display path of the whole subtree changes with one UPDATE —
// the entire reason paths aren't materialised.
//
// Returns ErrNotFound for an unknown id, and ErrDocFolderExists (wrapped
// with the location) when a sibling already carries the new name.
func (s *Store) RenameDocFolder(id int64, newName string) error {
	clean, err := ValidateFolderName(newName)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	f, err := getDocFolder(tx, id)
	if err != nil {
		return err
	}
	if err := ensureDocFolderNameFree(tx, f.RepoID, f.ParentID, clean, id); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE doc_folders SET name = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		clean, id,
	); err != nil {
		return mapDocFolderUnique(err, tx, f.ParentID, clean)
	}
	return tx.Commit()
}

// MoveDocFolder re-parents a folder. newParentID nil moves it to the
// root level. The folder keeps its name, uuid and documents; only its
// place in the tree changes.
//
// Store-boundary invariants enforced here (agent-CLI rule #4 — no
// transport may bypass them):
//
//   - CYCLE GUARD: newParentID may be neither `id` itself nor any
//     descendant of `id`. Walked via the destination's ancestor chain,
//     so a deep descendant is caught as surely as a direct child.
//     Violations return ErrDocFolderCycle.
//   - DEPTH CAP: the destination's depth plus the height of the subtree
//     being moved may not exceed MaxDocFolderDepth — moving a 3-deep
//     subtree under a folder at depth 14 is refused, not silently
//     flattened. Violations return ErrDocFolderTooDeep.
//   - SAME REPO: the destination must live in the moved folder's repo.
//     Violations return ErrDocFolderOtherRepo.
//   - SIBLING NAMES: the moved folder must not collide with a name
//     already present at the destination. Violations return
//     ErrDocFolderExists.
//
// Moving to a different parent appends the folder to the end of the
// destination's sibling list; a move to the parent it already has is a
// no-op that leaves position (and updated_at) untouched.
func (s *Store) MoveDocFolder(id int64, newParentID *int64) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	f, err := getDocFolder(tx, id)
	if err != nil {
		return err
	}
	if sameFolderRef(f.ParentID, newParentID) {
		return nil
	}
	destDepth := 0
	if newParentID != nil {
		if *newParentID == id {
			return fmt.Errorf("%w (folder %d)", ErrDocFolderCycle, id)
		}
		chain, err := folderChain(tx, *newParentID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return fmt.Errorf("%w: destination folder %d", ErrNotFound, *newParentID)
			}
			return err
		}
		dest := chain[len(chain)-1]
		if dest.RepoID != f.RepoID {
			return fmt.Errorf("%w: destination folder %d is in repo %d, folder %d is in repo %d",
				ErrDocFolderOtherRepo, dest.ID, dest.RepoID, f.ID, f.RepoID)
		}
		for _, a := range chain {
			if a.ID == id {
				return fmt.Errorf("%w: %q is an ancestor of the destination", ErrDocFolderCycle, f.Name)
			}
		}
		destDepth = len(chain)
	}
	height, err := docFolderSubtreeHeight(tx, id)
	if err != nil {
		return err
	}
	if destDepth+height > MaxDocFolderDepth {
		return fmt.Errorf("%w: destination is at depth %d and the moved subtree is %d deep (max %d)",
			ErrDocFolderTooDeep, destDepth, height, MaxDocFolderDepth)
	}
	if err := ensureDocFolderNameFree(tx, f.RepoID, newParentID, f.Name, id); err != nil {
		return err
	}
	pos, err := nextDocFolderPosition(tx, f.RepoID, newParentID)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE doc_folders SET parent_id = ?, position = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		newParentID, pos, id,
	); err != nil {
		return mapDocFolderUnique(err, tx, newParentID, f.Name)
	}
	return tx.Commit()
}

// ReorderDocFolder sets a folder's manual sibling order within its
// current parent. Positions are a plain integer sort key, NOT required
// to be dense or unique: reads order by (position, name, id), so two
// folders sharing a position still sort deterministically. Callers
// driving a drag-and-drop reorder write the new position for each
// affected sibling.
//
// Returns ErrNotFound for an unknown id.
func (s *Store) ReorderDocFolder(id int64, position int) error {
	if position < 0 {
		return fmt.Errorf("position must be >= 0, got %d", position)
	}
	res, err := s.DB.Exec(
		`UPDATE doc_folders SET position = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		position, id,
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

// DeleteDocFolder removes a folder. Its child folders go with it
// (doc_folders.parent_id is ON DELETE CASCADE) but its documents do NOT:
// documents.folder_id is ON DELETE SET NULL, so every page in the
// deleted subtree is re-rooted rather than destroyed. Deleting a folder
// is an organisational act, never a way to lose content.
//
// Returns ErrNotFound for an unknown id.
func (s *Store) DeleteDocFolder(id int64) error {
	res, err := s.DB.Exec(`DELETE FROM doc_folders WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteDocFolderByUUID is the sync-side delete: the importer
// propagates a remote deletion by uuid so it can run inside the outer
// transaction without resolving a stale id. Same cascade semantics as
// DeleteDocFolder.
func (s *Store) DeleteDocFolderByUUID(uuid string) error {
	res, err := s.DB.Exec(`DELETE FROM doc_folders WHERE uuid = ?`, uuid)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------------------------------------------------------------------
// Internals
// ---------------------------------------------------------------------

// checkDocFolderParent validates a prospective parent and returns its
// depth (0 for the root level, so the child's depth is always the
// returned value + 1).
func checkDocFolderParent(q docFolderQuerier, repoID int64, parentID *int64) (int, error) {
	if parentID == nil {
		return 0, nil
	}
	chain, err := folderChain(q, *parentID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return 0, fmt.Errorf("%w: parent folder %d", ErrNotFound, *parentID)
		}
		return 0, err
	}
	parent := chain[len(chain)-1]
	if parent.RepoID != repoID {
		return 0, fmt.Errorf("%w: parent folder %d is in repo %d, not %d",
			ErrDocFolderOtherRepo, parent.ID, parent.RepoID, repoID)
	}
	return len(chain), nil
}

// folderChain walks parent_id from `id` up to the root and returns the
// chain ROOT-FIRST and inclusive of `id` itself. len(chain) is therefore
// the folder's depth, with a root folder at depth 1.
func folderChain(q docFolderQuerier, id int64) ([]*model.DocFolder, error) {
	var chain []*model.DocFolder
	seen := map[int64]bool{}
	cur := &id
	for cur != nil {
		if seen[*cur] {
			return nil, fmt.Errorf("doc_folders: parent_id cycle detected at folder %d", *cur)
		}
		seen[*cur] = true
		if len(chain) >= maxDocFolderWalk {
			return nil, fmt.Errorf("doc_folders: parent_id walk from folder %d exceeded %d levels", id, maxDocFolderWalk)
		}
		f, err := getDocFolder(q, *cur)
		if err != nil {
			return nil, err
		}
		chain = append(chain, f)
		cur = f.ParentID
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain, nil
}

// docFolderSubtreeHeight returns how many levels the subtree rooted at
// `id` spans, counting the folder itself as 1. A leaf is 1, a folder
// with children is 2, and so on — the number MoveDocFolder adds to the
// destination's depth for the cap check.
func docFolderSubtreeHeight(q docFolderQuerier, id int64) (int, error) {
	level := []int64{id}
	height := 0
	for len(level) > 0 {
		height++
		if height > maxDocFolderWalk {
			return 0, fmt.Errorf("doc_folders: subtree walk below folder %d exceeded %d levels", id, maxDocFolderWalk)
		}
		next, err := docFolderChildIDs(q, level)
		if err != nil {
			return 0, err
		}
		level = next
	}
	return height, nil
}

// docFolderChildIDs returns the ids of every direct child of the
// supplied parents, in one IN-query per tree level.
func docFolderChildIDs(q docFolderQuerier, parents []int64) ([]int64, error) {
	if len(parents) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(parents))
	args := make([]any, len(parents))
	for i, p := range parents {
		placeholders[i] = "?"
		args[i] = p
	}
	rows, err := q.Query(
		`SELECT id FROM doc_folders WHERE parent_id IN (`+strings.Join(placeholders, ", ")+`)`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// docFolderByName looks one sibling up by name. `parent_id IS ?` (rather
// than `=`) is what lets the same statement serve both the root level
// (NULL) and a nested one — the exact NULL-vs-distinct asymmetry that
// forced doc_folders into TWO partial unique indexes.
func docFolderByName(q docFolderQuerier, repoID int64, parentID *int64, name string) (*model.DocFolder, error) {
	return scanDocFolder(q.QueryRow(
		`SELECT `+docFolderCols+` FROM doc_folders WHERE repo_id = ? AND parent_id IS ? AND name = ?`,
		repoID, parentID, name,
	))
}

// ensureDocFolderNameFree pre-checks the sibling-name uniqueness that
// uniq_doc_folders_root / uniq_doc_folders_child enforce, so the caller
// gets a located, matchable error instead of a raw SQLite constraint
// string. excludeID (0 for an insert) skips the row being updated.
func ensureDocFolderNameFree(q docFolderQuerier, repoID int64, parentID *int64, name string, excludeID int64) error {
	var id int64
	err := q.QueryRow(
		`SELECT id FROM doc_folders WHERE repo_id = ? AND parent_id IS ? AND name = ? AND id <> ?`,
		repoID, parentID, name, excludeID,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return docFolderExistsErr(q, parentID, name)
}

// docFolderExistsErr wraps ErrDocFolderExists with the location, so the
// message reads naturally for BOTH partial unique indexes: a root
// collision names the repo root, a child collision names the parent.
func docFolderExistsErr(q docFolderQuerier, parentID *int64, name string) error {
	if parentID == nil {
		return fmt.Errorf("%w: %q is already a folder at the root of this repo's page tree", ErrDocFolderExists, name)
	}
	parent, err := getDocFolder(q, *parentID)
	if err != nil {
		return fmt.Errorf("%w: %q is already a folder inside folder %d", ErrDocFolderExists, name, *parentID)
	}
	return fmt.Errorf("%w: %q is already a folder inside %q", ErrDocFolderExists, name, parent.Name)
}

// mapDocFolderUnique turns a late UNIQUE violation (one that lost a race
// with a concurrent writer after ensureDocFolderNameFree passed) into
// the same located ErrDocFolderExists. A uuid collision is left alone —
// that is a caller bug, not a naming clash.
func mapDocFolderUnique(err error, q docFolderQuerier, parentID *int64, name string) error {
	if err == nil || !strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return err
	}
	if strings.Contains(err.Error(), "uuid") {
		return err
	}
	return docFolderExistsErr(q, parentID, name)
}

// nextDocFolderPosition returns the append-to-the-end slot for a new or
// re-parented sibling.
func nextDocFolderPosition(q docFolderQuerier, repoID int64, parentID *int64) (int, error) {
	var pos int
	err := q.QueryRow(
		`SELECT COALESCE(MAX(position), -1) + 1 FROM doc_folders WHERE repo_id = ? AND parent_id IS ?`,
		repoID, parentID,
	).Scan(&pos)
	if err != nil {
		return 0, err
	}
	return pos, nil
}

// sameFolderRef reports whether two optional folder references point at
// the same place, treating nil (the tree root) as a value.
func sameFolderRef(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// newFolderPath derives the FolderPath view of a root-first, inclusive
// ancestor chain.
func newFolderPath(chain []*model.DocFolder) *FolderPath {
	segs := make([]string, len(chain))
	for i, f := range chain {
		segs[i] = f.Name
	}
	leaf := chain[len(chain)-1]
	return &FolderPath{
		ID:       leaf.ID,
		UUID:     leaf.UUID,
		Display:  strings.Join(segs, FolderPathSeparator),
		Segments: segs,
		Chain:    chain,
		Depth:    len(chain),
	}
}

func getDocFolder(q docFolderQuerier, id int64) (*model.DocFolder, error) {
	return scanDocFolder(q.QueryRow(`SELECT `+docFolderCols+` FROM doc_folders WHERE id = ?`, id))
}

func scanDocFolders(rows *sql.Rows) ([]*model.DocFolder, error) {
	var out []*model.DocFolder
	for rows.Next() {
		f, err := scanDocFolder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func scanDocFolder(row rowScanner) (*model.DocFolder, error) {
	var (
		f        model.DocFolder
		parentID sql.NullInt64
	)
	err := row.Scan(&f.ID, &f.UUID, &f.RepoID, &parentID, &f.Name, &f.Position, &f.CreatedAt, &f.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan doc folder: %w", err)
	}
	if parentID.Valid {
		p := parentID.Int64
		f.ParentID = &p
	}
	return &f, nil
}
