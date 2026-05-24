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
	feat, err := s.CreateFeature(repo.ID, "auth", "Auth", "")
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}
	c1, err := s.CreateFeatureComment(feat.ID, "alice", "first")
	if err != nil {
		t.Fatalf("create #1: %v", err)
	}
	if c1.UUID == "" {
		t.Fatalf("expected uuid to be minted, got empty")
	}
	if c1.FeatureID != feat.ID {
		t.Fatalf("FeatureID = %d, want %d", c1.FeatureID, feat.ID)
	}
	c2, err := s.CreateFeatureComment(feat.ID, "bob", "second")
	if err != nil {
		t.Fatalf("create #2: %v", err)
	}
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
	feat, err := s.CreateFeature(repo.ID, "auth", "Auth", "")
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}
	if _, err := s.CreateFeatureComment(feat.ID, "", "body"); err == nil {
		t.Fatal("expected empty author to be rejected")
	}
	if _, err := s.CreateFeatureComment(feat.ID, "alice", ""); err == nil {
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
	feat, err := s.CreateFeature(repo.ID, "auth", "Auth", "")
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}
	c, err := s.CreateFeatureComment(feat.ID, "alice", "hi")
	if err != nil {
		t.Fatalf("create comment: %v", err)
	}
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
	feat, err := s.CreateFeature(repo.ID, "auth", "Auth", "")
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}
	want := time.Date(2025, 3, 1, 9, 0, 0, 0, time.UTC)
	c, err := s.CreateFeatureCommentFromSync(feat.ID, "0192e000-0000-7000-0000-000000000001",
		"alice", "body", sql.NullTime{Time: want, Valid: true})
	if err != nil {
		t.Fatalf("create from sync: %v", err)
	}
	if !c.CreatedAt.Equal(want) {
		t.Fatalf("CreatedAt = %v, want %v", c.CreatedAt, want)
	}
	if c.UUID != "0192e000-0000-7000-0000-000000000001" {
		t.Fatalf("UUID = %q, want preserved", c.UUID)
	}
	if _, err := s.CreateFeatureCommentFromSync(feat.ID, "",
		"alice", "body", sql.NullTime{}); err == nil {
		t.Fatal("expected empty uuid to be rejected")
	}
}
