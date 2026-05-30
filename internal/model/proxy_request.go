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
	MaxProxyMethodLen     = 16
	MaxProxyHostLen       = 255
	MaxProxyPathLen       = 2 << 10 // 2 KiB — a long path-plus-query still fits.
	MaxProxyRawLogPathLen = 4 << 10 // 4 KiB — an absolute log-dir path.
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
