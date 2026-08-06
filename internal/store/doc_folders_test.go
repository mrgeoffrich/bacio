package store

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// seedFolderRepo is the scaffold for every doc-folder test: a store with
// one repo to hang a page tree off.
func seedFolderRepo(t *testing.T) (*Store, *model.Repo) {
	t.Helper()
	s := newTestStore(t)
	repo, err := s.CreateRepo("FLDR", "folder-test", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	return s, repo
}

// mustFolder creates a folder or fails the test.
func mustFolder(t *testing.T, s *Store, repoID int64, parent *int64, name string) *model.DocFolder {
	t.Helper()
	f, err := s.CreateDocFolder(repoID, parent, name)
	if err != nil {
		t.Fatalf("CreateDocFolder(%v, %q): %v", parent, name, err)
	}
	return f
}

// TestCreateDocFolderNested walks the happy path: root folders, nested
// children, auto-append positions, and the parent_id round-trip.
func TestCreateDocFolderNested(t *testing.T) {
	s, repo := seedFolderRepo(t)

	design := mustFolder(t, s, repo.ID, nil, "Design")
	ops := mustFolder(t, s, repo.ID, nil, "Ops")
	api := mustFolder(t, s, repo.ID, &design.ID, "API")
	auth := mustFolder(t, s, repo.ID, &api.ID, "Auth")

	if design.ParentID != nil {
		t.Errorf("root folder ParentID = %v, want nil", design.ParentID)
	}
	if api.ParentID == nil || *api.ParentID != design.ID {
		t.Errorf("API ParentID = %v, want %d", api.ParentID, design.ID)
	}

	// Positions auto-append inside each parent, independently.
	if design.Position != 0 || ops.Position != 1 {
		t.Errorf("root positions = %d, %d; want 0, 1", design.Position, ops.Position)
	}
	if api.Position != 0 {
		t.Errorf("API position = %d, want 0 (first child of Design)", api.Position)
	}
	if auth.Position != 0 {
		t.Errorf("Auth position = %d, want 0 (first child of API)", auth.Position)
	}

	// Every folder gets a uuid.
	for _, f := range []*model.DocFolder{design, ops, api, auth} {
		if f.UUID == "" {
			t.Errorf("folder %q has an empty uuid", f.Name)
		}
	}

	// ListDocFolders returns the whole tree flat; children read back via
	// ListDocFolderChildren one level at a time.
	all, err := s.ListDocFolders(repo.ID)
	if err != nil {
		t.Fatalf("ListDocFolders: %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("ListDocFolders = %d folders, want 4", len(all))
	}
	roots, err := s.ListDocFolderChildren(repo.ID, nil)
	if err != nil {
		t.Fatalf("ListDocFolderChildren(root): %v", err)
	}
	if len(roots) != 2 || roots[0].Name != "Design" || roots[1].Name != "Ops" {
		t.Fatalf("root children = %v, want [Design Ops] in position order", folderNames(roots))
	}
	kids, err := s.ListDocFolderChildren(repo.ID, &design.ID)
	if err != nil {
		t.Fatalf("ListDocFolderChildren(Design): %v", err)
	}
	if len(kids) != 1 || kids[0].Name != "API" {
		t.Fatalf("Design children = %v, want [API]", folderNames(kids))
	}

	// GetDocFolderByUUID is the sync-side lookup.
	got, err := s.GetDocFolderByUUID(auth.UUID)
	if err != nil {
		t.Fatalf("GetDocFolderByUUID: %v", err)
	}
	if got.ID != auth.ID {
		t.Errorf("GetDocFolderByUUID id = %d, want %d", got.ID, auth.ID)
	}
	if _, err := s.GetDocFolderByID(99999); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetDocFolderByID(missing) = %v, want ErrNotFound", err)
	}
}

// TestCreateDocFolderValidation proves creation runs through
// ValidateFolderName — in particular that '/' can never enter a folder
// name and forge a fake hierarchy.
func TestCreateDocFolderValidation(t *testing.T) {
	s, repo := seedFolderRepo(t)
	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"slash", "Design/API"},
		{"backslash", `Design\API`},
		{"dot", "."},
		{"dotdot", ".."},
		{"leading space", " Design"},
		{"trailing space", "Design "},
		{"control char", "Des\x01ign"},
		{"too long", strings.Repeat("x", 201)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.CreateDocFolder(repo.ID, nil, tc.in); err == nil {
				t.Fatalf("CreateDocFolder(%q) succeeded, want a validation error", tc.in)
			}
		})
	}
}

