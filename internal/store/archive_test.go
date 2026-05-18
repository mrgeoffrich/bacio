// BACI-68 store-layer tests for the archive lifecycle: the
// per-entity Set*Archived methods plus the three-pass auto-sweep.
//
// Per the user's "Use SQLite datetime('now') and skip age-based unit
// tests" call on BACI-68, the sweep tests deliberately exercise the
// structural cases (already-archived skip, parent-cascade semantics,
// childless features ignored, zero-link docs ignored) but skip the
// "issue older than 4 days" age window — that path relies on
// datetime('now') which is hard to fake without a clock injection.
// The bulk of the auto-sweep's logic lives in the parent-cascade
// passes anyway; the age-window predicate is a one-line SQL filter.
package store

import (
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

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
	live, _ := s.CreateFeature(repo.ID, "live", "Live", "")
	hidden, _ := s.CreateFeature(repo.ID, "hidden", "Hidden", "")
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
	featA, _ := s.CreateFeature(repo.ID, "a", "A", "")
	a1, _ := s.CreateIssue(repo.ID, &featA.ID, "a1", "", model.StateDone, nil)
	a2, _ := s.CreateIssue(repo.ID, &featA.ID, "a2", "", model.StateDone, nil)
	_ = s.SetIssueArchived(a1.ID, true)
	_ = s.SetIssueArchived(a2.ID, true)

	// Feature B: one live child → feature stays live.
	featB, _ := s.CreateFeature(repo.ID, "b", "B", "")
	b1, _ := s.CreateIssue(repo.ID, &featB.ID, "b1", "", model.StateDone, nil)
	_, _ = s.CreateIssue(repo.ID, &featB.ID, "b2", "", model.StateTodo, nil)
	_ = s.SetIssueArchived(b1.ID, true)

	// Feature C: zero children → never auto-archived (the brief
	// excludes childless features explicitly).
	featC, _ := s.CreateFeature(repo.ID, "c", "C", "")

	res, err := s.ArchiveSweep()
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

	feat, _ := s.CreateFeature(repo.ID, "f", "F", "")
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

	res, err := s.ArchiveSweep()
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
	feat, _ := s.CreateFeature(repo.ID, "f", "F", "")
	iss, _ := s.CreateIssue(repo.ID, &feat.ID, "i", "", model.StateDone, nil)
	_ = s.SetIssueArchived(iss.ID, true)

	// First sweep archives the feature.
	r1, err := s.ArchiveSweep()
	if err != nil {
		t.Fatalf("sweep 1: %v", err)
	}
	if r1.FeaturesArchived != 1 {
		t.Fatalf("first sweep FeaturesArchived = %d, want 1", r1.FeaturesArchived)
	}

	// Second sweep is a no-op.
	r2, err := s.ArchiveSweep()
	if err != nil {
		t.Fatalf("sweep 2: %v", err)
	}
	if r2.Total() != 0 {
		t.Fatalf("second sweep must be a no-op; got %+v", r2)
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
