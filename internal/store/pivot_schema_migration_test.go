package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// Legacy table shapes used to build a pre-pivot fixture DB. Each one is
// the table as it stood BEFORE the workspaces / Kanban / doc-folders
// pivot: no repos.kind, no documents.folder_id / folder_position, no
// issues.kanban_column_id / kanban_position, and a sync_state.kind
// CHECK that predates the two new record kinds.
//
// issues and documents deliberately carry their oldest CHECK-bearing
// shapes so the table-REBUILD migrations (migrateIssuesStateCheck,
// migrateIssuesDropUserActionReason, migrateDocumentsTypeCheck) all fire
// during Open(). Those rebuilds re-create their table from a hard-coded
// CREATE plus an explicit column list that knows nothing about the new
// pivot columns — so this fixture is the regression guard for the
// ordering rule in migrate(): the pivot ALTERs must run AFTER every
// rebuild, or the new columns are added and then silently dropped.
const (
	// repos as of the relaxed partial-unique era, minus `kind`.
	legacyReposTable = `
CREATE TABLE repos (
	id                 INTEGER PRIMARY KEY AUTOINCREMENT,
	uuid               TEXT    NOT NULL,
	prefix             TEXT    NOT NULL UNIQUE,
	name               TEXT    NOT NULL,
	path               TEXT    NOT NULL,
	remote_url         TEXT    NOT NULL DEFAULT '',
	next_issue_number  INTEGER NOT NULL DEFAULT 1,
	created_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
)`

	// documents with the pre-BACI-115 narrow type CHECK.
	legacyDocumentsTable = `
CREATE TABLE documents (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	uuid        TEXT    NOT NULL,
	repo_id     INTEGER NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
	filename    TEXT    NOT NULL,
	type        TEXT    NOT NULL CHECK (type IN
	              ('user_docs','project_in_planning','project_in_progress',
	               'project_complete','vendor_docs','architecture','designs',
	               'testing_plans')),
	content     TEXT    NOT NULL,
	size_bytes  INTEGER NOT NULL,
	source_path TEXT    NOT NULL DEFAULT '',
	archived_at DATETIME,
	created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(repo_id, filename)
)`

	// sync_state with the pre-pivot six-kind CHECK.
	legacySyncStateTable = `
CREATE TABLE sync_state (
	uuid             TEXT    NOT NULL PRIMARY KEY,
	kind             TEXT    NOT NULL CHECK (kind IN
	                   ('issue','feature','document','comment','feature_comment','repo')),
	last_synced_at   DATETIME NOT NULL,
	last_synced_hash TEXT    NOT NULL
)`
)

