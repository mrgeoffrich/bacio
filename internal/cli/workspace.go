package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/agentmode"
	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/inputio"
	"github.com/mrgeoffrich/bacio/internal/model"
)

const workspaceLongHelp = `Manage workspaces — tracked spaces that are not git repos.

A workspace holds issues, documents, doc folders and a Kanban board
exactly as a git repo does, but has no checkout on disk. That is the
whole point: use one for work that isn't code (planning, ops, personal
projects) without inventing a git repo to hang it off.

Because a workspace has no working tree, bacio cannot resolve it from
the current directory. Every command that targets one needs the global
selector:

  bacio --repo HOME issue add "Fix the back fence"
  BACIO_REPO=HOME bacio issue list

Workspaces share the prefix namespace with git repos, so an issue key
like HOME-42 is unambiguous. Filesystem-shaped verbs (agent dispatch,
sync setup, doc export --to-path, doc add --from-path) refuse a
workspace with a message saying why.`

func newWorkspaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workspace",
		Short: "Manage workspaces (tracked spaces with no git working tree)",
		Long:  workspaceLongHelp,
	}
	cmd.AddCommand(workspaceAddCmd(), workspaceListCmd(), workspaceRmCmd())
	return cmd
}

func workspaceAddCmd() *cobra.Command {
	var prefix, rawInput string
	cmd := &cobra.Command{
		Use:   "add [NAME]",
		Short: "Create a workspace (a repo with no working tree)",
		Long: `Create a workspace.

The prefix is optional — omit it and bacio allocates a 4-character one
from the name, the same way a git repo registration does. Pass --prefix
to pin it.

Creating a workspace seeds the mandatory catch-all features and the
starter Kanban board (Backlog / Doing / Waiting / Done), so it is ready
for issues immediately.`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput, "prefix")
			if err != nil {
				return err
			}
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.WorkspaceAddInput](raw)
				if err != nil {
					return err
				}
				if strings.TrimSpace(in.Name) == "" {
					return fmt.Errorf("name is required")
				}
				return createWorkspace(in.Name, in.Prefix)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <NAME> positional or --json")
			}
			return createWorkspace(args[0], prefix)
		},
	}
	cmd.Flags().StringVar(&prefix, "prefix", "", "pin the 4-character prefix instead of allocating one from the name")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func createWorkspace(name, prefix string) error {
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	// Validation (name non-empty, prefix shape, prefix collision) lives
	// in the client/store boundary so the same rules apply to the HTTP
	// and desktop surfaces; the CLI does not re-implement them.
	repo, err := c.CreateWorkspace(context.Background(), client.WorkspaceCreateInput{
		Name:   name,
		Prefix: prefix,
	}, opts.dryRun)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(repo)
	}
	if opts.output == outputJSON {
		return emit(repo)
	}
	return ok("workspace %s (%s) created — drive it with `bacio --repo %s <command>`",
		repo.Prefix, repo.Name, repo.Prefix)
}

func workspaceListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List workspaces (use `bacio repo list` for every tracked repo)",
		Args:  cobra.NoArgs,
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
			// Filter client-side rather than adding a kind parameter to
			// ListRepos: that method is declared in two other interfaces
			// (dispatcher.Backend, the sync registry store) and any new
			// parameter would break *store.Store's satisfaction of them.
			out := []*model.Repo{}
			for _, r := range repos {
				if r.IsWorkspace() {
					out = append(out, r)
				}
			}
			return emit(out)
		},
	}
}

const workspaceRmLongHelp = `Delete a workspace and everything that belongs to it.

DESTRUCTIVE & IRREVERSIBLE. Identical cascade to ` + "`bacio repo rm`" + ` —
every issue, comment, feature, document, doc folder, Kanban lane, link,
relation, PR attachment, agent session, notification and history row
attached to the workspace goes with it. There is no undo.

Requires --confirm <prefix> (the value must equal the target prefix).
Without it, this command prints an impact preview and exits non-zero so
that an AI agent driving bacio MUST stop and ask the user before
re-running with --confirm. Always rehearse with --dry-run first.

Refuses a git repo: use ` + "`bacio repo rm`" + ` for those.`

func workspaceRmCmd() *cobra.Command {
	var (
		rawInput string
		confirm  string
	)
	cmd := &cobra.Command{
		Use:   "rm [PREFIX]",
		Short: "Delete a workspace (and everything in it) — requires --confirm <prefix>",
		Long:  workspaceRmLongHelp,
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := agentmode.DenyIfEnabled("workspace rm"); err != nil {
				return err
			}
			raw, err := parseJSONInput(cmd, args, rawInput, "confirm")
			if err != nil {
				return err
			}
			var prefix string
			switch {
			case raw != nil:
				in, _, err := inputio.DecodeStrict[inputs.WorkspaceRmInput](raw)
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
			return removeWorkspace(prefix, confirm)
		},
	}
	addInputFlag(cmd, &rawInput)
	cmd.Flags().StringVar(&confirm, "confirm", "",
		"required token; must equal the target workspace's prefix (case-insensitive) to actually delete")
	return cmd
}

// removeWorkspace is `bacio repo rm` narrowed to workspaces. It funnels
// through the exact same client call (and therefore the same
// backend-side confirm gate and the same audit row) after one extra
// lookup that refuses a git repo — deleting the wrong kind of thing is
// the one mistake this verb can prevent for free.
func removeWorkspace(prefix, confirm string) error {
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	repo, err := c.GetRepoByPrefix(context.Background(), strings.ToUpper(strings.TrimSpace(prefix)))
	if err != nil {
		return err
	}
	if !repo.IsWorkspace() {
		return fmt.Errorf("%s is a git repo, not a workspace — use `bacio repo rm %s` instead",
			repo.Prefix, repo.Prefix)
	}
	return deleteRepoRow(c, repo.Prefix, confirm)
}

// refuseFilesystemVerbOnWorkspace is the CLI-side guard for the verbs a
// workspace legitimately cannot do because it has no checkout on disk.
//
// It exists so those verbs never reuse the PHANTOM message ("link it
// first"), which would send the user hunting for a git repo that does
// not and will never exist. A workspace is pathless on purpose and
// permanently; a phantom is pathless until someone links it.
func refuseFilesystemVerbOnWorkspace(repo *model.Repo, what string) error {
	if repo == nil || !repo.IsWorkspace() {
		return nil
	}
	return fmt.Errorf("%s is a workspace, not a git repo — %s", repo.Prefix, what)
}

// deleteRepoRow is the shared tail of `bacio repo rm` and
// `bacio workspace rm`: one DeleteRepo call, one confirm-gate renderer,
// one dry-run projection. Both verbs must behave identically here — the
// only difference between them is which kinds of row they accept.
func deleteRepoRow(c client.Client, prefix, confirm string) error {
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
	kind := "repo"
	if deleted.IsWorkspace() {
		kind = "workspace"
	}
	return ok("%s %s deleted (%s)", kind, deleted.Prefix, deleted.Name)
}
