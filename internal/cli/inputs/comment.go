package inputs

// CommentAddInput is the payload for `bacio comment add --json`.
type CommentAddInput struct {
	IssueKey string `json:"issue_key"`
	Author   string `json:"author"`
	Body     string `json:"body"`
}
