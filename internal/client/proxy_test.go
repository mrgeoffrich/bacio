package client_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// TestRoundTripProxyStats seeds proxy_requests rows once (the table is
// cross-cutting, no repo_id) and asserts the local and remote backends
// produce an identical per-FQDN rollup — the local backend reading the
// store directly, the remote backend over GET /proxy/stats. Keeps the two
// transports in lockstep for BACI-303 (roundtrip_test.go does not
// reflect-enumerate the interface, so this is explicit).
func TestRoundTripProxyStats(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	seed := func(host string, status int, dur time.Duration) {
		if _, err := p.store.AddProxyRequest(store.AddProxyRequestIn{
			Method: "POST", Host: host, Path: "/v1/messages",
			Status: status, BytesIn: 100, BytesOut: 200, Duration: dur,
		}); err != nil {
			t.Fatalf("seed (%s): %v", host, err)
		}
	}
	seed("api.anthropic.com", 200, 100*time.Millisecond)
	seed("api.anthropic.com", 200, 200*time.Millisecond)
	seed("api.anthropic.com", 500, 300*time.Millisecond)
	seed("example.com", 200, 50*time.Millisecond)

	local, err := p.local.ProxyStats(ctx, store.ProxyStatsFilter{})
	if err != nil {
		t.Fatalf("local ProxyStats: %v", err)
	}
	remote, err := p.remote.ProxyStats(ctx, store.ProxyStatsFilter{})
	if err != nil {
		t.Fatalf("remote ProxyStats: %v", err)
	}

	if len(local) != 2 || len(remote) != 2 {
		t.Fatalf("host count mismatch: local=%d remote=%d", len(local), len(remote))
	}
	assertSameRollup(t, local, remote)

	// Busiest-first ordering holds on both backends.
	if local[0].Host != "api.anthropic.com" || remote[0].Host != "api.anthropic.com" {
		t.Fatalf("busiest host first: local=%q remote=%q", local[0].Host, remote[0].Host)
	}
	// And the computed fields landed (not just structurally equal zeros).
	anthropic := local[0]
	if anthropic.RequestCount != 3 || anthropic.ErrorCount != 1 {
		t.Fatalf("anthropic rollup wrong: count=%d errors=%d", anthropic.RequestCount, anthropic.ErrorCount)
	}
	if anthropic.P50MS == 0 || anthropic.P95MS == 0 {
		t.Fatalf("percentiles unpopulated: p50=%d p95=%d", anthropic.P50MS, anthropic.P95MS)
	}
}

// TestRoundTripProxyCaptures seeds a correlated + an uncorrelated capture and
// asserts local and remote agree on the filtered list — same rows, same
// newest-first order, same issue-key/mode enrichment — for BACI-308.
func TestRoundTripProxyCaptures(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	iss, err := p.store.CreateIssue(p.repo.ID, nil, "drill", "", model.StateTodo, nil, "")
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	disp, err := p.store.AddDispatch(store.AddDispatchIn{
		RepoID: p.repo.ID, IssueID: &iss.ID, Mode: model.DispatchModeImplement,
		TargetSessionID: "00000000-0000-0000-0000-0000000000aa", CreatedBy: "user",
	})
	if err != nil {
		t.Fatalf("AddDispatch: %v", err)
	}

	t0 := time.Now().Add(-3 * time.Minute)
	if _, err := p.store.AddProxyRequest(store.AddProxyRequestIn{
		Method: "POST", Host: "api.anthropic.com", Path: "/v1/messages",
		Status: 200, IsAnthropic: true, DispatchID: &disp.ID,
		StartedAt: t0, EndedAt: t0,
	}); err != nil {
		t.Fatalf("seed correlated: %v", err)
	}
	if _, err := p.store.AddProxyRequest(store.AddProxyRequestIn{
		Method: "POST", Host: "api.anthropic.com", Path: "/v1/messages?count_tokens",
		Status: 200, StartedAt: t0.Add(time.Minute), EndedAt: t0.Add(time.Minute),
	}); err != nil {
		t.Fatalf("seed uncorrelated: %v", err)
	}

	f := store.ProxyRequestFilter{Host: "api.anthropic.com"}
	local, err := p.local.ListProxyCaptures(ctx, f)
	if err != nil {
		t.Fatalf("local ListProxyCaptures: %v", err)
	}
	remote, err := p.remote.ListProxyCaptures(ctx, f)
	if err != nil {
		t.Fatalf("remote ListProxyCaptures: %v", err)
	}
	if len(local) != 2 || len(remote) != 2 {
		t.Fatalf("row count mismatch: local=%d remote=%d", len(local), len(remote))
	}
	for i := range local {
		x, y := local[i], remote[i]
		if x.ID != y.ID || x.Host != y.Host || x.IssueKey != y.IssueKey || x.Mode != y.Mode {
			t.Fatalf("row[%d] mismatch:\n local=%+v\n remote=%+v", i, x, y)
		}
	}
	// The correlated row (older, so index 1 newest-first) carries the chip.
	if local[1].IssueKey != iss.Key || local[1].Mode != string(model.DispatchModeImplement) {
		t.Fatalf("correlated chip wrong: key=%q mode=%q", local[1].IssueKey, local[1].Mode)
	}
}

