package store

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// mustDoc creates a plain user-docs page or fails the test.
func mustDoc(t *testing.T, s *Store, repoID int64, filename string) *model.Document {
	t.Helper()
	d, err := s.CreateDocument(repoID, filename, model.DocTypeUserDocs, "body of "+filename, "")
	if err != nil {
		t.Fatalf("CreateDocument(%q): %v", filename, err)
	}
	return d
}

// nullTime is the "leave the column to its DEFAULT" argument for the
// *FromSync constructors.
func nullTime() sql.NullTime { return sql.NullTime{} }

// TestSetDocumentFolder covers the placement write: root ⇄ folder moves,
// the per-repo guard, the position sort key, and the deliberate choice
// NOT to bump documents.updated_at (folder membership is not part of
// doc.yaml, so a re-filing must not rewrite the synced document or win
// the LWW race against a real remote content edit).
func TestSetDocumentFolder(t *testing.T) {
	s, repo := seedFolderRepo(t)
	design := mustFolder(t, s, repo.ID, nil, "Design")
	ops := mustFolder(t, s, repo.ID, nil, "Ops")
	doc := mustDoc(t, s, repo.ID, "spec.md")

	// Every pre-pivot document starts at the tree root with position 0 —
	// no backfill, no migration.
	if doc.FolderID != nil || doc.FolderPosition != 0 {
		t.Fatalf("new document = (folder %v, pos %d), want (nil, 0)", doc.FolderID, doc.FolderPosition)
	}

	if err := s.SetDocumentFolder(doc.ID, &design.ID, 2); err != nil {
		t.Fatalf("SetDocumentFolder: %v", err)
	}
	got, err := s.GetDocumentByID(doc.ID, false)
	if err != nil {
		t.Fatalf("GetDocumentByID: %v", err)
	}
	if got.FolderID == nil || *got.FolderID != design.ID {
		t.Fatalf("FolderID = %v, want %d", got.FolderID, design.ID)
	}
	if got.FolderPosition != 2 {
		t.Fatalf("FolderPosition = %d, want 2 (written literally)", got.FolderPosition)
	}
	if !got.UpdatedAt.Equal(doc.UpdatedAt) {
		t.Errorf("documents.updated_at moved on a folder move (%v → %v); folder membership is not part of doc.yaml",
			doc.UpdatedAt, got.UpdatedAt)
	}

	// Move between folders, then back to the root.
	if err := s.SetDocumentFolder(doc.ID, &ops.ID, 0); err != nil {
		t.Fatalf("SetDocumentFolder(→Ops): %v", err)
	}
	if err := s.SetDocumentFolder(doc.ID, nil, 0); err != nil {
		t.Fatalf("SetDocumentFolder(→root): %v", err)
	}
	got, err = s.GetDocumentByID(doc.ID, false)
	if err != nil {
		t.Fatalf("GetDocumentByID: %v", err)
	}
	if got.FolderID != nil {
		t.Fatalf("FolderID = %v after a move to the root, want nil", got.FolderID)
	}

	// Guards.
	if err := s.SetDocumentFolder(doc.ID, &design.ID, -1); err == nil {
		t.Error("negative position accepted, want an error")
	}
	if err := s.SetDocumentFolder(99999, nil, 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetDocumentFolder(missing doc) = %v, want ErrNotFound", err)
	}
	missing := int64(99999)
	if err := s.SetDocumentFolder(doc.ID, &missing, 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetDocumentFolder(missing folder) = %v, want ErrNotFound", err)
	}
	other, err := s.CreateRepo("OTHR", "other", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create second repo: %v", err)
	}
	theirs := mustFolder(t, s, other.ID, nil, "Theirs")
	if err := s.SetDocumentFolder(doc.ID, &theirs.ID, 0); !errors.Is(err, ErrDocFolderOtherRepo) {
		t.Errorf("cross-repo SetDocumentFolder = %v, want ErrDocFolderOtherRepo", err)
	}
}

