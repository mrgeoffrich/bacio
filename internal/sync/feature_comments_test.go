package sync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/store"
)

// TestExportFeatureComments pins the BACI-124 export path — feature
// comments emit a YAML + MD pair under the feature folder's comments/
// subdir, with the same on-disk schema as issue comments.
func TestExportFeatureComments(t *testing.T) {
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	r, err := s.CreateRepo("MINI", "bacio", "/local/path", "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	feat, err := s.CreateFeature(r.ID, "auth", "Auth", "Notes.\n", "", "")
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}
	cres, err := s.CreateFeatureComment(feat.ID, "alice", "handoff body\n", "")
	if err != nil {
		t.Fatalf("create feature comment: %v", err)
	}
	cm := cres.Comment
	if _, err := s.DB.Exec(`UPDATE feature_comments SET created_at = '2026-05-01 10:00:00' WHERE id = ?`, cm.ID); err != nil {
		t.Fatalf("force ts: %v", err)
	}

	eng := &Engine{Store: s}
	dir := t.TempDir()
	res, err := eng.Export(context.Background(), dir)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if res.FeatureComments != 1 {
		t.Fatalf("res.FeatureComments = %d, want 1", res.FeatureComments)
	}

	// The comment files must live under <featureFolder>/comments/.
	commentsDir := filepath.Join(dir, "repos", "MINI", "features", "auth", "comments")
	entries, err := os.ReadDir(commentsDir)
	if err != nil {
		t.Fatalf("read comments dir: %v", err)
	}
	var yamls, mds int
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".yaml") {
			yamls++
		}
		if strings.HasSuffix(e.Name(), ".md") {
			mds++
		}
	}
	if yamls != 1 || mds != 1 {
		t.Fatalf("expected 1 yaml + 1 md, got %d yaml + %d md", yamls, mds)
	}
}

// TestRoundTripFeatureComments asserts the export → import → export
// cycle preserves a feature comment exactly: same uuid, same body, same
// author. Also exercises the deletion-propagation path — deleting the
// on-disk comment file makes the next import drop the DB row.
func TestRoundTripFeatureComments(t *testing.T) {
	// Source DB with one feature comment.
	src, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open src: %v", err)
	}
	t.Cleanup(func() { _ = src.Close() })
	r, err := src.CreateRepo("MINI", "bacio", "/src/path", "")
	if err != nil {
		t.Fatalf("create src repo: %v", err)
	}
	feat, err := src.CreateFeature(r.ID, "auth", "Auth", "", "", "")
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}
	cres, err := src.CreateFeatureComment(feat.ID, "alice", "ship it", "")
	if err != nil {
		t.Fatalf("create feature comment: %v", err)
	}
	cm := cres.Comment

	// Export the source.
	dir := t.TempDir()
	if _, err := (&Engine{Store: src}).Export(context.Background(), dir); err != nil {
		t.Fatalf("export src: %v", err)
	}

	// Import into a fresh DB.
	dst, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open dst: %v", err)
	}
	t.Cleanup(func() { _ = dst.Close() })
	res, err := (&Engine{Store: dst, SkipPropagateDeletes: true}).Import(context.Background(), dir)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.FeatureComments != 1 {
		t.Fatalf("import FeatureComments = %d, want 1", res.FeatureComments)
	}

	got, err := dst.GetFeatureCommentByUUID(cm.UUID)
	if err != nil {
		t.Fatalf("get imported comment: %v", err)
	}
	if got.Author != "alice" || got.Body != "ship it" {
		t.Fatalf("round-trip lost data: author=%q body=%q", got.Author, got.Body)
	}

	// Now blow away the comment files on disk and re-import — the
	// deletion-propagation path should drop the row.
	commentsDir := filepath.Join(dir, "repos", "MINI", "features", "auth", "comments")
	if err := os.RemoveAll(commentsDir); err != nil {
		t.Fatalf("remove comments dir: %v", err)
	}
	res, err = (&Engine{Store: dst}).Import(context.Background(), dir)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	deleted := false
	for _, d := range res.Deleted {
		if d.Kind == store.SyncKindFeatureComment && d.UUID == cm.UUID {
			deleted = true
			break
		}
	}
	if !deleted {
		t.Fatalf("expected deletion of feature comment, got: %+v", res.Deleted)
	}
	if _, err := dst.GetFeatureCommentByUUID(cm.UUID); err == nil {
		t.Fatalf("expected feature comment to be gone after delete-propagation")
	}
}

