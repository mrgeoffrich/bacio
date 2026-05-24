package inputs

// CommentAddInput is the payload for `bacio comment add --json`.
type CommentAddInput struct {
	IssueKey string `json:"issue_key"`
	Author   string `json:"author"`
	Body     string `json:"body"`
	// Eval (BACI-131) marks this as a quality-review note posted from
	// the kanban card's quick-eval composer — the comment row is
	// stored with eval=true and the server pins the in-flight
	// (agent_session_id, dispatch_id, mode) onto it at write time.
	// Omit / false = a normal comment with the four context fields
	// left at zero values.
	Eval bool `json:"eval,omitempty"`
	// TranscriptEventRef (BACI-141) anchors an eval comment to a
	// specific event inside a `.jsonl` transcript. Two recognised
	// shapes: `tool_use_id:<id>` (anchor by the durable Claude Code id
	// shared between an assistant tool_use block and its user-tool-
	// result counterpart) or `line_index:<n>` (fallback for assistant /
	// dispatch / system-reminder / attachment events without a
	// tool_use_id). Empty leaves the BACI-131 dispatch-level
	// anchoring; the per-event transcript composer fills this before
	// POSTing.
	TranscriptEventRef string `json:"transcript_event_ref,omitempty"`
}

// CommentRmInput is the payload for `bacio comment rm --json`.
// Comments are addressed by their immutable uuid — discoverable via
// `bacio comment list -o json`.
type CommentRmInput struct {
	IssueKey    string `json:"issue_key"`
	CommentUUID string `json:"comment_uuid"`
}