// TestDocumentFilterFolderScope is the three-way folder constraint the
// zero value can't express with a bare *int64: unconstrained, ROOT ONLY
// (folder_id IS NULL), and one specific folder.
func TestDocumentFilterFolderScope(t *testing.T) {
	s, repo := seedFolderRepo(t)
	design := mustFolder(t, s, repo.ID, nil, "Design")
	ops := mustFolder(t, s, repo.ID, nil, "Ops")

	atRoot := mustDoc(t, s, repo.ID, "readme.md")
	inDesign := mustDoc(t, s, repo.ID, "spec.md")
	inOps := mustDoc(t, s, repo.ID, "runbook.md")
	if err := s.SetDocumentFolder(inDesign.ID, &design.ID, 0); err != nil {
		t.Fatalf("SetDocumentFolder: %v", err)
	}
	if err := s.SetDocumentFolder(inOps.ID, &ops.ID, 0); err != nil {
		t.Fatalf("SetDocumentFolder: %v", err)
	}

	cases := []struct {
		name  string
		scope DocFolderScope
		want  []string
	}{
		{"zero value is unconstrained", DocFolderScope{}, []string{"readme.md", "runbook.md", "spec.md"}},
		{"AnyFolder", AnyFolder(), []string{"readme.md", "runbook.md", "spec.md"}},
		{"RootFolder", RootFolder(), []string{"readme.md"}},
		{"InFolder(Design)", InFolder(design.ID), []string{"spec.md"}},
		{"InFolder(Ops)", InFolder(ops.ID), []string{"runbook.md"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			docs, err := s.ListDocuments(DocumentFilter{RepoID: repo.ID, Folder: tc.scope})
			if err != nil {
				t.Fatalf("ListDocuments: %v", err)
			}
			if got := docNames(docs); !equalStrings(got, tc.want) {
				t.Fatalf("filenames = %v, want %v", got, tc.want)
			}
		})
	}

	// The scope is not recursive: a page in a child folder is not "in"
	// the parent.
	nested := mustFolder(t, s, repo.ID, &design.ID, "API")
	deep := mustDoc(t, s, repo.ID, "auth.md")
	if err := s.SetDocumentFolder(deep.ID, &nested.ID, 0); err != nil {
		t.Fatalf("SetDocumentFolder: %v", err)
	}
	docs, err := s.ListDocuments(DocumentFilter{RepoID: repo.ID, Folder: InFolder(design.ID)})
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if got := docNames(docs); !equalStrings(got, []string{"spec.md"}) {
		t.Fatalf("InFolder is recursive: got %v, want [spec.md]", got)
	}

	// Introspection accessors the transports use to echo the filter back.
	if AnyFolder().Constrained() {
		t.Error("AnyFolder().Constrained() = true")
	}
	if !RootFolder().Constrained() || RootFolder().FolderID() != nil {
		t.Error("RootFolder() should be constrained with a nil folder id")
	}
	if fid := InFolder(design.ID).FolderID(); fid == nil || *fid != design.ID {
		t.Errorf("InFolder(%d).FolderID() = %v", design.ID, fid)
	}

	// The folder constraint composes with the existing ones.
	arch := mustDoc(t, s, repo.ID, "arch.md")
	if err := s.SetDocumentFolder(arch.ID, &design.ID, 0); err != nil {
		t.Fatalf("SetDocumentFolder: %v", err)
	}
	if err := s.SetDocumentArchived(arch.ID, true); err != nil {
		t.Fatalf("SetDocumentArchived: %v", err)
	}
	docs, err = s.ListDocuments(DocumentFilter{RepoID: repo.ID, Folder: InFolder(design.ID)})
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if got := docNames(docs); !equalStrings(got, []string{"spec.md"}) {
		t.Fatalf("archived page still listed: %v", got)
	}
	docs, err = s.ListDocuments(DocumentFilter{RepoID: repo.ID, Folder: InFolder(design.ID), IncludeArchived: true})
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if got := docNames(docs); !equalStrings(got, []string{"arch.md", "spec.md"}) {
		t.Fatalf("with archived = %v, want [arch.md spec.md]", got)
	}
	_ = atRoot
}

