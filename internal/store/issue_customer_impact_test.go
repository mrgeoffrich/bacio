package store

import (
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestCreateIssueWithCustomerImpact (BACI-349) exercises the
// customer_impact round-trip on the insert path: blank stays blank, a
// non-empty value reads back, and an over-cap / control-char value is
// rejected at the store boundary. Mirrors TestCreateIssueWithBaseBranch.
func TestCreateIssueWithCustomerImpact(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("ICIA", "ici", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}

	// Blank impact on create → the "no impact" state, reads back as "".
	internal, err := s.CreateIssue(repo.ID, nil, "internal refactor", "", model.StateTodo, nil, "", "")
	if err != nil {
		t.Fatalf("create internal issue: %v", err)
	}
	if internal.CustomerImpact != "" {
		t.Fatalf("blank-impact issue: CustomerImpact = %q, want empty", internal.CustomerImpact)
	}

	// Non-empty impact on create → reads back verbatim.
	iss, err := s.CreateIssue(repo.ID, nil, "fix Safari login", "", model.StateTodo, nil, "", "Login no longer 500s on Safari")
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	if iss.CustomerImpact != "Login no longer 500s on Safari" {
		t.Fatalf("created impact = %q, want the Safari line", iss.CustomerImpact)
	}
	prefix, num, err := ParseIssueKey(iss.Key)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}
	got, err := s.GetIssueByKey(prefix, num)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.CustomerImpact != "Login no longer 500s on Safari" {
		t.Fatalf("get impact = %q, want the Safari line", got.CustomerImpact)
	}

	// Over-cap impact on create is rejected (same single-line validator,
	// title length cap).
	if _, err := s.CreateIssue(repo.ID, nil, "too long", "", model.StateTodo, nil, "", strings.Repeat("x", maxTitleLen+1)); err == nil {
		t.Fatalf("expected over-cap customer_impact rejection on create, got nil")
	}
}

// TestUpdateIssueCustomerImpact (BACI-349) exercises the edit path's
// pointer-and-presence semantics: nil = no change, a non-nil value sets,
// and a non-nil empty pointer clears back to the "no impact" state.
func TestUpdateIssueCustomerImpact(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("ICIB", "ici2", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	iss, err := s.CreateIssue(repo.ID, nil, "seed", "", model.StateTodo, nil, "", "First impact")
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	prefix, num, _ := ParseIssueKey(iss.Key)

	// nil pointer = no change — the existing value is left intact.
	if err := s.UpdateIssue(iss.ID, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("update no-op: %v", err)
	}
	got, _ := s.GetIssueByKey(prefix, num)
	if got.CustomerImpact != "First impact" {
		t.Fatalf("no-op update changed impact to %q, want unchanged", got.CustomerImpact)
	}

	// Set a new value.
	next := "Shipped list is scannable at a glance"
	if err := s.UpdateIssue(iss.ID, nil, nil, nil, nil, &next); err != nil {
		t.Fatalf("update impact: %v", err)
	}
	got, _ = s.GetIssueByKey(prefix, num)
	if got.CustomerImpact != next {
		t.Fatalf("post-update impact = %q, want %q", got.CustomerImpact, next)
	}

	// Clear via explicit empty pointer → back to the "no impact" state.
	empty := ""
	if err := s.UpdateIssue(iss.ID, nil, nil, nil, nil, &empty); err != nil {
		t.Fatalf("clear impact: %v", err)
	}
	got, _ = s.GetIssueByKey(prefix, num)
	if got.CustomerImpact != "" {
		t.Fatalf("post-clear impact = %q, want empty", got.CustomerImpact)
	}

	// Invalid impact on update (control char) is rejected.
	bad := "embedded\nnewline"
	if err := s.UpdateIssue(iss.ID, nil, nil, nil, nil, &bad); err == nil {
		t.Fatalf("expected ValidateCustomerImpact rejection for an embedded newline, got nil")
	}
}

// TestIssuesCustomerImpactColumnExists is the schema-level guard: a fresh
// in-memory store must expose issues.customer_impact as a NOT NULL
// DEFAULT '' column, so an INSERT that omits it lands the empty default.
// Mirrors TestIssuesBaseBranchColumnExists.
func TestIssuesCustomerImpactColumnExists(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("ICIC", "icic", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	if _, err := s.DB.Exec(
		`INSERT INTO issues (uuid, repo_id, number, title, state) VALUES (?, ?, ?, ?, ?)`,
		"019e6671-0000-7000-0000-0000000000c1", repo.ID, 1, "schema-check", "todo",
	); err != nil {
		t.Fatalf("insert without customer_impact: %v", err)
	}
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM issues WHERE customer_impact = ''`).Scan(&n); err != nil {
		t.Fatalf("count empty impact: %v", err)
	}
	if n == 0 {
		t.Fatalf("expected the schema default '' on the column-omitted row")
	}
}
