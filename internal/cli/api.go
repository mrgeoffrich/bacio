package cli

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/api"
	"github.com/mrgeoffrich/bacio/internal/logging"
	"github.com/mrgeoffrich/bacio/internal/version"
	"github.com/mrgeoffrich/bacio/internal/wtenv"
)

func newAPICmd() *cobra.Command {
	var (
		addr        string
		port        int
		token       string
		corsOrigins []string
	)
	cmd := &cobra.Command{
		Use:   "api",
		Short: "Run the local REST API server",
		Long: `Run a local HTTP REST API on top of the same SQLite database the CLI uses.

Defaults to 127.0.0.1:5320. Override the bind via --addr (host:port) or just
the port via --port. Set --token (or BACIO_API_TOKEN) to require
"Authorization: Bearer <token>" on every request except /healthz.

The persistent --user flag is silently ignored by this command — incoming
requests carry their own actor via the X-Actor header (default "api").`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Resolve the worktree env once: we need it for the default
			// bind addr AND for the BACI-73 logging resolver, which
			// reads the manifest's allocations.log_dir field.
			env, err := resolveEnv()
			if err != nil {
				return err
			}
			// If --addr was left at its zero-value sentinel, resolve via
			// the worktree manifest chain (BACI-63) — so a manifest-aware
			// worktree picks up its allocated port without the user
			// repeating --addr on every invocation.
			if !cmd.Flags().Changed("addr") {
				addr = env.APIAddr
			}
			if port > 0 {
				host, _, err := net.SplitHostPort(addr)
				if err != nil {
					return fmt.Errorf("invalid --addr %q: %w", addr, err)
				}
				addr = net.JoinHostPort(host, strconv.Itoa(port))
			}
			if token == "" {
				token = os.Getenv("BACIO_API_TOKEN")
			}

			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()

			// Build the slog handler. BACI-73: a file sink under the
			// resolved log dir is opened on top of stderr. A creation
			// failure falls back to stderr-only with a single warning
			// — never block the process from starting.
			var logger *slog.Logger
			var closeLogger func()
			logger, closeLogger = buildAPILogger(env)
			defer closeLogger()
			logger.Info("api starting",
				"addr", addr,
				"db", env.DBPath,
				"env_source", string(env.Source),
				"env_path", env.ManifestPath,
				"version", version.String(),
			)
			srv := api.New(s, api.Options{
				Addr:        addr,
				Token:       token,
				CORSOrigins: corsOrigins,
			}, logger)

			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			err = srv.Run(ctx)
			if err != nil {
				logger.Error("api shutdown", "err", err)
			} else {
				logger.Info("api shutdown", "reason", "signal")
			}
			return err
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:5320", "bind address (host:port)")
	cmd.Flags().IntVar(&port, "port", 0, "shorthand to override only the port (keeps host from --addr)")
	cmd.Flags().StringVar(&token, "token", "", "shared bearer token; falls back to BACIO_API_TOKEN env var")
	cmd.Flags().StringSliceVar(&corsOrigins, "cors-origin", nil,
		"allow cross-origin browser requests from this origin (repeatable; e.g. http://localhost:5174). Empty allow-list = same-origin only.")
	return cmd
}

// buildAPILogger wires the BACI-73 file-logging handler with a stderr
// fallback. The returned close func flushes the file handle on
// shutdown. A creation failure prints ONE stderr warning and drops the
// file sink — the process must never refuse to start because of a log
// problem.
//
// Component is "api" so the per-day filename is bacio-api-YYYY-MM-DD.log.
func buildAPILogger(env wtenv.Resolved) (*slog.Logger, func()) {
	return buildComponentLogger(env, "api")
}

// buildComponentLogger is the shared implementation behind
// buildAPILogger and future per-component variants (channel,
// desktop). It resolves the log dir + level, opens the handler, and
// returns a *slog.Logger plus a close func.
func buildComponentLogger(env wtenv.Resolved, component string) (*slog.Logger, func()) {
	// Re-resolve logging from the wtenv result. The wtenv layer knows
	// the manifest; the logging layer applies its own
	// flag/env/manifest/default chain on top.
	logRes, err := resolveLogging(env)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bacio %s: log config: %v — file logging disabled\n", component, err)
		l := slog.New(logging.StderrOnlyHandler(slog.LevelInfo)).With("component", component)
		return l, func() {}
	}
	h, err := logging.NewHandler(logging.NewHandlerOpts{
		Dir:       logRes.Dir,
		Component: component,
		Level:     logRes.Level,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "bacio %s: file logging disabled: %v\n", component, err)
		l := slog.New(logging.StderrOnlyHandler(logRes.Level)).With("component", component)
		return l, func() {}
	}
	logger := slog.New(h).With("component", component)
	return logger, func() { _ = h.Close() }
}
