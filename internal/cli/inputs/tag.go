package inputs

// TagAddInput is the payload for `bacio tag add --json`.
type TagAddInput struct {
	IssueKey string   `json:"issue_key"`
	Tags     []string `json:"tags"`
}

// TagRmInput is the payload for `bacio tag rm --json`.
type TagRmInput struct {
	IssueKey string   `json:"issue_key"`
	Tags     []string `json:"tags"`
}
