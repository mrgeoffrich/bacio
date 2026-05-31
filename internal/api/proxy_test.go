package api_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrgeoffrich/bacio/internal/api"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// fakeUpstream spins an httptest server that records the inbound request
// and echoes a fixed body, standing in for api.anthropic.com.
func fakeUpstream(t *testing.T, record func(*http.Request)) *httptest.Server {
	t.Helper()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if record != nil {
			record(r)
		}
		_, _ = io.WriteString(w, "upstream-ok")
	}))
	t.Cleanup(up.Close)
	return up
}

// TestProxyRoute_AuthExempt asserts the /anthropic/* route bypasses the
// bearer-token check: with a token configured, a request carrying NO
// Authorization header still reaches the upstream (200, not 401). Agent
// traffic carries its own Anthropic auth, not bacio's bearer token.
func TestProxyRoute_AuthExempt(t *testing.T) {
	up := fakeUpstream(t, nil)
	ts, _ := newTestAPI(t, api.Options{Token: "secret", ProxyUpstream: up.URL})

	resp := do(t, http.MethodPost, ts.URL+"/anthropic/v1/messages",
		strings.NewReader(`{"hi":1}`), nil)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (body %q), want 200 — proxy route must be auth-exempt", resp.StatusCode, body)
	}
	if string(body) != "upstream-ok" {
		t.Fatalf("body = %q, want %q (request didn't reach the upstream)", body, "upstream-ok")
	}
}

// TestProxyRoute_PresentRegardlessOfMountUI asserts the proxy route is
// mounted whether or not the web UI is mounted — the agent needs the pipe
// whether it launched `bacio api` (MountUI:false) or `bacio web`
// (MountUI:true).
func TestProxyRoute_PresentRegardlessOfMountUI(t *testing.T) {
	for _, mountUI := range []bool{false, true} {
		var gotPath, gotHost string
		up := fakeUpstream(t, func(r *http.Request) {
			gotPath = r.URL.Path
			gotHost = r.Host
		})
		ts, _ := newTestAPI(t, api.Options{MountUI: mountUI, ProxyUpstream: up.URL})

		// The upstream's host (127.0.0.1:<upstream-port>) — what the Host
		// header should be rewritten to, distinct from the front server.
		wantHost := strings.TrimPrefix(up.URL, "http://")

		resp := do(t, http.MethodGet, ts.URL+"/anthropic/v1/messages", nil, nil)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("MountUI=%v: status = %d (body %q), want 200", mountUI, resp.StatusCode, body)
		}
		if gotPath != "/v1/messages" {
			t.Fatalf("MountUI=%v: upstream path = %q, want %q (prefix not stripped)", mountUI, gotPath, "/v1/messages")
		}
		if gotHost != wantHost {
			t.Fatalf("MountUI=%v: upstream saw Host %q, want it rewritten to %q", mountUI, gotHost, wantHost)
		}
	}
}

// TestProxyRoute_DefaultUpstream asserts that leaving ProxyUpstream empty
// still mounts the route (selecting proxy.DefaultUpstream) rather than
// falling through to the ServeMux 404. The real api.anthropic.com isn't
// reachable (and must not be dialled) in a unit test, so we assert route
// presence without contacting the upstream: a request that the handler
// short-circuits before any dial. A POST with a malformed body still
// reaches the proxy handler, but to stay network-free we instead compare
// against the ServeMux 404 envelope using a bounded client and treat any
// non-404 (or a transport error, which only happens once the route is
// matched and a dial is attempted) as proof the route is mounted.
func TestProxyRoute_DefaultUpstream(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})

	// Control: a path the ServeMux has no handler for returns the api
	// not-found envelope. The proxy route must NOT look like this.
	missing := do(t, http.MethodGet, ts.URL+"/no-such-route", nil, nil)
	missingBody, _ := io.ReadAll(missing.Body)
	missing.Body.Close()
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("control: /no-such-route status = %d, want 404", missing.StatusCode)
	}

	// Bounded client so a dial to the (unreachable) default upstream can't
	// hang the test. A transport error here means the route matched and a
	// dial was attempted — i.e. the route IS mounted — so it's a pass.
	client := &http.Client{Timeout: 3 * time.Second}
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/anthropic/v1/messages", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		// Route matched, default upstream selected, dial attempted/failed.
		return
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound && string(body) == string(missingBody) {
		t.Fatalf("/anthropic/* returned the ServeMux 404 envelope — the route wasn't mounted for an empty ProxyUpstream")
	}
}

