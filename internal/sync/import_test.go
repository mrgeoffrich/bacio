package sync

import (
	"context"
	"fmt"
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

// TestImport_CommentDeletionPropagated: a user-initiated hard delete of
// a comment (the path behind `bacio comment rm` and the API/UI delete)
// is absence-driven — the deleted comment's file disappears from the
// sync tree, and a second importer drops the row via the same
// propagateDeletes pass that handles issue/feature/doc deletes.
func TestImport_CommentDeletionPropagated(t *testing.T) {
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
	// B sees the seeded comment (the fixture's one comment on MINI-1).
	repoB, _ := b.GetRepoByPrefix("MINI")
	iss1B, err := b.GetIssueByKey(repoB.Prefix, 1)
	if err != nil {
		t.Fatalf("get MINI-1 on B: %v", err)
	}
	if cs, _ := b.ListComments(iss1B.ID); len(cs) != 1 {
		t.Fatalf("B should have 1 comment after seed, got %d", len(cs))
	}
	// A hard delete drops the comment's files from the sync tree.
	commentDir := filepath.Join(dirA, "repos", "MINI", "issues", "MINI-1", "comments")
	if err := os.RemoveAll(commentDir); err != nil {
		t.Fatalf("rm comment dir: %v", err)
	}
	// Re-import on B drops the comment via propagateDeletes.
	res, err := (&Engine{Store: b}).Import(context.Background(), dirA)
	if err != nil {
		t.Fatalf("re-import B: %v", err)
	}
	if len(res.Deleted) != 1 || res.Deleted[0].Kind != store.SyncKindComment {
		t.Fatalf("expected 1 comment deletion, got %+v", res.Deleted)
	}
	if cs, _ := b.ListComments(iss1B.ID); len(cs) != 0 {
		t.Fatalf("B still has the deleted comment: %d", len(cs))
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

// TestImport_RepoCounterMonotonic_OlderRemote pins BACI-135: when a
// peer pushes a `repo.yaml` whose `next_issue_number` is *behind* the
// local high-water mark, the importer must preserve the local value
// rather than rewinding the counter. A rewound counter would let a
// subsequent `bacio issue add` re-mint a number that already lived in
// audit history.
//
// To isolate the upsertRepo monotonic-MAX (and not lean on the
// post-import RecomputeNextIssueNumber clamp doing the work), we
// force the local counter above MAX(issues.number)+1 before the
// import so the clamp has nothing to add — the only thing that can
// preserve the local value is upsertRepo itself.
func TestImport_RepoCounterMonotonic_OlderRemote(t *testing.T) {
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
	repoB, err := b.GetRepoByPrefix("MINI")
	if err != nil {
		t.Fatalf("get repo B: %v", err)
	}
	// Push the local counter above MAX(issues.number)+1 — simulates a
	// local that allocated numbers for issues that were later deleted
	// (so the safe high-water mark is genuinely above what the issues
	// table would otherwise indicate).
	const localHigh int64 = 50
	if _, err := b.DB.Exec(`UPDATE repos SET next_issue_number = ? WHERE id = ?`, localHigh, repoB.ID); err != nil {
		t.Fatalf("bump local next_issue_number: %v", err)
	}

	// Mutate the exported repo.yaml so the remote claims a value
	// *behind* the local cache. Bump remote updated_at past local so
	// the LWW gate isn't what's preserving the counter — we want the
	// monotonic-MAX in upsertRepo to be the only thing doing the work.
	repoPath := filepath.Join(dirA, "repos", "MINI", "repo.yaml")
	body, err := os.ReadFile(repoPath)
	if err != nil {
		t.Fatalf("read repo.yaml: %v", err)
	}
	// The exported repo.yaml carries whatever value the in-memory store
	// had at export time (the original 3 from seedExportFixture, two
	// seeded issues +1). Older-remote = a smaller value still.
	const remoteOlder int64 = 2
	bodyStr := strings.Replace(
		string(body),
		"next_issue_number: 3",
		fmt.Sprintf("next_issue_number: %d", remoteOlder),
		1,
	)
	if bodyStr == string(body) {
		t.Fatalf("expected to rewrite next_issue_number in repo.yaml; body:\n%s", body)
	}
	bodyStr = strings.Replace(bodyStr, "2025-11-01T09:00:00.000Z", "2026-06-01T10:00:00.000Z", -1)
	if err := os.WriteFile(repoPath, []byte(bodyStr), 0o644); err != nil {
		t.Fatalf("write repo.yaml: %v", err)
	}

	if _, err := (&Engine{Store: b}).Import(context.Background(), dirA); err != nil {
		t.Fatalf("re-import: %v", err)
	}
	repoB2, err := b.GetRepoByPrefix("MINI")
	if err != nil {
		t.Fatalf("get repo B post-import: %v", err)
	}
	if repoB2.NextIssueNumber < localHigh {
		t.Errorf("next_issue_number rewound: pre=%d post=%d remote=%d (monotonic-MAX should have held the local value)",
			localHigh, repoB2.NextIssueNumber, remoteOlder)
	}
}

// TestImport_RepoCounterMonotonic_AfterPropagateDeletes pins the
// second BACI-135 scenario: a peer's `repo.yaml` carries a counter
// *behind* the local high-water mark AND the same import propagates
// deletes that remove the local issues that justified that counter.
// The monotonic-MAX in upsertRepo must still preserve the local value
// — RecomputeNextIssueNumber's MAX(issues.number)+1 clamp can't save
// us here, because the issues whose numbers it would clamp against
// are exactly the ones propagateDeletes just dropped.
func TestImport_RepoCounterMonotonic_AfterPropagateDeletes(t *testing.T) {
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
	repoB, err := b.GetRepoByPrefix("MINI")
	if err != nil {
		t.Fatalf("get repo B: %v", err)
	}
	nin0 := repoB.NextIssueNumber
	if nin0 < 3 {
		t.Fatalf("fixture should leave next_issue_number >= 3; got %d", nin0)
	}

	// Mimic a peer that already collapsed the namespace: drop the
	// high-numbered issue from the export tree (propagateDeletes will
	// remove it from B on re-import) AND lower next_issue_number in the
	// remote repo.yaml so the importer is faced with a smaller value.
	if err := os.RemoveAll(filepath.Join(dirA, "repos", "MINI", "issues", "MINI-2")); err != nil {
		t.Fatalf("rm MINI-2: %v", err)
	}
	repoPath := filepath.Join(dirA, "repos", "MINI", "repo.yaml")
	body, err := os.ReadFile(repoPath)
	if err != nil {
		t.Fatalf("read repo.yaml: %v", err)
	}
	older := nin0 - 2
	bodyStr := strings.Replace(
		string(body),
		fmt.Sprintf("next_issue_number: %d", nin0),
		fmt.Sprintf("next_issue_number: %d", older),
		1,
	)
	if bodyStr == string(body) {
		t.Fatalf("expected to rewrite next_issue_number from %d in repo.yaml; body:\n%s", nin0, body)
	}
	bodyStr = strings.Replace(bodyStr, "2025-11-01T09:00:00.000Z", "2026-06-01T10:00:00.000Z", -1)
	if err := os.WriteFile(repoPath, []byte(bodyStr), 0o644); err != nil {
		t.Fatalf("write repo.yaml: %v", err)
	}

	res, err := (&Engine{Store: b}).Import(context.Background(), dirA)
	if err != nil {
		t.Fatalf("re-import: %v", err)
	}
	// Sanity: propagateDeletes did drop MINI-2.
	var sawIssueDelete bool
	for _, d := range res.Deleted {
		if d.Kind == store.SyncKindIssue && d.Label == "MINI-2" {
			sawIssueDelete = true
			break
		}
	}
	if !sawIssueDelete {
		t.Fatalf("expected MINI-2 deletion to propagate; got %+v", res.Deleted)
	}
	repoB2, err := b.GetRepoByPrefix("MINI")
	if err != nil {
		t.Fatalf("get repo B post-import: %v", err)
	}
	if repoB2.NextIssueNumber < nin0 {
		t.Errorf("next_issue_number rewound after propagateDeletes: pre=%d post=%d remote=%d (monotonic-MAX should have held the local value)",
			nin0, repoB2.NextIssueNumber, older)
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

// seedJSONLDocStore builds a minimal store with one .jsonl document and
// returns the store plus the document's UUID and body. Shared by the
// BACI-102 round-trip and legacy-fallback import tests.
func seedJSONLDocStore(t *testing.T) (*store.Store, string, string) {
	t.Helper()
	const body = "{\"line\":1}\n{\"line\":2}\n"
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	r, err := s.CreateRepo("MINI", "bacio", "/local/path", "git@github.com:user/bacio.git")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	doc, err := s.CreateDocument(r.ID, "bacio-transcript-BACI-12-agent-7.jsonl", model.DocTypeProjectComplete, body, "docs/transcript.jsonl")
	if err != nil {
		t.Fatalf("create jsonl doc: %v", err)
	}
	return s, doc.UUID, body
}

// TestImport_RoundTrip_JSONLDocument exports a .jsonl document from
// store A and imports it into a fresh store B, asserting the body
// survives. This proves export and import agree on the content<ext>
// extension derived from the document filename (BACI-102).
func TestImport_RoundTrip_JSONLDocument(t *testing.T) {
	a, docUUID, body := seedJSONLDocStore(t)
	dirA := t.TempDir()
	if _, err := (&Engine{Store: a}).Export(context.Background(), dirA); err != nil {
		t.Fatalf("export A: %v", err)
	}
	// Sanity: the body is on disk as content.jsonl, not content.md.
	jsonlPath := filepath.Join(dirA, "repos", "MINI", "docs", "bacio-transcript-BACI-12-agent-7.jsonl", "content.jsonl")
	if _, err := os.Stat(jsonlPath); err != nil {
		t.Fatalf("expected content.jsonl on disk: %v", err)
	}

	b, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open b: %v", err)
	}
	t.Cleanup(func() { b.Close() })
	if _, err := (&Engine{Store: b, Actor: "tester"}).Import(context.Background(), dirA); err != nil {
		t.Fatalf("import: %v", err)
	}
	d, err := b.GetDocumentByUUID(docUUID, true)
	if err != nil {
		t.Fatalf("get doc: %v", err)
	}
	if d.Content != body {
		t.Errorf("round-trip body mismatch:\ngot:  %q\nwant: %q", d.Content, body)
	}
}

// TestImport_LWW_PreservesLocallyAttachedPR pins BACI-142 for the
// pull_requests table. A worker calls AttachPR after export; the next
// import sees stale YAML that doesn't list the new URL. The local
// row must survive — pre-fix the LWW gate missed because the
// pull_requests writer didn't bump issues.updated_at, so
// replacePRsTx wiped the new row.
func TestImport_LWW_PreservesLocallyAttachedPR(t *testing.T) {
	s, uuids := seedExportFixture(t)
	dir := t.TempDir()
	if _, err := (&Engine{Store: s}).Export(context.Background(), dir); err != nil {
		t.Fatalf("export: %v", err)
	}
	// Attach a new PR locally — the on-disk YAML doesn't list it.
	iss1, err := s.GetIssueByUUID(uuids["iss1"])
	if err != nil {
		t.Fatalf("get iss1: %v", err)
	}
	const newPR = "https://github.com/x/y/pull/99"
	if _, err := s.AttachPR(iss1.ID, newPR); err != nil {
		t.Fatalf("attach pr: %v", err)
	}
	if _, err := (&Engine{Store: s, Actor: "tester"}).Import(context.Background(), dir); err != nil {
		t.Fatalf("import: %v", err)
	}
	prs, err := s.ListPRs(iss1.ID)
	if err != nil {
		t.Fatalf("list prs: %v", err)
	}
	found := false
	for _, pr := range prs {
		if pr.URL == newPR {
			found = true
			break
		}
	}
	if !found {
		urls := make([]string, 0, len(prs))
		for _, pr := range prs {
			urls = append(urls, pr.URL)
		}
		t.Fatalf("locally-attached PR %q was wiped by sync re-import; surviving PRs: %v", newPR, urls)
	}
}

// TestImport_LWW_PreservesLocallyCreatedRelation pins BACI-142 for
// the issue_relations table. A worker creates a new relation between
// two issues that already exist after export; the next import sees
// stale YAML that doesn't list the new edge. The local edge must
// survive — pre-fix replaceRelationsTx wiped it because the
// relations writer didn't bump issues.updated_at.
func TestImport_LWW_PreservesLocallyCreatedRelation(t *testing.T) {
	s, uuids := seedExportFixture(t)
	// Create a third issue and export, so the new relation we'll add
	// below can point at a target that already round-trips through the
	// fixture's YAML (otherwise the import would dangle it).
	r, err := s.GetRepoByUUID(uuids["repo"])
	if err != nil {
		t.Fatalf("get repo: %v", err)
	}
	iss3, err := s.CreateIssue(r.ID, nil, "Third issue", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("create iss3: %v", err)
	}
	// Pin iss3 timestamps so they're older than CURRENT_TIMESTAMP, so
	// LWW doesn't mis-fire on iss3 when re-imported.
	if _, err := s.DB.Exec(
		`UPDATE issues SET created_at = '2026-05-02 10:00:00', updated_at = '2026-05-03 10:00:00' WHERE id = ?`,
		iss3.ID,
	); err != nil {
		t.Fatalf("force ts iss3: %v", err)
	}
	dir := t.TempDir()
	if _, err := (&Engine{Store: s}).Export(context.Background(), dir); err != nil {
		t.Fatalf("export: %v", err)
	}
	// Add a relates_to edge from iss2 → iss3 after export — the YAML
	// for iss2 doesn't list it. iss2 has no relations in the fixture
	// so this is a from-scratch add that exercises the wipe path.
	iss2, err := s.GetIssueByUUID(uuids["iss2"])
	if err != nil {
		t.Fatalf("get iss2: %v", err)
	}
	if err := s.CreateRelation(iss2.ID, iss3.ID, model.RelRelatesTo); err != nil {
		t.Fatalf("create relation: %v", err)
	}
	if _, err := (&Engine{Store: s, Actor: "tester"}).Import(context.Background(), dir); err != nil {
		t.Fatalf("import: %v", err)
	}
	rels, err := s.ListIssueRelations(iss2.ID)
	if err != nil {
		t.Fatalf("list relations: %v", err)
	}
	found := false
	for _, r := range rels.Outgoing {
		if r.ToIssue == "MINI-3" && r.Type == model.RelRelatesTo {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("locally-created relation iss2→iss3 was wiped by sync re-import; surviving outgoing: %+v", rels.Outgoing)
	}
}

// TestImport_LWW_PreservesLocallyLinkedDoc pins BACI-142 for the
// document_links table. A worker calls LinkDocument after export;
// the next import sees stale YAML that doesn't list the new target.
// The local link must survive — pre-fix replaceDocLinksTx wiped it
// because the doc-link writer didn't bump documents.updated_at.
func TestImport_LWW_PreservesLocallyLinkedDoc(t *testing.T) {
	s, uuids := seedExportFixture(t)
	dir := t.TempDir()
	if _, err := (&Engine{Store: s}).Export(context.Background(), dir); err != nil {
		t.Fatalf("export: %v", err)
	}
	// Link the document to iss2 after export — the on-disk YAML
	// only lists the link to iss1.
	doc, err := s.GetDocumentByUUID(uuids["doc"], true)
	if err != nil {
		t.Fatalf("get doc: %v", err)
	}
	iss2, err := s.GetIssueByUUID(uuids["iss2"])
	if err != nil {
		t.Fatalf("get iss2: %v", err)
	}
	if _, err := s.LinkDocument(doc.ID, store.LinkTarget{IssueID: &iss2.ID}, "added after export"); err != nil {
		t.Fatalf("link doc: %v", err)
	}
	if _, err := (&Engine{Store: s, Actor: "tester"}).Import(context.Background(), dir); err != nil {
		t.Fatalf("import: %v", err)
	}
	links, err := s.ListDocumentLinks(doc.ID)
	if err != nil {
		t.Fatalf("list links: %v", err)
	}
	found := false
	for _, l := range links {
		if l.IssueID != nil && *l.IssueID == iss2.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("locally-created doc link to iss2 was wiped by sync re-import; surviving links: %+v", links)
	}
}

// TestImport_LWW_PreservesIssueUpdatedAtThroughPass2 pins the BACI-144
// re-stamp. Without it, pass 2's wipe-and-rewrite of issue side-data
// fires the schema triggers, which bump issues.updated_at to
// CURRENT_TIMESTAMP and silently clobber the LWW-correct value from
// the YAML. The test re-imports the fixture into a fresh DB and
// asserts that an issue's UpdatedAt matches the YAML exactly — not
// the import-time wall clock.
func TestImport_LWW_PreservesIssueUpdatedAtThroughPass2(t *testing.T) {
	a, uuids := seedExportFixture(t)
	dirA := t.TempDir()
	if _, err := (&Engine{Store: a}).Export(context.Background(), dirA); err != nil {
		t.Fatalf("export A: %v", err)
	}
	b, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open b: %v", err)
	}
	t.Cleanup(func() { b.Close() })
	if _, err := (&Engine{Store: b, Actor: "tester"}).Import(context.Background(), dirA); err != nil {
		t.Fatalf("import: %v", err)
	}
	// iss1 carries side-data (a PR, a tag, an outgoing relation) so
	// pass 2 actually fires the trigger bumps for it. The fixture
	// forces iss1.updated_at to '2026-05-09 14:22:00'.
	iss1, err := b.GetIssueByUUID(uuids["iss1"])
	if err != nil {
		t.Fatalf("get iss1: %v", err)
	}
	want := "2026-05-09T14:22:00Z"
	if got := iss1.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"); got != want {
		t.Fatalf("issue updated_at = %s, want %s (trigger CURRENT_TIMESTAMP overwrote YAML?)", got, want)
	}
}

// TestImport_LWW_PreservesDocumentUpdatedAtThroughPass2 mirrors the
// issue variant above for the documents table — pass 2's
// replaceDocLinksTx fires bump_document_updated_on_link_insert /
// _delete, which without the re-stamp would clobber the YAML's
// documents.updated_at.
func TestImport_LWW_PreservesDocumentUpdatedAtThroughPass2(t *testing.T) {
	a, uuids := seedExportFixture(t)
	dirA := t.TempDir()
	if _, err := (&Engine{Store: a}).Export(context.Background(), dirA); err != nil {
		t.Fatalf("export A: %v", err)
	}
	b, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open b: %v", err)
	}
	t.Cleanup(func() { b.Close() })
	if _, err := (&Engine{Store: b, Actor: "tester"}).Import(context.Background(), dirA); err != nil {
		t.Fatalf("import: %v", err)
	}
	// The fixture's doc has a link to iss1, so pass 2 fires the
	// link triggers. Forced timestamp is '2026-05-01 11:00:00'.
	doc, err := b.GetDocumentByUUID(uuids["doc"], true)
	if err != nil {
		t.Fatalf("get doc: %v", err)
	}
	want := "2026-05-01T11:00:00Z"
	if got := doc.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"); got != want {
		t.Fatalf("document updated_at = %s, want %s (trigger CURRENT_TIMESTAMP overwrote YAML?)", got, want)
	}
}

// TestImport_LegacyContentMD_JSONLDocument pins the no-migration
// fallback (BACI-102 Option 1): a .jsonl document synced by a
// pre-BACI-102 binary has its body on disk as content.md. A new-binary
// import must still read that body via the legacy fallback rather than
// treating it as an empty body.
func TestImport_LegacyContentMD_JSONLDocument(t *testing.T) {
	a, docUUID, body := seedJSONLDocStore(t)
	dirA := t.TempDir()
	if _, err := (&Engine{Store: a}).Export(context.Background(), dirA); err != nil {
		t.Fatalf("export A: %v", err)
	}
	// Simulate a pre-BACI-102 sync layout: the body lives at
	// content.md regardless of the .jsonl filename.
	docFolder := filepath.Join(dirA, "repos", "MINI", "docs", "bacio-transcript-BACI-12-agent-7.jsonl")
	if err := os.Rename(filepath.Join(docFolder, "content.jsonl"), filepath.Join(docFolder, "content.md")); err != nil {
		t.Fatalf("rename to legacy content.md: %v", err)
	}

	b, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open b: %v", err)
	}
	t.Cleanup(func() { b.Close() })
	if _, err := (&Engine{Store: b, Actor: "tester"}).Import(context.Background(), dirA); err != nil {
		t.Fatalf("import: %v", err)
	}
	d, err := b.GetDocumentByUUID(docUUID, true)
	if err != nil {
		t.Fatalf("get doc: %v", err)
	}
	if d.Content != body {
		t.Errorf("legacy-fallback body mismatch:\ngot:  %q\nwant: %q", d.Content, body)
	}
}

