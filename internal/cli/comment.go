package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/inputio"
)

func newCommentCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "comment", Short: "Manage issue comments"}
	cmd.AddCommand(commentAddCmd(), commentListCmd())
	return cmd
}

func commentAddCmd() *cobra.Command {
	var (
		author, body, bodyFile, rawInput string
	)
	cmd := &cobra.Command{
		Use:   "add [KEY]",
		Short: "Add a comment to an issue",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := readJSONInput(rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				if err := rejectMixedInput(cmd, args, "as", "body", "body-file"); err != nil {
					return err
				}
				in, _, err := inputio.DecodeStrict[inputs.CommentAddInput](raw)
				if err != nil {
					return err
				}
				if in.IssueKey == "" || in.Author == "" || in.Body == "" {
					return fmt.Errorf("issue_key, author, and body are required")
				}
				return addComment(*in, true)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <KEY> positional or --json")
			}
			text, err := readLongText(body, bodyFile, true, "body")
			if err != nil {
				return err
			}
			if author == "" {
				return fmt.Errorf("--as is required")
			}
			return addComment(inputs.CommentAddInput{
				IssueKey: args[0],
				Author:   author,
				Body:     text,
			}, false)
		},
	}
	cmd.Flags().StringVar(&author, "as", "", "comment author name (required when not using --json)")
	cmd.Flags().StringVar(&body, "body", "", "comment body or '-' for stdin")
	cmd.Flags().StringVar(&bodyFile, "body-file", "", "path to a markdown file")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func addComment(in inputs.CommentAddInput, strict bool) error {
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	if strict {
		if !isIssueKey(in.IssueKey) {
			return fmt.Errorf("issue_key %q must be canonical (e.g. \"MINI-42\")", in.IssueKey)
		}
	}
	repo, err := repoForIssueKey(c, in.IssueKey)
	if err != nil {
		return err
	}
	cm, err := c.AddComment(context.Background(), repo, in, opts.dryRun)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(cm)
	}
	return emit(cm)
}

func commentListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <KEY>",
		Short: "List comments on an issue",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()
			repo, err := repoForIssueKey(c, args[0])
			if err != nil {
				return err
			}
			cs, err := c.ListComments(context.Background(), repo, args[0])
			if err != nil {
				return err
			}
			return emit(cs)
		},
	}
}
