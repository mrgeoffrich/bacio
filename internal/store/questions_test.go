package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// seedQuestionSession is a tiny helper: registers a session against
// the seed repo, ready for the agent_session_questions tests.
func seedQuestionSession(t *testing.T, sessionID string) (*Store, *model.Repo) {
	t.Helper()
	s, repo, _ := seedRepoAndIssue(t)
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: sessionID, RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register session: %v", err)
	}
	return s, repo
}

func validSinglePayload() model.QuestionPayload {
	return model.QuestionPayload{
		Questions: []model.QuestionItem{{
			Question:    "Which approach should I take?",
			Header:      "Approach",
			MultiSelect: model.MultiSelectFlag(false),
			Options: []model.QuestionOption{
				{Label: "Option A", Description: "Quick and dirty"},
				{Label: "Option B", Description: "Slow and clean"},
			},
		}},
	}
}

func validMultiPayload() model.QuestionPayload {
	return model.QuestionPayload{
		Questions: []model.QuestionItem{{
			Question:    "Pick all the tags that apply",
			Header:      "Tags",
			MultiSelect: model.MultiSelectFlag(true),
			Options: []model.QuestionOption{
				{Label: "ui", Description: "UI work"},
				{Label: "tui", Description: "TUI surface"},
				{Label: "bug", Description: "defect / regression"},
			},
		}},
	}
}

