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
}

// IssueEditInput is the payload for `bacio issue edit --json`. Pointer fields
// combined with the decoder's presence map let callers distinguish "field
// omitted" from "field explicitly cleared":
//
//   - title       absent = no change; "" or null = invalid (titles are required)
//   - description absent = no change; "" or null = clear
//   - feature_slug absent = no change; "" or null = detach; non-empty = set
type IssueEditInput struct {
	Key         string  `json:"key"`
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
	FeatureSlug *string `json:"feature_slug,omitempty"`
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