// TestListDocumentsFolderOrder locks the read order inside one folder:
// folder_position first, filename as the tie-break — so an untouched
// folder (every position still 0, which is every pre-pivot document)
// keeps exactly the alphabetical order this list has always had.
func TestListDocumentsFolderOrder(t *testing.T) {
	s, repo := seedFolderRepo(t)
	design := mustFolder(t, s, repo.ID, nil, "Design")

	zulu := mustDoc(t, s, repo.ID, "zulu.md")
	alpha := mustDoc(t, s, repo.ID, "alpha.md")
	mike := mustDoc(t, s, repo.ID, "mike.md")

	// Untouched: pure alphabetical, unchanged from the pre-pivot list.
	docs, err := s.ListDocuments(DocumentFilter{RepoID: repo.ID})
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if got := docNames(docs); !equalStrings(got, []string{"alpha.md", "mike.md", "zulu.md"}) {
		t.Fatalf("default order = %v, want alphabetical", got)
	}

	// Filed with an explicit manual order, position wins.
	for i, d := range []*model.Document{zulu, mike, alpha} {
		if err := s.SetDocumentFolder(d.ID, &design.ID, i); err != nil {
			t.Fatalf("SetDocumentFolder: %v", err)
		}
	}
	docs, err = s.ListDocuments(DocumentFilter{RepoID: repo.ID, Folder: InFolder(design.ID)})
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if got := docNames(docs); !equalStrings(got, []string{"zulu.md", "mike.md", "alpha.md"}) {
		t.Fatalf("manual order = %v, want [zulu.md mike.md alpha.md]", got)
	}
}

// TestDocumentFolderScanRoundTrip proves folder_id / folder_position ride
// on every document read path — the lean list scan, the with-content
// single-row scan, and the uuid lookup the sync importer uses.
func TestDocumentFolderScanRoundTrip(t *testing.T) {
	s, repo := seedFolderRepo(t)
	design := mustFolder(t, s, repo.ID, nil, "Design")
	doc := mustDoc(t, s, repo.ID, "spec.md")
	if err := s.SetDocumentFolder(doc.ID, &design.ID, 7); err != nil {
		t.Fatalf("SetDocumentFolder: %v", err)
	}

	withContent, err := s.GetDocumentByID(doc.ID, true)
	if err != nil {
		t.Fatalf("GetDocumentByID(withContent): %v", err)
	}
	if withContent.Content == "" {
		t.Fatal("content column lost from the with-content scan")
	}
	byName, err := s.GetDocumentByFilename(repo.ID, "spec.md", false)
	if err != nil {
		t.Fatalf("GetDocumentByFilename: %v", err)
	}
	byUUID, err := s.GetDocumentByUUID(doc.UUID, true)
	if err != nil {
		t.Fatalf("GetDocumentByUUID: %v", err)
	}
	listed, err := s.ListDocuments(DocumentFilter{RepoID: repo.ID})
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("ListDocuments = %d rows, want 1", len(listed))
	}
	for name, d := range map[string]*model.Document{
		"withContent": withContent,
		"byFilename":  byName,
		"byUUID":      byUUID,
		"listed":      listed[0],
	} {
		if d.FolderID == nil || *d.FolderID != design.ID {
			t.Errorf("%s FolderID = %v, want %d", name, d.FolderID, design.ID)
		}
		if d.FolderPosition != 7 {
			t.Errorf("%s FolderPosition = %d, want 7", name, d.FolderPosition)
		}
	}
	// The BACI-204 snippet projection still rides on the end of the lean
	// column set after the two new columns were spliced in.
	if listed[0].Snippet != "body of spec.md" {
		t.Errorf("snippet = %q, want %q", listed[0].Snippet, "body of spec.md")
	}
}

func docNames(docs []*model.Document) []string {
	out := make([]string, len(docs))
	for i, d := range docs {
		out[i] = d.Filename
	}
	return out
}
