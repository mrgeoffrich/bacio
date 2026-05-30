package store

import (
	"strings"
	"testing"
	"time"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestAddProxyRequest_HappyPath covers the happy path: a fully-populated
// observation comes back with an id, the round-trip duration in ms, and the
// timestamps preserved.
func TestAddProxyRequest_HappyPath(t *testing.T) {
	s := newTestStore(t)
	started := time.Now().Add(-1500 * time.Millisecond).UTC().Truncate(time.Second)
	pr, err := s.AddProxyRequest(AddProxyRequestIn{
		Method:     "POST",
		Host:       "api.anthropic.com",
		Path:       "/v1/messages",
		Status:     200,
		BytesIn:    412,
		BytesOut:   8192,
		Duration:   1500 * time.Millisecond,
		RawLogPath: "/tmp/logs/proxy/2026-05-30/123.http",
		StartedAt:  started,
		EndedAt:    started.Add(1500 * time.Millisecond),
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if pr.ID == 0 {
		t.Fatalf("expected populated id, got %+v", pr)
	}
	if pr.Method != "POST" || pr.Host != "api.anthropic.com" || pr.Path != "/v1/messages" {
		t.Fatalf("request-line fields not round-tripped: %+v", pr)
	}
	if pr.Status != 200 || pr.BytesIn != 412 || pr.BytesOut != 8192 {
		t.Fatalf("status/byte counts wrong: %+v", pr)
	}
	if pr.DurationMS != 1500 {
		t.Fatalf("duration_ms = %d, want 1500", pr.DurationMS)
	}
	if pr.RawLogPath != "/tmp/logs/proxy/2026-05-30/123.http" {
		t.Fatalf("raw_log_path = %q", pr.RawLogPath)
	}
	if !pr.StartedAt.Equal(started) {
		t.Fatalf("started_at = %v, want %v", pr.StartedAt, started)
	}
}

// TestAddProxyRequest_UpstreamError records a status-0 observation — the
// shape the proxy hands the recorder when the round-trip fails before any
// response (the proxy then surfaces a 502 to the client).
func TestAddProxyRequest_UpstreamError(t *testing.T) {
	s := newTestStore(t)
	pr, err := s.AddProxyRequest(AddProxyRequestIn{
		Method:   "GET",
		Host:     "api.anthropic.com",
		Path:     "/v1/messages",
		Status:   0,
		Duration: 25 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if pr.Status != 0 {
		t.Fatalf("status = %d, want 0 for an upstream error", pr.Status)
	}
	// EndedAt defaults to StartedAt + Duration when both are zero, and
	// StartedAt defaults to now — so EndedAt must not be before StartedAt.
	if pr.EndedAt.Before(pr.StartedAt) {
		t.Fatalf("ended_at %v before started_at %v", pr.EndedAt, pr.StartedAt)
	}
}

// TestAddProxyRequest_SanitisesFields locks in that control characters are
// stripped and over-length fields are clamped rather than rejected — a
// capture row is a best-effort observation, not user input, so it lands
// sanitised rather than dropping the request from the index.
func TestAddProxyRequest_SanitisesFields(t *testing.T) {
	s := newTestStore(t)
	longPath := "/v1/" + strings.Repeat("a", model.MaxProxyPathLen)
	pr, err := s.AddProxyRequest(AddProxyRequestIn{
		Method: "PO\x00ST",
		Host:   "api.anthropic.com\x07",
		Path:   longPath,
		Status: 200,
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if strings.ContainsRune(pr.Method, 0) {
		t.Fatalf("NUL not stripped from method: %q", pr.Method)
	}
	if strings.ContainsRune(pr.Host, '\x07') {
		t.Fatalf("control char not stripped from host: %q", pr.Host)
	}
	if len(pr.Path) > model.MaxProxyPathLen {
		t.Fatalf("path not clamped: len %d, max %d", len(pr.Path), model.MaxProxyPathLen)
	}
}

// TestPruneProxyRequests deletes rows whose started_at is older than the
// retention window and keeps fresh rows.
func TestPruneProxyRequests(t *testing.T) {
	s := newTestStore(t)
	// Fresh row — must survive.
	fresh, err := s.AddProxyRequest(AddProxyRequestIn{
		Method: "POST", Host: "api.anthropic.com", Path: "/v1/messages", Status: 200,
	})
	if err != nil {
		t.Fatalf("add fresh: %v", err)
	}
	// Stale row — backdate started_at two retention windows into the past.
	stale, err := s.AddProxyRequest(AddProxyRequestIn{
		Method: "POST", Host: "api.anthropic.com", Path: "/v1/old", Status: 200,
	})
	if err != nil {
		t.Fatalf("add stale: %v", err)
	}
	old := time.Now().Add(-2 * ProxyRequestRetention).UTC().Format("2006-01-02 15:04:05")
	if _, err := s.DB.Exec(`UPDATE proxy_requests SET started_at = ? WHERE id = ?`, old, stale.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	if err := pruneProxyRequests(s.DB, ProxyRequestRetention); err != nil {
		t.Fatalf("prune: %v", err)
	}

	if _, err := s.GetProxyRequest(fresh.ID); err != nil {
		t.Fatalf("fresh row pruned — should have survived: %v", err)
	}
	if _, err := s.GetProxyRequest(stale.ID); err == nil {
		t.Fatalf("stale row survived prune — should have been deleted")
	}
}

// TestListProxyRequests returns rows newest-first.
func TestListProxyRequests(t *testing.T) {
	s := newTestStore(t)
	t0 := time.Now().Add(-2 * time.Minute)
	for i, ts := range []time.Time{t0, t0.Add(time.Minute), t0.Add(2 * time.Minute)} {
		if _, err := s.AddProxyRequest(AddProxyRequestIn{
			Method: "POST", Host: "api.anthropic.com", Path: "/v1/messages",
			Status: 200, StartedAt: ts, EndedAt: ts,
		}); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	rows, err := s.ListProxyRequests(0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	// Newest-first: each row's started_at >= the next.
	for i := 0; i+1 < len(rows); i++ {
		if rows[i].StartedAt.Before(rows[i+1].StartedAt) {
			t.Fatalf("rows not newest-first at %d: %v before %v", i, rows[i].StartedAt, rows[i+1].StartedAt)
		}
	}
}
