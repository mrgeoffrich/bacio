package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/mrgeoffrich/bacio/internal/inputio"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// BACI-53 ask_user_question REST surface. Three verbs cover the
// desktop and web modal:
//   GET    /agents/sessions/{session_id}/questions[?state=...]  — list
//   GET    /agents/sessions/{session_id}/questions/open         — bulk
//                                                                read of
//                                                                opens
//   GET    /agents/questions/{id}                               — show
//   POST   /agents/questions/{id}/answer                        — submit
//   POST   /agents/questions/{id}/cancel                        — dismiss
//
// All auth-gated like the rest of the API. X-Actor lands in the
// `answered_by` field for answer/cancel (falls back to the literal
// "api" — matches the existing convention). ?dry_run=1 / X-Dry-Run
// project the post-write row without touching the store.

// handleSessionQuestionsList returns the questions for one session,
// optionally filtered by state via ?state=open (default) or
// ?state=all for every state. Unknown session id returns 404.
func (d deps) handleSessionQuestionsList(w http.ResponseWriter, r *http.Request) {
	sid := r.PathValue("session_id")
	sess, err := d.store.ResolveAgentSession(sid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found",
				fmt.Sprintf("session %q not found", sid), nil)
			return
		}
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	states, err := parseStateFilter(r.URL.Query().Get("state"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(),
			map[string]any{"field": "state"})
		return
	}
	rows, err := d.store.ListSessionQuestions(sess.SessionID, states)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if rows == nil {
		rows = []*model.SessionQuestion{}
	}
	writeJSON(w, http.StatusOK, rows)
}

// handleSessionQuestionsOpen is the dedicated bulk-read endpoint —
// returns the open subset, mirroring the existing /todos endpoint.
// The desktop's AgentCard composite hydrates against this so the
// polling refresh gets every open question in one trip.
func (d deps) handleSessionQuestionsOpen(w http.ResponseWriter, r *http.Request) {
	sid := r.PathValue("session_id")
	sess, err := d.store.ResolveAgentSession(sid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found",
				fmt.Sprintf("session %q not found", sid), nil)
			return
		}
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	rows, err := d.store.ListSessionQuestions(sess.SessionID,
		[]model.QuestionState{model.QuestionOpen})
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if rows == nil {
		rows = []*model.SessionQuestion{}
	}
	writeJSON(w, http.StatusOK, rows)
}

// handleQuestionShow returns one question by id.
func (d deps) handleQuestionShow(w http.ResponseWriter, r *http.Request) {
	id, ok := readQuestionID(r, w)
	if !ok {
		return
	}
	q, err := d.store.GetSessionQuestion(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found",
				fmt.Sprintf("no question with id %d", id), nil)
			return
		}
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, q)
}

// questionAnswerInput is the request body for the answer endpoint.
// We don't reuse inputs.AgentQuestionsAnswerInput because the id
// always comes from the URL — having it duplicated in the body
// would invite a "URL says 7, body says 12" trap.
type questionAnswerInput struct {
	Answers map[string]any `json:"answers"`
}

// handleQuestionAnswer submits the user's answer for an open
// question. Authority-gated: X-Actor lands in answered_by.
func (d deps) handleQuestionAnswer(w http.ResponseWriter, r *http.Request) {
	id, ok := readQuestionID(r, w)
	if !ok {
		return
	}
	body, ok := readBody(r, w)
	if !ok {
		return
	}
	in := questionAnswerInput{}
	if len(body) > 0 {
		got, _, err := inputio.DecodeStrict[questionAnswerInput](body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
			return
		}
		in = *got
	}
	current, err := d.store.GetSessionQuestion(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found",
				fmt.Sprintf("no question with id %d", id), nil)
			return
		}
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if current.State != model.QuestionOpen {
		writeError(w, http.StatusConflict, "conflict",
			fmt.Sprintf("question %d is %s; only open questions can be answered", id, current.State), nil)
		return
	}
	if err := model.ValidateQuestionAnswers(current.Payload, in.Answers); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	actor := ActorFromContext(r.Context())
	if isDryRun(r) {
		projected := *current
		projected.State = model.QuestionAnswered
		projected.Answers = in.Answers
		now := time.Now().UTC()
		projected.AnsweredAt = &now
		projected.AnsweredBy = actor
		writeDryRun(w, http.StatusOK, &projected)
		return
	}
	updated, err := d.store.AnswerSessionQuestion(id, in.Answers, actor)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		Actor: actor,
		Op:    "question.answer", Kind: "question",
		TargetID: &updated.ID, TargetLabel: updated.RequestUUID,
		Details: questionAuditDetails(updated),
	})
	writeJSON(w, http.StatusOK, updated)
}

