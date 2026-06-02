package cli

import (
	"context"
	"fmt"
	"net"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/api"
	"github.com/mrgeoffrich/bacio/internal/version"
)

// newProxyServeCmd is `bacio proxy serve` (BACI-344) — a deliberately tiny,
// long-lived process that hosts ONLY the reverse-proxy /anthropic/* pipe
// (+ /healthz + /version). It is the process agents pin via
// ANTHROPIC_BASE_URL; splitting it off the `bacio web` UI/REST server means
// the binary, UI, and schema can be upgraded — restart `bacio web` — without
// dropping the liveness-critical proxy listener under in-flight agent turns.
//
// A harness-integration shim like `bacio api` / `bacio web` (not a six-rule
// mutating verb): no --json / --dry-run / schema entry. It binds the stable
// proxy port (env.ProxyAddr, the API port minus one) by default.
func newProxyServeCmd() *cobra.Command {
	var (
		addr string
		port int
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the standalone reverse-proxy listener (upgrade web without interrupting agents)",
		Long: `Run a deliberately tiny, long-lived process that hosts ONLY the BACI-301
reverse-proxy /anthropic/* pipe agents pin via ANTHROPIC_BASE_URL (plus the
unauthenticated /healthz + /version reads). No UI, no REST API, no controller,
no leader election.

This is the process you essentially never restart. With agents pointed at it,
you can stop / upgrade / restart 'bacio web' (UI + REST API + schema migrations)
freely — the proxy on the stable port keeps answering, so a mid-session agent's
Anthropic traffic is never interrupted.

Binds the stable proxy port by default — the resolved API port minus one
(127.0.0.1:5319 in the legacy default; <allocated>-1 under a worktree
manifest). Override the bind via --addr (host:port) or just the port via
--port. Capture is still recorded to proxy_requests + the raw .http files,
exactly as the all-in-one 'bacio web' does.

Use 'bacio proxy install-service' to register this command with the OS
supervisor (launchd / systemd user unit) so it survives logout / restart.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Resolve the worktree env once: we need it for the default
			// proxy bind addr AND for the BACI-73 logging resolver, which
			// reads the manifest's allocations.log_dir field.
			env, err := resolveEnv()
			if err != nil {
				return err
			}
			// If --addr was left at its zero-value sentinel, default to the
			// resolved stable proxy addr (env.ProxyAddr = API port − 1) so a
			// manifest-aware worktree picks up its allocated proxy port
			// without the user repeating --addr on every invocation.
			if !cmd.Flags().Changed("addr") {
				addr = env.ProxyAddr
			}
			if port > 0 {
				host, _, err := net.SplitHostPort(addr)
				if err != nil {
					return fmt.Errorf("invalid --addr %q: %w", addr, err)
				}
				addr = net.JoinHostPort(host, strconv.Itoa(port))
			}

			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()

			// Component "proxy" so the per-day filename is
			// bacio-proxy-YYYY-MM-DD.log and a concurrent `bacio web` doesn't
			// fight over the same handle.
			logger, closeLogger := buildComponentLogger(env, "proxy")
			defer closeLogger()
			logger.Info("proxy starting",
				"addr", addr,
				"db", env.DBPath,
				"env_source", string(env.Source),
				"env_path", env.ManifestPath,
				"version", version.String(),
			)

			// Resolve the log dir for the BACI-302 proxy capture sink. A
			// resolution failure isn't fatal — the recorder simply skips the
			// raw-to-disk write (index rows still land) — so swallow the
			// error and pass an empty dir.
			var logDir string
			if logRes, err := resolveLogging(env); err == nil {
				logDir = logRes.Dir
			}
			srv := api.NewProxyServer(s, api.Options{
				Addr:   addr,
				DBPath: env.DBPath,
				LogDir: logDir,
			}, logger)

			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			err = friendlyServeErr(addr, srv.Run(ctx))
			if err != nil {
				logger.Error("proxy shutdown", "err", err)
			} else {
				logger.Info("proxy shutdown", "reason", "signal")
			}
			return err
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:5319", "bind address (host:port); defaults to the resolved proxy port (API port − 1)")
	cmd.Flags().IntVar(&port, "port", 0, "shorthand to override only the port (keeps host from --addr)")
	return cmd
}
