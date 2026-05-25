package inputs

// PRAttachInput is the payload for `bacio pr attach --json`.
type PRAttachInput struct {
	IssueKey string `json:"issue_key"`
	URL      string `json:"url"`
}

// PRDetachInput is the payload for `bacio pr detach --json`.
type PRDetachInput struct {
	IssueKey string `json:"issue_key"`
	URL      string `json:"url"`
}

// PRCreateInput is the payload for `bacio pr create --json` (BACI-163):
// open a GitHub PR labelled `bacio:<KEY>` after pre-flighting for an
// existing PR carrying the same label. GHArgs is the slice of args
// forwarded verbatim to `gh pr create` (the JSON-driven equivalent of
// the shell `-- --title foo --body bar` passthrough); `--label` is
// injected by the wrapper and must not appear in GHArgs.
type PRCreateInput struct {
	IssueKey string   `json:"issue_key"`
	GHArgs   []string `json:"gh_args,omitempty"`
	Force    bool     `json:"force,omitempty"`
}
