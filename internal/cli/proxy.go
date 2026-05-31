package cli

import (
	"context"
	"fmt"
	"strconv"
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
	cmd.AddCommand(newProxyCaptureCmd())
	cmd.AddCommand(newProxyJobCmd())
	return cmd
}

// newProxyCaptureCmd is `bacio proxy capture <id>` — the BACI-306 parsed detail
// of one captured Anthropic SSE turn, keyed on the proxy_requests id (the id the
// raw .http file and `proxy stats` reference). Read-only, so prefer `-o json`
// for the full structured turn.
func newProxyCaptureCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "capture <id>",
		Short: "Show the parsed detail of one captured Anthropic SSE turn",
		Long: `Show the reconstructed assistant turn for one captured Anthropic
/v1/messages exchange — the ordered text / thinking / tool_use blocks and merged
token usage parsed from the response SSE stream (BACI-306). The id is the
proxy_requests id (the id the raw .http capture and 'bacio proxy stats'
reference). Only parseable streaming captures have detail; a non-stream
count_tokens / error reply or a truncated capture returns not-found.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil || id <= 0 {
				return fmt.Errorf("invalid capture id %q", args[0])
			}
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()
			msg, err := c.AnthropicCapture(context.Background(), id)
			if err != nil {
				return err
			}
			return emit(msg)
		},
	}
}

// newProxyJobCmd is `bacio proxy job <dispatch_id>` — the BACI-306 assembled
// per-job transcript for a dispatch: the ordered primary-thread messages, summed
// token usage, and the auxiliary turns (title-gen / structured-output probes).
// Read-only; prefer `-o json` for the full transcript.
func newProxyJobCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "job <dispatch_id>",
		Short: "Show a dispatch's assembled message transcript",
		Long: `Assemble a dispatch's captured Anthropic turns into one ordered
message transcript (BACI-306) — the primary conversation thread (user / tool_result
turns interleaved with the assistant turns), summed token usage across the job,
and the auxiliary turns kept separate. The dispatch_id is the per-job key the
capture correlates on. Returns not-found when the dispatch has no parsed captures.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil || id <= 0 {
				return fmt.Errorf("invalid dispatch id %q", args[0])
			}
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()
			tr, err := c.JobTranscript(context.Background(), id)
			if err != nil {
				return err
			}
			return emit(tr)
		},
	}
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
