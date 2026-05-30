package cli

import (
	"context"
	"time"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/store"
	"github.com/mrgeoffrich/bacio/internal/timeparse"
)

// newProxyCmd is the `bacio proxy` parent for read-only inspection of the
// BACI-302 reverse-proxy capture. Read-only (like `bacio history` /
// `bacio status`), so the subcommands carry no --json / --dry-run / schema
// surface.
func newProxyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "proxy",
		Short: "Inspect reverse-proxy capture",
	}
	cmd.AddCommand(newProxyStatsCmd())
	return cmd
}

// newProxyStatsCmd is `bacio proxy stats` — the BACI-303 per-FQDN
// aggregation over the proxy_requests index (request count, bytes
// in/out, error rate, p50/p95 latency, first/last seen), busiest host
// first. The proxy_requests table is cross-cutting (no repo_id), so the
// verb takes no repo and runs anywhere.
func newProxyStatsCmd() *cobra.Command {
	var (
		sinceStr string
		limit    int
	)
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Per-FQDN rollup of proxied traffic",
		Long: `Roll the per-request proxy capture index up into per-FQDN
statistics — request count, bytes in/out, error rate, p50/p95 round-trip
latency, and first/last seen — so you can see at a glance what each agent
session is talking to. Hosts are listed busiest-first.

  --since accepts a duration lookback: 30m, 1h, 1d, 2w`,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()

			f := store.ProxyStatsFilter{Limit: limit}
			if sinceStr != "" {
				d, err := timeparse.Lookback(sinceStr)
				if err != nil {
					return err
				}
				cutoff := time.Now().Add(-d)
				f.Since = &cutoff
			}
			stats, err := c.ProxyStats(context.Background(), f)
			if err != nil {
				return err
			}
			return emit(stats)
		},
	}
	cmd.Flags().StringVar(&sinceStr, "since", "", "look back this far (e.g. 30m, 1h, 1d, 2w)")
	cmd.Flags().IntVar(&limit, "limit", 0, "max rows to fold into the rollup (0 for the default cap)")
	return cmd
}