// newPrePivotFixtureDB writes a database file carrying the legacy table
// shapes above, seeds one row in each, and returns its path. The
// connection is closed before returning so the caller can hand the path
// straight to Open().
func newPrePivotFixtureDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pre-pivot.sqlite")
	// Foreign keys stay off on the fixture connection (SQLite's default)
	// — we are hand-building a partial schema and don't want ordering of
	// the CREATEs to matter.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	defer db.Close()

	for _, ddl := range []string{
		legacyReposTable,
		oldIssuesTableWithStateCheck,
		legacyDocumentsTable,
		legacySyncStateTable,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatalf("create legacy table: %v\n%s", err, ddl)
		}
	}

	if _, err := db.Exec(`
		INSERT INTO repos (id, uuid, prefix, name, path, remote_url, next_issue_number)
		VALUES (1, 'repo-uuid-legacy', 'OLD', 'legacy', '/tmp/legacy-checkout', '', 2)`,
	); err != nil {
		t.Fatalf("seed legacy repo: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO issues (id, uuid, repo_id, number, title, description, state)
		VALUES (1, 'issue-uuid-legacy', 1, 1, 'legacy issue', '', 'todo')`,
	); err != nil {
		t.Fatalf("seed legacy issue: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO documents (id, uuid, repo_id, filename, type, content, size_bytes)
		VALUES (1, 'doc-uuid-legacy', 1, 'legacy.md', 'project_complete', 'body', 4)`,
	); err != nil {
		t.Fatalf("seed legacy document: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO sync_state (uuid, kind, last_synced_at, last_synced_hash)
		VALUES ('doc-uuid-legacy', 'document', '2026-01-01 00:00:00', 'deadbeef')`,
	); err != nil {
		t.Fatalf("seed legacy sync_state: %v", err)
	}
	return path
}

// TestPivotSchemaMigration opens a pre-pivot fixture DB and asserts the
// full pivot schema lands: five new columns on three pre-existing
// tables, two brand-new tables, and a widened sync_state.kind CHECK —
// all without disturbing the rows that were already there.
func TestPivotSchemaMigration(t *testing.T) {
	path := newPrePivotFixtureDB(t)

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open on pre-pivot DB: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	// --- the five new columns on pre-existing tables ---------------
	for _, c := range []struct{ table, column string }{
		{"repos", "kind"},
		{"documents", "folder_id"},
		{"documents", "folder_position"},
		{"issues", "kanban_column_id"},
		{"issues", "kanban_position"},
	} {
		has, err := columnExists(s.DB, c.table, c.column)
		if err != nil {
			t.Fatalf("columnExists(%s.%s): %v", c.table, c.column, err)
		}
		if !has {
			t.Errorf("%s.%s missing after migrate — a table-rebuild migration probably ran AFTER the pivot ALTERs and dropped it", c.table, c.column)
		}
	}

	// --- the two new tables ----------------------------------------
	for _, table := range []string{"doc_folders", "kanban_columns"} {
		var n int
		if err := s.DB.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table,
		).Scan(&n); err != nil {
			t.Fatalf("look up table %s: %v", table, err)
		}
		if n != 1 {
			t.Errorf("table %s missing after Open (found %d)", table, n)
		}
	}

	// --- the new indexes -------------------------------------------
	for _, idx := range []string{
		"uniq_doc_folders_uuid",
		"uniq_doc_folders_child",
		"uniq_doc_folders_root",
		"uniq_kanban_columns_name",
		"uniq_kanban_columns_uuid",
	} {
		var n int
		if err := s.DB.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, idx,
		).Scan(&n); err != nil {
			t.Fatalf("look up index %s: %v", idx, err)
		}
		if n != 1 {
			t.Errorf("index %s missing after Open (found %d)", idx, n)
		}
	}

	// --- pre-existing rows survive with sane pivot defaults ---------
	var kind string
	if err := s.DB.QueryRow(`SELECT kind FROM repos WHERE prefix='OLD'`).Scan(&kind); err != nil {
		t.Fatalf("read migrated repo: %v", err)
	}
	if kind != "git" {
		t.Errorf("pre-existing repo kind = %q, want %q", kind, "git")
	}

	var (
		folderID  sql.NullInt64
		folderPos int
		docCount  int
	)
	if err := s.DB.QueryRow(
		`SELECT folder_id, folder_position FROM documents WHERE filename='legacy.md'`,
	).Scan(&folderID, &folderPos); err != nil {
		t.Fatalf("read migrated document: %v", err)
	}
	if folderID.Valid {
		t.Errorf("pre-existing document folder_id = %d, want NULL (tree root)", folderID.Int64)
	}
	if folderPos != 0 {
		t.Errorf("pre-existing document folder_position = %d, want 0", folderPos)
	}
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM documents`).Scan(&docCount); err != nil {
		t.Fatalf("count documents: %v", err)
	}
	if docCount != 1 {
		t.Errorf("documents count = %d, want 1 (the legacy row must survive the rebuild)", docCount)
	}

	var (
		kanbanColID sql.NullInt64
		kanbanPos   int
		issueTitle  string
	)
	if err := s.DB.QueryRow(
		`SELECT kanban_column_id, kanban_position, title FROM issues WHERE uuid='issue-uuid-legacy'`,
	).Scan(&kanbanColID, &kanbanPos, &issueTitle); err != nil {
		t.Fatalf("read migrated issue: %v", err)
	}
	if kanbanColID.Valid {
		t.Errorf("pre-existing issue kanban_column_id = %d, want NULL (not on the Kanban)", kanbanColID.Int64)
	}
	if kanbanPos != 0 {
		t.Errorf("pre-existing issue kanban_position = %d, want 0", kanbanPos)
	}
	if issueTitle != "legacy issue" {
		t.Errorf("pre-existing issue title = %q, want %q", issueTitle, "legacy issue")
	}

	// --- sync_state.kind CHECK was widened -------------------------
	if err := s.MarkSynced("folder-uuid-1", SyncKindDocFolder, "hash-a"); err != nil {
		t.Errorf("MarkSynced(doc_folder) on migrated DB: %v", err)
	}
	if err := s.MarkSynced("column-uuid-1", SyncKindKanbanColumn, "hash-b"); err != nil {
		t.Errorf("MarkSynced(kanban_column) on migrated DB: %v", err)
	}
	var legacyHash string
	if err := s.DB.QueryRow(
		`SELECT last_synced_hash FROM sync_state WHERE uuid='doc-uuid-legacy'`,
	).Scan(&legacyHash); err != nil {
		t.Fatalf("legacy sync_state row lost in the CHECK rebuild: %v", err)
	}
	if legacyHash != "deadbeef" {
		t.Errorf("legacy sync_state hash = %q, want %q", legacyHash, "deadbeef")
	}

	// --- Open() is idempotent on an already-migrated DB -------------
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open on migrated DB: %v", err)
	}
	t.Cleanup(func() { _ = s2.Close() })
	if err := s2.DB.QueryRow(`SELECT kind FROM repos WHERE prefix='OLD'`).Scan(&kind); err != nil {
		t.Fatalf("read repo after second Open: %v", err)
	}
	if kind != "git" {
		t.Errorf("after second Open, repo kind = %q, want %q", kind, "git")
	}
}