// handleQuestionCancel dismisses an open question.
func (d deps) handleQuestionCancel(w http.ResponseWriter, r *http.Request) {
	id, ok := readQuestionID(r, w)
	if !ok {
		return
	}
	// Cancel takes no body — but if the client sends one we still
	// read it to drain the connection cleanly.
	if _, ok := readBody(r, w); !ok {
		return
	}
	current, err := d.store.GetSessionQuestion(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found",
				fmt.Sprintf("no question with id %d", id), nil)
			return
		}
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if current.State != model.QuestionOpen {
		writeError(w, http.StatusConflict, "conflict",
			fmt.Sprintf("question %d is %s; only open questions can be cancelled", id, current.State), nil)
		return
	}
	actor := ActorFromContext(r.Context())
	if isDryRun(r) {
		projected := *current
		projected.State = model.QuestionCancelled
		now := time.Now().UTC()
		projected.AnsweredAt = &now
		projected.AnsweredBy = actor
		writeDryRun(w, http.StatusOK, &projected)
		return
	}
	updated, err := d.store.CancelSessionQuestion(id, actor)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		Actor: actor,
		Op:    "question.cancel", Kind: "question",
		TargetID: &updated.ID, TargetLabel: updated.RequestUUID,
		Details: questionAuditDetails(updated),
	})
	writeJSON(w, http.StatusOK, updated)
}

func readQuestionID(r *http.Request, w http.ResponseWriter) (int64, bool) {
	raw := r.PathValue("id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_input",
			fmt.Sprintf("invalid question id %q", raw),
			map[string]any{"field": "id"})
		return 0, false
	}
	return id, true
}

// parseStateFilter parses the ?state= query parameter. Empty
// defaults to "open". The literal "all" means "every state". A
// single state value filters to just that state. Comma-separated
// values are accepted for multi-state filters.
func parseStateFilter(q string) ([]model.QuestionState, error) {
	if q == "" || q == "open" {
		return []model.QuestionState{model.QuestionOpen}, nil
	}
	if q == "all" {
		return nil, nil
	}
	// Single value path — split on comma for multi.
	var out []model.QuestionState
	for _, s := range splitCSV(q) {
		st, err := model.ParseQuestionState(s)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	if len(out) == 0 {
		return []model.QuestionState{model.QuestionOpen}, nil
	}
	return out, nil
}

func splitCSV(s string) []string {
	var out []string
	start := 0
	for i, r := range s {
		if r == ',' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// questionAuditDetails composes the same audit-log Details string
// the local client uses, mirrored here so REST-driven mutations
// and CLI-driven mutations write equivalent rows.
func questionAuditDetails(q *model.SessionQuestion) string {
	parts := []string{"session=" + q.SessionID}
	if q.IssueKey != "" {
		parts = append(parts, "issue="+q.IssueKey)
	}
	parts = append(parts, "state="+string(q.State))
	if n := len(q.Payload.Questions); n > 0 {
		parts = append(parts, fmt.Sprintf("questions=%d", n))
	}
	return joinSpace(parts)
}

func joinSpace(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += " " + p
	}
	return out
}
