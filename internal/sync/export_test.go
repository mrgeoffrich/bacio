package sync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// seedExportFixture builds a small but realistic dataset directly
// against an in-memory SQLite store. Phase 2's tests used a hand
// rolled fakeStore behind a narrow interface; Phase 3 cleanup
// removed that shim and runs every test against the real schema.
//
// The dataset exercises:
//   - one repo with a feature
//   - two issues (one tied to the feature, one orphan)
//   - relations (blocks)
//   - tags
//   - one comment
//   - one PR
//   - a document linked to one of the issues
//
// Returned alongside the *store.Store is a snapshot map of the uuids
// the test needs to assert against, since uuids are
// auto-generated and the export-side tests want to match them in
// YAML output.
func seedExportFixture(t *testing.T) (*store.Store, map[string]string) {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	r, err := s.CreateRepo("MINI", "bacio", "/local/path", "git@github.com:user/bacio.git")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	feat, err := s.CreateFeature(r.ID, "auth-rewrite", "Rewrite auth", "We need to rewrite the auth layer.\n")
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}
	iss1, err := s.CreateIssue(r.ID, &feat.ID, "Add auth middleware 🔐", "First the middleware, then the rest.\n", model.StateInProgress, []string{"p1", "security"})
	if err != nil {
		t.Fatalf("create iss1: %v", err)
	}
	if err := s.SetIssueAssignee(iss1.ID, "geoff"); err != nil {
		t.Fatalf("set assignee: %v", err)
	}
	iss2, err := s.CreateIssue(r.ID, nil, `Refactor "config" loader`, "", model.StateBacklog, nil)
	if err != nil {
		t.Fatalf("create iss2: %v", err)
	}
	if err := s.CreateRelation(iss1.ID, iss2.ID, model.RelBlocks); err != nil {
		t.Fatalf("create relation: %v", err)
	}
	if _, err := s.AttachPR(iss1.ID, "https://github.com/x/y/pull/42"); err != nil {
		t.Fatalf("attach pr: %v", err)
	}
	c1, err := s.CreateComment(iss1.ID, "geoff", "Looks good to me.\n")
	if err != nil {
		t.Fatalf("create comment: %v", err)
	}
	doc, err := s.CreateDocument(r.ID, "auth-overview.md", model.DocTypeArchitecture, "# Auth overview\n\nNotes go here.\n", "docs/auth-overview.md")
	if err != nil {
		t.Fatalf("create document: %v", err)
	}
	if _, err := s.LinkDocument(doc.ID, store.LinkTarget{IssueID: &iss1.ID}, "design context"); err != nil {
		t.Fatalf("link doc: %v", err)
	}

	uuids := map[string]string{
		"repo":  r.UUID,
		"feat":  feat.UUID,
		"iss1":  iss1.UUID,
		"iss2":  iss2.UUID,
		"c1":    c1.UUID,
		"doc":   doc.UUID,
	}
	// Force deterministic timestamps so the byte-identical test holds
	// across runs. The store's CURRENT_TIMESTAMP is second-precision
	// and fluctuates between test runs, which is fine for the
	// cross-run determinism test (both runs share the same DB) but
	// would otherwise mask drift.
	if _, err := s.DB.Exec(`
		UPDATE repos     SET created_at = '2025-11-01 09:00:00', updated_at = '2025-11-01 09:00:00' WHERE id = ?;
	`, r.ID); err != nil {
		t.Fatalf("force ts repos: %v", err)
	}
	if _, err := s.DB.Exec(`
		UPDATE features  SET created_at = '2025-11-02 09:00:00', updated_at = '2025-11-02 09:00:00' WHERE id = ?;
	`, feat.ID); err != nil {
		t.Fatalf("force ts features: %v", err)
	}
	if _, err := s.DB.Exec(`
		UPDATE issues    SET created_at = '2026-05-01 10:14:22', updated_at = '2026-05-09 14:22:00' WHERE id = ?;
	`, iss1.ID); err != nil {
		t.Fatalf("force ts iss1: %v", err)
	}
	if _, err := s.DB.Exec(`
		UPDATE issues    SET created_at = '2026-05-02 10:00:00', updated_at = '2026-05-03 10:00:00' WHERE id = ?;
	`, iss2.ID); err != nil {
		t.Fatalf("force ts iss2: %v", err)
	}
	if _, err := s.DB.Exec(`
		UPDATE comments  SET created_at = '2026-05-09 14:22:00' WHERE id = ?;
	`, c1.ID); err != nil {
		t.Fatalf("force ts c1: %v", err)
	}
	if _, err := s.DB.Exec(`
		UPDATE documents SET created_at = '2026-04-20 11:00:00', updated_at = '2026-05-01 11:00:00' WHERE id = ?;
	`, doc.ID); err != nil {
		t.Fatalf("force ts doc: %v", err)
	}
	return s, uuids
}

