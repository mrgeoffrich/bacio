package api_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mrgeoffrich/bacio/internal/api"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// seedProxyRow inserts one proxy_requests index row directly via the
// store — the GET /proxy/stats handler reads the aggregation off these.
func seedProxyRow(t *testing.T, s *store.Store, host string, status int, dur time.Duration) {
	t.Helper()
	if _, err := s.AddProxyRequest(store.AddProxyRequestIn{
		Method: "POST", Host: host, Path: "/v1/messages",
		Status: status, BytesIn: 100, BytesOut: 200, Duration: dur,
	}); err != nil {
		t.Fatalf("seed proxy row (%s): %v", host, err)
	}
}

// TestProxyStatsEndpoint seeds rows for two hosts and asserts the
// GET /proxy/stats JSON body groups and computes the rollup, ordered
// busiest-first.
func TestProxyStatsEndpoint(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedProxyRow(t, s, "api.anthropic.com", 200, 100*time.Millisecond)
	seedProxyRow(t, s, "api.anthropic.com", 200, 200*time.Millisecond)
	seedProxyRow(t, s, "api.anthropic.com", 500, 300*time.Millisecond)
	seedProxyRow(t, s, "example.com", 200, 50*time.Millisecond)

	resp, raw := apiGet(t, ts.URL+"/proxy/stats")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	var stats []*model.ProxyFQDNStat
	if err := json.Unmarshal(raw, &stats); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("hosts: %d, want 2: %+v", len(stats), stats)
	}
	// Busiest-first: the 3-request host leads.
	if stats[0].Host != "api.anthropic.com" {
		t.Fatalf("busiest host first: got %q", stats[0].Host)
	}
	anthropic := stats[0]
	if anthropic.RequestCount != 3 {
		t.Fatalf("request_count = %d, want 3", anthropic.RequestCount)
	}
	if anthropic.ErrorCount != 1 {
		t.Fatalf("error_count = %d, want 1 (the 500)", anthropic.ErrorCount)
	}
	if anthropic.BytesIn != 300 || anthropic.BytesOut != 600 {
		t.Fatalf("byte sums wrong: in=%d out=%d", anthropic.BytesIn, anthropic.BytesOut)
	}
	if anthropic.P50MS == 0 || anthropic.P95MS == 0 {
		t.Fatalf("percentiles unpopulated: p50=%d p95=%d", anthropic.P50MS, anthropic.P95MS)
	}
}

// TestProxyStatsEmptyArrayNotNull asserts the handler emits [] not null
// for an empty proxy_requests table.
func TestProxyStatsEmptyArrayNotNull(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, raw := apiGet(t, ts.URL+"/proxy/stats")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if len(raw) == 0 || raw[0] != '[' {
		t.Fatalf("expected [], got %s", string(raw))
	}
}

// TestProxyStatsAuthRequired: /proxy/stats sits outside the /anthropic/
// auth exemption, so a bearer-protected deployment must require a token
// (unlike the agent passthrough route).
func TestProxyStatsAuthRequired(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{Token: "secret"})
	resp, _ := apiGet(t, ts.URL+"/proxy/stats")
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

// TestProxyStatsBadLimit rejects a negative limit at the handler boundary.
func TestProxyStatsBadLimit(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, _ := apiGet(t, ts.URL+"/proxy/stats?limit=-1")
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

// TestProxyStatsSinceExcludesBackdated: a `since` lookback excludes rows
// whose started_at predates the window.
func TestProxyStatsSinceExcludesBackdated(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedProxyRow(t, s, "api.anthropic.com", 200, 10*time.Millisecond)
	stale, err := s.AddProxyRequest(store.AddProxyRequestIn{
		Method: "POST", Host: "api.anthropic.com", Path: "/v1/old", Status: 200,
	})
	if err != nil {
		t.Fatalf("add stale: %v", err)
	}
	old := time.Now().Add(-48 * time.Hour).UTC().Format("2006-01-02 15:04:05")
	if _, err := s.DB.Exec(`UPDATE proxy_requests SET started_at = ? WHERE id = ?`, old, stale.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	resp, raw := apiGet(t, ts.URL+"/proxy/stats?since=1h")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	var stats []*model.ProxyFQDNStat
	_ = json.Unmarshal(raw, &stats)
	if len(stats) != 1 || stats[0].RequestCount != 1 {
		t.Fatalf("since window wrong: %+v", stats)
	}
}
