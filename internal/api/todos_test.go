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
	want := []model.SessionTodo{
		{Position: 0, Content: "Plan", Status: model.TodoCompleted},
		{Position: 1, Content: "Implement", Status: model.TodoInProgress},
		{Position: 2, Content: "Review", Status: model.TodoPending},
	}
	if err := s.ReplaceSessionTodos("sess-todos", want); err != nil {
		t.Fatalf("seed todos: %v", err)
	}
	resp, body := apiReq(t, "GET", ts.URL+"/agents/sessions/sess-todos/todos", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
	var got []model.SessionTodo
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Content != w.Content || got[i].Status != w.Status || got[i].Position != w.Position {
			t.Fatalf("row %d = %+v, want %+v", i, got[i], w)
		}
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
