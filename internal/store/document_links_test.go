package store

import (
	"testing"
	"time"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestCountTranscriptDocsByIssue covers the bulk-count helper that
// powers the BACI-141 board-card transcript indicator: rows of type
// `transcript` count, the legacy `project_complete` rows whose
// filename matches IsBacioTranscriptFilename count too, and an issue
// with zero matching docs is absent from the result map.
func TestCountTranscriptDocsByIssue(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)
	other, err := s.CreateIssue(repo.ID, nil, "other", "", model.StateTodo, nil, "", "")
	if err != nil {
		t.Fatalf("CreateIssue other: %v", err)
	}

	// Typed transcript — must count.
	typed, err := s.CreateDocument(repo.ID,
		"bacio-transcript-AGNT-1-agent-typed.jsonl",
		model.DocTypeTranscript, "{}\n", "")
	if err != nil {
		t.Fatalf("CreateDocument typed: %v", err)
	}
	if _, err := s.LinkDocument(typed.ID, LinkTarget{IssueID: &iss.ID}, ""); err != nil {
		t.Fatalf("LinkDocument typed: %v", err)
	}

	// Legacy filename-matching transcript stored as project_complete
	// (the pre-BACI-115 shape). Must count via the filename fallback.
	legacy, err := s.CreateDocument(repo.ID,
		"bacio-transcript-AGNT-1-agent-legacy.jsonl",
		model.DocTypeProjectComplete, "{}\n", "")
	if err != nil {
		t.Fatalf("CreateDocument legacy: %v", err)
	}
	if _, err := s.LinkDocument(legacy.ID, LinkTarget{IssueID: &iss.ID}, ""); err != nil {
		t.Fatalf("LinkDocument legacy: %v", err)
	}

	// A non-transcript doc — must NOT count.
	notes, err := s.CreateDocument(repo.ID, "notes.md", model.DocTypeUserDocs, "hi", "")
	if err != nil {
		t.Fatalf("CreateDocument notes: %v", err)
	}
	if _, err := s.LinkDocument(notes.ID, LinkTarget{IssueID: &iss.ID}, ""); err != nil {
		t.Fatalf("LinkDocument notes: %v", err)
	}

	counts, err := s.CountTranscriptDocsByIssue([]int64{iss.ID, other.ID})
	if err != nil {
		t.Fatalf("CountTranscriptDocsByIssue: %v", err)
	}
	if got, want := counts[iss.ID], 2; got != want {
		t.Fatalf("counts[iss] = %d, want %d (typed + legacy)", got, want)
	}
	if _, ok := counts[other.ID]; ok {
		t.Fatalf("counts[other] = %d, want absent (no linked transcripts)", counts[other.ID])
	}
}

// TestLatestPlanForIssue covers BACI-216's per-issue plan lookup:
// no plan returns nil; a single plan is returned; multiple plans
// pick the one with the newest updated_at (the link-row's own
// created_at is NOT the rule); non-plan typed docs are ignored.
func TestLatestPlanForIssue(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)

	// No plan linked yet — nil result, no error.
	got, err := s.LatestPlanForIssue(iss.ID)
	if err != nil {
		t.Fatalf("LatestPlanForIssue empty: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for no-plan issue, got %+v", got)
	}

	// A non-plan doc linked to the same issue — must NOT win the
	// lookup even though it'd otherwise be "newer".
	notes, err := s.CreateDocument(repo.ID, "notes.md", model.DocTypeUserDocs, "hi", "")
	if err != nil {
		t.Fatalf("CreateDocument notes: %v", err)
	}
	if _, err := s.LinkDocument(notes.ID, LinkTarget{IssueID: &iss.ID}, ""); err != nil {
		t.Fatalf("LinkDocument notes: %v", err)
	}
	if got, err := s.LatestPlanForIssue(iss.ID); err != nil {
		t.Fatalf("LatestPlanForIssue with notes only: %v", err)
	} else if got != nil {
		t.Fatalf("non-plan doc must not win lookup, got %+v", got)
	}

	// Single plan — wins.
	planA, err := s.CreateDocument(repo.ID, "plan-a.md", model.DocTypePlan, "plan a", "")
	if err != nil {
		t.Fatalf("CreateDocument planA: %v", err)
	}
	if _, err := s.LinkDocument(planA.ID, LinkTarget{IssueID: &iss.ID}, ""); err != nil {
		t.Fatalf("LinkDocument planA: %v", err)
	}
	got, err = s.LatestPlanForIssue(iss.ID)
	if err != nil {
		t.Fatalf("LatestPlanForIssue single: %v", err)
	}
	if got == nil || got.DocumentID != planA.ID {
		t.Fatalf("expected planA, got %+v", got)
	}
	if got.UUID == "" || got.Filename != "plan-a.md" {
		t.Fatalf("unexpected projection fields: %+v", got)
	}

	// Second plan added — newer updated_at must win.
	planB, err := s.CreateDocument(repo.ID, "plan-b.md", model.DocTypePlan, "plan b", "")
	if err != nil {
		t.Fatalf("CreateDocument planB: %v", err)
	}
	if _, err := s.LinkDocument(planB.ID, LinkTarget{IssueID: &iss.ID}, ""); err != nil {
		t.Fatalf("LinkDocument planB: %v", err)
	}
	// Forcibly stamp planA as the most-recently-edited body so we
	// know the picker actually reads updated_at and not some other
	// stable ordering. The store doesn't bump updated_at on a
	// no-op edit — drive the column directly.
	if _, err := s.DB.Exec(
		`UPDATE documents SET updated_at = ? WHERE id = ?`,
		time.Now().Add(time.Hour), planA.ID,
	); err != nil {
		t.Fatalf("bump planA updated_at: %v", err)
	}
	got, err = s.LatestPlanForIssue(iss.ID)
	if err != nil {
		t.Fatalf("LatestPlanForIssue picks latest: %v", err)
	}
	if got == nil || got.DocumentID != planA.ID {
		t.Fatalf("expected planA (newer updated_at) to win, got %+v", got)
	}
}