// TestProxyRoute_CapturesIndexRow drives a request through the api server to
// a fake upstream and asserts (after the async capture worker drains) that a
// proxy_requests index row landed with the transport-level fields filled in.
// This is the BACI-302 capture pipeline end-to-end through newTestAPI.
func TestProxyRoute_CapturesIndexRow(t *testing.T) {
	up := fakeUpstream(t, nil)
	logDir := t.TempDir()
	ts, s := newTestAPI(t, api.Options{ProxyUpstream: up.URL, LogDir: logDir})

	resp := do(t, http.MethodPost, ts.URL+"/anthropic/v1/messages",
		strings.NewReader(`{"hi":1}`), nil)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (body %q), want 200", resp.StatusCode, body)
	}

	// Capture is async (off the request path), so poll the index until the
	// row lands or the deadline passes.
	row := waitForProxyRow(t, s)
	if row.Method != http.MethodPost {
		t.Errorf("method = %q, want POST", row.Method)
	}
	if row.Path != "/v1/messages" {
		t.Errorf("path = %q, want /v1/messages (prefix stripped)", row.Path)
	}
	if row.Status != http.StatusOK {
		t.Errorf("status = %d, want 200", row.Status)
	}
	if row.BytesOut != int64(len("upstream-ok")) {
		t.Errorf("bytes_out = %d, want %d", row.BytesOut, len("upstream-ok"))
	}
	if row.RawLogPath == "" {
		t.Errorf("raw_log_path is empty — the capture file should have been written under %q", logDir)
	}
}

// TestProxyRoute_StreamOutlivesWriteTimeout reproduces the bug where the API
// server's WriteTimeout (server.go) cut a slow Anthropic turn mid-stream: the
// connection's write deadline is set when the request is read, so an upstream
// that responds after the timeout has its downstream relay fail on arrival,
// and the agent receives a truncated SSE stream (no message_stop) and retries.
// The fake upstream deliberately delays past a short front-server WriteTimeout
// before replying; the assertion is that the /anthropic route's deadline-lift
// (clearStreamDeadline + statusRecorder.Unwrap) lets the full stream through.
func TestProxyRoute_StreamOutlivesWriteTimeout(t *testing.T) {
	const (
		writeTimeout  = 60 * time.Millisecond
		upstreamDelay = 300 * time.Millisecond // > writeTimeout: the bug's trigger
	)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(upstreamDelay)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "event: message_stop\ndata: {}\n\n")
	}))
	defer up.Close()

	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := api.New(s, api.Options{ProxyUpstream: up.URL, DBPath: dbPath}, logger)

	// A real server whose WriteTimeout is SHORTER than the upstream delay —
	// httptest.NewServer doesn't apply http.Server timeouts, so build it here.
	front := httptest.NewUnstartedServer(srv.Handler())
	front.Config.WriteTimeout = writeTimeout
	front.Start()
	defer front.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(front.URL+"/anthropic/v1/messages",
		"application/json", strings.NewReader(`{"hi":1}`))
	if err != nil {
		t.Fatalf("post through proxy: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read proxied body (stream cut by the %v server WriteTimeout?): %v", writeTimeout, err)
	}
	if !strings.Contains(string(body), "message_stop") {
		t.Fatalf("proxied stream was truncated by the %v server WriteTimeout — got %q; "+
			"clearStreamDeadline / statusRecorder.Unwrap did not lift the deadline", writeTimeout, body)
	}
}

// waitForProxyRow polls the proxy_requests index until at least one row
// appears, returning the newest, or fails the test on timeout.
func waitForProxyRow(t *testing.T, s *store.Store) *model.ProxyRequest {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		rows, err := s.ListProxyRequests(1)
		if err != nil {
			t.Fatalf("list proxy requests: %v", err)
		}
		if len(rows) > 0 {
			return rows[0]
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for a proxy_requests index row to land")
	return nil
}
