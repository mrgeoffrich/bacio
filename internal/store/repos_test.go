package store

import (
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestDeleteRepoCascades pins the invariant that DeleteRepo wipes
// every dependent row across the schema (issues, comments, features,
// documents, links, relations, PRs, tags, tui_settings) via FK
// CASCADE, and that DeleteHistoryByRepo wipes the audit rows the
// CASCADE doesn't reach.
func TestDeleteRepoCascades(t *testing.T) {
	s := newTestStore(t)

	// Build a repo with one of every kind of dependent row, then keep
	// a second repo around to make sure we only delete what we asked
	// for (no overzealous WHERE clauses).
	target, err := s.CreateRepo("KILL", "kill-me", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create target repo: %v", err)
	}
	keep, err := s.CreateRepo("KEEP", "keep-me", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create keep repo: %v", err)
	}

	// Target-side fixtures: feature → issue (with feature_id), comment,
	// tag, relation between two issues, PR, document, document_link.
	feat, err := s.CreateFeature(target.ID, "feat", "Feature", "")
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}
	iss1, err := s.CreateIssue(target.ID, &feat.ID, "first", "", model.StateTodo, []string{"bug"})
	if err != nil {
		t.Fatalf("create issue 1: %v", err)
	}
	iss2, err := s.CreateIssue(target.ID, nil, "second", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("create issue 2: %v", err)
	}
	if _, err := s.CreateComment(CreateCommentIn{IssueID: iss1.ID, Author: "tester", Body: "hi"}); err != nil {
		t.Fatalf("create comment: %v", err)
	}
	if err := s.CreateRelation(iss1.ID, iss2.ID, model.RelBlocks); err != nil {
		t.Fatalf("create relation: %v", err)
	}
	if _, err := s.AttachPR(iss1.ID, "https://example.com/pr/1"); err != nil {
		t.Fatalf("attach PR: %v", err)
	}
	doc, err := s.CreateDocument(target.ID, "design.md", model.DocTypeDesigns, "body", "")
	if err != nil {
		t.Fatalf("create doc: %v", err)
	}
	if _, err := s.LinkDocument(doc.ID, LinkTarget{IssueID: &iss1.ID}, "design"); err != nil {
		t.Fatalf("link doc: %v", err)
	}
	if err := s.SaveHiddenStates(target.ID, map[model.State]bool{model.StateTodo: true}); err != nil {
		t.Fatalf("save hidden states: %v", err)
	}
	if err := s.RecordHistory(model.HistoryEntry{
		RepoID: &target.ID, RepoPrefix: target.Prefix,
		Actor: "tester", Op: "issue.create", Kind: "issue",
		TargetID: &iss1.ID, TargetLabel: iss1.Key,
	}); err != nil {
		t.Fatalf("record history: %v", err)
	}

	// Keep-side: one issue + one history row, untouched by the delete.
	keepIss, err := s.CreateIssue(keep.ID, nil, "keeper", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("create keep issue: %v", err)
	}
	if err := s.RecordHistory(model.HistoryEntry{
		RepoID: &keep.ID, RepoPrefix: keep.Prefix,
		Actor: "tester", Op: "issue.create", Kind: "issue",
		TargetID: &keepIss.ID, TargetLabel: keepIss.Key,
	}); err != nil {
		t.Fatalf("record keep history: %v", err)
	}

	counts, err := s.RepoCascadeCountsForID(target.ID)
	if err != nil {
		t.Fatalf("counts: %v", err)
	}
	want := RepoCascadeCounts{
		Issues: 2, Comments: 1, Relations: 1, PullRequests: 1, Tags: 1,
		Features: 1, Documents: 1, DocumentLinks: 1, TUISettings: 1, History: 1,
	}
	if counts != want {
		t.Fatalf("cascade counts:\n got %+v\nwant %+v", counts, want)
	}

	if err := s.DeleteHistoryByRepo(target.ID); err != nil {
		t.Fatalf("delete history: %v", err)
	}
	if err := s.DeleteRepo(target.ID); err != nil {
		t.Fatalf("delete repo: %v", err)
	}

	// Target side: every dependent table must report zero rows.
	for _, q := range []struct {
		name string
		sql  string
	}{
		{"repos", `SELECT COUNT(*) FROM repos WHERE id = ?`},
		{"features", `SELECT COUNT(*) FROM features WHERE repo_id = ?`},
		{"issues", `SELECT COUNT(*) FROM issues WHERE repo_id = ?`},
		{"comments", `SELECT COUNT(*) FROM comments WHERE issue_id IN (SELECT id FROM issues WHERE repo_id = ?)`},
		{"issue_tags", `SELECT COUNT(*) FROM issue_tags WHERE issue_id IN (SELECT id FROM issues WHERE repo_id = ?)`},
		{"issue_relations", `SELECT COUNT(*) FROM issue_relations WHERE from_issue_id IN (SELECT id FROM issues WHERE repo_id = ?) OR to_issue_id IN (SELECT id FROM issues WHERE repo_id = ?)`},
		{"issue_pull_requests", `SELECT COUNT(*) FROM issue_pull_requests WHERE issue_id IN (SELECT id FROM issues WHERE repo_id = ?)`},
		{"documents", `SELECT COUNT(*) FROM documents WHERE repo_id = ?`},
		{"document_links", `SELECT COUNT(*) FROM document_links WHERE document_id IN (SELECT id FROM documents WHERE repo_id = ?)`},
		{"tui_settings", `SELECT COUNT(*) FROM tui_settings WHERE repo_id = ?`},
		{"history", `SELECT COUNT(*) FROM history WHERE repo_id = ?`},
	} {
		var n int
		nargs := 1
		if q.name == "issue_relations" {
			nargs = 2
		}
		args := make([]any, nargs)
		for i := range args {
			args[i] = target.ID
		}
		if err := s.DB.QueryRow(q.sql, args...).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", q.name, err)
		}
		if n != 0 {
			t.Errorf("%s: %d rows survived deletion (want 0)", q.name, n)
		}
	}

	// Keep side: untouched.
	if r, err := s.GetRepoByID(keep.ID); err != nil || r == nil {
		t.Errorf("keep repo gone: err=%v repo=%v", err, r)
	}
	var keepHistory int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM history WHERE repo_id = ?`, keep.ID).Scan(&keepHistory); err != nil {
		t.Fatalf("count keep history: %v", err)
	}
	if keepHistory != 1 {
		t.Errorf("keep history rows = %d, want 1 (target's delete leaked across repos)", keepHistory)
	}

	// DeleteRepo on a missing id should report ErrNotFound.
	if err := s.DeleteRepo(target.ID); err != ErrNotFound {
		t.Errorf("DeleteRepo(missing) err = %v, want ErrNotFound", err)
	}
}
