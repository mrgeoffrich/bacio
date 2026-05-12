package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/inputio"
)

func newRepoCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "repo", Short: "Inspect tracked repos"}
	cmd.AddCommand(repoListCmd(), repoShowCmd(), repoRmCmd())
	return cmd
}

func repoListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all tracked repos",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()
			repos, err := c.ListRepos(context.Background())
			if err != nil {
				return err
			}
			return emit(repos)
		},
	}
}

func repoShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show [PREFIX]",
		Short: "Show details for a repo (defaults to current directory)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()
			if len(args) == 1 {
				repo, err := c.GetRepoByPrefix(context.Background(), strings.ToUpper(args[0]))
				if err != nil {
					return err
				}
				return emit(repo)
			}
			repo, err := resolveRepoC(c)
			if err != nil {
				return err
			}
			return emit(repo)
		},
	}
}

const repoRmLongHelp = `Delete a repo and everything that belongs to it.

DESTRUCTIVE & IRREVERSIBLE. Cascades through every issue, comment,
feature, document, link, relation, PR attachment, TUI setting and
history row attached to the repo. There is no undo.

Requires --confirm <prefix> (the value must equal the target prefix).
Without it, this command prints an impact preview and exits non-zero so
that an AI agent driving bacio MUST stop and ask the user before re-running
with --confirm. Always rehearse with --dry-run first to inspect the
cascade.`

func repoRmCmd() *cobra.Command {
	var (
		rawInput string
		confirm  string
	)
	cmd := &cobra.Command{
		Use:   "rm [PREFIX]",
		Short: "Delete a repo (and all its issues/features/docs/history) — requires --confirm <prefix>",
		Long:  repoRmLongHelp,
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput, "confirm")
			if err != nil {
				return err
			}
			var prefix string
			switch {
			case raw != nil:
				in, _, err := inputio.DecodeStrict[inputs.RepoRmInput](raw)
				if err != nil {
					return err
				}
				if strings.TrimSpace(in.Prefix) == "" {
					return fmt.Errorf("prefix is required")
				}
				prefix = in.Prefix
				confirm = in.Confirm
			case len(args) == 1:
				prefix = args[0]
			default:
				return fmt.Errorf("requires <PREFIX> positional or --json")
			}
			return removeRepo(prefix, confirm)
		},
	}
	addInputFlag(cmd, &rawInput)
	cmd.Flags().StringVar(&confirm, "confirm", "",
		"required token; must equal the target repo's prefix (case-insensitive) to actually delete")
	return cmd
}

func removeRepo(prefix, confirm string) error {
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	deleted, preview, err := c.DeleteRepo(context.Background(), prefix, confirm, opts.dryRun)
	if err != nil {
		// Confirm gate trip: format the alert and return a non-nil
		// error so the process exits non-zero, but channel the
		// preview through emit() (or our text alert) first so the
		// caller sees the impact.
		var confErr *client.RepoConfirmError
		if errors.As(err, &confErr) {
			return formatRepoConfirmError(confErr)
		}
		return err
	}
	if opts.dryRun {
		return emitDryRun(toRepoDeletePreview(preview))
	}
	return ok("repo %s deleted (%s)", deleted.Prefix, deleted.Name)
}

// formatRepoConfirmError renders the LLM-targeted alert. In JSON mode
// we emit the structured preview-plus-error envelope on stdout and
// return a terse error to drive the non-zero exit. In text mode we
// print the loud "STOP" block and return the same terse error.
func formatRepoConfirmError(e *client.RepoConfirmError) error {
	if e.Preview == nil {
		return fmt.Errorf("%s", e.Error())
	}
	if opts.output == outputJSON {
		envelope := struct {
			Error            string             `json:"error"`
			Message          string             `json:"message"`
			Repo             any                `json:"repo"`
			Cascade          any                `json:"cascade"`
			Irreversible     bool               `json:"irreversible"`
			ConfirmationHint string             `json:"confirmation_hint"`
		}{
			Error:            "confirm_required",
			Message:          e.Error(),
			Repo:             e.Preview.Repo,
			Cascade:          e.Preview.Cascade,
			Irreversible:     true,
			ConfirmationHint: "re-run with --confirm " + e.Prefix,
		}
		_ = emit(envelope)
		return fmt.Errorf("aborted: %s", e.Error())
	}
	repo := e.Preview.Repo
	c := e.Preview.Cascade
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "⚠️  STOP — DESTRUCTIVE OPERATION REQUIRES HUMAN APPROVAL")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "`bacio repo rm %s` will permanently delete repo %s (%s) and:\n", repo.Prefix, repo.Prefix, repo.Name)
	fmt.Fprintf(os.Stderr, "  • %d issues\n", c.Issues)
	fmt.Fprintf(os.Stderr, "  • %d comments\n", c.Comments)
	fmt.Fprintf(os.Stderr, "  • %d issue relations\n", c.Relations)
	fmt.Fprintf(os.Stderr, "  • %d PR attachments\n", c.PullRequests)
	fmt.Fprintf(os.Stderr, "  • %d tags\n", c.Tags)
	fmt.Fprintf(os.Stderr, "  • %d features\n", c.Features)
	fmt.Fprintf(os.Stderr, "  • %d documents\n", c.Documents)
	fmt.Fprintf(os.Stderr, "  • %d document links\n", c.DocumentLinks)
	fmt.Fprintf(os.Stderr, "  • %d TUI settings\n", c.TUISettings)
	fmt.Fprintf(os.Stderr, "  • %d history rows\n", c.History)
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "This is IRREVERSIBLE. There is no undo, no trash, no recovery.")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "If you are an AI agent: do NOT proceed without explicit human")
	fmt.Fprintln(os.Stderr, "approval. Show this preview to the user, get a clear")
	fmt.Fprintf(os.Stderr, "\"yes, delete %s\" in their own words, then re-run with:\n", repo.Prefix)
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "  bacio repo rm %s --confirm %s\n", repo.Prefix, repo.Prefix)
	fmt.Fprintln(os.Stderr)
	return fmt.Errorf("aborted: %s", e.Error())
}

// repoDeletePreview is the text/JSON shape emitted for `--dry-run`.
// Lives in the cli package so the JSON tags match what `bacio` has always
// printed for *.rm previews (lowercase, snake_case).
type repoDeletePreview struct {
	Repo        any  `json:"repo"`
	Cascade     any  `json:"cascade"`
	WouldDelete bool `json:"would_delete"`
}

func toRepoDeletePreview(p *client.RepoDeletePreview) *repoDeletePreview {
	if p == nil {
		return nil
	}
	return &repoDeletePreview{
		Repo:        p.Repo,
		Cascade:     p.Cascade,
		WouldDelete: p.WouldDelete,
	}
}
