package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/model"
)

func featurePlanCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "plan <slug>",
		Short: "Print feature issues in execution order, respecting `blocks` dependencies",
		Long: `Compute a topological order over the open issues in a feature, using the
'blocks' relation to determine dependencies. Issues with all blockers satisfied
appear first; issues blocked by other open work appear after their blockers.

Open issues are anything not in done / cancelled. Cross-feature
blockers (open issues outside this feature) are surfaced as blocked_by hints
but cannot be ordered against in-feature issues, so they don't gate the topo
position.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()
			repo, err := resolveRepoC(c)
			if err != nil {
				return err
			}
			// `bacio feature plan` is the open-only text rendering;
			// BACI-236's include-closed widening is reserved for the
			// dependency-graph view on the Features screen.
			view, err := c.PlanFeature(context.Background(), repo, args[0], false)
			if err != nil {
				return err
			}
			out := &planView{Feature: view.Feature}
			for _, e := range view.Order {
				out.Order = append(out.Order, planEntry{
					Key:       e.Key,
					Title:     e.Title,
					State:     e.State,
					Assignee:  e.Assignee,
					BlockedBy: e.BlockedBy,
				})
			}
			return emit(out)
		},
	}
}

type planEntry struct {
	Key       string      `json:"key"`
	Title     string      `json:"title"`
	State     model.State `json:"state"`
	Assignee  string      `json:"assignee,omitempty"`
	BlockedBy []string    `json:"blocked_by,omitempty"`
}

type planView struct {
	Feature string      `json:"feature"`
	Order   []planEntry `json:"order"`
}
