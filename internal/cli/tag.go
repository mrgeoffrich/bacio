package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/agentmode"
	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/inputio"
	"github.com/mrgeoffrich/bacio/internal/model"
)

func newTagCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "tag",
		Short:             "Manage tags on issues",
		PersistentPreRunE: requireClaimForGroup, // BACI-126b
	}
	cmd.AddCommand(tagAddCmd(), tagRmCmd())
	return cmd
}

func tagAddCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "add [KEY] [tag...]",
		Short: "Add one or more tags to an issue (idempotent)",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.TagAddInput](raw)
				if err != nil {
					return err
				}
				if in.IssueKey == "" || len(in.Tags) == 0 {
					return fmt.Errorf("issue_key and a non-empty tags array are required")
				}
				return mutateTags(in.IssueKey, in.Tags, true, true)
			}
			if len(args) < 2 {
				return fmt.Errorf("requires <KEY> <tag> [<tag>...] positionals or --json")
			}
			return mutateTags(args[0], args[1:], true, false)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func tagRmCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "rm [KEY] [tag...]",
		Short: "Remove one or more tags from an issue",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := agentmode.DenyIfEnabled("tag rm"); err != nil {
				return err
			}
			raw, err := parseJSONInput(cmd, args, rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.TagRmInput](raw)
				if err != nil {
					return err
				}
				if in.IssueKey == "" || len(in.Tags) == 0 {
					return fmt.Errorf("issue_key and a non-empty tags array are required")
				}
				return mutateTags(in.IssueKey, in.Tags, false, true)
			}
			if len(args) < 2 {
				return fmt.Errorf("requires <KEY> <tag> [<tag>...] positionals or --json")
			}
			return mutateTags(args[0], args[1:], false, false)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func mutateTags(key string, rawTags []string, add, strict bool) error {
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	if strict && !isIssueKey(key) {
		return fmt.Errorf("issue_key %q must be canonical (e.g. \"MINI-42\")", key)
	}
	repo, err := repoForIssueKey(c, key)
	if err != nil {
		return err
	}
	var updated *model.Issue
	if add {
		updated, err = c.AddTags(context.Background(), repo, key, rawTags, opts.dryRun)
	} else {
		updated, err = c.RemoveTags(context.Background(), repo, key, rawTags, opts.dryRun)
	}
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(updated)
	}
	return emit(updated)
}
