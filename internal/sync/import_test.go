package sync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// TestImport_RoundTripFromExport is the headline Phase 3 invariant:
// export DB-A, import into a fresh DB-B, re-export DB-B, the two
// folder trees are byte-identical. This subsumes a long list of
// per-record assertions — if any field, hash, label, or
// cross-reference round-trip is broken, the bytes diverge.
func TestImport_RoundTripFromExport(t *testing.T) {
	a, _ := seedExportFixture(t)
	dirA := t.TempDir()
	if _, err := (&Engine{Store: a}).Export(context.Background(), dirA); err != nil {
		t.Fatalf("export A: %v", err)
	}

	b, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open b: %v", err)
	}
	t.Cleanup(func() { b.Close() })
	res, err := (&Engine{Store: b, Actor: "tester"}).Import(context.Background(), dirA)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Inserted == 0 {
		t.Fatal("import inserted 0 rows; expected the full fixture")
	}
	if len(res.Dangling) > 0 {
		t.Errorf("unexpected dangling refs: %+v", res.Dangling)
	}
	if len(res.Renumbered) > 0 || len(res.Renamed) > 0 || len(res.Deleted) > 0 {
		t.Errorf("unexpected churn on first import: %+v / %+v / %+v", res.Renumbered, res.Renamed, res.Deleted)
	}

	dirB := t.TempDir()
	if _, err := (&Engine{Store: b}).Export(context.Background(), dirB); err != nil {
		t.Fatalf("export B: %v", err)
	}

	treeA := readTree(t, dirA)
	treeB := readTree(t, dirB)
	if len(treeA) != len(treeB) {
		t.Fatalf("file count mismatch A=%d B=%d", len(treeA), len(treeB))
	}
	for path, body := range treeA {
		other, ok := treeB[path]
		if !ok {
			t.Errorf("missing %s in B", path)
			continue
		}
		if string(body) != string(other) {
			t.Errorf("body differs for %s:\nA:\n%s\nB:\n%s", path, body, other)
		}
	}
}

// TestImport_ReimportReportsNoop: importing the same export twice in
// a row should report every record as `noop` on the second pass —
// nothing differs, no UPDATE was warranted. Phase 5 added the
// fields-actually-changed precheck inside applyIssues / applyDocuments
// (applyFeatures had it from Phase 3) to make this hold.
func TestImport_ReimportReportsNoop(t *testing.T) {
	a, _ := seedExportFixture(t)
	dirA := t.TempDir()
	if _, err := (&Engine{Store: a}).Export(context.Background(), dirA); err != nil {
		t.Fatalf("export A: %v", err)
	}
	b, _ := store.Open(":memory:")
	t.Cleanup(func() { b.Close() })
	first, err := (&Engine{Store: b}).Import(context.Background(), dirA)
	if err != nil {
		t.Fatalf("import 1: %v", err)
	}
	if first.Inserted == 0 {
		t.Fatal("first import inserted nothing")
	}
	second, err := (&Engine{Store: b}).Import(context.Background(), dirA)
	if err != nil {
		t.Fatalf("import 2: %v", err)
	}
	if second.Inserted != 0 {
		t.Errorf("second import unexpectedly inserted %d", second.Inserted)
	}
	if second.Updated != 0 {
		t.Errorf("second import reported %d updates; expected 0 (everything is unchanged)", second.Updated)
	}
	if second.NoOp == 0 {
		t.Errorf("second import reported 0 noops; expected the full record set")
	}
}

