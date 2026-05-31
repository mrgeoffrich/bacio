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

// TestAddProxyRequest_ClassificationColumns round-trips the BACI-305
// classification + correlation columns through AddProxyRequest →
// GetProxyRequest, and confirms an unset dispatch_id reads back as nil.
func TestAddProxyRequest_ClassificationColumns(t *testing.T) {
	s := newTestStore(t)
	dispatchID := int64(4242)
	pr, err := s.AddProxyRequest(AddProxyRequestIn{
		Method:      "POST",
		Host:        "api.anthropic.com",
		Path:        "/v1/messages",
		Status:      200,
		ContentType: "text/event-stream",
		IsStream:    true,
		IsAnthropic: true,
		SessionID:   "sess-corr-1",
		DispatchID:  &dispatchID,
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	got, err := s.GetProxyRequest(pr.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ContentType != "text/event-stream" {
		t.Errorf("content_type = %q, want text/event-stream", got.ContentType)
	}
	if !got.IsStream || !got.IsAnthropic {
		t.Errorf("is_stream/is_anthropic = %v/%v, want true/true", got.IsStream, got.IsAnthropic)
	}
	if got.SessionID != "sess-corr-1" {
		t.Errorf("session_id = %q, want sess-corr-1", got.SessionID)
	}
	if got.DispatchID == nil || *got.DispatchID != 4242 {
		t.Errorf("dispatch_id = %v, want 4242", got.DispatchID)
	}

	// A capture with no correlation reads back with empty/nil columns.
	bare, err := s.AddProxyRequest(AddProxyRequestIn{
		Method: "GET", Host: "api.anthropic.com", Path: "/v1/messages", Status: 200,
	})
	if err != nil {
		t.Fatalf("add bare: %v", err)
	}
	bareGot, err := s.GetProxyRequest(bare.ID)
	if err != nil {
		t.Fatalf("get bare: %v", err)
	}
	if bareGot.IsStream || bareGot.IsAnthropic || bareGot.SessionID != "" || bareGot.DispatchID != nil {
		t.Errorf("bare row should have empty classification/correlation: %+v", bareGot)
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

// TestListProxyRequestsFiltered covers the BACI-308 drill-down read: the
// host / dispatch_id / is_anthropic / since filters narrow the rows, the
// result is newest-first, and the cap is honoured (and ceilinged).
func TestListProxyRequestsFiltered(t *testing.T) {
	s := newTestStore(t)
	t0 := time.Now().Add(-10 * time.Minute)
	d1 := int64(11)
	d2 := int64(22)

	// Three anthropic captures on host A (two correlated to d1, one to d2),
	// one non-anthropic capture on host A, and one on host B — laid down out
	// of time order so ORDER BY has work to do.
	add := func(host string, anthropic bool, dispatch *int64, at time.Time) *model.ProxyRequest {
		t.Helper()
		pr, err := s.AddProxyRequest(AddProxyRequestIn{
			Method: "POST", Host: host, Path: "/v1/messages", Status: 200,
			IsAnthropic: anthropic, DispatchID: dispatch,
			StartedAt: at, EndedAt: at,
		})
		if err != nil {
			t.Fatalf("add (%s): %v", host, err)
		}
		return pr
	}
	add("api.anthropic.com", true, &d1, t0.Add(1*time.Minute))
	add("api.anthropic.com", true, &d1, t0.Add(3*time.Minute))
	add("api.anthropic.com", true, &d2, t0.Add(2*time.Minute))
	add("api.anthropic.com", false, nil, t0.Add(4*time.Minute)) // a count_tokens-style probe
	add("example.com", false, nil, t0.Add(5*time.Minute))

	// Host filter: only api.anthropic.com rows (4 of 5), newest-first.
	hostRows, err := s.ListProxyRequestsFiltered(ProxyRequestFilter{Host: "api.anthropic.com"})
	if err != nil {
		t.Fatalf("host filter: %v", err)
	}
	if len(hostRows) != 4 {
		t.Fatalf("host filter got %d rows, want 4", len(hostRows))
	}
	for i := 0; i+1 < len(hostRows); i++ {
		if hostRows[i].StartedAt.Before(hostRows[i+1].StartedAt) {
			t.Fatalf("host rows not newest-first at %d", i)
		}
	}

	// Dispatch filter: the two d1 captures only.
	dispRows, err := s.ListProxyRequestsFiltered(ProxyRequestFilter{DispatchID: &d1})
	if err != nil {
		t.Fatalf("dispatch filter: %v", err)
	}
	if len(dispRows) != 2 {
		t.Fatalf("dispatch filter got %d rows, want 2", len(dispRows))
	}

	// is_anthropic filter (combined with host): drops the probe and the
	// example.com row, leaving the 3 anthropic captures.
	anthFalse := false
	anthTrue := true
	anthRows, err := s.ListProxyRequestsFiltered(ProxyRequestFilter{Host: "api.anthropic.com", IsAnthropic: &anthTrue})
	if err != nil {
		t.Fatalf("anthropic filter: %v", err)
	}
	if len(anthRows) != 3 {
		t.Fatalf("anthropic filter got %d rows, want 3", len(anthRows))
	}
	nonAnth, err := s.ListProxyRequestsFiltered(ProxyRequestFilter{Host: "api.anthropic.com", IsAnthropic: &anthFalse})
	if err != nil {
		t.Fatalf("non-anthropic filter: %v", err)
	}
	if len(nonAnth) != 1 {
		t.Fatalf("non-anthropic filter got %d rows, want 1", len(nonAnth))
	}

	// Since cutoff: only rows at/after t0+4m (the probe + example.com).
	cutoff := t0.Add(4 * time.Minute)
	sinceRows, err := s.ListProxyRequestsFiltered(ProxyRequestFilter{Since: &cutoff})
	if err != nil {
		t.Fatalf("since filter: %v", err)
	}
	if len(sinceRows) != 2 {
		t.Fatalf("since filter got %d rows, want 2", len(sinceRows))
	}

	// Explicit small cap is honoured.
	capped, err := s.ListProxyRequestsFiltered(ProxyRequestFilter{Host: "api.anthropic.com", Limit: 2})
	if err != nil {
		t.Fatalf("cap: %v", err)
	}
	if len(capped) != 2 {
		t.Fatalf("cap got %d rows, want 2", len(capped))
	}

	// An over-ceiling limit is clamped to defaultProxyRequestLimit (it must
	// not error or return more than the table holds).
	all, err := s.ListProxyRequestsFiltered(ProxyRequestFilter{Limit: defaultProxyRequestLimit * 10})
	if err != nil {
		t.Fatalf("ceiling: %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("ceiling got %d rows, want 5 (all)", len(all))
	}

	// No match yields a non-nil empty slice.
	none, err := s.ListProxyRequestsFiltered(ProxyRequestFilter{Host: "nope.example"})
	if err != nil {
		t.Fatalf("no-match: %v", err)
	}
	if none == nil || len(none) != 0 {
		t.Fatalf("no-match should be non-nil empty, got %v", none)
	}
}

// addProxyRow is a tiny helper for the aggregation tests — a fixed
// method/path with the host, status, and duration the test cares about.
func addProxyRow(t *testing.T, s *Store, host string, status int, dur time.Duration) {
	t.Helper()
	if _, err := s.AddProxyRequest(AddProxyRequestIn{
		Method: "POST", Host: host, Path: "/v1/messages",
		Status: status, BytesIn: 100, BytesOut: 200, Duration: dur,
	}); err != nil {
		t.Fatalf("add proxy row (%s): %v", host, err)
	}
}

// statByHost finds the rollup entry for host, or fails the test.
func statByHost(t *testing.T, stats []*model.ProxyFQDNStat, host string) *model.ProxyFQDNStat {
	t.Helper()
	for _, s := range stats {
		if s.Host == host {
			return s
		}
	}
	t.Fatalf("no rollup entry for host %q in %+v", host, stats)
	return nil
}

// TestProxyStatsByFQDN_GroupsByHost: two hosts with differing volumes
// roll up independently, and the result is ordered busiest-first.
func TestProxyStatsByFQDN_GroupsByHost(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 3; i++ {
		addProxyRow(t, s, "api.anthropic.com", 200, 100*time.Millisecond)
	}
	addProxyRow(t, s, "example.com", 200, 50*time.Millisecond)

	stats, err := s.ProxyStatsByFQDN(ProxyStatsFilter{})
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("got %d hosts, want 2: %+v", len(stats), stats)
	}
	// Busiest-first ordering.
	if stats[0].Host != "api.anthropic.com" {
		t.Fatalf("busiest host first: got %q", stats[0].Host)
	}
	anthropic := statByHost(t, stats, "api.anthropic.com")
	if anthropic.RequestCount != 3 {
		t.Fatalf("anthropic request_count = %d, want 3", anthropic.RequestCount)
	}
	if anthropic.BytesIn != 300 || anthropic.BytesOut != 600 {
		t.Fatalf("anthropic byte sums wrong: in=%d out=%d", anthropic.BytesIn, anthropic.BytesOut)
	}
	example := statByHost(t, stats, "example.com")
	if example.RequestCount != 1 {
		t.Fatalf("example request_count = %d, want 1", example.RequestCount)
	}
}

// TestProxyStatsByFQDN_ErrorRate: status==0 (upstream round-trip
// failure) and status>=400 both count as errors, in numerator AND
// denominator.
func TestProxyStatsByFQDN_ErrorRate(t *testing.T) {
	s := newTestStore(t)
	addProxyRow(t, s, "api.anthropic.com", 200, 10*time.Millisecond) // ok
	addProxyRow(t, s, "api.anthropic.com", 200, 10*time.Millisecond) // ok
	addProxyRow(t, s, "api.anthropic.com", 429, 10*time.Millisecond) // 4xx error
	addProxyRow(t, s, "api.anthropic.com", 0, 10*time.Millisecond)   // round-trip failure

	stats, err := s.ProxyStatsByFQDN(ProxyStatsFilter{})
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	st := statByHost(t, stats, "api.anthropic.com")
	if st.RequestCount != 4 {
		t.Fatalf("request_count = %d, want 4 (status-0 not dropped from denominator)", st.RequestCount)
	}
	if st.ErrorCount != 2 {
		t.Fatalf("error_count = %d, want 2 (429 + status-0)", st.ErrorCount)
	}
	if st.ErrorRate != 0.5 {
		t.Fatalf("error_rate = %v, want 0.5", st.ErrorRate)
	}
}

// TestProxyStatsByFQDN_Percentiles: nearest-rank p50/p95 on a fixed set
// of durations 10..100ms (n=10). p50 = 5th = 50, p95 = 10th = 100.
func TestProxyStatsByFQDN_Percentiles(t *testing.T) {
	s := newTestStore(t)
	// Insert out of duration order to prove the ORDER BY sorts the bucket.
	for _, ms := range []int64{30, 100, 10, 70, 50, 20, 90, 40, 80, 60} {
		addProxyRow(t, s, "api.anthropic.com", 200, time.Duration(ms)*time.Millisecond)
	}
	stats, err := s.ProxyStatsByFQDN(ProxyStatsFilter{})
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	st := statByHost(t, stats, "api.anthropic.com")
	if st.P50MS != 50 {
		t.Fatalf("p50_ms = %d, want 50", st.P50MS)
	}
	if st.P95MS != 100 {
		t.Fatalf("p95_ms = %d, want 100", st.P95MS)
	}
}

// TestProxyStatsByFQDN_SinceWindow: backdated rows fall outside an
// explicit Since lower bound and don't count toward the rollup.
func TestProxyStatsByFQDN_SinceWindow(t *testing.T) {
	s := newTestStore(t)
	// Fresh row — inside the window.
	addProxyRow(t, s, "api.anthropic.com", 200, 10*time.Millisecond)
	// Stale row — backdate started_at well before the cutoff.
	stale, err := s.AddProxyRequest(AddProxyRequestIn{
		Method: "POST", Host: "api.anthropic.com", Path: "/v1/old", Status: 200,
	})
	if err != nil {
		t.Fatalf("add stale: %v", err)
	}
	old := time.Now().Add(-48 * time.Hour).UTC().Format("2006-01-02 15:04:05")
	if _, err := s.DB.Exec(`UPDATE proxy_requests SET started_at = ? WHERE id = ?`, old, stale.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	cutoff := time.Now().Add(-1 * time.Hour)
	stats, err := s.ProxyStatsByFQDN(ProxyStatsFilter{Since: &cutoff})
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	st := statByHost(t, stats, "api.anthropic.com")
	if st.RequestCount != 1 {
		t.Fatalf("request_count = %d, want 1 (backdated row excluded by Since)", st.RequestCount)
	}
}

// TestProxyStatsByFQDN_Empty: an empty table returns a non-nil empty
// slice, not nil (so the JSON read surface emits [] not null).
func TestProxyStatsByFQDN_Empty(t *testing.T) {
	s := newTestStore(t)
	stats, err := s.ProxyStatsByFQDN(ProxyStatsFilter{})
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats == nil {
		t.Fatalf("expected non-nil empty slice, got nil")
	}
	if len(stats) != 0 {
		t.Fatalf("expected empty slice, got %+v", stats)
	}
}
