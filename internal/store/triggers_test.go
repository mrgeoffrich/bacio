package store

import (
	"testing"
	"time"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// The BACI-144 triggers promote the "bump <parent>.updated_at when
// side-data changes" invariant out of per-call-site Go code into the
// schema. These tests pin each trigger in isolation: insert (or
// delete) a side-data row directly via tx.Exec — bypassing the higher
// level Store helpers — and assert that the parent's updated_at
// advanced past a captured Before timestamp.
//
// SQLite's CURRENT_TIMESTAMP is 1-second granular, so each test
// sleeps a hair over a second between the Before capture and the
// trigger-firing write. Matches the existing precedent in
// agents_test.go::TestAgentLastSeenBump.
const triggerSleep = 1100 * time.Millisecond

// seedIssueForTriggers creates a repo and one issue, then forces the
// issue's updated_at to a known-old value so the trigger's
// CURRENT_TIMESTAMP bump is unambiguously detectable.
func seedIssueForTriggers(t *testing.T) (*Store, int64) {
	t.Helper()
	s := newTestStore(t)
	repo, err := s.CreateRepo("TRG", "trigger-tests", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	iss, err := s.CreateIssue(repo.ID, nil, "Trigger fixture", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	if _, err := s.DB.Exec(
		`UPDATE issues SET updated_at = '2020-01-01 00:00:00' WHERE id = ?`,
		iss.ID,
	); err != nil {
		t.Fatalf("force ts: %v", err)
	}
	return s, iss.ID
}

// seedTwoIssuesForTriggers creates a repo and two issues, both forced
// to an old updated_at — for the relation triggers, which bump both
// endpoints.
func seedTwoIssuesForTriggers(t *testing.T) (*Store, int64, int64) {
	t.Helper()
	s := newTestStore(t)
	repo, err := s.CreateRepo("TRG", "trigger-tests", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	a, err := s.CreateIssue(repo.ID, nil, "From", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("create from: %v", err)
	}
	b, err := s.CreateIssue(repo.ID, nil, "To", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("create to: %v", err)
	}
	if _, err := s.DB.Exec(
		`UPDATE issues SET updated_at = '2020-01-01 00:00:00' WHERE id IN (?, ?)`,
		a.ID, b.ID,
	); err != nil {
		t.Fatalf("force ts: %v", err)
	}
	return s, a.ID, b.ID
}

// seedDocumentForTriggers creates a repo, an issue (for link targets)
// and a document with a known-old updated_at.
func seedDocumentForTriggers(t *testing.T) (s *Store, docID, issueID int64) {
	t.Helper()
	s = newTestStore(t)
	repo, err := s.CreateRepo("TRG", "trigger-tests", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	iss, err := s.CreateIssue(repo.ID, nil, "Doc target", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	doc, err := s.CreateDocument(repo.ID, "trigger-doc.md", model.DocTypeArchitecture, "body\n", "")
	if err != nil {
		t.Fatalf("create doc: %v", err)
	}
	if _, err := s.DB.Exec(
		`UPDATE documents SET updated_at = '2020-01-01 00:00:00' WHERE id = ?`,
		doc.ID,
	); err != nil {
		t.Fatalf("force ts: %v", err)
	}
	return s, doc.ID, iss.ID
}

func issueUpdatedAt(t *testing.T, s *Store, id int64) time.Time {
	t.Helper()
	var ts time.Time
	if err := s.DB.QueryRow(`SELECT updated_at FROM issues WHERE id = ?`, id).Scan(&ts); err != nil {
		t.Fatalf("read updated_at: %v", err)
	}
	return ts
}

func documentUpdatedAt(t *testing.T, s *Store, id int64) time.Time {
	t.Helper()
	var ts time.Time
	if err := s.DB.QueryRow(`SELECT updated_at FROM documents WHERE id = ?`, id).Scan(&ts); err != nil {
		t.Fatalf("read updated_at: %v", err)
	}
	return ts
}

// --- issue_pull_requests --------------------------------------------

func TestTrigger_BumpIssueUpdatedOnPRInsert(t *testing.T) {
	s, issueID := seedIssueForTriggers(t)
	before := issueUpdatedAt(t, s, issueID)
	time.Sleep(triggerSleep)
	if _, err := s.DB.Exec(
		`INSERT INTO issue_pull_requests (issue_id, url) VALUES (?, ?)`,
		issueID, "https://github.com/x/y/pull/1",
	); err != nil {
		t.Fatalf("insert pr: %v", err)
	}
	after := issueUpdatedAt(t, s, issueID)
	if !after.After(before) {
		t.Fatalf("PR insert did not advance issues.updated_at: before=%v after=%v", before, after)
	}
}

func TestTrigger_BumpIssueUpdatedOnPRDelete(t *testing.T) {
	s, issueID := seedIssueForTriggers(t)
	if _, err := s.DB.Exec(
		`INSERT INTO issue_pull_requests (issue_id, url) VALUES (?, ?)`,
		issueID, "https://github.com/x/y/pull/1",
	); err != nil {
		t.Fatalf("seed pr: %v", err)
	}
	// Reset the parent's updated_at so the delete is what we measure.
	if _, err := s.DB.Exec(
		`UPDATE issues SET updated_at = '2020-01-01 00:00:00' WHERE id = ?`,
		issueID,
	); err != nil {
		t.Fatalf("reset ts: %v", err)
	}
	before := issueUpdatedAt(t, s, issueID)
	time.Sleep(triggerSleep)
	if _, err := s.DB.Exec(
		`DELETE FROM issue_pull_requests WHERE issue_id = ?`, issueID,
	); err != nil {
		t.Fatalf("delete pr: %v", err)
	}
	after := issueUpdatedAt(t, s, issueID)
	if !after.After(before) {
		t.Fatalf("PR delete did not advance issues.updated_at: before=%v after=%v", before, after)
	}
}

// --- issue_relations -------------------------------------------------

func TestTrigger_BumpIssueUpdatedOnRelationInsert(t *testing.T) {
	s, fromID, toID := seedTwoIssuesForTriggers(t)
	beforeFrom := issueUpdatedAt(t, s, fromID)
	beforeTo := issueUpdatedAt(t, s, toID)
	time.Sleep(triggerSleep)
	if _, err := s.DB.Exec(
		`INSERT INTO issue_relations (from_issue_id, to_issue_id, type) VALUES (?, ?, 'blocks')`,
		fromID, toID,
	); err != nil {
		t.Fatalf("insert relation: %v", err)
	}
	if got := issueUpdatedAt(t, s, fromID); !got.After(beforeFrom) {
		t.Fatalf("relation insert did not advance issues.updated_at on from: before=%v after=%v", beforeFrom, got)
	}
	if got := issueUpdatedAt(t, s, toID); !got.After(beforeTo) {
		t.Fatalf("relation insert did not advance issues.updated_at on to: before=%v after=%v", beforeTo, got)
	}
}

func TestTrigger_BumpIssueUpdatedOnRelationDelete(t *testing.T) {
	s, fromID, toID := seedTwoIssuesForTriggers(t)
	if _, err := s.DB.Exec(
		`INSERT INTO issue_relations (from_issue_id, to_issue_id, type) VALUES (?, ?, 'blocks')`,
		fromID, toID,
	); err != nil {
		t.Fatalf("seed relation: %v", err)
	}
	if _, err := s.DB.Exec(
		`UPDATE issues SET updated_at = '2020-01-01 00:00:00' WHERE id IN (?, ?)`,
		fromID, toID,
	); err != nil {
		t.Fatalf("reset ts: %v", err)
	}
	beforeFrom := issueUpdatedAt(t, s, fromID)
	beforeTo := issueUpdatedAt(t, s, toID)
	time.Sleep(triggerSleep)
	if _, err := s.DB.Exec(
		`DELETE FROM issue_relations WHERE from_issue_id = ? AND to_issue_id = ?`,
		fromID, toID,
	); err != nil {
		t.Fatalf("delete relation: %v", err)
	}
	if got := issueUpdatedAt(t, s, fromID); !got.After(beforeFrom) {
		t.Fatalf("relation delete did not advance issues.updated_at on from: before=%v after=%v", beforeFrom, got)
	}
	if got := issueUpdatedAt(t, s, toID); !got.After(beforeTo) {
		t.Fatalf("relation delete did not advance issues.updated_at on to: before=%v after=%v", beforeTo, got)
	}
}

// --- issue_tags ------------------------------------------------------

func TestTrigger_BumpIssueUpdatedOnTagInsert(t *testing.T) {
	s, issueID := seedIssueForTriggers(t)
	before := issueUpdatedAt(t, s, issueID)
	time.Sleep(triggerSleep)
	if _, err := s.DB.Exec(
		`INSERT INTO issue_tags (issue_id, tag) VALUES (?, ?)`, issueID, "p1",
	); err != nil {
		t.Fatalf("insert tag: %v", err)
	}
	after := issueUpdatedAt(t, s, issueID)
	if !after.After(before) {
		t.Fatalf("tag insert did not advance issues.updated_at: before=%v after=%v", before, after)
	}
}

func TestTrigger_BumpIssueUpdatedOnTagDelete(t *testing.T) {
	s, issueID := seedIssueForTriggers(t)
	if _, err := s.DB.Exec(
		`INSERT INTO issue_tags (issue_id, tag) VALUES (?, ?)`, issueID, "p1",
	); err != nil {
		t.Fatalf("seed tag: %v", err)
	}
	if _, err := s.DB.Exec(
		`UPDATE issues SET updated_at = '2020-01-01 00:00:00' WHERE id = ?`, issueID,
	); err != nil {
		t.Fatalf("reset ts: %v", err)
	}
	before := issueUpdatedAt(t, s, issueID)
	time.Sleep(triggerSleep)
	if _, err := s.DB.Exec(
		`DELETE FROM issue_tags WHERE issue_id = ? AND tag = ?`, issueID, "p1",
	); err != nil {
		t.Fatalf("delete tag: %v", err)
	}
	after := issueUpdatedAt(t, s, issueID)
	if !after.After(before) {
		t.Fatalf("tag delete did not advance issues.updated_at: before=%v after=%v", before, after)
	}
}

// --- document_links --------------------------------------------------

func TestTrigger_BumpDocumentUpdatedOnLinkInsert(t *testing.T) {
	s, docID, issueID := seedDocumentForTriggers(t)
	before := documentUpdatedAt(t, s, docID)
	time.Sleep(triggerSleep)
	if _, err := s.DB.Exec(
		`INSERT INTO document_links (document_id, issue_id, feature_id, description) VALUES (?, ?, NULL, '')`,
		docID, issueID,
	); err != nil {
		t.Fatalf("insert link: %v", err)
	}
	after := documentUpdatedAt(t, s, docID)
	if !after.After(before) {
		t.Fatalf("link insert did not advance documents.updated_at: before=%v after=%v", before, after)
	}
}

func TestTrigger_BumpDocumentUpdatedOnLinkDelete(t *testing.T) {
	s, docID, issueID := seedDocumentForTriggers(t)
	if _, err := s.DB.Exec(
		`INSERT INTO document_links (document_id, issue_id, feature_id, description) VALUES (?, ?, NULL, '')`,
		docID, issueID,
	); err != nil {
		t.Fatalf("seed link: %v", err)
	}
	if _, err := s.DB.Exec(
		`UPDATE documents SET updated_at = '2020-01-01 00:00:00' WHERE id = ?`, docID,
	); err != nil {
		t.Fatalf("reset ts: %v", err)
	}
	before := documentUpdatedAt(t, s, docID)
	time.Sleep(triggerSleep)
	if _, err := s.DB.Exec(
		`DELETE FROM document_links WHERE document_id = ? AND issue_id = ?`, docID, issueID,
	); err != nil {
		t.Fatalf("delete link: %v", err)
	}
	after := documentUpdatedAt(t, s, docID)
	if !after.After(before) {
		t.Fatalf("link delete did not advance documents.updated_at: before=%v after=%v", before, after)
	}
}

// TestTrigger_BumpDocumentUpdatedOnLinkDescriptionUpdate pins the
// AFTER UPDATE OF description trigger — a LinkDocument call that
// upserts an existing (document, target) pair only UPDATEs the
// description column, with no INSERT. Without the description-update
// trigger, that path wouldn't bump documents.updated_at and a sync
// round-trip would clobber the new description.
func TestTrigger_BumpDocumentUpdatedOnLinkDescriptionUpdate(t *testing.T) {
	s, docID, issueID := seedDocumentForTriggers(t)
	if _, err := s.DB.Exec(
		`INSERT INTO document_links (document_id, issue_id, feature_id, description) VALUES (?, ?, NULL, 'old')`,
		docID, issueID,
	); err != nil {
		t.Fatalf("seed link: %v", err)
	}
	if _, err := s.DB.Exec(
		`UPDATE documents SET updated_at = '2020-01-01 00:00:00' WHERE id = ?`, docID,
	); err != nil {
		t.Fatalf("reset ts: %v", err)
	}
	before := documentUpdatedAt(t, s, docID)
	time.Sleep(triggerSleep)
	if _, err := s.DB.Exec(
		`UPDATE document_links SET description = 'new' WHERE document_id = ? AND issue_id = ?`,
		docID, issueID,
	); err != nil {
		t.Fatalf("update description: %v", err)
	}
	after := documentUpdatedAt(t, s, docID)
	if !after.After(before) {
		t.Fatalf("link description update did not advance documents.updated_at: before=%v after=%v", before, after)
	}
}
