package client_test

import (
	"context"
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