// TestDocFolderSiblingUniqueness covers BOTH partial unique indexes
// through the store API: uniq_doc_folders_root (parent_id IS NULL) and
// uniq_doc_folders_child. Both must surface as ErrDocFolderExists with a
// message naming the right location, and the same name under a different
// parent must stay legal.
func TestDocFolderSiblingUniqueness(t *testing.T) {
	s, repo := seedFolderRepo(t)
	design := mustFolder(t, s, repo.ID, nil, "Design")
	mustFolder(t, s, repo.ID, &design.ID, "API")

	// Root collision.
	_, err := s.CreateDocFolder(repo.ID, nil, "Design")
	if !errors.Is(err, ErrDocFolderExists) {
		t.Fatalf("duplicate root folder err = %v, want ErrDocFolderExists", err)
	}
	if !strings.Contains(err.Error(), "root") {
		t.Errorf("root collision message %q should mention the root", err)
	}

	// Child collision under the same parent.
	_, err = s.CreateDocFolder(repo.ID, &design.ID, "API")
	if !errors.Is(err, ErrDocFolderExists) {
		t.Fatalf("duplicate child folder err = %v, want ErrDocFolderExists", err)
	}
	if !strings.Contains(err.Error(), `"Design"`) {
		t.Errorf("child collision message %q should name the parent folder", err)
	}

	// The same name at a different level is fine — a root "API"
	// alongside the nested one.
	if _, err := s.CreateDocFolder(repo.ID, nil, "API"); err != nil {
		t.Errorf("root folder named API alongside a nested API: %v", err)
	}

	// And in a different repo entirely.
	other, err := s.CreateRepo("OTHR", "other", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create second repo: %v", err)
	}
	if _, err := s.CreateDocFolder(other.ID, nil, "Design"); err != nil {
		t.Errorf("root folder named Design in another repo: %v", err)
	}
}

// TestDocFolderCrossRepoParent locks the per-repo invariant: a folder
// may not be parented into another repo's tree, on create or on move.
func TestDocFolderCrossRepoParent(t *testing.T) {
	s, repo := seedFolderRepo(t)
	other, err := s.CreateRepo("OTHR", "other", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create second repo: %v", err)
	}
	mine := mustFolder(t, s, repo.ID, nil, "Design")
	theirs := mustFolder(t, s, other.ID, nil, "Theirs")

	if _, err := s.CreateDocFolder(repo.ID, &theirs.ID, "API"); !errors.Is(err, ErrDocFolderOtherRepo) {
		t.Errorf("cross-repo create err = %v, want ErrDocFolderOtherRepo", err)
	}
	if err := s.MoveDocFolder(mine.ID, &theirs.ID); !errors.Is(err, ErrDocFolderOtherRepo) {
		t.Errorf("cross-repo move err = %v, want ErrDocFolderOtherRepo", err)
	}
}