// TestDocFolderPartialUniques locks in the reason doc_folders carries
// TWO partial unique indexes instead of one composite UNIQUE(repo_id,
// parent_id, name): SQLite treats NULLs as DISTINCT inside a composite
// UNIQUE, so a plain three-column constraint would silently admit two
// root folders with the same name.
func TestDocFolderPartialUniques(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("TREE", "tree-test", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}

	insert := func(uuid string, parent *int64, name string) error {
		_, err := s.DB.Exec(
			`INSERT INTO doc_folders (uuid, repo_id, parent_id, name) VALUES (?, ?, ?, ?)`,
			uuid, repo.ID, parent, name,
		)
		return err
	}

	// Two ROOT folders with the same name must collide (this is the
	// case a naive composite UNIQUE would let through).
	if err := insert("f-root-1", nil, "Design"); err != nil {
		t.Fatalf("insert first root folder: %v", err)
	}
	if err := insert("f-root-2", nil, "Design"); err == nil {
		t.Error("second root folder named 'Design' inserted, want uniq_doc_folders_root violation")
	}

	var rootID int64
	if err := s.DB.QueryRow(`SELECT id FROM doc_folders WHERE uuid='f-root-1'`).Scan(&rootID); err != nil {
		t.Fatalf("read root folder id: %v", err)
	}

	// Two children of the SAME parent with the same name collide.
	if err := insert("f-child-1", &rootID, "API"); err != nil {
		t.Fatalf("insert first child folder: %v", err)
	}
	if err := insert("f-child-2", &rootID, "API"); err == nil {
		t.Error("second child named 'API' under the same parent inserted, want uniq_doc_folders_child violation")
	}

	// The same name under a DIFFERENT parent is fine — including a root
	// folder that shares its name with a nested one.
	if err := insert("f-root-3", nil, "API"); err != nil {
		t.Errorf("root folder named 'API' alongside a nested 'API': %v", err)
	}

	// uuid is globally unique regardless of repo / parent.
	if err := insert("f-root-1", nil, "Something else"); err == nil {
		t.Error("duplicate folder uuid inserted, want uniq_doc_folders_uuid violation")
	}
}

// TestPivotForeignKeyActions proves the two ON DELETE SET NULL clauses
// added via ALTER TABLE are actually enforced: deleting a folder must
// re-root its pages rather than destroy them, and deleting a Kanban
// lane must drop its cards off the board rather than delete the issues.
func TestPivotForeignKeyActions(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("FKEY", "fk-test", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	doc, err := s.CreateDocument(repo.ID, "page.md", "user_docs", "body", "")
	if err != nil {
		t.Fatalf("create document: %v", err)
	}
	issue, err := s.CreateIssue(repo.ID, nil, "card", "", "todo", nil, "", "")
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}

	res, err := s.DB.Exec(
		`INSERT INTO doc_folders (uuid, repo_id, name) VALUES ('fk-folder', ?, 'Design')`, repo.ID)
	if err != nil {
		t.Fatalf("insert folder: %v", err)
	}
	folderID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("folder id: %v", err)
	}
	if _, err := s.DB.Exec(
		`UPDATE documents SET folder_id = ?, folder_position = 3 WHERE id = ?`, folderID, doc.ID); err != nil {
		t.Fatalf("file document into folder: %v", err)
	}

	res, err = s.DB.Exec(
		`INSERT INTO kanban_columns (uuid, repo_id, name, position) VALUES ('fk-col', ?, 'Doing', 1)`, repo.ID)
	if err != nil {
		t.Fatalf("insert kanban column: %v", err)
	}
	colID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("column id: %v", err)
	}
	if _, err := s.DB.Exec(
		`UPDATE issues SET kanban_column_id = ?, kanban_position = 2 WHERE id = ?`, colID, issue.ID); err != nil {
		t.Fatalf("place issue on the board: %v", err)
	}

	if _, err := s.DB.Exec(`DELETE FROM doc_folders WHERE id = ?`, folderID); err != nil {
		t.Fatalf("delete folder: %v", err)
	}
	if _, err := s.DB.Exec(`DELETE FROM kanban_columns WHERE id = ?`, colID); err != nil {
		t.Fatalf("delete kanban column: %v", err)
	}

	var gotFolder sql.NullInt64
	if err := s.DB.QueryRow(`SELECT folder_id FROM documents WHERE id = ?`, doc.ID).Scan(&gotFolder); err != nil {
		t.Fatalf("document destroyed with its folder: %v", err)
	}
	if gotFolder.Valid {
		t.Errorf("document folder_id = %d after folder delete, want NULL", gotFolder.Int64)
	}

	var gotCol sql.NullInt64
	if err := s.DB.QueryRow(`SELECT kanban_column_id FROM issues WHERE id = ?`, issue.ID).Scan(&gotCol); err != nil {
		t.Fatalf("issue destroyed with its Kanban column: %v", err)
	}
	if gotCol.Valid {
		t.Errorf("issue kanban_column_id = %d after column delete, want NULL", gotCol.Int64)
	}
}
