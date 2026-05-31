package model

import (
	"encoding/json"
	"time"
)

// Anthropic message-detail model (BACI-306). These are the parsed shape of one
// captured /v1/messages turn and the per-job transcript assembled from many.
// They are the single source of truth shared by the parser (internal/anthropic),
// the store (proxy_messages), the REST handlers, and the CLI.
//
// The JSON tags are snake_case and deliberately mirror the React transcript
// viewer's TypeScript model (desktop/frontend/src/lib/transcript/types.ts —
// AssistantBlock, ToolResult, TokenUsage). Keeping the wire shapes aligned lets
// BACI-307/308 point the existing TranscriptView at this new REST source with a
// thin seam rather than a fresh DTO. Same precedent as ProxyFQDNStat, which was
// shaped up front for the Monitor screen.

// AnthropicUsage is the merged token usage for one assistant turn. Anthropic
// splits usage across two SSE events — input/cache tokens arrive on
// message_start.message.usage and the final output_tokens (+ the thinking-token
// breakdown) on message_delta.usage — so the parser merges both into one struct.
// The viewer's TokenUsage carries input/output/cache fields with the same names.
type AnthropicUsage struct {
	InputTokens              int64 `json:"input_tokens,omitempty"`
	OutputTokens             int64 `json:"output_tokens,omitempty"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens,omitempty"`
	// ThinkingTokens is output_tokens_details.thinking_tokens off message_delta —
	// the slice of OutputTokens that was extended thinking, surfaced separately so
	// a reader can see the thinking cost without re-deriving it.
	ThinkingTokens int64 `json:"thinking_tokens,omitempty"`
}

// AnthropicBlock is one content block in a message — the union the viewer's
// AssistantBlock encodes (text | thinking | tool_use), plus the tool_result
// shape that appears in the request messages[] (a user turn replying to a prior
// tool_use). Type is the discriminator; the other fields are populated per type:
//   - "text":        Text
//   - "thinking":    Thinking
//   - "tool_use":    ID, Name, Input
//   - "tool_result": ToolUseID, Content, IsError
type AnthropicBlock struct {
	Type string `json:"type"`

	// text / thinking
	Text     string `json:"text,omitempty"`
	Thinking string `json:"thinking,omitempty"`

	// tool_use — Input is the accumulated input JSON (parsed from the streamed
	// input_json_delta fragments on the response side, or carried verbatim from
	// the request side). RawMessage so the original JSON survives round-tripping.
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// tool_result — replies to a prior tool_use by id. Content is polymorphic
	// (string or block list) so it's kept as raw JSON, mirroring the viewer's
	// ToolResultContent union.
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

// AnthropicMessage is one entry in the request messages[] array: a role
// ("user" | "assistant") and its content blocks. The request body carries the
// whole conversation-so-far as these; the per-capture delta is the slice of
// messages appended since the previous primary capture.
type AnthropicMessage struct {
	Role    string           `json:"role"`
	Content []AnthropicBlock `json:"content"`
}

// AnthropicTurn is the single assistant turn reconstructed from one capture's
// response SSE stream: the ordered content blocks the model emitted, the merged
// token usage, the stop reason, and the model name. This is the response half
// of a capture; the request half is the messages[] / delta.
type AnthropicTurn struct {
	Model      string           `json:"model,omitempty"`
	Blocks     []AnthropicBlock `json:"blocks"`
	Usage      AnthropicUsage   `json:"usage"`
	StopReason string           `json:"stop_reason,omitempty"`
}

// ParsedCapture is the full parse of one .http capture: the request-side facts
// the assembler classifies on (model, system fingerprint, message count, whether
// the request carried an output_config structured-output probe) plus the
// reconstructed assistant Turn and the request-message Delta (set by the
// assembler — the slice of messages[] appended since the previous primary
// capture for this thread).
//
// Metadata (the request's metadata.user_id, which carries device_id /
// account_uuid / session_id PII) is deliberately NOT a field — the parser never
// reads it into the model, so it can never be persisted.
type ParsedCapture struct {
	Model           string        `json:"model"`
	SystemFP        string        `json:"system_fingerprint"`
	MessageCount    int           `json:"message_count"`
	HasOutputConfig bool          `json:"has_output_config"`
	Turn            AnthropicTurn `json:"turn"`
	// Delta is the request messages appended since the previous primary capture
	// in this thread (computed by Classify). Empty on the first capture / an
	// auxiliary capture; the full request messages[] are never stored, only the
	// delta, to keep per-job storage O(n).
	Delta []AnthropicMessage `json:"delta,omitempty"`
	// Messages is the full request conversation-so-far. It is transient assembly
	// state (json:"-") — the parser fills it so Classify can compute Delta against
	// the prior thread's message count; it is never serialised or persisted (the
	// growing full body is exactly the O(n²) cost the delta avoids).
	Messages []AnthropicMessage `json:"-"`
}

// MaxProxyMessageBody caps the delta_json / turn_json a single proxy_messages
// row stores. A capture's reconstructed turn (and the user/tool_result delta
// that precedes it) can carry a large file-read tool_result; this bound keeps a
// single pathological turn from bloating the synced DB, comparable to the old
// 2.5 MB .jsonl cap per direction. Past the cap the body is replaced with a
// truncation marker so a reader knows the row is partial.
const MaxProxyMessageBody = 2 << 20 // 2 MiB per direction.

// ProxyMessage is one persisted proxy_messages row: the parsed detail of one
// captured Anthropic SSE turn, durable so a job's transcript can be assembled
// without re-reading the raw .http file. It mirrors the columns in schema.sql.
// DeltaJSON / TurnJSON are the serialised AnthropicMessage[] / AnthropicTurn
// (a truncation marker string when the body exceeded MaxProxyMessageBody).
type ProxyMessage struct {
	ID             int64          `json:"id"`
	ProxyRequestID int64          `json:"proxy_request_id"`
	DispatchID     *int64         `json:"dispatch_id,omitempty"`
	SessionID      string         `json:"session_id,omitempty"`
	ClaudeAgentID  string         `json:"claude_agent_id,omitempty"`
	Model          string         `json:"model,omitempty"`
	SystemFP       string         `json:"system_fingerprint,omitempty"`
	MessageCount   int            `json:"message_count"`
	IsPrimary      bool           `json:"is_primary"`
	StopReason     string         `json:"stop_reason,omitempty"`
	DeltaJSON      string         `json:"delta_json,omitempty"`
	TurnJSON       string         `json:"turn_json,omitempty"`
	Usage          AnthropicUsage `json:"usage"`
	StartedAt      time.Time      `json:"started_at"`
}

// AnthropicTranscript is the assembled per-job conversation: the ordered
// messages (user/tool_result turns interleaved with the assistant turns) of the
// primary thread, the summed token usage across the job, and the auxiliary turns
// (title-gen, structured-output probes) that share the dispatch but aren't part
// of the main conversation. Model is the primary thread's model.
type AnthropicTranscript struct {
	DispatchID *int64             `json:"dispatch_id,omitempty"`
	Model      string             `json:"model,omitempty"`
	Messages   []AnthropicMessage `json:"messages"`
	Usage      AnthropicUsage     `json:"usage"`
	// Auxiliary turns (one per non-primary capture) — kept so the full picture
	// is visible without polluting the primary thread. Each is one assistant
	// turn with its own usage; ordered by capture sequence.
	Auxiliary []AnthropicTurn `json:"auxiliary,omitempty"`
}
