package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/inputio"
	"github.com/mrgeoffrich/bacio/internal/store"
)

func newPRCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "pr", Short: "Attach pull requests to an issue"}
	cmd.AddCommand(prAttachCmd(), prDetachCmd(), prListCmd())
	return cmd
}

func validatePRURL(raw string) (string, error) {
	return store.ValidatePRURLStrict(raw)
}

func prAttachCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "attach [KEY] [URL]",
		Short: "Attach a pull request URL to an issue",
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
				in, _, err := inputio.DecodeStrict[inputs.PRAttachInput](raw)
				if err != nil {
					return err
				}
				if in.IssueKey == "" || in.URL == "" {
					return fmt.Errorf("issue_key and url are required")
				}
				return attachPR(in.IssueKey, in.URL, true)
			}
			if len(args) != 2 {
				return fmt.Errorf("requires <KEY> <URL> positionals or --json")
			}
			return attachPR(args[0], args[1], false)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func attachPR(key, prURL string, strict bool) error {
	pr, err := validatePRURL(prURL)
	if err != nil {
		return err
	}
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
	created, err := c.AttachPR(context.Background(), repo, key, pr, opts.dryRun)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(created)
	}
	return emit(created)
}

func prDetachCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "detach [KEY] [URL]",
		Short: "Detach a pull request URL from an issue",
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
				in, _, err := inputio.DecodeStrict[inputs.PRDetachInput](raw)
				if err != nil {
					return err
				}
				if in.IssueKey == "" || in.URL == "" {
					return fmt.Errorf("issue_key and url are required")
				}
				return detachPR(in.IssueKey, in.URL, true)
			}
			if len(args) != 2 {
				return fmt.Errorf("requires <KEY> <URL> positionals or --json")
			}
			return detachPR(args[0], args[1], false)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func detachPR(key, prURL string, strict bool) error {
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
	preview, _, err := c.DetachPR(context.Background(), repo, key, prURL, opts.dryRun)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(&prDetachPreview{
			IssueKey:    preview.IssueKey,
			URL:         preview.URL,
			WouldRemove: preview.WouldRemove,
		})
	}
	canonical, _ := c.ResolveIssueKey(context.Background(), repo, key)
	return ok("detached %s from %s", prURL, canonical)
}

// prDetachPreview is the dry-run payload for `bacio pr detach`.
type prDetachPreview struct {
	IssueKey    string `json:"issue_key"`
	URL         string `json:"url"`
	WouldRemove int    `json:"would_remove"`
}

func prListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <KEY>",
		Short: "List pull requests attached to an issue",
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
			prs, err := c.ListPRs(context.Background(), repo, args[0])
			if err != nil {
				return err
			}
			return emit(prs)
		},
	}
}
