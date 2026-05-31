package store

import (
	"database/sql"
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// AddProxyRequestIn is the validated tuple AddProxyRequest consumes — one
// BACI-302 transport-level observation of a request that flowed through the
// reverse proxy. Method/Host/Path are the request-line fields; Status is the
// upstream response status (0 when the round-trip failed before any
// response); BytesIn/BytesOut are the request/response body sizes; Duration
// is the round-trip wall time; RawLogPath points at the raw req/resp capture
// on disk (empty when the capture file couldn't be written). StartedAt /
// EndedAt bracket the round-trip.
type AddProxyRequestIn struct {
	Method     string
	Host       string
	Path       string
	Status     int
	BytesIn    int64
	BytesOut   int64
	Duration   time.Duration
	RawLogPath string
	StartedAt  time.Time
	EndedAt    time.Time
	// BACI-305 classification (transport-level, set by the recorder):
	// ContentType is the response Content-Type (clamped), IsStream marks an
	// SSE response, IsAnthropic marks an Anthropic message-API capture.
	ContentType string
	IsStream    bool
	IsAnthropic bool
	// Correlation, lifted from Claude Code's own request headers: SessionID is
	// X-Claude-Code-Session-Id (the supervisor session), ClaudeAgentID is
	// X-Claude-Code-Agent-Id (the per-subagent id). DispatchID is resolved from
	// the agent-id→dispatch binding (else the session's active dispatch). All
	// empty/nil when the header is absent or unresolved.
	SessionID     string
	ClaudeAgentID string
	DispatchID    *int64
}

// defaultProxyRequestLimit caps an unbounded ListProxyRequests query so a
// future read surface (BACI-303/304) can't pull the entire table at once.
const defaultProxyRequestLimit = 500

// AddProxyRequest records one proxied request in the cross-cutting index.
// Method/host/path are sanitised (control chars stripped, length-clamped)
// rather than rejected — a capture row is a best-effort observation, not
// user input, so it should land truncated rather than drop the request from
// the index. The returned row carries the freshly-assigned id.
func (s *Store) AddProxyRequest(in AddProxyRequestIn) (*model.ProxyRequest, error) {
	method := clampProxyField(in.Method, model.MaxProxyMethodLen)
	host := clampProxyField(in.Host, model.MaxProxyHostLen)
	path := clampProxyField(in.Path, model.MaxProxyPathLen)
	rawLogPath := clampProxyField(in.RawLogPath, model.MaxProxyRawLogPathLen)
	contentType := clampProxyField(in.ContentType, model.MaxProxyContentTypeLen)
	sessionID := clampProxyField(in.SessionID, model.MaxProxySessionIDLen)
	claudeAgentID := clampProxyField(in.ClaudeAgentID, model.MaxProxyAgentIDLen)

	started := in.StartedAt
	if started.IsZero() {
		started = time.Now()
	}
	ended := in.EndedAt
	if ended.IsZero() {
		ended = started.Add(in.Duration)
	}
	durationMS := in.Duration.Milliseconds()
	if durationMS < 0 {
		durationMS = 0
	}

	res, err := s.DB.Exec(`
		INSERT INTO proxy_requests
		    (method, host, path, status, bytes_in, bytes_out,
		     duration_ms, raw_log_path, started_at, ended_at,
		     content_type, is_stream, is_anthropic, session_id,
		     claude_agent_id, dispatch_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		method, host, path, in.Status, in.BytesIn, in.BytesOut,
		durationMS, rawLogPath, started.UTC(), ended.UTC(),
		contentType, proxyBit(in.IsStream), proxyBit(in.IsAnthropic),
		sessionID, claudeAgentID, in.DispatchID,
	)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetProxyRequest(id)
}

// clampProxyField strips disallowed control characters and clamps the value
// to max bytes. A capture field is machine-generated transport data, not
// free text — we never reject it, we just make it safe to store and render.
func clampProxyField(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if isDisallowedControlSingle(r) {
			return -1
		}
		return r
	}, s)
	if len(s) > max {
		s = s[:max]
	}
	return s
}

// proxyBit renders a Go bool as the 0/1 INTEGER the proxy_requests
// classification columns store — matching the store-wide convention
// (e.g. features.state_manual) of int columns scanned back with `!= 0`.
func proxyBit(b bool) int {
	if b {
		return 1
	}
	return 0
}

const proxyRequestSelect = `
	SELECT id, method, host, path, status, bytes_in, bytes_out,
	       duration_ms, raw_log_path, started_at, ended_at,
	       content_type, is_stream, is_anthropic, session_id,
	       claude_agent_id, dispatch_id
	FROM proxy_requests`

// GetProxyRequest fetches one row by primary key, or ErrNotFound.
func (s *Store) GetProxyRequest(id int64) (*model.ProxyRequest, error) {
	row := s.DB.QueryRow(proxyRequestSelect+` WHERE id = ?`, id)
	pr, err := scanProxyRequest(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return pr, err
}

// ListProxyRequests returns proxy-request rows newest-first, capped at limit
// (<= 0 falls back to defaultProxyRequestLimit). The read surfaces that drive
// per-FQDN aggregation (BACI-303) and the Monitor screen (BACI-304) build on
// this; in BACI-302 it backs the tests only.
func (s *Store) ListProxyRequests(limit int) ([]*model.ProxyRequest, error) {
	if limit <= 0 {
		limit = defaultProxyRequestLimit
	}
	rows, err := s.DB.Query(proxyRequestSelect+` ORDER BY started_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.ProxyRequest
	for rows.Next() {
		pr, err := scanProxyRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, pr)
	}
	return out, rows.Err()
}

func scanProxyRequest(r rowScanner) (*model.ProxyRequest, error) {
	var v model.ProxyRequest
	var isStream, isAnthropic int
	var dispatchID sql.NullInt64
	if err := r.Scan(
		&v.ID, &v.Method, &v.Host, &v.Path, &v.Status,
		&v.BytesIn, &v.BytesOut, &v.DurationMS, &v.RawLogPath,
		&v.StartedAt, &v.EndedAt,
		&v.ContentType, &isStream, &isAnthropic, &v.SessionID,
		&v.ClaudeAgentID, &dispatchID,
	); err != nil {
		return nil, err
	}
	v.IsStream = isStream != 0
	v.IsAnthropic = isAnthropic != 0
	if dispatchID.Valid {
		id := dispatchID.Int64
		v.DispatchID = &id
	}
	return &v, nil
}

// ProxyStatsFilter parameterises the BACI-303 per-FQDN aggregation.
// Since (inclusive lower bound on started_at) bounds the window the
// rollup covers; nil means no lower bound. Limit caps how many rows the
// scan folds in — <= 0 falls back to defaultProxyStatsLimit. The window
// keeps the in-Go percentile pass cheap: this is one developer's agent
// traffic, not a public service.
type ProxyStatsFilter struct {
	Since *time.Time
	Limit int
}

// defaultProxyStatsLimit caps the per-FQDN aggregation scan. It is more
// generous than the list-oriented defaultProxyRequestLimit because the
// percentiles want a representative sample per host, and the in-Go fold
// over a few thousand rows is still cheap for a single-dev workload. A
// caller wanting the whole (retention-bounded) table passes a larger
// explicit Limit.
const defaultProxyStatsLimit = 5000

// proxyStatsAccum holds the per-host fold state while ProxyStatsByFQDN
// scans the windowed rows.
type proxyStatsAccum struct {
	count     int64
	bytesIn   int64
	bytesOut  int64
	errCount  int64
	first     time.Time
	last      time.Time
	durations []int64
}

// ProxyStatsByFQDN rolls proxy_requests rows up into one
// model.ProxyFQDNStat per distinct host (the BACI-303 read surface).
// The cheap aggregates (count, byte sums, error count, first/last seen)
// and the duration buckets come from a single ORDER BY host, duration_ms
// scan; the percentiles have no portable SQLite function under
// modernc.org/sqlite, so they're computed in Go off the per-host bucket.
// The result is ordered busiest-first (RequestCount desc, host asc
// tiebreak) and is always non-nil (an empty table yields an empty slice).
func (s *Store) ProxyStatsByFQDN(f ProxyStatsFilter) ([]*model.ProxyFQDNStat, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = defaultProxyStatsLimit
	}
	q := `SELECT host, status, duration_ms, bytes_in, bytes_out, started_at
	      FROM proxy_requests`
	var args []any
	if f.Since != nil {
		q += ` WHERE started_at >= ?`
		args = append(args, f.Since.UTC().Format("2006-01-02 15:04:05"))
	}
	// ORDER BY host, duration_ms so each host's durations arrive already
	// sorted — the percentile helper just indexes the bucket.
	q += ` ORDER BY host, duration_ms LIMIT ?`
	args = append(args, limit)

	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	accum := map[string]*proxyStatsAccum{}
	for rows.Next() {
		var (
			host       string
			status     int
			durationMS int64
			bytesIn    int64
			bytesOut   int64
			startedAt  time.Time
		)
		if err := rows.Scan(&host, &status, &durationMS, &bytesIn, &bytesOut, &startedAt); err != nil {
			return nil, err
		}
		a := accum[host]
		if a == nil {
			a = &proxyStatsAccum{first: startedAt, last: startedAt}
			accum[host] = a
		}
		a.count++
		a.bytesIn += bytesIn
		a.bytesOut += bytesOut
		if status >= 400 || status == 0 {
			a.errCount++
		}
		if startedAt.Before(a.first) {
			a.first = startedAt
		}
		if startedAt.After(a.last) {
			a.last = startedAt
		}
		a.durations = append(a.durations, durationMS)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]*model.ProxyFQDNStat, 0, len(accum))
	for host, a := range accum {
		stat := &model.ProxyFQDNStat{
			Host:         host,
			RequestCount: a.count,
			BytesIn:      a.bytesIn,
			BytesOut:     a.bytesOut,
			ErrorCount:   a.errCount,
			FirstSeen:    a.first,
			LastSeen:     a.last,
			P50MS:        percentile(a.durations, 0.50),
			P95MS:        percentile(a.durations, 0.95),
		}
		if a.count > 0 {
			stat.ErrorRate = float64(a.errCount) / float64(a.count)
		}
		out = append(out, stat)
	}
	// Busiest FQDN first; deterministic host tiebreak so equal-volume
	// hosts have a stable order.
	sort.Slice(out, func(i, j int) bool {
		if out[i].RequestCount != out[j].RequestCount {
			return out[i].RequestCount > out[j].RequestCount
		}
		return out[i].Host < out[j].Host
	})
	return out, nil
}

// percentile returns the q-th percentile (0 <= q <= 1) of sorted, using
// the nearest-rank method. The caller passes a slice already sorted
// ascending; an empty slice yields 0.
func percentile(sorted []int64, q float64) int64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	// Nearest-rank: rank = ceil(q * n), 1-based, clamped to [1, n].
	rank := int(math.Ceil(q * float64(n)))
	if rank < 1 {
		rank = 1
	}
	if rank > n {
		rank = n
	}
	return sorted[rank-1]
}
