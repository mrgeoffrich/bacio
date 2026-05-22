package sync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Inspect is the read-only browsing counterpart to Verify. It opens
// no DB, performs no validation; it just parses one record (or
// summarises one prefix) so the user can eyeball what the sync repo
// contains. Useful for "what did the other client push?" debugging
// without needing the project repo's DB.
//
// Like Verify, Inspect requires sentinel-mode (bacio-sync.yaml at the
// root) — running it against a random folder of YAML would silently
// produce nonsense.

// InspectOptions selects what Inspect returns. Exactly one of Issue /
// Feature / Document is non-empty when picking a single record;
// leaving all three empty produces a per-prefix summary.
type InspectOptions struct {
	Prefix   string
	Issue    string // issue label, e.g. "MINI-7" — exact match against folder name
	Feature  string // feature slug
	Document string // document filename
}

// InspectResult is the structured outcome of one Inspect call. Exactly
// one of the four pointer fields is populated.
type InspectResult struct {
	SyncRepo string `json:"sync_repo"`
	Prefix   string `json:"prefix"`

	RepoSummary *InspectRepoSummary `json:"repo_summary,omitempty"`
	Issue       *InspectIssue       `json:"issue,omitempty"`
	Feature     *InspectFeature     `json:"feature,omitempty"`
	Document    *InspectDocument    `json:"document,omitempty"`
}

// InspectRepoSummary is what `bacio sync inspect <prefix>` (no flags)
// returns: a parsed repo.yaml plus per-kind counts and a list of
// recent renumbers/renames driven from the redirects.yaml file. We
// cap the recent-redirects list so a long history doesn't drown the
// summary.
type InspectRepoSummary struct {
	Repo            *ParsedRepo `json:"repo"`
	Issues          int         `json:"issues"`
	Features        int         `json:"features"`
	Documents       int         `json:"documents"`
	Comments        int         `json:"comments"`
	RecentRedirects []Redirect  `json:"recent_redirects,omitempty"`
}

// InspectIssue carries the parsed YAML and the loaded body so the
// human-text renderer can produce a clean read-out without re-parsing
// markdown. Comments are surfaced too — they're cheap and a common
// thing to want when debugging.
type InspectIssue struct {
	Issue       *ParsedIssue      `json:"issue"`
	Description string            `json:"description"`
	Comments    []*InspectComment `json:"comments,omitempty"`
}

// InspectComment is one comment with its body.
type InspectComment struct {
	Comment *ParsedComment `json:"comment"`
	Body    string         `json:"body"`
}

// InspectFeature is the parsed feature.yaml plus its description body.
type InspectFeature struct {
	Feature     *ParsedFeature `json:"feature"`
	Description string         `json:"description"`
}

// InspectDocument is the parsed doc.yaml plus its content.
type InspectDocument struct {
	Document *ParsedDocument `json:"document"`
	Content  string          `json:"content"`
}

// recentRedirectsCap is how many redirect entries the per-prefix
// summary surfaces. The full list is always available in
// redirects.yaml on disk; this is just a sanity-glance.
const recentRedirectsCap = 10

