package sync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v4"
)

// THE MOST IMPORTANT TEST IN THIS PACKAGE.
//
// An older bacio parses repo.yaml / issue.yaml / doc.yaml with
// KnownFields(true) (strictDecode). One new key in any of them and that
// binary hard-fails its ENTIRE `bacio sync` run — not the one record,
// the whole run — and its next export silently strips the key back out
// because ExportStaged diffs staging-vs-target byte-wise.
//
// So the workspaces + Kanban pivot puts every new synced field in NEW
// SIBLING records under repos/<PREFIX>/ instead. These tests are what
// keep it that way. If one of them fails because you added a key, do
// not update the golden — move the data to a sibling record.

// legacyManifestKeys freezes the top-level key set of each manifest as
// an older bacio's ParsedRepo / ParsedIssue / ParsedDocument structs
// declare it. A key not in this list makes that binary fail with
// `field <x> not found in type sync.Parsed…`.
//
// Optional keys (emitted only when set) are listed too — they are all
// keys that shipped BEFORE the pivot, so every binary that could
// receive them already knows them.
var legacyManifestKeys = map[string][]string{
	"repo.yaml": {
		"created_at", "name", "next_issue_number", "prefix", "remote_url",
		"updated_at", "uuid",
	},
	"issue.yaml": {
		"archived_at", "assignee", "created_at", "customer_impact",
		"description_hash", "feature", "number", "prs", "relations", "state",
		"tags", "title", "updated_at", "uuid",
	},
	"doc.yaml": {
		"archived_at", "content_hash", "created_at", "filename", "links",
		"source_path", "type", "updated_at", "uuid",
	},
}

