package store

import (
	"testing"
	"time"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestLatestPRByIssue covers BACI-239's bulk per-issue PR denorm:
// empty input returns an empty map; issues with no PR are absent;
// when an issue has multiple PRs the most-recently-attached one wins
// (`created_at DESC, id DESC`) and `count` reflects the total.
func TestLatestPRByIssue(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)
	other, err := s.CreateIssue(repo.ID, nil, "other", "", model.StateTodo, nil, "")
	if err != nil {
		t.Fatalf("CreateIssue other: %v", err)
	}
	empty, err := s.CreateIssue(repo.ID, nil, "empty", "", model.StateTodo, nil, "")
	if err != nil {
		t.Fatalf("CreateIssue empty: %v", err)
	}

	// Empty input → empty map (caller path with no issues in scope).
	bulk, err := s.LatestPRByIssue(nil)
	if err != nil {
		t.Fatalf("LatestPRByIssue empty: %v", err)
	}
	if len(bulk) != 0 {
		t.Fatalf("empty input should produce empty map, got %d entries", len(bulk))
	}

	// iss gets two PRs; other gets one; empty gets none.
	prA, err := s.AttachPR(iss.ID, "https://example.com/pr/1")
	if err != nil {
		t.Fatalf("AttachPR iss/A: %v", err)
	}
	prB, err := s.AttachPR(iss.ID, "https://example.com/pr/2")
	if err != nil {
		t.Fatalf("AttachPR iss/B: %v", err)
	}
	oPR, err := s.AttachPR(other.ID, "https://example.com/pr/3")
	if err != nil {
		t.Fatalf("AttachPR other: %v", err)
	}

	// Bump prB's created_at into the future so it wins over prA even
	// though we want to prove the ordering rule against the stored
	// column (the AttachPR sequence already creates them in B-after-A
	// order; the bump here makes the test resilient to clock skew on
	// fast machines where both inserts share a timestamp).
	if _, err := s.DB.Exec(
		`UPDATE issue_pull_requests SET created_at = ? WHERE id = ?`,
		time.Now().Add(time.Hour), prB.ID,
	); err != nil {
		t.Fatalf("bump prB created_at: %v", err)
	}

	bulk, err = s.LatestPRByIssue([]int64{iss.ID, other.ID, empty.ID})
	if err != nil {
		t.Fatalf("LatestPRByIssue: %v", err)
	}
	if got := bulk[iss.ID]; got == nil || got.URL != prB.URL {
		t.Fatalf("bulk[iss] = %+v, want prB URL %q", got, prB.URL)
	}
	if got := bulk[iss.ID]; got == nil || got.Count != 2 {
		t.Fatalf("bulk[iss].Count = %d, want 2", got.Count)
	}
	if got := bulk[other.ID]; got == nil || got.URL != oPR.URL {
		t.Fatalf("bulk[other] = %+v, want oPR URL %q", got, oPR.URL)
	}
	if got := bulk[other.ID]; got == nil || got.Count != 1 {
		t.Fatalf("bulk[other].Count = %d, want 1", got.Count)
	}
	if _, ok := bulk[empty.ID]; ok {
		t.Fatalf("bulk[empty] should be absent (no PRs), got %+v", bulk[empty.ID])
	}

	// Sanity: a single-PR issue's URL roundtrips cleanly. Guards against
	// any future drift in the AttachPR / scanPR pair.
	_ = prA
}
