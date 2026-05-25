package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/agentmode"
	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/inputio"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/sync"
)

func newRepoCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "repo", Short: "Inspect tracked repos"}
	cmd.AddCommand(repoListCmd(), repoShowCmd(), repoRmCmd(), repoLinkCmd())
	return cmd
}

func repoListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all tracked repos",
		RunE: func(cmd *cobra.Command, args []string) error {
			if root, ok := resolveSyncRepoRoot(); ok {
				return listReposFromSyncRepo(root)
			}
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

// listReposFromSyncRepo reads index.yaml at the sync repo root and
// renders the entries through the same `[]*model.Repo` path the
// project-repo branch uses. We convert RepoIndexEntry → model.Repo
// (lossy: no ID, no Path, no NextIssueNumber) so existing JSON/text
// renderers keep working without a new shape. Falls back to a scan
// of `repos/*/repo.yaml` for older sync repos that pre-date index.yaml.
func listReposFromSyncRepo(syncRoot string) error {
	idx, err := sync.ReadIndex(syncRoot)
	if err != nil && !errors.Is(err, sync.ErrNoIndex) {
		return err
	}
	repos := []*model.Repo{}
	if idx != nil {
		for _, e := range idx.Repos {
			repos = append(repos, &model.Repo{
				UUID:      e.UUID,
				Prefix:    e.Prefix,
				Name:      e.Name,
				RemoteURL: e.Remote,
			})
		}
		return emit(repos)
	}
	// Fallback: scan repos/*/repo.yaml when no index.yaml is present
	// (sync repos created before this file existed).
	entries, err := os.ReadDir(filepath.Join(syncRoot, "repos"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(syncRoot, "repos", entry.Name(), "repo.yaml"))
		if err != nil {
			continue
		}
		parsed, err := sync.ParseRepoYAML(b)
		if err != nil {
			continue
		}
		repos = append(repos, &model.Repo{
			UUID:      parsed.UUID,
			Prefix:    parsed.Prefix,
			Name:      parsed.Name,
			RemoteURL: parsed.RemoteURL,
		})
	}
	return emit(repos)
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
			if err := agentmode.DenyIfEnabled("repo rm"); err != nil {
				return err
			}
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

// repoLinkLongHelp explains the BACI-112 surface in detail — read by
// the user via `bacio repo link --help` and by the schema-driven
// agent path via `bacio schema show repo.link`.
const repoLinkLongHelp = `Link a phantom repo (synced-but-pathless) to a local git working tree.

A phantom repo is a row inserted by 'bacio sync clone' for a sync repo
that carries metadata for a project the local DB doesn't yet have a
checkout for. This command binds the phantom to a path on disk:
  - the supplied path must be absolute and point at an existing git
    working tree;
  - no other repo may already be bound to that path;
  - some sync repo on this machine must carry repos/<PREFIX>/ — i.e.
    'bacio sync clone' (or 'bacio sync setup' attach) has already
    fetched the owning sync repo locally.

After linking, .bacio/config.yaml is written at the working tree with
the owning sync remote URL so subsequent 'bacio' invocations from
inside the tree pick up the registered sync remote automatically.

Idempotent: re-linking to the same path is a no-op (no audit row, no
config rewrite). Use --dry-run to rehearse without writing.`

func repoLinkCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "link [PREFIX] [PATH]",
		Short: "Bind a phantom repo (synced-but-pathless) to a local git working tree",
		Long:  repoLinkLongHelp,
		Args:  cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput)
			if err != nil {
				return err
			}
			var (
				prefix string
				path   string
			)
			switch {
			case raw != nil:
				in, _, err := inputio.DecodeStrict[inputs.RepoLinkInput](raw)
				if err != nil {
					return err
				}
				prefix = in.Prefix
				path = in.Path
			case len(args) == 2:
				prefix = args[0]
				path = args[1]
			default:
				return fmt.Errorf("requires <PREFIX> <PATH> positional or --json")
			}
			if strings.TrimSpace(prefix) == "" {
				return fmt.Errorf("prefix is required")
			}
			if strings.TrimSpace(path) == "" {
				return fmt.Errorf("path is required")
			}
			// Expand a relative path (most often `.`) to absolute BEFORE
			// openClient() — otherwise the implicit cwd-upgrade in
			// resolveRepoC could fire first and beat the explicit
			// link to the row. Doing this client-side keeps the
			// LinkPhantomRepo backend code path-agnostic.
			abs, err := filepath.Abs(path)
			if err != nil {
				return fmt.Errorf("resolve path: %w", err)
			}
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()
			result, err := c.LinkPhantomRepo(context.Background(), prefix, abs, opts.dryRun)
			if err != nil {
				var linkErr *client.RepoLinkError
				if errors.As(err, &linkErr) {
					return formatRepoLinkError(linkErr)
				}
				return err
			}
			if opts.dryRun {
				return emitDryRun(result)
			}
			if result.AlreadyLinked {
				return ok("repo %s already linked to %s — nothing to do", result.Repo.Prefix, result.Repo.Path)
			}
			return ok("repo %s linked to %s (sync remote: %s)",
				result.Repo.Prefix, result.Repo.Path, result.SyncRemoteURL)
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

// formatRepoLinkError surfaces a typed link error with a JSON envelope
// in --output json mode (so an agent driver can recognise the kind
// without parsing the message) and a plain message otherwise.
func formatRepoLinkError(e *client.RepoLinkError) error {
	if opts.output == outputJSON {
		envelope := struct {
			Error          string `json:"error"`
			Kind           string `json:"kind"`
			Message        string `json:"message"`
			Prefix         string `json:"prefix,omitempty"`
			Path           string `json:"path,omitempty"`
			ExistingPrefix string `json:"existing_prefix,omitempty"`
		}{
			Error:          string(e.Kind),
			Kind:           string(e.Kind),
			Message:        e.Error(),
			Prefix:         e.Prefix,
			Path:           e.Path,
			ExistingPrefix: e.ExistingPrefix,
		}
		_ = emit(envelope)
	}
	return e
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
