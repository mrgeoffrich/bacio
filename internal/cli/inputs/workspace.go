package inputs

// WorkspaceAddInput is the payload for `bacio workspace add --json`.
//
// A workspace is a repo row with `kind='workspace'` and no working tree
// — the home for tracked work that isn't a git checkout. Name is
// required. Prefix is optional: leave it out and bacio allocates a
// 4-character prefix from Name through exactly the same AllocatePrefix
// machinery a git registration uses, so workspaces and git repos share
// one prefix namespace (`MINI-42` is unambiguous either way).
//
// Because a workspace has no path, it can never be resolved from the
// current directory. Every subsequent command that targets it needs the
// global `--repo <PREFIX>` selector (or `$BACIO_REPO`).
type WorkspaceAddInput struct {
	Name   string `json:"name"`
	Prefix string `json:"prefix,omitempty"`
}

// WorkspaceRmInput is the payload for `bacio workspace rm --json`. It is
// `bacio repo rm` narrowed to workspaces — same backend call, same
// backend-side confirm gate — so `confirm` must equal `prefix`
// (case-insensitive) before anything is deleted. Without it the call
// returns the impact preview as an error so an agent stops and asks the
// user first. The verb refuses a git repo outright and points at
// `bacio repo rm`.
type WorkspaceRmInput struct {
	Prefix  string `json:"prefix"`
	Confirm string `json:"confirm,omitempty"`
}
