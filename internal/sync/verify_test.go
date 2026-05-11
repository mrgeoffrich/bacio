package sync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildVerifyFixture exports a populated DB into a tmpdir and writes
// the sync sentinel at the root, producing a clean sync repo we can
// then deliberately corrupt to test Verify's catches. We reuse
// seedExportFixture from export_test.go so the data shape matches the
// rest of the sync test suite.
func buildVerifyFixture(t *testing.T) string {
	t.Helper()
	s, _ := seedExportFixture(t)
	dir := t.TempDir()
	if _, err := (&Engine{Store: s}).Export(context.Background(), dir); err != nil {
		t.Fatalf("export: %v", err)
	}
	if err := WriteSentinel(dir, Sentinel{SchemaVersion: 1}); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}
	return dir
}

func TestVerify_CleanRepoReportsNoFindings(t *testing.T) {
	dir := buildVerifyFixture(t)
	res, err := (&Engine{}).Verify(context.Background(), dir)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Errorf("expected no errors, got %d: %+v", len(res.Errors), res.Errors)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("expected no warnings, got %d: %+v", len(res.Warnings), res.Warnings)
	}
	// Counts should be > 0 (we exported a non-empty fixture).
	if res.Repos == 0 || res.Issues == 0 || res.Features == 0 {
		t.Errorf("unexpected zero count: %+v", res)
	}
}

func TestVerify_DetectsCorruptDescriptionHash(t *testing.T) {
	dir := buildVerifyFixture(t)
	// Stomp on the issue.yaml description_hash with a bogus value.
	yamlPath := filepath.Join(dir, "repos", "MINI", "issues", "MINI-1", "issue.yaml")
	b, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatalf("read issue.yaml: %v", err)
	}
	munged := strings.Replace(string(b), `description_hash: "sha256:`, `description_hash: "sha256:00`, 1)
	if munged == string(b) {
		t.Fatalf("did not find description_hash marker in issue.yaml")
	}
	if err := os.WriteFile(yamlPath, []byte(munged), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	res, err := (&Engine{}).Verify(context.Background(), dir)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	found := false
	for _, w := range res.Warnings {
		if w.Kind == VerifyKindHashMismatch {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected hash_mismatch warning; got errors=%+v warnings=%+v", res.Errors, res.Warnings)
	}
}

func TestVerify_DetectsOrphanComment(t *testing.T) {
	dir := buildVerifyFixture(t)
	// Find any comment .md and delete it.
	commentsDir := filepath.Join(dir, "repos", "MINI", "issues", "MINI-1", "comments")
	entries, err := os.ReadDir(commentsDir)
	if err != nil {
		t.Fatalf("read comments dir: %v", err)
	}
	deleted := false
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			if err := os.Remove(filepath.Join(commentsDir, e.Name())); err != nil {
				t.Fatalf("remove md: %v", err)
			}
			deleted = true
			break
		}
	}
	if !deleted {
		t.Skip("no comment .md to remove (fixture changed?)")
	}
	res, err := (&Engine{}).Verify(context.Background(), dir)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	found := false
	for _, e := range res.Errors {
		if e.Kind == VerifyKindOrphanComment {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected orphan_comment error; got %+v", res.Errors)
	}
}

func TestVerify_DetectsUUIDCollision(t *testing.T) {
	dir := buildVerifyFixture(t)
	// Read one feature.yaml's uuid and set it on a second feature
	// (we only have one feature in the fixture, so duplicate the
	// feature folder under a sibling name with the same uuid). The
	// simpler shortcut is to copy the feature folder to a new path.
	src := filepath.Join(dir, "repos", "MINI", "features", "auth-rewrite")
	dst := filepath.Join(dir, "repos", "MINI", "features", "auth-rewrite-clone")
	if err := copyTree(src, dst); err != nil {
		t.Fatalf("copy: %v", err)
	}
	// Edit dst's feature.yaml so the slug is different but the uuid
	// matches. We just rewrite slug to match the new folder name.
	yamlPath := filepath.Join(dst, "feature.yaml")
	b, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatalf("read clone yaml: %v", err)
	}
	munged := strings.Replace(string(b), `slug: "auth-rewrite"`, `slug: "auth-rewrite-clone"`, 1)
	if err := os.WriteFile(yamlPath, []byte(munged), 0o644); err != nil {
		t.Fatalf("write clone: %v", err)
	}
	res, err := (&Engine{}).Verify(context.Background(), dir)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	found := false
	for _, e := range res.Errors {
		if e.Kind == VerifyKindUUIDCollision {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected uuid_collision error; got %+v", res.Errors)
	}
}

func TestVerify_DetectsDanglingReference(t *testing.T) {
	dir := buildVerifyFixture(t)
	// Rewrite the feature uuid in MINI-1's issue.yaml to a fresh
	// UUID that doesn't exist in the scan, simulating a stale
	// reference.
	yamlPath := filepath.Join(dir, "repos", "MINI", "issues", "MINI-1", "issue.yaml")
	b, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatalf("read issue.yaml: %v", err)
	}
	// The feature: block contains a uuid: "<original>"; replace with
	// a value that's the right shape but unused.
	original := string(b)
	bogusUUID := "0190c2a3-7f3a-7b2c-8b21-deadbeefcafe"
	// We only need to swap the feature's uuid line. Find the line
	// that starts after "feature:".
	idx := strings.Index(original, "feature:")
	if idx < 0 {
		t.Skip("issue does not reference a feature; fixture changed?")
	}
	tail := original[idx:]
	uIdx := strings.Index(tail, "uuid:")
	if uIdx < 0 {
		t.Fatalf("no uuid: line under feature: in fixture")
	}
	// Replace the uuid value on that line.
	lineEnd := strings.Index(tail[uIdx:], "\n")
	if lineEnd < 0 {
		lineEnd = len(tail) - uIdx
	}
	newLine := `uuid: "` + bogusUUID + `"`
	munged := original[:idx+uIdx] + newLine + tail[uIdx+lineEnd:]
	if err := os.WriteFile(yamlPath, []byte(munged), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	res, err := (&Engine{}).Verify(context.Background(), dir)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	found := false
	for _, w := range res.Warnings {
		if w.Kind == VerifyKindDanglingRef {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected dangling_reference warning; got errors=%+v warnings=%+v", res.Errors, res.Warnings)
	}
}

func TestVerify_RejectsNonSyncRepo(t *testing.T) {
	dir := t.TempDir()
	_, err := (&Engine{}).Verify(context.Background(), dir)
	if err == nil {
		t.Fatal("expected error verifying non-sync-repo dir")
	}
}

// copyTree shallow-copies a directory tree. Just enough for the
// fixture munging in tests; not a general-purpose helper.
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, info.Mode().Perm())
	})
}
