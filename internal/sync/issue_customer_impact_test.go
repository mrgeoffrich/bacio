// BACI-349 sync round-trip tests for issues.customer_impact. Mirrors the
// feature_handoffs_test.go two-machine shape: set the field on A,
// export + import into B, verify it landed; plus the churn-guard that a
// blank-impact issue.yaml carries no customer_impact key, and that an
// impact-only edit perturbs contentHashIssue (so a sibling sees `updated`
// rather than a silent `noop`).
package sync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/store"
)

// TestImport_RoundTripsCustomerImpact sets a customer impact on A's iss1,
// confirms the emitter wrote the key, and that B picks it up on import.
func TestImport_RoundTripsCustomerImpact(t *testing.T) {
	a, uuids := seedExportFixture(t)

	if _, err := a.DB.Exec(
		`UPDATE issues SET customer_impact = 'Login no longer 500s on Safari', updated_at = '2026-05-15 09:00:00' WHERE uuid = ?`,
		uuids["iss1"]); err != nil {
		t.Fatalf("set customer_impact on A: %v", err)
	}

	dirA := t.TempDir()
	if _, err := (&Engine{Store: a}).Export(context.Background(), dirA); err != nil {
		t.Fatalf("export A: %v", err)
	}

	issPath := filepath.Join(dirA, "repos", "MINI", "issues", "MINI-1", "issue.yaml")
	raw, err := os.ReadFile(issPath)
	if err != nil {
		t.Fatalf("read issue.yaml: %v", err)
	}
	if !strings.Contains(string(raw), `customer_impact: "Login no longer 500s on Safari"`) {
		t.Fatalf("issue.yaml missing customer_impact line; body:\n%s", raw)
	}

	b, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open b: %v", err)
	}
	t.Cleanup(func() { b.Close() })
	if _, err := (&Engine{Store: b}).Import(context.Background(), dirA); err != nil {
		t.Fatalf("import into B: %v", err)
	}
	issB, err := b.GetIssueByUUID(uuids["iss1"])
	if err != nil {
		t.Fatalf("get iss B: %v", err)
	}
	if issB.CustomerImpact != "Login no longer 500s on Safari" {
		t.Fatalf("customer_impact did not round-trip; got %q", issB.CustomerImpact)
	}

	// Clear on A, re-export, re-import — the update branch must follow the
	// field back to empty.
	if _, err := a.DB.Exec(
		`UPDATE issues SET customer_impact = '', updated_at = '2026-05-20 09:00:00' WHERE uuid = ?`,
		uuids["iss1"]); err != nil {
		t.Fatalf("clear customer_impact on A: %v", err)
	}
	dirA2 := t.TempDir()
	if _, err := (&Engine{Store: a}).Export(context.Background(), dirA2); err != nil {
		t.Fatalf("re-export A: %v", err)
	}
	if _, err := (&Engine{Store: b}).Import(context.Background(), dirA2); err != nil {
		t.Fatalf("re-import into B: %v", err)
	}
	issB2, _ := b.GetIssueByUUID(uuids["iss1"])
	if issB2.CustomerImpact != "" {
		t.Fatalf("customer_impact clear did not round-trip; got %q, want empty", issB2.CustomerImpact)
	}
}

// TestExport_CustomerImpactOmitsBlank is the churn-guard: an issue with a
// blank customer_impact (the default for every pre-BACI-349 issue) emits
// an issue.yaml that does NOT carry the key, so existing issue files stay
// byte-identical and the next sync doesn't churn every issue.
func TestExport_CustomerImpactOmitsBlank(t *testing.T) {
	a, _ := seedExportFixture(t)
	dirA := t.TempDir()
	if _, err := (&Engine{Store: a}).Export(context.Background(), dirA); err != nil {
		t.Fatalf("export A: %v", err)
	}
	issPath := filepath.Join(dirA, "repos", "MINI", "issues", "MINI-1", "issue.yaml")
	raw, err := os.ReadFile(issPath)
	if err != nil {
		t.Fatalf("read issue.yaml: %v", err)
	}
	if strings.Contains(string(raw), "customer_impact") {
		t.Fatalf("issue.yaml carries customer_impact for a blank-impact issue; body:\n%s", raw)
	}
}

// TestContentHashIssue_CustomerImpactSensitive confirms an impact-only
// edit perturbs the content hash so a re-import reports `updated`, not a
// silent `noop`.
func TestContentHashIssue_CustomerImpactSensitive(t *testing.T) {
	base := &scannedIssue{Parsed: &ParsedIssue{
		UUID:   "019e6671-0000-7000-0000-0000000000d1",
		Number: 1,
		State:  "todo",
		Title:  "t",
	}}
	withImpact := &scannedIssue{Parsed: &ParsedIssue{
		UUID:           base.Parsed.UUID,
		Number:         1,
		State:          "todo",
		Title:          "t",
		CustomerImpact: "Login no longer 500s on Safari",
	}}
	if contentHashIssue(base) == contentHashIssue(withImpact) {
		t.Fatalf("contentHashIssue ignored customer_impact — an impact-only edit would read as a no-op")
	}
}
