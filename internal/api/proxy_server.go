package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/mrgeoffrich/bacio/internal/proxy"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// buildProxyHandler constructs the BACI-301 reverse-proxy forwarding
// handler — upstream resolution, the BACI-302 capture recorder, and the
// clearStreamDeadline wrap — from a deps bag. Extracted (BACI-344) so the
// full router (newRouter) and the standalone `bacio proxy serve` mux
// (newProxyOnlyHandler) build the IDENTICAL handler with no drift: the
// capture recorder, upstream parse/fallback, and the deadline lift all
// live in one place.
//
// Empty ProxyUpstream selects the Anthropic default; a malformed explicit
// value is logged and falls back to the default rather than panicking
// (the value is internal config).
func buildProxyHandler(d deps) http.Handler {
	proxyLogger := d.logger
	if proxyLogger == nil {
		proxyLogger = slog.Default()
	}
	upstreamRaw := d.opts.ProxyUpstream
	if upstreamRaw == "" {
		upstreamRaw = proxy.DefaultUpstream
	}
	upstreamURL, err := url.Parse(upstreamRaw)
	if err != nil || upstreamURL.Host == "" {
		proxyLogger.Error("invalid proxy upstream — falling back to default",
			"upstream", upstreamRaw, "err", err)
		upstreamURL, _ = url.Parse(proxy.DefaultUpstream)
	}
	// BACI-302: the capture recorder observes every proxied request — raw
	// req/resp to <LogDir>/proxy/, a lightweight index row in proxy_requests
	// — off the request path so the proxy's streaming hot path is never
	// gated on a disk/DB write. Constructed here so the proxy package stays
	// free of the store and the log dir.
	proxyRecorder := newCaptureRecorder(d.store, d.opts.LogDir, proxyLogger)
	// clearStreamDeadline lifts any server-level Write/Read deadline for this
	// route only — a streaming Anthropic turn routinely outlives the JSON
	// API's 30s WriteTimeout, and the server-level deadline would otherwise
	// cut the response mid-stream. See clearStreamDeadline in middleware.go.
	return clearStreamDeadline(proxy.New(upstreamURL, proxyLogger, proxyRecorder))
}

// newProxyOnlyHandler is the whole HTTP surface of the standalone
// `bacio proxy serve` process (BACI-344): ONLY the auth-exempt
// /anthropic/* reverse-proxy route plus the unauthenticated /healthz and
// /version reads. Splitting the proxy onto its own deliberately tiny,
// long-lived process lets `bacio web` (UI + REST API) be upgraded and
// restarted without dropping the liveness-critical /anthropic pipe every
// agent pins via ANTHROPIC_BASE_URL.
//
// No bearer-token auth wrapper: the only forwarding route is the one auth()
// exempts anyway (agent traffic carries its own Anthropic auth), and the two
// health reads are unauthenticated. No CORS and no bodyCap either — there is
// no browser surface, and bodyCap's MaxBytesReader would wrongly cap the
// streaming /anthropic request body. recoverPanic + requestLog are the only
// wrappers, matching newRouter's outermost two layers.
func newProxyOnlyHandler(d deps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", d.handleHealthz)
	mux.HandleFunc("GET /version", d.handleVersion)
	mux.Handle(proxy.PathPrefix+"/", buildProxyHandler(d))

	logger := d.logger
	if logger == nil {
		logger = slog.Default()
	}
	var h http.Handler = mux
	h = requestLog(h, logger)
	h = recoverPanic(h, logger)
	return h
}

// ProxyServer is the standalone reverse-proxy listener (BACI-344). It
// reuses the same store, capture recorder, and proxy handler as the full
// api.Server but serves nothing else — no UI, no REST API, no leader
// election, no background tickers. The caller (cmd/bacio) owns the store
// lifecycle; NewProxyServer does not take ownership.
type ProxyServer struct {
	httpServer *http.Server
	logger     *slog.Logger
}

// NewProxyServer wires the proxy-only HTTP server but does not start
// listening. It threads the same Options shape api.New takes — only Addr,
// ProxyUpstream, and LogDir are consulted; Token / CORSOrigins / MountUI /
// DBPath are ignored (the proxy surface has no auth, no browser, no REST
// handlers).
//
// The listener carries ReadHeaderTimeout (a slow-loris backstop) but NO
// server-level ReadTimeout / WriteTimeout: the only handler is the
// streaming /anthropic route, which clearStreamDeadline already governs,
// and there are no short-lived JSON handlers needing the protective 30s
// the full api.Server applies.
func NewProxyServer(s *store.Store, opts Options, logger *slog.Logger) *ProxyServer {
	if logger == nil {
		logger = slog.Default()
	}
	handler := newProxyOnlyHandler(deps{store: s, opts: opts, logger: logger})
	return &ProxyServer{
		httpServer: &http.Server{
			Addr:              opts.Addr,
			Handler:           handler,
			ReadHeaderTimeout: 5 * time.Second,
			IdleTimeout:       60 * time.Second,
		},
		logger: logger,
	}
}

// Handler returns the wrapped http.Handler so tests can drive the proxy
// server via httptest.NewServer without going through a real listener
// (mirrors Server.Handler()).
func (p *ProxyServer) Handler() http.Handler { return p.httpServer.Handler }

// Run starts the listener and blocks until ctx is cancelled, then performs
// a 5s graceful shutdown via http.Server.Shutdown. Mirrors Server.Run's
// shape minus the leader-election lifecycle — the standalone proxy never
// becomes the leader (it runs no background tickers).
func (p *ProxyServer) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		p.logger.Info("proxy listening", "addr", p.httpServer.Addr)
		if err := p.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := p.httpServer.Shutdown(shutdownCtx); err != nil {
			p.logger.Error("proxy shutdown error", "err", err)
			return err
		}
		<-errCh
		return nil
	case err := <-errCh:
		return err
	}
}