// TestImport_FeatureCommentResurrectionGuard pins BACI-338 for feature
// comments — the same file-*present* local-delete path as
// TestImport_CommentResurrectionGuard, keyed on feature_comments. A
// feature comment hard-deleted DB-only on dst (its .yaml/.md pair left
// on disk) must stay deleted on re-import: the guard removes the file
// pair, drops the sync_state row, and propagates the deletion.
func TestImport_FeatureCommentResurrectionGuard(t *testing.T) {
	src, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open src: %v", err)
	}
	t.Cleanup(func() { _ = src.Close() })
	r, err := src.CreateRepo("MINI", "bacio", "/src/path", "")
	if err != nil {
		t.Fatalf("create src repo: %v", err)
	}
	feat, err := src.CreateFeature(r.ID, "auth", "Auth", "", "", "")
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}
	cres, err := src.CreateFeatureComment(feat.ID, "alice", "delete me", "")
	if err != nil {
		t.Fatalf("create feature comment: %v", err)
	}
	cm := cres.Comment

	dir := t.TempDir()
	if _, err := (&Engine{Store: src}).Export(context.Background(), dir); err != nil {
		t.Fatalf("export src: %v", err)
	}

	// Seed dst with the feature comment row + its sync_state.
	dst, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open dst: %v", err)
	}
	t.Cleanup(func() { _ = dst.Close() })
	if _, err := (&Engine{Store: dst}).Import(context.Background(), dir); err != nil {
		t.Fatalf("seed dst: %v", err)
	}
	if _, err := dst.GetFeatureCommentByUUID(cm.UUID); err != nil {
		t.Fatalf("dst should have the feature comment after seed: %v", err)
	}
	// Hard-delete the row on dst; the on-disk pair in dir is left present.
	if err := dst.DeleteFeatureCommentByUUID(cm.UUID); err != nil {
		t.Fatalf("delete feature comment row on dst: %v", err)
	}
	if _, err := dst.GetSyncState(cm.UUID); err != nil {
		t.Fatalf("dst should still carry the deleted comment's sync_state: %v", err)
	}
	yamlPath, mdPath := featureCommentFilePaths(t, dir, "MINI", "auth", cm.UUID)

	// Re-import: the guard must NOT re-insert.
	res, err := (&Engine{Store: dst}).Import(context.Background(), dir)
	if err != nil {
		t.Fatalf("re-import dst: %v", err)
	}
	deleted := false
	for _, d := range res.Deleted {
		if d.Kind == store.SyncKindFeatureComment && d.UUID == cm.UUID {
			deleted = true
			break
		}
	}
	if !deleted {
		t.Fatalf("expected feature comment deletion for %s, got %+v", cm.UUID, res.Deleted)
	}
	if res.Inserted != 0 {
		t.Errorf("guard re-inserted: res.Inserted = %d, want 0", res.Inserted)
	}
	if _, err := dst.GetFeatureCommentByUUID(cm.UUID); err == nil {
		t.Fatalf("feature comment resurrected on dst")
	}
	if _, err := os.Stat(yamlPath); !os.IsNotExist(err) {
		t.Errorf("feature comment .yaml still present: %v", err)
	}
	if _, err := os.Stat(mdPath); !os.IsNotExist(err) {
		t.Errorf("feature comment .md still present: %v", err)
	}
	if _, err := dst.GetSyncState(cm.UUID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("sync_state row survived the guard: %v", err)
	}
}

// featureCommentFilePaths resolves the on-disk .yaml/.md pair for a
// feature comment by uuid under repos/<prefix>/features/<slug>/comments/.
func featureCommentFilePaths(t *testing.T, root, prefix, slug, uuid string) (yamlPath, mdPath string) {
	t.Helper()
	commentsDir := filepath.Join(root, "repos", prefix, "features", slug, "comments")
	entries, err := os.ReadDir(commentsDir)
	if err != nil {
		t.Fatalf("read comments dir %s: %v", commentsDir, err)
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, uuid+".yaml") {
			yamlPath = filepath.Join(commentsDir, name)
		}
		if strings.HasSuffix(name, uuid+".md") {
			mdPath = filepath.Join(commentsDir, name)
		}
	}
	if yamlPath == "" || mdPath == "" {
		t.Fatalf("feature comment files for %s not found under %s", uuid, commentsDir)
	}
	return yamlPath, mdPath
}
