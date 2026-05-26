// BACI-68 store-layer tests for the archive lifecycle: the
// per-entity Set*Archived methods plus the three-pass auto-sweep.
//
// Per the user's "Use SQLite datetime('now') and skip age-based unit
// tests" call on BACI-68, the legacy sweep tests deliberately exercise
// the structural cases (already-archived skip, parent-cascade
// semantics, childless features ignored, zero-link docs ignored) and
// archive the issue children by hand. BACI-162 added direct age-window
// coverage on top — using `terminal_at` back-dating via SQL — so the
// configurable retention window + auto_enabled toggle are now locked
// in alongside the cascade structural cases.
package store

import (
	"testing"
	"time"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestArchiveSweepIntervalPinned locks the BACI-175 cadence. The value
// is load-bearing — see the leading doc comment in archive.go for why
// 5m and not 1h. A future bump should fail this test loudly so the
// change is deliberate, not a drive-by edit.
func TestArchiveSweepIntervalPinned(t *testing.T) {
	if ArchiveSweepInterval != 5*time.Minute {
		t.Fatalf("ArchiveSweepInterval = %v, want 5m (see BACI-175 — change deliberately)", ArchiveSweepInterval)
	}
}

func TestSetIssueArchivedIdempotentAndSticky(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("TST", "test", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	iss, err := s.CreateIssue(repo.ID, nil, "issue", "", model.StateDone, nil)
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	if iss.ArchivedAt != nil {
		t.Fatal("fresh issue must not be archived")
	}

	// Archive — flips archived_at.
	if err := s.SetIssueArchived(iss.ID, true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	got, _ := s.GetIssueByID(iss.ID)
	if got.ArchivedAt == nil {
		t.Fatal("after SetIssueArchived(true), archived_at must be set")
	}
	first := *got.ArchivedAt

	// Idempotent — re-archiving leaves the original timestamp alone.
	// The partial-WHERE in SetIssueArchived (AND archived_at IS NULL)
	// is what makes this work; a naive UPDATE would refresh the
	// timestamp and confuse "when did this get archived?".
	if err := s.SetIssueArchived(iss.ID, true); err != nil {
		t.Fatalf("re-archive: %v", err)
	}
	got, _ = s.GetIssueByID(iss.ID)
	if got.ArchivedAt == nil || !got.ArchivedAt.Equal(first) {
		t.Fatalf("re-archive must preserve the original timestamp; was %v, now %v", first, got.ArchivedAt)
	}

	// Sticky — reopening to a non-terminal state does NOT clear
	// archived_at. Reopening is a separate user action from
	// unarchiving, per the design call in the brief.
	if err := s.SetIssueState(iss.ID, model.StateTodo); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, _ = s.GetIssueByID(iss.ID)
	if got.ArchivedAt == nil {
		t.Fatal("reopening must NOT auto-unarchive (sticky)")
	}

	// Unarchive — clears archived_at.
	if err := s.SetIssueArchived(iss.ID, false); err != nil {
		t.Fatalf("unarchive: %v", err)
	}
	got, _ = s.GetIssueByID(iss.ID)
	if got.ArchivedAt != nil {
		t.Fatal("after SetIssueArchived(false), archived_at must be NULL")
	}
}

func TestListIssuesHidesArchivedByDefault(t *testing.T) {
	s := newTestStore(t)
	repo, _ := s.CreateRepo("TST", "test", t.TempDir(), "")
	live, _ := s.CreateIssue(repo.ID, nil, "live", "", model.StateTodo, nil)
	hidden, _ := s.CreateIssue(repo.ID, nil, "hidden", "", model.StateDone, nil)
	if err := s.SetIssueArchived(hidden.ID, true); err != nil {
		t.Fatalf("archive: %v", err)
	}

	got, err := s.ListIssues(IssueFilter{RepoID: &repo.ID})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].ID != live.ID {
		t.Fatalf("default list must hide archived; got %d rows", len(got))
	}

	got, err = s.ListIssues(IssueFilter{RepoID: &repo.ID, IncludeArchived: true})
	if err != nil {
		t.Fatalf("list inclusive: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("IncludeArchived list must return both; got %d", len(got))
	}
}

func TestListFeaturesHidesArchivedByDefault(t *testing.T) {
	s := newTestStore(t)
	repo, _ := s.CreateRepo("TST", "test", t.TempDir(), "")
	live, _ := s.CreateFeature(repo.ID, "live", "Live", "", "", "")
	hidden, _ := s.CreateFeature(repo.ID, "hidden", "Hidden", "", "", "")
	if err := s.SetFeatureArchived(hidden.ID, true); err != nil {
		t.Fatalf("archive: %v", err)
	}

	got, err := s.ListFeatures(repo.ID, false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].ID != live.ID {
		t.Fatalf("default features list must hide archived; got %d rows", len(got))
	}

	got, err = s.ListFeaturesFiltered(FeatureFilter{RepoID: repo.ID, IncludeArchived: true})
	if err != nil {
		t.Fatalf("list inclusive: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("IncludeArchived features list must return both; got %d", len(got))
	}
}

func TestListDocumentsHidesArchivedByDefault(t *testing.T) {
	s := newTestStore(t)
	repo, _ := s.CreateRepo("TST", "test", t.TempDir(), "")
	live, _ := s.CreateDocument(repo.ID, "live.md", model.DocTypeArchitecture, "live", "")
	hidden, _ := s.CreateDocument(repo.ID, "hidden.md", model.DocTypeArchitecture, "hidden", "")
	if err := s.SetDocumentArchived(hidden.ID, true); err != nil {
		t.Fatalf("archive: %v", err)
	}

	got, err := s.ListDocuments(DocumentFilter{RepoID: repo.ID})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].ID != live.ID {
		t.Fatalf("default docs list must hide archived; got %d rows", len(got))
	}

	got, err = s.ListDocuments(DocumentFilter{RepoID: repo.ID, IncludeArchived: true})
	if err != nil {
		t.Fatalf("list inclusive: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("IncludeArchived docs list must return both; got %d", len(got))
	}
}

// TestArchiveSweepFeatureCascade locks in the rule "a feature is
// auto-archived when every child issue is archived, and the feature
// had at least one child". The age-window predicate on the issues
// pass is skipped (per the user's call); we archive the issues by
// hand to set up the cascade input.
func TestArchiveSweepFeatureCascade(t *testing.T) {
	s := newTestStore(t)
	repo, _ := s.CreateRepo("TST", "test", t.TempDir(), "")

	// Feature A: every child issue is archived → feature gets archived.
	featA, _ := s.CreateFeature(repo.ID, "a", "A", "", "", "")
	a1, _ := s.CreateIssue(repo.ID, &featA.ID, "a1", "", model.StateDone, nil)
	a2, _ := s.CreateIssue(repo.ID, &featA.ID, "a2", "", model.StateDone, nil)
	_ = s.SetIssueArchived(a1.ID, true)
	_ = s.SetIssueArchived(a2.ID, true)

	// Feature B: one live child → feature stays live.
	featB, _ := s.CreateFeature(repo.ID, "b", "B", "", "", "")
	b1, _ := s.CreateIssue(repo.ID, &featB.ID, "b1", "", model.StateDone, nil)
	_, _ = s.CreateIssue(repo.ID, &featB.ID, "b2", "", model.StateTodo, nil)
	_ = s.SetIssueArchived(b1.ID, true)

	// Feature C: zero children → never auto-archived (the brief
	// excludes childless features explicitly).
	featC, _ := s.CreateFeature(repo.ID, "c", "C", "", "", "")

	res, err := s.ArchiveSweep(false)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.FeaturesArchived != 1 {
		t.Fatalf("FeaturesArchived = %d, want 1 (feature A only)", res.FeaturesArchived)
	}
	gotA, _ := s.GetFeatureByID(featA.ID)
	if gotA.ArchivedAt == nil {
		t.Fatal("feature A must be archived after sweep")
	}
	gotB, _ := s.GetFeatureByID(featB.ID)
	if gotB.ArchivedAt != nil {
		t.Fatal("feature B has a live child — must NOT be archived")
	}
	gotC, _ := s.GetFeatureByID(featC.ID)
	if gotC.ArchivedAt != nil {
		t.Fatal("feature C is childless — must NOT be archived")
	}
}

// TestArchiveSweepDocumentCascade locks in the rule "a doc is
// auto-archived when every linked parent is archived, and the doc had
// at least one link". Docs with zero links are explicitly NOT
// orphans — they were never attached, so the sweep leaves them alone.
// Covers multi-linked docs across issue + feature parents.
func TestArchiveSweepDocumentCascade(t *testing.T) {
	s := newTestStore(t)
	repo, _ := s.CreateRepo("TST", "test", t.TempDir(), "")

	feat, _ := s.CreateFeature(repo.ID, "f", "F", "", "", "")
	iss, _ := s.CreateIssue(repo.ID, &feat.ID, "i", "", model.StateDone, nil)

	// Doc 1: linked only to an archived issue → archived.
	doc1, _ := s.CreateDocument(repo.ID, "doc1.md", model.DocTypeArchitecture, "", "")
	if _, err := s.LinkDocument(doc1.ID, LinkTarget{IssueID: &iss.ID}, ""); err != nil {
		t.Fatalf("link doc1: %v", err)
	}
	_ = s.SetIssueArchived(iss.ID, true)

	// Doc 2: linked to both the archived feature and archived issue
	// → archived (every linked parent is archived).
	doc2, _ := s.CreateDocument(repo.ID, "doc2.md", model.DocTypeArchitecture, "", "")
	if _, err := s.LinkDocument(doc2.ID, LinkTarget{IssueID: &iss.ID}, ""); err != nil {
		t.Fatalf("link doc2/issue: %v", err)
	}
	if _, err := s.LinkDocument(doc2.ID, LinkTarget{FeatureID: &feat.ID}, ""); err != nil {
		t.Fatalf("link doc2/feature: %v", err)
	}
	_ = s.SetFeatureArchived(feat.ID, true)

	// Doc 3: linked to a separate live issue → stays live.
	liveIss, _ := s.CreateIssue(repo.ID, nil, "live", "", model.StateTodo, nil)
	doc3, _ := s.CreateDocument(repo.ID, "doc3.md", model.DocTypeArchitecture, "", "")
	if _, err := s.LinkDocument(doc3.ID, LinkTarget{IssueID: &liveIss.ID}, ""); err != nil {
		t.Fatalf("link doc3: %v", err)
	}

	// Doc 4: zero links → NOT an orphan; never auto-archived even
	// though it has no live parents. The user must archive these by
	// hand.
	doc4, _ := s.CreateDocument(repo.ID, "doc4.md", model.DocTypeArchitecture, "", "")

	res, err := s.ArchiveSweep(false)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.DocumentsArchived != 2 {
		t.Fatalf("DocumentsArchived = %d, want 2 (doc1 + doc2)", res.DocumentsArchived)
	}

	d1, _ := s.GetDocumentByID(doc1.ID, false)
	if d1.ArchivedAt == nil {
		t.Fatal("doc1 must be archived (single linked parent is archived)")
	}
	d2, _ := s.GetDocumentByID(doc2.ID, false)
	if d2.ArchivedAt == nil {
		t.Fatal("doc2 must be archived (every linked parent is archived)")
	}
	d3, _ := s.GetDocumentByID(doc3.ID, false)
	if d3.ArchivedAt != nil {
		t.Fatal("doc3 has a live parent — must NOT be archived")
	}
	d4, _ := s.GetDocumentByID(doc4.ID, false)
	if d4.ArchivedAt != nil {
		t.Fatal("doc4 has zero links — sweep must leave it alone")
	}
}

// TestArchiveSweepIdempotent locks in the "safe to run on a quiet DB"
// contract — a second sweep over already-archived rows is a no-op.
func TestArchiveSweepIdempotent(t *testing.T) {
	s := newTestStore(t)
	repo, _ := s.CreateRepo("TST", "test", t.TempDir(), "")
	feat, _ := s.CreateFeature(repo.ID, "f", "F", "", "", "")
	iss, _ := s.CreateIssue(repo.ID, &feat.ID, "i", "", model.StateDone, nil)
	_ = s.SetIssueArchived(iss.ID, true)

	// First sweep archives the feature.
	r1, err := s.ArchiveSweep(false)
	if err != nil {
		t.Fatalf("sweep 1: %v", err)
	}
	if r1.FeaturesArchived != 1 {
		t.Fatalf("first sweep FeaturesArchived = %d, want 1", r1.FeaturesArchived)
	}

	// Second sweep is a no-op.
	r2, err := s.ArchiveSweep(false)
	if err != nil {
		t.Fatalf("sweep 2: %v", err)
	}
	if r2.Total() != 0 {
		t.Fatalf("second sweep must be a no-op; got %+v", r2)
	}
}

// TestArchiveSweepRespectsAutoEnabledFalse — BACI-162. When the
// archive.auto_enabled toggle is false the issue pass is skipped
// entirely, even for terminal-state rows whose terminal_at is
// comfortably past the retention window. Flipping the toggle back on
// (without changing the row) lets the next sweep archive it.
func TestArchiveSweepRespectsAutoEnabledFalse(t *testing.T) {
	s := newTestStore(t)
	repo, _ := s.CreateRepo("TST", "test", t.TempDir(), "")
	iss, _ := s.CreateIssue(repo.ID, nil, "ancient", "", model.StateDone, nil)
	// Back-date terminal_at past any sane retention window.
	if _, err := s.DB.Exec(
		`UPDATE issues SET terminal_at = datetime('now','-365 days') WHERE id = ?`,
		iss.ID,
	); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	// Disable auto-archive and sweep: the row should stay live.
	if err := s.SetArchiveAutoEnabled(false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	res, err := s.ArchiveSweep(false)
	if err != nil {
		t.Fatalf("sweep (disabled): %v", err)
	}
	if res.IssuesArchived != 0 {
		t.Fatalf("auto_enabled=false: IssuesArchived = %d, want 0", res.IssuesArchived)
	}
	got, _ := s.GetIssueByID(iss.ID)
	if got.ArchivedAt != nil {
		t.Fatal("auto_enabled=false must NOT archive an issue, even if terminal_at is ancient")
	}

	// Re-enable and re-sweep: the row should now archive.
	if err := s.SetArchiveAutoEnabled(true); err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	res, err = s.ArchiveSweep(false)
	if err != nil {
		t.Fatalf("sweep (enabled): %v", err)
	}
	if res.IssuesArchived != 1 {
		t.Fatalf("auto_enabled=true: IssuesArchived = %d, want 1", res.IssuesArchived)
	}
}

// TestArchiveSweepRespectsRetentionDays — BACI-162. The configured
// retention window drives the cutoff: an issue whose terminal_at is
// older than retention_days archives; one whose terminal_at is
// fresher than retention_days stays live.
func TestArchiveSweepRespectsRetentionDays(t *testing.T) {
	s := newTestStore(t)
	repo, _ := s.CreateRepo("TST", "test", t.TempDir(), "")

	// Two terminal-state issues. One ancient, one fresh.
	old, _ := s.CreateIssue(repo.ID, nil, "old", "", model.StateDone, nil)
	young, _ := s.CreateIssue(repo.ID, nil, "young", "", model.StateDone, nil)
	if _, err := s.DB.Exec(
		`UPDATE issues SET terminal_at = datetime('now','-2 days') WHERE id = ?`, old.ID,
	); err != nil {
		t.Fatalf("backdate old: %v", err)
	}
	if _, err := s.DB.Exec(
		`UPDATE issues SET terminal_at = datetime('now','-30 minutes') WHERE id = ?`, young.ID,
	); err != nil {
		t.Fatalf("backdate young: %v", err)
	}

	// Retention=1 day: old (2 days) is eligible; young (30 min) is not.
	if err := s.SetArchiveRetentionDays(1); err != nil {
		t.Fatalf("set retention: %v", err)
	}
	res, err := s.ArchiveSweep(false)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.IssuesArchived != 1 {
		t.Fatalf("retention=1: IssuesArchived = %d, want 1", res.IssuesArchived)
	}
	gotOld, _ := s.GetIssueByID(old.ID)
	if gotOld.ArchivedAt == nil {
		t.Fatal("old (terminal_at=-2d) must archive at retention=1")
	}
	gotYoung, _ := s.GetIssueByID(young.ID)
	if gotYoung.ArchivedAt != nil {
		t.Fatal("young (terminal_at=-30m) must NOT archive at retention=1")
	}
}

// TestArchiveSweepUsesTerminalAtNotUpdatedAt — BACI-162 / BACI-138.
// The retention clock anchors on terminal_at, not updated_at. Bumping
// updated_at via an unrelated edit (here, an updated_at SQL write
// directly; the equivalent end-user action would be a tag add) on a
// closed issue must NOT extend the retention window.
func TestArchiveSweepUsesTerminalAtNotUpdatedAt(t *testing.T) {
	s := newTestStore(t)
	repo, _ := s.CreateRepo("TST", "test", t.TempDir(), "")
	iss, _ := s.CreateIssue(repo.ID, nil, "edited recently", "", model.StateDone, nil)
	// terminal_at well past the retention window; updated_at fresh
	// (simulates a tag edit on a long-closed issue).
	if _, err := s.DB.Exec(
		`UPDATE issues SET terminal_at = datetime('now','-30 days'), updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		iss.ID,
	); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := s.SetArchiveRetentionDays(7); err != nil {
		t.Fatalf("set retention: %v", err)
	}
	res, err := s.ArchiveSweep(false)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.IssuesArchived != 1 {
		t.Fatalf("IssuesArchived = %d, want 1 (terminal_at is the clock anchor; updated_at must be ignored)", res.IssuesArchived)
	}
}

// TestArchiveSweepCascadeRunsEvenWhenAutoOff — BACI-162. Disabling
// archive.auto_enabled skips ONLY the issue pass. The feature +
// document cascade passes still run, so a manually-archived issue
// continues to cascade to its parent feature and linked docs.
func TestArchiveSweepCascadeRunsEvenWhenAutoOff(t *testing.T) {
	s := newTestStore(t)
	repo, _ := s.CreateRepo("TST", "test", t.TempDir(), "")
	feat, _ := s.CreateFeature(repo.ID, "f", "F", "", "", "")
	iss, _ := s.CreateIssue(repo.ID, &feat.ID, "manual", "", model.StateDone, nil)
	doc, _ := s.CreateDocument(repo.ID, "d.md", model.DocTypeArchitecture, "", "")
	if _, err := s.LinkDocument(doc.ID, LinkTarget{IssueID: &iss.ID}, ""); err != nil {
		t.Fatalf("link doc: %v", err)
	}

	// Manually archive the child issue.
	if err := s.SetIssueArchived(iss.ID, true); err != nil {
		t.Fatalf("archive issue: %v", err)
	}

	// Disable auto-archive — the cascade passes should still run.
	if err := s.SetArchiveAutoEnabled(false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	res, err := s.ArchiveSweep(false)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.IssuesArchived != 0 {
		t.Fatalf("issue pass must be skipped: IssuesArchived = %d, want 0", res.IssuesArchived)
	}
	if res.FeaturesArchived != 1 {
		t.Fatalf("cascade must still run: FeaturesArchived = %d, want 1", res.FeaturesArchived)
	}
	if res.DocumentsArchived != 1 {
		t.Fatalf("cascade must still run: DocumentsArchived = %d, want 1", res.DocumentsArchived)
	}
}

// TestArchiveSweepBumpsUpdatedAt — BACI-189. The bump_*_updated_on_
// archive_change schema triggers must fire on every row the sweep
// stamps, so the next sync round-trip's LWW gate sees the local
// archive as newer than the remote YAML. Without the bump, sync
// imports the unchanged YAML over the local row and silently clears
// archived_at — symptom: the sweep reports the same N rows archived
// every tick and never converges.
func TestArchiveSweepBumpsUpdatedAt(t *testing.T) {
	s := newTestStore(t)
	repo, _ := s.CreateRepo("TST", "test", t.TempDir(), "")

	feat, _ := s.CreateFeature(repo.ID, "f", "F", "", "", "")
	iss, _ := s.CreateIssue(repo.ID, &feat.ID, "old", "", model.StateDone, nil)
	doc, _ := s.CreateDocument(repo.ID, "d.md", model.DocTypeArchitecture, "", "")
	if _, err := s.LinkDocument(doc.ID, LinkTarget{IssueID: &iss.ID}, ""); err != nil {
		t.Fatalf("link doc: %v", err)
	}

	// Back-date terminal_at past the default retention window so the
	// issue pass picks it up; back-date updated_at on every entity so
	// we can detect the trigger's bump as a forward jump.
	if _, err := s.DB.Exec(
		`UPDATE issues SET terminal_at = datetime('now','-30 days'), updated_at = '2026-01-01 00:00:00' WHERE id = ?`,
		iss.ID,
	); err != nil {
		t.Fatalf("backdate issue: %v", err)
	}
	if _, err := s.DB.Exec(
		`UPDATE features SET updated_at = '2026-01-01 00:00:00' WHERE id = ?`, feat.ID,
	); err != nil {
		t.Fatalf("backdate feature: %v", err)
	}
	if _, err := s.DB.Exec(
		`UPDATE documents SET updated_at = '2026-01-01 00:00:00' WHERE id = ?`, doc.ID,
	); err != nil {
		t.Fatalf("backdate document: %v", err)
	}

	sweepStart := time.Now().UTC().Add(-1 * time.Second)
	res, err := s.ArchiveSweep(false)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.IssuesArchived != 1 || res.FeaturesArchived != 1 || res.DocumentsArchived != 1 {
		t.Fatalf("expected to archive 1 of each kind; got %+v", res)
	}

	gotIss, _ := s.GetIssueByID(iss.ID)
	if !gotIss.UpdatedAt.After(sweepStart) {
		t.Errorf("issue updated_at = %v, want > sweepStart %v (trigger did not fire?)", gotIss.UpdatedAt, sweepStart)
	}
	gotFeat, _ := s.GetFeatureByID(feat.ID)
	if !gotFeat.UpdatedAt.After(sweepStart) {
		t.Errorf("feature updated_at = %v, want > sweepStart %v", gotFeat.UpdatedAt, sweepStart)
	}
	gotDoc, _ := s.GetDocumentByID(doc.ID, false)
	if !gotDoc.UpdatedAt.After(sweepStart) {
		t.Errorf("document updated_at = %v, want > sweepStart %v", gotDoc.UpdatedAt, sweepStart)
	}
}

// TestSetIssueArchivedBumpsUpdatedAtOnEveryFlip — BACI-189 manual-verb
// path. Each SetIssueArchived(true/false) must bump updated_at via
// the schema trigger so a manual `bacio issue archive` followed by a
// sync round-trip preserves the local archive state. Same property
// holds for features and documents — covered by the sibling tests
// below.
func TestSetIssueArchivedBumpsUpdatedAtOnEveryFlip(t *testing.T) {
	s := newTestStore(t)
	repo, _ := s.CreateRepo("TST", "test", t.TempDir(), "")
	iss, _ := s.CreateIssue(repo.ID, nil, "i", "", model.StateDone, nil)

	// Back-date updated_at so the post-archive bump is observably
	// later. CURRENT_TIMESTAMP is second-precision, so a same-second
	// flip can read as equal — back-dating sidesteps that.
	if _, err := s.DB.Exec(
		`UPDATE issues SET updated_at = '2026-01-01 00:00:00' WHERE id = ?`, iss.ID,
	); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	before, _ := s.GetIssueByID(iss.ID)

	if err := s.SetIssueArchived(iss.ID, true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	afterArchive, _ := s.GetIssueByID(iss.ID)
	if !afterArchive.UpdatedAt.After(before.UpdatedAt) {
		t.Errorf("archive: updated_at did not advance; before=%v after=%v", before.UpdatedAt, afterArchive.UpdatedAt)
	}

	// Back-date again so the unarchive flip's bump is detectable.
	if _, err := s.DB.Exec(
		`UPDATE issues SET updated_at = '2026-01-01 00:00:00' WHERE id = ?`, iss.ID,
	); err != nil {
		t.Fatalf("backdate 2: %v", err)
	}
	beforeUnarchive, _ := s.GetIssueByID(iss.ID)

	if err := s.SetIssueArchived(iss.ID, false); err != nil {
		t.Fatalf("unarchive: %v", err)
	}
	afterUnarchive, _ := s.GetIssueByID(iss.ID)
	if !afterUnarchive.UpdatedAt.After(beforeUnarchive.UpdatedAt) {
		t.Errorf("unarchive: updated_at did not advance; before=%v after=%v", beforeUnarchive.UpdatedAt, afterUnarchive.UpdatedAt)
	}
}

// TestSetFeatureArchivedBumpsUpdatedAtOnEveryFlip — BACI-189 manual
// feature archive path.
func TestSetFeatureArchivedBumpsUpdatedAtOnEveryFlip(t *testing.T) {
	s := newTestStore(t)
	repo, _ := s.CreateRepo("TST", "test", t.TempDir(), "")
	feat, _ := s.CreateFeature(repo.ID, "f", "F", "", "", "")

	if _, err := s.DB.Exec(
		`UPDATE features SET updated_at = '2026-01-01 00:00:00' WHERE id = ?`, feat.ID,
	); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	before, _ := s.GetFeatureByID(feat.ID)

	if err := s.SetFeatureArchived(feat.ID, true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	afterArchive, _ := s.GetFeatureByID(feat.ID)
	if !afterArchive.UpdatedAt.After(before.UpdatedAt) {
		t.Errorf("archive: updated_at did not advance; before=%v after=%v", before.UpdatedAt, afterArchive.UpdatedAt)
	}

	if _, err := s.DB.Exec(
		`UPDATE features SET updated_at = '2026-01-01 00:00:00' WHERE id = ?`, feat.ID,
	); err != nil {
		t.Fatalf("backdate 2: %v", err)
	}
	beforeUnarchive, _ := s.GetFeatureByID(feat.ID)

	if err := s.SetFeatureArchived(feat.ID, false); err != nil {
		t.Fatalf("unarchive: %v", err)
	}
	afterUnarchive, _ := s.GetFeatureByID(feat.ID)
	if !afterUnarchive.UpdatedAt.After(beforeUnarchive.UpdatedAt) {
		t.Errorf("unarchive: updated_at did not advance; before=%v after=%v", beforeUnarchive.UpdatedAt, afterUnarchive.UpdatedAt)
	}
}

// TestSetDocumentArchivedBumpsUpdatedAtOnEveryFlip — BACI-189 manual
// document archive path.
func TestSetDocumentArchivedBumpsUpdatedAtOnEveryFlip(t *testing.T) {
	s := newTestStore(t)
	repo, _ := s.CreateRepo("TST", "test", t.TempDir(), "")
	doc, _ := s.CreateDocument(repo.ID, "d.md", model.DocTypeArchitecture, "", "")

	if _, err := s.DB.Exec(
		`UPDATE documents SET updated_at = '2026-01-01 00:00:00' WHERE id = ?`, doc.ID,
	); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	before, _ := s.GetDocumentByID(doc.ID, false)

	if err := s.SetDocumentArchived(doc.ID, true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	afterArchive, _ := s.GetDocumentByID(doc.ID, false)
	if !afterArchive.UpdatedAt.After(before.UpdatedAt) {
		t.Errorf("archive: updated_at did not advance; before=%v after=%v", before.UpdatedAt, afterArchive.UpdatedAt)
	}

	if _, err := s.DB.Exec(
		`UPDATE documents SET updated_at = '2026-01-01 00:00:00' WHERE id = ?`, doc.ID,
	); err != nil {
		t.Fatalf("backdate 2: %v", err)
	}
	beforeUnarchive, _ := s.GetDocumentByID(doc.ID, false)

	if err := s.SetDocumentArchived(doc.ID, false); err != nil {
		t.Fatalf("unarchive: %v", err)
	}
	afterUnarchive, _ := s.GetDocumentByID(doc.ID, false)
	if !afterUnarchive.UpdatedAt.After(beforeUnarchive.UpdatedAt) {
		t.Errorf("unarchive: updated_at did not advance; before=%v after=%v", beforeUnarchive.UpdatedAt, afterUnarchive.UpdatedAt)
	}
}

// TestArchiveTriggerNoOpsWhenCallerSetsUpdatedAt — BACI-189. The sync
// importer's pass-1 wholesale UPDATE writes archived_at AND updated_at
// in the same statement. The trigger's `WHEN NEW.updated_at = OLD.
// updated_at` guard must keep the trigger from clobbering the
// LWW-authoritative timestamp. Without this guard, an imported row's
// updated_at would jump to CURRENT_TIMESTAMP on every import and
// permanently lose LWW orientation.
func TestArchiveTriggerNoOpsWhenCallerSetsUpdatedAt(t *testing.T) {
	s := newTestStore(t)
	repo, _ := s.CreateRepo("TST", "test", t.TempDir(), "")
	iss, _ := s.CreateIssue(repo.ID, nil, "i", "", model.StateDone, nil)

	// Mimic the importer: one UPDATE setting both columns to YAML's
	// values. The trigger's WHEN clause should be false (NEW.updated_at
	// differs from OLD.updated_at) so the trigger does NOT fire and
	// the YAML's updated_at survives byte-for-byte.
	authoritative := "2026-05-15 09:00:00"
	if _, err := s.DB.Exec(
		`UPDATE issues SET archived_at = '2026-05-10 12:00:00', updated_at = ? WHERE id = ?`,
		authoritative, iss.ID,
	); err != nil {
		t.Fatalf("authoritative update: %v", err)
	}

	got, _ := s.GetIssueByID(iss.ID)
	wantTS, _ := time.Parse("2006-01-02 15:04:05", authoritative)
	if !got.UpdatedAt.UTC().Equal(wantTS) {
		t.Errorf("updated_at = %v, want %v (trigger fired despite caller setting updated_at)", got.UpdatedAt.UTC(), wantTS)
	}
}

// TestArchiveSweepDryRunPreviewsCounts pins the dry-run contract:
// counts reflect what a real sweep would archive (issue age pass +
// feature cascade + doc cascade), but no row is actually modified.
// A subsequent wet sweep must archive the same rows the dry-run
// previewed.
func TestArchiveSweepDryRunPreviewsCounts(t *testing.T) {
	s := newTestStore(t)
	repo, _ := s.CreateRepo("TST", "test", t.TempDir(), "")

	// One feature with one child issue past retention, plus a doc
	// linked to that issue. A real sweep would archive all three; a
	// dry-run must report 1/1/1 without touching the rows.
	feat, _ := s.CreateFeature(repo.ID, "f", "F", "", "", "")
	iss, _ := s.CreateIssue(repo.ID, &feat.ID, "ancient", "", model.StateDone, nil)
	if _, err := s.DB.Exec(
		`UPDATE issues SET terminal_at = datetime('now','-30 days') WHERE id = ?`, iss.ID,
	); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	doc, _ := s.CreateDocument(repo.ID, "d.md", model.DocTypeArchitecture, "", "")
	if _, err := s.LinkDocument(doc.ID, LinkTarget{IssueID: &iss.ID}, ""); err != nil {
		t.Fatalf("link doc: %v", err)
	}

	preview, err := s.ArchiveSweep(true)
	if err != nil {
		t.Fatalf("dry-run sweep: %v", err)
	}
	if preview.IssuesArchived != 1 || preview.FeaturesArchived != 1 || preview.DocumentsArchived != 1 {
		t.Fatalf("dry-run preview = %+v, want 1/1/1", preview)
	}

	// Nothing should have actually been archived.
	gotIss, _ := s.GetIssueByID(iss.ID)
	if gotIss.ArchivedAt != nil {
		t.Fatal("dry-run must leave issue.archived_at unset")
	}
	gotFeat, _ := s.GetFeatureByID(feat.ID)
	if gotFeat.ArchivedAt != nil {
		t.Fatal("dry-run must leave feature.archived_at unset")
	}
	gotDoc, _ := s.GetDocumentByID(doc.ID, false)
	if gotDoc.ArchivedAt != nil {
		t.Fatal("dry-run must leave document.archived_at unset")
	}

	// A wet sweep over the same state archives the same rows.
	wet, err := s.ArchiveSweep(false)
	if err != nil {
		t.Fatalf("wet sweep: %v", err)
	}
	if wet != preview {
		t.Fatalf("wet sweep counts %+v != dry-run preview %+v", wet, preview)
	}
}

func TestDisplayShowArchivedDefaultsFalse(t *testing.T) {
	s := newTestStore(t)
	v, err := s.GetDisplayShowArchived()
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if v {
		t.Fatal("default must be false")
	}
	if err := s.SetDisplayShowArchived(true); err != nil {
		t.Fatalf("set: %v", err)
	}
	v, _ = s.GetDisplayShowArchived()
	if !v {
		t.Fatal("after set(true), must read true")
	}
}
