package api_test

import (
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
	"github.com/mrgeoffrich/bacio/internal/store"
)

func TestCommentsListEmpty(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	resp, body := apiGet(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/comments")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if strings.TrimSpace(string(body)) != "[]" {
		t.Fatalf("expected [], got %s", body)
	}
}

func TestCommentAddHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	resp, body := apiPost(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/comments",
		`{"author":"alice","body":"hello"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	for _, want := range []string{`"author": "alice"`, `"body": "hello"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("missing %s in: %s", want, body)
		}
	}
	assertHistoryOps(t, s, []string{"comment.add"})
}

func TestCommentAddDryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	resp, body := apiPost(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/comments?dry_run=true",
		`{"author":"alice","body":"hello"}`)
	if resp.StatusCode != 201 || resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("status: %d, header=%q", resp.StatusCode, resp.Header.Get("X-Dry-Run"))
	}
	if !strings.Contains(string(body), `"body": "hello"`) {
		t.Fatalf("body: %s", body)
	}
	assertHistoryOps(t, s, nil)
}

func TestCommentAddMissingAuthor(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	resp, _ := apiPost(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/comments",
		`{"body":"hello"}`)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestCommentAddMissingBody(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	resp, _ := apiPost(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/comments",
		`{"author":"alice"}`)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestCommentAddURLKeyWins(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	a := seedIssue(t, s, repo, "a")
	b := seedIssue(t, s, repo, "b")
	resp, _ := apiPost(t, ts.URL+"/repos/MINI/issues/"+a.Key+"/comments",
		`{"issue_key":"`+b.Key+`","author":"x","body":"hi"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	csA, _ := s.ListComments(a.ID)
	csB, _ := s.ListComments(b.ID)
	if len(csA) != 1 || len(csB) != 0 {
		t.Fatalf("URL did not win: a=%d b=%d", len(csA), len(csB))
	}
}

func TestCommentAddCrossRepo(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	repo2 := seedRepo2(t, s)
	iss := seedIssue(t, s, repo2, "x")
	_ = repo
	resp, _ := apiPost(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/comments",
		`{"author":"a","body":"b"}`)
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestCommentAddBadAuthor(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	resp, _ := apiPost(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/comments",
		`{"author":"bad\tname","body":"b"}`)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestCommentDeleteHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	cm, err := s.CreateComment(store.CreateCommentIn{IssueID: iss.ID, Author: "alice", Body: "hello"})
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	resp, body := apiDelete(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/comments/"+cm.UUID, nil)
	if resp.StatusCode != 204 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	cs, _ := s.ListComments(iss.ID)
	if len(cs) != 0 {
		t.Fatalf("expected comment gone, got %d", len(cs))
	}
	// CreateComment via the store writes no history — only the handler
	// records comment.add — so only comment.delete is expected here.
	assertHistoryOps(t, s, []string{"comment.delete"})
}

func TestCommentDeleteDryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	cm, err := s.CreateComment(store.CreateCommentIn{IssueID: iss.ID, Author: "alice", Body: "hello"})
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	resp, body := apiDelete(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/comments/"+cm.UUID+"?dry_run=1", nil)
	if resp.StatusCode != 200 || resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("status: %d, header=%q, body=%s", resp.StatusCode, resp.Header.Get("X-Dry-Run"), body)
	}
	if !strings.Contains(string(body), `"would_remove": 1`) {
		t.Fatalf("body: %s", body)
	}
	cs, _ := s.ListComments(iss.ID)
	if len(cs) != 1 {
		t.Fatalf("dry-run deleted the row: %d", len(cs))
	}
	// Dry-run writes no audit row, and store CreateComment writes none.
	assertHistoryOps(t, s, nil)
}

func TestCommentDeleteUnknownUUID(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	resp, _ := apiDelete(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/comments/019e0000-0000-7000-8000-000000000000", nil)
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestCommentDeleteWrongIssue(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	a := seedIssue(t, s, repo, "a")
	b := seedIssue(t, s, repo, "b")
	cm, err := s.CreateComment(store.CreateCommentIn{IssueID: a.ID, Author: "alice", Body: "hello"})
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	// Address comment-on-a via b's key — must 404, comment survives.
	resp, _ := apiDelete(t, ts.URL+"/repos/MINI/issues/"+b.Key+"/comments/"+cm.UUID, nil)
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	cs, _ := s.ListComments(a.ID)
	if len(cs) != 1 {
		t.Fatalf("comment on wrong issue was deleted: %d", len(cs))
	}
}

// TestCommentAddEvalResolvesContext (BACI-131) locks in that
// {"eval": true} writes the row with eval=1 and the resolved
// (agent_session_id, mode) snapshotted from the open claim + matching
// dispatch. The dispatch_id FK is set to the matching dispatch's id.
func TestCommentAddEvalResolvesContext(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	if _, err := s.UpsertAgentSession(store.UpsertAgentSessionIn{
		SessionID: "watcher", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("UpsertAgentSession: %v", err)
	}
	if _, _, _, _, err := s.AddAgentClaim("watcher", iss.ID, "watching"); err != nil {
		t.Fatalf("AddAgentClaim: %v", err)
	}
	if _, err := s.AddDispatch(store.AddDispatchIn{
		RepoID: repo.ID, TargetSessionID: "watcher", IssueID: &iss.ID,
		Mode: "implement", CreatedBy: "supervisor",
	}); err != nil {
		t.Fatalf("AddDispatch: %v", err)
	}
	resp, body := apiPost(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/comments",
		`{"author":"alice","body":"missed criterion 3","eval":true}`)
	if resp.StatusCode != 201 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	for _, want := range []string{
		`"eval": true`,
		`"agent_session_id": "watcher"`,
		`"mode": "implement"`,
	} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("missing %s in: %s", want, body)
		}
	}
	cs, _ := s.ListComments(iss.ID)
	if len(cs) != 1 || !cs[0].Eval || cs[0].AgentSessionID != "watcher" || cs[0].Mode != "implement" {
		t.Fatalf("eval row not persisted correctly: %+v", cs)
	}
	if cs[0].DispatchID == nil {
		t.Fatalf("DispatchID nil — expected the resolved dispatch row's id")
	}
}

// TestCommentAddEvalNoClaim (BACI-131) keeps the store defensive when
// an eval write lands on an untaken issue — the UI blocks this case
// but the API still accepts the row, leaves the context fields empty.
func TestCommentAddEvalNoClaim(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	resp, body := apiPost(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/comments",
		`{"author":"alice","body":"no claim","eval":true}`)
	if resp.StatusCode != 201 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"eval": true`) {
		t.Fatalf("eval missing: %s", body)
	}
	cs, _ := s.ListComments(iss.ID)
	if len(cs) != 1 || !cs[0].Eval || cs[0].AgentSessionID != "" || cs[0].Mode != "" {
		t.Fatalf("expected eval row with empty context, got %+v", cs)
	}
}
