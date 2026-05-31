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
	cmd.AddCommand(newProxyCapturesCmd())
	cmd.AddCommand(newProxyCaptureCmd())
	cmd.AddCommand(newProxyRawCmd())
	cmd.AddCommand(newProxyJobCmd())
	cmd.AddCommand(newProxyGrepCmd())
	return cmd
}

// newProxyGrepCmd is `bacio proxy grep <text>` (alias `search`) — the BACI-320
// content search over the captured Anthropic message bodies, complementing the
// index-level filtering `proxy captures` does. It greps the parsed turns for a
// case-insensitive substring and emits one match line per matching content block,
// each carrying its capture id so you can drill straight into `proxy capture
// <id>` — closing the search→drill-in loop. Scope with the same correlation
// filters as `captures` (--dispatch / --session / --agent / --since), plus
// optional --role / --block. Read-only, so prefer `-o json` for the full rows.
func newProxyGrepCmd() *cobra.Command {
	var (
		role     string
		block    string
		dispatch int64
		session  string
		agent    string
		sinceStr string
		limit    int
	)
	cmd := &cobra.Command{
		Use:     "grep <text>",
		Aliases: []string{"search"},
		Short:   "Search captured Anthropic turns by content",
		Long: `Search the captured Anthropic message bodies for a substring — the
content filter that complements 'bacio proxy captures' (the index filter). The
match is case-insensitive; each result is one content block, carrying its capture
id so you can drill in with 'bacio proxy capture <id>' / 'proxy raw <id>'.

Scope with the same correlation filters as 'captures' — --dispatch (one job),
--session, --agent, --since (a lookback window) — plus optional --role
(assistant | user) and --block (text | thinking | tool_use | tool_result).

  --since accepts a duration lookback: 30m, 1h, 1d, 2w

The search runs over the parsed message bodies, which store JSON-escaped content,
so a needle containing characters JSON escapes (a literal quote, a newline) won't
match; this is a plain-word forensic search. The raw .http bytes remain available
via 'proxy raw <id>' for a known capture.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch role {
			case "", "assistant", "user":
			default:
				return fmt.Errorf("invalid --role %q (want assistant or user)", role)
			}
			switch block {
			case "", "text", "thinking", "tool_use", "tool_result":
			default:
				return fmt.Errorf("invalid --block %q (want text, thinking, tool_use or tool_result)", block)
			}

			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()

			f := store.ProxyMessageFilter{
				Query: args[0], Role: role, Block: block,
				SessionID: session, ClaudeAgentID: agent, Limit: limit,
			}
			if dispatch > 0 {
				f.DispatchID = &dispatch
			}
			if sinceStr != "" {
				d, err := timeparse.Lookback(sinceStr)
				if err != nil {
					return err
				}
				cutoff := time.Now().Add(-d)
				f.Since = &cutoff
			}
			matches, err := c.SearchProxyMessages(context.Background(), f)
			if err != nil {
				return err
			}
			return emit(matches)
		},
	}
	cmd.Flags().StringVar(&role, "role", "", "scope to one role (assistant or user)")
	cmd.Flags().StringVar(&block, "block", "", "scope to one block type (text, thinking, tool_use, tool_result)")
	cmd.Flags().Int64Var(&dispatch, "dispatch", 0, "scope to one dispatch's captures")
	cmd.Flags().StringVar(&session, "session", "", "scope to one Claude Code session id")
	cmd.Flags().StringVar(&agent, "agent", "", "scope to one Claude Code agent id")
	cmd.Flags().StringVar(&sinceStr, "since", "", "look back this far (e.g. 30m, 1h, 1d, 2w)")
	cmd.Flags().IntVar(&limit, "limit", 0, "max match lines (0 for the default cap)")
	return cmd
}

// newProxyCapturesCmd is `bacio proxy captures` — the BACI-308 filtered
// capture list the Monitor drill-down walks an FQDN stat row down into. It
// scopes to a host (and optionally a dispatch / anthropic-only / lookback
// window), newest-first and capped, each row enriched with its dispatch's
// issue-key + mode. Read-only, so prefer `-o json` for the full rows.
func newProxyCapturesCmd() *cobra.Command {
	var (
		host         string
		dispatch     int64
		anthropic    bool
		anthropicSet bool
		sinceStr     string
		limit        int
	)
	cmd := &cobra.Command{
		Use:   "captures",
		Short: "List captured requests for a host (or a job)",
		Long: `List the individual proxy captures behind a 'bacio proxy stats'
host — the per-request rows the Monitor drill-down walks into (BACI-308). Scope
with --host (the FQDN the rollup pivots on) and optionally --dispatch (one job's
captures), --anthropic (only the parseable message-API captures), and --since (a
lookback window). Rows are newest-first and capped; each carries its dispatch's
issue key + mode where the capture correlates.

  --since accepts a duration lookback: 30m, 1h, 1d, 2w`,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()

			f := store.ProxyRequestFilter{Host: host, Limit: limit}
			if dispatch > 0 {
				f.DispatchID = &dispatch
			}
			if anthropicSet {
				f.IsAnthropic = &anthropic
			}
			if sinceStr != "" {
				d, err := timeparse.Lookback(sinceStr)
				if err != nil {
					return err
				}
				cutoff := time.Now().Add(-d)
				f.Since = &cutoff
			}
			rows, err := c.ListProxyCaptures(context.Background(), f)
			if err != nil {
				return err
			}
			return emit(rows)
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "scope to one upstream FQDN (e.g. api.anthropic.com)")
	cmd.Flags().Int64Var(&dispatch, "dispatch", 0, "scope to one dispatch's captures")
	cmd.Flags().BoolVar(&anthropic, "anthropic", false, "only the parseable Anthropic message-API captures")
	cmd.Flags().StringVar(&sinceStr, "since", "", "look back this far (e.g. 30m, 1h, 1d, 2w)")
	cmd.Flags().IntVar(&limit, "limit", 0, "max rows (0 for the default cap)")
	cmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		anthropicSet = cmd.Flags().Changed("anthropic")
		return nil
	}
	return cmd
}

// newProxyRawCmd is `bacio proxy raw <id>` — the BACI-308 raw .http capture
// for one proxy_requests id: the inflated, auth-redacted req/resp bytes the
// recorder wrote to disk. The escape hatch for a capture that isn't a
// parseable Anthropic turn (a count_tokens probe, an error, a non-Anthropic
// host). Returns not-found when the row has no raw file or it's been pruned.
func newProxyRawCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "raw <id>",
		Short: "Print the raw .http capture for one request",
		Long: `Print the raw req/resp capture for one proxied request — the
inflated, auth-redacted .http bytes the recorder wrote to disk (BACI-302/308).
The id is the proxy_requests id (the id 'bacio proxy stats' / 'captures'
reference). Use it to inspect a capture that isn't a parseable Anthropic turn
(a count_tokens probe, an error reply, a non-Anthropic host). Returns not-found
when the row has no raw file or the file has been pruned.`,
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
			raw, err := c.ProxyCaptureRaw(context.Background(), id)
			if err != nil {
				return err
			}
			cmd.OutOrStdout().Write(raw)
			return nil
		},
	}
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
