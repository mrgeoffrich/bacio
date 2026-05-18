package api_test

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

func seedSessionWithQuestion(t *testing.T, s *store.Store, sessionID string) (*model.AgentSession, *model.SessionQuestion) {
	t.Helper()
	repo := seedRepo(t, s)
	sess, err := s.UpsertAgentSession(store.UpsertAgentSessionIn{
		SessionID: sessionID, RepoID: repo.ID, Actor: "agent-claude",
	})
	if err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	q, err := s.AddSessionQuestion(store.AddSessionQuestionIn{
		SessionID: sess.SessionID,
		IssueKey:  "",
		AskedBy:   "agent-claude",
		Payload: model.QuestionPayload{
			Questions: []model.QuestionItem{{
				Question:    "Pick a side",
				Header:      "Side",
				MultiSelect: model.MultiSelectFlag(false),
				Options: []model.QuestionOption{
					{Label: "Left", Description: "go port-side"},
					{Label: "Right", Description: "go starboard"},
				},
			}},
		},
	})
	if err != nil {
		t.Fatalf("add question: %v", err)
	}
	return sess, q
}

// TestQuestionsListEmpty returns []SessionQuestion (not null) for an
// otherwise-clean session — the same shape contract as /todos.
func TestQuestionsListEmpty(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	sess, err := s.UpsertAgentSession(store.UpsertAgentSessionIn{
		SessionID: "sess-empty", RepoID: repo.ID, Actor: "agent-alice",
	})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	resp, body := apiGet(t, ts.URL+"/agents/sessions/"+sess.SessionID+"/questions")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, body)
	}
	if string(body) != "[]\n" && string(body) != "[]" {
		t.Fatalf("empty list body = %q, want []", body)
	}
}

// TestQuestionsListHappy round-trips an open question through GET
// /agents/sessions/{sid}/questions — default state filter is open.
func TestQuestionsListHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	sess, q := seedSessionWithQuestion(t, s, "sess-1")
	resp, body := apiGet(t, ts.URL+"/agents/sessions/"+sess.SessionID+"/questions")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, body)
	}
	var rows []*model.SessionQuestion
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode: %v body=%s", err, body)
	}
	if len(rows) != 1 || rows[0].ID != q.ID {
		t.Fatalf("rows = %v, want one row with id=%d", rows, q.ID)
	}
}

// TestQuestionAnswerHappy submits an answer and verifies the row
// flips state, records the actor, and writes an audit row.
func TestQuestionAnswerHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	_, q := seedSessionWithQuestion(t, s, "sess-a")
	url := ts.URL + "/agents/questions/" + strconv.FormatInt(q.ID, 10) + "/answer"
	resp, body := apiReq(t, "POST", url,
		map[string]any{"answers": map[string]any{"Pick a side": "Left"}},
		map[string]string{"X-Actor": "geoff"})
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, body)
	}
	var got model.SessionQuestion
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v body=%s", err, body)
	}
	if got.State != model.QuestionAnswered {
		t.Fatalf("state = %q, want answered", got.State)
	}
	if got.AnsweredBy != "geoff" {
		t.Fatalf("answered_by = %q, want geoff", got.AnsweredBy)
	}
	assertHistoryOps(t, s, []string{"question.answer"})
}

// TestQuestionAnswerDryRun projects the post-write row without
// flipping state in the DB.
func TestQuestionAnswerDryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	_, q := seedSessionWithQuestion(t, s, "sess-dr")
	url := ts.URL + "/agents/questions/" + strconv.FormatInt(q.ID, 10) + "/answer?dry_run=true"
	resp, body := apiReq(t, "POST", url,
		map[string]any{"answers": map[string]any{"Pick a side": "Right"}},
		map[string]string{"X-Actor": "geoff"})
	if resp.StatusCode != 200 || resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("status=%d dryRun=%q body=%s", resp.StatusCode, resp.Header.Get("X-Dry-Run"), body)
	}
	row, err := s.GetSessionQuestion(q.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if row.State != model.QuestionOpen {
		t.Fatalf("state = %q after dry-run, want open", row.State)
	}
	assertHistoryOps(t, s, nil)
}

// TestQuestionAnswerConflict refuses to answer an already-answered
// row with 409.
func TestQuestionAnswerConflict(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	_, q := seedSessionWithQuestion(t, s, "sess-c")
	if _, err := s.AnswerSessionQuestion(q.ID,
		model.QuestionAnswers{"Pick a side": "Left"}, "geoff"); err != nil {
		t.Fatalf("seed answer: %v", err)
	}
	url := ts.URL + "/agents/questions/" + strconv.FormatInt(q.ID, 10) + "/answer"
	resp, body := apiReq(t, "POST", url,
		map[string]any{"answers": map[string]any{"Pick a side": "Right"}},
		map[string]string{"X-Actor": "geoff"})
	if resp.StatusCode != 409 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, body)
	}
}

// TestQuestionAnswerValidationError rejects an unknown question key
// with 400.
func TestQuestionAnswerValidationError(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	_, q := seedSessionWithQuestion(t, s, "sess-v")
	url := ts.URL + "/agents/questions/" + strconv.FormatInt(q.ID, 10) + "/answer"
	resp, body := apiReq(t, "POST", url,
		map[string]any{"answers": map[string]any{"No such question": "Left"}},
		map[string]string{"X-Actor": "geoff"})
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, body)
	}
}

// TestQuestionCancelHappy flips the row to cancelled and records
// the actor + audit row.
func TestQuestionCancelHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	_, q := seedSessionWithQuestion(t, s, "sess-x")
	url := ts.URL + "/agents/questions/" + strconv.FormatInt(q.ID, 10) + "/cancel"
	resp, body := apiReq(t, "POST", url, nil,
		map[string]string{"X-Actor": "geoff"})
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, body)
	}
	var got model.SessionQuestion
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v body=%s", err, body)
	}
	if got.State != model.QuestionCancelled {
		t.Fatalf("state = %q, want cancelled", got.State)
	}
	assertHistoryOps(t, s, []string{"question.cancel"})
}

// TestQuestionShowNotFound returns 404 for an unknown id.
func TestQuestionShowNotFound(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, _ := apiGet(t, ts.URL+"/agents/questions/99999")
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}
