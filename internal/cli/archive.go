// BACI-68: top-level `bacio archive` command group. Holds the manual
// sweep trigger (mirrors the hourly leader-elected Controller tick).
// The per-entity archive / unarchive verbs live alongside their entity
// command groups (issue / feature / doc) — keeping them next to the
// other CRUD verbs for that surface is the more discoverable layout
// than nesting everything under `bacio archive`.
package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/inputio"
)

func newArchiveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "archive",
		Short: "BACI-68 archive lifecycle helpers (sweep + per-entity verbs)",
		Long: `Manual triggers for the BACI-68 archive lifecycle. The hourly
leader-elected Controller tick runs ` + "`archive sweep`" + ` automatically;
the verb is exposed here so users (and agents) can run it on demand
without waiting for the hour. Per-entity manual verbs live on their
own command groups: ` + "`bacio issue archive`" + ` / ` + "`bacio doc archive`" + ` /
` + "`bacio feature archive`" + ` (and the matching ` + "`unarchive`" + ` counterparts).`,
	}
	cmd.AddCommand(archiveSweepCmd())
	return cmd
}

func archiveSweepCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "sweep",
		Short: "Run the BACI-68 archive sweep on demand (same three SQL passes the leader runs hourly)",
		Long: `Run the archive sweep on demand. The sweep:

  1. Archives issues in (done, cancelled) older than 4 days that aren't already archived.
  2. Archives features whose every child issue is archived (and the feature had at least one child).
  3. Archives documents whose every linked parent (issue and/or feature) is archived (and the doc had at least one link).

Idempotent — safe to run on a quiet DB. Returns the per-pass counts.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				if _, _, err := inputio.DecodeStrict[inputs.ArchiveSweepInput](raw); err != nil {
					return err
				}
			}
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()
			res, err := c.ArchiveSweep(context.Background(), opts.dryRun)
			if err != nil {
				return err
			}
			if opts.dryRun {
				return emitDryRun(res)
			}
			return emit(res)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}
