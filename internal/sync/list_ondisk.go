package sync

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ListIssuesOnDisk parses every issue.yaml under repos/<prefix>/issues/
// and returns the parsed records sorted by issue number. Used by the
// sync-repo aware `bacio issue list`; read-only, no DB involvement.
// Returns an empty slice (no error) when the prefix has no issues
// folder yet.
func ListIssuesOnDisk(syncRoot, prefix string) ([]*ParsedIssue, error) {
	issuesDir := filepath.Join(syncRoot, "repos", prefix, "issues")
	entries, err := os.ReadDir(issuesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read issues dir: %w", err)
	}
	out := make([]*ParsedIssue, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		yamlPath := filepath.Join(issuesDir, entry.Name(), "issue.yaml")
		b, err := os.ReadFile(yamlPath)
		if err != nil {
			if os.IsNotExist(err) {
				// Folder without an issue.yaml is suspect but
				// shouldn't fail the whole list. Skip silently —
				// `bacio sync verify` is the place to report this.
				continue
			}
			return nil, fmt.Errorf("read %s: %w", yamlPath, err)
		}
		parsed, err := ParseIssueYAML(b)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", yamlPath, err)
		}
		out = append(out, parsed)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out, nil
}

// ReadIssueDescription returns the body of the description.md next
// to the named issue's issue.yaml. Returns "" with no error when the
// file is missing — matches how the rest of the sync layer treats a
// missing description.
func ReadIssueDescription(syncRoot, prefix, label string) (string, error) {
	path := filepath.Join(syncRoot, "repos", prefix, "issues", label, "description.md")
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(NormalizeBody(b)), nil
}

// ListDocumentsOnDisk parses every doc.yaml under repos/<prefix>/docs/
// and returns the parsed records sorted by filename.
func ListDocumentsOnDisk(syncRoot, prefix string) ([]*ParsedDocument, error) {
	docsDir := filepath.Join(syncRoot, "repos", prefix, "docs")
	entries, err := os.ReadDir(docsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read docs dir: %w", err)
	}
	out := make([]*ParsedDocument, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		yamlPath := filepath.Join(docsDir, entry.Name(), "doc.yaml")
		b, err := os.ReadFile(yamlPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read %s: %w", yamlPath, err)
		}
		parsed, err := ParseDocumentYAML(b)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", yamlPath, err)
		}
		out = append(out, parsed)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Filename) < strings.ToLower(out[j].Filename) })
	return out, nil
}