// TestAddSessionQuestionHappyPath covers the round trip: ask → list →
// answer → list. State flips, answer is recorded, question becomes
// invisible to ListOpenQuestionsBySessions.
func TestAddSessionQuestionHappyPath(t *testing.T) {
	s, _ := seedQuestionSession(t, "asq-1")
	q, err := s.AddSessionQuestion(AddSessionQuestionIn{
		SessionID: "asq-1",
		IssueKey:  "AGNT-1",
		Payload:   validSinglePayload(),
		AskedBy:   "cheerful-otter@claude.shiny",
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if q.ID == 0 || q.RequestUUID == "" {
		t.Fatalf("expected populated id + uuid, got %+v", q)
	}
	if q.State != model.QuestionOpen {
		t.Fatalf("state = %q, want open", q.State)
	}
	if q.IssueKey != "AGNT-1" {
		t.Fatalf("issue_key = %q, want AGNT-1", q.IssueKey)
	}

	// ListOpenQuestionsBySessions returns the row, keyed by session_pk
	bulk, err := s.ListOpenQuestionsBySessions([]string{"asq-1"})
	if err != nil {
		t.Fatalf("bulk: %v", err)
	}
	if len(bulk[q.SessionPK]) != 1 || bulk[q.SessionPK][0].ID != q.ID {
		t.Fatalf("bulk[%d] = %+v, want [%d]", q.SessionPK, bulk[q.SessionPK], q.ID)
	}

	// Lookup by UUID — the channel uses this to re-correlate
	got, err := s.GetSessionQuestionByUUID(q.RequestUUID)
	if err != nil {
		t.Fatalf("by uuid: %v", err)
	}
	if got.ID != q.ID {
		t.Fatalf("by uuid = %d, want %d", got.ID, q.ID)
	}

	// Answer the question
	answers := model.QuestionAnswers{"Which approach should I take?": "Option A"}
	answered, err := s.AnswerSessionQuestion(q.ID, answers, "geoff")
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if answered.State != model.QuestionAnswered {
		t.Fatalf("state = %q, want answered", answered.State)
	}
	if answered.AnsweredBy != "geoff" {
		t.Fatalf("answered_by = %q, want geoff", answered.AnsweredBy)
	}
	if v, ok := answered.Answers["Which approach should I take?"]; !ok || v != "Option A" {
		t.Fatalf("answers = %v, want {Which approach should I take?: Option A}", answered.Answers)
	}

	// Bulk read no longer returns it (state=answered)
	bulk, err = s.ListOpenQuestionsBySessions([]string{"asq-1"})
	if err != nil {
		t.Fatalf("bulk after answer: %v", err)
	}
	if len(bulk) != 0 {
		t.Fatalf("expected no open rows after answer; got %v", bulk)
	}

	// DrainSettled returns the answered row for the channel to deliver
	settled, err := s.DrainSettledQuestionsForSession("asq-1")
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if len(settled) != 1 || settled[0].ID != q.ID {
		t.Fatalf("drain = %v, want one entry id=%d", settled, q.ID)
	}
}

// TestAddSessionQuestionRejectsInvalidPayload covers the
// ValidateQuestionPayload entry point — both too-few questions and
// too-many options.
func TestAddSessionQuestionRejectsInvalidPayload(t *testing.T) {
	s, _ := seedQuestionSession(t, "asq-bad")

	// Too few questions
	_, err := s.AddSessionQuestion(AddSessionQuestionIn{
		SessionID: "asq-bad",
		Payload:   model.QuestionPayload{},
		AskedBy:   "agent-x",
	})
	if err == nil || !strings.Contains(err.Error(), "between") {
		t.Fatalf("empty payload err = %v, want between/got 0 message", err)
	}

	// Too many options
	tooMany := validSinglePayload()
	tooMany.Questions[0].Options = []model.QuestionOption{
		{Label: "a"}, {Label: "b"}, {Label: "c"}, {Label: "d"}, {Label: "e"},
	}
	_, err = s.AddSessionQuestion(AddSessionQuestionIn{
		SessionID: "asq-bad", Payload: tooMany, AskedBy: "agent-x",
	})
	if err == nil || !strings.Contains(err.Error(), "option") {
		t.Fatalf("too many options err = %v, want option message", err)
	}

	// Header too long (>12 chars)
	longHeader := validSinglePayload()
	longHeader.Questions[0].Header = "thirteenchars"
	_, err = s.AddSessionQuestion(AddSessionQuestionIn{
		SessionID: "asq-bad", Payload: longHeader, AskedBy: "agent-x",
	})
	if err == nil || !strings.Contains(err.Error(), "header") {
		t.Fatalf("long header err = %v, want header message", err)
	}
}

// TestAddSessionQuestionUnknownSession returns ErrNotFound so the
// channel can log-and-drop on an unknown id rather than crashing.
func TestAddSessionQuestionUnknownSession(t *testing.T) {
	s := newTestStore(t)
	_, err := s.AddSessionQuestion(AddSessionQuestionIn{
		SessionID: "never-registered",
		Payload:   validSinglePayload(),
		AskedBy:   "agent-x",
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestAnswerSessionQuestionRejectsAlreadyAnswered locks the state
// guard — you can't answer the same question twice.
func TestAnswerSessionQuestionRejectsAlreadyAnswered(t *testing.T) {
	s, _ := seedQuestionSession(t, "asq-double")
	q, err := s.AddSessionQuestion(AddSessionQuestionIn{
		SessionID: "asq-double", Payload: validSinglePayload(), AskedBy: "a",
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	answers := model.QuestionAnswers{"Which approach should I take?": "Option A"}
	if _, err := s.AnswerSessionQuestion(q.ID, answers, "geoff"); err != nil {
		t.Fatalf("first answer: %v", err)
	}
	_, err = s.AnswerSessionQuestion(q.ID, answers, "geoff")
	if err == nil || !strings.Contains(err.Error(), "only open") {
		t.Fatalf("second answer err = %v, want only-open guard", err)
	}
}

// TestAnswerSessionQuestionRejectsMismatchedShape locks the shape
// check — multi-select gives []string back, single-select returns
// a single string, and unknown keys fail.
func TestAnswerSessionQuestionRejectsMismatchedShape(t *testing.T) {
	s, _ := seedQuestionSession(t, "asq-shape")
	q, err := s.AddSessionQuestion(AddSessionQuestionIn{
		SessionID: "asq-shape", Payload: validMultiPayload(), AskedBy: "a",
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	// String to a multi-select question — rejected
	_, err = s.AnswerSessionQuestion(q.ID,
		model.QuestionAnswers{"Pick all the tags that apply": "ui"}, "geoff")
	if err == nil || !strings.Contains(err.Error(), "multi-select") {
		t.Fatalf("string-for-multi err = %v, want multi-select message", err)
	}

	// Unknown question key
	_, err = s.AnswerSessionQuestion(q.ID,
		model.QuestionAnswers{"Some other question": []string{"ui"}}, "geoff")
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("unknown key err = %v, want does-not-match message", err)
	}

	// Empty multi-select selection
	_, err = s.AnswerSessionQuestion(q.ID,
		model.QuestionAnswers{"Pick all the tags that apply": []string{}}, "geoff")
	if err == nil || !strings.Contains(err.Error(), "select at least one") {
		t.Fatalf("empty multi err = %v, want at-least-one message", err)
	}

	// Happy path — multi-select with two real labels
	if _, err := s.AnswerSessionQuestion(q.ID,
		model.QuestionAnswers{"Pick all the tags that apply": []string{"ui", "tui"}}, "geoff"); err != nil {
		t.Fatalf("happy multi: %v", err)
	}
}

// TestCancelSessionQuestion flips an open row to cancelled and is
// surfaced via DrainSettled.
func TestCancelSessionQuestion(t *testing.T) {
	s, _ := seedQuestionSession(t, "asq-cancel")
	q, err := s.AddSessionQuestion(AddSessionQuestionIn{
		SessionID: "asq-cancel", Payload: validSinglePayload(), AskedBy: "a",
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	got, err := s.CancelSessionQuestion(q.ID, "geoff")
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if got.State != model.QuestionCancelled {
		t.Fatalf("state = %q, want cancelled", got.State)
	}
	// Cancelling again is rejected
	if _, err := s.CancelSessionQuestion(q.ID, "geoff"); err == nil {
		t.Fatalf("expected second cancel to be rejected")
	}
	settled, err := s.DrainSettledQuestionsForSession("asq-cancel")
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if len(settled) != 1 || settled[0].State != model.QuestionCancelled {
		t.Fatalf("drain = %v, want cancelled row", settled)
	}
}

// TestAbandonOpenQuestionsForSession flips every still-open row for
// one session to abandoned without touching settled rows.
func TestAbandonOpenQuestionsForSession(t *testing.T) {
	s, _ := seedQuestionSession(t, "asq-restart")
	open1, err := s.AddSessionQuestion(AddSessionQuestionIn{
		SessionID: "asq-restart", Payload: validSinglePayload(), AskedBy: "a",
	})
	if err != nil {
		t.Fatalf("add 1: %v", err)
	}
	open2, err := s.AddSessionQuestion(AddSessionQuestionIn{
		SessionID: "asq-restart", Payload: validMultiPayload(), AskedBy: "a",
	})
	if err != nil {
		t.Fatalf("add 2: %v", err)
	}
	// Answer one to make sure abandon doesn't touch settled rows
	if _, err := s.AnswerSessionQuestion(open1.ID,
		model.QuestionAnswers{"Which approach should I take?": "Option A"}, "geoff"); err != nil {
		t.Fatalf("answer 1: %v", err)
	}
	n, err := s.AbandonOpenQuestionsForSession("asq-restart")
	if err != nil {
		t.Fatalf("abandon: %v", err)
	}
	if n != 1 {
		t.Fatalf("abandon returned %d, want 1 (only open2 was open)", n)
	}
	// open1 still answered; open2 now abandoned
	after1, err := s.GetSessionQuestion(open1.ID)
	if err != nil {
		t.Fatalf("get 1: %v", err)
	}
	if after1.State != model.QuestionAnswered {
		t.Fatalf("open1 state = %q, want answered (abandon must not touch settled rows)", after1.State)
	}
	after2, err := s.GetSessionQuestion(open2.ID)
	if err != nil {
		t.Fatalf("get 2: %v", err)
	}
	if after2.State != model.QuestionAbandoned {
		t.Fatalf("open2 state = %q, want abandoned", after2.State)
	}
}

// TestSessionQuestionCascadeOnSessionDelete locks the FK cascade:
// removing the parent session row removes every question, mirroring
// the agent_session_todos contract.
func TestSessionQuestionCascadeOnSessionDelete(t *testing.T) {
	s, _ := seedQuestionSession(t, "asq-cascade")
	q, err := s.AddSessionQuestion(AddSessionQuestionIn{
		SessionID: "asq-cascade", Payload: validSinglePayload(), AskedBy: "a",
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := s.DB.Exec(`DELETE FROM agent_sessions WHERE session_id = ?`, "asq-cascade"); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if _, err := s.GetSessionQuestion(q.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after cascade = %v, want ErrNotFound", err)
	}
}
