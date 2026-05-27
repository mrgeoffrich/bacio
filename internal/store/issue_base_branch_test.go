package store

import (
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestCreateIssueWithBaseBranch (BACI-232) exercises the per-issue
// base_branch round-trip — set on insert, read back, update via
// UpdateIssue, clear via UpdateIssue with an explicit empty pointer.
// Mirrors the shape of TestCreateFeatureWithBranchName.
func TestCreateIssueWithBaseBranch(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("IBBA", "ibb", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}

	// Empty base_branch on create → column reads NULL → BaseBranch is nil.
	noBase, err := s.CreateIssue(repo.ID, nil, "no override", "", model.StateTodo, nil, "")
	if err != nil {
		t.Fatalf("create issue no-override: %v", err)
	}
	if noBase.BaseBranch != nil {
		t.Fatalf("created issue with empty base: BaseBranch = %v, want nil", *noBase.BaseBranch)
	}

	// Non-empty base_branch on create → column reads back the value.
	iss, err := s.CreateIssue(repo.ID, nil, "ship to main", "", model.StateTodo, nil, "main")
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	if iss.BaseBranch == nil || *iss.BaseBranch != "main" {
		t.Fatalf("created base = %v, want main", iss.BaseBranch)
	}
	prefix, num, err := ParseIssueKey(iss.Key)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}
	got, err := s.GetIssueByKey(prefix, num)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.BaseBranch == nil || *got.BaseBranch != "main" {
		t.Fatalf("get base = %v, want main", got.BaseBranch)
	}

	// Update via the pointer-and-presence path: set a new value.
	newBase := "release/2025.06"
	if err := s.UpdateIssue(iss.ID, nil, nil, nil, &newBase); err != nil {
		t.Fatalf("update base: %v", err)
	}
	got, err = s.GetIssueByKey(prefix, num)
	if err != nil {
		t.Fatalf("get post-update: %v", err)
	}
	if got.BaseBranch == nil || *got.BaseBranch != "release/2025.06" {
		t.Fatalf("post-update base = %v, want release/2025.06", got.BaseBranch)
	}

	// Clear via explicit empty pointer → column goes back to NULL.
	empty := ""
	if err := s.UpdateIssue(iss.ID, nil, nil, nil, &empty); err != nil {
		t.Fatalf("clear base: %v", err)
	}
	got, err = s.GetIssueByKey(prefix, num)
	if err != nil {
		t.Fatalf("get post-clear: %v", err)
	}
	if got.BaseBranch != nil {
		t.Fatalf("post-clear BaseBranch = %v, want nil", *got.BaseBranch)
	}

	// Invalid base_branch on update is rejected (same validator as
	// features.branch_name).
	bad := "has spaces"
	if err := s.UpdateIssue(iss.ID, nil, nil, nil, &bad); err == nil {
		t.Fatalf("expected ValidateBranchName rejection for %q, got nil", bad)
	}
}

// TestCreateIssue_InvalidBaseBranch confirms validation fires on the
// insert path too, not just on update.
func TestCreateIssue_InvalidBaseBranch(t *testing.T) {
	s := newTestStore(t)
	repo, _ := s.CreateRepo("IBBB", "ibb2", t.TempDir(), "")
	if _, err := s.CreateIssue(repo.ID, nil, "bad", "", model.StateTodo, nil, "with spaces"); err == nil {
		t.Fatalf("expected ValidateBranchName rejection on create, got nil")
	}
}

// TestIssuesBaseBranchColumnExists is the schema-level guard: a fresh
// in-memory store must expose issues.base_branch, and a caller can
// write NULL by passing the empty sentinel. Mirrors
// TestFeaturesBranchNameColumnExists.
func TestIssuesBaseBranchColumnExists(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("IBBC", "ibbc", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	// Direct column read confirms the column is present and selectable.
	if _, err := s.DB.Exec(
		`INSERT INTO issues (uuid, repo_id, number, title, state, base_branch) VALUES (?, ?, ?, ?, ?, ?)`,
		"019e6671-0000-7000-0000-000000000001", repo.ID, 1, "schema-check", "todo", nil,
	); err != nil {
		t.Fatalf("insert with NULL base_branch: %v", err)
	}
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM issues WHERE base_branch IS NULL`).Scan(&n); err != nil {
		t.Fatalf("count nulls: %v", err)
	}
	if n == 0 {
		t.Fatalf("expected at least one NULL-base row")
	}
}