// emptyStore returns a freshly-opened in-memory store with no rows.
// Used to assert the no-data export path.
func emptyStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestExport_ByteIdenticalAcrossRuns is the headline Phase 2 invariant:
// two exports of the same DB produce byte-identical filesystem trees.
// Hash-stability of every individual YAML file is what makes this work,
// but the test asserts the union (every file path + every file body).
func TestExport_ByteIdenticalAcrossRuns(t *testing.T) {
	s, _ := seedExportFixture(t)
	eng := &Engine{Store: s}

	dir1 := t.TempDir()
	dir2 := t.TempDir()

	r1, err := eng.Export(context.Background(), dir1)
	if err != nil {
		t.Fatalf("first export: %v", err)
	}
	r2, err := eng.Export(context.Background(), dir2)
	if err != nil {
		t.Fatalf("second export: %v", err)
	}
	if r1.Files != r2.Files {
		t.Fatalf("file counts differ: %d vs %d", r1.Files, r2.Files)
	}
	if r1.Files == 0 {
		t.Fatal("export produced zero files")
	}

	tree1 := readTree(t, dir1)
	tree2 := readTree(t, dir2)
	if len(tree1) != len(tree2) {
		t.Fatalf("file count mismatch: %d vs %d\nleft: %v\nright: %v",
			len(tree1), len(tree2), keys(tree1), keys(tree2))
	}
	for path, body1 := range tree1 {
		body2, ok := tree2[path]
		if !ok {
			t.Errorf("missing in second export: %s", path)
			continue
		}
		if string(body1) != string(body2) {
			t.Errorf("body differs for %s:\nleft:  %q\nright: %q", path, body1, body2)
		}
	}
}

// TestExport_ProducesExpectedTopLevelLayout asserts the directory shape
// matches the design doc — one file per record, named correctly.
func TestExport_ProducesExpectedTopLevelLayout(t *testing.T) {
	s, _ := seedExportFixture(t)
	eng := &Engine{Store: s}
	dir := t.TempDir()
	if _, err := eng.Export(context.Background(), dir); err != nil {
		t.Fatalf("export: %v", err)
	}
	must := []string{
		"repos/MINI/repo.yaml",
		"repos/MINI/features/auth-rewrite/feature.yaml",
		"repos/MINI/features/auth-rewrite/description.md",
		"repos/MINI/issues/MINI-1/issue.yaml",
		"repos/MINI/issues/MINI-1/description.md",
		"repos/MINI/issues/MINI-2/issue.yaml",
		"repos/MINI/issues/MINI-2/description.md",
		"repos/MINI/docs/auth-overview.md/doc.yaml",
		"repos/MINI/docs/auth-overview.md/content.md",
	}
	for _, p := range must {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p))); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
	commentDir := filepath.Join(dir, "repos", "MINI", "issues", "MINI-1", "comments")
	entries, err := os.ReadDir(commentDir)
	if err != nil {
		t.Fatalf("read comments dir: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 comment files (.yaml + .md), got %d", len(entries))
	}
}

