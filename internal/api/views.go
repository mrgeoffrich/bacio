// Package api transport-only composite types. The CLI keeps its own
// equivalents (in internal/cli/output.go and the *_preview structs in
// the relevant cli/*.go files) for text rendering; both surfaces emit
// the same JSON shape so consumers don't need a per-surface parser.
package api

import (
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

type IssueView struct {
	Issue        *model.Issue          `json:"issue"`
	Comments     []*model.Comment      `json:"comments"`
	Relations    *store.IssueRelations `json:"relations"`
	PullRequests []*model.PullRequest  `json:"pull_requests"`
	Documents    []*model.DocumentLink `json:"documents"`
	// Claimants is the per-issue agent-claim history (open + released,
	// newest first); Taken is the derived "an agent is actively holding
	// this" signal — true iff Claimants has an open claim.
	Claimants []*model.AgentClaim `json:"claimants"`
	Taken     bool                `json:"taken"`
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

// FeatureView mirrors internal/cli/output.go:featureView so JSON consumers
// see the same shape from `bacio feature show -o json` and the API.
type FeatureView struct {
	Feature   *model.Feature        `json:"feature"`
	Issues    []*model.Issue        `json:"issues"`
	Documents []*model.DocumentLink `json:"documents"`
}

// FeatureDeletePreview mirrors internal/cli/feature.go:featureDeletePreview.
// IssuesUnlinked counts issues that would have feature_id set to NULL (the
// schema cascades via SET NULL); DocumentLinks counts document_links rows
// that would actually be removed.
type FeatureDeletePreview struct {
	Feature        *model.Feature `json:"feature"`
	WouldDelete    bool           `json:"would_delete"`
	IssuesUnlinked int            `json:"issues_unlinked"`
	DocumentLinks  int            `json:"document_links"`
}

// PlanEntry mirrors internal/cli/plan.go:planEntry.
type PlanEntry struct {
	Key       string      `json:"key"`
	Title     string      `json:"title"`
	State     model.State `json:"state"`
	Assignee  string      `json:"assignee,omitempty"`
	BlockedBy []string    `json:"blocked_by,omitempty"`
}

// PlanView mirrors internal/cli/plan.go:planView.
type PlanView struct {
	Feature string      `json:"feature"`
	Order   []PlanEntry `json:"order"`
}

// IssueBrief mirrors internal/cli/issue.go:issueBrief — bulk-context JSON
// payload for an issue (issue + feature + linked docs + comments + relations
// + PRs) collapsed into one structured response.
type IssueBrief struct {
	Issue        *model.Issue          `json:"issue"`
	Feature      *model.Feature        `json:"feature,omitempty"`
	Relations    *store.IssueRelations `json:"relations"`
	PullRequests []*model.PullRequest  `json:"pull_requests"`
	Documents    []*BriefDoc           `json:"documents"`
	Comments     []*model.Comment      `json:"comments"`
	// Claimants is the per-issue agent-claim history (open + released,
	// newest first), each carrying the prompt that session ran; Taken is
	// the derived "an agent is actively holding this" signal.
	Claimants []*model.AgentClaim `json:"claimants"`
	Taken     bool                `json:"taken"`
	Warnings  []string            `json:"warnings"`
}

// BriefDoc mirrors internal/cli/issue.go:briefDoc — one linked document
// with its full content inlined and an attribution path captured in
// LinkedVia.
type BriefDoc struct {
	Filename    string             `json:"filename"`
	Type        model.DocumentType `json:"type"`
	Description string             `json:"description,omitempty"`
	SourcePath  string             `json:"source_path,omitempty"`
	LinkedVia   []string           `json:"linked_via"`
	Content     string             `json:"content"`
}

// ClaimResult mirrors internal/cli/issue.go:claimResult. Issue is nil when
// no work is currently claimable so JSON consumers see {"issue": null}.
type ClaimResult struct {
	Issue *model.Issue `json:"issue"`
}

// DocView mirrors internal/cli/output.go:docView so JSON consumers see the
// same shape from `bacio doc show -o json` and `GET /documents/{filename}`.
type DocView struct {
	Document *model.Document       `json:"document"`
	Links    []*model.DocumentLink `json:"links"`
}

// DocumentCascadeCount counts the link rows that would be removed alongside
// a document delete. Issue and feature link rows live in the same table —
// kept separate here to match the cascade preview rendered for issue
// deletes (CascadeCount) so consumers can pick out either kind.
type DocumentCascadeCount struct {
	IssueLinks   int `json:"issue_links"`
	FeatureLinks int `json:"feature_links"`
}

// DocumentDeletePreview is the dry-run payload for DELETE /documents/{filename}.
type DocumentDeletePreview struct {
	Document    *model.Document      `json:"document"`
	Cascade     DocumentCascadeCount `json:"cascade"`
	WouldDelete bool                 `json:"would_delete"`
}

// DocumentUnlinkPreview is the dry-run payload for DELETE /documents/{filename}/links.
type DocumentUnlinkPreview struct {
	Filename    string `json:"filename"`
	Target      string `json:"target"`
	WouldRemove int    `json:"would_remove"`
}
