package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/api"
	"github.com/mrgeoffrich/bacio/internal/browseropen"
	"github.com/mrgeoffrich/bacio/internal/version"
)

// friendlyServeErr rewrites a raw "bind: address already in use" from
// the API server into an actionable message. The bare syscall error
// reads as "free this port", which invites a dispatched agent to kill
// whatever holds it — and when the holder is the user's own running
// `bacio web` / desktop app, that takes a live UI down. Naming the
// likely cause and pointing at the right fix (a per-worktree port, or
// an explicit free one — never a kill) heads that off. Non-bind
// errors pass straight through.
func friendlyServeErr(addr string, err error) error {
	if err == nil || !errors.Is(err, syscall.EADDRINUSE) {
		return err
	}
	return fmt.Errorf("cannot bind %s: another process is already listening there.\n\n"+
		"This is almost certainly another bacio instance — your own `bacio web`,\n"+
		"the desktop app, or a sibling worktree. Do NOT kill the process holding\n"+
		"the port; that may take down a UI you depend on. Instead either:\n"+
		"  • run this from a worktree set up with `bacio worktree init`, so it\n"+
		"    gets its own port automatically, or\n"+
		"  • pass an explicit free port with --port / --addr.\n"+
		"`bacio status` reports the port this instance resolved.", addr)
}

// browserPollInterval and browserPollDeadline bound how long the
// goroutine waits for /healthz to come up before launching the
// browser. Polling rather than racing on a Listen-callback keeps
// internal/api/ untouched and copes naturally with a token-protected
// server (the /healthz handler is auth-exempt).
//
// Exposed as variables (not constants) so the test can shorten them.
var (
	browserPollInterval = 100 * time.Millisecond
	browserPollDeadline = 5 * time.Second
)

// browserLauncher is the seam tests stub to capture the would-be
// browseropen.Open call without actually shelling out. The default
// implementation defers to the real helper.
var browserLauncher = browseropen.Open

func newWebCmd() *cobra.Command {
	var (
		addr        string
		port        int
		token       string
		corsOrigins []string
		noOpen      bool
	)
	cmd := &cobra.Command{
		Use:   "web",
		Short: "Run the REST API + embedded web bundle and open the browser",
		Long: `Run the local HTTP REST API on top of the same SQLite database the CLI
uses, mount the embedded React web bundle at /ui/, and open the OS
default browser to http://<addr>/ui/.

Same flag surface as 'bacio api', plus --no-open to skip the browser
launch (useful for SSH sessions, headless CI, or agent-driven smoke
tests that drive the page through Playwright instead of a real
browser). The /ui/ mount and the bare /ui → /ui/ redirect are
deliberately scoped to this command — 'bacio api' is API-only and
serves 404 on /ui/ (BACI-72).

When the embedded bundle is absent (e.g. './build.sh --skip-web') the
server still runs, but a one-line hint is printed and the browser
launch is skipped — there is nothing meaningful to open.

Incoming requests carry their own actor via the X-Actor header
(default "api").`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// If --addr was left at its zero-value sentinel, resolve via
			// the worktree manifest chain (BACI-63) — so a manifest-aware
			// worktree picks up its allocated port without the user
			// repeating --addr on every invocation.
			//
			// Resolved unconditionally (not just when --addr is
			// unchanged) because env.DBPath also feeds the BACI-89
			// background sync runner's cross-process lock.
			env, err := resolveEnv()
			if err != nil {
				return err
			}
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

			// BACI-121: route web + leader + sync + controller events into
			// a per-process log file via the BACI-73 chain. Component
			// "web" so concurrent `bacio api` + `bacio web` processes
			// don't fight over the same bacio-api-YYYY-MM-DD.log handle.
			// A creation failure falls back to stderr-only with a single
			// warning — never blocks the process from starting.
			logger, closeLogger := buildComponentLogger(env, "web")
			defer closeLogger()
			logger.Info("web starting",
				"addr", addr,
				"db", env.DBPath,
				"env_source", string(env.Source),
				"env_path", env.ManifestPath,
				"version", version.String(),
			)

			// Bundle-absence check at startup so the user sees the hint
			// without having to refresh a tab. The server still runs —
			// the missing bundle is a build-time omission, not a
			// runtime catastrophe — but --no-open is implied because
			// there is nothing meaningful to open.
			bundlePresent := api.HasWebUI()
			if !bundlePresent {
				fmt.Fprintln(os.Stderr,
					"warning: the bacio web UI bundle hasn't been built into this binary.")
				fmt.Fprintln(os.Stderr,
					"  Rebuild with `./build.sh` (the web bundle is default-on; --skip-web opts out).")
				fmt.Fprintln(os.Stderr,
					"  Skipping the browser launch — /ui/ would 404.")
			}

			srv := api.New(s, api.Options{
				Addr:        addr,
				Token:       token,
				CORSOrigins: corsOrigins,
				MountUI:     true,
				DBPath:      env.DBPath,
			}, logger)

			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			// Spawn the browser-opener BEFORE Run blocks. The goroutine
			// polls /healthz until the listener is up (or the context
			// cancels), then defers to browseropen.Open. A launch
			// failure logs at info level and exits the goroutine — the
			// server keeps running.
			if !noOpen && bundlePresent {
				go openBrowserWhenReady(ctx, addr, logger)
			}

			err = friendlyServeErr(addr, srv.Run(ctx))
			if err != nil {
				logger.Error("web shutdown", "err", err)
			} else {
				logger.Info("web shutdown", "reason", "signal")
			}
			return err
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:5320", "bind address (host:port)")
	cmd.Flags().IntVar(&port, "port", 0, "shorthand to override only the port (keeps host from --addr)")
	cmd.Flags().StringVar(&token, "token", "", "shared bearer token; falls back to BACIO_API_TOKEN env var")
	cmd.Flags().StringSliceVar(&corsOrigins, "cors-origin", nil,
		"allow cross-origin browser requests from this origin (repeatable; e.g. http://localhost:5174). Empty allow-list = same-origin only.")
	cmd.Flags().BoolVar(&noOpen, "no-open", false,
		"start the server and mount the bundle, but skip the browser launch (SSH, headless CI, agent-driven testing)")
	return cmd
}

// openBrowserWhenReady polls GET http://<addr>/healthz until it
// returns 200, then launches the OS default browser at /ui/. The
// browser launch is best-effort: a failure is logged at info level
// and the goroutine exits without affecting the server.
func openBrowserWhenReady(ctx context.Context, addr string, logger *slog.Logger) {
	url := fmt.Sprintf("http://%s/ui/", addr)
	healthURL := fmt.Sprintf("http://%s/healthz", addr)

	ticker := time.NewTicker(browserPollInterval)
	defer ticker.Stop()
	deadline := time.NewTimer(browserPollDeadline)
	defer deadline.Stop()

	client := &http.Client{Timeout: browserPollInterval}
	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			logger.Info("browser launch skipped: /healthz did not come up within deadline; open it manually",
				"url", url, "deadline", browserPollDeadline)
			return
		case <-ticker.C:
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
			if err != nil {
				continue
			}
			resp, err := client.Do(req)
			if err != nil {
				continue
			}
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				if err := browserLauncher(ctx, url); err != nil {
					logger.Info("browser launch failed; open it manually",
						"url", url, "err", err)
				} else {
					logger.Info("opened browser", "url", url)
				}
				return
			}
		}
	}
}
