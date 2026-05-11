package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
	"github.com/mrgeoffrich/bacio/internal/timeparse"
)

func newHistoryCmd() *cobra.Command {
	var (
		limit       int
		offset      int
		userFilter  string
		opFilter    string
		kindFilter  string
		sinceStr    string
		fromStr     string
		toStr       string
		allRepos    bool
		oldestFirst bool
	)
	cmd := &cobra.Command{
		Use:   "history",
		Short: "Show the audit log of mutations",
		Long: `By default, lists the most recent mutations in the current repo
(newest first). All filters can be combined freely except --since,
which is mutually exclusive with --from (it's just sugar for
--from = now - duration).

Time inputs:
  --since accepts durations: 30m, 1h, 1d, 2w
  --from / --to accept either local-time stamps (YYYY-MM-DD,
  YYYY-MM-DD HH:MM, YYYY-MM-DD HH:MM:SS) or RFC 3339 (Z-suffixed
  UTC). Bare dates start at 00:00 in the local timezone.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if sinceStr != "" && fromStr != "" {
				return fmt.Errorf("--since and --from are mutually exclusive")
			}
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()

			f := store.HistoryFilter{
				Limit:       limit,
				Offset:      offset,
				Actor:       userFilter,
				Op:          opFilter,
				Kind:        kindFilter,
				OldestFirst: oldestFirst,
			}
			var repo *model.Repo
			if !allRepos {
				repo, err = resolveRepoC(c)
				if err != nil {
					return err
				}
			}
			if sinceStr != "" {
				d, err := timeparse.Lookback(sinceStr)
				if err != nil {
					return err
				}
				cutoff := time.Now().Add(-d)
				f.From = &cutoff
			}
			if fromStr != "" {
				t, err := timeparse.Timestamp(fromStr)
				if err != nil {
					return fmt.Errorf("--from: %w", err)
				}
				f.From = &t
			}
			if toStr != "" {
				t, err := timeparse.Timestamp(toStr)
				if err != nil {
					return fmt.Errorf("--to: %w", err)
				}
				f.To = &t
			}
			if f.From != nil && f.To != nil && f.To.Before(*f.From) {
				return fmt.Errorf("--to (%s) must not be before --from (%s)", f.To.Local(), f.From.Local())
			}
			entries, err := c.ListHistory(context.Background(), repo, f)
			if err != nil {
				return err
			}
			return emit(entries)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 50, "max number of entries (0 for no limit)")
	cmd.Flags().IntVar(&offset, "offset", 0, "skip the first N entries (for pagination)")
	cmd.Flags().StringVar(&userFilter, "user-filter", "", "filter by actor name")
	cmd.Flags().StringVar(&opFilter, "op", "", "filter by op (e.g. issue.create)")
	cmd.Flags().StringVar(&kindFilter, "kind", "", "filter by kind (issue, feature, document, repo)")
	cmd.Flags().StringVar(&sinceStr, "since", "", "look back this far (e.g. 30m, 1h, 1d, 2w); shorthand for --from")
	cmd.Flags().StringVar(&fromStr, "from", "", "include entries on/after this timestamp")
	cmd.Flags().StringVar(&toStr, "to", "", "include entries on/before this timestamp")
	cmd.Flags().BoolVar(&allRepos, "all-repos", false, "include entries from every repo")
	cmd.Flags().BoolVar(&oldestFirst, "oldest-first", false, "sort oldest first instead of newest first")
	return cmd
}
