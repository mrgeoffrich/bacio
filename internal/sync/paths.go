package sync

import (
	"fmt"
	"path"
	"strings"
	"time"
)

// Folder/file path generation for the on-disk sync layout. Phase 2 only
// uses these as forward-direction generators (DB → path); Phase 3's
// importer adds case-collision detection. We keep the helpers in one
// place because the YAML schema embeds these labels by reference (e.g.
// the comment filename's timestamp prefix is also written into the
// comment's `created_at` field) and we want a single source of truth.
//
// All inputs that come from user data — slugs, filenames — are
// NFC-normalised on the way out, matching the design doc's "NFC at the
// boundary" rule. The DB itself isn't normalised in Phase 2; the
// canonical form lives on disk only.

// IssueFolder returns the slash-separated relative path of an issue's
// folder under the sync repo root, e.g. "repos/MINI/issues/MINI-7". The
// number isn't NFC-normalised (it's a non-negative integer) and the
// prefix is already constrained by the validator to ASCII alnum.
func IssueFolder(prefix string, number int64) string {
	prefix = strings.ToUpper(strings.TrimSpace(prefix))
	return path.Join("repos", prefix, "issues", fmt.Sprintf("%s-%d", prefix, number))
}

// FeatureFolder returns the slash-separated relative path of a feature's
// folder, e.g. "repos/MINI/features/auth-rewrite". Slugs are
// NFC-normalised; bacio's slug validator already constrains them to
// lowercase kebab-case so this is a no-op in practice but cheap.
func FeatureFolder(prefix, slug string) string {
	prefix = strings.ToUpper(strings.TrimSpace(prefix))
	return path.Join("repos", prefix, "features", NormalizeNFC(slug))
}

// DocumentFolder returns the slash-separated relative path of a
// document's folder, e.g. "repos/MINI/docs/auth-overview.md". The
// filename is used verbatim (with NFC normalisation) — bacio's
// ValidateDocFilenameStrict already rejects `/`, `\`, NUL, and control
// chars, so the result is always safe as a single path segment.
func DocumentFolder(prefix, filename string) string {
	prefix = strings.ToUpper(strings.TrimSpace(prefix))
	return path.Join("repos", prefix, "docs", NormalizeNFC(filename))
}

// CommentFile returns the (yaml, md) sibling paths for one comment
// under an issue folder. The filename format is
// "<RFC3339-with-colons-as-dashes>--<full-uuid>.{yaml,md}". The colons
// in the timestamp become dashes so the file is creatable on Windows
// and case-insensitive filesystems; the dashes-as-separator looks
// odd at first glance but it's what the design doc specifies and
// matches what humans see in `ls`.
//
// The full uuid is included (not a short prefix) so concurrent comment
// adds across machines never collide on filename, even in the
// astronomically unlikely event of two timestamp-identical UUIDv7
// generations.
func CommentFile(issueFolder string, createdAt time.Time, uuid string) (yamlPath, mdPath string) {
	stamp := createdAt.UTC().Truncate(time.Millisecond).Format("2006-01-02T15-04-05.000Z")
	base := stamp + "--" + uuid
	dir := path.Join(issueFolder, "comments")
	return path.Join(dir, base+".yaml"), path.Join(dir, base+".md")
}

// FeatureCommentFile returns the (yaml, md) sibling paths for one
// feature-scoped comment under its feature folder (BACI-124). Mirrors
// CommentFile 1:1 — the on-disk YAML / MD schema is identical between
// issue and feature comments — but lives under
// <featureFolder>/comments/ rather than <issueFolder>/comments/.
func FeatureCommentFile(featureFolder string, createdAt time.Time, uuid string) (yamlPath, mdPath string) {
	stamp := createdAt.UTC().Truncate(time.Millisecond).Format("2006-01-02T15-04-05.000Z")
	base := stamp + "--" + uuid
	dir := path.Join(featureFolder, "comments")
	return path.Join(dir, base+".yaml"), path.Join(dir, base+".md")
}

// IssueDescriptionFile is the markdown sibling of issue.yaml.
func IssueDescriptionFile(issueFolder string) string {
	return path.Join(issueFolder, "description.md")
}

// IssueYAMLFile is the issue.yaml inside an issue folder.
func IssueYAMLFile(issueFolder string) string {
	return path.Join(issueFolder, "issue.yaml")
}