// Inspect walks the sync repo for `opts.Prefix` and returns the
// requested view. Returns a Go error on missing prefix, missing
// record, or unreadable filesystem; per-record parse problems
// propagate as errors here (unlike Verify, where they're findings)
// because Inspect is "show me one specific thing" — if the parse
// fails, there's nothing to show.
func (e *Engine) Inspect(ctx context.Context, syncRepoRoot string, opts InspectOptions) (*InspectResult, error) {
	if syncRepoRoot == "" {
		return nil, fmt.Errorf("sync.Inspect: syncRepoRoot is empty")
	}
	if !IsSyncRepo(syncRepoRoot) {
		return nil, fmt.Errorf("sync.Inspect: %s is not a sync repo (no bacio-sync.yaml at root)", syncRepoRoot)
	}
	if opts.Prefix == "" {
		return nil, fmt.Errorf("sync.Inspect: prefix is required")
	}

	res := &InspectResult{SyncRepo: syncRepoRoot, Prefix: opts.Prefix}

	// At most one of {Issue, Feature, Document} should be set; we
	// don't enforce strictly because the CLI cobra layer handles
	// flag exclusivity, but the dispatch order below picks the first
	// non-empty.
	switch {
	case opts.Issue != "":
		ir, err := inspectIssue(syncRepoRoot, opts.Prefix, opts.Issue)
		if err != nil {
			return nil, err
		}
		res.Issue = ir
		return res, nil
	case opts.Feature != "":
		fr, err := inspectFeature(syncRepoRoot, opts.Prefix, opts.Feature)
		if err != nil {
			return nil, err
		}
		res.Feature = fr
		return res, nil
	case opts.Document != "":
		dr, err := inspectDocument(syncRepoRoot, opts.Prefix, opts.Document)
		if err != nil {
			return nil, err
		}
		res.Document = dr
		return res, nil
	}
	// No target → per-prefix summary.
	sum, err := inspectSummary(syncRepoRoot, opts.Prefix)
	if err != nil {
		return nil, err
	}
	res.RepoSummary = sum
	return res, nil
}

func inspectSummary(syncRepoRoot, prefix string) (*InspectRepoSummary, error) {
	repoYAMLPath := filepath.Join(syncRepoRoot, filepath.FromSlash(RepoYAMLFile(prefix)))
	repoBytes, err := os.ReadFile(repoYAMLPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no repo.yaml under repos/%s", prefix)
		}
		return nil, fmt.Errorf("read repo.yaml: %w", err)
	}
	parsedRepo, err := ParseRepoYAML(repoBytes)
	if err != nil {
		return nil, err
	}
	out := &InspectRepoSummary{Repo: parsedRepo}

	// Counts — cheap directory walks, no parsing.
	out.Issues = countDirsUnder(filepath.Join(syncRepoRoot, "repos", prefix, "issues"))
	out.Features = countDirsUnder(filepath.Join(syncRepoRoot, "repos", prefix, "features"))
	out.Documents = countDirsUnder(filepath.Join(syncRepoRoot, "repos", prefix, "docs"))
	// Comments require descending one level into each issue folder.
	out.Comments = countCommentsForPrefix(syncRepoRoot, prefix)

	// Redirects: load + take the most recent N (the file is
	// chronologically sorted so we just slice the tail).
	rs, err := LoadRedirects(syncRepoRoot, prefix)
	if err != nil {
		return nil, err
	}
	if len(rs) > 0 {
		// Stable copy in case downstream mutates.
		sort.Slice(rs, func(i, j int) bool {
			return rs[i].ChangedAt.After(rs[j].ChangedAt)
		})
		if len(rs) > recentRedirectsCap {
			rs = rs[:recentRedirectsCap]
		}
		out.RecentRedirects = rs
	}
	return out, nil
}

