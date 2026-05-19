package inputs

// DocAddInput is the payload for `bacio doc add --json` (and `bacio doc upsert
// --json`, which shares the same shape). Either filename or source_path is
// required; type is required unless source_path is in a directory whose
// convention determines a type.
type DocAddInput struct {
	Filename   string `json:"filename,omitempty"`
	Type       string `json:"type,omitempty"`
	Content    string `json:"content"`
	SourcePath string `json:"source_path,omitempty"`
}

// DocEditInput is the payload for `bacio doc edit --json`.
//
//   - type    absent = no change; "" or null = invalid
//   - content absent = no change; "" or null = clear
type DocEditInput struct {
	Filename string  `json:"filename"`
	Type     *string `json:"type,omitempty"`
	Content  *string `json:"content,omitempty"`
}

// DocRenameInput is the payload for `bacio doc rename --json`.
type DocRenameInput struct {
	OldFilename string `json:"old_filename"`
	NewFilename string `json:"new_filename"`
	Type        string `json:"type,omitempty"`
}

// DocRmInput is the payload for `bacio doc rm --json`.
type DocRmInput struct {
	Filename string `json:"filename"`
}

// DocLinkInput is the payload for `bacio doc link --json`. Exactly one of
// issue_key or feature_slug must be set.
type DocLinkInput struct {
	Filename    string `json:"filename"`
	IssueKey    string `json:"issue_key,omitempty"`
	FeatureSlug string `json:"feature_slug,omitempty"`
	Description string `json:"description,omitempty"`
}

// DocUnlinkInput is the payload for `bacio doc unlink --json`. Exactly one of
// issue_key or feature_slug must be set.
type DocUnlinkInput struct {
	Filename    string `json:"filename"`
	IssueKey    string `json:"issue_key,omitempty"`
	FeatureSlug string `json:"feature_slug,omitempty"`
}

// DocExportInput is the payload for `bacio doc export --json`. Exactly one of
// to or to_path must be set: `to` writes to a repo-relative path, `to_path`
// uses the document's stored source_path.
type DocExportInput struct {
	Filename string `json:"filename"`
	To       string `json:"to,omitempty"`
	ToPath   bool   `json:"to_path,omitempty"`
}

// DocArchiveInput is the payload for `bacio doc archive --json`
// (BACI-68).
type DocArchiveInput struct {
	Filename string `json:"filename"`
}

// DocUnarchiveInput is the payload for `bacio doc unarchive --json`
// (BACI-68).
type DocUnarchiveInput struct {
	Filename string `json:"filename"`
}