// FeatureDescriptionFile is the markdown sibling of feature.yaml.
func FeatureDescriptionFile(featureFolder string) string {
	return path.Join(featureFolder, "description.md")
}

// FeatureYAMLFile is the feature.yaml inside a feature folder.
func FeatureYAMLFile(featureFolder string) string {
	return path.Join(featureFolder, "feature.yaml")
}

// DocumentContentFile is the body sibling of doc.yaml. The body file is
// named "content" plus the extension carried by the document's own
// filename (".md", ".jsonl", ".txt", ...) so a synced body's on-disk
// type is honest — a JSONL transcript syncs as content.jsonl, not
// content.md. A filename with no extension yields a bare "content".
//
// The extension is taken from the NFC-normalised filename for parity
// with DocumentFolder (which NFC-normalises the filename before using
// it as the folder segment); in practice extensions are ASCII so the
// normalisation is a no-op, but it keeps the two helpers consistent.
func DocumentContentFile(docFolder, filename string) string {
	ext := path.Ext(NormalizeNFC(filename))
	return path.Join(docFolder, "content"+ext)
}

// LegacyDocumentContentFile is the pre-BACI-102 body path: every
// document body synced as content.md regardless of the document's real
// extension. The import / verify / inspect read paths fall back to this
// when the extension-derived file is absent, so documents synced by an
// old binary stay readable without a migration.
func LegacyDocumentContentFile(docFolder string) string {
	return path.Join(docFolder, "content.md")
}

// DocumentYAMLFile is the doc.yaml inside a doc folder.
func DocumentYAMLFile(docFolder string) string {
	return path.Join(docFolder, "doc.yaml")
}

// RepoYAMLFile is the per-repo metadata file.
func RepoYAMLFile(prefix string) string {
	prefix = strings.ToUpper(strings.TrimSpace(prefix))
	return path.Join("repos", prefix, "repo.yaml")
}

// ---------------------------------------------------------------------
// Pivot container records — workspaces, doc folders, kanban lanes
// ---------------------------------------------------------------------
//
// THE A0 RULE. Everything below lands at a NEW sibling path under
// repos/<PREFIX>/ and NEVER as a new key in repo.yaml / issue.yaml /
// doc.yaml, and NEVER as a new file inside an existing record folder.
// Two mechanisms make that non-negotiable:
//
//   - Every record manifest is parsed with KnownFields(true)
//     (strictDecode, yaml_parse.go). A new key in an existing manifest
//     makes an OLDER bacio hard-fail its entire `bacio sync` run, and
//     its next export silently strips the key back out (ExportStaged
//     diffs staging-vs-target byte-wise).
//   - recordFolderOf (export_staging.go) resolves a stale file to the
//     record folder an export should os.RemoveAll. A new file inside
//     repos/<P>/docs/<filename>/ would make an older binary delete the
//     whole document folder, after which propagateDeletes drops the doc
//     from the DB on every machine — silent, cross-machine data loss.
//
// The three shapes below are all invisible to an older binary:
//
//	repos/<P>/workspace.yaml               3 segments  -> recordFolderOf ""
//	repos/<P>/folders/<uuid>/folder.yaml   parts[2]="folders" -> not in the switch -> ""
//	repos/<P>/kanban/<uuid>/column.yaml    parts[2]="kanban"  -> not in the switch -> ""
//
// and an older binary's scanners only ever read repo.yaml plus the
// features/ issues/ docs/ subdirs, so the new siblings are never read,
// rewritten or deleted by it. Pinned by TestLegacyRecordFolderOfIgnoresPivotPaths
// (internal/sync/pivot_backcompat_test.go), which runs a verbatim frozen copy
// of the pre-pivot recordFolderOf against every path above.
//
// The folder segment for both container kinds is the record's UUID, not
// a human label. A label would be renameable, which would need rename
// detection, redirects, and case-collision handling; the uuid is
// immutable so the path is a pure function of identity and a rename is
// a pure content change.
const (
	// WorkspaceSentinelName is the file whose PRESENCE at
	// repos/<PREFIX>/workspace.yaml means "this prefix is a workspace,
	// not a git repo". repo.yaml deliberately does NOT learn a `kind`
	// key — see the A0 rule above.
	WorkspaceSentinelName = "workspace.yaml"
	// DocFoldersSubdir is the per-repo subdir holding doc-folder records.
	DocFoldersSubdir = "folders"
	// DocFolderManifestName is the manifest inside one doc-folder record.
	DocFolderManifestName = "folder.yaml"
	// KanbanColumnsSubdir is the per-repo subdir holding kanban-lane records.
	KanbanColumnsSubdir = "kanban"
	// KanbanColumnManifestName is the manifest inside one kanban-lane record.
	KanbanColumnManifestName = "column.yaml"
)

