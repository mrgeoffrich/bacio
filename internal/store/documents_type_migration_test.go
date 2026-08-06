package store

import (
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestMigrateDocumentsTypeCheck simulates a pre-BACI-115 DB whose
// documents.type CHECK omits the four new types (plan, transcript,
// rendered_transcript, review), and asserts the migration rebuilds
// the table so a `transcript` row inserts cleanly afterwards.
func TestMigrateDocumentsTypeCheck(t *testing.T) {
	s, repo, _ := seedRepoAndIssue(t)

	// A fresh DB already carries the widened CHECK from schema.sql.
	stale, err := documentsTypeCheckIsStale(s.DB)
	if err != nil {
		t.Fatalf("staleness check on fresh DB: %v", err)
	}
	if stale {
		t.Fatalf("fresh DB: documentsTypeCheckIsStale = true, want false")
	}

	// Recreate documents with the pre-BACI-115 narrow CHECK to mimic an
	// old DB. The table is empty at this point in the fixture, so a
	// drop+recreate loses nothing.
	if _, err := s.DB.Exec(`DROP TABLE documents`); err != nil {
		t.Fatalf("drop documents: %v", err)
	}
	if _, err := s.DB.Exec(`
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
		)`); err != nil {
		t.Fatalf("recreate old documents: %v", err)
	}

	// Seed a project_complete row so the rebuild has data to copy.
	if _, err := s.DB.Exec(`
		INSERT INTO documents (uuid, repo_id, filename, type, content, size_bytes)
		VALUES ('uuid-legacy', ?, 'legacy.md', 'project_complete', 'body', 4)`,
		repo.ID,
	); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	stale, err = documentsTypeCheckIsStale(s.DB)
	if err != nil {
		t.Fatalf("staleness check on old DB: %v", err)
	}
	if !stale {
		t.Fatalf("old-shape DB: documentsTypeCheckIsStale = false, want true")
	}

	// Before the migration, a 'transcript' insert is rejected by the CHECK.
	if _, err := s.DB.Exec(
		`INSERT INTO documents (uuid, repo_id, filename, type, content, size_bytes)
		 VALUES ('uuid-pre', ?, 'pre.jsonl', 'transcript', '{}', 2)`,
		repo.ID,
	); err == nil {
		t.Fatal("insert type='transcript' on old-shape DB succeeded, want CHECK violation")
	}

	if err := migrateDocumentsTypeCheck(s.DB); err != nil {
		t.Fatalf("migrateDocumentsTypeCheck: %v", err)
	}

	// Post-migration the staleness check flips false.
	stale, err = documentsTypeCheckIsStale(s.DB)
	if err != nil {
		t.Fatalf("staleness check post-migration: %v", err)
	}
	if stale {
		t.Fatalf("post-migration: documentsTypeCheckIsStale = true, want false")
	}

	// The legacy row survived the rebuild.
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM documents WHERE filename='legacy.md' AND type='project_complete'`).Scan(&n); err != nil {
		t.Fatalf("count legacy: %v", err)
	}
	if n != 1 {
		t.Fatalf("legacy row count = %d, want 1", n)
	}

	// The rebuild above re-creates `documents` from a hard-coded CREATE
	// plus an explicit column list, so it drops the pivot's folder_id /
	// folder_position columns — precisely the ordering hazard the pivot
	// ALTERs are pinned to the END of migrate() to survive. Open() always
	// runs the whole of migrate(), so the columns come straight back; do
	// the same here before exercising the store API, whose docCols now
	// projects them.
	if err := migrate(s.DB); err != nil {
		t.Fatalf("migrate after the documents rebuild: %v", err)
	}
	for _, col := range []string{"folder_id", "folder_position"} {
		has, err := columnExists(s.DB, "documents", col)
		if err != nil {
			t.Fatalf("columnExists(documents, %s): %v", col, err)
		}
		if !has {
			t.Fatalf("documents.%s missing after the rebuild + migrate(); the pivot ALTERs must stay at the end of migrate()", col)
		}
	}

	// Each of the BACI-115 types plus the later session_retro addition
	// inserts cleanly through the store API after the rebuild.
	for _, tt := range []model.DocumentType{
		model.DocTypePlan,
		model.DocTypeTranscript,
		model.DocTypeRenderedTranscript,
		model.DocTypeReview,
		model.DocTypeSessionRetro,
	} {
		filename := "test-" + string(tt) + ".md"
		if _, err := s.CreateDocument(repo.ID, filename, tt, "body", ""); err != nil {
			t.Errorf("CreateDocument(%q): %v", tt, err)
		}
	}

	// idx_documents_type still exists (the rebuild recreates it).
	var idxSQL string
	if err := s.DB.QueryRow(`SELECT sql FROM sqlite_master WHERE type='index' AND name='idx_documents_type'`).Scan(&idxSQL); err != nil {
		t.Fatalf("idx_documents_type missing after rebuild: %v", err)
	}
	if !strings.Contains(idxSQL, "documents") {
		t.Errorf("idx_documents_type SQL doesn't reference documents: %q", idxSQL)
	}

	// And running the migration a second time is a no-op (idempotent).
	if err := migrateDocumentsTypeCheck(s.DB); err != nil {
		t.Fatalf("migrateDocumentsTypeCheck (second run): %v", err)
	}
}
