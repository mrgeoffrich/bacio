package sync

import (
	"context"
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
	cm, err := s.CreateFeatureComment(feat.ID, "alice", "handoff body\n")
	if err != nil {
		t.Fatalf("create feature comment: %v", err)
	}
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
	cm, err := src.CreateFeatureComment(feat.ID, "alice", "ship it")
	if err != nil {
		t.Fatalf("create feature comment: %v", err)
	}

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
