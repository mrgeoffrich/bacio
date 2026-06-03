// Package inputs defines the JSON payload shapes accepted by every mutating
// `bacio` command. Each *Input struct is the source of truth for both the
// strict JSON decoder (in internal/cli) and the runtime schema published by
// `bacio schema`.
package inputs

// IssueAddInput is the payload for `bacio issue add --json`.
type IssueAddInput struct {
	Title       string   `json:"title"`
	FeatureSlug string   `json:"feature_slug,omitempty"`
	Description string   `json:"description,omitempty"`
	State       string   `json:"state,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	// BaseBranch (BACI-232) is the per-issue override for the branch a
	// PR for this issue is opened against. Empty (the default) keeps
	// the inherit-from-feature behaviour. A non-empty value follows the
	// same git refname rules as features.branch_name (no whitespace, no
	// `..`, no leading `-`, etc.).
	BaseBranch string `json:"base_branch,omitempty"`
	// CustomerImpact (BACI-349) is the optional single-line statement of
	// the change in the user's terms. Empty (the default) is the
	// first-class "no user-facing change" state — read surfaces fall back
	// to the title. Authored at scope-time; single-line, title-capped.
	CustomerImpact string `json:"customer_impact,omitempty"`
}

// IssueEditInput is the payload for `bacio issue edit --json`. Pointer fields
// combined with the decoder's presence map let callers distinguish "field
// omitted" from "field explicitly cleared":
//
//   - title       absent = no change; "" or null = invalid (titles are required)
//   - description absent = no change; "" or null = clear
//   - feature_slug absent = no change; "" or null = detach; non-empty = set
//   - base_branch absent = no change; "" or null = clear (inherit); non-empty = set
//   - customer_impact absent = no change; "" or null = clear (no impact); non-empty = set
type IssueEditInput struct {
	Key         string  `json:"key"`
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
	FeatureSlug *string `json:"feature_slug,omitempty"`
	// BaseBranch (BACI-232) — same pointer-plus-presence dance as the
	// other nullable fields: nil = no change; non-nil empty = clear
	// (back to NULL, inherit from feature); non-nil non-empty = set
	// + validate.
	BaseBranch *string `json:"base_branch,omitempty"`
	// CustomerImpact (BACI-349) — same pointer-plus-presence dance: nil =
	// no change; non-nil empty = clear (back to the "no impact" state);
	// non-nil non-empty = set + validate (single-line, title-capped).
	CustomerImpact *string `json:"customer_impact,omitempty"`
}

// IssueStateInput is the payload for `bacio issue state --json`.
type IssueStateInput struct {
	Key   string `json:"key"`
	State string `json:"state"`
}

// IssueAssignInput is the payload for `bacio issue assign --json`.
type IssueAssignInput struct {
	Key      string `json:"key"`
	Assignee string `json:"assignee"`
}

// IssueUnassignInput is the payload for `bacio issue unassign --json`.
type IssueUnassignInput struct {
	Key string `json:"key"`
}

// IssueNextInput is the payload for `bacio issue next --json`.
type IssueNextInput struct {
	FeatureSlug string `json:"feature_slug"`
}

// IssueRmInput is the payload for `bacio issue rm --json`.
type IssueRmInput struct {
	Key string `json:"key"`
}

// IssueArchiveInput is the payload for `bacio issue archive --json`
// (BACI-68). Archive is sticky — reopening an archived issue (state
// -> todo/in_progress/...) does NOT auto-unarchive it; the user must
// unarchive explicitly via IssueUnarchive.
type IssueArchiveInput struct {
	Key string `json:"key"`
}

// IssueUnarchiveInput is the payload for `bacio issue unarchive --json`
// (BACI-68).
type IssueUnarchiveInput struct {
	Key string `json:"key"`
}
