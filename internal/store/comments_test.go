package store

import (
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestCreateCommentRoundTripsNormal locks in that a vanilla (non-eval)
// comment round-trips through CreateComment / ListComments with the
// four BACI-131 context fields left at zero values.
func TestCreateCommentRoundTripsNormal(t *testing.T) {
	s, _, iss := seedRepoAndIssue(t)
	cm, err := s.CreateComment(CreateCommentIn{
		IssueID: iss.ID,
		Author:  "alice",
		Body:    "hello",
	})
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	if cm.Eval {
		t.Fatalf("Eval = true, want false on a normal comment")
	}
	if cm.AgentSessionID != "" || cm.Mode != "" || cm.DispatchID != nil {
		t.Fatalf("context fields populated on a normal comment: %+v", cm)
	}
	listed, err := s.ListComments(iss.ID)
	if err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("listed %d comments, want 1", len(listed))
	}
	if listed[0].AgentName != "" {
		t.Fatalf("AgentName = %q, want empty on a normal comment", listed[0].AgentName)
	}
}

// TestCreateCommentRoundTripsEval locks in that the eval flag and the
// (agent_session_id, dispatch_id, mode) triple survive the round trip
// when set, and that ListComments' JOIN resolves the agent identity
// slug.
func TestCreateCommentRoundTripsEval(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)
	ag, _, err := s.UpsertAgent("vivid-finch@claude.shiny", true)
	if err != nil {
		t.Fatalf("UpsertAgent: %v", err)
	}
	sess, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "be1a8a0f-975c-4be3-9084-1d226f20ec27",
		RepoID:    repo.ID,
		AgentID:   &ag.ID,
		Actor:     "agent-vivid-finch",
	})
	if err != nil {
		t.Fatalf("UpsertAgentSession: %v", err)
	}
	dispatch, err := s.AddDispatch(AddDispatchIn{
		RepoID: repo.ID, TargetSessionID: sess.SessionID, IssueID: &iss.ID,
		Mode: model.DispatchModeImplement, CreatedBy: "supervisor",
	})
	if err != nil {
		t.Fatalf("AddDispatch: %v", err)
	}
	cm, err := s.CreateComment(CreateCommentIn{
		IssueID:        iss.ID,
		Author:         "geoff",
		Body:           "missed acceptance criterion 3",
		Eval:           true,
		AgentSessionID: sess.SessionID,
		DispatchID:     &dispatch.ID,
		Mode:           "implement",
	})
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	if !cm.Eval || cm.AgentSessionID != sess.SessionID || cm.Mode != "implement" {
		t.Fatalf("eval round-trip lost fields: %+v", cm)
	}
	if cm.DispatchID == nil || *cm.DispatchID != dispatch.ID {
		t.Fatalf("DispatchID = %v, want %d", cm.DispatchID, dispatch.ID)
	}
	listed, err := s.ListComments(iss.ID)
	if err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("listed %d, want 1", len(listed))
	}
	if listed[0].AgentName != "vivid-finch@claude.shiny" {
		t.Fatalf("AgentName = %q, want vivid-finch@claude.shiny", listed[0].AgentName)
	}
}

// TestCreateCommentRejectsControlCharInSession proves the eval-triple
// validator rejects a malformed agent_session_id at the store boundary
// (defence-in-depth — the HTTP path resolves the field server-side).
func TestCreateCommentRejectsControlCharInSession(t *testing.T) {
	s, _, iss := seedRepoAndIssue(t)
	_, err := s.CreateComment(CreateCommentIn{
		IssueID:        iss.ID,
		Author:         "alice",
		Body:           "nope",
		Eval:           true,
		AgentSessionID: "bad\x00id",
	})
	if err == nil {
		t.Fatalf("expected control-char rejection, got nil")
	}
	if !strings.Contains(err.Error(), "session_id") {
		t.Fatalf("error %q does not mention session_id", err.Error())
	}
}

