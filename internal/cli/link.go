package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/inputio"
)

func newLinkCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "link [FROM] [type] [TO]",
		Short: "Create a relation between two issues",
		Long:  "Types: blocks, relates-to, duplicate-of",
		Args:  cobra.RangeArgs(0, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := readJSONInput(rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				if err := rejectMixedInput(cmd, args); err != nil {
					return err
				}
				in, _, err := inputio.DecodeStrict[inputs.LinkInput](raw)
				if err != nil {
					return err
				}
				if in.From == "" || in.Type == "" || in.To == "" {
					return fmt.Errorf("from, type, and to are required")
				}
				return createRelation(*in, true)
			}
			if len(args) != 3 {
				return fmt.Errorf("requires <FROM> <type> <TO> positionals or --json")
			}
			return createRelation(inputs.LinkInput{
				From: args[0],
				Type: args[1],
				To:   args[2],
			}, false)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func createRelation(in inputs.LinkInput, strict bool) error {
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	if strict {
		if !isIssueKey(in.From) {
			return fmt.Errorf("from %q must be canonical (e.g. \"MINI-42\")", in.From)
		}
		if !isIssueKey(in.To) {
			return fmt.Errorf("to %q must be canonical (e.g. \"MINI-42\")", in.To)
		}
	}
	repo, err := repoForIssueKey(c, in.From)
	if err != nil {
		return err
	}
	rel, err := c.LinkRelation(context.Background(), repo, in, opts.dryRun)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(rel)
	}
	return ok("%s %s %s", rel.FromIssue, rel.Type, rel.ToIssue)
}

func newUnlinkCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "unlink [A] [B]",
		Short: "Remove all relations between two issues",
		Args:  cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := readJSONInput(rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				if err := rejectMixedInput(cmd, args); err != nil {
					return err
				}
				in, _, err := inputio.DecodeStrict[inputs.UnlinkInput](raw)
				if err != nil {
					return err
				}
				if in.A == "" || in.B == "" {
					return fmt.Errorf("a and b are required")
				}
				return removeRelation(*in, true)
			}
			if len(args) != 2 {
				return fmt.Errorf("requires <A> <B> positionals or --json")
			}
			return removeRelation(inputs.UnlinkInput{A: args[0], B: args[1]}, false)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func removeRelation(in inputs.UnlinkInput, strict bool) error {
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	if strict {
		if !isIssueKey(in.A) {
			return fmt.Errorf("a %q must be canonical (e.g. \"MINI-42\")", in.A)
		}
		if !isIssueKey(in.B) {
			return fmt.Errorf("b %q must be canonical (e.g. \"MINI-42\")", in.B)
		}
	}
	repo, err := repoForIssueKey(c, in.A)
	if err != nil {
		return err
	}
	preview, n, err := c.UnlinkRelation(context.Background(), repo, in, opts.dryRun)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(&relationDeletePreview{
			A:           preview.A,
			B:           preview.B,
			WouldRemove: preview.WouldRemove,
		})
	}
	a, _ := c.ResolveIssueKey(context.Background(), repo, in.A)
	b, _ := c.ResolveIssueKey(context.Background(), repo, in.B)
	return ok("removed %d relation(s) between %s and %s", n, a, b)
}

// relationDeletePreview is the dry-run payload for `bacio unlink`.
type relationDeletePreview struct {
	A           string `json:"a"`
	B           string `json:"b"`
	WouldRemove int    `json:"would_remove"`
}
