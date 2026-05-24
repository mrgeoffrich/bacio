package api_test

import (
	"encoding/json"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// TestAgentSessionTodosEmpty: a registered session with no todos yet
// returns [] (never null), 200.
func TestAgentSessionTodosEmpty(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	sid := uuidFor("sess-empty")
	status, _ := registerSession(t, ts.URL, "MINI", sid, nil)
	if status != 201 {
		t.Fatalf("register status %d", status)
	}
	resp, body := apiReq(t, "GET", ts.URL+"/agents/sessions/"+sid+"/todos", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
	if string(body) != "[]" && string(body) != "[]\n" {
		t.Fatalf("body = %s, want []", body)
	}
}

// TestAgentSessionTodosRoundTrip: a snapshot written via the store
// surfaces verbatim over HTTP. The hook (post-tool-use) is the only
// real-life writer; the test injects via the store directly.
func TestAgentSessionTodosRoundTrip(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	sid := uuidFor("sess-todos")
	if status, _ := registerSession(t, ts.URL, "MINI", sid, nil); status != 201 {
		t.Fatalf("register status %d", status)
	}
	// Seed three Task* events: TaskCreate "a"/"b"/"c" at positions 0/1/2
	// (the order the post-tool-use hook would write them), then TaskUpdate
	// to settle the statuses we want the HTTP read to see.
	seeds := []struct {
		taskID, content string
		status          model.TodoStatus
	}{
		{"a", "Plan", model.TodoCompleted},
		{"b", "Implement", model.TodoInProgress},
		{"c", "Review", model.TodoPending},
	}
	for _, sd := range seeds {
		if err := s.UpsertSessionTodoFromTask(sid, sd.taskID, "MINI-1", sd.content, sd.status, nil); err != nil {
			t.Fatalf("seed %s: %v", sd.taskID, err)
		}
	}
	resp, body := apiReq(t, "GET", ts.URL+"/agents/sessions/"+sid+"/todos", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
	var got []model.SessionTodo
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != len(seeds) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(seeds))
	}
	for i, sd := range seeds {
		if got[i].Content != sd.content || got[i].Status != sd.status || got[i].Position != i {
			t.Fatalf("row %d = %+v, want pos=%d content=%q status=%q", i, got[i], i, sd.content, sd.status)
		}
	}
}

// TestAgentSessionTodosIssueKeyFilter (BACI-62): an optional
// `?issue_key=` query param narrows the returned rows to one job.
// Unfiltered call returns every row; filtered call returns only
// the matching issue's rows.
func TestAgentSessionTodosIssueKeyFilter(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	sid := uuidFor("sess-juggle")
	if status, _ := registerSession(t, ts.URL, "MINI", sid, nil); status != 201 {
		t.Fatalf("register status %d", status)
	}
	// Session worked MINI-1 then MINI-2.
	if err := s.UpsertSessionTodoFromTask(sid, "a", "MINI-1", "first job done", model.TodoCompleted, nil); err != nil {
		t.Fatalf("seed a: %v", err)
	}
	if err := s.UpsertSessionTodoFromTask(sid, "b", "MINI-2", "current job", model.TodoInProgress, nil); err != nil {
		t.Fatalf("seed b: %v", err)
	}
	resp, body := apiReq(t, "GET", ts.URL+"/agents/sessions/"+sid+"/todos", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("unfiltered status %d body %s", resp.StatusCode, body)
	}
	var unfiltered []model.SessionTodo
	if err := json.Unmarshal(body, &unfiltered); err != nil {
		t.Fatalf("decode unfiltered: %v", err)
	}
	if len(unfiltered) != 2 {
		t.Fatalf("unfiltered len = %d, want 2", len(unfiltered))
	}
	resp, body = apiReq(t, "GET", ts.URL+"/agents/sessions/"+sid+"/todos?issue_key=MINI-2", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("filtered status %d body %s", resp.StatusCode, body)
	}
	var filtered []model.SessionTodo
	if err := json.Unmarshal(body, &filtered); err != nil {
		t.Fatalf("decode filtered: %v", err)
	}
	if len(filtered) != 1 || filtered[0].TaskID != "b" {
		t.Fatalf("filtered = %+v, want one row task=b", filtered)
	}
}

// TestAgentSessionTodosDispatchIDFilter (BACI-132): an optional
// `?dispatch_id=` query param narrows further to one dispatch within
// the (session, issue). The unfiltered shape and the issue_key-only
// shape stay back-compat.
func TestAgentSessionTodosDispatchIDFilter(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	issue := seedIssue(t, s, repo, "title")
	sid := uuidFor("sess-disp-filter")
	if status, _ := registerSession(t, ts.URL, "MINI", sid, nil); status != 201 {
		t.Fatalf("register status %d", status)
	}
	dA, err := s.AddDispatch(store.AddDispatchIn{
		RepoID: repo.ID, TargetSessionID: sid, IssueID: &issue.ID,
		Mode: model.DispatchMode("plan"), Payload: "stub", CreatedBy: "test",
	})
	if err != nil {
		t.Fatalf("dispatch A: %v", err)
	}
	dB, err := s.AddDispatch(store.AddDispatchIn{
		RepoID: repo.ID, TargetSessionID: sid, IssueID: &issue.ID,
		Mode: model.DispatchMode("implement"), Payload: "stub", CreatedBy: "test",
	})
	if err != nil {
		t.Fatalf("dispatch B: %v", err)
	}
	if err := s.UpsertSessionTodoFromTask(sid, "a1", issue.Key, "plan/first", model.TodoCompleted, &dA.ID); err != nil {
		t.Fatalf("a1: %v", err)
	}
	if err := s.UpsertSessionTodoFromTask(sid, "b1", issue.Key, "impl/first", model.TodoPending, &dB.ID); err != nil {
		t.Fatalf("b1: %v", err)
	}
	// dispatch_id=dA → only the plan row.
	url := ts.URL + "/agents/sessions/" + sid + "/todos?issue_key=" + issue.Key
	resp, body := apiReq(t, "GET", url+"&dispatch_id="+itoa(dA.ID), nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("dA status %d body %s", resp.StatusCode, body)
	}
	var gotA []model.SessionTodo
	if err := json.Unmarshal(body, &gotA); err != nil {
		t.Fatalf("decode A: %v", err)
	}
	if len(gotA) != 1 || gotA[0].Content != "plan/first" {
		t.Fatalf("A = %+v, want one row plan/first", gotA)
	}
	// dispatch_id=dB → only the implement row.
	resp, body = apiReq(t, "GET", url+"&dispatch_id="+itoa(dB.ID), nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("dB status %d body %s", resp.StatusCode, body)
	}
	var gotB []model.SessionTodo
	if err := json.Unmarshal(body, &gotB); err != nil {
		t.Fatalf("decode B: %v", err)
	}
	if len(gotB) != 1 || gotB[0].Content != "impl/first" {
		t.Fatalf("B = %+v, want one row impl/first", gotB)
	}
}

// TestAgentSessionTodosDispatchIDRequiresIssueKey (BACI-132): a call
// with `?dispatch_id=` but no `?issue_key=` is rejected with 400.
func TestAgentSessionTodosDispatchIDRequiresIssueKey(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	sid := uuidFor("sess-disp-noiss")
	if status, _ := registerSession(t, ts.URL, "MINI", sid, nil); status != 201 {
		t.Fatalf("register status %d", status)
	}
	resp, body := apiReq(t, "GET", ts.URL+"/agents/sessions/"+sid+"/todos?dispatch_id=42", nil, nil)
	if resp.StatusCode != 400 {
		t.Fatalf("status %d body %s, want 400", resp.StatusCode, body)
	}
	if !contains(body, "dispatch_id requires issue_key") {
		t.Fatalf("body missing 'dispatch_id requires issue_key': %s", body)
	}
}

// TestAgentSessionTodosDispatchIDInvalidInt (BACI-132): a non-integer
// `?dispatch_id=` is rejected with 400 rather than coerced to 0.
func TestAgentSessionTodosDispatchIDInvalidInt(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	sid := uuidFor("sess-disp-bad")
	if status, _ := registerSession(t, ts.URL, "MINI", sid, nil); status != 201 {
		t.Fatalf("register status %d", status)
	}
	resp, body := apiReq(t, "GET", ts.URL+"/agents/sessions/"+sid+"/todos?issue_key=MINI-1&dispatch_id=banana", nil, nil)
	if resp.StatusCode != 400 {
		t.Fatalf("status %d body %s, want 400", resp.StatusCode, body)
	}
}

// itoa is a local minimal int-to-string for use in test query
// strings — avoids dragging strconv in for two callers.
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var out []byte
	for n > 0 {
		out = append([]byte{byte('0' + n%10)}, out...)
		n /= 10
	}
	if neg {
		out = append([]byte{'-'}, out...)
	}
	return string(out)
}

// TestAgentSessionTodosNotFound: an unknown session id surfaces as 404
// with the standard error envelope.
func TestAgentSessionTodosNotFound(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, body := apiReq(t, "GET", ts.URL+"/agents/sessions/ghost/todos", nil, nil)
	if resp.StatusCode != 404 {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
	// Crude envelope check — keep us honest if writeError ever changes
	// shape.
	if !contains(body, "not_found") {
		t.Fatalf("body missing not_found code: %s", body)
	}
}

func contains(b []byte, sub string) bool {
	return len(b) >= len(sub) && indexOf(b, sub) >= 0
}

func indexOf(b []byte, sub string) int {
	for i := 0; i+len(sub) <= len(b); i++ {
		if string(b[i:i+len(sub)]) == sub {
			return i
		}
	}
	return -1
}

// Compile-time use of the store package to keep go vet happy if the
// test file later drops every store-typed expression.
var _ = store.AgentSessionRetention
