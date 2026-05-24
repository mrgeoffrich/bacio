package store

import (
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestCountTranscriptDocsByIssue covers the bulk-count helper that
// powers the BACI-141 board-card transcript indicator: rows of type
// `transcript` count, the legacy `project_complete` rows whose
// filename matches IsBacioTranscriptFilename count too, and an issue
// with zero matching docs is absent from the result map.
func TestCountTranscriptDocsByIssue(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)
	other, err := s.CreateIssue(repo.ID, nil, "other", "", model.StateTodo, nil)
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
