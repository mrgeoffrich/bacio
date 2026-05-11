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

// DocumentContentFile is the markdown sibling of doc.yaml.
func DocumentContentFile(docFolder string) string {
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

// RepoFolder is the per-repo folder under the sync repo root.
func RepoFolder(prefix string) string {
	prefix = strings.ToUpper(strings.TrimSpace(prefix))
	return path.Join("repos", prefix)
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
