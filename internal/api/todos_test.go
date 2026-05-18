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
	status, _ := registerSession(t, ts.URL, "MINI", "sess-empty", nil)
	if status != 201 {
		t.Fatalf("register status %d", status)
	}
	resp, body := apiReq(t, "GET", ts.URL+"/agents/sessions/sess-empty/todos", nil, nil)
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
	if status, _ := registerSession(t, ts.URL, "MINI", "sess-todos", nil); status != 201 {
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
		if err := s.UpsertSessionTodoFromTask("sess-todos", sd.taskID, "MINI-1", sd.content, sd.status); err != nil {
			t.Fatalf("seed %s: %v", sd.taskID, err)
		}
	}
	resp, body := apiReq(t, "GET", ts.URL+"/agents/sessions/sess-todos/todos", nil, nil)
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
	if status, _ := registerSession(t, ts.URL, "MINI", "sess-juggle", nil); status != 201 {
		t.Fatalf("register status %d", status)
	}
	// Session worked MINI-1 then MINI-2.
	if err := s.UpsertSessionTodoFromTask("sess-juggle", "a", "MINI-1", "first job done", model.TodoCompleted); err != nil {
		t.Fatalf("seed a: %v", err)
	}
	if err := s.UpsertSessionTodoFromTask("sess-juggle", "b", "MINI-2", "current job", model.TodoInProgress); err != nil {
		t.Fatalf("seed b: %v", err)
	}
	resp, body := apiReq(t, "GET", ts.URL+"/agents/sessions/sess-juggle/todos", nil, nil)
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
	resp, body = apiReq(t, "GET", ts.URL+"/agents/sessions/sess-juggle/todos?issue_key=MINI-2", nil, nil)
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
