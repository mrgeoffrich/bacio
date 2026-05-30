package store

import (
	"database/sql"
	"errors"
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
		     duration_ms, raw_log_path, started_at, ended_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		method, host, path, in.Status, in.BytesIn, in.BytesOut,
		durationMS, rawLogPath, started.UTC(), ended.UTC(),
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

const proxyRequestSelect = `
	SELECT id, method, host, path, status, bytes_in, bytes_out,
	       duration_ms, raw_log_path, started_at, ended_at
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
	if err := r.Scan(
		&v.ID, &v.Method, &v.Host, &v.Path, &v.Status,
		&v.BytesIn, &v.BytesOut, &v.DurationMS, &v.RawLogPath,
		&v.StartedAt, &v.EndedAt,
	); err != nil {
		return nil, err
	}
	return &v, nil
}