// TestExport_EmptyDB exercises the no-data path: an empty store must
// produce an empty target dir (or at least no files), not an error.
func TestExport_EmptyDB(t *testing.T) {
	s := emptyStore(t)
	eng := &Engine{Store: s}
	dir := t.TempDir()
	r, err := eng.Export(context.Background(), dir)
	if err != nil {
		t.Fatalf("export empty: %v", err)
	}
	if r.Files != 0 {
		t.Fatalf("expected 0 files, got %d", r.Files)
	}
	if r.Repos != 0 {
		t.Fatalf("expected 0 repos, got %d", r.Repos)
	}
}

// TestExport_DryRunWritesNothing checks that DryRun=true skips disk
// writes but still walks every record so the count is meaningful.
func TestExport_DryRunWritesNothing(t *testing.T) {
	s, _ := seedExportFixture(t)
	eng := &Engine{Store: s, DryRun: true}
	dir := t.TempDir()
	r, err := eng.Export(context.Background(), dir)
	if err != nil {
		t.Fatalf("dry-run export: %v", err)
	}
	if r.Files == 0 {
		t.Fatal("dry-run still reports zero files; should count what it would write")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("dry-run wrote %d entries to %s; expected none", len(entries), dir)
	}
}

// TestExport_IssueYAMLContent spot-checks one record's YAML contents
// against the expected schema, including the "always quoted strings"
// rule and the relations + tags shape.
func TestExport_IssueYAMLContent(t *testing.T) {
	s, uuids := seedExportFixture(t)
	eng := &Engine{Store: s}
	dir := t.TempDir()
	if _, err := eng.Export(context.Background(), dir); err != nil {
		t.Fatalf("export: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "repos/MINI/issues/MINI-1/issue.yaml"))
	if err != nil {
		t.Fatalf("read issue.yaml: %v", err)
	}
	body := string(got)
	checks := []string{
		// quoted user strings
		`assignee: "geoff"`,
		`state: "in_progress"`,
		`title: "Add auth middleware 🔐"`,
		// uuid present
		`uuid: "` + uuids["iss1"] + `"`,
		// number is unquoted int
		"number: 1",
		// timestamps quoted with ms precision
		`created_at: "2026-05-01T10:14:22.000Z"`,
		`updated_at: "2026-05-09T14:22:00.000Z"`,
		// feature reference
		`label: "auth-rewrite"`,
		`uuid: "` + uuids["feat"] + `"`,
		// tags sorted alphabetically
		`- "p1"`,
		`- "security"`,
		// relations -> blocks contains MINI-2
		`label: "MINI-2"`,
	}
	for _, want := range checks {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
	if strings.Contains(body, "\r") {
		t.Errorf("CR found in issue.yaml")
	}
}

// TestExport_DescriptionHashMatchesContent: the description_hash field
// in issue.yaml must be the sha256 of description.md's bytes.
func TestExport_DescriptionHashMatchesContent(t *testing.T) {
	s, _ := seedExportFixture(t)
	eng := &Engine{Store: s}
	dir := t.TempDir()
	if _, err := eng.Export(context.Background(), dir); err != nil {
		t.Fatalf("export: %v", err)
	}
	desc, err := os.ReadFile(filepath.Join(dir, "repos/MINI/issues/MINI-1/description.md"))
	if err != nil {
		t.Fatalf("read description.md: %v", err)
	}
	wantHash := ContentHash(desc)
	yamlBytes, err := os.ReadFile(filepath.Join(dir, "repos/MINI/issues/MINI-1/issue.yaml"))
	if err != nil {
		t.Fatalf("read issue.yaml: %v", err)
	}
	if !strings.Contains(string(yamlBytes), `description_hash: "`+wantHash+`"`) {
		t.Errorf("description_hash missing or wrong; want %q\nyaml:\n%s", wantHash, yamlBytes)
	}
}

func readTree(t *testing.T, root string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = body
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	return out
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
