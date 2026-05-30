package store

import (
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestTakenInlinedOnListAndShow locks in BACI-37: the derived `taken`
// flag is populated on every issue read path (list, show, brief — all of
// which go through scanIssue), driven by the same join the desktop's
// ListOpenClaims runs. A fresh issue is not taken; an open claim flips
// it; a release flips it back; an ended session does not count.
func TestTakenInlinedOnListAndShow(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)

	// Fresh issue: not taken.
	got, err := s.GetIssueByID(iss.ID)
	if err != nil {
		t.Fatalf("get fresh: %v", err)
	}
	if got.Taken {
		t.Fatal("fresh issue reports taken=true")
	}
	list, err := s.ListIssues(IssueFilter{RepoID: &repo.ID})
	if err != nil {
		t.Fatalf("list fresh: %v", err)
	}
	if len(list) != 1 || list[0].Taken {
		t.Fatalf("ListIssues reported taken on a fresh issue: %+v", list)
	}

	// Open claim against an alive session flips taken to true on every
	// read path.
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "taken-sess", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, _, _, _, err := s.AddAgentClaim("taken-sess", iss.ID, "claimed"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	got, err = s.GetIssueByID(iss.ID)
	if err != nil {
		t.Fatalf("get after claim: %v", err)
	}
	if !got.Taken {
		t.Fatal("GetIssueByID reports taken=false after an open claim")
	}
	list, err = s.ListIssues(IssueFilter{RepoID: &repo.ID})
	if err != nil {
		t.Fatalf("list after claim: %v", err)
	}
	if len(list) != 1 || !list[0].Taken {
		t.Fatalf("ListIssues did not surface taken: %+v", list)
	}

	// Releasing the claim flips taken back to false.
	if _, _, _, err := s.ReleaseAgentClaim("taken-sess", iss.ID, model.StateInReview); err != nil {
		t.Fatalf("release: %v", err)
	}
	got, err = s.GetIssueByID(iss.ID)
	if err != nil {
		t.Fatalf("get after release: %v", err)
	}
	if got.Taken {
		t.Fatal("released claim still reports taken=true")
	}

	// Claim again, then end the session — taken must drop back to false
	// (an ended session isn't busy, matching OpenClaimsBySession's gate).
	if _, _, _, _, err := s.AddAgentClaim("taken-sess", iss.ID, "again"); err != nil {
		t.Fatalf("re-claim: %v", err)
	}
	if _, _, _, _, _, err := s.EndAgentSession("taken-sess", string(model.EndReasonStop), model.StateInReview, DispatchCascadeCancel); err != nil {
		t.Fatalf("end: %v", err)
	}
	got, err = s.GetIssueByID(iss.ID)
	if err != nil {
		t.Fatalf("get after end: %v", err)
	}
	if got.Taken {
		t.Fatal("issue still reports taken=true after the holding session ended")
	}
}