// WorkspaceYAMLFile returns the workspace sentinel path for a prefix,
// e.g. "repos/MINI/workspace.yaml". Three path segments, so
// recordFolderOf returns "" for it and no binary — old or new — ever
// plans a delete against it. That matches repo.yaml's treatment
// exactly: a repo folder is never removed by the export diff.
func WorkspaceYAMLFile(prefix string) string {
	prefix = strings.ToUpper(strings.TrimSpace(prefix))
	return path.Join("repos", prefix, WorkspaceSentinelName)
}

// DocFolderFolder returns the record folder for one doc folder, e.g.
// "repos/MINI/folders/0191f0d2-....". The segment is the folder's uuid.
func DocFolderFolder(prefix, uuid string) string {
	prefix = strings.ToUpper(strings.TrimSpace(prefix))
	return path.Join("repos", prefix, DocFoldersSubdir, NormalizeNFC(uuid))
}

// DocFolderYAMLFile is the folder.yaml inside a doc-folder record folder.
func DocFolderYAMLFile(folder string) string {
	return path.Join(folder, DocFolderManifestName)
}

// KanbanColumnFolder returns the record folder for one kanban lane,
// e.g. "repos/MINI/kanban/0191f0d2-....". The segment is the column's uuid.
func KanbanColumnFolder(prefix, uuid string) string {
	prefix = strings.ToUpper(strings.TrimSpace(prefix))
	return path.Join("repos", prefix, KanbanColumnsSubdir, NormalizeNFC(uuid))
}

// KanbanColumnYAMLFile is the column.yaml inside a kanban record folder.
func KanbanColumnYAMLFile(folder string) string {
	return path.Join(folder, KanbanColumnManifestName)
}

// RepoFolder is the per-repo folder under the sync repo root.
func RepoFolder(prefix string) string {
	prefix = strings.ToUpper(strings.TrimSpace(prefix))
	return path.Join("repos", prefix)
}

// DeriveSyncLabel turns a canonical sync-remote URL into a short
// human-facing label: the trailing path segment with any `.git` suffix
// stripped. Cheap and prefix-agnostic so it handles `git@host:user/repo.git`
// and `https://host/user/repo.git` alike without parsing as a URL.
//
// Returns "" for degenerate input (empty / bare `.git`). Every caller
// picks its own fallback (`bacio sync init`'s clone-path default is
// "default"; the UI may fall back to the full URL) — pushing one
// hard-coded fallback into the helper would force the wrong default on
// the other callers.
//
// Single source of truth for the URL→label rule shared across CLI, TUI,
// desktop, and web (BACI-105).
func DeriveSyncLabel(remoteURL string) string {
	base := strings.TrimRight(remoteURL, "/")
	if i := strings.LastIndexAny(base, "/:"); i >= 0 {
		base = base[i+1:]
	}
	return strings.TrimSuffix(base, ".git")
}

// DetectCaseInsensitiveCollisions groups folder names that case-fold
// (NFC-normalised + lower-cased) to the same value. The map's key is
// the case-folded form; the value is the list of original names that
// collide. Singleton groups are omitted — only true collisions appear
// in the result.
//
// Phase 2 doesn't use this directly (it's an import-side check), but
// the helper sits naturally with the rest of the path utilities and
// has its own tests, so we land it here. Phase 3's import pipeline is
// what wires it into the validation step.
func DetectCaseInsensitiveCollisions(folders []string) map[string][]string {
	groups := make(map[string][]string)
	for _, f := range folders {
		key := strings.ToLower(NormalizeNFC(f))
		groups[key] = append(groups[key], f)
	}
	for k, v := range groups {
		if len(v) < 2 {
			delete(groups, k)
		}
	}
	return groups
}
