package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/tui"
)

func newTUICmd() *cobra.Command {
	var (
		snapshot string
		snapIss  string
		snapW    int
		snapH    int
		repoFlag string
	)
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Open the full-screen kanban board for the current repo",
		Long: `Open the full-screen kanban board for the current repo.

With --snapshot, renders a view to stdout non-interactively at the given
size. Useful for layout debugging and CI snapshot tests.

Snapshot targets:
  board, features, docs, history          — the four tabs as drawn
  card-overlay                            — focused card's fullscreen view
  doc-overlay, feature-overlay            — fullscreen reader for the
                                            first item in that tab
  picker                                  — board column-visibility picker`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if inRemoteMode() {
				return fmt.Errorf("bacio tui: not supported in remote mode (TUI talks to SQLite directly); start the TUI against a local DB")
			}
			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()
			var repo *model.Repo
			if repoFlag != "" {
				// Bypass the CWD/git-root resolution so synthetic repos
				// without a real working tree (e.g. `bacio demo` seeds)
				// can be opened by prefix.
				repo, err = s.GetRepoByPrefix(strings.ToUpper(repoFlag))
			} else {
				repo, err = resolveRepo(s)
			}
			if err != nil {
				return err
			}
			if snapshot != "" {
				return tui.Snapshot(s, repo, tui.SnapshotOpts{
					Target: snapshot,
					Issue:  snapIss,
					Width:  snapW,
					Height: snapH,
				})
			}
			return tui.Run(s, repo)
		},
	}
	cmd.Flags().StringVar(&snapshot, "snapshot", "", "render a view to stdout instead of starting the TUI (board|features|docs|history|card-overlay|doc-overlay|feature-overlay|picker)")
	cmd.Flags().StringVar(&snapIss, "issue", "", "issue key (e.g. MINI-1) to focus for board/card-overlay snapshots")
	cmd.Flags().IntVar(&snapW, "width", 120, "terminal width for --snapshot")
	cmd.Flags().IntVar(&snapH, "height", 40, "terminal height for --snapshot")
	cmd.Flags().StringVar(&repoFlag, "repo", "", "open this repo prefix instead of resolving from the current git working tree (useful for `bacio demo` seeds)")
	return cmd
}
