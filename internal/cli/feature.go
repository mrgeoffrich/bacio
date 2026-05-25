package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/agentmode"
	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/inputio"
	"github.com/mrgeoffrich/bacio/internal/model"
)

func newFeatureCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "feature", Short: "Manage features (groups of issues)"}
	cmd.AddCommand(
		featureAddCmd(),
		featureListCmd(),
		featureShowCmd(),
		featureEditCmd(),
		featureRmCmd(),
		featurePlanCmd(),
		featureArchiveCmd(),
		featureUnarchiveCmd(),
		featureStateCmd(),
		newFeatureCommentCmd(),
	)
	return cmd
}

func featureAddCmd() *cobra.Command {
	var (
		slug, description, descriptionFile, emoji, rawInput string
	)
	cmd := &cobra.Command{
		Use:   "add [title]",
		Short: "Create a feature in the current repo",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput,
				"slug", "description", "description-file", "emoji")
			if err != nil {
				return err
			}
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.FeatureAddInput](raw)
				if err != nil {
					return err
				}
				if in.Title == "" {
					return fmt.Errorf("title is required")
				}
				return createFeature(*in)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <title> positional or --json")
			}
			desc, err := readLongText(description, descriptionFile, false, "description")
			if err != nil {
				return err
			}
			return createFeature(inputs.FeatureAddInput{
				Title:       args[0],
				Slug:        slug,
				Description: desc,
				Emoji:       emoji,
			})
		},
	}
	cmd.Flags().StringVar(&slug, "slug", "", "explicit slug (default: derived from title)")
	cmd.Flags().StringVar(&description, "description", "", "description text or '-' for stdin")
	cmd.Flags().StringVar(&descriptionFile, "description-file", "", "path to a markdown file")
	cmd.Flags().StringVar(&emoji, "emoji", "", "single-glyph emoji for the feature (kanban card decoration; empty for none)")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func createFeature(in inputs.FeatureAddInput) error {
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	repo, err := resolveRepoC(c)
	if err != nil {
		return err
	}
	f, err := c.CreateFeature(context.Background(), repo, in, opts.dryRun)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(f)
	}
	return emit(f)
}

func featureListCmd() *cobra.Command {
	var (
		withDescription bool
		includeArchived bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List features in the current repo (descriptions are stripped by default; pass --with-description to include them)",
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
			// BACI-68: archived features hidden by default. Per-call
			// --include-archived overrides; the display.show_archived
			// setting also lifts the filter when on.
			show := includeArchived
			if !show {
				v, _ := c.GetDisplayShowArchived(context.Background())
				show = v
			}
			fs, err := c.ListFeatures(context.Background(), repo, withDescription, show)
			if err != nil {
				return err
			}
			return emit(fs)
		},
	}
	cmd.Flags().BoolVar(&withDescription, "with-description", false, "include each feature's full description in JSON output (off by default to keep responses small)")
	cmd.Flags().BoolVar(&includeArchived, "include-archived", false, "include archived features in the list (BACI-68); overrides the display.show_archived setting for this call")
	return cmd
}

func featureShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <slug>",
		Short: "Show a feature with its issues, attachments, and linked documents",
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
			view, err := retryWithRedirect(repo, "feature", args[0],
				func(slug string) (*client.FeatureView, error) {
					return c.ShowFeature(context.Background(), repo, slug)
				})
			if err != nil {
				return err
			}
			return emit(&featureView{
				Feature:   view.Feature,
				Issues:    view.Issues,
				Documents: view.Documents,
				Comments:  view.Comments,
			})
		},
	}
}

func featureEditCmd() *cobra.Command {
	var (
		title, description, descriptionFile, emoji, rawInput string
	)
	cmd := &cobra.Command{
		Use:   "edit [slug]",
		Short: "Edit a feature's title, description, or emoji",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput,
				"title", "description", "description-file", "emoji")
			if err != nil {
				return err
			}
			if raw != nil {
				in, present, err := inputio.DecodeStrict[inputs.FeatureEditInput](raw)
				if err != nil {
					return err
				}
				if in.Slug == "" {
					return fmt.Errorf("slug is required")
				}
				var tPtr, dPtr, ePtr *string
				if _, ok := present["title"]; ok {
					if in.Title == nil || *in.Title == "" {
						return fmt.Errorf("title cannot be empty or null; omit the field to leave it unchanged")
					}
					tPtr = in.Title
				}
				if _, ok := present["description"]; ok {
					if in.Description == nil {
						empty := ""
						dPtr = &empty
					} else {
						dPtr = in.Description
					}
				}
				if _, ok := present["emoji"]; ok {
					if in.Emoji == nil {
						empty := ""
						ePtr = &empty
					} else {
						ePtr = in.Emoji
					}
				}
				return applyFeatureEdit(in.Slug, tPtr, dPtr, ePtr)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <slug> positional or --json")
			}
			var tPtr, dPtr, ePtr *string
			if cmd.Flags().Changed("title") {
				tPtr = &title
			}
			if description != "" || descriptionFile != "" {
				d, err := readLongText(description, descriptionFile, true, "description")
				if err != nil {
					return err
				}
				dPtr = &d
			}
			if cmd.Flags().Changed("emoji") {
				ePtr = &emoji
			}
			return applyFeatureEdit(args[0], tPtr, dPtr, ePtr)
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "new title")
	cmd.Flags().StringVar(&description, "description", "", "new description text or '-' for stdin")
	cmd.Flags().StringVar(&descriptionFile, "description-file", "", "path to a markdown file")
	cmd.Flags().StringVar(&emoji, "emoji", "", "new emoji (single glyph; pass --emoji '' to clear)")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func applyFeatureEdit(slug string, tPtr, dPtr, ePtr *string) error {
	if tPtr == nil && dPtr == nil && ePtr == nil {
		return fmt.Errorf("nothing to update; pass title, description, and/or emoji")
	}
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	repo, err := resolveRepoC(c)
	if err != nil {
		return err
	}
	updated, err := c.UpdateFeature(context.Background(), repo, slug, tPtr, dPtr, ePtr, opts.dryRun)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(updated)
	}
	return emit(updated)
}

func featureRmCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "rm [slug]",
		Short: "Delete a feature (issues are kept, unlinked from it)",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := agentmode.DenyIfEnabled("feature rm"); err != nil {
				return err
			}
			raw, err := parseJSONInput(cmd, args, rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.FeatureRmInput](raw)
				if err != nil {
					return err
				}
				if in.Slug == "" {
					return fmt.Errorf("slug is required")
				}
				return removeFeature(in.Slug)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <slug> positional or --json")
			}
			return removeFeature(args[0])
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func removeFeature(slug string) error {
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	repo, err := resolveRepoC(c)
	if err != nil {
		return err
	}
	deleted, preview, err := c.DeleteFeature(context.Background(), repo, slug, opts.dryRun)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(&featureDeletePreview{
			Feature:        preview.Feature,
			WouldDelete:    preview.WouldDelete,
			IssuesUnlinked: preview.IssuesUnlinked,
			DocumentLinks:  preview.DocumentLinks,
			Comments:       preview.Comments,
		})
	}
	return ok("feature %s deleted", deleted.Slug)
}

// featureDeletePreview is the dry-run payload for `bacio feature rm`.
// IssuesUnlinked counts issues that would have their feature_id set to NULL
// (the schema cascades via SET NULL, not DELETE); DocumentLinks counts
// document_links rows that would actually be removed.
type featureDeletePreview struct {
	Feature        *model.Feature `json:"feature"`
	WouldDelete    bool           `json:"would_delete"`
	IssuesUnlinked int            `json:"issues_unlinked"`
	DocumentLinks  int            `json:"document_links"`
	// Comments is the BACI-124 feature_comments cascade count.
	Comments int `json:"comments"`
}

// featureArchiveCmd / featureUnarchiveCmd are the BACI-68 manual
// archive verbs on the feature surface.
func featureArchiveCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "archive [SLUG]",
		Short: "Archive a feature (BACI-68) — hides it from default lists; row + children are untouched",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runFeatureArchiveVerb(cmd, args, rawInput, true)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func featureUnarchiveCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "unarchive [SLUG]",
		Short: "Unarchive a feature (BACI-68) — clears archived_at",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runFeatureArchiveVerb(cmd, args, rawInput, false)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

// featureStateCmd (BACI-199) — `bacio feature state <slug> <state>`
// (or `--json '{"slug":"x","state":"done"}'`). Mirrors the shape of
// issueStateCmd: positional or JSON, dry-run honoured, strict-decode
// rejects unknown fields. Manual writes via this verb stamp the
// sticky bit so the leader-elected archive-sweep's auto-completion
// pass leaves the row alone.
func featureStateCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "state [SLUG] [state]",
		Short: "Set a feature's state (active|done|cancelled). Pins the value against the auto-completion sweep (BACI-199).",
		Args:  cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.FeatureStateInput](raw)
				if err != nil {
					return err
				}
				if in.Slug == "" || in.State == "" {
					return fmt.Errorf("slug and state are required")
				}
				return setFeatureState(in.Slug, in.State)
			}
			if len(args) != 2 {
				return fmt.Errorf("requires <SLUG> <state> positionals or --json")
			}
			return setFeatureState(args[0], args[1])
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func setFeatureState(slug, stateStr string) error {
	st, err := model.ParseFeatureState(stateStr)
	if err != nil {
		return err
	}
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	repo, err := resolveRepoC(c)
	if err != nil {
		return err
	}
	updated, err := c.SetFeatureState(context.Background(), repo, slug, st, opts.dryRun)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(updated)
	}
	return emit(updated)
}

func runFeatureArchiveVerb(cmd *cobra.Command, args []string, rawInput string, archive bool) error {
	raw, err := parseJSONInput(cmd, args, rawInput)
	if err != nil {
		return err
	}
	var slug string
	if raw != nil {
		if archive {
			in, _, err := inputio.DecodeStrict[inputs.FeatureArchiveInput](raw)
			if err != nil {
				return err
			}
			slug = in.Slug
		} else {
			in, _, err := inputio.DecodeStrict[inputs.FeatureUnarchiveInput](raw)
			if err != nil {
				return err
			}
			slug = in.Slug
		}
	} else {
		if len(args) != 1 {
			return fmt.Errorf("requires <SLUG> positional or --json")
		}
		slug = args[0]
	}
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	repo, err := resolveRepoC(c)
	if err != nil {
		return err
	}
	var updated *model.Feature
	if archive {
		updated, err = c.ArchiveFeature(context.Background(), repo, slug, opts.dryRun)
	} else {
		updated, err = c.UnarchiveFeature(context.Background(), repo, slug, opts.dryRun)
	}
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(updated)
	}
	return emit(updated)
}
