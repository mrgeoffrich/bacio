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

			logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
			srv := api.New(s, api.Options{
				Addr:        addr,
				Token:       token,
				CORSOrigins: corsOrigins,
			}, logger)

			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			return srv.Run(ctx)
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:5320", "bind address (host:port)")
	cmd.Flags().IntVar(&port, "port", 0, "shorthand to override only the port (keeps host from --addr)")
	cmd.Flags().StringVar(&token, "token", "", "shared bearer token; falls back to BACIO_API_TOKEN env var")
	cmd.Flags().StringSliceVar(&corsOrigins, "cors-origin", nil,
		"allow cross-origin browser requests from this origin (repeatable; e.g. http://localhost:5174). Empty allow-list = same-origin only.")
	return cmd
}
