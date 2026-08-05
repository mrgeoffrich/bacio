// Package api implements the local HTTP REST surface of bacio. It shares the
// same SQLite store, validators, audit log, and JSON-input schemas as the
// CLI; nothing in this package is allowed to import internal/cli or to
// open the store itself — the cobra command owns that lifecycle.
package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/mrgeoffrich/bacio/internal/store"
)

// Options is the bind/auth configuration for an API server. Token is the
// shared bearer secret; an empty string disables auth (and logs nothing
// on /healthz, which is unauthenticated regardless).
//
// CORSOrigins is the allow-list of cross-origin browser callers. Empty
// (the default) means same-origin only — no CORS headers are emitted
// and preflight requests fall through to the route handler, which
// returns 405. Populate it (via --cors-origin, repeatable) when the
// React bundle is hosted on a different origin from the API.
//
// MountUI decides whether the embedded web bundle is served at /ui/
// and the bare /ui → /ui/ redirect is registered. `bacio web` sets
// this true; `bacio api` (BACI-72) leaves it false so an API-only
// deployment never mounts the SPA — `GET /ui/` then falls through to
// the ServeMux 404. Tests that need the UI surface opt in by passing
// `api.Options{MountUI: true}`.
type Options struct {
	Addr        string
	Token       string
	CORSOrigins []string
	MountUI     bool
	// DBPath is the resolved SQLite path. Threaded through to the
	// BACI-89 background sync runner so its cross-process lock file
	// lands beside the right DB. Empty falls back to store.DefaultPath.
	DBPath string
	// ProxyUpstream is the upstream the BACI-301 reverse proxy forwards
	// the auth-exempt /anthropic/* route to. Empty (the default for both
	// `bacio api` and `bacio web`) selects proxy.DefaultUpstream
	// (https://api.anthropic.com); tests point it at a fake upstream.
	ProxyUpstream string
	// LogDir is the resolved per-worktree log directory (BACI-73
	// logging.Resolved.Dir). BACI-302 writes the raw proxy req/resp
	// capture files under <LogDir>/proxy/. Empty disables the raw-to-disk
	// write (index rows still land); `bacio api` / `bacio web` thread the
	// resolved dir, tests leave it empty or point it at a temp dir.
	LogDir string
	// LaunchRepoPrefix is the repo the process was started in (BACI-368),
	// resolved by the cobra command from its cwd — the API layer is told
	// the answer rather than shelling out to git itself. Empty when the
	// process wasn't started inside a git repo; served as-is by
	// GET /launch-repo.
	LaunchRepoPrefix string
}

// Server is the wired-up HTTP server. The caller (cmd/bacio) owns the store
// and is responsible for Open/Close; New does not take ownership.
type Server struct {
	httpServer *http.Server
	store      *store.Store
	opts       Options
	logger     *slog.Logger
	handler    http.Handler
}

// New constructs the server but does not start listening.
func New(s *store.Store, opts Options, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	srv := &Server{
		store:  s,
		opts:   opts,
		logger: logger,
	}
	// The handler built here is the no-leader variant — tests reach it
	// via Server.Handler() without going through Run, so an inert leader
	// surface (GET /leader returns the empty state) is the right default.
	// Run builds its own leader-aware handler and assigns it to
	// httpServer.Handler before listening.
	srv.handler = newRouter(deps{store: s, opts: opts, logger: logger})
	srv.httpServer = &http.Server{
		Addr:              opts.Addr,
		Handler:           srv.handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	return srv
}

// Handler returns the wrapped http.Handler so tests can drive the server
// via httptest.NewServer without going through a real listener.
func (s *Server) Handler() http.Handler { return s.handler }

// Run starts the listener and blocks until ctx is cancelled, then performs
// a 5s graceful shutdown via http.Server.Shutdown.
//
// Run owns the UI leader-election lifecycle (BACI-50): it starts the
// elector + tickers before ListenAndServe, swaps in a leader-aware
// handler so GET /leader returns the cached state, and releases the
// lease as the first step of the graceful shutdown. Tests that drive
// Server.Handler() directly bypass this and see the inert leader
// handler from New.
func (s *Server) Run(ctx context.Context) error {
	leaderSvc := newAPILeaderService(s.store, s.opts.Addr, s.opts.DBPath, s.logger)
	s.httpServer.Handler = newRouter(deps{
		store:  s.store,
		opts:   s.opts,
		logger: s.logger,
		leader: leaderSvc,
	})

	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("api listening", "addr", s.opts.Addr)
		if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()
	select {
	case <-ctx.Done():
		// Release the lease first so a standby UI can promote within
		// one heartbeat tick (~10s) instead of waiting out the 180s
		// stale window.
		leaderSvc.stop()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			s.logger.Error("api shutdown error", "err", err)
			return err
		}
		<-errCh
		return nil
	case err := <-errCh:
		leaderSvc.stop()
		return err
	}
}