// TestResolveEvalContextNoClaim returns a zero context when no claim
// is open on the issue — the UI blocks this case but the store stays
// defensive.
func TestResolveEvalContextNoClaim(t *testing.T) {
	s, _, iss := seedRepoAndIssue(t)
	ctx, err := s.ResolveEvalContext(iss.ID)
	if err != nil {
		t.Fatalf("ResolveEvalContext: %v", err)
	}
	if ctx.AgentSessionID != "" || ctx.DispatchID != nil || ctx.Mode != "" {
		t.Fatalf("expected zero context, got %+v", ctx)
	}
}

// TestResolveEvalContextClaimNoDispatch returns the session id only
// when a claim is open but no dispatch matches — the manual-claim
// path the brief calls out.
func TestResolveEvalContextClaimNoDispatch(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "manual-claim", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("UpsertAgentSession: %v", err)
	}
	if _, _, _, _, err := s.AddAgentClaim("manual-claim", iss.ID, "manual"); err != nil {
		t.Fatalf("AddAgentClaim: %v", err)
	}
	ctx, err := s.ResolveEvalContext(iss.ID)
	if err != nil {
		t.Fatalf("ResolveEvalContext: %v", err)
	}
	if ctx.AgentSessionID != "manual-claim" {
		t.Fatalf("AgentSessionID = %q, want manual-claim", ctx.AgentSessionID)
	}
	if ctx.DispatchID != nil || ctx.Mode != "" {
		t.Fatalf("manual-claim path should leave dispatch/mode empty: %+v", ctx)
	}
}

// TestCommentTranscriptEventRefRoundTrips (BACI-141) locks in that the
// new per-event anchor handle round-trips through CreateComment /
// ListComments verbatim and that an empty handle leaves the column NULL
// (no leakage into the read shape).
func TestCommentTranscriptEventRefRoundTrips(t *testing.T) {
	s, _, iss := seedRepoAndIssue(t)
	// Empty ref: dispatch-level anchoring (the BACI-131 default).
	plain, err := s.CreateComment(CreateCommentIn{
		IssueID: iss.ID,
		Author:  "alice",
		Body:    "plain note",
	})
	if err != nil {
		t.Fatalf("CreateComment plain: %v", err)
	}
	if plain.TranscriptEventRef != "" {
		t.Fatalf("plain comment leaked ref: %q", plain.TranscriptEventRef)
	}
	// Non-empty ref: per-event anchoring (BACI-141).
	anchored, err := s.CreateComment(CreateCommentIn{
		IssueID:            iss.ID,
		Author:             "alice",
		Body:               "anchored note",
		TranscriptEventRef: "tool_use_id:abc123",
	})
	if err != nil {
		t.Fatalf("CreateComment anchored: %v", err)
	}
	if anchored.TranscriptEventRef != "tool_use_id:abc123" {
		t.Fatalf("ref round-trip lost: %q", anchored.TranscriptEventRef)
	}
	// Line-index variant for non-tool events.
	lineRef, err := s.CreateComment(CreateCommentIn{
		IssueID:            iss.ID,
		Author:             "alice",
		Body:               "by line",
		TranscriptEventRef: "line_index:42",
	})
	if err != nil {
		t.Fatalf("CreateComment line_index: %v", err)
	}
	if lineRef.TranscriptEventRef != "line_index:42" {
		t.Fatalf("line_index ref lost: %q", lineRef.TranscriptEventRef)
	}
	listed, err := s.ListComments(iss.ID)
	if err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	if len(listed) != 3 {
		t.Fatalf("listed %d, want 3", len(listed))
	}
	wantRefs := map[string]string{
		plain.UUID:    "",
		anchored.UUID: "tool_use_id:abc123",
		lineRef.UUID:  "line_index:42",
	}
	for _, c := range listed {
		if got := c.TranscriptEventRef; got != wantRefs[c.UUID] {
			t.Fatalf("listed %q ref = %q, want %q", c.UUID, got, wantRefs[c.UUID])
		}
	}
}