func inspectIssue(syncRepoRoot, prefix, label string) (*InspectIssue, error) {
	folder := filepath.Join("repos", prefix, "issues", label)
	yamlPath := IssueYAMLFile(folder)
	yamlAbs := filepath.Join(syncRepoRoot, filepath.FromSlash(yamlPath))
	yamlBytes, err := os.ReadFile(yamlAbs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("issue %s not found under repos/%s/issues", label, prefix)
		}
		return nil, fmt.Errorf("read issue.yaml: %w", err)
	}
	parsed, err := ParseIssueYAML(yamlBytes)
	if err != nil {
		return nil, err
	}
	bodyAbs := filepath.Join(syncRepoRoot, filepath.FromSlash(IssueDescriptionFile(folder)))
	body, _ := os.ReadFile(bodyAbs) // missing description.md → empty body
	out := &InspectIssue{
		Issue:       parsed,
		Description: string(NormalizeBody(body)),
	}
	// Comments.
	commentsDir := filepath.Join(syncRepoRoot, filepath.FromSlash(folder), "comments")
	entries, err := os.ReadDir(commentsDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read comments dir: %w", err)
	}
	// Sort alphabetically — the timestamp-prefixed filenames yield
	// chronological order naturally.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		yamlPath := filepath.Join(commentsDir, entry.Name())
		yamlBytes, err := os.ReadFile(yamlPath)
		if err != nil {
			return nil, fmt.Errorf("read comment yaml: %w", err)
		}
		parsed, err := ParseCommentYAML(yamlBytes)
		if err != nil {
			return nil, fmt.Errorf("parse comment %s: %w", entry.Name(), err)
		}
		mdPath := strings.TrimSuffix(yamlPath, ".yaml") + ".md"
		body, _ := os.ReadFile(mdPath)
		out.Comments = append(out.Comments, &InspectComment{
			Comment: parsed,
			Body:    string(NormalizeBody(body)),
		})
	}
	return out, nil
}

func inspectFeature(syncRepoRoot, prefix, slug string) (*InspectFeature, error) {
	folder := FeatureFolder(prefix, slug)
	yamlPath := FeatureYAMLFile(folder)
	yamlAbs := filepath.Join(syncRepoRoot, filepath.FromSlash(yamlPath))
	yamlBytes, err := os.ReadFile(yamlAbs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("feature %s not found under repos/%s/features", slug, prefix)
		}
		return nil, fmt.Errorf("read feature.yaml: %w", err)
	}
	parsed, err := ParseFeatureYAML(yamlBytes)
	if err != nil {
		return nil, err
	}
	bodyAbs := filepath.Join(syncRepoRoot, filepath.FromSlash(FeatureDescriptionFile(folder)))
	body, _ := os.ReadFile(bodyAbs)
	return &InspectFeature{
		Feature:     parsed,
		Description: string(NormalizeBody(body)),
	}, nil
}

func inspectDocument(syncRepoRoot, prefix, filename string) (*InspectDocument, error) {
	folder := DocumentFolder(prefix, filename)
	yamlPath := DocumentYAMLFile(folder)
	yamlAbs := filepath.Join(syncRepoRoot, filepath.FromSlash(yamlPath))
	yamlBytes, err := os.ReadFile(yamlAbs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("document %s not found under repos/%s/docs", filename, prefix)
		}
		return nil, fmt.Errorf("read doc.yaml: %w", err)
	}
	parsed, err := ParseDocumentYAML(yamlBytes)
	if err != nil {
		return nil, err
	}
	bodyAbs := resolveDocBody(syncRepoRoot, folder, filename)
	body, _ := os.ReadFile(bodyAbs)
	return &InspectDocument{
		Document: parsed,
		Content:  string(NormalizeBody(body)),
	}, nil
}

// countDirsUnder counts the immediate-child directories. Returns 0 on
// any error (missing dir, permission denied) — Inspect's summary is a
// best-effort glance, not a verifier.
func countDirsUnder(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() {
			count++
		}
	}
	return count
}

// countCommentsForPrefix walks every issue's comments/ subdir and
// counts the .yaml files. Best-effort.
func countCommentsForPrefix(syncRepoRoot, prefix string) int {
	issuesDir := filepath.Join(syncRepoRoot, "repos", prefix, "issues")
	entries, err := os.ReadDir(issuesDir)
	if err != nil {
		return 0
	}
	total := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		commentsDir := filepath.Join(issuesDir, entry.Name(), "comments")
		commentEntries, err := os.ReadDir(commentsDir)
		if err != nil {
			continue
		}
		for _, ce := range commentEntries {
			if !ce.IsDir() && strings.HasSuffix(ce.Name(), ".yaml") {
				total++
			}
		}
	}
	return total
}