// TestMoveDocFolderCycleGuard is the store-boundary invariant that
// matters most: a folder may never be moved inside itself or below one
// of its own descendants, at any depth.
func TestMoveDocFolderCycleGuard(t *testing.T) {
	s, repo := seedFolderRepo(t)
	a := mustFolder(t, s, repo.ID, nil, "A")
	b := mustFolder(t, s, repo.ID, &a.ID, "B")
	c := mustFolder(t, s, repo.ID, &b.ID, "C")
	d := mustFolder(t, s, repo.ID, &c.ID, "D")
	// An unrelated root subtree the moves below must stay legal against.
	z := mustFolder(t, s, repo.ID, nil, "Z")

	cases := []struct {
		name   string
		move   int64
		parent *int64
	}{
		{"self as parent", a.ID, &a.ID},
		{"direct descendant", a.ID, &b.ID},
		{"deep descendant", a.ID, &d.ID},
		{"mid-tree self", b.ID, &b.ID},
		{"mid-tree deep descendant", b.ID, &d.ID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := s.MoveDocFolder(tc.move, tc.parent); !errors.Is(err, ErrDocFolderCycle) {
				t.Fatalf("MoveDocFolder err = %v, want ErrDocFolderCycle", err)
			}
		})
	}

	// The tree survived every refused move intact.
	path, err := s.ResolveFolderPath(d.ID)
	if err != nil {
		t.Fatalf("ResolveFolderPath after refused moves: %v", err)
	}
	if path.Display != "A/B/C/D" {
		t.Fatalf("tree mutated by a refused move: %q, want A/B/C/D", path.Display)
	}

	// A legal move still works: hang B (and C, D with it) under Z.
	if err := s.MoveDocFolder(b.ID, &z.ID); err != nil {
		t.Fatalf("legal MoveDocFolder: %v", err)
	}
	path, err = s.ResolveFolderPath(d.ID)
	if err != nil {
		t.Fatalf("ResolveFolderPath after move: %v", err)
	}
	if path.Display != "Z/B/C/D" {
		t.Errorf("after move, D path = %q, want Z/B/C/D", path.Display)
	}

	// And a move back to the root.
	if err := s.MoveDocFolder(b.ID, nil); err != nil {
		t.Fatalf("MoveDocFolder to root: %v", err)
	}
	moved, err := s.GetDocFolderByID(b.ID)
	if err != nil {
		t.Fatalf("GetDocFolderByID: %v", err)
	}
	if moved.ParentID != nil {
		t.Errorf("after move to root, ParentID = %v, want nil", moved.ParentID)
	}
}

// TestMoveDocFolderNoOpAndCollision covers the two remaining move
// branches: re-parenting to the parent it already has is a no-op, and a
// name already taken at the destination is refused.
func TestMoveDocFolderNoOpAndCollision(t *testing.T) {
	s, repo := seedFolderRepo(t)
	design := mustFolder(t, s, repo.ID, nil, "Design")
	ops := mustFolder(t, s, repo.ID, nil, "Ops")
	api := mustFolder(t, s, repo.ID, &design.ID, "API")
	mustFolder(t, s, repo.ID, &ops.ID, "API")

	// No-op move: same parent, position untouched.
	if err := s.MoveDocFolder(api.ID, &design.ID); err != nil {
		t.Fatalf("no-op MoveDocFolder: %v", err)
	}
	// Destination already has an "API".
	if err := s.MoveDocFolder(api.ID, &ops.ID); !errors.Is(err, ErrDocFolderExists) {
		t.Fatalf("colliding MoveDocFolder err = %v, want ErrDocFolderExists", err)
	}
	// Unknown folder / unknown destination.
	if err := s.MoveDocFolder(99999, nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("MoveDocFolder(missing) = %v, want ErrNotFound", err)
	}
	missing := int64(99999)
	if err := s.MoveDocFolder(api.ID, &missing); !errors.Is(err, ErrNotFound) {
		t.Errorf("MoveDocFolder(→missing) = %v, want ErrNotFound", err)
	}
}

