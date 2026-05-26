package store

import (
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestListDocuments_LinksAndSnippet locks the BACI-204 contract on
// the extended list payload: link rows ride back on each document
// via one IN-query (no N+1); the snippet projection is empty for
// transcripts (audit material, not browsing material) and trimmed
// at a word boundary for any other type whose body exceeds the cap.
func TestListDocuments_LinksAndSnippet(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)
	feat, err := s.CreateFeature(repo.ID, "auth-rewrite", "Auth rewrite", "", "", "")
	if err != nil {
		t.Fatalf("CreateFeature: %v", err)
	}

	// A plan-typed doc with two links — one issue, one feature — so
	// the list hydrator round-trips both.
	plan, err := s.CreateDocument(repo.ID, "BACI-1-plan.md", model.DocTypePlan, "plan body content", "")
	if err != nil {
		t.Fatalf("CreateDocument plan: %v", err)
	}
	if _, err := s.LinkDocument(plan.ID, LinkTarget{IssueID: &iss.ID}, "primary"); err != nil {
		t.Fatalf("LinkDocument plan→issue: %v", err)
	}
	if _, err := s.LinkDocument(plan.ID, LinkTarget{FeatureID: &feat.ID}, ""); err != nil {
		t.Fatalf("LinkDocument plan→feature: %v", err)
	}

	// A transcript-typed doc: snippet must be empty even though the
	// body content is non-empty.
	if _, err := s.CreateDocument(repo.ID,
		"bacio-transcript-AGNT-1-agent-abc.jsonl",
		model.DocTypeTranscript,
		`{"event":"hello world transcript body"}`,
		""); err != nil {
		t.Fatalf("CreateDocument transcript: %v", err)
	}

	// A long plan-shaped doc that exceeds the snippet cap so the
	// word-boundary trim is exercised.
	long := strings.Repeat("alpha beta gamma delta ", 50) // 1150 chars
	if _, err := s.CreateDocument(repo.ID, "BACI-2-plan.md", model.DocTypePlan, long, ""); err != nil {
		t.Fatalf("CreateDocument long: %v", err)
	}

	// An unlinked architecture doc: list still surfaces it, but Links
	// stays nil (omitempty drops it from the JSON).
	if _, err := s.CreateDocument(repo.ID, "arch.md", model.DocTypeArchitecture, "short", ""); err != nil {
		t.Fatalf("CreateDocument arch: %v", err)
	}

	docs, err := s.ListDocuments(DocumentFilter{RepoID: repo.ID})
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}

	byName := map[string]*model.Document{}
	for _, d := range docs {
		byName[d.Filename] = d
	}

	// Plan doc: 2 links round-trip, snippet equals the (short) body
	// because it's under the cap.
	got := byName["BACI-1-plan.md"]
	if got == nil {
		t.Fatalf("BACI-1-plan.md missing from list")
	}
	if len(got.Links) != 2 {
		t.Fatalf("plan links: got %d want 2 (links=%+v)", len(got.Links), got.Links)
	}
	gotKeys := map[string]bool{}
	for _, l := range got.Links {
		gotKeys[l.Target()] = true
	}
	if !gotKeys[iss.Key] || !gotKeys["feature/auth-rewrite"] {
		t.Fatalf("plan link targets = %v, want both %s and feature/auth-rewrite", gotKeys, iss.Key)
	}
	if got.Snippet != "plan body content" {
		t.Fatalf("plan snippet = %q, want %q", got.Snippet, "plan body content")
	}

	// Transcript doc: snippet must be empty regardless of body, links nil.
	tr := byName["bacio-transcript-AGNT-1-agent-abc.jsonl"]
	if tr == nil {
		t.Fatalf("transcript missing from list")
	}
	if tr.Snippet != "" {
		t.Fatalf("transcript snippet should be blank, got %q", tr.Snippet)
	}
	if len(tr.Links) != 0 {
		t.Fatalf("transcript links should be empty, got %+v", tr.Links)
	}

	// Long doc: snippet capped at ~snippetByteLimit and clipped at a
	// space (no mid-word truncation).
	longDoc := byName["BACI-2-plan.md"]
	if longDoc == nil {
		t.Fatalf("BACI-2-plan.md missing from list")
	}
	if len(longDoc.Snippet) >= snippetByteLimit {
		t.Fatalf("long snippet len = %d, want < %d (word-boundary trim)", len(longDoc.Snippet), snippetByteLimit)
	}
	if longDoc.Snippet == "" {
		t.Fatalf("long snippet should not be empty")
	}
	if last := longDoc.Snippet[len(longDoc.Snippet)-1]; last != 'a' && last != 'e' && last != 'r' { // last letter of alpha|gamma|delta
		// The trim walks back to a space and clips before it, so the
		// final character must be the last letter of one of the words.
		t.Fatalf("long snippet ends mid-word: %q (last byte=%q)", longDoc.Snippet, string(last))
	}

	// Unlinked arch doc: surfaces, snippet equals the (short) body,
	// no links.
	arch := byName["arch.md"]
	if arch == nil {
		t.Fatalf("arch.md missing from list")
	}
	if arch.Snippet != "short" {
		t.Fatalf("arch snippet = %q, want %q", arch.Snippet, "short")
	}
	if len(arch.Links) != 0 {
		t.Fatalf("arch links should be empty, got %+v", arch.Links)
	}
}
