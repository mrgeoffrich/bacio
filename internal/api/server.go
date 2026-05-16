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
type Options struct {
	Addr        string
	Token       string
	CORSOrigins []string
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
func (s *Server) Run(ctx context.Context) error {
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
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			s.logger.Error("api shutdown error", "err", err)
			return err
		}
		<-errCh
		return nil
	case err := <-errCh:
		return err
	}
}