// TestDocFolderDepthCap proves MaxDocFolderDepth is enforced on both
// create (one level too far down) and move (a subtree that would push
// the destination past the cap).
func TestDocFolderDepthCap(t *testing.T) {
	s, repo := seedFolderRepo(t)

	// Build a chain exactly MaxDocFolderDepth deep. The root folder is
	// depth 1, so this is the deepest legal tree.
	var parent *int64
	var deepest *model.DocFolder
	for i := 1; i <= MaxDocFolderDepth; i++ {
		f := mustFolder(t, s, repo.ID, parent, fmt.Sprintf("L%d", i))
		deepest = f
		parent = &f.ID
	}
	path, err := s.ResolveFolderPath(deepest.ID)
	if err != nil {
		t.Fatalf("ResolveFolderPath: %v", err)
	}
	if path.Depth != MaxDocFolderDepth {
		t.Fatalf("deepest folder Depth = %d, want %d", path.Depth, MaxDocFolderDepth)
	}

	// One more level is refused.
	_, err = s.CreateDocFolder(repo.ID, &deepest.ID, "TooDeep")
	if !errors.Is(err, ErrDocFolderTooDeep) {
		t.Fatalf("create past the cap err = %v, want ErrDocFolderTooDeep", err)
	}

	// A 2-tall subtree at the root can't be moved under a folder at
	// depth MaxDocFolderDepth-1: 15 + 2 = 17 > 16.
	sub := mustFolder(t, s, repo.ID, nil, "Sub")
	mustFolder(t, s, repo.ID, &sub.ID, "SubChild")
	chain, err := s.ResolveFolderPath(deepest.ID)
	if err != nil {
		t.Fatalf("ResolveFolderPath: %v", err)
	}
	penultimate := chain.Chain[MaxDocFolderDepth-2] // depth 15
	if err := s.MoveDocFolder(sub.ID, &penultimate.ID); !errors.Is(err, ErrDocFolderTooDeep) {
		t.Fatalf("move past the cap err = %v, want ErrDocFolderTooDeep", err)
	}

	// The same subtree fits one level higher: 14 + 2 = 16.
	fits := chain.Chain[MaxDocFolderDepth-3] // depth 14
	if err := s.MoveDocFolder(sub.ID, &fits.ID); err != nil {
		t.Fatalf("move that exactly fills the cap: %v", err)
	}
}

// TestRenameDocFolder covers the rename path, including the collision
// error and the fact that a rename re-derives the display path of the
// entire subtree with a single UPDATE.
func TestRenameDocFolder(t *testing.T) {
	s, repo := seedFolderRepo(t)
	design := mustFolder(t, s, repo.ID, nil, "Design")
	api := mustFolder(t, s, repo.ID, &design.ID, "API")
	auth := mustFolder(t, s, repo.ID, &api.ID, "Auth")
	mustFolder(t, s, repo.ID, nil, "Ops")

	if err := s.RenameDocFolder(design.ID, "Architecture"); err != nil {
		t.Fatalf("RenameDocFolder: %v", err)
	}
	path, err := s.ResolveFolderPath(auth.ID)
	if err != nil {
		t.Fatalf("ResolveFolderPath: %v", err)
	}
	if path.Display != "Architecture/API/Auth" {
		t.Errorf("after rename, path = %q, want Architecture/API/Auth", path.Display)
	}

	// Colliding with an existing sibling is refused.
	if err := s.RenameDocFolder(design.ID, "Ops"); !errors.Is(err, ErrDocFolderExists) {
		t.Errorf("colliding rename err = %v, want ErrDocFolderExists", err)
	}
	// Renaming to its own current name is a legal no-op.
	if err := s.RenameDocFolder(design.ID, "Architecture"); err != nil {
		t.Errorf("self rename: %v", err)
	}
	// Validation still applies.
	if err := s.RenameDocFolder(design.ID, "A/B"); err == nil {
		t.Error("rename to a name containing '/' succeeded, want a validation error")
	}
	if err := s.RenameDocFolder(99999, "Nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("RenameDocFolder(missing) = %v, want ErrNotFound", err)
	}
}

