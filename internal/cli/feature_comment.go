package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/agentmode"
	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/inputio"
)

// newFeatureCommentCmd is the BACI-124 `bacio feature comment` subgroup —
// the feature-scoped mirror of `bacio comment`. The address space differs
// (feature slug vs issue key), so it lives under the feature surface
// rather than under the issue-scoped command group.
func newFeatureCommentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "comment",
		Short: "Manage feature-scoped handoff comments (BACI-124)",
	}
	cmd.AddCommand(featureCommentAddCmd(), featureCommentRmCmd(), featureCommentListCmd())
	return cmd
}

func featureCommentAddCmd() *cobra.Command {
	var (
		author, body, bodyFile, kind, rawInput string
	)
	cmd := &cobra.Command{
		Use:   "add [SLUG]",
		Short: "Add a comment to a feature",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput, "as", "body", "body-file", "kind")
			if err != nil {
				return err
			}
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.FeatureCommentAddInput](raw)
				if err != nil {
					return err
				}
				if in.FeatureSlug == "" || in.Author == "" || in.Body == "" {
					return fmt.Errorf("feature_slug, author, and body are required")
				}
				return addFeatureComment(*in)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <SLUG> positional or --json")
			}
			text, err := readLongText(body, bodyFile, true, "body")
			if err != nil {
				return err
			}
			if author == "" {
				return fmt.Errorf("--as is required")
			}
			return addFeatureComment(inputs.FeatureCommentAddInput{
				FeatureSlug: args[0],
				Author:      author,
				Body:        text,
				Kind:        kind,
			})
		},
	}
	cmd.Flags().StringVar(&author, "as", "", "comment author name (required when not using --json)")
	cmd.Flags().StringVar(&body, "body", "", "comment body or '-' for stdin")
	cmd.Flags().StringVar(&bodyFile, "body-file", "", "path to a markdown file")
	// BACI-333: 'note' (default — a hand-typed comment, never gated) or
	// 'handoff' (a worker close-out note, dropped without error on a
	// feature with handoffs disabled).
	cmd.Flags().StringVar(&kind, "kind", "", "comment kind: note (default) or handoff")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func addFeatureComment(in inputs.FeatureCommentAddInput) error {
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	repo, err := resolveRepoC(c)
	if err != nil {
		return err
	}
	res, err := c.AddFeatureComment(context.Background(), repo, in, opts.dryRun)
	if err != nil {
		return err
	}
	// BACI-333: a dropped handoff emits the {skipped,reason} result so the
	// caller (worker or human) can see the write was a deliberate no-op
	// rather than a silent success. A real insert emits the comment row.
	out := func(v any) error {
		if opts.dryRun {
			return emitDryRun(v)
		}
		return emit(v)
	}
	if res.Skipped {
		return out(res)
	}
	return out(res.Comment)
}

func featureCommentRmCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "rm [SLUG] [COMMENT-UUID]",
		Short: "Delete a comment from a feature",
		Long: "Delete a feature-scoped comment. Comments are addressed by " +
			"their immutable uuid, discoverable via " +
			"`bacio feature comment list <slug> -o json`.",
		Args: cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := agentmode.DenyIfEnabled("feature comment rm"); err != nil {
				return err
			}
			raw, err := parseJSONInput(cmd, args, rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.FeatureCommentRmInput](raw)
				if err != nil {
					return err
				}
				if in.FeatureSlug == "" || in.CommentUUID == "" {
					return fmt.Errorf("feature_slug and comment_uuid are required")
				}
				return rmFeatureComment(*in)
			}
			if len(args) != 2 {
				return fmt.Errorf("requires <SLUG> <COMMENT-UUID> positionals or --json")
			}
			return rmFeatureComment(inputs.FeatureCommentRmInput{
				FeatureSlug: args[0],
				CommentUUID: args[1],
			})
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

// featureCommentDeletePreview is the output payload for `bacio feature
// comment rm --dry-run`.
type featureCommentDeletePreview struct {
	FeatureSlug string `json:"feature_slug"`
	CommentUUID string `json:"comment_uuid"`
	WouldRemove int    `json:"would_remove"`
}

func rmFeatureComment(in inputs.FeatureCommentRmInput) error {
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	repo, err := resolveRepoC(c)
	if err != nil {
		return err
	}
	preview, _, err := c.DeleteFeatureComment(context.Background(), repo, in, opts.dryRun)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(&featureCommentDeletePreview{
			FeatureSlug: preview.FeatureSlug,
			CommentUUID: preview.CommentUUID,
			WouldRemove: preview.WouldRemove,
		})
	}
	return ok("deleted comment %s from %s", in.CommentUUID, in.FeatureSlug)
}

func featureCommentListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <SLUG>",
		Short: "List comments on a feature",
		Args:  cobra.ExactArgs(1),
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
			cs, err := c.ListFeatureComments(context.Background(), repo, args[0])
			if err != nil {
				return err
			}
			return emit(cs)
		},
	}
}
