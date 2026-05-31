package model

import "time"

// Field-length caps for a BACI-302 proxy_requests index row. The method,
// host, and path are transport-level fields lifted off the request line —
// generous enough for any legitimate Anthropic URL but tight enough that a
// runaway value gets truncated rather than bloating the index. The store
// clamps over-length values rather than rejecting them: a capture row is a
// best-effort observation, not user input, so it should land truncated
// rather than drop the request from the index entirely.
const (
	MaxProxyMethodLen      = 16
	MaxProxyHostLen        = 255
	MaxProxyPathLen        = 2 << 10 // 2 KiB — a long path-plus-query still fits.
	MaxProxyRawLogPathLen  = 4 << 10 // 4 KiB — an absolute log-dir path.
	MaxProxyContentTypeLen = 255     // BACI-305 — a response Content-Type header value.
	MaxProxySessionIDLen   = 64      // a session_id (UUID-shaped) correlation key.
	MaxProxyAgentIDLen     = 64      // a Claude Code per-subagent id (hex-shaped).
)

// ProxyRequest is one BACI-302 transport-level observation of a request
// that flowed through the reverse proxy: method, upstream host, path,
// response status, byte counts each way, round-trip duration, and the
// path to the raw req/resp capture on disk. It is deliberately generic —
// no Anthropic-specific body parsing (that is BACI-305/306).
//
// The table is cross-cutting like the audit log: a proxy request can't be
// attributed to an agent session / worktree / repo today, so there is no
// repo_id FK. Rows are bounded by the BACI-302 retention prune that mirrors
// the audit log's 60-day window.
//
// Status is 0 when the upstream round-trip failed before any response (the
// proxy then surfaces a 502 to the client). BytesOut is the response body
// size streamed back to the client; BytesIn is the request body forwarded
// upstream. RawLogPath is the on-disk capture file, empty when the write
// failed (capture never blocks the request, so a write failure leaves the
// index row without a file reference rather than dropping the row).
type ProxyRequest struct {
	ID         int64     `json:"id"`
	Method     string    `json:"method"`
	Host       string    `json:"host"`
	Path       string    `json:"path"`
	Status     int       `json:"status"`
	BytesIn    int64     `json:"bytes_in"`
	BytesOut   int64     `json:"bytes_out"`
	DurationMS int64     `json:"duration_ms"`
	RawLogPath string    `json:"raw_log_path,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	EndedAt    time.Time `json:"ended_at"`
	// BACI-305 classification: ContentType is the response Content-Type
	// (base type, lowercased); IsStream marks an SSE response; IsAnthropic
	// marks a capture of the Anthropic message API. These let BACI-306's
	// per-job parser select exactly the parseable Anthropic substrate.
	ContentType  string `json:"content_type,omitempty"`
	IsStream     bool   `json:"is_stream,omitempty"`
	IsAnthropic  bool   `json:"is_anthropic,omitempty"`
	// Correlation, lifted from Claude Code's own request headers (no bacio
	// header injection). SessionID is X-Claude-Code-Session-Id — the supervisor
	// session, mapping directly to agent_sessions.session_id. ClaudeAgentID is
	// X-Claude-Code-Agent-Id — the per-subagent id (present only on a
	// Task-spawned subagent's traffic), what pins a request to a specific
	// dispatch since subagents share the session id. DispatchID is resolved
	// from the agent-id→dispatch binding (else the session's active dispatch).
	// All best-effort: empty/nil when the header is absent or unresolved.
	// DispatchID has no FK — a capture row survives the dispatch.
	SessionID     string `json:"session_id,omitempty"`
	ClaudeAgentID string `json:"claude_agent_id,omitempty"`
	DispatchID    *int64 `json:"dispatch_id,omitempty"`
	// BACI-321 terminal-failure marker: when a reparse attempt yielded no
	// proxy_messages row (a truncated / malformed capture that can never
	// parse), this is stamped so the leader-gated backfill sweep skips the
	// row instead of re-reading its file every minute. Nil until a parse
	// attempt has given up.
	ParseFailedAt *time.Time `json:"parse_failed_at,omitempty"`
}

// ProxyCaptureRow is one row of the BACI-308 filtered capture list — a
// ProxyRequest index row enriched, when the capture correlates to a dispatch,
// with that dispatch's issue key and mode so the Monitor drill-down sheet can
// show the job context (the issue-key + mode chip) at a glance without a second
// fetch per row. It is the wire shape of GET /proxy/captures; the embedded
// ProxyRequest flattens its snake_case fields inline, and IssueKey / Mode are
// empty when the row has no dispatch_id or the dispatch has since been deleted.
type ProxyCaptureRow struct {
	ProxyRequest
	IssueKey string `json:"issue_key,omitempty"`
	Mode     string `json:"mode,omitempty"`
}

// ProxyMessageMatch is one match line from the BACI-320 content search over the
// parsed proxy_messages bodies (`bacio proxy grep <text>` / GET /proxy/search) —
// a single content block whose text contains the searched substring. One capture
// can produce several matches (one per matching block), so the search emits one
// of these per matching block, not per capture: the `--limit` cap counts these
// match lines, the unit a reader counts.
//
// ProxyRequestID is the proxy_requests id (the CAPTURE column) so the reader can
// drill straight into `proxy capture <id>` / `proxy raw <id>` — closing the
// search→drill-in loop. Role is "assistant" (the reconstructed turn) or "user"
// (a request user/tool_result delta turn); Block is the block type the match was
// found in ("text"|"thinking"|"tool_use"|"tool_result"); Snippet is the matched
// block's collapsed, capped text (never raw JSON field names). DispatchID /
// SessionID / ClaudeAgentID are the correlation lifted from the row for the
// --dispatch / --session / --agent audit lens; empty/nil when unset.
type ProxyMessageMatch struct {
	ProxyRequestID int64  `json:"proxy_request_id"`
	DispatchID     *int64 `json:"dispatch_id,omitempty"`
	Role           string `json:"role"`
	Block          string `json:"block"`
	Snippet        string `json:"snippet"`
	SessionID      string `json:"session_id,omitempty"`
	ClaudeAgentID  string `json:"claude_agent_id,omitempty"`
}

// ProxyFQDNStat is the BACI-303 per-FQDN rollup of proxy_requests rows:
// one entry per distinct upstream host, summarising how much an agent
// session talked to it. RequestCount / BytesIn / BytesOut are simple
// sums; ErrorRate is ErrorCount / RequestCount, where an error is a row
// with Status >= 400 || Status == 0 (0 = the upstream round-trip failed
// before any response, per the BACI-302 convention — status-0 rows count
// in both the numerator and the denominator, they are not dropped).
// P50MS / P95MS are the round-trip latency percentiles in milliseconds;
// FirstSeen / LastSeen bracket the host's activity window. The JSON tags
// are snake_case and stable so BACI-304's Monitor screen can declare a
// matching DTO against them without a server round-trip change.
type ProxyFQDNStat struct {
	Host         string    `json:"host"`
	RequestCount int64     `json:"request_count"`
	BytesIn      int64     `json:"bytes_in"`
	BytesOut     int64     `json:"bytes_out"`
	ErrorCount   int64     `json:"error_count"`
	ErrorRate    float64   `json:"error_rate"`
	P50MS        int64     `json:"p50_ms"`
	P95MS        int64     `json:"p95_ms"`
	FirstSeen    time.Time `json:"first_seen"`
	LastSeen     time.Time `json:"last_seen"`
}
