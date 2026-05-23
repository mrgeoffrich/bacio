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
	// BACI-115 — typed attachments. `attach_transcript` and the per-mode
	// dispatch templates set these at attach time so `bacio issue brief`
	// can decide whether to inline a doc's body or surface metadata only.
	// The canonical stored form uses underscores (the DB CHECK and
	// ParseDocumentType's output are underscore-cased); the dash form
	// (e.g. `--type rendered-transcript`) still parses on input.
	DocTypePlan               DocumentType = "plan"
	DocTypeTranscript         DocumentType = "transcript"
	DocTypeRenderedTranscript DocumentType = "rendered_transcript"
	DocTypeReview             DocumentType = "review"
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
	DocTypePlan,
	DocTypeTranscript,
	DocTypeRenderedTranscript,
	DocTypeReview,
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

// DocTypeInlinedInBrief reports whether `bacio issue brief` should
// inline the body of a doc of this type. Today only `plan` and
// `review` docs are inlined; transcripts (which can run to MBs after a
// full plan → implement → ship cycle) and every other type are
// surfaced as metadata only. See BACI-115.
func DocTypeInlinedInBrief(t DocumentType) bool {
	switch t {
	case DocTypePlan, DocTypeReview:
		return true
	}
	return false
}

// IsBacioTranscriptFilename reports whether the filename matches the
// `attach_transcript` naming convention — `bacio-transcript-<KEY>-agent-<id>.jsonl`.
// Used as a read-side fallback (BACI-115) so transcripts attached
// before the `transcript` type existed (i.e. still stored as
// `project_complete`) are never inlined in `bacio issue brief`.
func IsBacioTranscriptFilename(name string) bool {
	name = strings.TrimSpace(name)
	if !strings.HasPrefix(name, "bacio-transcript-") {
		return false
	}
	if !strings.HasSuffix(name, ".jsonl") {
		return false
	}
	return strings.Contains(name, "-agent-")
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
	// ArchivedAt (BACI-68) is non-nil iff the document is archived —
	// hidden from default lists, but the row and its links remain.
	// The auto-sweep stamps it when every linked parent (issue and/or
	// feature) is archived (and the doc had at least one link); docs
	// with zero links are NOT orphans and stay visible. Manual
	// `bacio doc archive` / `unarchive` writes or clears it on demand.
	ArchivedAt *time.Time `json:"archived_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
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