// TestDeleteDocFolderCascades locks the two FK actions the tree depends
// on: deleting a folder takes its whole subtree of folders with it
// (ON DELETE CASCADE on parent_id) but RE-ROOTS every page inside that
// subtree instead of deleting it (ON DELETE SET NULL on
// documents.folder_id). Deleting a folder must never lose content.
func TestDeleteDocFolderCascades(t *testing.T) {
	s, repo := seedFolderRepo(t)
	design := mustFolder(t, s, repo.ID, nil, "Design")
	api := mustFolder(t, s, repo.ID, &design.ID, "API")
	auth := mustFolder(t, s, repo.ID, &api.ID, "Auth")
	keep := mustFolder(t, s, repo.ID, nil, "Ops")

	top := mustDoc(t, s, repo.ID, "top.md")
	deep := mustDoc(t, s, repo.ID, "deep.md")
	elsewhere := mustDoc(t, s, repo.ID, "elsewhere.md")
	if err := s.SetDocumentFolder(top.ID, &design.ID, 0); err != nil {
		t.Fatalf("SetDocumentFolder top: %v", err)
	}
	if err := s.SetDocumentFolder(deep.ID, &auth.ID, 0); err != nil {
		t.Fatalf("SetDocumentFolder deep: %v", err)
	}
	if err := s.SetDocumentFolder(elsewhere.ID, &keep.ID, 0); err != nil {
		t.Fatalf("SetDocumentFolder elsewhere: %v", err)
	}

	if err := s.DeleteDocFolder(design.ID); err != nil {
		t.Fatalf("DeleteDocFolder: %v", err)
	}

	// The whole subtree of folders is gone.
	for _, f := range []*model.DocFolder{design, api, auth} {
		if _, err := s.GetDocFolderByID(f.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("folder %q survived the cascade (err = %v)", f.Name, err)
		}
	}
	// The unrelated folder is untouched.
	if _, err := s.GetDocFolderByID(keep.ID); err != nil {
		t.Errorf("unrelated folder deleted: %v", err)
	}

	// Both pages survive, re-rooted; the unrelated page keeps its folder.
	for _, id := range []int64{top.ID, deep.ID} {
		d, err := s.GetDocumentByID(id, false)
		if err != nil {
			t.Fatalf("document %d lost to a folder delete: %v", id, err)
		}
		if d.FolderID != nil {
			t.Errorf("document %q FolderID = %v after cascade, want nil (re-rooted)", d.Filename, d.FolderID)
		}
	}
	still, err := s.GetDocumentByID(elsewhere.ID, false)
	if err != nil {
		t.Fatalf("unrelated document: %v", err)
	}
	if still.FolderID == nil || *still.FolderID != keep.ID {
		t.Errorf("unrelated document FolderID = %v, want %d", still.FolderID, keep.ID)
	}

	// Delete-by-uuid is the sync-side twin.
	if err := s.DeleteDocFolderByUUID(keep.UUID); err != nil {
		t.Fatalf("DeleteDocFolderByUUID: %v", err)
	}
	if err := s.DeleteDocFolderByUUID(keep.UUID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second DeleteDocFolderByUUID = %v, want ErrNotFound", err)
	}
	if err := s.DeleteDocFolder(99999); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteDocFolder(missing) = %v, want ErrNotFound", err)
	}
}