// TestLatestPlanByIssue covers BACI-216's bulk sibling:
// empty input returns an empty map; issues with no plan are absent;
// the per-issue picker uses the document's updated_at (NOT the
// link's created_at).
func TestLatestPlanByIssue(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)
	other, err := s.CreateIssue(repo.ID, nil, "other", "", model.StateTodo, nil, "", "")
	if err != nil {
		t.Fatalf("CreateIssue other: %v", err)
	}
	empty, err := s.CreateIssue(repo.ID, nil, "empty", "", model.StateTodo, nil, "", "")
	if err != nil {
		t.Fatalf("CreateIssue empty: %v", err)
	}

	// Empty input → empty map (caller path with no issues in scope).
	bulk, err := s.LatestPlanByIssue(nil)
	if err != nil {
		t.Fatalf("LatestPlanByIssue empty: %v", err)
	}
	if len(bulk) != 0 {
		t.Fatalf("empty input should produce empty map, got %d entries", len(bulk))
	}

	// iss gets two plans; other gets one; empty gets none.
	pA, err := s.CreateDocument(repo.ID, "iss-plan-a.md", model.DocTypePlan, "a", "")
	if err != nil {
		t.Fatalf("CreateDocument pA: %v", err)
	}
	if _, err := s.LinkDocument(pA.ID, LinkTarget{IssueID: &iss.ID}, ""); err != nil {
		t.Fatalf("LinkDocument pA: %v", err)
	}
	pB, err := s.CreateDocument(repo.ID, "iss-plan-b.md", model.DocTypePlan, "b", "")
	if err != nil {
		t.Fatalf("CreateDocument pB: %v", err)
	}
	if _, err := s.LinkDocument(pB.ID, LinkTarget{IssueID: &iss.ID}, ""); err != nil {
		t.Fatalf("LinkDocument pB: %v", err)
	}
	oP, err := s.CreateDocument(repo.ID, "other-plan.md", model.DocTypePlan, "o", "")
	if err != nil {
		t.Fatalf("CreateDocument oP: %v", err)
	}
	if _, err := s.LinkDocument(oP.ID, LinkTarget{IssueID: &other.ID}, ""); err != nil {
		t.Fatalf("LinkDocument oP: %v", err)
	}
	// A non-plan doc on iss — must NOT pollute the result.
	notes, err := s.CreateDocument(repo.ID, "iss-notes.md", model.DocTypeUserDocs, "hi", "")
	if err != nil {
		t.Fatalf("CreateDocument notes: %v", err)
	}
	if _, err := s.LinkDocument(notes.ID, LinkTarget{IssueID: &iss.ID}, ""); err != nil {
		t.Fatalf("LinkDocument notes: %v", err)
	}

	// Bump pA's updated_at into the future so it wins over pB even
	// though pB was created later (link-row ordering would otherwise
	// pick the wrong plan).
	if _, err := s.DB.Exec(
		`UPDATE documents SET updated_at = ? WHERE id = ?`,
		time.Now().Add(time.Hour), pA.ID,
	); err != nil {
		t.Fatalf("bump pA updated_at: %v", err)
	}

	bulk, err = s.LatestPlanByIssue([]int64{iss.ID, other.ID, empty.ID})
	if err != nil {
		t.Fatalf("LatestPlanByIssue: %v", err)
	}
	if got := bulk[iss.ID]; got == nil || got.DocumentID != pA.ID {
		t.Fatalf("bulk[iss] = %+v, want pA (id=%d)", got, pA.ID)
	}
	if got := bulk[other.ID]; got == nil || got.DocumentID != oP.ID {
		t.Fatalf("bulk[other] = %+v, want oP (id=%d)", got, oP.ID)
	}
	if _, ok := bulk[empty.ID]; ok {
		t.Fatalf("bulk[empty] should be absent (no plans), got %+v", bulk[empty.ID])
	}
}