// TestLegacyManifestsCarryNoPivotKeys walks every repo.yaml,
// issue.yaml and doc.yaml an export produces from a DB that has the
// full pivot dataset — a workspace, nested doc folders with documents
// in them, Kanban lanes with cards on them — and asserts not one of
// them learned a key.
func TestLegacyManifestsCarryNoPivotKeys(t *testing.T) {
	s, _ := seedExportFixture(t)
	seedPivotContainers(t, s)

	dir := t.TempDir()
	eng := &Engine{Store: s}
	if _, err := eng.Export(context.Background(), dir); err != nil {
		t.Fatalf("export: %v", err)
	}

	checked := map[string]int{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		want, tracked := legacyManifestKeys[d.Name()]
		if !tracked {
			return nil
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		var got map[string]any
		if err := yaml.Unmarshal(body, &got); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		allowed := map[string]bool{}
		for _, k := range want {
			allowed[k] = true
		}
		for k := range got {
			if !allowed[k] {
				rel, _ := filepath.Rel(dir, p)
				t.Errorf("A0 VIOLATION: %s gained the key %q.\n"+
					"An older bacio parses this file with KnownFields(true) and will hard-fail its whole "+
					"sync run on it, then strip the key on its next export.\n"+
					"Move the data to a new sibling record under repos/<PREFIX>/ instead — never a new key here.",
					rel, k)
			}
		}
		checked[d.Name()]++
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	// Guard against the test silently passing because the export wrote
	// nothing to inspect.
	for _, name := range []string{"repo.yaml", "issue.yaml", "doc.yaml"} {
		if checked[name] == 0 {
			t.Fatalf("no %s files were checked — the fixture or the export path changed", name)
		}
	}
}

// TestPivotContainersDoNotPerturbLegacyManifests exports the same DB
// twice — once before the pivot containers exist, once after adding a
// workspace sentinel, a folder tree with documents in it, and Kanban
// lanes with cards on them — and asserts every repo.yaml / issue.yaml /
// doc.yaml is byte-identical between the two.
//
// This is the value-level half of the guarantee that the key-set test
// above makes at the schema level: not only does no key appear,
// nothing about placing a document in a folder or a card on a lane
// changes a single byte of the record's own manifest. That is what
// stops the first sync after an upgrade from rewriting (and, from an
// older peer's side, fighting over) every file in the sync repo.
func TestPivotContainersDoNotPerturbLegacyManifests(t *testing.T) {
	s, _ := seedExportFixture(t)
	eng := &Engine{Store: s}

	before := t.TempDir()
	if _, err := eng.Export(context.Background(), before); err != nil {
		t.Fatalf("pre-pivot export: %v", err)
	}

	seedPivotContainers(t, s)

	after := t.TempDir()
	if _, err := eng.Export(context.Background(), after); err != nil {
		t.Fatalf("post-pivot export: %v", err)
	}

	for _, rel := range []string{
		"repos/MINI/repo.yaml",
		"repos/MINI/issues/MINI-1/issue.yaml",
		"repos/MINI/issues/MINI-2/issue.yaml",
		"repos/MINI/docs/auth-overview.md/doc.yaml",
		"repos/MINI/features/auth-rewrite/feature.yaml",
	} {
		a := readFileOrFail(t, filepath.Join(before, filepath.FromSlash(rel)))
		b := readFileOrFail(t, filepath.Join(after, filepath.FromSlash(rel)))
		if a != b {
			t.Errorf("%s changed when pivot containers were added.\n--- before ---\n%s\n--- after ---\n%s", rel, a, b)
		}
	}
}

// TestLegacyManifestBytesAreFrozen pins the exact bytes of the three
// manifests an older binary reads. The key-set test above catches a new
// key; this one also catches a reordering, a re-quoting, or a
// timestamp-format change — anything that would make an older peer see
// a byte diff and rewrite the file on its next export.
//
// uuids are the only substituted values; every timestamp and hash in
// the fixture is deterministic.
func TestLegacyManifestBytesAreFrozen(t *testing.T) {
	s, uuids := seedExportFixture(t)
	seedPivotContainers(t, s)

	dir := t.TempDir()
	eng := &Engine{Store: s}
	if _, err := eng.Export(context.Background(), dir); err != nil {
		t.Fatalf("export: %v", err)
	}

	cases := []struct {
		rel  string
		want string
	}{
		{"repos/MINI/repo.yaml", `created_at: "2025-11-01T09:00:00.000Z"
name: "bacio"
next_issue_number: 3
prefix: "MINI"
remote_url: "git@github.com:user/bacio.git"
updated_at: "2025-11-01T09:00:00.000Z"
uuid: "` + uuids["repo"] + `"
`},
		{"repos/MINI/issues/MINI-1/issue.yaml", `assignee: "geoff"
created_at: "2026-05-01T10:14:22.000Z"
description_hash: "sha256:0af83bf49f7dc1dd4a42d00735be7ead6e09ed9d50e89700a76395d9826d7ba2"
feature:
  label: "auth-rewrite"
  uuid: "` + uuids["feat"] + `"
number: 1
prs:
  - "https://github.com/x/y/pull/42"
relations:
  blocks:
    - label: "MINI-2"
      uuid: "` + uuids["iss2"] + `"
  duplicate_of: []
  relates_to: []
state: "in_review"
tags:
  - "p1"
  - "security"
title: "Add auth middleware 🔐"
updated_at: "2026-05-09T14:22:00.000Z"
uuid: "` + uuids["iss1"] + `"
`},
		{"repos/MINI/docs/auth-overview.md/doc.yaml", `content_hash: "sha256:451fe2c030c043e92f8602670c3a05fa82e8ecf66286654fbcf9af571e2ac1e0"
created_at: "2026-04-20T11:00:00.000Z"
filename: "auth-overview.md"
links:
  - kind: "issue"
    target_label: "MINI-1"
    target_uuid: "` + uuids["iss1"] + `"
source_path: "docs/auth-overview.md"
type: "architecture"
updated_at: "2026-05-01T11:00:00.000Z"
uuid: "` + uuids["doc"] + `"
`},
	}
	for _, tc := range cases {
		got := readFileOrFail(t, filepath.Join(dir, filepath.FromSlash(tc.rel)))
		if got != tc.want {
			t.Errorf("%s bytes drifted from the frozen pre-pivot shape.\n--- want ---\n%s\n--- got ---\n%s",
				tc.rel, tc.want, got)
		}
	}
}

// legacyRecordFolderOf is a VERBATIM FROZEN COPY of recordFolderOf as
// it existed before the pivot (export_staging.go at 1d80385). Do not
// "fix" it, do not keep it in sync with the live function — its whole
// job is to be the older binary, so the tests below can prove what an
// older binary does with paths it has never heard of.
func legacyRecordFolderOf(relPath string) string {
	parts := strings.Split(relPath, "/")
	if len(parts) < 4 || parts[0] != "repos" {
		return ""
	}
	switch parts[2] {
	case "features", "issues":
		if len(parts) >= 6 && parts[4] == "comments" {
			return relPath
		}
		return strings.Join(parts[:4], "/")
	case "docs":
		return strings.Join(parts[:4], "/")
	}
	return ""
}

// TestLegacyRecordFolderOfIgnoresPivotPaths is the old-binary-safety
// proof. computeFileOps only ever plans an opDelete for a path whose
// recordFolderOf is non-empty, so an older binary returning "" for
// every pivot path means it can never delete one — which is the
// catastrophic case the A0 rule exists to prevent (an os.RemoveAll of a
// record folder, followed by propagateDeletes dropping the record from
// the DB on every machine).
func TestLegacyRecordFolderOfIgnoresPivotPaths(t *testing.T) {
	const u = "0191f0d2-1111-7000-8000-aaaaaaaaaaaa"
	for _, p := range []string{
		WorkspaceYAMLFile("MINI"),
		DocFolderYAMLFile(DocFolderFolder("MINI", u)),
		KanbanColumnYAMLFile(KanbanColumnFolder("MINI", u)),
		DocFolderFolder("MINI", u),
		KanbanColumnFolder("MINI", u),
	} {
		if got := legacyRecordFolderOf(p); got != "" {
			t.Errorf("an older bacio would plan a DELETE of %q for path %q — this is the silent "+
				"cross-machine data-loss case. The path must not start repos/<P>/{features,issues,docs}/.",
				got, p)
		}
	}
}

// TestRecordFolderOfHandlesPivotPaths pins THIS binary's deliberate
// divergence from the frozen function above.
//
// A new binary must be able to delete a container record folder:
// without it, deleting a folder locally would leave folder.yaml on disk
// forever and the very next import would scan it and resurrect the
// folder. The divergence is safe precisely because an older binary
// carries its own compiled copy of recordFolderOf — nothing here can
// make it delete anything.
//
// workspace.yaml stays on the "never deleted" side in BOTH binaries: it
// is three segments, exactly like its sibling repo.yaml, and bacio has
// never pruned a whole repos/<PREFIX>/ folder.
func TestRecordFolderOfHandlesPivotPaths(t *testing.T) {
	const u = "0191f0d2-1111-7000-8000-aaaaaaaaaaaa"

	if got := recordFolderOf(WorkspaceYAMLFile("MINI")); got != "" {
		t.Errorf("workspace.yaml must never be delete-planned, got %q", got)
	}
	if got := recordFolderOf(DocFolderYAMLFile(DocFolderFolder("MINI", u))); got != DocFolderFolder("MINI", u) {
		t.Errorf("folder.yaml should scope a delete to its record folder, got %q", got)
	}
	if got := recordFolderOf(KanbanColumnYAMLFile(KanbanColumnFolder("MINI", u))); got != KanbanColumnFolder("MINI", u) {
		t.Errorf("column.yaml should scope a delete to its record folder, got %q", got)
	}
	// And the legacy kinds are untouched.
	if got := recordFolderOf("repos/MINI/docs/auth-overview.md/doc.yaml"); got != "repos/MINI/docs/auth-overview.md" {
		t.Errorf("doc record folder resolution regressed: %q", got)
	}
	if got := recordFolderOf("repos/MINI/issues/MINI-1/comments/x.yaml"); got != "repos/MINI/issues/MINI-1/comments/x.yaml" {
		t.Errorf("comment-scoped delete regressed: %q", got)
	}
}

// TestPivotPathsAreSiblingsNotNested guards the other half of the A0
// rule: no pivot file may land INSIDE an existing record folder, and no
// record folder may be nested. Either would make an older binary
// os.RemoveAll the enclosing record.
func TestPivotPathsAreSiblingsNotNested(t *testing.T) {
	const u = "0191f0d2-1111-7000-8000-aaaaaaaaaaaa"
	for _, p := range []string{
		WorkspaceYAMLFile("MINI"),
		DocFolderYAMLFile(DocFolderFolder("MINI", u)),
		KanbanColumnYAMLFile(KanbanColumnFolder("MINI", u)),
	} {
		parts := strings.Split(p, "/")
		if len(parts) < 3 || parts[0] != "repos" {
			t.Fatalf("unexpected shape %q", p)
		}
		if len(parts) == 3 {
			continue // repos/<P>/<file>.yaml — safe by length
		}
		switch parts[2] {
		case "features", "issues", "docs":
			t.Errorf("%q nests inside an existing record kind (%q) — an older bacio would delete "+
				"the enclosing record folder and propagate the delete to every machine", p, parts[2])
		}
		if len(parts) > 5 {
			t.Errorf("%q is nested deeper than <kind>/<record>/<manifest>; keep container records flat", p)
		}
	}
}

// TestIndexYAMLShapeIsFrozen. index.yaml is ALSO parsed with
// strictDecode (ReadIndex), so it is a fourth file the A0 rule covers —
// something the pivot plan didn't call out. The export counts folders
// and lanes, but deliberately does not surface those counts in
// index.yaml, because an older binary reading a new key there fails
// every `bacio issue list` / `doc list` that goes through the sync repo.
func TestIndexYAMLShapeIsFrozen(t *testing.T) {
	s, _ := seedExportFixture(t)
	seedPivotContainers(t, s)

	dir := t.TempDir()
	eng := &Engine{Store: s}
	if _, err := eng.Export(context.Background(), dir); err != nil {
		t.Fatalf("export: %v", err)
	}
	body := readFileOrFail(t, filepath.Join(dir, IndexFilename))
	var parsed struct {
		SchemaVersion int              `yaml:"schema_version"`
		Repos         []map[string]any `yaml:"repos"`
	}
	if err := yaml.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("parse index: %v", err)
	}
	allowed := map[string]bool{
		"comments": true, "documents": true, "feature_comments": true,
		"features": true, "issues": true, "name": true, "prefix": true,
		"remote": true, "uuid": true,
	}
	if len(parsed.Repos) == 0 {
		t.Fatal("index.yaml listed no repos")
	}
	for _, entry := range parsed.Repos {
		keys := make([]string, 0, len(entry))
		for k := range entry {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if !allowed[k] {
				t.Errorf("A0 VIOLATION: index.yaml entry gained the key %q. ReadIndex uses "+
					"strictDecode, so an older bacio hard-fails on it.", k)
			}
		}
	}
}

func readFileOrFail(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
