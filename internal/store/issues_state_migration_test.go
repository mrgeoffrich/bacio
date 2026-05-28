package store

import (
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// oldIssuesTableWithStateCheck mirrors the issues shape AFTER the
// Pipeline column ALTERs but BEFORE the CHECK drop — i.e. the exact
// precondition migrateIssuesStateCheck expects on an upgrading DB: every
// current column present, plus the legacy 6-state CHECK on `state`.
const oldIssuesTableWithStateCheck = `
CREATE TABLE issues (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	uuid        TEXT    NOT NULL,
	repo_id     INTEGER NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
	number      INTEGER NOT NULL,
	feature_id  INTEGER REFERENCES features(id) ON DELETE SET NULL,
	title       TEXT    NOT NULL,
	description TEXT    NOT NULL DEFAULT '',
	state       TEXT    NOT NULL CHECK (state IN
	              ('todo','in_progress','needs_action','in_review','done','cancelled')),
	assignee    TEXT    NOT NULL DEFAULT '',
	archived_at DATETIME,
	terminal_at DATETIME,
	user_action_reason_type TEXT,
	base_branch TEXT,
	priority    INTEGER NOT NULL DEFAULT 0,
	engine_mode TEXT    NOT NULL DEFAULT 'off',
	engine_pause_reason TEXT NOT NULL DEFAULT '',
	created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(repo_id, number)
)`

// TestMigrateIssuesStateCheck is the crown-jewel migration test: the
// issues rebuild must drop the state CHECK WITHOUT cascade-deleting any
// of the many child tables that REFERENCE issues. It seeds one row in
// every child (comments, tags, PRs, relations, claims, document_links,
// pipeline_jobs), runs the rebuild, and asserts each child survives and
// the two new Pipeline states become writable.
func TestMigrateIssuesStateCheck(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("PIPE", "pipe-test", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	feat, err := s.CreateFeature(repo.ID, "f1", "Feature 1", "", "", "")
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}
	sess, err := s.UpsertAgentSession(UpsertAgentSessionIn{SessionID: "sess-mig", RepoID: repo.ID, Actor: "agent-x"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	doc, err := s.CreateDocument(repo.ID, "d.md", model.DocTypePlan, "body", "")
	if err != nil {
		t.Fatalf("create document: %v", err)
	}

	// Fresh DB carries no state CHECK (schema.sql dropped it).
	if present, err := issuesStateCheckPresent(s.DB); err != nil {
		t.Fatalf("present (fresh): %v", err)
	} else if present {
		t.Fatal("fresh DB: issuesStateCheckPresent = true, want false")
	}

	// Simulate an upgrading DB: swap in the old-shape issues table (empty,
	// so the drop cascades nothing), then seed a parent issue + one row in
	// every child table.
	if _, err := s.DB.Exec(`DROP TABLE issues`); err != nil {
		t.Fatalf("drop issues: %v", err)
	}
	if _, err := s.DB.Exec(oldIssuesTableWithStateCheck); err != nil {
		t.Fatalf("recreate old issues: %v", err)
	}
	if _, err := s.DB.Exec(`
		INSERT INTO issues (id, uuid, repo_id, number, feature_id, title, state)
		VALUES (1, 'u1', ?, 1, ?, 'parent', 'todo'),
		       (2, 'u2', ?, 2, NULL, 'other', 'todo')`,
		repo.ID, feat.ID, repo.ID,
	); err != nil {
		t.Fatalf("seed issues: %v", err)
	}
	seedChildren := []struct {
		name string
		stmt string
		args []any
	}{
		{"comments", `INSERT INTO comments (uuid, issue_id, author, body) VALUES ('cu', 1, 'me', 'hi')`, nil},
		{"issue_tags", `INSERT INTO issue_tags (issue_id, tag) VALUES (1, 'urgent')`, nil},
		{"issue_pull_requests", `INSERT INTO issue_pull_requests (issue_id, url) VALUES (1, 'http://pr/1')`, nil},
		{"issue_relations", `INSERT INTO issue_relations (from_issue_id, to_issue_id, type) VALUES (1, 2, 'blocks')`, nil},
		{"agent_claims", `INSERT INTO agent_claims (session_pk, issue_id) VALUES (?, 1)`, []any{sess.ID}},
		{"document_links", `INSERT INTO document_links (document_id, issue_id) VALUES (?, 1)`, []any{doc.ID}},
		{"pipeline_jobs", `INSERT INTO pipeline_jobs (issue_id, sequence, mode, status) VALUES (1, 1, 'plan', 'pending')`, nil},
	}
	for _, c := range seedChildren {
		if _, err := s.DB.Exec(c.stmt, c.args...); err != nil {
			t.Fatalf("seed %s: %v", c.name, err)
		}
	}

	// Precondition: CHECK present and the new state rejected.
	if present, err := issuesStateCheckPresent(s.DB); err != nil {
		t.Fatalf("present (old): %v", err)
	} else if !present {
		t.Fatal("old-shape DB: issuesStateCheckPresent = false, want true")
	}
	if _, err := s.DB.Exec(`UPDATE issues SET state='in_pipeline' WHERE id=1`); err == nil {
		t.Fatal("UPDATE to in_pipeline on old-shape DB succeeded, want CHECK violation")
	}

	// Run the rebuild.
	if err := migrateIssuesStateCheck(s.DB); err != nil {
		t.Fatalf("migrateIssuesStateCheck: %v", err)
	}

	// CHECK gone.
	if present, err := issuesStateCheckPresent(s.DB); err != nil {
		t.Fatalf("present (post): %v", err)
	} else if present {
		t.Fatal("post-rebuild: issuesStateCheckPresent = true, want false")
	}

	// Every child survived the DROP TABLE — the cascade-safety assertion.
	countCheck := func(query string, want int) {
		t.Helper()
		var n int
		if err := s.DB.QueryRow(query).Scan(&n); err != nil {
			t.Fatalf("count (%s): %v", query, err)
		}
		if n != want {
			t.Fatalf("count (%s) = %d, want %d", query, n, want)
		}
	}
	countCheck(`SELECT COUNT(*) FROM issues`, 2)
	countCheck(`SELECT COUNT(*) FROM comments WHERE issue_id=1`, 1)
	countCheck(`SELECT COUNT(*) FROM issue_tags WHERE issue_id=1`, 1)
	countCheck(`SELECT COUNT(*) FROM issue_pull_requests WHERE issue_id=1`, 1)
	countCheck(`SELECT COUNT(*) FROM issue_relations WHERE from_issue_id=1`, 1)
	countCheck(`SELECT COUNT(*) FROM agent_claims WHERE issue_id=1`, 1)
	countCheck(`SELECT COUNT(*) FROM document_links WHERE issue_id=1`, 1)
	countCheck(`SELECT COUNT(*) FROM pipeline_jobs WHERE issue_id=1`, 1)

	// The two new Pipeline states are now writable.
	if _, err := s.DB.Exec(`UPDATE issues SET state='in_pipeline' WHERE id=1`); err != nil {
		t.Fatalf("UPDATE to in_pipeline post-rebuild: %v", err)
	}
	if _, err := s.DB.Exec(`UPDATE issues SET state='to_be_shipped' WHERE id=2`); err != nil {
		t.Fatalf("UPDATE to to_be_shipped post-rebuild: %v", err)
	}

	// Indexes recreated by the rebuild.
	var idxName string
	if err := s.DB.QueryRow(`SELECT name FROM sqlite_master WHERE type='index' AND name='idx_issues_state'`).Scan(&idxName); err != nil {
		t.Fatalf("idx_issues_state missing after rebuild: %v", err)
	}

	// Idempotent: a second run is a no-op (CHECK already gone).
	if err := migrateIssuesStateCheck(s.DB); err != nil {
		t.Fatalf("migrateIssuesStateCheck (second run): %v", err)
	}
}
