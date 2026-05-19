package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestNewWebCmd_FlagsDefined locks in the user-visible flag surface so
// a future refactor can't silently drop --addr / --port / --token /
// --cors-origin / --no-open. The four shared flags must stay in lockstep
// with `bacio api`; --no-open is web-only (BACI-72).
func TestNewWebCmd_FlagsDefined(t *testing.T) {
	cmd := newWebCmd()
	want := []string{"addr", "port", "token", "cors-origin", "no-open"}
	for _, name := range want {
		if cmd.Flags().Lookup(name) == nil {
			t.Fatalf("--%s flag missing from `bacio web`", name)
		}
	}
}

// TestNewWebCmd_AddrDefaultMatchesAPI keeps the bind-address default
// aligned with `bacio api`: 127.0.0.1:5320. Drift here means a user
// running `bacio web` would end up on a different port than the
// documented default — and the resolveEnv worktree-manifest path keys
// off the same constant.
func TestNewWebCmd_AddrDefaultMatchesAPI(t *testing.T) {
	web := newWebCmd().Flags().Lookup("addr")
	api := newAPICmd().Flags().Lookup("addr")
	if web.DefValue != api.DefValue {
		t.Fatalf("addr default drift: web=%q api=%q", web.DefValue, api.DefValue)
	}
}

// TestOpenBrowserWhenReady_LaunchesAfterHealthz spins a tiny test
// server with a /healthz handler, swaps in a fake browser launcher,
// and asserts the launcher is invoked exactly once with the right URL
// once /healthz returns 200.
func TestOpenBrowserWhenReady_LaunchesAfterHealthz(t *testing.T) {
	withFastPoll(t)

	var launches atomic.Int32
	var seenURL atomic.Value // string
	prev := browserLauncher
	t.Cleanup(func() { browserLauncher = prev })
	browserLauncher = func(_ context.Context, url string) error {
		launches.Add(1)
		seenURL.Store(url)
		return nil
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true}`)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	host, port, err := net.SplitHostPort(stripScheme(ts.URL))
	if err != nil {
		t.Fatalf("split test url: %v", err)
	}
	addr := net.JoinHostPort(host, port)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	openBrowserWhenReady(ctx, addr, discardLogger())

	if n := launches.Load(); n != 1 {
		t.Fatalf("expected 1 launch, got %d", n)
	}
	wantURL := fmt.Sprintf("http://%s/ui/", addr)
	if got := seenURL.Load().(string); got != wantURL {
		t.Fatalf("launcher url: want %q, got %q", wantURL, got)
	}
}

// TestOpenBrowserWhenReady_ContextCancelStops verifies the goroutine
// exits promptly when its context cancels (no listener ever comes up).
// Catches a bug where a forgotten select arm would block on the timer.
func TestOpenBrowserWhenReady_ContextCancelStops(t *testing.T) {
	withFastPoll(t)

	prev := browserLauncher
	t.Cleanup(func() { browserLauncher = prev })
	browserLauncher = func(context.Context, string) error {
		t.Fatalf("launcher must not run when /healthz never comes up")
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		// 127.0.0.1:1 is reserved + closed — /healthz never returns 200.
		openBrowserWhenReady(ctx, "127.0.0.1:1", discardLogger())
		close(done)
	}()
	cancel()
	select {
	case <-done:
		// good
	case <-time.After(2 * time.Second):
		t.Fatalf("openBrowserWhenReady did not exit after ctx cancel")
	}
}

// TestOpenBrowserWhenReady_DeadlineExits asserts the goroutine gives up
// after the poll deadline if /healthz never returns 200, instead of
// blocking the caller forever.
func TestOpenBrowserWhenReady_DeadlineExits(t *testing.T) {
	prev := browserLauncher
	t.Cleanup(func() { browserLauncher = prev })
	browserLauncher = func(context.Context, string) error {
		t.Fatalf("launcher must not run when /healthz never comes up")
		return nil
	}
	prevInterval := browserPollInterval
	prevDeadline := browserPollDeadline
	browserPollInterval = 5 * time.Millisecond
	browserPollDeadline = 50 * time.Millisecond
	t.Cleanup(func() {
		browserPollInterval = prevInterval
		browserPollDeadline = prevDeadline
	})

	done := make(chan struct{})
	start := time.Now()
	go func() {
		openBrowserWhenReady(context.Background(), "127.0.0.1:1", discardLogger())
		close(done)
	}()
	select {
	case <-done:
		// The goroutine should have exited near the deadline, not at
		// the test timeout below.
		if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
			t.Fatalf("openBrowserWhenReady took %v to honour deadline %v", elapsed, browserPollDeadline)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("openBrowserWhenReady did not exit after deadline")
	}
}

func withFastPoll(t *testing.T) {
	t.Helper()
	prevInterval := browserPollInterval
	prevDeadline := browserPollDeadline
	browserPollInterval = 5 * time.Millisecond
	browserPollDeadline = 2 * time.Second
	t.Cleanup(func() {
		browserPollInterval = prevInterval
		browserPollDeadline = prevDeadline
	})
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func stripScheme(s string) string {
	if i := strings.Index(s, "://"); i >= 0 {
		return s[i+3:]
	}
	return s
}
