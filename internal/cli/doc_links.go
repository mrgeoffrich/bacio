package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/inputio"
)

func docLinkCmd() *cobra.Command {
	var why, rawInput string
	cmd := &cobra.Command{
		Use:   "link [filename] [ISSUE-KEY|feature-slug]",
		Short: "Link a document to an issue or feature (upsert; --why replaces description)",
		Args:  cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := readJSONInput(rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				if err := rejectMixedInput(cmd, args, "why"); err != nil {
					return err
				}
				in, _, err := inputio.DecodeStrict[inputs.DocLinkInput](raw)
				if err != nil {
					return err
				}
				if in.Filename == "" {
					return fmt.Errorf("filename is required")
				}
				return linkDocumentJSON(*in)
			}
			if len(args) != 2 {
				return fmt.Errorf("requires <filename> <ISSUE-KEY|feature-slug> positionals or --json")
			}
			return linkDocumentArgs(args[0], args[1], why)
		},
	}
	cmd.Flags().StringVar(&why, "why", "", "description of why this document is linked (optional)")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func linkDocumentJSON(in inputs.DocLinkInput) error {
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	repo, err := resolveRepoC(c)
	if err != nil {
		return err
	}
	link, err := c.LinkDocument(context.Background(), repo, in, opts.dryRun)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(link)
	}
	return emit(link)
}

func linkDocumentArgs(filename, ref, why string) error {
	in := inputs.DocLinkInput{Filename: filename, Description: strings.TrimSpace(why)}
	if isIssueKey(strings.TrimSpace(ref)) {
		in.IssueKey = strings.TrimSpace(ref)
	} else {
		in.FeatureSlug = strings.TrimSpace(ref)
	}
	return linkDocumentJSON(in)
}

func docUnlinkCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "unlink [filename] [ISSUE-KEY|feature-slug]",
		Short: "Remove a link between a document and an issue or feature",
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
				in, _, err := inputio.DecodeStrict[inputs.DocUnlinkInput](raw)
				if err != nil {
					return err
				}
				if in.Filename == "" {
					return fmt.Errorf("filename is required")
				}
				return unlinkDocumentJSON(*in)
			}
			if len(args) != 2 {
				return fmt.Errorf("requires <filename> <ISSUE-KEY|feature-slug> positionals or --json")
			}
			in := inputs.DocUnlinkInput{Filename: args[0]}
			if isIssueKey(strings.TrimSpace(args[1])) {
				in.IssueKey = strings.TrimSpace(args[1])
			} else {
				in.FeatureSlug = strings.TrimSpace(args[1])
			}
			return unlinkDocumentJSON(in)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func unlinkDocumentJSON(in inputs.DocUnlinkInput) error {
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	repo, err := resolveRepoC(c)
	if err != nil {
		return err
	}
	preview, _, err := c.UnlinkDocument(context.Background(), repo, in, opts.dryRun)
	if err != nil {
		return err
	}
	target := in.IssueKey
	if target == "" {
		target = "feature/" + in.FeatureSlug
	}
	if opts.dryRun {
		return emitDryRun(&docUnlinkPreview{
			Filename:    preview.Filename,
			Target:      preview.Target,
			WouldRemove: preview.WouldRemove,
		})
	}
	return ok("unlinked %s from %s", in.Filename, target)
}

// docUnlinkPreview is the dry-run payload for `bacio doc unlink`.
type docUnlinkPreview struct {
	Filename    string `json:"filename"`
	Target      string `json:"target"`
	WouldRemove int    `json:"would_remove"`
}
