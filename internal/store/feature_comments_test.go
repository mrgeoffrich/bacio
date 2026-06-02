package store

import (
	"database/sql"
	"errors"
	"testing"
	"time"
)

// TestFeatureCommentCRUD exercises the CRUD path on the BACI-124
// feature_comments table: insert, list, lookup by uuid, update, and
// delete.
func TestFeatureCommentCRUD(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("FCT", "fct", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	feat, err := s.CreateFeature(repo.ID, "auth", "Auth", "", "", "")
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}
	r1, err := s.CreateFeatureComment(feat.ID, "alice", "first", "")
	if err != nil {
		t.Fatalf("create #1: %v", err)
	}
	c1 := r1.Comment
	if c1.UUID == "" {
		t.Fatalf("expected uuid to be minted, got empty")
	}
	if c1.FeatureID != feat.ID {
		t.Fatalf("FeatureID = %d, want %d", c1.FeatureID, feat.ID)
	}
	// Empty kind defaults to 'note'.
	if c1.Kind != FeatureCommentKindNote {
		t.Fatalf("Kind = %q, want %q", c1.Kind, FeatureCommentKindNote)
	}
	r2, err := s.CreateFeatureComment(feat.ID, "bob", "second", "")
	if err != nil {
		t.Fatalf("create #2: %v", err)
	}
	c2 := r2.Comment
	got, err := s.ListFeatureComments(feat.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("list returned %d rows, want 2", len(got))
	}
	if got[0].UUID != c1.UUID || got[1].UUID != c2.UUID {
		t.Fatalf("list order = [%s, %s], want [%s, %s]",
			got[0].UUID, got[1].UUID, c1.UUID, c2.UUID)
	}
	byUUID, err := s.GetFeatureCommentByUUID(c1.UUID)
	if err != nil {
		t.Fatalf("get by uuid: %v", err)
	}
	if byUUID.ID != c1.ID {
		t.Fatalf("get by uuid id = %d, want %d", byUUID.ID, c1.ID)
	}
	// Count helper used by FeatureDeletePreview.
	n, err := s.CountFeatureComments(feat.ID)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Fatalf("count = %d, want 2", n)
	}
	// Update — sync importer path.
	newBody := "updated"
	if err := s.UpdateFeatureCommentByUUID(c1.UUID, nil, &newBody); err != nil {
		t.Fatalf("update: %v", err)
	}
	after, err := s.GetFeatureCommentByUUID(c1.UUID)
	if err != nil {
		t.Fatalf("get post-update: %v", err)
	}
	if after.Body != newBody {
		t.Fatalf("post-update body = %q, want %q", after.Body, newBody)
	}
	// Delete.
	if err := s.DeleteFeatureCommentByUUID(c1.UUID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetFeatureCommentByUUID(c1.UUID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("post-delete get error = %v, want ErrNotFound", err)
	}
	if err := s.DeleteFeatureCommentByUUID(c1.UUID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete-already-deleted error = %v, want ErrNotFound", err)
	}
}

// TestFeatureCommentValidation locks in the same author/body validation
// rules as comments — empty author / body / control characters are
// rejected at the store boundary.
func TestFeatureCommentValidation(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("FCT", "fct", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	feat, err := s.CreateFeature(repo.ID, "auth", "Auth", "", "", "")
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}
	if _, err := s.CreateFeatureComment(feat.ID, "", "body", ""); err == nil {
		t.Fatal("expected empty author to be rejected")
	}
	if _, err := s.CreateFeatureComment(feat.ID, "alice", "", ""); err == nil {
		t.Fatal("expected empty body to be rejected")
	}
}

// TestFeatureCommentCascade pins the ON DELETE CASCADE FK — deleting a
// feature must wipe its feature_comments rows.
func TestFeatureCommentCascade(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("FCT", "fct", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	feat, err := s.CreateFeature(repo.ID, "auth", "Auth", "", "", "")
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}
	r, err := s.CreateFeatureComment(feat.ID, "alice", "hi", "")
	if err != nil {
		t.Fatalf("create comment: %v", err)
	}
	c := r.Comment
	if err := s.DeleteFeature(feat.ID); err != nil {
		t.Fatalf("delete feature: %v", err)
	}
	if _, err := s.GetFeatureCommentByUUID(c.UUID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("post-cascade get error = %v, want ErrNotFound", err)
	}
}

// TestCreateFeatureCommentFromSync covers the sync import path — caller-
// supplied uuid + createdAt timestamp.
func TestCreateFeatureCommentFromSync(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("FCT", "fct", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	feat, err := s.CreateFeature(repo.ID, "auth", "Auth", "", "", "")
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}
	want := time.Date(2025, 3, 1, 9, 0, 0, 0, time.UTC)
	c, err := s.CreateFeatureCommentFromSync(feat.ID, "0192e000-0000-7000-0000-000000000001",
		"alice", "body", "handoff", sql.NullTime{Time: want, Valid: true})
	if err != nil {
		t.Fatalf("create from sync: %v", err)
	}
	if !c.CreatedAt.Equal(want) {
		t.Fatalf("CreatedAt = %v, want %v", c.CreatedAt, want)
	}
	if c.UUID != "0192e000-0000-7000-0000-000000000001" {
		t.Fatalf("UUID = %q, want preserved", c.UUID)
	}
	// kind round-trips on the sync path; the backstop is NOT applied
	// here (sync replays whatever the origin recorded).
	if c.Kind != FeatureCommentKindHandoff {
		t.Fatalf("Kind = %q, want %q", c.Kind, FeatureCommentKindHandoff)
	}
	if _, err := s.CreateFeatureCommentFromSync(feat.ID, "",
		"alice", "body", "", sql.NullTime{}); err == nil {
		t.Fatal("expected empty uuid to be rejected")
	}
}