// TestRoundTripProxyCaptureRaw asserts local and remote return identical raw
// .http bytes for a capture whose file is on disk, and both surface ErrNotFound
// (HTTP 404) when the file is missing — for BACI-308.
func TestRoundTripProxyCaptureRaw(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	rawPath := filepath.Join(t.TempDir(), "cap.http")
	want := "==== REQUEST ====\r\nPOST /v1/messages HTTP/1.1\r\n\r\n{}\r\n"
	if err := os.WriteFile(rawPath, []byte(want), 0o644); err != nil {
		t.Fatalf("write raw: %v", err)
	}
	withFile, err := p.store.AddProxyRequest(store.AddProxyRequestIn{
		Method: "POST", Host: "api.anthropic.com", Path: "/v1/messages",
		Status: 200, RawLogPath: rawPath,
	})
	if err != nil {
		t.Fatalf("add with-file: %v", err)
	}

	localRaw, err := p.local.ProxyCaptureRaw(ctx, withFile.ID)
	if err != nil {
		t.Fatalf("local raw: %v", err)
	}
	remoteRaw, err := p.remote.ProxyCaptureRaw(ctx, withFile.ID)
	if err != nil {
		t.Fatalf("remote raw: %v", err)
	}
	if string(localRaw) != want || string(remoteRaw) != want {
		t.Fatalf("raw mismatch:\n local=%q\n remote=%q\n want=%q", localRaw, remoteRaw, want)
	}

	// A row with no raw file: both backends miss (local ErrNotFound, remote
	// HTTP 404 surfaced as an error).
	noFile, err := p.store.AddProxyRequest(store.AddProxyRequestIn{
		Method: "POST", Host: "api.anthropic.com", Path: "/v1/messages", Status: 200,
	})
	if err != nil {
		t.Fatalf("add no-file: %v", err)
	}
	if _, err := p.local.ProxyCaptureRaw(ctx, noFile.ID); err == nil {
		t.Fatalf("local raw on no-file row: expected error, got nil")
	}
	if _, err := p.remote.ProxyCaptureRaw(ctx, noFile.ID); err == nil {
		t.Fatalf("remote raw on no-file row: expected error, got nil")
	}
}

// assertSameRollup compares two rollups element-wise on the
// JSON-transported fields (timestamps cross the wire as RFC 3339, so
// compare with Equal rather than ==).
func assertSameRollup(t *testing.T, a, b []*model.ProxyFQDNStat) {
	t.Helper()
	if len(a) != len(b) {
		t.Fatalf("rollup length mismatch: %d vs %d", len(a), len(b))
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.Host != y.Host || x.RequestCount != y.RequestCount ||
			x.BytesIn != y.BytesIn || x.BytesOut != y.BytesOut ||
			x.ErrorCount != y.ErrorCount || x.ErrorRate != y.ErrorRate ||
			x.P50MS != y.P50MS || x.P95MS != y.P95MS {
			t.Fatalf("rollup[%d] mismatch:\n local=%+v\n remote=%+v", i, x, y)
		}
		if !x.FirstSeen.Equal(y.FirstSeen) || !x.LastSeen.Equal(y.LastSeen) {
			t.Fatalf("rollup[%d] timestamp mismatch:\n local first=%v last=%v\n remote first=%v last=%v",
				i, x.FirstSeen, x.LastSeen, y.FirstSeen, y.LastSeen)
		}
	}
}
