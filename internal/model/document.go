package model

import (
	"fmt"
	"strings"
	"time"
)

type DocumentType string

const (
	DocTypeUserDocs          DocumentType = "user_docs"
	DocTypeProjectInPlanning DocumentType = "project_in_planning"
	DocTypeProjectInProgress DocumentType = "project_in_progress"
	DocTypeProjectComplete   DocumentType = "project_complete"
	DocTypeVendorDocs        DocumentType = "vendor_docs"
	DocTypeArchitecture      DocumentType = "architecture"
	DocTypeDesigns           DocumentType = "designs"
	DocTypeTestingPlans      DocumentType = "testing_plans"
)

var allDocTypes = []DocumentType{
	DocTypeUserDocs,
	DocTypeProjectInPlanning,
	DocTypeProjectInProgress,
	DocTypeProjectComplete,
	DocTypeVendorDocs,
	DocTypeArchitecture,
	DocTypeDesigns,
	DocTypeTestingPlans,
}

func AllDocumentTypes() []DocumentType { return append([]DocumentType(nil), allDocTypes...) }

// ParseDocumentType accepts dashes, spaces, or underscores between words and
// is case-insensitive ("user-docs", "User Docs", "USER_DOCS" all parse).
// The returned canonical form uses underscores so it matches what the
// database CHECK constraint expects.
func ParseDocumentType(s string) (DocumentType, error) {
	norm := strings.ToLower(strings.NewReplacer(" ", "_", "-", "_").Replace(strings.TrimSpace(s)))
	for _, t := range allDocTypes {
		if string(t) == norm {
			return t, nil
		}
	}
	return "", fmt.Errorf("unknown document type %q (valid: %s)", s, strings.Join(docTypeStrings(), ", "))
}

func docTypeStrings() []string {
	out := make([]string, len(allDocTypes))
	for i, t := range allDocTypes {
		out[i] = string(t)
	}
	return out
}

type Document struct {
	ID         int64        `json:"id"`
	UUID       string       `json:"uuid"`
	RepoID     int64        `json:"repo_id"`
	Filename   string       `json:"filename"`
	Type       DocumentType `json:"type"`
	SizeBytes  int64        `json:"size_bytes"`
	Content    string       `json:"content,omitempty"` // populated only on show
	SourcePath string       `json:"source_path,omitempty"`
	CreatedAt  time.Time    `json:"created_at"`
	UpdatedAt  time.Time    `json:"updated_at"`
}

// DocumentLink describes one (document, issue|feature, description) edge.
// The "Target" field renders the linked end as a human-readable label
// (e.g. "MINI-42" or "feature/auth-rewrite") for text and JSON output.
type DocumentLink struct {
	ID               int64        `json:"id"`
	DocumentID       int64        `json:"document_id"`
	DocumentFilename string       `json:"document_filename,omitempty"`
	DocumentType     DocumentType `json:"document_type,omitempty"`
	IssueID          *int64       `json:"issue_id,omitempty"`
	IssueKey         string       `json:"issue_key,omitempty"`
	FeatureID        *int64       `json:"feature_id,omitempty"`
	FeatureSlug      string       `json:"feature_slug,omitempty"`
	Description      string       `json:"description,omitempty"`
	CreatedAt        time.Time    `json:"created_at"`
}

// Target returns a compact label for the linked end ("MINI-42" or
// "feature/auth-rewrite"), useful for both text rendering and AI consumption.
func (l DocumentLink) Target() string {
	if l.IssueKey != "" {
		return l.IssueKey
	}
	if l.FeatureSlug != "" {
		return "feature/" + l.FeatureSlug
	}
	return ""
}
