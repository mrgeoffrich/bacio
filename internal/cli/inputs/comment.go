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
	// TranscriptEventRef (BACI-141) is the optional per-event anchor
	// set by the transcript viewer's per-event composer when the user
	// pins a note to a specific event inside a `.jsonl` transcript.
	// Two practical formats: `tool_use_id:<id>` (durable across re-
	// renders) and `line_index:<n>` (fallback for events without a
	// tool_use_id). Empty = unanchored, the dispatch-card-level note
	// the viewer pins to the prompt card.
	TranscriptEventRef string `json:"transcript_event_ref,omitempty"`
}

// CommentRmInput is the payload for `bacio comment rm --json`.
// Comments are addressed by their immutable uuid — discoverable via
// `bacio comment list -o json`.
type CommentRmInput struct {
	IssueKey    string `json:"issue_key"`
	CommentUUID string `json:"comment_uuid"`
}
