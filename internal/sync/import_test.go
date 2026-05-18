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

// TestImport_LWW_Issue_PreservesNewerLocal: BACI-5. When the remote
// YAML's updated_at is older than the local DB row's, import must
// preserve local (body, state, tags) and report the skip via
// ImportResult.Skipped / SkippedStale instead of silently downgrading.
func TestImport_LWW_Issue_PreservesNewerLocal(t *testing.T) {
	b, uuids := seedExportFixture(t)
	dirA := t.TempDir()
	if _, err := (&Engine{Store: b}).Export(context.Background(), dirA); err != nil {
		t.Fatalf("export: %v", err)
	}
	// Tamper with MINI-1's exported YAML so a naive import would
	// observe a "change". Leave updated_at intact (older than the
	// value we'll set locally below).
	iss1Path := filepath.Join(dirA, "repos", "MINI", "issues", "MINI-1", "issue.yaml")
	body, err := os.ReadFile(iss1Path)
	if err != nil {
		t.Fatalf("read iss1 yaml: %v", err)
	}
	tampered := strings.Replace(string(body), `state: "in_progress"`, `state: "todo"`, 1)
	if tampered == string(body) {
		t.Fatalf("expected to flip state in yaml, body:\n%s", body)
	}
	if err := os.WriteFile(iss1Path, []byte(tampered), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Bump local MINI-1 updated_at to a value strictly newer than the
	// fixture's '2026-05-09 14:22:00'.
	if _, err := b.DB.Exec(
		`UPDATE issues SET updated_at = '2026-05-15 10:00:00' WHERE uuid = ?`, uuids["iss1"],
	); err != nil {
		t.Fatalf("bump local updated_at: %v", err)
	}

	res, err := (&Engine{Store: b}).Import(context.Background(), dirA)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Skipped < 1 {
		t.Fatalf("expected Skipped >= 1, got %d (res=%+v)", res.Skipped, res)
	}
	var hit *SkippedStaleEntry
	for i := range res.SkippedStale {
		if res.SkippedStale[i].UUID == uuids["iss1"] {
			hit = &res.SkippedStale[i]
			break
		}
	}
	if hit == nil {
		t.Fatalf("MINI-1 not in SkippedStale: %+v", res.SkippedStale)
	}
	if hit.Kind != "issue" {
		t.Errorf("SkippedStale.Kind: got %q, want issue", hit.Kind)
	}
	if hit.Label != "MINI-1" {
		t.Errorf("SkippedStale.Label: got %q, want MINI-1", hit.Label)
	}

	// Local body preserved — state must still be in_progress, not the
	// "todo" we wrote into the YAML.
	iss, err := b.GetIssueByKey("MINI", 1)
	if err != nil {
		t.Fatalf("get issue: %v", err)
	}
	if iss.State != model.StateInProgress {
		t.Errorf("local state was overwritten to %v; expected in_progress (LWW skip should have preserved it)", iss.State)
	}
	// Tags on local must also be preserved (side-data skip).
	tagSet := map[string]bool{}
	for _, tag := range iss.Tags {
		tagSet[tag] = true
	}
	if !tagSet["p1"] || !tagSet["security"] {
		t.Errorf("local tags clobbered: got %v; expected p1+security to survive", iss.Tags)
	}
}

// TestImport_LWW_Feature_PreservesNewerLocal: feature variant of the
// LWW skip test.
func TestImport_LWW_Feature_PreservesNewerLocal(t *testing.T) {
	b, uuids := seedExportFixture(t)
	dirA := t.TempDir()
	if _, err := (&Engine{Store: b}).Export(context.Background(), dirA); err != nil {
		t.Fatalf("export: %v", err)
	}
	featPath := filepath.Join(dirA, "repos", "MINI", "features", "auth-rewrite", "feature.yaml")
	body, err := os.ReadFile(featPath)
	if err != nil {
		t.Fatalf("read feature yaml: %v", err)
	}
	tampered := strings.Replace(string(body), `title: "Rewrite auth"`, `title: "Older remote title"`, 1)
	if tampered == string(body) {
		t.Fatalf("expected to replace title in feature yaml; body:\n%s", body)
	}
	if err := os.WriteFile(featPath, []byte(tampered), 0o644); err != nil {
		t.Fatalf("write feature yaml: %v", err)
	}
	if _, err := b.DB.Exec(
		`UPDATE features SET updated_at = '2026-05-15 10:00:00' WHERE uuid = ?`, uuids["feat"],
	); err != nil {
		t.Fatalf("bump local feature updated_at: %v", err)
	}

	res, err := (&Engine{Store: b}).Import(context.Background(), dirA)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	var hit *SkippedStaleEntry
	for i := range res.SkippedStale {
		if res.SkippedStale[i].UUID == uuids["feat"] {
			hit = &res.SkippedStale[i]
			break
		}
	}
	if hit == nil {
		t.Fatalf("feature not in SkippedStale: %+v", res.SkippedStale)
	}
	if hit.Kind != "feature" {
		t.Errorf("SkippedStale.Kind: got %q, want feature", hit.Kind)
	}
	f, err := b.GetFeatureByUUID(uuids["feat"])
	if err != nil {
		t.Fatalf("get feature: %v", err)
	}
	if f.Title != "Rewrite auth" {
		t.Errorf("local title was overwritten to %q; expected Rewrite auth", f.Title)
	}
}

// TestImport_LWW_Document_PreservesNewerLocal: document variant.
func TestImport_LWW_Document_PreservesNewerLocal(t *testing.T) {
	b, uuids := seedExportFixture(t)
	dirA := t.TempDir()
	if _, err := (&Engine{Store: b}).Export(context.Background(), dirA); err != nil {
		t.Fatalf("export: %v", err)
	}
	docMD := filepath.Join(dirA, "repos", "MINI", "docs", "auth-overview.md", "content.md")
	if err := os.WriteFile(docMD, []byte("# Overwritten by older remote\n"), 0o644); err != nil {
		t.Fatalf("write doc md: %v", err)
	}
	if _, err := b.DB.Exec(
		`UPDATE documents SET updated_at = '2026-05-15 10:00:00' WHERE uuid = ?`, uuids["doc"],
	); err != nil {
		t.Fatalf("bump local doc updated_at: %v", err)
	}
	res, err := (&Engine{Store: b}).Import(context.Background(), dirA)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	var hit *SkippedStaleEntry
	for i := range res.SkippedStale {
		if res.SkippedStale[i].UUID == uuids["doc"] {
			hit = &res.SkippedStale[i]
			break
		}
	}
	if hit == nil {
		t.Fatalf("doc not in SkippedStale: %+v", res.SkippedStale)
	}
	if hit.Kind != "document" {
		t.Errorf("SkippedStale.Kind: got %q, want document", hit.Kind)
	}
	d, err := b.GetDocumentByUUID(uuids["doc"], true)
	if err != nil {
		t.Fatalf("get doc: %v", err)
	}
	if !strings.Contains(d.Content, "Auth overview") {
		t.Errorf("local doc content was overwritten: %q", d.Content)
	}
}

// TestImport_LWW_NewerRemote_Updates: regression check that the
// existing "remote newer, apply remote" behaviour still fires when
// remote's updated_at is greater than local's.
func TestImport_LWW_NewerRemote_Updates(t *testing.T) {
	b, uuids := seedExportFixture(t)
	dirA := t.TempDir()
	if _, err := (&Engine{Store: b}).Export(context.Background(), dirA); err != nil {
		t.Fatalf("export: %v", err)
	}
	iss1Path := filepath.Join(dirA, "repos", "MINI", "issues", "MINI-1", "issue.yaml")
	body, err := os.ReadFile(iss1Path)
	if err != nil {
		t.Fatalf("read iss1 yaml: %v", err)
	}
	// Bump remote updated_at past local (fixture sets local to
	// '2026-05-09 14:22:00'), and flip state. The YAML uses RFC3339 in
	// UTC, e.g. "2026-05-09T14:22:00Z".
	tampered := strings.Replace(string(body), "2026-05-09T14:22:00Z", "2026-06-01T10:00:00Z", 1)
	tampered = strings.Replace(tampered, `state: "in_progress"`, `state: "done"`, 1)
	if tampered == string(body) {
		t.Fatalf("expected to bump remote ts + flip state; body:\n%s", body)
	}
	if err := os.WriteFile(iss1Path, []byte(tampered), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	res, err := (&Engine{Store: b}).Import(context.Background(), dirA)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Skipped != 0 {
		t.Errorf("expected Skipped=0 when remote is newer, got %d (entries: %+v)", res.Skipped, res.SkippedStale)
	}
	iss, err := b.GetIssueByKey("MINI", 1)
	if err != nil {
		t.Fatalf("get issue: %v", err)
	}
	if iss.State != model.StateDone {
		t.Errorf("expected remote update to apply (state=done); got %v", iss.State)
	}
	_ = uuids
}

// TestImport_RoundTripsArchivedAt is the BACI-68 reviewer follow-up
// (point 5 on PR #103). Archive an issue, feature, and document on
// machine A, export, import into machine B, and verify each row lands
// archived. Then unarchive on A, re-export/import, and verify B
// follows back to live. Without the sync-layer field add the second
// machine would silently drop the flag in either direction.
func TestImport_RoundTripsArchivedAt(t *testing.T) {
	a, uuids := seedExportFixture(t)

	// Archive one of each kind on machine A. Bump updated_at so the
	// LWW gate inside applyIssues/applyFeatures/applyDocuments lets
	// the change through on import.
	if _, err := a.DB.Exec(
		`UPDATE issues SET archived_at = '2026-05-10 12:00:00', updated_at = '2026-05-15 09:00:00' WHERE uuid = ?`,
		uuids["iss1"]); err != nil {
		t.Fatalf("archive iss: %v", err)
	}
	if _, err := a.DB.Exec(
		`UPDATE features SET archived_at = '2026-05-10 12:01:00', updated_at = '2026-05-15 09:00:00' WHERE uuid = ?`,
		uuids["feat"]); err != nil {
		t.Fatalf("archive feat: %v", err)
	}
	if _, err := a.DB.Exec(
		`UPDATE documents SET archived_at = '2026-05-10 12:02:00', updated_at = '2026-05-15 09:00:00' WHERE uuid = ?`,
		uuids["doc"]); err != nil {
		t.Fatalf("archive doc: %v", err)
	}

	dirA := t.TempDir()
	if _, err := (&Engine{Store: a}).Export(context.Background(), dirA); err != nil {
		t.Fatalf("export A: %v", err)
	}

	b, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open b: %v", err)
	}
	t.Cleanup(func() { b.Close() })
	if _, err := (&Engine{Store: b}).Import(context.Background(), dirA); err != nil {
		t.Fatalf("import into B: %v", err)
	}

	issB, err := b.GetIssueByKey("MINI", 1)
	if err != nil {
		t.Fatalf("get iss B: %v", err)
	}
	if issB.ArchivedAt == nil {
		t.Error("issue archived_at did not round-trip into B")
	}
	featB, err := b.GetFeatureByUUID(uuids["feat"])
	if err != nil {
		t.Fatalf("get feat B: %v", err)
	}
	if featB.ArchivedAt == nil {
		t.Error("feature archived_at did not round-trip into B")
	}
	docB, err := b.GetDocumentByUUID(uuids["doc"], false)
	if err != nil {
		t.Fatalf("get doc B: %v", err)
	}
	if docB.ArchivedAt == nil {
		t.Error("document archived_at did not round-trip into B")
	}

	// Now unarchive on A, re-export, re-import — B should follow back
	// to live (this exercises the apply-side update branch's
	// nullableTimeEqual check).
	if _, err := a.DB.Exec(
		`UPDATE issues SET archived_at = NULL, updated_at = '2026-05-20 09:00:00' WHERE uuid = ?`,
		uuids["iss1"]); err != nil {
		t.Fatalf("unarchive iss: %v", err)
	}
	if _, err := a.DB.Exec(
		`UPDATE features SET archived_at = NULL, updated_at = '2026-05-20 09:00:00' WHERE uuid = ?`,
		uuids["feat"]); err != nil {
		t.Fatalf("unarchive feat: %v", err)
	}
	if _, err := a.DB.Exec(
		`UPDATE documents SET archived_at = NULL, updated_at = '2026-05-20 09:00:00' WHERE uuid = ?`,
		uuids["doc"]); err != nil {
		t.Fatalf("unarchive doc: %v", err)
	}
	dirA2 := t.TempDir()
	if _, err := (&Engine{Store: a}).Export(context.Background(), dirA2); err != nil {
		t.Fatalf("re-export A: %v", err)
	}
	if _, err := (&Engine{Store: b}).Import(context.Background(), dirA2); err != nil {
		t.Fatalf("re-import into B: %v", err)
	}
	issB2, _ := b.GetIssueByKey("MINI", 1)
	if issB2.ArchivedAt != nil {
		t.Errorf("issue archived_at clear did not round-trip; got %v", issB2.ArchivedAt)
	}
	featB2, _ := b.GetFeatureByUUID(uuids["feat"])
	if featB2.ArchivedAt != nil {
		t.Errorf("feature archived_at clear did not round-trip; got %v", featB2.ArchivedAt)
	}
	docB2, _ := b.GetDocumentByUUID(uuids["doc"], false)
	if docB2.ArchivedAt != nil {
		t.Errorf("document archived_at clear did not round-trip; got %v", docB2.ArchivedAt)
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