// TestImport_RoundTrip_SweepArchivedSurvivesNextImport reproduces the
// BACI-189 symptom: machine A's auto-archive sweep stamps a terminal-
// state issue, machine A exports, machine B imports + re-exports, A
// pulls B's export, A's archived_at must survive.
//
// Pre-fix the local sweep bumped archived_at but NOT updated_at, so on
// the next import the LWW gate saw `YAML.updated_at == local.
// updated_at` and fell into the wholesale-UPDATE branch with YAML's
// NULL archived_at — silently clobbering the sweep's work. With the
// BACI-189 schema trigger the sweep also bumps updated_at, so the
// LWW gate (in either direction) sees the local row as newer and
// preserves it.
func TestImport_RoundTrip_SweepArchivedSurvivesNextImport(t *testing.T) {
	a, uuids := seedExportFixture(t)

	// Back-date iss2's terminal_at past the retention window so the
	// sweep on A picks it up. iss2 is terminal (StateTodo by default
	// in the fixture, so move it to done first).
	if err := a.SetIssueState(idFromUUID(t, a, uuids["iss2"]), model.StateDone); err != nil {
		t.Fatalf("close iss2: %v", err)
	}
	if _, err := a.DB.Exec(
		`UPDATE issues SET terminal_at = datetime('now','-30 days'), updated_at = '2026-05-03 10:00:00' WHERE uuid = ?`,
		uuids["iss2"],
	); err != nil {
		t.Fatalf("backdate iss2: %v", err)
	}

	// Run the sweep on A — this is the writer that pre-fix forgot to
	// bump updated_at. Post-fix the schema trigger bumps it for us.
	res, err := a.ArchiveSweep(false)
	if err != nil {
		t.Fatalf("sweep on A: %v", err)
	}
	if res.IssuesArchived < 1 {
		t.Fatalf("sweep on A archived %d issues, want >= 1", res.IssuesArchived)
	}

	// Round-trip A → B → A. The classic BACI-189 symptom: A's
	// archived_at gets clobbered on the second import.
	dirA := t.TempDir()
	if _, err := (&Engine{Store: a}).Export(context.Background(), dirA); err != nil {
		t.Fatalf("export A: %v", err)
	}
	b, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open b: %v", err)
	}
	t.Cleanup(func() { b.Close() })
	if _, err := (&Engine{Store: b, Actor: "tester"}).Import(context.Background(), dirA); err != nil {
		t.Fatalf("import into B: %v", err)
	}
	// B re-exports and A re-imports. Pre-fix this is where the
	// archived_at gets silently cleared because A's stale updated_at
	// lets the YAML's NULL win.
	dirB := t.TempDir()
	if _, err := (&Engine{Store: b}).Export(context.Background(), dirB); err != nil {
		t.Fatalf("export B: %v", err)
	}
	if _, err := (&Engine{Store: a, Actor: "tester"}).Import(context.Background(), dirB); err != nil {
		t.Fatalf("re-import into A: %v", err)
	}

	gotA, err := a.GetIssueByUUID(uuids["iss2"])
	if err != nil {
		t.Fatalf("get iss2 on A after round-trip: %v", err)
	}
	if gotA.ArchivedAt == nil {
		t.Fatal("archived_at clobbered to NULL on second import — sweep did not bump updated_at, BACI-189 still reproduces")
	}
}

