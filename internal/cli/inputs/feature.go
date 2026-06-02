package inputs

// FeatureAddInput is the payload for `bacio feature add --json`.
type FeatureAddInput struct {
	Title       string `json:"title"`
	Slug        string `json:"slug,omitempty"`
	Description string `json:"description,omitempty"`
	// Emoji (BACI-172) is an optional single-glyph "feature brand" the
	// kanban renders in the top-left of every card under this feature.
	// Empty (the default) = no glyph. Validated at the store boundary
	// to be either empty or exactly one grapheme cluster.
	Emoji string `json:"emoji,omitempty"`
	// BranchName (BACI-225) is the per-feature integration branch.
	// Empty (the default) keeps the legacy behaviour: every issue
	// under the feature ships straight to main, single global ship-it
	// concurrency slot. A non-empty value follows git refname rules
	// (no whitespace, no `..`, no leading `-`, etc.) and is the
	// foundation for the BACI-226+ follow-ons.
	BranchName string `json:"branch_name,omitempty"`
}

// FeatureEditInput is the payload for `bacio feature edit --json`.
//
//   - title       absent = no change; "" or null = invalid
//   - description absent = no change; "" or null = clear
//   - emoji       absent = no change; "" = clear; null = clear
//   - branch_name absent = no change; "" = clear; null = clear
type FeatureEditInput struct {
	Slug        string  `json:"slug"`
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
	// Emoji (BACI-172) — pointer + presence map distinguishes "absent"
	// (leave unchanged) from "explicit empty" (clear the glyph).
	Emoji *string `json:"emoji,omitempty"`
	// BranchName (BACI-225) — same pointer-plus-presence dance as
	// Emoji: nil = no change; non-nil empty = clear (back to NULL,
	// "ship to main"); non-nil non-empty = set + validate.
	BranchName *string `json:"branch_name,omitempty"`
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

// FeatureStateInput is the payload for `bacio feature state --json`
// (BACI-199). Both fields are required.
type FeatureStateInput struct {
	Slug  string `json:"slug"`
	State string `json:"state"`
}

// FeatureAutoCloseInput is the payload for `bacio feature auto-close
// --json` (BACI-250). Both fields are required — boolean uses an
// explicit value (no pointer) because the JSON schema must reject an
// absent `enabled` field rather than defaulting to false.
type FeatureAutoCloseInput struct {
	Slug    string `json:"slug"`
	Enabled bool   `json:"enabled"`
}

// FeatureHandoffsInput is the payload for `bacio feature handoffs --json`
// (BACI-333). Both fields are required — like FeatureAutoCloseInput the
// boolean uses an explicit value (no pointer) so the JSON schema rejects
// an absent `enabled` rather than defaulting to false.
type FeatureHandoffsInput struct {
	Slug    string `json:"slug"`
	Enabled bool   `json:"enabled"`
}

// FeatureCommentAddInput is the payload for `bacio feature comment add
// --json` (BACI-124). The feature-scoped mirror of CommentAddInput —
// feature_slug replaces issue_key because feature comments live under a
// slug-addressed parent.
type FeatureCommentAddInput struct {
	FeatureSlug string `json:"feature_slug"`
	Author      string `json:"author"`
	Body        string `json:"body"`
	// Kind (BACI-333) is 'note' (the default — a hand-typed comment) or
	// 'handoff' (a worker close-out note). A handoff write to a feature
	// with collect_handoffs off is dropped by the store backstop without
	// erroring; a note always inserts. Omitempty so the common note path
	// stays a two-field payload.
	Kind string `json:"kind,omitempty"`
}

// FeatureCommentRmInput is the payload for `bacio feature comment rm
// --json` (BACI-124). Feature comments are addressed by their immutable
// uuid — discoverable via `bacio feature comment list -o json`.
type FeatureCommentRmInput struct {
	FeatureSlug string `json:"feature_slug"`
	CommentUUID string `json:"comment_uuid"`
}
