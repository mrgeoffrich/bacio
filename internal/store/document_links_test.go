package store

import (
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestCountTranscriptDocsByIssue (BACI-141) covers the bulk reader
// that fans the per-issue transcript-doc count onto each board card.
// It exercises three shapes: the canonical `transcript`-typed doc, the
// legacy `project_complete`-typed doc whose filename matches
// model.IsBacioTranscriptFilename, and a non-transcript attachment
// (a `plan`-typed doc) that must be ignored.
func TestCountTranscriptDocsByIssue(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)
	// Negative-control issue with no docs at all.
	iss2, err := s.CreateIssue(repo.ID, nil, "Second issue", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	// Canonical post-BACI-115 transcript-typed doc.
	d1, err := s.CreateDocument(repo.ID, "bacio-transcript-BACI-1-agent-abc.jsonl", model.DocTypeTranscript, "{}\n", "")
	if err != nil {
		t.Fatalf("CreateDocument transcript: %v", err)
	}
	if _, err := s.LinkDocument(d1.ID, LinkTarget{IssueID: &iss.ID}, ""); err != nil {
		t.Fatalf("LinkDocument transcript: %v", err)
	}

	// Legacy `project_complete`-typed doc with the bacio-transcript naming.
	d2, err := s.CreateDocument(repo.ID, "bacio-transcript-BACI-1-agent-legacy.jsonl", model.DocTypeProjectComplete, "{}\n", "")
	if err != nil {
		t.Fatalf("CreateDocument legacy: %v", err)
	}
	if _, err := s.LinkDocument(d2.ID, LinkTarget{IssueID: &iss.ID}, ""); err != nil {
		t.Fatalf("LinkDocument legacy: %v", err)
	}

	// Non-transcript doc — must NOT be counted.
	d3, err := s.CreateDocument(repo.ID, "plan.md", model.DocTypePlan, "# plan", "")
	if err != nil {
		t.Fatalf("CreateDocument plan: %v", err)
	}
	if _, err := s.LinkDocument(d3.ID, LinkTarget{IssueID: &iss.ID}, ""); err != nil {
		t.Fatalf("LinkDocument plan: %v", err)
	}

	counts, err := s.CountTranscriptDocsByIssue([]int64{iss.ID, iss2.ID})
	if err != nil {
		t.Fatalf("CountTranscriptDocsByIssue: %v", err)
	}
	if got := counts[iss.ID]; got != 2 {
		t.Fatalf("counts[iss] = %d, want 2 (transcript + legacy)", got)
	}
	if _, ok := counts[iss2.ID]; ok {
		t.Fatalf("counts[iss2] present, want absent (zero matches)")
	}

	// Empty input returns empty map, not nil.
	empty, err := s.CountTranscriptDocsByIssue(nil)
	if err != nil {
		t.Fatalf("CountTranscriptDocsByIssue empty: %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty input got %v, want empty map", empty)
	}
}