// TestImport_LWW_ExplicitUnarchiveStillWins is the inverse of the
// BACI-189 fix: a YAML row with `archived_at=null, updated_at=newer`
// (i.e. machine B explicitly unarchived) must still win over A's
// locally-archived row. The trigger is gated on
// `NEW.updated_at = OLD.updated_at`, so an importer UPDATE that writes
// BOTH columns leaves the YAML's NULL archived_at and YAML's newer
// updated_at intact.
func TestImport_LWW_ExplicitUnarchiveStillWins(t *testing.T) {
	a, uuids := seedExportFixture(t)

	// On A: archive iss2 with an OLD updated_at — this is the row B
	// will overwrite.
	if _, err := a.DB.Exec(
		`UPDATE issues SET archived_at = '2026-04-01 12:00:00', updated_at = '2026-04-01 12:00:00' WHERE uuid = ?`,
		uuids["iss2"],
	); err != nil {
		t.Fatalf("archive iss2 on A: %v", err)
	}

	// Stage YAML representing machine B's authoritative unarchive
	// (archived_at=null, updated_at=newer). Easiest path: build it by
	// exporting from a fresh store that mirrors A but with the
	// unarchive applied + a newer updated_at.
	b, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open b: %v", err)
	}
	t.Cleanup(func() { b.Close() })

	// Bootstrap B from A's current state.
	dirSeed := t.TempDir()
	if _, err := (&Engine{Store: a}).Export(context.Background(), dirSeed); err != nil {
		t.Fatalf("seed export from A: %v", err)
	}
	if _, err := (&Engine{Store: b, Actor: "tester"}).Import(context.Background(), dirSeed); err != nil {
		t.Fatalf("seed import into B: %v", err)
	}

	// On B: explicitly unarchive with a NEWER updated_at than A's
	// stale '2026-04-01' stamp. Both columns are set in one UPDATE
	// (importer pattern); the trigger's WHEN clause should keep the
	// YAML's updated_at byte-for-byte.
	if _, err := b.DB.Exec(
		`UPDATE issues SET archived_at = NULL, updated_at = '2026-05-20 09:00:00' WHERE uuid = ?`,
		uuids["iss2"],
	); err != nil {
		t.Fatalf("unarchive on B: %v", err)
	}

	dirB := t.TempDir()
	if _, err := (&Engine{Store: b}).Export(context.Background(), dirB); err != nil {
		t.Fatalf("export B: %v", err)
	}
	if _, err := (&Engine{Store: a, Actor: "tester"}).Import(context.Background(), dirB); err != nil {
		t.Fatalf("import B into A: %v", err)
	}

	gotA, err := a.GetIssueByUUID(uuids["iss2"])
	if err != nil {
		t.Fatalf("get iss2 on A: %v", err)
	}
	if gotA.ArchivedAt != nil {
		t.Errorf("LWW unarchive lost: A still has archived_at=%v, want NULL", gotA.ArchivedAt)
	}
}

// idFromUUID is a small test helper: look up the integer id of an
// issue by its uuid via the store's DB handle.
func idFromUUID(t *testing.T, s *store.Store, uuid string) int64 {
	t.Helper()
	var id int64
	if err := s.DB.QueryRow(`SELECT id FROM issues WHERE uuid = ?`, uuid).Scan(&id); err != nil {
		t.Fatalf("lookup id for %s: %v", uuid, err)
	}
	return id
}
