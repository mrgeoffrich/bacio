package inputs

// CommentAddInput is the payload for `bacio comment add --json`.
type CommentAddInput struct {
	IssueKey string `json:"issue_key"`
	Author   string `json:"author"`
	Body     string `json:"body"`
}

// CommentRmInput is the payload for `bacio comment rm --json`.
// Comments are addressed by their immutable uuid — discoverable via
// `bacio comment list -o json`.
type CommentRmInput struct {
	IssueKey    string `json:"issue_key"`
	CommentUUID string `json:"comment_uuid"`
}