// TestCreateFeatureComment_HandoffDroppedWhenDisabled (BACI-333): a
// kind=handoff write to a feature with collect_handoffs off is dropped —
// Skipped:true, no row inserted, no error.
func TestCreateFeatureComment_HandoffDroppedWhenDisabled(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("FCT", "fct", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	feat, err := s.CreateFeature(repo.ID, "bucket", "Bucket", "", "", "")
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}
	if err := s.SetFeatureCollectHandoffs(feat.ID, false); err != nil {
		t.Fatalf("disable handoffs: %v", err)
	}
	res, err := s.CreateFeatureComment(feat.ID, "agent-bob", "noise", FeatureCommentKindHandoff)
	if err != nil {
		t.Fatalf("create handoff: %v", err)
	}
	if !res.Skipped {
		t.Fatalf("expected Skipped, got %+v", res)
	}
	if res.Comment != nil {
		t.Fatalf("expected nil Comment on skip, got %+v", res.Comment)
	}
	if res.Reason == "" {
		t.Fatal("expected a Reason on skip")
	}
	n, err := s.CountFeatureComments(feat.ID)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("count = %d, want 0 (handoff dropped)", n)
	}
}

// TestCreateFeatureComment_NoteAlwaysInserts (BACI-333): a hand-typed
// note is never gated — it inserts even when collect_handoffs is off.
func TestCreateFeatureComment_NoteAlwaysInserts(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("FCT", "fct", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	feat, err := s.CreateFeature(repo.ID, "bucket", "Bucket", "", "", "")
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}
	if err := s.SetFeatureCollectHandoffs(feat.ID, false); err != nil {
		t.Fatalf("disable handoffs: %v", err)
	}
	res, err := s.CreateFeatureComment(feat.ID, "alice", "real note", FeatureCommentKindNote)
	if err != nil {
		t.Fatalf("create note: %v", err)
	}
	if res.Skipped || res.Comment == nil {
		t.Fatalf("expected an inserted note, got %+v", res)
	}
	if res.Comment.Kind != FeatureCommentKindNote {
		t.Fatalf("Kind = %q, want note", res.Comment.Kind)
	}
}

// TestCreateFeatureComment_HandoffKeptWhenEnabled (BACI-333): on a
// default (collect_handoffs ON) feature a handoff inserts normally.
func TestCreateFeatureComment_HandoffKeptWhenEnabled(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("FCT", "fct", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	feat, err := s.CreateFeature(repo.ID, "auth", "Auth", "", "", "")
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}
	res, err := s.CreateFeatureComment(feat.ID, "agent-bob", "handoff note", FeatureCommentKindHandoff)
	if err != nil {
		t.Fatalf("create handoff: %v", err)
	}
	if res.Skipped || res.Comment == nil {
		t.Fatalf("expected an inserted handoff, got %+v", res)
	}
	if res.Comment.Kind != FeatureCommentKindHandoff {
		t.Fatalf("Kind = %q, want handoff", res.Comment.Kind)
	}
}

// TestCreateFeatureComment_RejectsUnknownKind (BACI-333): a bogus kind
// fails loud at the store boundary.
func TestCreateFeatureComment_RejectsUnknownKind(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("FCT", "fct", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	feat, err := s.CreateFeature(repo.ID, "auth", "Auth", "", "", "")
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}
	if _, err := s.CreateFeatureComment(feat.ID, "alice", "body", "bogus"); err == nil {
		t.Fatal("expected unknown kind to be rejected")
	}
}

// TestSetFeatureCollectHandoffs (BACI-333): default reads true; flip OFF
// persists; flip back ON persists.
func TestSetFeatureCollectHandoffs(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("FCT", "fct", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	feat, err := s.CreateFeature(repo.ID, "bucket", "Bucket", "", "", "")
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}
	if !feat.CollectHandoffs {
		t.Fatal("default CollectHandoffs = false, want true (opt-out)")
	}
	if err := s.SetFeatureCollectHandoffs(feat.ID, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	off, err := s.GetFeatureByID(feat.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if off.CollectHandoffs {
		t.Fatal("after disable CollectHandoffs = true, want false")
	}
	if err := s.SetFeatureCollectHandoffs(feat.ID, true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	on, err := s.GetFeatureByID(feat.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !on.CollectHandoffs {
		t.Fatal("after enable CollectHandoffs = false, want true")
	}
}