// TestFolderPathRoundTrip is the ResolveFolderPath ⇄ FolderByPath
// contract: every folder's derived display path resolves back to that
// exact folder, and the empty path means the tree ROOT (nil, nil) rather
// than an error.
func TestFolderPathRoundTrip(t *testing.T) {
	s, repo := seedFolderRepo(t)
	design := mustFolder(t, s, repo.ID, nil, "Design")
	api := mustFolder(t, s, repo.ID, &design.ID, "API")
	auth := mustFolder(t, s, repo.ID, &api.ID, "Auth")
	// A same-named folder in a different branch proves resolution is
	// segment-by-segment rather than a global name lookup.
	ops := mustFolder(t, s, repo.ID, nil, "Ops")
	opsAPI := mustFolder(t, s, repo.ID, &ops.ID, "API")

	cases := []struct {
		folder  *model.DocFolder
		display string
		depth   int
	}{
		{design, "Design", 1},
		{api, "Design/API", 2},
		{auth, "Design/API/Auth", 3},
		{ops, "Ops", 1},
		{opsAPI, "Ops/API", 2},
	}
	for _, tc := range cases {
		t.Run(tc.display, func(t *testing.T) {
			path, err := s.ResolveFolderPath(tc.folder.ID)
			if err != nil {
				t.Fatalf("ResolveFolderPath: %v", err)
			}
			if path.Display != tc.display {
				t.Fatalf("Display = %q, want %q", path.Display, tc.display)
			}
			if path.Depth != tc.depth || len(path.Chain) != tc.depth || len(path.Segments) != tc.depth {
				t.Fatalf("Depth/Chain/Segments = %d/%d/%d, want all %d",
					path.Depth, len(path.Chain), len(path.Segments), tc.depth)
			}
			if path.ID != tc.folder.ID || path.UUID != tc.folder.UUID {
				t.Fatalf("path identity = (%d, %s), want (%d, %s)", path.ID, path.UUID, tc.folder.ID, tc.folder.UUID)
			}
			// Chain is root-first and inclusive.
			if path.Chain[len(path.Chain)-1].ID != tc.folder.ID {
				t.Fatalf("Chain does not end at the folder itself")
			}
			if path.Chain[0].ParentID != nil {
				t.Fatalf("Chain does not start at a root folder")
			}
			back, err := s.FolderByPath(repo.ID, tc.display)
			if err != nil {
				t.Fatalf("FolderByPath(%q): %v", tc.display, err)
			}
			if back == nil || back.ID != tc.folder.ID {
				t.Fatalf("FolderByPath(%q) = %v, want folder %d", tc.display, back, tc.folder.ID)
			}
		})
	}

	// The empty path (and its slashy equivalents) means the tree root.
	for _, p := range []string{"", "/", "//"} {
		got, err := s.FolderByPath(repo.ID, p)
		if err != nil {
			t.Errorf("FolderByPath(%q) err = %v, want nil", p, err)
		}
		if got != nil {
			t.Errorf("FolderByPath(%q) = %v, want nil (the tree root)", p, got)
		}
	}
	// Stray separators around a real path are tolerated.
	if got, err := s.FolderByPath(repo.ID, "/Design//API/"); err != nil || got == nil || got.ID != api.ID {
		t.Errorf("FolderByPath with stray separators = (%v, %v), want folder %d", got, err, api.ID)
	}
	// A missing segment is ErrNotFound, named.
	_, err := s.FolderByPath(repo.ID, "Design/Nope/Auth")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("FolderByPath(missing segment) = %v, want ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "Nope") {
		t.Errorf("missing-segment error %q should name the failing segment", err)
	}
	// A path that exists in another branch does not resolve here.
	if _, err := s.FolderByPath(repo.ID, "Ops/API/Auth"); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-branch path resolved: %v", err)
	}
	if _, err := s.ResolveFolderPath(99999); !errors.Is(err, ErrNotFound) {
		t.Errorf("ResolveFolderPath(missing) = %v, want ErrNotFound", err)
	}
}