// TestCommentTranscriptEventRefRejectsControlChar locks the
// boundary-validator down: a malformed handle (control char) fails
// loud rather than landing in the DB.
func TestCommentTranscriptEventRefRejectsControlChar(t *testing.T) {
	s, _, iss := seedRepoAndIssue(t)
	_, err := s.CreateComment(CreateCommentIn{
		IssueID:            iss.ID,
		Author:             "alice",
		Body:               "nope",
		TranscriptEventRef: "tool_use_id:bad\x00id",
	})
	if err == nil {
		t.Fatalf("expected control-char rejection, got nil")
	}
	if !strings.Contains(err.Error(), "transcript_event_ref") {
		t.Fatalf("error %q does not mention transcript_event_ref", err.Error())
	}
}

// TestCountEvalCommentsByIssue (BACI-141) covers the bulk reader that
// fans the per-issue eval-comment count onto each board card.
func TestCountEvalCommentsByIssue(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)
	// A second issue in the same repo, used as a negative control.
	iss2, err := s.CreateIssue(repo.ID, nil, "Second issue", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	// One normal comment, one eval comment, on iss; nothing on iss2.
	if _, err := s.CreateComment(CreateCommentIn{
		IssueID: iss.ID, Author: "alice", Body: "normal",
	}); err != nil {
		t.Fatalf("CreateComment normal: %v", err)
	}
	if _, err := s.CreateComment(CreateCommentIn{
		IssueID: iss.ID, Author: "geoff", Body: "eval", Eval: true,
	}); err != nil {
		t.Fatalf("CreateComment eval: %v", err)
	}
	counts, err := s.CountEvalCommentsByIssue([]int64{iss.ID, iss2.ID})
	if err != nil {
		t.Fatalf("CountEvalCommentsByIssue: %v", err)
	}
	if got := counts[iss.ID]; got != 1 {
		t.Fatalf("counts[iss] = %d, want 1", got)
	}
	if _, ok := counts[iss2.ID]; ok {
		t.Fatalf("counts[iss2] present, want absent (zero matches)")
	}
	// Empty input returns empty map, not nil.
	empty, err := s.CountEvalCommentsByIssue(nil)
	if err != nil {
		t.Fatalf("CountEvalCommentsByIssue empty: %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty input got %v, want empty map", empty)
	}
}

// TestResolveEvalContextClaimWithDispatch surfaces the newest matching
// non-cancelled dispatch's mode and id.
func TestResolveEvalContextClaimWithDispatch(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "dispatched", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("UpsertAgentSession: %v", err)
	}
	if _, _, _, _, err := s.AddAgentClaim("dispatched", iss.ID, ""); err != nil {
		t.Fatalf("AddAgentClaim: %v", err)
	}
	// An older cancelled dispatch — must be skipped.
	cancelled, err := s.AddDispatch(AddDispatchIn{
		RepoID: repo.ID, TargetSessionID: "dispatched", IssueID: &iss.ID,
		Mode: model.DispatchModePlan, CreatedBy: "supervisor",
	})
	if err != nil {
		t.Fatalf("AddDispatch cancelled: %v", err)
	}
	if _, err := s.CancelDispatch(cancelled.ID); err != nil {
		t.Fatalf("CancelDispatch: %v", err)
	}
	// The newer pending dispatch — picked up.
	wanted, err := s.AddDispatch(AddDispatchIn{
		RepoID: repo.ID, TargetSessionID: "dispatched", IssueID: &iss.ID,
		Mode: model.DispatchModeImplement, CreatedBy: "supervisor",
	})
	if err != nil {
		t.Fatalf("AddDispatch wanted: %v", err)
	}
	ctx, err := s.ResolveEvalContext(iss.ID)
	if err != nil {
		t.Fatalf("ResolveEvalContext: %v", err)
	}
	if ctx.AgentSessionID != "dispatched" {
		t.Fatalf("AgentSessionID = %q, want dispatched", ctx.AgentSessionID)
	}
	if ctx.Mode != string(model.DispatchModeImplement) {
		t.Fatalf("Mode = %q, want implement", ctx.Mode)
	}
	if ctx.DispatchID == nil || *ctx.DispatchID != wanted.ID {
		t.Fatalf("DispatchID = %v, want %d", ctx.DispatchID, wanted.ID)
	}
}
