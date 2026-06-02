package api_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// newTestProxy spins the standalone BACI-344 proxy-only server (the whole
// surface `bacio proxy serve` runs) behind an httptest server, returning it
// and the backing store. Mirrors newTestAPI but for the proxy-only handler.
func newTestProxy(t *testing.T, opts api.Options) (*httptest.Server, *store.Store) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "proxy.sqlite")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if opts.DBPath == "" {
		opts.DBPath = dbPath
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := api.NewProxyServer(s, opts, logger)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, s
}

// TestProxyOnlyHandler_ForwardsAnthropic asserts the standalone proxy
// surface forwards /anthropic/* to the upstream with the prefix stripped
// and the Host header rewritten — the same forwarding contract the full
// router holds, now on a process that serves nothing else.
func TestProxyOnlyHandler_ForwardsAnthropic(t *testing.T) {
	var gotPath, gotHost string
	up := fakeUpstream(t, func(r *http.Request) {
		gotPath = r.URL.Path
		gotHost = r.Host
	})
	ts, _ := newTestProxy(t, api.Options{ProxyUpstream: up.URL})
	wantHost := strings.TrimPrefix(up.URL, "http://")

	resp := do(t, http.MethodPost, ts.URL+"/anthropic/v1/messages",
		strings.NewReader(`{"hi":1}`), nil)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (body %q), want 200", resp.StatusCode, body)
	}
	if string(body) != "upstream-ok" {
		t.Fatalf("body = %q, want %q (request didn't reach the upstream)", body, "upstream-ok")
	}
	if gotPath != "/v1/messages" {
		t.Fatalf("upstream path = %q, want /v1/messages (prefix not stripped)", gotPath)
	}
	if gotHost != wantHost {
		t.Fatalf("upstream saw Host %q, want it rewritten to %q", gotHost, wantHost)
	}
}

// TestProxyOnlyHandler_HealthzNoAuth asserts /healthz returns 200 with a
// JSON body and needs no bearer token — the standalone proxy has no auth
// wrapper at all (a Token in Options is ignored), so a keep-alive
// supervisor's health probe always works.
func TestProxyOnlyHandler_HealthzNoAuth(t *testing.T) {
	// A Token is set in Options to prove it is ignored on this surface.
	ts, _ := newTestProxy(t, api.Options{Token: "secret"})

	resp := do(t, http.MethodGet, ts.URL+"/healthz", nil, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/healthz status = %d, want 200 (proxy surface has no auth)", resp.StatusCode)
	}
	var hr struct {
		OK      bool   `json:"ok"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&hr); err != nil {
		t.Fatalf("decode /healthz body: %v", err)
	}
	if !hr.OK {
		t.Fatalf("/healthz body ok = false, want true")
	}
}

// TestProxyOnlyHandler_RecordsCapture drives a request through the
// standalone proxy to a fake upstream and asserts the BACI-302 capture
// pipeline still lands a proxy_requests index row — the capture surface is
// the same on the split process, so a version-behind proxy keeps capturing.
func TestProxyOnlyHandler_RecordsCapture(t *testing.T) {
	up := fakeUpstream(t, nil)
	logDir := t.TempDir()
	ts, s := newTestProxy(t, api.Options{ProxyUpstream: up.URL, LogDir: logDir})

	resp := do(t, http.MethodPost, ts.URL+"/anthropic/v1/messages",
		strings.NewReader(`{"hi":1}`), nil)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (body %q), want 200", resp.StatusCode, body)
	}

	row := waitForProxyRow(t, s)
	if row.Method != http.MethodPost {
		t.Errorf("method = %q, want POST", row.Method)
	}
	if row.Path != "/v1/messages" {
		t.Errorf("path = %q, want /v1/messages (prefix stripped)", row.Path)
	}
	if row.RawLogPath == "" {
		t.Errorf("raw_log_path is empty — the capture file should have been written under %q", logDir)
	}
}

// TestProxyOnlyHandler_SharedWithRouter is the drift guard: the extracted
// buildProxyHandler must forward identically whether reached via the full
// router (newTestAPI) or the standalone proxy-only mux (newTestProxy). Both
// are pointed at the same fake upstream and must produce the same stripped
// path, rewritten host, and echoed body.
func TestProxyOnlyHandler_SharedWithRouter(t *testing.T) {
	driveBody := func(t *testing.T, baseURL string) string {
		resp := do(t, http.MethodPost, baseURL+"/anthropic/v1/messages",
			strings.NewReader(`{"hi":1}`), nil)
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return string(b)
	}

	var routerPath, routerHost string
	upRouter := fakeUpstream(t, func(r *http.Request) { routerPath = r.URL.Path; routerHost = r.Host })
	tsRouter, _ := newTestAPI(t, api.Options{ProxyUpstream: upRouter.URL})
	wantRouterHost := strings.TrimPrefix(upRouter.URL, "http://")
	routerBody := driveBody(t, tsRouter.URL)

	var proxyPath, proxyHost string
	upProxy := fakeUpstream(t, func(r *http.Request) { proxyPath = r.URL.Path; proxyHost = r.Host })
	tsProxy, _ := newTestProxy(t, api.Options{ProxyUpstream: upProxy.URL})
	wantProxyHost := strings.TrimPrefix(upProxy.URL, "http://")
	proxyBody := driveBody(t, tsProxy.URL)

	if routerPath != "/v1/messages" || proxyPath != "/v1/messages" {
		t.Fatalf("stripped path drift: router=%q proxy=%q, both want /v1/messages", routerPath, proxyPath)
	}
	if routerHost != wantRouterHost || proxyHost != wantProxyHost {
		t.Fatalf("host-rewrite drift: router=%q (want %q) proxy=%q (want %q)",
			routerHost, wantRouterHost, proxyHost, wantProxyHost)
	}
	if routerBody != proxyBody {
		t.Fatalf("body drift: router=%q proxy=%q", routerBody, proxyBody)
	}
}
