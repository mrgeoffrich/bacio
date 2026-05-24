package inputs

// FeatureAddInput is the payload for `bacio feature add --json`.
type FeatureAddInput struct {
	Title       string `json:"title"`
	Slug        string `json:"slug,omitempty"`
	Description string `json:"description,omitempty"`
}

// FeatureEditInput is the payload for `bacio feature edit --json`.
//
//   - title       absent = no change; "" or null = invalid
//   - description absent = no change; "" or null = clear
type FeatureEditInput struct {
	Slug        string  `json:"slug"`
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
}

// FeatureRmInput is the payload for `bacio feature rm --json`.
type FeatureRmInput struct {
	Slug string `json:"slug"`
}

// FeatureArchiveInput is the payload for `bacio feature archive --json`
// (BACI-68).
type FeatureArchiveInput struct {
	Slug string `json:"slug"`
}

// FeatureUnarchiveInput is the payload for `bacio feature unarchive
// --json` (BACI-68).
type FeatureUnarchiveInput struct {
	Slug string `json:"slug"`
}

// FeatureCommentAddInput is the payload for `bacio feature comment add
// --json` (BACI-124). The feature-scoped mirror of CommentAddInput —
// feature_slug replaces issue_key because feature comments live under a
// slug-addressed parent.
type FeatureCommentAddInput struct {
	FeatureSlug string `json:"feature_slug"`
	Author      string `json:"author"`
	Body        string `json:"body"`
}

// FeatureCommentRmInput is the payload for `bacio feature comment rm
// --json` (BACI-124). Feature comments are addressed by their immutable
// uuid — discoverable via `bacio feature comment list -o json`.
type FeatureCommentRmInput struct {
	FeatureSlug string `json:"feature_slug"`
	CommentUUID string `json:"comment_uuid"`
}