// TestImport_CollisionRenumber: DB-B has a local-only issue at the
// same number as one of DB-A's issues but with a different uuid.
// Importing A into B should renumber B's issue and append a
// redirects entry.
func TestImport_CollisionRenumber(t *testing.T) {
	a, _ := seedExportFixture(t)
	dirA := t.TempDir()
	if _, err := (&Engine{Store: a}).Export(context.Background(), dirA); err != nil {
		t.Fatalf("export A: %v", err)
	}

	// B starts as a copy of A's repo (so the prefix-uuid match works),
	// plus an extra issue that collides on number with A's issue 1.
	// Easiest path: import A's repo.yaml only, then add issues.
	b, _ := store.Open(":memory:")
	t.Cleanup(func() { b.Close() })

	// Import everything from A into B first to share the repo uuid.
	if _, err := (&Engine{Store: b}).Import(context.Background(), dirA); err != nil {
		t.Fatalf("seed B: %v", err)
	}
	// Now delete A's MINI-1 from disk and add a fresh local issue
	// to B. The local issue gets number 3 (next available); when we
	// rename it to MINI-1, it'll collide with A's incoming MINI-1.
	// Actually simpler: delete B's MINI-1 (from sync) and re-create
	// it locally so it gets a new uuid.
	repo, err := b.GetRepoByPrefix("MINI")
	if err != nil {
		t.Fatalf("get repo: %v", err)
	}
	if _, err := b.DB.Exec(`DELETE FROM issues WHERE repo_id = ? AND number = 1`, repo.ID); err != nil {
		t.Fatalf("delete iss1: %v", err)
	}
	// Drop sync_state for the deleted uuid so the next import doesn't
	// see it as "previously synced, now missing → propagate delete".
	if _, err := b.DB.Exec(`DELETE FROM sync_state WHERE kind = 'issue'`); err != nil {
		t.Fatalf("clear sync_state: %v", err)
	}
	// Create a new local issue at number 1 (it'll allocate next, but
	// we force the number directly).
	freshIssue, err := b.CreateIssue(repo.ID, nil, "Local replacement for 1", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("create fresh: %v", err)
	}
	if _, err := b.DB.Exec(`UPDATE issues SET number = 1 WHERE id = ?`, freshIssue.ID); err != nil {
		t.Fatalf("force number: %v", err)
	}

	// Now import A → B. A's MINI-1 has uuid X; B's MINI-1 has a
	// different uuid. Collision rule: B's local-only row is
	// renumbered, A's keeps the label.
	res, err := (&Engine{Store: b}).Import(context.Background(), dirA)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(res.Renumbered) != 1 {
		t.Fatalf("expected 1 renumber, got %d: %+v", len(res.Renumbered), res.Renumbered)
	}
	if got := res.Renumbered[0].OldNumber; got != 1 {
		t.Errorf("renumber.OldNumber: got %d, want 1", got)
	}
	if got := res.Renumbered[0].NewNumber; got <= 1 {
		t.Errorf("renumber.NewNumber: got %d, want > 1", got)
	}
	// redirects.yaml on disk should record the move.
	body, err := os.ReadFile(filepath.Join(dirA, "repos", "MINI", "redirects.yaml"))
	if err != nil {
		t.Fatalf("read redirects: %v", err)
	}
	if !strings.Contains(string(body), `kind: "issue"`) {
		t.Errorf("redirects.yaml missing entry:\n%s", body)
	}
}

// TestImport_DeletionPropagated: DB-B has imported A previously; A's
// export tree loses an issue folder; re-importing should propagate
// the delete and drop the sync_state row.
func TestImport_DeletionPropagated(t *testing.T) {
	a, _ := seedExportFixture(t)
	dirA := t.TempDir()
	if _, err := (&Engine{Store: a}).Export(context.Background(), dirA); err != nil {
		t.Fatalf("export A: %v", err)
	}
	b, _ := store.Open(":memory:")
	t.Cleanup(func() { b.Close() })
	if _, err := (&Engine{Store: b}).Import(context.Background(), dirA); err != nil {
		t.Fatalf("seed B: %v", err)
	}
	// Remove MINI-2 from the export tree.
	if err := os.RemoveAll(filepath.Join(dirA, "repos", "MINI", "issues", "MINI-2")); err != nil {
		t.Fatalf("rm MINI-2: %v", err)
	}
	res, err := (&Engine{Store: b}).Import(context.Background(), dirA)
	if err != nil {
		t.Fatalf("re-import: %v", err)
	}
	if len(res.Deleted) != 1 {
		t.Fatalf("expected 1 deletion, got %d: %+v", len(res.Deleted), res.Deleted)
	}
	if res.Deleted[0].Label != "MINI-2" {
		t.Errorf("deleted label: got %q, want MINI-2", res.Deleted[0].Label)
	}
	// And the issue is gone from DB.
	repo, _ := b.GetRepoByPrefix("MINI")
	_, err = b.GetIssueByKey(repo.Prefix, 2)
	if err == nil {
		t.Error("MINI-2 still exists in B after deletion propagation")
	}
}

// TestImport_DryRunRollsBack: a dry-run import reports what would
// happen but leaves the DB unchanged.
func TestImport_DryRunRollsBack(t *testing.T) {
	a, _ := seedExportFixture(t)
	dirA := t.TempDir()
	if _, err := (&Engine{Store: a}).Export(context.Background(), dirA); err != nil {
		t.Fatalf("export A: %v", err)
	}
	b, _ := store.Open(":memory:")
	t.Cleanup(func() { b.Close() })
	res, err := (&Engine{Store: b, DryRun: true}).Import(context.Background(), dirA)
	if err != nil {
		t.Fatalf("dry-run import: %v", err)
	}
	if res.Inserted == 0 {
		t.Error("dry-run reported 0 inserts; expected populated counts")
	}
	// And DB should be empty.
	repos, _ := b.ListRepos()
	if len(repos) != 0 {
		t.Errorf("dry-run wrote %d repos; expected 0", len(repos))
	}
}

// TestImport_EmptySource: importing from a folder with no repos/
// directory is fine; reports zeros.
func TestImport_EmptySource(t *testing.T) {
	dir := t.TempDir()
	b, _ := store.Open(":memory:")
	t.Cleanup(func() { b.Close() })
	res, err := (&Engine{Store: b}).Import(context.Background(), dir)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Repos != 0 || res.Issues != 0 {
		t.Errorf("expected zero counts, got %+v", res)
	}
}