// TestEnsureFolderPath covers the mkdir -p helper: missing segments are
// created, existing ones are reused, and the whole chain lands together.
func TestEnsureFolderPath(t *testing.T) {
	s, repo := seedFolderRepo(t)

	deep, err := s.EnsureFolderPath(repo.ID, "Design/API/Auth")
	if err != nil {
		t.Fatalf("EnsureFolderPath: %v", err)
	}
	path, err := s.ResolveFolderPath(deep.ID)
	if err != nil {
		t.Fatalf("ResolveFolderPath: %v", err)
	}
	if path.Display != "Design/API/Auth" {
		t.Fatalf("created path = %q, want Design/API/Auth", path.Display)
	}
	all, err := s.ListDocFolders(repo.ID)
	if err != nil {
		t.Fatalf("ListDocFolders: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("EnsureFolderPath created %d folders, want 3", len(all))
	}

	// Re-running is idempotent and returns the same leaf.
	again, err := s.EnsureFolderPath(repo.ID, "Design/API/Auth")
	if err != nil {
		t.Fatalf("second EnsureFolderPath: %v", err)
	}
	if again.ID != deep.ID {
		t.Errorf("second EnsureFolderPath made a new folder (%d vs %d)", again.ID, deep.ID)
	}
	// A partially-existing path only creates the missing tail.
	tail, err := s.EnsureFolderPath(repo.ID, "Design/API/Tokens")
	if err != nil {
		t.Fatalf("EnsureFolderPath tail: %v", err)
	}
	if tail.ParentID == nil || *tail.ParentID != *deep.ParentID {
		t.Errorf("Tokens parent = %v, want the existing API folder %v", tail.ParentID, deep.ParentID)
	}
	// The empty path is the root.
	if got, err := s.EnsureFolderPath(repo.ID, ""); err != nil || got != nil {
		t.Errorf("EnsureFolderPath(\"\") = (%v, %v), want (nil, nil)", got, err)
	}
	// Validation and the depth cap both apply.
	if _, err := s.EnsureFolderPath(repo.ID, "Design/ /Auth"); err == nil {
		t.Error("EnsureFolderPath with a whitespace segment succeeded, want a validation error")
	}
	tooDeep := strings.Repeat("x/", MaxDocFolderDepth+1)
	if _, err := s.EnsureFolderPath(repo.ID, tooDeep); !errors.Is(err, ErrDocFolderTooDeep) {
		t.Errorf("EnsureFolderPath(over-deep) = %v, want ErrDocFolderTooDeep", err)
	}
}

// TestReorderDocFolder covers the sibling-order sort key.
func TestReorderDocFolder(t *testing.T) {
	s, repo := seedFolderRepo(t)
	a := mustFolder(t, s, repo.ID, nil, "Alpha")
	b := mustFolder(t, s, repo.ID, nil, "Beta")
	c := mustFolder(t, s, repo.ID, nil, "Gamma")

	// Auto-append order is creation order.
	if got := folderNames(mustList(t, s, repo.ID, nil)); !equalStrings(got, []string{"Alpha", "Beta", "Gamma"}) {
		t.Fatalf("initial order = %v", got)
	}
	// Pull Gamma to the front, push Alpha to the back.
	if err := s.ReorderDocFolder(c.ID, 0); err != nil {
		t.Fatalf("ReorderDocFolder: %v", err)
	}
	if err := s.ReorderDocFolder(a.ID, 2); err != nil {
		t.Fatalf("ReorderDocFolder: %v", err)
	}
	if err := s.ReorderDocFolder(b.ID, 1); err != nil {
		t.Fatalf("ReorderDocFolder: %v", err)
	}
	if got := folderNames(mustList(t, s, repo.ID, nil)); !equalStrings(got, []string{"Gamma", "Beta", "Alpha"}) {
		t.Fatalf("reordered = %v, want [Gamma Beta Alpha]", got)
	}
	// Positions are a sort key, not an index: a tie falls back to name.
	for _, f := range []*model.DocFolder{a, b, c} {
		if err := s.ReorderDocFolder(f.ID, 0); err != nil {
			t.Fatalf("ReorderDocFolder: %v", err)
		}
	}
	if got := folderNames(mustList(t, s, repo.ID, nil)); !equalStrings(got, []string{"Alpha", "Beta", "Gamma"}) {
		t.Fatalf("all-tied order = %v, want the name tie-break", got)
	}
	if err := s.ReorderDocFolder(a.ID, -1); err == nil {
		t.Error("negative position accepted, want an error")
	}
	if err := s.ReorderDocFolder(99999, 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("ReorderDocFolder(missing) = %v, want ErrNotFound", err)
	}
}

// TestCreateDocFolderFromSync covers the import-side constructor:
// caller-supplied uuid, explicit position, and the same validation and
// uniqueness guards as the interactive path.
func TestCreateDocFolderFromSync(t *testing.T) {
	s, repo := seedFolderRepo(t)
	root, err := s.CreateDocFolderFromSync(repo.ID, "folder-uuid-root", nil, "Design", 3, nullTime(), nullTime())
	if err != nil {
		t.Fatalf("CreateDocFolderFromSync: %v", err)
	}
	if root.UUID != "folder-uuid-root" || root.Position != 3 {
		t.Fatalf("synced folder = (%q, pos %d), want (folder-uuid-root, pos 3)", root.UUID, root.Position)
	}
	if _, err := s.CreateDocFolderFromSync(repo.ID, "", nil, "X", 0, nullTime(), nullTime()); err == nil {
		t.Error("CreateDocFolderFromSync with an empty uuid succeeded, want an error")
	}
	if _, err := s.CreateDocFolderFromSync(repo.ID, "folder-uuid-2", nil, "Design", 0, nullTime(), nullTime()); !errors.Is(err, ErrDocFolderExists) {
		t.Error("CreateDocFolderFromSync ignored the sibling-name uniqueness guard")
	}
}

func folderNames(fs []*model.DocFolder) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Name
	}
	return out
}

func mustList(t *testing.T, s *Store, repoID int64, parent *int64) []*model.DocFolder {
	t.Helper()
	fs, err := s.ListDocFolderChildren(repoID, parent)
	if err != nil {
		t.Fatalf("ListDocFolderChildren: %v", err)
	}
	return fs
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
