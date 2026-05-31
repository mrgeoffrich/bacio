package client_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mrgeoffrich/bacio/internal/client"
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

// TestRoundTripProxySearch seeds parsed proxy_messages rows once and asserts the
// local and remote backends grep them identically — same match lines, same
// newest-first order, same capture id / role / block / snippet — for BACI-320.
// The roundtrip suite doesn't reflect-enumerate the interface, so this is
// explicit, like TestRoundTripProxyCaptures.
func TestRoundTripProxySearch(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	dispatchID := int64(99)
	// Capture 1: needle in the assistant turn.
	cap1 := &model.ParsedCapture{
		Model: "claude-opus-4-8", SystemFP: "fp", MessageCount: 1,
		Turn: model.AnthropicTurn{Blocks: []model.AnthropicBlock{{Type: "text", Text: "a stray token court appears"}}},
	}
	if _, err := p.store.AddProxyMessage(store.AddProxyMessageIn{
		ProxyRequestID: 1, DispatchID: &dispatchID, SessionID: "sess",
		Capture: cap1, IsPrimary: true, StartedAt: time.Now().Add(-2 * time.Minute),
	}); err != nil {
		t.Fatalf("seed cap1: %v", err)
	}
	// Capture 2: needle in the user delta.
	cap2 := &model.ParsedCapture{
		Model: "claude-opus-4-8", SystemFP: "fp", MessageCount: 3,
		Turn: model.AnthropicTurn{Blocks: []model.AnthropicBlock{{Type: "text", Text: "ok"}}},
	}
	if _, err := p.store.AddProxyMessage(store.AddProxyMessageIn{
		ProxyRequestID: 2, DispatchID: &dispatchID, SessionID: "sess",
		Capture: cap2,
		Delta:   []model.AnthropicMessage{{Role: "user", Content: []model.AnthropicBlock{{Type: "text", Text: "go to court now"}}}},
		IsPrimary: true, StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed cap2: %v", err)
	}

	f := store.ProxyMessageFilter{Query: "court"}
	local, err := p.local.SearchProxyMessages(ctx, f)
	if err != nil {
		t.Fatalf("local SearchProxyMessages: %v", err)
	}
	remote, err := p.remote.SearchProxyMessages(ctx, f)
	if err != nil {
		t.Fatalf("remote SearchProxyMessages: %v", err)
	}
	if len(local) != 2 || len(remote) != 2 {
		t.Fatalf("match count mismatch: local=%d remote=%d", len(local), len(remote))
	}
	for i := range local {
		x, y := local[i], remote[i]
		if x.ProxyRequestID != y.ProxyRequestID || x.Role != y.Role ||
			x.Block != y.Block || x.Snippet != y.Snippet {
			t.Fatalf("match[%d] mismatch:\n local=%+v\n remote=%+v", i, x, y)
		}
	}
	// Newest-first: capture 2 (user delta) leads, capture 1 (assistant) trails.
	if local[0].ProxyRequestID != 2 || local[0].Role != "user" {
		t.Fatalf("match[0] = %+v, want capture 2 / user (newest)", local[0])
	}
	if local[1].ProxyRequestID != 1 || local[1].Role != "assistant" {
		t.Fatalf("match[1] = %+v, want capture 1 / assistant", local[1])
	}
}

// TestRoundTripProxyReparse seeds an unparsed dispatch-correlated Anthropic
// capture with its .http file on disk, then drives the BACI-321 backfill on both
// backends: dry-run projects the count without writing (no proxy_messages row, no
// audit), the wet run backfills the row and records a `proxy.reparse` audit, and
// --rebuild is refused on both backends as not-implemented-in-v1.
func TestRoundTripProxyReparse(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	dispatchID := int64(321)
	rawPath := filepath.Join(t.TempDir(), "turn.http")
	raw := "==== REQUEST ====\r\nPOST /v1/messages HTTP/1.1\r\nContent-Type: application/json\r\n\r\n" +
		`{"model":"claude-opus-4-8","system":"sys","messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}` +
		"\r\n==== RESPONSE ====\r\nHTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n\r\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-opus-4-8\",\"usage\":{\"input_tokens\":12}}}\n\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":4}}"
	if err := os.WriteFile(rawPath, []byte(raw), 0o644); err != nil {
		t.Fatalf("write raw: %v", err)
	}
	t0 := time.Now().Add(-1 * time.Hour)
	if _, err := p.store.AddProxyRequest(store.AddProxyRequestIn{
		Method: "POST", Host: "api.anthropic.com", Path: "/v1/messages", Status: 200,
		RawLogPath: rawPath, StartedAt: t0, EndedAt: t0,
		ContentType: "text/event-stream", IsStream: true, IsAnthropic: true,
		DispatchID: &dispatchID,
	}); err != nil {
		t.Fatalf("seed capture: %v", err)
	}

	in := client.ReparseProxyOpts{Dispatch: &dispatchID}

	// Dry-run on both backends: projects 1 capture, writes nothing.
	for name, c := range map[string]client.Client{"local": p.local, "remote": p.remote} {
		res, err := c.ReparseProxyMessages(ctx, in, true)
		if err != nil {
			t.Fatalf("%s dry-run: %v", name, err)
		}
		if res.CapturesReparsed != 1 || res.DispatchesScanned != 1 {
			t.Fatalf("%s dry-run result = %+v, want 1 scanned / 1 reparsed", name, res)
		}
		if _, err := p.store.JobTranscript(dispatchID); err != store.ErrNotFound {
			t.Fatalf("%s dry-run wrote a row (JobTranscript err = %v, want ErrNotFound)", name, err)
		}
	}

	// --rebuild is refused on both backends without touching anything.
	for name, c := range map[string]client.Client{"local": p.local, "remote": p.remote} {
		if _, err := c.ReparseProxyMessages(ctx, client.ReparseProxyOpts{Rebuild: true}, false); err == nil {
			t.Fatalf("%s --rebuild should be refused, got nil error", name)
		}
	}

	// Wet run on the local backend: backfills the row + records the audit.
	res, err := p.local.ReparseProxyMessages(ctx, in, false)
	if err != nil {
		t.Fatalf("local wet reparse: %v", err)
	}
	if res.CapturesReparsed != 1 {
		t.Fatalf("wet reparse result = %+v, want 1 reparsed", res)
	}
	tr, err := p.store.JobTranscript(dispatchID)
	if err != nil {
		t.Fatalf("JobTranscript after reparse: %v", err)
	}
	if tr.Usage.InputTokens != 12 || tr.Usage.OutputTokens != 4 {
		t.Errorf("reparsed usage = %+v, want input 12 output 4", tr.Usage)
	}
	hist, err := p.store.ListHistory(store.HistoryFilter{Op: "proxy.reparse"})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(hist) != 1 {
		t.Fatalf("audit rows for proxy.reparse = %d, want 1", len(hist))
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
