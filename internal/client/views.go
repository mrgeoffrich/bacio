package client

import (
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// View shapes mirror internal/api/views.go and internal/cli/output.go
// 1:1 so the JSON wire format is identical regardless of backend. The
// CLI's text renderer in internal/cli operates on its own private
// types; the local backend constructs them directly while the remote
// backend decodes JSON into the matching client.* types and the cli
// package wraps them into its private equivalents via field-by-field
// copy.

type IssueView struct {
	Issue        *model.Issue          `json:"issue"`
	Comments     []*model.Comment      `json:"comments"`
	Relations    *store.IssueRelations `json:"relations"`
	PullRequests []*model.PullRequest  `json:"pull_requests"`
	Documents    []*model.DocumentLink `json:"documents"`
}

type FeatureView struct {
	Feature   *model.Feature        `json:"feature"`
	Issues    []*model.Issue        `json:"issues"`
	Documents []*model.DocumentLink `json:"documents"`
}

type CascadeCount struct {
	Comments      int `json:"comments"`
	Relations     int `json:"relations"`
	PullRequests  int `json:"pull_requests"`
	DocumentLinks int `json:"document_links"`
	Tags          int `json:"tags"`
}

type IssueDeletePreview struct {
	Issue       *model.Issue `json:"issue"`
	Cascade     CascadeCount `json:"cascade"`
	WouldDelete bool         `json:"would_delete"`
}

type FeatureDeletePreview struct {
	Feature        *model.Feature `json:"feature"`
	WouldDelete    bool           `json:"would_delete"`
	IssuesUnlinked int            `json:"issues_unlinked"`
	DocumentLinks  int            `json:"document_links"`
}

type RelationDeletePreview struct {
	A           string `json:"a"`
	B           string `json:"b"`
	WouldRemove int    `json:"would_remove"`
}

type PRDetachPreview struct {
	IssueKey    string `json:"issue_key"`
	URL         string `json:"url"`
	WouldRemove int    `json:"would_remove"`
}

type PlanEntry struct {
	Key       string      `json:"key"`
	Title     string      `json:"title"`
	State     model.State `json:"state"`
	Assignee  string      `json:"assignee,omitempty"`
	BlockedBy []string    `json:"blocked_by,omitempty"`
}

type PlanView struct {
	Feature string      `json:"feature"`
	Order   []PlanEntry `json:"order"`
}

type IssueBrief struct {
	Issue        *model.Issue          `json:"issue"`
	Feature      *model.Feature        `json:"feature,omitempty"`
	Relations    *store.IssueRelations `json:"relations"`
	PullRequests []*model.PullRequest  `json:"pull_requests"`
	Documents    []*BriefDoc           `json:"documents"`
	Comments     []*model.Comment      `json:"comments"`
	Warnings     []string              `json:"warnings"`
}

type BriefDoc struct {
	Filename    string             `json:"filename"`
	Type        model.DocumentType `json:"type"`
	Description string             `json:"description,omitempty"`
	SourcePath  string             `json:"source_path,omitempty"`
	LinkedVia   []string           `json:"linked_via"`
	Content     string             `json:"content"`
}

type DocView struct {
	Document *model.Document       `json:"document"`
	Links    []*model.DocumentLink `json:"links"`
}

type DocumentCascadeCount struct {
	IssueLinks   int `json:"issue_links"`
	FeatureLinks int `json:"feature_links"`
}

type DocumentDeletePreview struct {
	Document    *model.Document      `json:"document"`
	Cascade     DocumentCascadeCount `json:"cascade"`
	WouldDelete bool                 `json:"would_delete"`
}

// RepoDeletePreview is what `bacio repo rm` returns:
//   - on `--dry-run`, exit 0 with this payload (no changes made);
//   - when `confirm` is missing or mismatched, the same payload is
//     bundled inside an error so the LLM driving the CLI sees the
//     full impact and the alert message before deciding whether to
//     re-run with --confirm.
type RepoDeletePreview struct {
	Repo        *model.Repo             `json:"repo"`
	Cascade     store.RepoCascadeCounts `json:"cascade"`
	WouldDelete bool                    `json:"would_delete"`
}

type DocumentUnlinkPreview struct {
	Filename    string `json:"filename"`
	Target      string `json:"target"`
	WouldRemove int    `json:"would_remove"`
}
